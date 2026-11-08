//go:build browser

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/1xxn/getafjob/internal/runner/browser"
	"github.com/1xxn/getafjob/internal/runner/executor"
)

// site serves the fixtures and records POST /submit.
type site struct {
	*httptest.Server
	mu   sync.Mutex
	subs []map[string]any
}

func newSite(t *testing.T) *site {
	t.Helper()
	dir := os.Getenv("GETAFJOB_FIXTURES_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "testdata", "fixtures", "site")
	}
	s := &site{}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.Dir(dir)))
	mux.HandleFunc("POST /submit", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.subs = append(s.subs, body)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *site) submissions() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.subs)
}

// coord records what the engine reports.
type coord struct {
	mu        sync.Mutex
	calls     []string
	refuse    error
	asked     []Question
	confirmed bool
}

func (c *coord) log(s string) { c.mu.Lock(); c.calls = append(c.calls, s); c.mu.Unlock() }

func (c *coord) Checkpoint(_ context.Context, cp Checkpoint) error {
	if len(cp.Screenshot) == 0 {
		return errors.New("checkpoint without screenshot")
	}
	c.log("checkpoint:" + string(cp.Step))
	return nil
}
func (c *coord) Ask(_ context.Context, qs []Question) error { c.asked = qs; c.log("ask"); return nil }
func (c *coord) BeginSubmit(context.Context) error          { c.log("begin_submit"); return c.refuse }
func (c *coord) Submitted(_ context.Context, confirmed bool, _ browser.Effect) error {
	c.confirmed = confirmed
	c.log("submitted")
	return nil
}
func (c *coord) Park(_ context.Context, reason string, _ []byte) error {
	c.log("park:" + reason)
	return nil
}

// planner answers fields by label prefix; required fields it does not know become questions.
type planner map[string]executor.Value

func (pl planner) Plan(_ context.Context, st *browser.State) (executor.Plan, []Question, error) {
	plan := executor.Plan{Origins: []string{originOf(st.Frames[0].URL)}}
	var qs []Question
	for _, f := range st.Fields() {
		if IsSecurityCode(f) {
			if strings.TrimSpace(f.Value) == "" {
				qs = append(qs, Question{Kind: "security_code", FieldID: f.ID, Label: f.Label})
			}
			continue
		}
		matched := false
		for prefix, v := range pl {
			if strings.HasPrefix(f.Label, prefix) {
				v.FieldID, v.Label = f.ID, f.Label
				plan.Values = append(plan.Values, v)
				matched = true
				break
			}
		}
		if !matched && f.Required {
			qs = append(qs, Question{Kind: "field", FieldID: f.ID, Label: f.Label})
		}
	}
	return plan, qs, nil
}

func originOf(raw string) string {
	u, _ := url.Parse(raw)
	return u.Scheme + "://" + u.Host
}

type run struct {
	site  *site
	page  *rod.Page
	docs  *browser.Documents
	coord *coord
}

