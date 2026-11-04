//go:build browser

package browser

// Phase 0 spike (#9): go-rod against the hardest form fixtures. Results: docs/adr/0011-browser-automation-go-rod.md.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// site serves testdata/fixtures/site and records POST /submit bodies.
type site struct {
	*httptest.Server
	mu   sync.Mutex
	subs []map[string]any
}

func fixturesDir() string {
	if d := os.Getenv("GETAFJOB_FIXTURES_DIR"); d != "" {
		return d
	}
	return filepath.Join("..", "..", "..", "testdata", "fixtures", "site")
}

func newSite(t *testing.T) *site {
	t.Helper()
	s := &site{}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.Dir(fixturesDir())))
	mux.HandleFunc("POST /submit", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.subs = append(s.subs, body)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	// Embeds a page of another site (other host name → out-of-process iframe with site isolation).
	mux.HandleFunc("GET /oopif", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `<!doctype html><title>OOPIF</title><h1>Careers</h1>
<iframe src=%q style="width:100%%;height:600px"></iframe>`, r.URL.Query().Get("src"))
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// submission waits for the next recorded submission.
func (s *site) submission(t *testing.T) map[string]any {
	t.Helper()
	var got map[string]any
	eventually(t, 10*time.Second, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.subs) == 0 {
			return errors.New("no submission")
		}
		got = s.subs[0]
		return nil
	})
	return got
}

