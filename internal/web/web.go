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
	"golang.org/x/time/rate"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/i18n"
	"github.com/1xxn/getafjob/internal/web/views"
)

//go:embed static
var staticFiles embed.FS

// Server holds the dependencies of the UI.
type Server struct {
	Identity *identity.Service
	Logger   *slog.Logger
	limiter  *limiter
}

// Register adds all UI routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	s.limiter = newLimiter(rate.Every(6*time.Second), 10)
	sub, _ := fs.Sub(staticFiles, "static")
	static := http.StripPrefix("/static/", http.FileServerFS(sub))
	mux.Handle("GET /static/", cacheFor(time.Hour, static))

	mux.Handle("GET /{$}", s.page(s.home))
	mux.Handle("GET /login", s.page(s.loginForm))
	mux.Handle("POST /login", s.limited(s.page(s.loginSubmit)))
	mux.Handle("GET /login/link/{token}", s.page(s.linkConfirm))
	mux.Handle("POST /login/link/{token}", s.limited(s.page(s.linkSubmit)))
	mux.Handle("GET /invite/{token}", s.page(s.inviteForm))
	mux.Handle("POST /invite/{token}", s.limited(s.page(s.inviteSubmit)))
	mux.Handle("POST /logout", s.page(s.logout))
	mux.Handle("/", s.page(s.notFound))
}

// page sets the locale, forbids caching of personal pages and recovers the signed-in user.
func (s *Server) page(h func(http.ResponseWriter, *http.Request, *identity.User)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		u := s.currentUser(r)
		l := i18n.FromAcceptLanguage(r.Header.Get("Accept-Language"))
		if u != nil {
			l = u.Locale
		}
		h(w, r.WithContext(views.WithLocale(r.Context(), l)), u)
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
	s.Logger.ErrorContext(r.Context(), "request failed", "route", r.Pattern, "err", err)
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
	s.render(w, r, http.StatusOK, views.Home(u.DisplayName))
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
