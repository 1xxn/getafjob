package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/web"
)

func TestStyleGuide(t *testing.T) {
	off := http.NewServeMux()
	(&web.Server{}).Register(off)
	rec := httptest.NewRecorder()
	off.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dev/styleguide", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("style guide without the dev switch: %d", rec.Code)
	}

	a := newApp(t)
	if rec := a.do(http.MethodGet, "/dev/styleguide", nil, "", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("signed out: %d", rec.Code)
	}
	cookie := signedIn(t, a)
	rec = a.do(http.MethodGet, "/dev/styleguide", nil, cookie, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "ui-btn-primary") || !strings.Contains(body, "/static/ui.css") ||
		!strings.Contains(body, "/static/icons.svg") || strings.Contains(body, "data-theme") {
		t.Fatalf("style guide: %d", rec.Code)
	}
	checkNonces(t, rec)
	if rec := a.do(http.MethodGet, "/dev/styleguide?theme=dark", nil, cookie, nil); !strings.Contains(rec.Body.String(), `data-theme="dark"`) {
		t.Fatal("manual dark theme not set")
	}
	if rec := a.do(http.MethodGet, "/dev/styleguide?theme=x%22", nil, cookie, nil); strings.Contains(rec.Body.String(), "data-theme") {
		t.Fatal("unknown theme value passed through")
	}
}
