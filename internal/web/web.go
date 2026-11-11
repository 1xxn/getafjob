// Package web serves the server-rendered UI (templ views) of the serve role.
package web

import (
	"context"
	"embed"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/1xxn/getafjob/internal/ai"
	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/assist"
	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/urlimport"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/notify"
	"github.com/1xxn/getafjob/internal/platform/httpx"
	"github.com/1xxn/getafjob/internal/platform/i18n"
	"github.com/1xxn/getafjob/internal/platform/webpush"
	"github.com/1xxn/getafjob/internal/portals"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/profile/cvimport"
	"github.com/1xxn/getafjob/internal/tracking"
	"github.com/1xxn/getafjob/internal/web/views"
)

//go:generate go run ./tsbuild

//go:embed static
var staticFiles embed.FS

// TrackingService manages user mailbox settings and status suggestions.
type TrackingService interface {
	GetMailbox(ctx context.Context, userID uuid.UUID) (tracking.MailboxAccount, error)
	SaveMailbox(ctx context.Context, userID uuid.UUID, host string, port int, tls bool, username, password string, enabled bool) error
	SuggestionsForApplication(ctx context.Context, userID, appID uuid.UUID) ([]tracking.StatusSuggestion, error)
	AcceptSuggestion(ctx context.Context, userID, suggestionID uuid.UUID) error
	DismissSuggestion(ctx context.Context, userID, suggestionID uuid.UUID) error
}

// NotificationService manages user notification settings and test alerts.
type NotificationService interface {
	GetPreferences(ctx context.Context, userID uuid.UUID) (notify.Preferences, error)
	SavePreferences(ctx context.Context, prefs notify.Preferences) error
	SendTestNotification(ctx context.Context, userID uuid.UUID) error
}

// AssistService manages extension pairing and tokens.
type AssistService interface {
	GeneratePairingCode(ctx context.Context, userID uuid.UUID) (string, error)
	ListExtensionTokens(ctx context.Context, userID uuid.UUID) ([]assist.Token, error)
	RevokeExtensionToken(ctx context.Context, id, userID uuid.UUID) error
}

// PortalsService manages portal sessions and accounts for Easy Apply.
type PortalsService interface {
	AdminEnabled() bool
	ListAccounts(ctx context.Context, userID uuid.UUID) ([]portals.AccountView, error)
	SaveCookies(ctx context.Context, userID uuid.UUID, portal string, cookiesJSON []byte, dailyLimit int) error
	Disconnect(ctx context.Context, userID uuid.UUID, portal string) error
	SetDailyLimit(ctx context.Context, userID uuid.UUID, portal string, limit int) error
}

// AIService manages AI configuration, budgets, and capabilities (ADR 0012, #44).
type AIService interface {
	AdminEnabled() bool
	GetSettings(ctx context.Context, userID uuid.UUID) (ai.UserSettings, error)
	SaveSettings(ctx context.Context, userID uuid.UUID, enabled bool, customKey string, clearKey bool) error
}

// PrivacyService exports and permanently deletes user data.
type PrivacyService interface {
	Export(ctx context.Context, userID uuid.UUID, w io.Writer) error
	DeleteAccount(ctx context.Context, userID uuid.UUID) error
}

// DiscoveryService manages job URL ingestion, crawl triggers, status, and board additions.
type DiscoveryService interface {
	ImportJob(ctx context.Context, userID uuid.UUID, rawURL string) (int64, error)
	TriggerCrawl(ctx context.Context) (queued bool, err error)
	CrawlStatus(ctx context.Context) (urlimport.CrawlStatus, error)
	AddBoard(ctx context.Context, rawURL string) (discovery.Board, error)
}

// Server holds the dependencies of the UI.
type Server struct {
	Identity       *identity.Service
	Profile        *profile.Service
	Documents      *profile.Documents
	Items          *profile.Items
	Stations       *profile.Stations
	Importer       *cvimport.Importer // nil: no CV import
	Jobs           *matching.Listings
	Discovery      DiscoveryService // nil: no discovery controls
	Apps           *application.Service
	Tracking       TrackingService
	Notify         NotificationService
	Assist         AssistService
	Portals        PortalsService
	AI             AIService
	Privacy        PrivacyService
	Shots          Screenshots
	Handoffs       HandoffHub
	PushStore      webpush.Store
	VAPIDPublicKey string
	Logger         *slog.Logger
	limiter        *limiter
}

