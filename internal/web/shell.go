package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/i18n"
	"github.com/1xxn/getafjob/internal/web/views"
)

const (
	themeCookie   = "theme"
	navCookie     = "nav"
	flashCookie   = "flash"
	oneYearSecond = 365 * 24 * 3600
)

// flashKeys are the one-time messages a redirect may leave for the next page; anything else is ignored.
var flashKeys = map[string]bool{"flash.language": true,
	"flash.jobs.queue": true, "flash.jobs.save": true, "flash.jobs.skip": true, "flash.jobs.block": true,
	"flash.jobs.queue_partial": true, "flash.jobs.save_partial": true, "flash.jobs.skip_partial": true,
	"flash.jobs.block_partial": true, "flash.jobs.not_queued.mail": true, "flash.jobs.not_queued.duplicate": true,
	"flash.jobs.not_queued.company": true, "flash.jobs.not_queued.excluded": true, "flash.jobs.not_queued.other": true}

// shell returns the app frame data of the request: theme and sidebar width from cookies for everyone, for
// signed-in users also the counters (computed once, when the frame renders) and a one-time message.
func (s *Server) shell(w http.ResponseWriter, r *http.Request, u *identity.User) views.Shell {
	sh := views.Shell{Theme: cookieTheme(r), Path: r.URL.Path, Back: r.URL.RequestURI()}
	if u == nil {
		return sh
	}
	sh.UserName, sh.Admin = u.DisplayName, u.Role == identity.RoleAdmin
	if c, err := r.Cookie(navCookie); err == nil && c.Value == "collapsed" {
		sh.Collapsed = true
	}
	if r.Method == http.MethodGet && !partial(r) && strings.Contains(r.Header.Get("Accept"), "text/html") {
		if c, err := r.Cookie(flashCookie); err == nil {
			if flashKeys[c.Value] {
				sh.Flash = c.Value
			}
			http.SetCookie(w, &http.Cookie{Name: flashCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true,
				SameSite: http.SameSiteLaxMode})
		}
	}
	ctx, id := r.Context(), u.ID
	sh.Counts = sync.OnceValue(func() *views.NavCounts {
		if s.Apps == nil {
			return nil
		}
		c, err := s.Apps.NavCounts(ctx, id)
		if err != nil {
			s.Logger.WarnContext(ctx, "navigation counters", "err", err)
			return nil
		}
		return &views.NavCounts{NeedsInput: c.NeedsInput, Approvals: c.Approvals, Running: c.Running}
	})
	return sh
}

func cookieTheme(r *http.Request) string {
	if c, err := r.Cookie(themeCookie); err == nil {
		return views.ParseTheme(c.Value)
	}
	return ""
}

// localBack accepts only a path on this site as the return target of a settings form.
func localBack(s string) string {
	if !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/\\") || strings.ContainsAny(s, "\r\n") {
		return "/"
	}
	return s
}

// setTheme stores the reader's theme choice in a cookie (light, dark, or empty for the system setting).
func (s *Server) setTheme(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	c := &http.Cookie{Name: themeCookie, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}
	if t := views.ParseTheme(r.PostFormValue("theme")); t != "" {
		c.Value, c.MaxAge = t, oneYearSecond
	} else {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
	http.Redirect(w, r, localBack(r.PostFormValue("back")), http.StatusSeeOther) //nolint:gosec // localBack keeps only paths on this site
}

// setLanguage changes the user's UI language.
func (s *Server) setLanguage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := s.Identity.SetLocale(r.Context(), u.ID, i18n.Parse(r.PostFormValue("locale"))); err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "flash.language", Path: "/", MaxAge: 60, HttpOnly: true,
		Secure: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, localBack(r.PostFormValue("back")), http.StatusSeeOther) //nolint:gosec // localBack keeps only paths on this site
}

// shellCounts returns the navigation counters as JSON; the shell script refreshes them on live events.
func (s *Server) shellCounts(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	out := map[string]int{"needs_input": 0, "approvals": 0, "running": 0}
	if c := views.ShellFrom(r.Context()).Counts; c != nil {
		if n := c(); n != nil {
			out["needs_input"], out["approvals"], out["running"] = n.NeedsInput, n.Approvals, n.Running
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
