package privacy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/identity"
)

// Service coordinates data export, account erasure with cryptographic shredding,
// and retention cleanup.
type Service struct {
	identity IdentityService
	profile  ProfileService
	items    ItemService
	docs     DocumentService
	apps     ApplicationService
	matching MatchingService
	keys     KeyManager
	blobs    BlobStore
	now      func() time.Time
}

// NewService returns a new privacy service.
func NewService(
	identity IdentityService,
	profile ProfileService,
	items ItemService,
	docs DocumentService,
	apps ApplicationService,
	matching MatchingService,
	keys KeyManager,
	blobs BlobStore,
) *Service {
	return &Service{
		identity: identity,
		profile:  profile,
		items:    items,
		docs:     docs,
		apps:     apps,
		matching: matching,
		keys:     keys,
		blobs:    blobs,
		now:      time.Now,
	}
}

// WithClock sets the clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// Export bundles all personal data, history, and uploaded files into a portable ZIP archive.
func (s *Service) Export(ctx context.Context, userID uuid.UUID, w io.Writer) error {
	data := ExportData{
		ExportedAt: s.now(),
	}

	// 1. Identity
	u, err := s.identity.UserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("export identity: %w", err)
	}
	data.User = UserSummary{
		ID:          u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Role:        string(u.Role),
		Locale:      string(u.Locale),
	}
	passkeys, err := s.identity.Passkeys(ctx, userID)
	if err == nil {
		for _, pk := range passkeys {
			data.Passkeys = append(data.Passkeys, PasskeySummary{
				Name:       pk.Name,
				CreatedAt:  pk.CreatedAt,
				LastUsedAt: pk.LastUsed,
			})
		}
	}
	sessions, err := s.identity.Sessions(ctx, userID, "")
	if err == nil {
		for _, sess := range sessions {
			data.Sessions = append(data.Sessions, SessionSummary{
				Method:     string(sess.Method),
				CreatedAt:  sess.CreatedAt,
				LastSeenAt: sess.LastSeen,
			})
		}
	}

	// 2. Profile & Preferences
	if s.profile != nil {
		snap, err := s.profile.Get(ctx, userID)
		if err == nil {
			data.Profile = snap.Profile
			data.Preferences = snap.Preferences
		}
	}

	// 3. Answers & Texts
	if s.items != nil {
		answers, err := s.items.Answers(ctx, userID)
		if err == nil {
			for _, a := range answers {
				data.Answers = append(data.Answers, AnswerSummary{
					Question: a.Question,
					Answer:   a.Answer,
				})
			}
		}
		texts, err := s.items.TextBlocks(ctx, userID)
		if err == nil {
			for _, t := range texts {
				data.Texts = append(data.Texts, TextSummary{
					Name:     t.Name,
					Language: t.Language,
					Body:     t.Body,
				})
			}
		}
	}

	// 4. Documents
	files := make(map[string][]byte)
	if s.docs != nil {
		docs, err := s.docs.List(ctx, userID)
		if err == nil {
			for _, d := range docs {
				data.Documents = append(data.Documents, DocumentSummary{
					ID:        d.ID,
					Kind:      d.Kind,
					Name:      d.Name,
					Mime:      d.Mime,
					Size:      d.Size,
					CreatedAt: d.CreatedAt,
				})
				_, content, err := s.docs.Read(ctx, userID, d.ID)
				if err == nil {
					name := d.Name
					if _, exists := files[name]; exists {
						name = fmt.Sprintf("%s_%s", d.ID.String()[:8], d.Name)
					}
					files[name] = content
				}
			}
		}
	}

	// 5. Matches
	if s.matching != nil {
		matches, err := s.matching.UserMatches(ctx, userID)
		if err == nil {
			for _, m := range matches {
				data.Matches = append(data.Matches, MatchSummary{
					JobID:      m.JobID,
					Title:      m.Title,
					Company:    m.Company,
					Location:   m.Location,
					Workplace:  m.Workplace,
					Score:      m.Score,
					Decision:   string(m.Decision),
					Reasons:    m.Reasons,
					ComputedAt: m.ComputedAt,
				})
			}
		}
	}

	// 6. Applications
	if s.apps != nil {
		apps, err := s.apps.UserApplications(ctx, userID)
		if err == nil {
			for _, a := range apps {
				var evs []EventSummary
				for _, e := range a.Events {
					evs = append(evs, EventSummary{
						Event: string(e.Event),
						From:  string(e.From),
						To:    string(e.To),
						Actor: string(e.Actor),
						At:    e.At,
						Data:  e.Detail,
					})
				}
				data.Applications = append(data.Applications, ApplicationSummary{
					ID:        a.ID,
					JobID:     a.JobID,
					State:     string(a.State),
					Company:   a.Company,
					Title:     a.Title,
					Location:  a.Location,
					URL:       a.URL,
					CreatedAt: a.CreatedAt,
					UpdatedAt: a.UpdatedAt,
					Events:    evs,
				})
			}
		}

		st, err := s.apps.Settings(ctx, userID)
		if err == nil {
			data.Settings.AutonomyLevel = string(st.Level)
			data.Settings.DailyLimit = st.DailyLimit
			data.Settings.RunnerChoice = string(st.RunnerChoice)
			data.Settings.EasyApplyEnabled = st.EasyApplyEnabled
		}
	}

	// 7. Audit Events
	events, err := s.identity.AuditEvents(ctx, userID)
	if err == nil {
		for _, e := range events {
			data.AuditEvents = append(data.AuditEvents, AuditSummary{
				Actor:     e.Actor,
				Code:      e.Code,
				Params:    e.Params,
				CreatedAt: s.now(), // or event timestamp
			})
		}
	}

	return WriteArchive(w, data, files)
}

