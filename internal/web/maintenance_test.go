package web_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/maintenance"
	"github.com/1xxn/getafjob/internal/web"
)

type fakeMaintenance struct {
	mu sync.Mutex
	st maintenance.State
}

func (f *fakeMaintenance) State(context.Context) (maintenance.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st, nil
}

func (f *fakeMaintenance) Set(_ context.Context, on bool, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.st = maintenance.State{On: on, Message: message}
	if on {
		f.st.Since = time.Now()
	}
	return nil
}

// In maintenance everyone but admins gets the maintenance page with 503; sign-in stays open, admins work with a
// banner and end it with one click (#518).
func TestMaintenanceMode(t *testing.T) {
	m := &fakeMaintenance{}
	a := newApp(t, func(s *web.Server) { s.Maintenance = m })
	admin := signedInAs(t, a, "admin@example.org", identity.RoleAdmin)
	user := signedIn(t, a)

	if rec := a.do(http.MethodPost, "/admin/maintenance", url.Values{"on": {"1"}, "message": {"back at\n18:00"}}, user, nil); rec.Code != http.StatusNotFound || m.st.On {
		t.Fatalf("user starts maintenance: %d", rec.Code)
	}
	if body := a.do(http.MethodGet, "/admin/system", nil, admin, nil).Body.String(); !strings.Contains(body, `data-maintenance="off"`) {
		t.Fatal("system page lacks the switch")
	}
	rec := a.do(http.MethodPost, "/admin/maintenance", url.Values{"on": {"1"}, "message": {"back at\n18:00"}, "back": {"/admin/system"}}, admin, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/system" || !m.st.On || m.st.Message != "back at 18:00" {
		t.Fatalf("start: %d %q %+v", rec.Code, rec.Header().Get("Location"), m.st)
	}

	for name, session := range map[string]string{"visitor": "", "user": user} {
		for _, p := range []string{"/", "/jobs", "/admin/system"} {
			rec := a.do(http.MethodGet, p, nil, session, nil)
			body := rec.Body.String()
			if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" ||
				rec.Header().Get("X-Robots-Tag") != "noindex" || !strings.Contains(body, "data-maintenance-page") ||
				!strings.Contains(body, "back at 18:00") {
				t.Errorf("%s %s: %d %s", name, p, rec.Code, body)
			}
		}
		if rec := a.do(http.MethodPost, "/jobs/crawl", nil, session, nil); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s crawls in maintenance: %d", name, rec.Code)
		}
	}
	if rec := a.do(http.MethodGet, "/login", nil, "", nil); rec.Code != http.StatusOK {
		t.Errorf("sign-in closed in maintenance: %d", rec.Code)
	}
	home := a.do(http.MethodGet, "/", nil, admin, nil)
	if home.Code != http.StatusOK || !strings.Contains(home.Body.String(), "data-maintenance-banner") {
		t.Fatalf("admin home: %d", home.Code)
	}
	if body := a.do(http.MethodGet, "/admin/system", nil, admin, nil).Body.String(); !strings.Contains(body, `data-maintenance="on"`) {
		t.Error("system page does not show maintenance on")
	}
	if body := a.do(http.MethodGet, "/admin/users", nil, admin, nil).Body.String(); !strings.Contains(body, "Maintenance started") {
		t.Error("audit log lacks the start")
	}

	if rec := a.do(http.MethodPost, "/admin/maintenance", url.Values{"on": {"0"}, "back": {"/"}}, admin, nil); rec.Code != http.StatusSeeOther || m.st.On {
		t.Fatalf("end: %d %+v", rec.Code, m.st)
	}
	if rec := a.do(http.MethodGet, "/", nil, user, nil); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "data-maintenance") {
		t.Errorf("user after maintenance: %d", rec.Code)
	}
	if body := a.do(http.MethodGet, "/", nil, admin, nil).Body.String(); strings.Contains(body, "data-maintenance-banner") {
		t.Error("banner stays after maintenance")
	}
}
