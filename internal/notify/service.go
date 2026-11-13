package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/platform/i18n"
)

// ntfyTopicRx is the topic shape ntfy accepts; it keeps the topic one path segment of the server URL.
var ntfyTopicRx = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

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
	if prefs.NtfyEnabled && strings.TrimSpace(prefs.NtfyTopic) == "" {
		return fmt.Errorf("%w: ntfy topic is required when ntfy is enabled", ErrInvalidPreferences)
	}
	if t := strings.TrimSpace(prefs.NtfyTopic); t != "" && !ntfyTopicRx.MatchString(t) {
		return fmt.Errorf("%w: ntfy topic may hold letters, digits, - and _ only", ErrInvalidPreferences)
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

	// 1. Web Push
	if prefs.WebPushEnabled && s.webpush != nil {
		link := n.URL
		if link != "" && !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") && s.baseURL != "" {
			link = s.baseURL + "/" + strings.TrimLeft(link, "/")
		}
		if err := s.webpush.SendWebPush(ctx, n.UserID, n.Title, n.Body, link); err != nil {
			errs = append(errs, fmt.Errorf("webpush: %w", err))
		}
	}

	// 2. Email
	if prefs.EmailEnabled && s.email != nil {
		// Only the account address, which sign-in has verified: getafjob never mails an address the user typed.
		to, _ := s.store.GetUserEmail(ctx, n.UserID)
		if to != "" {
			emailBody := n.Body
			if n.URL != "" {
				link := n.URL
				if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") && s.baseURL != "" {
					link = s.baseURL + "/" + strings.TrimLeft(link, "/")
				}
				emailBody += "\n\n" + link
			}
			if err := s.email.SendEmail(ctx, to, n.Title, emailBody); err != nil {
				errs = append(errs, fmt.Errorf("email: %w", err))
			}
		}
	}

	// 3. ntfy
	if prefs.NtfyEnabled && s.ntfy != nil && strings.TrimSpace(prefs.NtfyTopic) != "" {
		if err := s.ntfy.SendNtfy(ctx, prefs.NtfyURL, prefs.NtfyTopic, prefs.NtfyToken, n); err != nil {
			errs = append(errs, fmt.Errorf("ntfy: %w", err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
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
