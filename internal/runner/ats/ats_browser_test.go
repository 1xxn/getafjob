//go:build browser

package ats

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
	"github.com/1xxn/getafjob/internal/runner/planner"
)

type runCoord struct {
	asked     []engine.Question
	submitted bool
}

func (c *runCoord) Checkpoint(context.Context, engine.Checkpoint) error { return nil }
func (c *runCoord) Ask(_ context.Context, qs []engine.Question) error   { c.asked = qs; return nil }
func (c *runCoord) BeginSubmit(context.Context) error                   { return nil }
func (c *runCoord) Submitted(context.Context, bool, browser.Effect) error {
	c.submitted = true
	return nil
}
func (c *runCoord) Park(_ context.Context, reason string, _ []byte) error {
	return errors.New("parked: " + reason)
}

func TestAshbyAdapterSubmitsYesNoFormWithoutFormElement(t *testing.T) {
	dir := os.Getenv("GETAFJOB_FIXTURES_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "testdata", "fixtures", "site")
	}
	var mu sync.Mutex
	var subs []map[string]any
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.Dir(dir)))
	mux.HandleFunc("POST /submit", func(_ http.ResponseWriter, r *http.Request) {
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

	docs, err := browser.NewDocuments(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	k := planner.Knowledge{
		Origins:  []string{srv.URL},
		Language: "en",
		Profile: map[string]string{
			"personal.full_name": "Max Mustermann",
			"personal.email":     "max.mustermann@example.org",
		},
	}

	ad := Lookup(Ashby)
	tr, err := NewTracker()
	if err != nil {
		t.Fatal(err)
	}

	run := func() (*runCoord, error) {
		p, err := b.Page(proto.TargetCreateTarget{})
		if err != nil {
			t.Fatal(err)
		}
		p = p.Timeout(60 * time.Second)
		if err := p.Navigate(srv.URL + "/ashby_yesno.html"); err != nil {
			t.Fatal(err)
		}
		_ = p.WaitLoad()
		c := &runCoord{}
		e := ad.NewEngine(&executor.Executor{Docs: docs}, &planner.Planner{K: k}, c, 5*time.Second)
		return c, e.Run(t.Context(), p)
	}

	c, err := run()
	if !errors.Is(err, engine.ErrAsked) || len(c.asked) != 1 {
		t.Fatalf("first run: %v, asked %+v", err, c.asked)
	}
	tr.Record(t.Context(), ad.Name, OutcomeAsked)

	k.Answers = []planner.Answer{{
		ID:       "0192f3a4-5b6c-7d8e-9f01-000000000099",
		Question: c.asked[0].Label,
		Answer:   "Yes",
	}}
	if c, err = run(); err != nil || !c.submitted {
		t.Fatalf("second run: %v, %+v", err, c)
	}
	tr.Record(t.Context(), ad.Name, OutcomeSubmitted)

	mu.Lock()
	defer mu.Unlock()
	if len(subs) != 1 || subs[0]["name"] != "Max Mustermann" || subs[0]["regulated"] != "yes" {
		t.Fatalf("submissions = %v", subs)
	}
	if got := tr.Maturity(Ashby); got != 0.5 {
		t.Fatalf("maturity = %v, want 0.5", got)
	}
}
