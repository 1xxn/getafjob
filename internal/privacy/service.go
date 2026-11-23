package privacy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/tracking"
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
	stations StationService // nil: no work history in the export
	extras   Extras
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

// WithExtras adds notes, saved searches, the blocklist, notification and inbox settings to the export.
func (s *Service) WithExtras(e Extras) *Service {
	s.extras = e
	return s
}

// WithStations adds the work history and education to the export.
func (s *Service) WithStations(st StationService) *Service {
	s.stations = st
	return s
}

// Export writes a zip with everything stored about the user: account, profile, work history, answers, texts,
// documents, matches, applications, settings and the audit log. It reads everything before it writes; when a part
// cannot be read it fails instead of handing out an incomplete export.
func (s *Service) Export(ctx context.Context, userID uuid.UUID, w io.Writer) error {
	data, files, err := s.collect(ctx, userID)
	if err != nil {
		return err
	}
	return WriteArchive(w, data, files)
}

// collect gathers the export; the first failed read ends it.
func (s *Service) collect(ctx context.Context, userID uuid.UUID) (ExportData, map[string][]byte, error) {
	data := ExportData{ExportedAt: s.now()}
	fail := func(part string, err error) (ExportData, map[string][]byte, error) {
		return ExportData{}, nil, fmt.Errorf("export %s: %w", part, err)
	}

	u, err := s.identity.UserByID(ctx, userID)
	if err != nil {
		return fail("account", err)
	}
	data.User = UserSummary{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, Role: string(u.Role), Locale: string(u.Locale)}
	passkeys, err := s.identity.Passkeys(ctx, userID)
	if err != nil {
		return fail("passkeys", err)
	}
	for _, pk := range passkeys {
		data.Passkeys = append(data.Passkeys, PasskeySummary{Name: pk.Name, CreatedAt: pk.CreatedAt, LastUsedAt: pk.LastUsed})
	}
	sessions, err := s.identity.Sessions(ctx, userID, "")
	if err != nil {
		return fail("sessions", err)
	}
	for _, sess := range sessions {
		data.Sessions = append(data.Sessions, SessionSummary{Method: string(sess.Method), CreatedAt: sess.CreatedAt, LastSeenAt: sess.LastSeen})
	}

	if s.profile != nil {
		snap, err := s.profile.Get(ctx, userID)
		if err != nil {
			return fail("profile", err)
		}
		data.Profile, data.Preferences = snap.Profile, snap.Preferences
	}
	if s.stations != nil {
		works, err := s.stations.WorkExperiences(ctx, userID)
		if err != nil {
			return fail("work history", err)
		}
		edus, err := s.stations.Educations(ctx, userID)
		if err != nil {
			return fail("education", err)
		}
		data.WorkExperiences, data.Educations = works, edus
	}

	if s.items != nil {
		answers, err := s.items.Answers(ctx, userID)
		if err != nil {
			return fail("answers", err)
		}
		for _, a := range answers {
			data.Answers = append(data.Answers, AnswerSummary{Question: a.Question, Answer: a.Answer})
		}
		texts, err := s.items.TextBlocks(ctx, userID)
		if err != nil {
			return fail("texts", err)
		}
		for _, t := range texts {
			data.Texts = append(data.Texts, TextSummary{Name: t.Name, Language: t.Language, Body: t.Body})
		}
	}

	files := make(map[string][]byte)
	if s.docs != nil {
		docs, err := s.docs.List(ctx, userID)
		if err != nil {
			return fail("documents", err)
		}
		for _, d := range docs {
			data.Documents = append(data.Documents, DocumentSummary{ID: d.ID, Kind: d.Kind, Name: d.Name, Mime: d.Mime,
				Size: d.Size, CreatedAt: d.CreatedAt})
			_, content, err := s.docs.Read(ctx, userID, d.ID)
			if err != nil {
				return fail("document "+d.ID.String(), err)
			}
			name := d.Name
			if _, exists := files[name]; exists {
				name = fmt.Sprintf("%s_%s", d.ID.String()[:8], d.Name)
			}
			files[name] = content
		}
	}

	if s.matching != nil {
		matches, err := s.matching.UserMatches(ctx, userID)
		if err != nil {
			return fail("matches", err)
		}
		for _, m := range matches {
			data.Matches = append(data.Matches, MatchSummary{JobID: m.JobID, Title: m.Title, Company: m.Company,
				Location: m.Location, Workplace: m.Workplace, Score: m.Score, Decision: string(m.Decision),
				Reasons: m.Reasons, ComputedAt: m.ComputedAt})
		}
	}

	if s.apps != nil {
		apps, err := s.apps.UserApplications(ctx, userID)
		if err != nil {
			return fail("applications", err)
		}
		for _, a := range apps {
			var evs []EventSummary
			for _, e := range a.Events {
				evs = append(evs, EventSummary{Event: string(e.Event), From: string(e.From), To: string(e.To),
					Actor: string(e.Actor), At: e.At, Data: e.Detail})
			}
			sum := ApplicationSummary{ID: a.ID, JobID: a.JobID,
				State: string(a.State), Company: a.Company, Title: a.Title, Location: a.Location, URL: a.URL,
				CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, Events: evs}
			if s.extras.Notes != nil {
				n, err := s.extras.Notes.GetNotes(ctx, userID, a.ID)
				switch {
				case err == nil:
					sum.Notes = &NoteSummary{Notes: n.Notes, SalaryDiscussed: n.SalaryDiscussed, InterviewAt: n.InterviewAt,
						FollowUpAt: n.FollowUpAt, UpdatedAt: n.UpdatedAt}
				case !errors.Is(err, application.ErrNotFound):
					return fail("notes", err)
				}
			}
			data.Applications = append(data.Applications, sum)
		}
		st, err := s.apps.Settings(ctx, userID)
		if err != nil {
			return fail("settings", err)
		}
		data.Settings.AutonomyLevel = string(st.Level)
		data.Settings.DailyLimit = st.DailyLimit
		data.Settings.RunnerChoice = string(st.RunnerChoice)
		data.Settings.EasyApplyEnabled = st.EasyApplyEnabled
	}

	if err := s.collectExtras(ctx, userID, &data); err != nil {
		return ExportData{}, nil, err
	}

	events, err := s.identity.AuditEvents(ctx, userID)
	if err != nil {
		return fail("audit log", err)
	}
	for _, e := range events {
		data.AuditEvents = append(data.AuditEvents, AuditSummary{Actor: e.Actor, Code: e.Code, Params: e.Params, CreatedAt: e.At})
	}
	return data, files, nil
}

