package web_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// The profile and each station save themselves: the answer is the status line (and the new version of the
// profile); invalid values are named and not stored. Dragging stores a whole new order of the stations.
func TestProfileAutosaveAndOrder(t *testing.T) {
	a := newApp(t)
	session := signedIn(t, a)
	hx := map[string]string{"HX-Request": "true"}
	version := func() string {
		page := a.do(http.MethodGet, "/profile", nil, session, nil).Body.String()
		m := regexp.MustCompile(`id="version-profile" name="version" value="(\d+)"`).FindStringSubmatch(page)
		if m == nil {
			t.Fatalf("no version input: %s", page)
		}
		return m[1]
	}
	v := version()
	form := url.Values{"version": {v}, "first_name": {"Max"}, "last_name": {"Mustermann"}, "email": {"max.mustermann@example.org"},
		"city": {"Berlin"}}
	rec := a.do(http.MethodPost, "/profile/autosave", form, session, hx)
	if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, `data-save-state="saved"`) ||
		!strings.Contains(body, `id="version-profile"`) || !strings.Contains(body, "hx-swap-oob") {
		t.Fatalf("autosave %d: %s", rec.Code, body)
	}
	if got := version(); got == v {
		t.Fatalf("version stayed %s", got)
	}
	if page := a.do(http.MethodGet, "/profile", nil, session, nil).Body.String(); !strings.Contains(page, `value="Berlin"`) {
		t.Fatal("city not stored")
	}

	form.Set("version", version())
	form.Set("email", "not an address")
	rec = a.do(http.MethodPost, "/profile/autosave", form, session, hx)
	if body := rec.Body.String(); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, `data-save-state="invalid"`) ||
		!strings.Contains(body, "check:") {
		t.Fatalf("invalid email %d: %s", rec.Code, body)
	}
	// Another window saved meanwhile: the old version is refused.
	form.Set("email", "max.mustermann@example.org")
	form.Set("version", version())
	_ = a.do(http.MethodPost, "/profile/autosave", form, session, hx)
	if rec := a.do(http.MethodPost, "/profile/autosave", form, session, hx); rec.Code != http.StatusConflict {
		t.Errorf("stale version %d", rec.Code)
	}

	// Two positions; the second is edited by autosave, then dragged to the top.
	for _, title := range []string{"Junior Developer", "Senior Developer"} {
		if rec := a.do(http.MethodPost, "/profile/experiences", url.Values{"title": {title}, "company": {"Muster GmbH"},
			"start_date": {"2020"}}, session, nil); rec.Code != http.StatusSeeOther {
			t.Fatalf("add %s: %d", title, rec.Code)
		}
	}
	page := a.do(http.MethodGet, "/profile", nil, session, nil).Body.String()
	ids := regexp.MustCompile(`data-sort-item data-id="([a-f0-9-]+)"`).FindAllStringSubmatch(page, -1)
	if len(ids) != 2 {
		t.Fatalf("stations %v", ids)
	}
	first, second := ids[0][1], ids[1][1]
	rec = a.do(http.MethodPost, "/profile/experiences/"+second+"/autosave", url.Values{"title": {"Staff Developer"},
		"company": {"Muster GmbH"}, "start_date": {"2022"}}, session, hx)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="save-`+second+`"`) {
		t.Fatalf("station autosave %d: %s", rec.Code, rec.Body.String())
	}
	if rec := a.do(http.MethodPost, "/profile/experiences/order", url.Values{"id": {second, first}}, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("order %d", rec.Code)
	}
	page = a.do(http.MethodGet, "/profile", nil, session, nil).Body.String()
	if i, j := strings.Index(page, `data-id="`+second+`"`), strings.Index(page, `data-id="`+first+`"`); i < 0 || j < 0 || i > j {
		t.Fatal("order not stored")
	}
	if !strings.Contains(page, `value="Staff Developer"`) {
		t.Fatal("station autosave not stored")
	}
	// An order that leaves an entry out changes nothing.
	_ = a.do(http.MethodPost, "/profile/experiences/order", url.Values{"id": {first}}, session, nil)
	page = a.do(http.MethodGet, "/profile", nil, session, nil).Body.String()
	if strings.Index(page, `data-id="`+second+`"`) > strings.Index(page, `data-id="`+first+`"`) {
		t.Fatal("a partial order was stored")
	}
}
