package web_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/web"
)

type fakeOps struct {
	enabled map[int64]bool
	retried []int64
}

func (f *fakeOps) Runners(context.Context, time.Time) ([]web.RunnerStat, error) {
	return []web.RunnerStat{{ID: "runner-a", LastLease: time.Now(), Active: 1, Runs24h: 3, Submitted24h: 1}}, nil
}

func (f *fakeOps) Sources(context.Context, string, string, int) ([]web.SourceStat, int, error) {
	return []web.SourceStat{{ID: 7, ATS: "lever", Slug: "beispiel", Company: "Beispiel AG", Enabled: f.enabled[7], LastError: "http 404"}}, 1, nil
}

func (f *fakeOps) SetSourceEnabled(_ context.Context, id int64, on bool) (bool, error) {
	if id != 7 {
		return false, nil
	}
	f.enabled[id] = on
	return true, nil
}

func (f *fakeOps) Queue(context.Context, time.Time) ([]web.QueueCount, []web.QueueFailure, error) {
	return []web.QueueCount{{Kind: "crawl", State: "completed", Count: 2}},
		[]web.QueueFailure{{ID: 42, Kind: "match", State: "discarded", Attempt: 3, Error: "boom", At: time.Now()}}, nil
}

func (f *fakeOps) Retry(_ context.Context, id int64) error {
	f.retried = append(f.retried, id)
	return nil
}

// Runners, sources and the queue are for admins only; switching a source and running a job again are audited.
func TestAdminOpsPages(t *testing.T) {
	ops := &fakeOps{enabled: map[int64]bool{7: true}}
	a := newApp(t, func(s *web.Server) { s.Ops = ops })
	admin := signedInAs(t, a, "ops-admin@example.org", identity.RoleAdmin)
	user := signedIn(t, a)

	for path, want := range map[string]string{
		"/admin/runners": `data-runner="runner-a"`,
		"/admin/sources": `data-source="7"`,
		"/admin/queue":   `data-job="42"`,
	} {
		if rec := a.do(http.MethodGet, path, nil, user, nil); rec.Code != http.StatusNotFound {
			t.Errorf("user sees %s: %d", path, rec.Code)
		}
		if body := a.do(http.MethodGet, path, nil, admin, nil).Body.String(); !strings.Contains(body, want) {
			t.Errorf("%s lacks %s", path, want)
		}
	}
	if rec := a.do(http.MethodPost, "/admin/sources/7/disable", nil, user, nil); rec.Code != http.StatusNotFound || !ops.enabled[7] {
		t.Errorf("user switched a source: %d", rec.Code)
	}
	rec := a.do(http.MethodPost, "/admin/sources/7/disable", map[string][]string{"back": {"/admin/sources?state=failing"}}, admin, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/sources?state=failing" || ops.enabled[7] {
		t.Fatalf("disable %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := a.do(http.MethodPost, "/admin/sources/9/enable", nil, admin, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown source %d", rec.Code)
	}
	if rec := a.do(http.MethodPost, "/admin/queue/42/retry", nil, admin, nil); rec.Header().Get("Location") != "/admin/queue?done=retried" ||
		len(ops.retried) != 1 {
		t.Fatalf("retry %d", rec.Code)
	}
	log := a.do(http.MethodGet, "/admin/users", nil, admin, nil).Body.String()
	for _, want := range []string{"Board 7 switched off", "Job 42 queued again"} {
		if !strings.Contains(log, want) {
			t.Errorf("admin log lacks %q", want)
		}
	}
}