func eventually(t *testing.T, timeout time.Duration, f func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		err := f()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type spike struct {
	t    *testing.T
	b    *Browser
	page *rod.Page
	site *site
	cv   string
}

func newSpike(t *testing.T, fixture string) *spike {
	t.Helper()
	s := &spike{t: t, b: launch(t, Options{}), site: newSite(t)}
	page, err := s.b.Page(proto.TargetCreateTarget{})
	if err != nil {
		t.Fatal(err)
	}
	s.page = page.Timeout(30 * time.Second)
	s.cv = filepath.Join(t.TempDir(), "Max_Mustermann_CV.pdf")
	if err := os.WriteFile(s.cv, []byte("%PDF-1.4\n% fictional CV\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.open(s.site.URL + "/" + fixture)
	return s
}

func (s *spike) open(url string) {
	s.t.Helper()
	if err := s.page.Navigate(url); err != nil {
		s.t.Fatal(err)
	}
	if err := s.page.WaitLoad(); err != nil {
		s.t.Fatal(err)
	}
}

func (s *spike) extract() *State {
	s.t.Helper()
	st, err := Extract(s.page)
	if err != nil {
		s.t.Fatal(err)
	}
	return st
}

func (s *spike) field(st *State, kind, label string) Field {
	s.t.Helper()
	for _, f := range st.Fields() {
		if (kind == "" || f.Kind == kind) && strings.HasPrefix(f.Label, label) {
			return f
		}
	}
	s.t.Fatalf("no %s field %q in %+v", kind, label, st.Fields())
	return Field{}
}

func (s *spike) button(st *State, text string, inForm bool) Button {
	s.t.Helper()
	for _, b := range st.Buttons() {
		if b.Text == text && (b.Form != "") == inForm {
			return b
		}
	}
	s.t.Fatalf("no button %q (in form: %v) in %+v", text, inForm, st.Buttons())
	return Button{}
}

func (s *spike) el(st *State, id string) *rod.Element {
	s.t.Helper()
	el, err := st.Element(id)
	if err != nil {
		s.t.Fatal(err)
	}
	return el.Timeout(15 * time.Second)
}

func (s *spike) fill(st *State, label, value string) {
	s.t.Helper()
	if err := s.el(st, s.field(st, "", label).ID).Input(value); err != nil {
		s.t.Fatalf("fill %q: %v", label, err)
	}
}

func (s *spike) click(st *State, id string) {
	s.t.Helper()
	if err := s.el(st, id).Click(proto.InputMouseButtonLeft, 1); err != nil {
		s.t.Fatalf("click %s: %v", id, err)
	}
}

// clickAt clicks where the element is, like a person would, also when another element lies on top
// (react-select's placeholder covers its input).
func (s *spike) clickAt(el *rod.Element) {
	s.t.Helper()
	if err := el.ScrollIntoView(); err != nil {
		s.t.Fatal(err)
	}
	shape, err := el.Shape()
	if err != nil {
		s.t.Fatal(err)
	}
	pt := shape.OnePointInside()
	if pt == nil {
		s.t.Fatal("element has no visible area")
	}
	if err := s.page.Mouse.MoveTo(*pt); err != nil {
		s.t.Fatal(err)
	}
	if err := s.page.Mouse.Click(proto.InputMouseButtonLeft, 1); err != nil {
		s.t.Fatal(err)
	}
}

// pickOption clicks the option whose text matches exactly; options can be re-rendered while we look, so retry.
func (s *spike) pickOption(text string) {
	s.t.Helper()
	rx := "^" + regexp.QuoteMeta(text) + "$"
	eventually(s.t, 8*time.Second, func() error {
		opt, err := s.page.Sleeper(rod.NotFoundSleeper).ElementR(`[role="option"]`, rx)
		if err != nil {
			return err
		}
		return opt.Timeout(2*time.Second).Click(proto.InputMouseButtonLeft, 1)
	})
}

func TestSpikeHeaderButtonIsNotTheSubmit(t *testing.T) {
	s := newSpike(t, "greenhouse_header_submit.html")
	st := s.extract()
	header := s.button(st, "Submit application", false)
	submit := s.button(st, "Submit application", true)
	if header.ID == submit.ID || submit.Kind != "submit" {
		t.Fatalf("header %+v, submit %+v", header, submit)
	}
	s.fill(st, "First Name", "Max")
	s.fill(st, "Last Name", "Mustermann")
	s.fill(st, "Email", "max.mustermann@example.com")
	if err := s.el(st, s.field(st, "file", "Resume/CV").ID).SetFiles([]string{s.cv}); err != nil {
		t.Fatal(err)
	}
	s.click(st, submit.ID)
	sub := s.site.submission(t)
	if sub["form"] != "greenhouse_header_submit" || sub["resume"] != "Max_Mustermann_CV.pdf" || sub["email"] != "max.mustermann@example.com" {
		t.Fatalf("submission = %v", sub)
	}
}

func TestSpikeReactSelectPicksAndVerifies(t *testing.T) {
	s := newSpike(t, "greenhouse_select.html")
	st := s.extract()
	s.fill(st, "Vorname", "Max")
	s.fill(st, "Nachname", "Mustermann")
	s.fill(st, "E-Mail", "max.mustermann@example.com")
	choose := func(label, typed, option, shown string) {
		f := s.field(st, "combobox", label)
		el := s.el(st, f.ID)
		s.clickAt(el)
		if typed != "" {
			if err := el.Input(typed); err != nil {
				t.Fatal(err)
			}
		}
		s.pickOption(option)
		// Verify with a fresh extraction: the chosen value must show up as the field's value.
		eventually(t, 3*time.Second, func() error {
			if got := s.field(s.extract(), "combobox", label).Value; got != shown {
				return fmt.Errorf("%s shows %q, want %q", label, got, shown)
			}
			return nil
		})
	}
	choose("Land", "", "Deutschland +49", "+49")
	choose("Falls du nicht in Berlin lebst", "", "Yes", "Yes")
	choose("Welche Kommunikationssprache", "Germ", "German", "German")
	choose("Standort", "Berl", "Berlin, Germany", "Berlin, Germany") // async suggestions, rendered twice
	s.click(st, s.button(st, "Bewerbung einreichen", true).ID)
	sub := s.site.submission(t)
	if sub["relocate"] != "Yes" || sub["language"] != "German" || sub["location"] != "Berlin, Germany" {
		t.Fatalf("submission = %v", sub)
	}
}

func TestSpikeListboxButtonsAreSelects(t *testing.T) {
	s := newSpike(t, "workday_listbox.html")
	st := s.extract()
	for _, b := range st.Buttons() {
		if b.Text == "Select One" {
			t.Fatalf("listbox button reported as button: %+v", b)
		}
	}
	for label, option := range map[string]string{"State": "Berlin", "Phone": "Mobile"} {
		s.click(st, s.field(st, "combobox", label).ID)
		s.pickOption(option)
	}
	again := s.extract()
	if v := s.field(again, "combobox", "State").Value; v != "Berlin" {
		t.Errorf("State = %q", v)
	}
	if v := s.field(again, "combobox", "Phone").Value; v != "Mobile" {
		t.Errorf("Phone = %q", v)
	}
}

func TestSpikeDropzoneTakesFilesOnlyFromItsChooser(t *testing.T) {
	s := newSpike(t, "dropzone.html")
	st := s.extract()
	input := s.el(st, s.field(st, "file", "").ID)
	zone, err := input.Parent()
	if err != nil {
		t.Fatal(err)
	}
	setFiles, err := s.page.HandleFileDialog()
	if err != nil {
		t.Fatal(err)
	}
	s.clickAt(zone)
	if err := setFiles([]string{s.cv}); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, func() error {
		label, err := s.page.Element("#label")
		if err != nil {
			return err
		}
		if txt, _ := label.Text(); txt != "Max_Mustermann_CV.pdf" {
			return fmt.Errorf("label = %q", txt)
		}
		return nil
	})
	if visible, _ := s.page.MustElement("#confirm").Visible(); !visible {
		t.Fatal("confirm button not shown")
	}
}

func TestSpikeLazyIframeBelowTheFold(t *testing.T) {
	s := newSpike(t, "lazy_embed.html")
	if _, err := s.page.Eval(`() => window.scrollTo(0, document.body.scrollHeight)`); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, func() error {
		st, err := Extract(s.page)
		if err != nil {
			return err
		}
		for _, f := range st.Fields() {
			if strings.HasPrefix(f.Label, "First Name") && strings.HasPrefix(f.ID, "1-") {
				return nil
			}
		}
		return fmt.Errorf("form in lazy iframe not readable yet (%d frames)", len(st.Frames))
	})
}

func TestSpikeSelfClosingPopup(t *testing.T) {
	s := newSpike(t, "popup_posting.html")
	st := s.extract()
	waitPopup := s.page.WaitOpen()
	s.click(st, s.button(st, "Apply now", false).ID)
	popup, err := waitPopup()
	if err != nil {
		t.Fatalf("popup: %v", err)
	}
	eventually(t, 10*time.Second, func() error {
		info, err := s.page.Info()
		if err != nil {
			return err
		}
		if !strings.HasSuffix(info.URL, "/apply.html") {
			return fmt.Errorf("still at %s", info.URL)
		}
		if _, err := popup.Info(); err == nil {
			return errors.New("popup still open")
		}
		return nil
	})
	if err := s.page.WaitLoad(); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, func() error {
		st, err := Extract(s.page)
		if err != nil {
			return err
		}
		for _, f := range st.Fields() {
			if strings.HasPrefix(f.Label, "Vorname") {
				return nil
			}
		}
		return errors.New("form not found after popup closed")
	})
}

