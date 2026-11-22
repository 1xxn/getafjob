package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/platform/i18n"
)

// Service dispatches notifications and generates daily summaries.
type Service struct {
	store   Store
	ntfy    NtfyClient
	email   EmailClient
	webpush WebPushClient
	baseURL string
	logger  *slog.Logger
	now     func() time.Time
}

// NewService returns a notification service.
func NewService(store Store, ntfy NtfyClient, email EmailClient, webpush WebPushClient, baseURL string, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:   store,
		ntfy:    ntfy,
		email:   email,
		webpush: webpush,
		baseURL: strings.TrimRight(baseURL, "/"),
		logger:  logger,
		now:     time.Now,
	}
}

// SetLocation makes daily summaries and quiet hours follow the clock of loc instead of the server's.
func (s *Service) SetLocation(loc *time.Location) {
	if loc != nil {
		s.now = func() time.Time { return time.Now().In(loc) }
	}
}

// SetNow overrides the time source (used in tests).
func (s *Service) SetNow(now func() time.Time) {
	s.now = now
}

// GetPreferences returns user notification preferences, falling back to defaults if not set.
func (s *Service) GetPreferences(ctx context.Context, userID uuid.UUID) (Preferences, error) {
	prefs, err := s.store.GetPreferences(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			email, _ := s.store.GetUserEmail(ctx, userID)
			return DefaultPreferences(userID, email), nil
		}
		return Preferences{}, err
	}
	return prefs, nil
}

// SavePreferences persists user notification preferences.
func (s *Service) SavePreferences(ctx context.Context, prefs Preferences) error {
	if prefs.UserID == uuid.Nil {
		return errors.New("user ID is required")
	}
	if bad := prefs.Invalid(); len(bad) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidPreferences, strings.Join(bad, ", "))
	}
	return s.store.SavePreferences(ctx, prefs)
}

