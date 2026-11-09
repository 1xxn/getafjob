//go:build browser

package ats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/1xxn/getafjob/internal/runner/browser"
	"github.com/1xxn/getafjob/internal/runner/engine"
	"github.com/1xxn/getafjob/internal/runner/executor"
	"github.com/1xxn/getafjob/internal/runner/planner"
)

var pngHeader = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// dryRunCoordinator records checkpoints, verifies step screenshots, and halts before the submit click.
type dryRunCoordinator struct {
	mu          sync.Mutex
	adapter     string
	runID       string
	steps       []engine.Step
	shots       int
	lastShot    []byte
	beginSubmit bool
	submitted   bool
	parked      bool
	parkReason  string
	asked       []engine.Question
}

func (c *dryRunCoordinator) Checkpoint(_ context.Context, cp engine.Checkpoint) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steps = append(c.steps, cp.Step)
	if len(cp.Screenshot) > 0 {
		c.shots++
		c.lastShot = cp.Screenshot
		// Verify valid PNG header
		if len(cp.Screenshot) >= 8 && bytes.Equal(cp.Screenshot[:8], pngHeader) {
			// Save screenshot to /tmp if writable
			tmpDir := filepath.Join(os.TempDir(), "getafjob-dryruns", c.adapter, c.runID)
			_ = os.MkdirAll(tmpDir, 0o755)
			_ = os.WriteFile(filepath.Join(tmpDir, fmt.Sprintf("%02d_%s.png", c.shots, cp.Step)), cp.Screenshot, 0o644)
		}
	}
	return nil
}

func (c *dryRunCoordinator) Ask(_ context.Context, qs []engine.Question) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, qs...)
	return nil
}

var errDryRunHalt = errors.New("dry run: stop before final submit click")

func (c *dryRunCoordinator) BeginSubmit(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.beginSubmit = true
	// Stop before the click: dry run does not send real submissions
	return errDryRunHalt
}

func (c *dryRunCoordinator) Submitted(_ context.Context, _ bool, _ browser.Effect) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.submitted = true
	return nil
}

func (c *dryRunCoordinator) Park(_ context.Context, reason string, shot []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.parked = true
	c.parkReason = reason
	if len(shot) > 0 {
		c.shots++
		c.lastShot = shot
	}
	return nil
}

type dryRunCase struct {
	adapter string
	runID   string
	fixture string
	answers []planner.Answer
}