// Register adds all UI routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	s.limiter = newLimiter(rate.Every(6*time.Second), 10)
	sub, _ := fs.Sub(staticFiles, "static")
	if err := views.SetAssets(sub); err != nil && s.Logger != nil {
		s.Logger.Error("static file versions", "err", err)
	}
	static := http.StripPrefix("/static/", http.FileServerFS(sub))
	mux.Handle("GET /static/", staticCache(static))
	mux.Handle("GET /sw.js", cacheFor(time.Hour, http.HandlerFunc(s.serviceWorker)))
	mux.Handle("GET /push/key", http.HandlerFunc(s.vapidPublicKey))
	mux.Handle("POST /push/subscribe", s.limited(s.page(s.pushSubscribe)))
	mux.Handle("DELETE /push/subscribe", s.limited(s.page(s.pushUnsubscribe)))
	mux.Handle("GET /speculation-rules", cacheFor(time.Hour, http.HandlerFunc(speculationRules)))

	mux.Handle("GET /{$}", s.page(s.home))
	mux.Handle("GET /login", s.page(s.loginForm))
	mux.Handle("POST /login", s.limited(s.page(s.loginSubmit)))
	mux.Handle("GET /login/link/{token}", s.page(s.linkConfirm))
	mux.Handle("POST /login/link/{token}", s.limited(s.page(s.linkSubmit)))
	mux.Handle("GET /invite/{token}", s.page(s.inviteForm))
	mux.Handle("POST /invite/{token}", s.limited(s.page(s.inviteSubmit)))
	mux.Handle("POST /logout", s.page(s.logout))
	mux.Handle("GET /jobs", s.page(s.jobsPage))
	mux.Handle("POST /jobs/import", s.limited(s.page(s.importJob)))
	mux.Handle("POST /jobs/crawl", s.limited(s.page(s.triggerCrawl)))
	mux.Handle("GET /jobs/crawl/status", s.page(s.crawlStatus))
	mux.Handle("POST /jobs/boards", s.limited(s.page(s.addBoard)))
	mux.Handle("POST /jobs/blocklist", s.page(s.addBlocklist))
	mux.Handle("POST /jobs/blocklist/delete", s.page(s.deleteBlocklist))
	mux.Handle("GET /jobs/{id}", s.page(s.jobPage))
	mux.Handle("POST /jobs/{id}/skills", s.page(s.addJobSkill))
	mux.Handle("POST /jobs/{id}/cv", s.page(s.setJobCV))
	mux.Handle("GET /jobs/{id}/apply", s.page(s.jobApplyPreview))
	mux.Handle("POST /jobs/{id}/apply", s.page(s.jobApplySubmit))
	mux.Handle("POST /jobs/{id}/bookmark", s.page(s.jobBookmark))
	mux.Handle("POST /jobs/{id}/apply-external", s.page(s.jobApplyExternal))
	mux.Handle("GET /tracker", s.page(s.trackerPage))
	mux.Handle("POST /tracker/external", s.limited(s.page(s.trackerAddExternal)))
	mux.Handle("POST /tracker/{id}/stage", s.page(s.trackerSetStage))
	mux.Handle("POST /tracker/{id}/notes", s.page(s.trackerSaveNotes))
	mux.Handle("GET /approvals", s.page(s.approvalsPage))
	mux.Handle("POST /approvals/{id}/approve", s.page(s.approvalApprove))
	mux.Handle("POST /approvals/{id}/cv", s.page(s.approvalSetCV))
	mux.Handle("GET /applications/{id}", s.page(s.applicationPage))
	mux.Handle("GET /applications/{id}/screenshots/{checkpoint}", s.page(s.applicationScreenshot))
	mux.Handle("GET /applications/{id}/screen", s.page(s.screenWebSocket))
	mux.Handle("GET /live/indicator", s.page(s.liveIndicator))
	mux.Handle("POST /applications/{id}/answer", s.page(s.applicationAnswer))
	mux.Handle("POST /applications/{id}/park", s.page(s.applicationPark))
	mux.Handle("POST /applications/{id}/suggestions/{sugId}/accept", s.page(s.suggestionAccept))
	mux.Handle("POST /applications/{id}/suggestions/{sugId}/dismiss", s.page(s.suggestionDismiss))
	mux.Handle("GET /profile", s.page(s.profilePage))
	mux.Handle("POST /profile", s.page(s.profileSave))
	mux.Handle("POST /profile/experiences", s.page(s.experienceAdd))
	mux.Handle("POST /profile/experiences/{id}", s.page(s.experienceSave))
	mux.Handle("POST /profile/experiences/{id}/up", s.page(s.experienceMove("up")))
	mux.Handle("POST /profile/experiences/{id}/down", s.page(s.experienceMove("down")))
	mux.Handle("POST /profile/experiences/{id}/delete", s.page(s.experienceDelete))
	mux.Handle("POST /profile/educations", s.page(s.educationAdd))
	mux.Handle("POST /profile/educations/{id}", s.page(s.educationSave))
	mux.Handle("POST /profile/educations/{id}/up", s.page(s.educationMove("up")))
	mux.Handle("POST /profile/educations/{id}/down", s.page(s.educationMove("down")))
	mux.Handle("POST /profile/educations/{id}/delete", s.page(s.educationDelete))
	mux.Handle("GET /preferences", s.page(s.preferencesPage))
	mux.Handle("POST /preferences", s.page(s.preferencesSave))
	mux.Handle("POST /preferences/notify/test", s.page(s.notifyTest))
	mux.Handle("POST /preferences/assist/pair", s.page(s.assistPair))
	mux.Handle("POST /preferences/runner/token", s.page(s.runnerToken))
	mux.Handle("GET /documents", s.page(s.documentsPage))
	mux.Handle("POST /documents", s.page(s.documentUpload))
	mux.Handle("GET /documents/{id}", s.page(s.documentDownload))
	mux.Handle("POST /documents/{id}/variant", s.page(s.documentVariant))
	mux.Handle("POST /documents/{id}/delete", s.page(s.documentDelete))
	mux.Handle("POST /documents/{id}/import", s.limited(s.page(s.documentImport)))
	mux.Handle("GET /documents/{id}/review", s.limited(s.page(s.documentReview)))
	mux.Handle("POST /documents/{id}/review", s.limited(s.page(s.documentReviewSave)))
	mux.Handle("GET /answers", s.page(s.answersPage))
	mux.Handle("POST /answers", s.page(s.answerSave))
	mux.Handle("POST /answers/{id}", s.page(s.answerSave))
	mux.Handle("POST /answers/{id}/delete", s.page(s.itemDelete(profile.ItemAnswer, "/answers")))
	mux.Handle("GET /texts", s.page(s.textsPage))
	mux.Handle("POST /texts", s.page(s.textSave))
	mux.Handle("POST /texts/{id}", s.page(s.textSave))
	mux.Handle("POST /texts/{id}/delete", s.page(s.itemDelete(profile.ItemText, "/texts")))
	mux.Handle("GET /setup", s.page(s.setupStart))
	mux.Handle("GET /setup/{step}", s.page(s.setupPage))
	mux.Handle("POST /setup/{step}", s.page(s.setupSave))
	mux.Handle("POST /setup/cv", s.limited(s.page(s.setupCVUpload)))
	mux.Handle("POST /setup/cv/import/{id}", s.limited(s.page(s.setupCVImport)))
	mux.Handle("GET /account", s.page(s.account))
	mux.Handle("POST /account/sessions/others", s.page(s.signOutOthers))
	mux.Handle("POST /account/passkeys/options", s.page(s.passkeyAddOptions))
	mux.Handle("POST /account/passkeys", s.page(s.passkeyAdd))
	mux.Handle("POST /account/passkeys/{id}/delete", s.page(s.passkeyDelete))
	mux.Handle("POST /account/recovery/cancel", s.page(s.cancelRecovery))
	mux.Handle("GET /account/export", s.page(s.accountExport))
	mux.Handle("POST /account/delete", s.page(s.accountDelete))
	mux.Handle("GET /privacy", s.page(s.privacyPage))
	mux.Handle("POST /login/passkey/options", s.limited(s.page(s.passkeyLoginOptions)))
	mux.Handle("POST /login/passkey", s.limited(s.page(s.passkeyLogin)))
	mux.Handle("/", s.page(s.notFound))
}