// Notify delivers a notification across all enabled channels for the recipient.
func (s *Service) Notify(ctx context.Context, n Notification) error {
	prefs, err := s.store.GetPreferences(ctx, n.UserID)
	if err != nil {
		email, _ := s.store.GetUserEmail(ctx, n.UserID)
		prefs = DefaultPreferences(n.UserID, email)
	}

	if n.Key != "" {
		l, err := s.store.UserLocale(ctx, n.UserID)
		if err != nil {
			l = i18n.Default
		}
		n.Title, n.Body = i18n.T(l, "notify."+n.Key+".title", n.Params), i18n.T(l, "notify."+n.Key+".body", n.Params)
	}

	// Quiet hours check: bypass for High or Urgent priority (e.g. CAPTCHAs, handoffs closing)
	if n.Priority < PriorityHigh && prefs.InQuietHours(s.now()) {
		s.logger.InfoContext(ctx, "notification skipped during quiet hours", "user_id", n.UserID, "event", n.Event)
		return nil
	}

	var errs []error
	sendPushNtfy := n.Event != EventDailySummary || prefs.DailySummaryEnabled

	// 1. Web Push
	if sendPushNtfy && prefs.WebPushEnabled && s.webpush != nil {
		link := s.absURL(n.URL)
		if err := s.webpush.SendWebPush(ctx, n.UserID, n.Title, n.Body, link); err != nil {
			errs = append(errs, fmt.Errorf("webpush: %w", err))
		}
	}

	// 2. Email (opt-in per event kind; non-urgent events bundle per kind and quarter hour)
	if prefs.EmailFor(n.Event) && s.email != nil {
		// Only the account address, which sign-in has verified: getafjob never mails an address the user typed.
		to, _ := s.store.GetUserEmail(ctx, n.UserID)
		if to != "" {
			link := s.absURL(n.URL)
			if kind, immediate := EmailBundleKind(n.Event); immediate {
				emailBody := n.Body
				if link != "" {
					emailBody += "\n\n" + link
				}
				if err := s.email.SendEmail(ctx, to, n.Title, emailBody); err != nil {
					errs = append(errs, fmt.Errorf("email: %w", err))
				}
			} else {
				createdAt := n.CreatedAt
				if createdAt.IsZero() {
					createdAt = s.now()
				}
				if err := s.store.EnqueuePendingEmail(ctx, PendingEmail{
					UserID:    n.UserID,
					Kind:      kind,
					Title:     n.Title,
					Body:      n.Body,
					URL:       link,
					CreatedAt: createdAt,
				}); err != nil {
					errs = append(errs, fmt.Errorf("email queue: %w", err))
				}
			}
		}
	}

	// 3. ntfy
	if sendPushNtfy && prefs.NtfyEnabled && s.ntfy != nil && strings.TrimSpace(prefs.NtfyTopic) != "" {
		if err := s.ntfy.SendNtfy(ctx, prefs.NtfyURL, prefs.NtfyTopic, prefs.NtfyToken, n); err != nil {
			errs = append(errs, fmt.Errorf("ntfy: %w", err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (s *Service) absURL(raw string) string {
	if raw == "" || strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") || s.baseURL == "" {
		return raw
	}
	return s.baseURL + "/" + strings.TrimLeft(raw, "/")
}

// FlushEmailBundles sends at most one bundled digest email per user and kind every BundleWindow (#515).
func (s *Service) FlushEmailBundles(ctx context.Context) (int, error) {
	if s.email == nil {
		return 0, nil
	}
	now := s.now()
	bundles, err := s.store.ListDueEmailBundles(ctx, now.Add(-BundleWindow))
	if err != nil {
		return 0, fmt.Errorf("listing due email bundles: %w", err)
	}
	sent := 0
	for _, b := range bundles {
		if len(b.Items) == 0 {
			continue
		}
		var maxID int64
		for _, it := range b.Items {
			if it.ID > maxID {
				maxID = it.ID
			}
		}
		prefs, err := s.GetPreferences(ctx, b.UserID)
		if err != nil {
			s.logger.WarnContext(ctx, "loading preferences for email bundle", "user_id", b.UserID, "err", err)
			continue
		}
		to, _ := s.store.GetUserEmail(ctx, b.UserID)
		if !prefs.EmailForKind(b.Kind) || to == "" {
			if err := s.store.MarkEmailBundleSent(ctx, b.UserID, b.Kind, maxID, now); err != nil {
				s.logger.WarnContext(ctx, "draining disabled email bundle", "user_id", b.UserID, "kind", b.Kind, "err", err)
			}
			continue
		}
		subj, body := s.formatBundle(ctx, b)
		if err := s.email.SendEmail(ctx, to, subj, body); err != nil {
			s.logger.WarnContext(ctx, "sending email bundle", "user_id", b.UserID, "kind", b.Kind, "err", err)
			continue
		}
		if err := s.store.MarkEmailBundleSent(ctx, b.UserID, b.Kind, maxID, now); err != nil {
			s.logger.WarnContext(ctx, "recording email bundle send", "user_id", b.UserID, "kind", b.Kind, "err", err)
			continue
		}
		sent++
	}
	return sent, nil
}

func (s *Service) formatBundle(ctx context.Context, b PendingEmailBundle) (subj, body string) {
	if len(b.Items) == 1 {
		it := b.Items[0]
		body = it.Body
		if it.URL != "" {
			body += "\n\n" + it.URL
		}
		return it.Title, body
	}
	l, err := s.store.UserLocale(ctx, b.UserID)
	if err != nil {
		l = i18n.Default
	}
	subj = i18n.T(l, "notify.bundle."+b.Kind+".title", map[string]any{"n": len(b.Items)})
	parts := make([]string, 0, len(b.Items))
	for _, it := range b.Items {
		lines := []string{"• " + it.Title}
		if it.Body != "" {
			lines = append(lines, "  "+it.Body)
		}
		if it.URL != "" {
			lines = append(lines, "  "+it.URL)
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	return subj, strings.Join(parts, "\n\n")
}

// SendDailySummary aggregates stats and sends the 24h summary notification to a user.
func (s *Service) SendDailySummary(ctx context.Context, userID uuid.UUID) error {
	now := s.now()
	stats, err := s.store.DailyStats(ctx, userID, now.Add(-24*time.Hour))
	if err != nil {
		return fmt.Errorf("aggregating daily stats: %w", err)
	}

	l, err := s.store.UserLocale(ctx, userID)
	if err != nil {
		l = i18n.Default
	}
	title, body := stats.FormatSummary(l)
	n := Notification{
		UserID:    userID,
		Event:     EventDailySummary,
		Title:     title,
		Body:      body,
		URL:       "/",
		Priority:  PriorityLow,
		Tags:      []string{"briefcase", "chart_with_upwards_trend"},
		CreatedAt: now,
	}

	if err := s.Notify(ctx, n); err != nil {
		return err
	}

	return s.store.UpdateLastSummaryAt(ctx, userID, now)
}

// CheckAndSendDailySummaries checks all users and delivers summaries that are due.
func (s *Service) CheckAndSendDailySummaries(ctx context.Context) (int, error) {
	now := s.now()
	candidates, err := s.store.ListDailySummaryCandidates(ctx)
	if err != nil {
		return 0, fmt.Errorf("listing daily summary candidates: %w", err)
	}

	sent := 0
	for _, p := range candidates {
		if p.IsSummaryDue(now) {
			if err := s.SendDailySummary(ctx, p.UserID); err != nil {
				s.logger.WarnContext(ctx, "failed sending daily summary", "user_id", p.UserID, "err", err)
				continue
			}
			sent++
		}
	}
	return sent, nil
}

// SendTestNotification sends an immediate test notification to verify all channels.
func (s *Service) SendTestNotification(ctx context.Context, userID uuid.UUID) error {
	now := s.now()
	n := Notification{
		UserID:    userID,
		Event:     EventTest,
		Key:       "test",
		URL:       "/settings/notifications",
		Priority:  PriorityHigh,
		Tags:      []string{"bell", "white_check_mark"},
		CreatedAt: now,
	}
	return s.Notify(ctx, n)
}
