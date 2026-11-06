//go:build browser

package planner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/1xxn/getafjob/internal/runner/browser"
	"github.com/1xxn/getafjob/internal/runner/engine"
	"github.com/1xxn/getafjob/internal/runner/executor"
)

type coord struct {
	asked     []engine.Question
	submitted bool
}

func (c *coord) Checkpoint(context.Context, engine.Checkpoint) error { return nil }
func (c *coord) Ask(_ context.Context, qs []engine.Question) error   { c.asked = qs; return nil }
func (c *coord) BeginSubmit(context.Context) error                   { return nil }
func (c *coord) Submitted(context.Context, bool, browser.Effect) error {
	c.submitted = true
	return nil
}
func (c *coord) Park(_ context.Context, reason string, _ []byte) error {
	return errors.New("parked: " + reason)
}

// TestPlannerFillsAMultiPageForm runs the engine with the real planner: the first run asks for the privacy
// consent, the answer goes into the answer bank, the second run submits.
func TestPlannerFillsAMultiPageForm(t *testing.T) {
	dir := os.Getenv("GETAFJOB_FIXTURES_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "testdata", "fixtures", "site")
	}
	var mu sync.Mutex
	var subs []map[string]any
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.Dir(dir)))
	mux.HandleFunc("POST /submit", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		subs = append(subs, b)
		mu.Unlock()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b, err := browser.Launch(context.Background(), browser.Options{Bin: os.Getenv("GETAFJOB_BROWSER_BIN")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close(context.Background()) }()
	docsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(docsDir, "Max_Mustermann_CV.pdf"), []byte("%PDF-1.4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	docs, err := browser.NewDocuments(docsDir)
	if err != nil {
		t.Fatal(err)
	}
	k := know
	k.Origins = []string{srv.URL}
	k.Language = "de"
	k.Profile = map[string]string{"personal.first_name": "Max", "personal.last_name": "Mustermann",
		"personal.email": "max.mustermann@example.org", "personal.phone": "+49 30 1234567", "preferences.talent_pool": "false"}
	k.Answers = nil

	run := func() (*coord, error) {
		p, err := b.Page(proto.TargetCreateTarget{})
		if err != nil {
			t.Fatal(err)
		}
		p = p.Timeout(90 * time.Second)
		if err := p.Navigate(srv.URL + "/step1.html"); err != nil {
			t.Fatal(err)
		}
		_ = p.WaitLoad()
		c := &coord{}
		e := &engine.Engine{Exec: &executor.Executor{Docs: docs}, Planner: &Planner{K: k}, Coord: c, Wait: 5 * time.Second}
		return c, e.Run(t.Context(), p)
	}
	c, err := run()
	if !errors.Is(err, engine.ErrAsked) || len(c.asked) != 1 {
		t.Fatalf("first run: %v, asked %+v", err, c.asked)
	}
	k.Answers = []Answer{{ID: "0192f3a4-5b6c-7d8e-9f01-000000000003", Question: c.asked[0].Label, Answer: "Ja"}}
	if c, err = run(); err != nil || !c.submitted {
		t.Fatalf("second run: %v, %+v", err, c)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(subs) != 1 || subs[0]["vorname"] != "Max" || subs[0]["datenschutz"] != true || subs[0]["talentpool"] != false ||
		subs[0]["cv"] != "Max_Mustermann_CV.pdf" {
		t.Fatalf("submissions = %v", subs)
	}
}
