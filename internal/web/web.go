// Package web serves the server-rendered UI (templ views) of the serve role.
package web

import (
	"context"
	"embed"
	"errors"
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

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/notify"
	"github.com/1xxn/getafjob/internal/platform/httpx"
	"github.com/1xxn/getafjob/internal/platform/i18n"
	"github.com/1xxn/getafjob/internal/platform/webpush"
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

// Server holds the dependencies of the UI.
type Server struct {
	Identity       *identity.Service
	Profile        *profile.Service
	Documents      *profile.Documents
	Items          *profile.Items
	Importer       *cvimport.Importer // nil: no CV import
	Jobs           *matching.Listings
	Apps           *application.Service
	Tracking       TrackingService
	Notify         NotificationService
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
	static := http.StripPrefix("/static/", http.FileServerFS(sub))
	mux.Handle("GET /static/", cacheFor(time.Hour, static))
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
	mux.Handle("GET /jobs/{id}", s.page(s.jobPage))
	mux.Handle("GET /approvals", s.page(s.approvalsPage))
	mux.Handle("POST /approvals/{id}/approve", s.page(s.approvalApprove))
	mux.Handle("GET /applications/{id}", s.page(s.applicationPage))
	mux.Handle("GET /applications/{id}/screenshots/{checkpoint}", s.page(s.applicationScreenshot))
	mux.Handle("GET /applications/{id}/screen", s.page(s.screenWebSocket))
	mux.Handle("POST /applications/{id}/answer", s.page(s.applicationAnswer))
	mux.Handle("POST /applications/{id}/park", s.page(s.applicationPark))
	mux.Handle("POST /applications/{id}/suggestions/{sugId}/accept", s.page(s.suggestionAccept))
	mux.Handle("POST /applications/{id}/suggestions/{sugId}/dismiss", s.page(s.suggestionDismiss))
	mux.Handle("GET /profile", s.page(s.profilePage))
	mux.Handle("POST /profile", s.page(s.profileSave))
	mux.Handle("GET /preferences", s.page(s.preferencesPage))
	mux.Handle("POST /preferences", s.page(s.preferencesSave))
	mux.Handle("POST /preferences/notify/test", s.page(s.notifyTest))
	mux.Handle("GET /documents", s.page(s.documentsPage))
	mux.Handle("POST /documents", s.page(s.documentUpload))
	mux.Handle("GET /documents/{id}", s.page(s.documentDownload))
	mux.Handle("POST /documents/{id}/variant", s.page(s.documentVariant))
	mux.Handle("POST /documents/{id}/delete", s.page(s.documentDelete))
	mux.Handle("POST /documents/{id}/import", s.limited(s.page(s.documentImport)))
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