// page sets the locale, forbids caching of personal pages and recovers the signed-in user.
func (s *Server) page(h func(http.ResponseWriter, *http.Request, *identity.User)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Speculation-Rules", `"/speculation-rules"`)
		w.Header().Add("Vary", "HX-Request")
		u, token := s.currentUser(w, r)
		l := i18n.FromAcceptLanguage(r.Header.Get("Accept-Language"))
		if u != nil {
			l = u.Locale
		}
		ctx := context.WithValue(views.WithLocale(r.Context(), l), tokenKey{}, token)
		ctx = templ.WithNonce(ctx, httpx.Nonce(ctx))
		h(w, r.WithContext(ctx), u)
	})
}

func cacheFor(d time.Duration, h http.Handler) http.Handler {
	v := "public, max-age=" + itoa(int(d.Seconds()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", v)
		h.ServeHTTP(w, r)
	})
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.Render(r.Context(), w); err != nil {
		s.Logger.ErrorContext(r.Context(), "render failed", "err", err)
	}
}

func (s *Server) problem(w http.ResponseWriter, r *http.Request, u *identity.User, status int, title, text string) {
	name := ""
	if u != nil {
		name = u.DisplayName
	}
	s.render(w, r, status, views.Problem(views.T(r.Context(), title), text, name))
}

