package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/web/views"
)

func TestStaticFilesAreVersioned(t *testing.T) {
	mux := http.NewServeMux()
	(&Server{}).Register(mux)
	sub, _ := fs.Sub(staticFiles, "static")
	if _, err := fs.Stat(sub, "js/handoff.js"); err != nil {
		t.Fatal(err)
	}
	u := views.Asset("js/handoff.js")
	if !strings.HasPrefix(u, "/static/js/handoff.js?v=") || len(u) < len("/static/js/handoff.js?v=")+12 {
		t.Fatalf("asset url %q", u)
	}
	for url, want := range map[string]string{u: "immutable", "/static/js/handoff.js": "max-age=3600",
		"/static/js/handoff.js?v=stale": "max-age=3600"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Cache-Control"), want) {
			t.Errorf("%s: %d %q, want %q", url, rec.Code, rec.Header().Get("Cache-Control"), want)
		}
	}
}
