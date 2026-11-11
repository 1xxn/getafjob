package web_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/web"
)

func TestAppShell(t *testing.T) {
	a := newApp(t)
	cookie := signedIn(t, a)

	page := a.do(http.MethodGet, "/jobs", nil, cookie, nil)
	body := page.Body.String()
	for _, want := range []string{`hx-boost:inherited="true"`, `id="live-dock"`, `id="palette"`, `id="confirm-dialog"`,
		`href="/jobs" class="shell-link" aria-current="page"`, "/static/js/shell.js"} {
		if !strings.Contains(body, want) {
			t.Errorf("shell lacks %s", want)
		}
	}
	checkNonces(t, page)

	// Theme: stored in a cookie, back only to a local path, unknown values clear it.
	rec := a.do(http.MethodPost, "/settings/theme", url.Values{"theme": {"dark"}, "back": {"/jobs?page=2"}}, cookie, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/jobs?page=2" ||
		!strings.Contains(rec.Header().Get("Set-Cookie"), "theme=dark") {
		t.Fatalf("theme: %d %q %q", rec.Code, rec.Header().Get("Location"), rec.Header().Get("Set-Cookie"))
	}
	for _, back := range []string{"//evil.example", "https://evil.example", "/\\evil.example", ""} {
		rec := a.do(http.MethodPost, "/settings/theme", url.Values{"theme": {"x"}, "back": {back}}, cookie, nil)
		if rec.Header().Get("Location") != "/" || !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
			t.Errorf("back %q: %q %q", back, rec.Header().Get("Location"), rec.Header().Get("Set-Cookie"))
		}
	}
	dark := a.do(http.MethodGet, "/jobs", nil, "", cookies(cookie, "theme=dark", nil))
	if !strings.Contains(dark.Body.String(), `<html lang="en" data-theme="dark"`) {
		t.Fatal("theme cookie not applied")
	}

	// Language: saved for the user, the next page says so once.
	rec = a.do(http.MethodPost, "/settings/language", url.Values{"locale": {"de"}, "back": {"/jobs"}}, cookie, nil)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Set-Cookie"), "flash=flash.language") {
		t.Fatalf("language: %d %q", rec.Code, rec.Header().Get("Set-Cookie"))
	}
	de := a.do(http.MethodGet, "/jobs", nil, "", cookies(cookie, "flash=flash.language", map[string]string{"Accept": "text/html"}))
	if !strings.Contains(de.Body.String(), `lang="de"`) || !strings.Contains(de.Body.String(), "Sprache geändert.") ||
		!strings.Contains(de.Header().Get("Set-Cookie"), "flash=;") {
		t.Fatal("language not switched or flash not shown and cleared")
	}
	if forged := a.do(http.MethodGet, "/jobs", nil, "", cookies(cookie, "flash=error.generic.title", map[string]string{"Accept": "text/html"})); strings.Contains(forged.Body.String(), `class="ui-toast `) {
		t.Fatal("unknown flash key rendered")
	}

	// Counters as JSON for the live refresh.
	rec = a.do(http.MethodGet, "/shell/counts", nil, cookie, nil)
	var counts map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &counts); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("counts: %d %v", rec.Code, err)
	}
	if _, ok := counts["approvals"]; !ok {
		t.Fatalf("counts: %v", counts)
	}
	if rec := a.do(http.MethodGet, "/shell/counts", nil, "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("counts signed out: %d", rec.Code)
	}
}

// cookies sends the session plus extra cookies in one Cookie header.
func cookies(session, extra string, hdr map[string]string) map[string]string {
	out := map[string]string{"Cookie": web.SessionCookie + "=" + session + "; " + extra}
	for k, v := range hdr {
		out[k] = v
	}
	return out
}