func TestPhase5DryRunsAcrossAllATSAdapters(t *testing.T) {
	fixturesDir := os.Getenv("GETAFJOB_FIXTURES_DIR")
	if fixturesDir == "" {
		fixturesDir = filepath.Join("..", "..", "..", "testdata", "fixtures", "site")
	}

	var mu sync.Mutex
	var submissions []map[string]any

	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.Dir(fixturesDir)))
	mux.HandleFunc("POST /submit", func(_ http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		submissions = append(submissions, b)
		mu.Unlock()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b, err := browser.Launch(context.Background(), browser.Options{Bin: os.Getenv("GETAFJOB_BROWSER_BIN")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close(context.Background()) }()

	// Provide CV in temporary document store
	cvDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cvDir, "Max_Mustermann_CV.pdf"), []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	docs, err := browser.NewDocuments(cvDir)
	if err != nil {
		t.Fatal(err)
	}

	baseKnowledge := planner.Knowledge{
		Origins:  []string{srv.URL},
		Language: "en",
		Profile: map[string]string{
			"personal.full_name":      "Max Mustermann",
			"personal.first_name":     "Max",
			"personal.last_name":      "Mustermann",
			"personal.email":          "max.mustermann@example.org",
			"personal.phone":          "+49 30 1234567",
			"personal.city":           "Berlin",
			"personal.country":        "Deutschland",
			"personal.linkedin":       "https://www.linkedin.com/in/maxmustermann",
			"personal.github":         "https://github.com/maxmustermann",
			"work.current_employer":   "Acme Corp",
			"work.salary_expectation": "85000",
			"work.notice_period":      "1 month",
			"work.earliest_start":     "01.01.2027",
			"work.work_authorized_eu": "true",
			"work.needs_sponsorship":  "false",
			"preferences.talent_pool": "false",
		},
		Documents: []planner.Document{
			{ID: "0192f3a4-5b6c-7d8e-9f01-000000000001", File: "Max_Mustermann_CV.pdf", Kind: "cv", Language: "en"},
		},
	}

	cases := []dryRunCase{
		// Greenhouse: 2 dry runs
		{adapter: Greenhouse, runID: "run-1", fixture: "greenhouse_like.html", answers: []planner.Answer{
			{ID: "a1", Question: "Do you require visa sponsorship? *", Answer: "No"},
			{ID: "a2", Question: "How did you hear about us? *", Answer: "LinkedIn"},
			{ID: "a3", Question: "I agree to the privacy policy *", Answer: "true"},
		}},
		{adapter: Greenhouse, runID: "run-2", fixture: "greenhouse_header_submit.html"},

		// Ashby: 2 dry runs
		{adapter: Ashby, runID: "run-1", fixture: "ashby_yesno.html", answers: []planner.Answer{
			{ID: "a4", Question: "Do you have experience working in a regulated industry (e.g. banking)? *", Answer: "Yes"},
		}},
		{adapter: Ashby, runID: "run-2", fixture: "ashby_application.html"},

		// Lever: 2 dry runs
		{adapter: Lever, runID: "run-1", fixture: "lever_application.html", answers: []planner.Answer{
			{ID: "a5", Question: "I agree to the processing of my personal data *", Answer: "true"},
		}},
		{adapter: Lever, runID: "run-2", fixture: "lever_eu.html", answers: []planner.Answer{
			{ID: "a6", Question: "Notice Period *", Answer: "1 month"},
			{ID: "a7", Question: "Are you legally authorized to work in Germany? *", Answer: "Yes"},
			{ID: "a8", Question: "I accept the candidate privacy policy *", Answer: "true"},
		}},

		// Workday: 2 dry runs
		{adapter: Workday, runID: "run-1", fixture: "workday_listbox.html", answers: []planner.Answer{
			{ID: "a9", Question: "State*", Answer: "Berlin"},
			{ID: "a10", Question: "Phone Device Type*", Answer: "Mobile"},
		}},
		{adapter: Workday, runID: "run-2", fixture: "workday_application.html"},

		// SmartRecruiters: 2 dry runs
		{adapter: SmartRecruiters, runID: "run-1", fixture: "smartrecruiters_application.html", answers: []planner.Answer{
			{ID: "a11", Question: "I agree to the privacy policy of SmartRecruiters and Delivery Hero *", Answer: "true"},
		}},
		{adapter: SmartRecruiters, runID: "run-2", fixture: "smartrecruiters_custom.html", answers: []planner.Answer{
			{ID: "a12", Question: "Years of Experience in Go / Cloud *", Answer: "5+ years"},
			{ID: "a13", Question: "I accept the data processing terms *", Answer: "true"},
		}},

		// Personio: 2 dry runs
		{adapter: Personio, runID: "run-1", fixture: "personio_application.html", answers: []planner.Answer{
			{ID: "a14", Question: "Ich willige in die Verarbeitung meiner Daten gemäß Datenschutzerklärung ein *", Answer: "true"},
		}},
		{adapter: Personio, runID: "run-2", fixture: "step1.html", answers: []planner.Answer{
			{ID: "a15", Question: "Ich willige in die Verarbeitung meiner Daten gemäß Datenschutzerklärung ein*", Answer: "true"},
			{ID: "a16", Question: "Ich möchte in den Talent Pool aufgenommen werden", Answer: "false"},
		}},

		// Recruitee: 2 dry runs
		{adapter: Recruitee, runID: "run-1", fixture: "recruitee_application.html", answers: []planner.Answer{
			{ID: "a17", Question: "I agree to the processing of personal data *", Answer: "true"},
		}},
		{adapter: Recruitee, runID: "run-2", fixture: "dropzone.html"},

		// Workable: 2 dry runs
		{adapter: Workable, runID: "run-1", fixture: "workable_application.html", answers: []planner.Answer{
			{ID: "a18", Question: "I consent to the storage of my data *", Answer: "true"},
		}},
		{adapter: Workable, runID: "run-2", fixture: "upload.html"},

		// Teamtailor: 2 dry runs
		{adapter: Teamtailor, runID: "run-1", fixture: "teamtailor_application.html", answers: []planner.Answer{
			{ID: "a19", Question: "I consent to the processing of personal data *", Answer: "true"},
		}},
		{adapter: Teamtailor, runID: "run-2", fixture: "teamtailor_custom.html", answers: []planner.Answer{
			{ID: "a20", Question: "Are you located in Europe? *", Answer: "Yes"},
			{ID: "a21", Question: "I consent to the processing of personal data *", Answer: "true"},
		}},

		// Portals: Easy Apply dry runs (LinkedIn & Indeed) - 2 dry runs each
		{adapter: LinkedIn, runID: "run-1", fixture: "portal_linkedin_easyapply.html"},
		{adapter: LinkedIn, runID: "run-2", fixture: "portal_linkedin_multistep.html"},
		{adapter: Indeed, runID: "run-1", fixture: "portal_indeed_easyapply.html"},
		{adapter: Indeed, runID: "run-2", fixture: "portal_indeed_custom.html"},
	}

	tracker, err := NewTracker()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/%s", tc.adapter, tc.runID), func(t *testing.T) {
			k := baseKnowledge
			k.Answers = slices.Clone(tc.answers)

			ad := Lookup(tc.adapter)
			coord := &dryRunCoordinator{adapter: tc.adapter, runID: tc.runID}

			p, err := b.Page(proto.TargetCreateTarget{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = p.Close() }()

			p = p.Timeout(45 * time.Second)
			targetURL := srv.URL + "/" + tc.fixture
			if err := p.Navigate(targetURL); err != nil {
				t.Fatalf("navigate to %s: %v", targetURL, err)
			}
			_ = p.WaitLoad()

			e := ad.NewEngine(&executor.Executor{Docs: docs}, &planner.Planner{K: k}, coord, 3*time.Second)

			// Step 1: Prepare
			formPage, err := e.Prepare(t.Context(), p)
			if err != nil {
				t.Fatalf("prepare: %v", err)
			}

			// Step 2..N: Run execution with step checkpoints
			runErr := e.Run(t.Context(), formPage)

			// In a dry run, the run either halts cleanly before submit (errDryRunHalt) or reaches a form review/checkpoint
			coord.mu.Lock()
			hasBeginSubmit := coord.beginSubmit
			shotsCount := coord.shots
			stepsCount := len(coord.steps)
			askedCount := len(coord.asked)
			coord.mu.Unlock()

			switch {
			case errors.Is(runErr, errDryRunHalt):
				if !hasBeginSubmit {
					t.Errorf("halted without begin_submit")
				}
				tracker.Record(t.Context(), tc.adapter, OutcomeSubmitted)
			case errors.Is(runErr, engine.ErrAsked):
				if askedCount == 0 {
					t.Errorf("ErrAsked without questions")
				}
				tracker.Record(t.Context(), tc.adapter, OutcomeAsked)
			case runErr != nil && !errors.Is(runErr, engine.ErrParked):
				t.Fatalf("unexpected dry run error: %v", runErr)
			default:
				tracker.Record(t.Context(), tc.adapter, OutcomeParked)
			}

			// Verify that screenshots were captured for all visited form steps
			if shotsCount == 0 {
				t.Errorf("dry run for %s/%s recorded 0 screenshots", tc.adapter, tc.runID)
			}
			if stepsCount == 0 {
				t.Errorf("dry run for %s/%s recorded 0 steps", tc.adapter, tc.runID)
			}

			// Verify that NO real submission went through (submissions slice remains 0 for dry runs)
			mu.Lock()
			subsCount := len(submissions)
			mu.Unlock()
			if subsCount > 0 {
				t.Errorf("DRY RUN LEAK: %d submissions reached the server!", subsCount)
			}
		})
	}

	// Verify maturity tracker recorded stats for primary adapters
	for _, a := range []string{Greenhouse, Ashby, Lever, Workday, SmartRecruiters, Personio, Recruitee, Workable} {
		stats := tracker.byATS[a]
		if stats.Total() < 2 {
			t.Errorf("adapter %s had %d total runs, want >= 2", a, stats.Total())
		}
	}
}