// collectExtras adds saved searches, the blocklist, notification and inbox settings. Secrets (the ntfy token, the
// mailbox password) stay out.
func (s *Service) collectExtras(ctx context.Context, userID uuid.UUID, data *ExportData) error {
	e := s.extras
	if e.Searches != nil {
		list, err := e.Searches.List(ctx, userID)
		if err != nil {
			return fmt.Errorf("export saved searches: %w", err)
		}
		for _, x := range list {
			data.SavedSearches = append(data.SavedSearches, SearchSummary{Name: x.Name, Query: x.Query, Alert: x.Alert})
		}
	}
	if e.Blocklist != nil {
		list, err := e.Blocklist.Blocklist(ctx, userID)
		if err != nil {
			return fmt.Errorf("export blocklist: %w", err)
		}
		for _, x := range list {
			data.Blocklist = append(data.Blocklist, BlocklistSummary{Kind: x.Kind, Pattern: x.Pattern, CreatedAt: x.CreatedAt})
		}
	}
	if e.Notify != nil {
		p, err := e.Notify.GetPreferences(ctx, userID)
		if err != nil {
			return fmt.Errorf("export notification settings: %w", err)
		}
		p.NtfyToken = ""
		data.Notifications = p
	}
	if e.Mailbox != nil {
		mb, err := e.Mailbox.GetMailbox(ctx, userID)
		switch {
		case err == nil:
			data.Mailbox = &MailboxSummary{Host: mb.Host, Port: mb.Port, Username: mb.Username, Enabled: mb.Enabled,
				LastCheckedAt: mb.LastCheckedAt}
		case !errors.Is(err, tracking.ErrNotFound):
			return fmt.Errorf("export inbox tracking: %w", err)
		}
		sugs, err := e.Mailbox.Suggestions(ctx, userID, "")
		if err != nil {
			return fmt.Errorf("export mail suggestions: %w", err)
		}
		for _, x := range sugs {
			data.MailSuggestions = append(data.MailSuggestions, SuggestionSummary{ApplicationID: x.ApplicationID,
				From: x.MessageFrom, Subject: x.MessageSubject, MessageDate: x.MessageDate,
				SuggestedState: x.SuggestedState, Status: x.Status})
		}
	}
	return nil
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