// DeleteAccount permanently deletes the user's data from PostgreSQL, deletes all
// documents and screenshots from the blob store, and cryptographically shreds
// the per-user data key.
func (s *Service) DeleteAccount(ctx context.Context, userID uuid.UUID) error {
	var errs []error

	// 1. Delete documents from blob store
	if s.docs != nil && s.blobs != nil {
		docs, err := s.docs.List(ctx, userID)
		if err == nil {
			for _, d := range docs {
				if err := s.blobs.Delete(ctx, userID, "documents/"+d.ID.String()); err != nil {
					errs = append(errs, fmt.Errorf("delete doc blob %s: %w", d.ID, err))
				}
			}
		}
	}

	// 2. Delete run screenshots from blob store
	if s.apps != nil && s.blobs != nil {
		shots, err := s.apps.UserScreenshots(ctx, userID)
		if err == nil {
			for _, key := range shots {
				if err := s.blobs.Delete(ctx, userID, key); err != nil {
					errs = append(errs, fmt.Errorf("delete screenshot %s: %w", key, err))
				}
			}
		}
	}

	// 3. Crypto-shredding: zero and delete user data key from user_keys table
	if s.keys != nil {
		if _, err := s.keys.Forget(ctx, userID); err != nil {
			errs = append(errs, fmt.Errorf("crypto-shred key: %w", err))
		}
	}

	// 4. Delete user record in PostgreSQL (cascades to all tenant tables)
	if err := s.identity.DeleteUser(ctx, userID); err != nil {
		if !errors.Is(err, identity.ErrNotFound) {
			errs = append(errs, fmt.Errorf("delete user row: %w", err))
		}
	}

	return errors.Join(errs...)
}

// Cleanup performs periodic data retention tasks: expired auth tokens/challenges,
// unaccepted invitations, and run screenshots past 90 days retention.
func (s *Service) Cleanup(ctx context.Context) error {
	var errs []error
	if s.identity != nil {
		if err := s.identity.Cleanup(ctx); err != nil {
			errs = append(errs, fmt.Errorf("identity cleanup: %w", err))
		}
	}
	if s.apps != nil && s.blobs != nil {
		if _, err := s.apps.PurgeExpiredScreenshots(ctx, s.blobs); err != nil {
			errs = append(errs, fmt.Errorf("screenshots cleanup: %w", err))
		}
	}
	return errors.Join(errs...)
}