func start(t *testing.T, fixture string) *run {
	t.Helper()
	s := newSite(t)
	b, err := browser.Launch(context.Background(), browser.Options{Bin: os.Getenv("GETAFJOB_BROWSER_BIN")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	p, err := b.Page(proto.TargetCreateTarget{})
	if err != nil {
		t.Fatal(err)
	}
	p = p.Timeout(90 * time.Second)
	if err := p.Navigate(s.URL + "/" + fixture); err != nil {
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
	return &run{site: s, page: p, docs: docs, coord: &coord{}}
}

func (r *run) engine(pl planner) *Engine {
	return &Engine{Exec: &executor.Executor{Docs: r.docs}, Planner: pl, Coord: r.coord, Wait: 5 * time.Second}
}

var cv = executor.Value{Files: []string{"Max_Mustermann_CV.pdf"}, Source: "document#0192f3a4-5b6c-7d8e-9f01-23456789abcd"}

func TestMultiPageApplication(t *testing.T) {
	r := start(t, "step1.html")
	pl := planner{
		"Vorname":           {Text: "Max", Source: "profile.personal.first_name"},
		"Nachname":          {Text: "Mustermann", Source: "profile.personal.last_name"},
		"E-Mail":            {Text: "max.mustermann@example.org", Source: "profile.personal.email"},
		"Telefonnummer":     {Text: "+49 30 1234567", Source: "profile.personal.phone"},
		"Lebenslauf":        cv,
		"Ich willige":       {Text: "true", Source: "answer#1"},
		"Ich möchte in den": {Text: "false", Source: "profile.talent_pool"},
	}
	if err := r.engine(pl).Run(t.Context(), r.page); err != nil {
		t.Fatalf("run: %v (calls %v)", err, r.coord.calls)
	}
	subs := r.site.submissions()
	if len(subs) != 1 || subs[0]["vorname"] != "Max" || subs[0]["tel"] != "+49 30 1234567" || subs[0]["cv"] != "Max_Mustermann_CV.pdf" ||
		subs[0]["datenschutz"] != true || subs[0]["talentpool"] != false {
		t.Fatalf("submissions = %v", subs)
	}
	if !r.coord.confirmed {
		t.Fatal("confirmation page not recognized")
	}
	begin := slices.Index(r.coord.calls, "begin_submit")
	if begin < 0 || slices.Index(r.coord.calls, "submitted") < begin {
		t.Fatalf("calls = %v", r.coord.calls)
	}
}

func TestSubmitUsesTheButtonInsideTheForm(t *testing.T) {
	r := start(t, "greenhouse_header_submit.html")
	pl := planner{
		"First Name": {Text: "Max", Source: "profile.personal.first_name"},
		"Last Name":  {Text: "Mustermann", Source: "profile.personal.last_name"},
		"Email":      {Text: "max.mustermann@example.org", Source: "profile.personal.email"},
		"Resume/CV":  cv,
	}
	if err := r.engine(pl).Run(t.Context(), r.page); err != nil {
		t.Fatalf("run: %v (calls %v)", err, r.coord.calls)
	}
	if subs := r.site.submissions(); len(subs) != 1 || subs[0]["resume"] != "Max_Mustermann_CV.pdf" {
		t.Fatalf("submissions = %v", subs)
	}
	if !r.coord.confirmed {
		t.Fatal("confirmation page not recognized")
	}
}

func TestNoClickWithoutRegisteredSubmit(t *testing.T) {
	r := start(t, "greenhouse_header_submit.html")
	r.coord.refuse = errors.New("already submitted")
	pl := planner{
		"First Name": {Text: "Max", Source: "profile.personal.first_name"},
		"Last Name":  {Text: "Mustermann", Source: "profile.personal.last_name"},
		"Email":      {Text: "max.mustermann@example.org", Source: "profile.personal.email"},
		"Resume/CV":  cv,
	}
	if err := r.engine(pl).Run(t.Context(), r.page); err == nil {
		t.Fatal("run went on without a registered submit")
	}
	time.Sleep(time.Second)
	if subs := r.site.submissions(); len(subs) != 0 {
		t.Fatalf("submitted anyway: %v", subs)
	}
}

func TestUnknownRequiredFieldAsksTheUser(t *testing.T) {
	r := start(t, "greenhouse_header_submit.html")
	pl := planner{"First Name": {Text: "Max", Source: "profile.personal.first_name"}}
	if err := r.engine(pl).Run(t.Context(), r.page); !errors.Is(err, ErrAsked) {
		t.Fatalf("run: %v", err)
	}
	if len(r.coord.asked) != 3 || slices.Contains(r.coord.calls, "begin_submit") {
		t.Fatalf("asked %v, calls %v", r.coord.asked, r.coord.calls)
	}
	if v := r.page.MustEval(`() => document.getElementById("first_name").value`).Str(); v != "" {
		t.Fatalf("filled before asking: %q", v)
	}
}

func TestPrepareReachesTheForm(t *testing.T) {
	cases := map[string]string{
		"posting.html":                  "/apply.html",                    // link opens a new tab with an embedded form
		"lazy_embed.html":               "/lazy_embed.html",               // lazy iframe below the fold
		"popup_posting.html":            "/apply.html",                    // self-closing popup, the opener moves on
		"cookie_posting.html":           "/greenhouse_header_submit.html", // cookie banner over the page
		"external_posting.html":         "/greenhouse_header_submit.html", // external link to the employer form
		"greenhouse_header_submit.html": "/greenhouse_header_submit.html",
	}
	for fixture, want := range cases {
		t.Run(fixture, func(t *testing.T) {
			r := start(t, fixture)
			p, err := r.engine(nil).Prepare(t.Context(), r.page)
			if err != nil {
				t.Fatalf("prepare: %v", err)
			}
			info, err := p.Info()
			if err != nil || !strings.HasSuffix(info.URL, want) {
				t.Fatalf("at %v, want %s", info, want)
			}
			if fixture == "cookie_posting.html" {
				if c := p.MustEval(`() => document.cookie`).Str(); c != "consent=necessary" {
					t.Fatalf("cookie = %q", c)
				}
			}
		})
	}
}

func TestPrepareGivesUpWithoutAForm(t *testing.T) {
	r := start(t, "clicks.html")
	if _, err := r.engine(nil).Prepare(t.Context(), r.page); !errors.Is(err, ErrNoForm) {
		t.Fatalf("prepare: %v", err)
	}
}

func TestSilentAndDeadSubmits(t *testing.T) {
	pl := planner{
		"First Name": {Text: "Max", Source: "profile.personal.first_name"},
		"Last Name":  {Text: "Mustermann", Source: "profile.personal.last_name"},
		"Email":      {Text: "max.mustermann@example.org", Source: "profile.personal.email"},
	}

	t.Run("silent submit records network effect without confirmation", func(t *testing.T) {
		r := start(t, "silent_submit.html")
		e := r.engine(pl)
		e.Wait = 2 * time.Second
		if err := e.Run(t.Context(), r.page); err != nil {
			t.Fatalf("run: %v (calls %v)", err, r.coord.calls)
		}
		if subs := r.site.submissions(); len(subs) != 1 || subs[0]["form"] != "silent_submit" {
			t.Fatalf("submissions = %v", subs)
		}
		if r.coord.confirmed || !slices.Contains(r.coord.calls, "submitted") {
			t.Fatalf("confirmed=%v, calls=%v", r.coord.confirmed, r.coord.calls)
		}
	})

	t.Run("dead submit retries once and parks", func(t *testing.T) {
		r := start(t, "dead_submit.html")
		e := r.engine(pl)
		e.Wait = 1500 * time.Millisecond
		if err := e.Run(t.Context(), r.page); !errors.Is(err, ErrParked) {
			t.Fatalf("run: %v (calls %v)", err, r.coord.calls)
		}
		if !slices.Contains(r.coord.calls, "park:submit had no effect after one retry") {
			t.Fatalf("calls = %v", r.coord.calls)
		}
		if clicks := r.page.MustEval(`() => window.deadSubmitClicks || 0`).Int(); clicks != 2 {
			t.Fatalf("deadSubmitClicks = %d, want 2 (initial + 1 retry)", clicks)
		}
	})
}

func TestSecurityCodeAlwaysGoesToHuman(t *testing.T) {
	r := start(t, "security_code.html")
	// Even when the planner has an answer for the security code label, the engine asks the human first.
	pl := planner{
		"First Name":    {Text: "Max", Source: "profile.personal.first_name"},
		"Last Name":     {Text: "Mustermann", Source: "profile.personal.last_name"},
		"Email":         {Text: "max.mustermann@example.org", Source: "profile.personal.email"},
		"Security code": {Text: "STALE999", Source: "answer#1"},
	}
	if err := r.engine(pl).Run(t.Context(), r.page); !errors.Is(err, ErrAsked) {
		t.Fatalf("first run: %v", err)
	}
	if len(r.coord.asked) != 1 || r.coord.asked[0].Kind != "security_code" {
		t.Fatalf("asked = %+v", r.coord.asked)
	}
	// Simulate the human entering the e-mail code in the remote browser during handoff.
	r.page.MustEval(`() => { document.getElementById("security_code").value = "73918462"; }`)
	delete(pl, "Security code")
	if err := r.engine(pl).Run(t.Context(), r.page); err != nil {
		t.Fatalf("second run after human code entry: %v (calls %v)", err, r.coord.calls)
	}
	subs := r.site.submissions()
	if len(subs) != 1 || subs[0]["security_code"] != "73918462" {
		t.Fatalf("submissions = %v", subs)
	}
}

func TestCaptchaCountsAsSolvedOnlyWithToken(t *testing.T) {
	pl := planner{
		"First Name": {Text: "Max", Source: "profile.personal.first_name"},
		"Last Name":  {Text: "Mustermann", Source: "profile.personal.last_name"},
		"Email":      {Text: "max.mustermann@example.org", Source: "profile.personal.email"},
	}

	t.Run("turnstile requires token not just checkbox", func(t *testing.T) {
		r := start(t, "turnstile.html")
		r.page.MustEval(`() => { document.getElementById("cf-check").checked = true; }`)
		if err := r.engine(pl).Run(t.Context(), r.page); !errors.Is(err, ErrAsked) {
			t.Fatalf("without token: %v", err)
		}
		if len(r.coord.asked) != 1 || r.coord.asked[0].Kind != "captcha" {
			t.Fatalf("asked = %+v", r.coord.asked)
		}
		r.page.MustEval(`() => { document.getElementById("cf-token").value = "XXXX.DUMMY.TOKEN.XXXX"; }`)
		if err := r.engine(pl).Run(t.Context(), r.page); err != nil {
			t.Fatalf("with token: %v (calls %v)", err, r.coord.calls)
		}
		if subs := r.site.submissions(); len(subs) != 1 || subs[0]["turnstile"] != "XXXX.DUMMY.TOKEN.XXXX" {
			t.Fatalf("submissions = %v", subs)
		}
	})

	t.Run("recaptcha test key requires token", func(t *testing.T) {
		r := start(t, "recaptcha_testkey.html")
		if err := r.engine(pl).Run(t.Context(), r.page); !errors.Is(err, ErrAsked) {
			t.Fatalf("without token: %v", err)
		}
		if len(r.coord.asked) != 1 || r.coord.asked[0].Kind != "captcha" {
			t.Fatalf("asked = %+v", r.coord.asked)
		}
		r.page.MustEval(`() => { document.getElementById("g-recaptcha-response").value = "03AGdBq24PBCbwiDRaS_test_token"; }`)
		if err := r.engine(pl).Run(t.Context(), r.page); err != nil {
			t.Fatalf("with token: %v (calls %v)", err, r.coord.calls)
		}
		if subs := r.site.submissions(); len(subs) != 1 || subs[0]["recaptcha"] != "03AGdBq24PBCbwiDRaS_test_token" {
			t.Fatalf("submissions = %v", subs)
		}
	})
}