func TestSpikePressedToggleIsReported(t *testing.T) {
	s := newSpike(t, "ashby_yesno.html")
	st := s.extract()
	s.fill(st, "Name", "Max Mustermann")
	s.fill(st, "Email", "max.mustermann@example.com")
	yes := s.button(st, "Yes", false)
	if yes.Pressed {
		t.Fatal("pressed before click")
	}
	s.click(st, yes.ID)
	if !s.button(s.extract(), "Yes", false).Pressed {
		t.Fatal("pressed state not reported after click; the engine could click it off again")
	}
	s.click(st, s.button(st, "Submit Application", false).ID)
	if sub := s.site.submission(t); sub["regulated"] != "yes" {
		t.Fatalf("submission = %v", sub)
	}
}

func TestSpikeShadowDOMInsideIframe(t *testing.T) {
	s := newSpike(t, "apply.html")
	var st *State
	eventually(t, 5*time.Second, func() error {
		st = s.extract()
		if len(st.Frames) < 2 || len(st.Frames[1].Fields) == 0 {
			return errors.New("iframe form not loaded")
		}
		return nil
	})
	tel := s.field(st, "text", "Telefonnummer")
	if !strings.HasPrefix(tel.ID, "1-") {
		t.Fatalf("shadow DOM field not in frame 1: %+v", tel)
	}
	s.fill(st, "Vorname", "Max")
	s.fill(st, "Nachname", "Mustermann")
	s.fill(st, "E-Mail-Adresse", "max.mustermann@example.com")
	s.fill(st, "Telefonnummer", "+49 000 0000000")
	s.click(st, s.button(st, "Weiter", true).ID)

	var step2 *State
	eventually(t, 10*time.Second, func() error {
		step2 = s.extract()
		for _, f := range step2.Fields() {
			if f.Kind == "file" {
				return nil
			}
		}
		return errors.New("step 2 not loaded")
	})
	if err := s.el(step2, s.field(step2, "file", "Lebenslauf").ID).SetFiles([]string{s.cv}); err != nil {
		t.Fatal(err)
	}
	s.click(step2, s.field(step2, "checkbox", "Ich willige").ID)
	s.click(step2, s.button(step2, "Bewerbung absenden", true).ID)
	sub := s.site.submission(t)
	if sub["tel"] != "+49 000 0000000" || sub["vorname"] != "Max" || sub["cv"] != "Max_Mustermann_CV.pdf" || sub["datenschutz"] != true {
		t.Fatalf("submission = %v", sub)
	}
}

