//go:build browser

package executor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/1xxn/getafjob/internal/runner/browser"
)

func fixturesDir() string {
	if d := os.Getenv("GETAFJOB_FIXTURES_DIR"); d != "" {
		return d
	}
	return filepath.Join("..", "..", "..", "testdata", "fixtures", "site")
}

type form struct {
	t    *testing.T
	page *rod.Page
	url  string
	docs *browser.Documents
}

func open(t *testing.T) *form {
	t.Helper()
	srv := httptest.NewServer(http.FileServer(http.Dir(fixturesDir())))
	t.Cleanup(srv.Close)
	b, err := browser.Launch(context.Background(), browser.Options{Bin: os.Getenv("GETAFJOB_BROWSER_BIN")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	p, err := b.Page(proto.TargetCreateTarget{})
	if err != nil {
		t.Fatal(err)
	}
	ads := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) + "/newsletter.html"
	p = p.Timeout(60 * time.Second)
	if err := p.Navigate(srv.URL + "/executor.html?ads=" + ads); err != nil {
		t.Fatal(err)
	}
	if err := p.WaitLoad(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Max_Mustermann_CV.pdf"), []byte("%PDF-1.4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	docs, err := browser.NewDocuments(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &form{t: t, page: p, url: srv.URL, docs: docs}
}

func (f *form) state() *browser.State {
	f.t.Helper()
	var st *browser.State
	deadline := time.Now().Add(10 * time.Second)
	for {
		var err error
		st, err = browser.Extract(f.page)
		if err == nil && len(st.Frames) >= 2 && len(st.Frames[1].Fields) > 0 {
			return st
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("form not ready: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (f *form) id(st *browser.State, label string) string {
	f.t.Helper()
	for _, fd := range st.Fields() {
		if strings.HasPrefix(fd.Label, label) {
			return fd.ID
		}
	}
	f.t.Fatalf("no field %q", label)
	return ""
}

func (f *form) value(label string, text string, source string) Value {
	st := f.state()
	fd, _ := st.Field(f.id(st, label))
	return Value{FieldID: fd.ID, Label: fd.Label, Text: text, Source: source}
}

func TestFillEveryKindAndVerify(t *testing.T) {
	f := open(t)
	x := &Executor{Docs: f.docs}
	st := f.state()
	langs := f.value("Languages", "", "answer#7")
	langs.Options = []string{"Englisch", "German"}
	cv := f.value("CV", "", "document#0192f3a4-5b6c-7d8e-9f01-23456789abcd")
	cv.Files = []string{"Max_Mustermann_CV.pdf"}
	plan := Plan{Origins: []string{f.url}, Values: []Value{
		f.value("First name", "Max", "profile.personal.first_name"),
		f.value("Email", "max.mustermann@example.org", "profile.personal.email"),
		f.value("Short pitch", "Go and PostgreSQL for seven years.", "textblock#4"),
		f.value("Earliest start", "2027-01-01", "profile.work.earliest_start"),
		f.value("Country", "Deutschland", "answer#3"),
		f.value("I agree", "true", "answer#5"),
		f.value("Do you need a work permit?", "No", "profile.work.needs_sponsorship"),
		langs, cv,
	}}
	_, done, err := x.Fill(st, plan)
	if err != nil {
		t.Fatalf("fill: %v (done %+v)", err, done)
	}
	shown, kinds := map[string]string{}, map[string]string{}
	for _, d := range done {
		shown[strings.Fields(d.Label)[0]] = d.Shown
		kinds[strings.Fields(d.Label)[0]] = d.Kind
	}
	if kinds["I"] != "checkbox" || kinds["First"] != "text" {
		t.Errorf("kinds = %v", kinds)
	}
	want := map[string]string{"First": "Max", "Email": "max.mustermann@example.org", "Country": "Germany",
		"Do": "Nein", "Languages": "German; English", "I": "checked", "CV": "Max_Mustermann_CV.pdf",
		"Earliest": "2027-01-01"}
	for k, v := range want {
		if shown[k] != v {
			t.Errorf("%s shows %q, want %q", k, shown[k], v)
		}
	}
}

func TestFillRefuses(t *testing.T) {
	cases := []struct {
		name string
		edit func(f *form) Plan
		want error
	}{
		{"value without provenance", func(f *form) Plan {
			return Plan{Origins: []string{f.url}, Values: []Value{f.value("First name", "Max", "profile.personal.first_name"),
				f.value("Email", "guess@example.org", "")}}
		}, ErrNoProvenance},
		{"field label changed", func(f *form) Plan {
			v := f.value("First name", "Max", "profile.personal.first_name")
			v.Label = "Last name"
			return Plan{Origins: []string{f.url}, Values: []Value{v}}
		}, ErrFieldChanged},
		{"foreign origin", func(f *form) Plan {
			return Plan{Origins: []string{f.url}, Values: []Value{f.value("Newsletter email", "max@example.org", "profile.personal.email")}}
		}, ErrForeignOrigin},
		{"password", func(f *form) Plan {
			return Plan{Origins: []string{f.url}, Values: []Value{f.value("Password", "x", "answer#1")}}
		}, ErrRefused},
		{"too long", func(f *form) Plan {
			return Plan{Origins: []string{f.url}, Values: []Value{f.value("Short pitch", strings.Repeat("x", 41), "textblock#4")}}
		}, ErrTooLong},
		{"no matching option", func(f *form) Plan {
			return Plan{Origins: []string{f.url}, Values: []Value{f.value("Country", "Spain", "answer#3")}}
		}, ErrNoOption},
	}
	f := open(t)
	x := &Executor{Docs: f.docs}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := x.Fill(f.state(), tc.edit(f))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	// Nothing was filled by the refused plans (the provenance check runs before any field).
	if v := f.page.MustEval(`() => document.querySelector("[name=first]").value + "|" + document.querySelector("[name=email]").value`).Str(); v != "|" {
		t.Fatal(fmt.Errorf("fields were filled: %q", v))
	}
}

func extract(f *form) (*browser.State, error) { return browser.Extract(f.page) }

func mustButton(t *testing.T, st *browser.State, id string) browser.Button {
	t.Helper()
	b, ok := st.Button(id)
	if !ok {
		t.Fatalf("no button %s", id)
	}
	return b
}