// failed logs an unexpected error and shows a generic page (no details for the client). It logs the route pattern,
// not the path: paths of sign-in and invitation links carry one-time tokens.
func (s *Server) failed(w http.ResponseWriter, r *http.Request, u *identity.User, err error) {
	msg := ""
	if err != nil {
		msg = strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\r", " "), "\n", " ")
	}
	s.Logger.ErrorContext(r.Context(), "request failed", "route", r.Pattern, "err", msg)
	s.problem(w, r, u, http.StatusInternalServerError, "error.generic.title", views.T(r.Context(), "error.generic.text"))
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request, u *identity.User) {
	s.problem(w, r, u, http.StatusNotFound, "error.not_found.title", views.T(r.Context(), "error.not_found.text"))
}

func (s *Server) home(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	nudge := false
	if s.Identity.PasskeysEnabled() {
		keys, err := s.Identity.Passkeys(r.Context(), u.ID)
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		nudge = len(keys) == 0
	}
	snap, err := s.Profile.Get(r.Context(), u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	s.render(w, r, http.StatusOK, views.Home(u.DisplayName, nudge, len(snap.Profile.Missing()) > 0))
}

// limiter is a token bucket per client address.
type limiter struct {
	mu      sync.Mutex
	limit   rate.Limit
	burst   int
	buckets map[string]*bucket
}

type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

func newLimiter(l rate.Limit, burst int) *limiter {
	return &limiter{limit: l, burst: burst, buckets: map[string]*bucket{}}
}

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) > 10000 {
		for k, b := range l.buckets {
			if now.Sub(b.seen) > 10*time.Minute {
				delete(l.buckets, k)
			}
		}
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{lim: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[key] = b
	}
	b.seen = now
	return b.lim.AllowN(now, 1)
}

// clientIP is the address Caddy reports (it replaces untrusted X-Forwarded-For); serve is reachable only via Caddy.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) limited(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow(clientIP(r), time.Now()) {
			ctx := views.WithLocale(r.Context(), i18n.FromAcceptLanguage(r.Header.Get("Accept-Language")))
			r = r.WithContext(ctx)
			s.problem(w, r, nil, http.StatusTooManyRequests, "error.generic.title", views.T(ctx, "error.rate_limited"))
			return
		}
		h.ServeHTTP(w, r)
	})
}

func isCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// speculation prefetches job pages a user is about to open (hover or touch); prefetch only, nothing runs early.
const speculation = `{"prefetch":[{"where":{"href_matches":"/jobs/*"},"eagerness":"moderate"}]}`

func speculationRules(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/speculationrules+json")
	_, _ = w.Write([]byte(speculation))
}

// staticCache lets browsers keep a static file for a year when the URL names its current version (views.Asset),
// and for an hour otherwise.
func staticCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/static/")
		if v := r.URL.Query().Get("v"); v != "" && v == views.AssetVersion(p) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		next.ServeHTTP(w, r)
	})
}