func TestSpikeOutOfProcessIframe(t *testing.T) {
	s := newSpike(t, "posting.html")
	other := strings.Replace(s.site.URL, "127.0.0.1", "localhost", 1) + "/step1.html"
	s.open(s.site.URL + "/oopif?src=" + other)
	var st *State
	eventually(t, 10*time.Second, func() error {
		var err error
		st, err = Extract(s.page)
		if err != nil {
			return err
		}
		if len(st.Frames) < 2 || len(st.Frames[1].Fields) == 0 {
			return fmt.Errorf("cross-site iframe not readable (%d frames)", len(st.Frames))
		}
		return nil
	})
	targets, err := proto.TargetGetTargets{}.Call(s.b)
	if err != nil {
		t.Fatal(err)
	}
	oopif := false
	for _, ti := range targets.TargetInfos {
		if ti.Type == "iframe" && strings.Contains(ti.URL, "localhost") {
			oopif = true
		}
	}
	if !oopif {
		t.Fatal("iframe is not out-of-process: site isolation off?")
	}
	// No rod.Element in an OOPIF: focus over the frame's own CDP session, type on the top page.
	tel := s.field(st, "text", "Telefonnummer")
	if _, err := st.Element(tel.ID); !errors.Is(err, ErrOutOfProcess) {
		t.Fatalf("Element in OOPIF: %v", err)
	}
	if err := st.Focus(tel.ID); err != nil {
		t.Fatal(err)
	}
	if err := (proto.InputInsertText{Text: "+49 000 0000000"}).Call(s.page); err != nil {
		t.Fatal(err)
	}
	if v := s.field(s.extract(), "text", "Telefonnummer").Value; v != "+49 000 0000000" {
		t.Fatalf("value in OOPIF shadow field = %q", v)
	}
}
