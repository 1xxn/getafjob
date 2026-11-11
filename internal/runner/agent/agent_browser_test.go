//go:build browser

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/timestamppb"

	runnerv1 "github.com/1xxn/getafjob/internal/gen/proto/runner/v1"
	"github.com/1xxn/getafjob/internal/gen/proto/runner/v1/runnerv1connect"
	"github.com/1xxn/getafjob/internal/runner/browser"
)

const token = "run-token-0123456789abcdef0123456789abcdef0123"

// gateway is a fake gateway that records the run's calls.
type gateway struct {
	runnerv1connect.UnimplementedRunnerServiceHandler
	mu      sync.Mutex
	calls   []string
	asked   []*runnerv1.Question
	shots   int
	handoff func(*runnerv1.HandoffRequest) (*runnerv1.HandoffResponse, error)
}

func (g *gateway) Handoff(ctx context.Context, r *runnerv1.HandoffRequest) (*runnerv1.HandoffResponse, error) {
	if err := check(ctx); err != nil {
		return nil, err
	}
	if g.handoff == nil {
		return g.UnimplementedRunnerServiceHandler.Handoff(ctx, r)
	}
	res, err := g.handoff(r)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (g *gateway) log(s string) { g.mu.Lock(); g.calls = append(g.calls, s); g.mu.Unlock() }

func check(ctx context.Context) error {
	info, _ := connect.CallInfoForServerContext(ctx)
	var h *connect.Header
	if info != nil {
		h = info.RequestHeader()
	}
	if h == nil || h.Get("Authorization") != "Bearer "+token {
		return connect.NewError(connect.CodeUnauthenticated, "token")
	}
	return nil
}

func (g *gateway) FetchDocument(ctx context.Context, r *runnerv1.FetchDocumentRequest) (*runnerv1.FetchDocumentResponse, error) {
	if err := check(ctx); err != nil {
		return nil, err
	}
	g.log("document")
	return &runnerv1.FetchDocumentResponse{Content: []byte("%PDF-1.4\n")}, nil
}

func (g *gateway) Checkpoint(ctx context.Context, r *runnerv1.CheckpointRequest) (*runnerv1.CheckpointResponse, error) {
	if err := check(ctx); err != nil {
		return nil, err
	}
	if !json.Valid(r.GetDataJson()) {
		return nil, errors.New("invalid json")
	}
	g.mu.Lock()
	if len(r.GetScreenshot()) > 0 {
		g.shots++
	}
	g.mu.Unlock()
	g.log("checkpoint:" + r.GetStep())
	return &runnerv1.CheckpointResponse{}, nil
}

func (g *gateway) Ask(ctx context.Context, r *runnerv1.AskRequest) (*runnerv1.AskResponse, error) {
	if err := check(ctx); err != nil {
		return nil, err
	}
	g.asked = r.GetQuestions()
	g.log("ask")
	return &runnerv1.AskResponse{}, nil
}

func (g *gateway) BeginSubmit(ctx context.Context, r *runnerv1.BeginSubmitRequest) (*runnerv1.BeginSubmitResponse, error) {
	if err := check(ctx); err != nil {
		return nil, err
	}
	g.log("begin_submit")
	return &runnerv1.BeginSubmitResponse{}, nil
}

func (g *gateway) Submitted(ctx context.Context, r *runnerv1.SubmittedRequest) (*runnerv1.SubmittedResponse, error) {
	if err := check(ctx); err != nil {
		return nil, err
	}
	state := "unconfirmed"
	if r.GetConfirmed() {
		state = "confirmed"
	}
	g.log("submitted:" + state)
	return &runnerv1.SubmittedResponse{}, nil
}

func (g *gateway) Park(ctx context.Context, r *runnerv1.ParkRequest) (*runnerv1.ParkResponse, error) {
	if err := check(ctx); err != nil {
		return nil, err
	}
	g.log("park:" + r.GetReason())
	return &runnerv1.ParkResponse{}, nil
}

func setup(t *testing.T) (*gateway, *Agent, string, func() []map[string]any) {
	t.Helper()
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
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)

	g := &gateway{}
	srv := connect.NewServer()
	runnerv1connect.RegisterRunnerServiceHandler(srv, g)
	gmux := http.NewServeMux()
	connecthttp.Mount(gmux, srv)
	gs := httptest.NewServer(gmux)
	t.Cleanup(gs.Close)
	client := connect.NewClient(connecthttp.NewTransport(gs.Client(), gs.URL))
	a := &Agent{Client: runnerv1connect.NewRunnerServiceClient(client), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Browser: browser.Options{Bin: os.Getenv("GETAFJOB_BROWSER_BIN")}, Wait: 5 * time.Second}
	return g, a, site.URL, func() []map[string]any { mu.Lock(); defer mu.Unlock(); return slices.Clone(subs) }
}

const cvID = "0192f3a4-5b6c-7d8e-9f01-0000000000c1"

func run(site string) *runnerv1.Run {
	return &runnerv1.Run{RunId: "r1", Token: token, ExpiresAt: timestamppb.New(time.Now().Add(10 * time.Minute)),
		StartUrl: site + "/posting.html", Origins: []string{site}, Language: "de",
		Profile: map[string]string{"personal.first_name": "Max", "personal.last_name": "Mustermann",
			"personal.email": "max.mustermann@example.org", "personal.phone": "+49 30 1234567", "preferences.talent_pool": "false"},
		Documents:    []*runnerv1.Document{{Id: cvID, FileName: "../Max_Mustermann_CV.pdf", Kind: "cv"}},
		CvDocumentId: cvID}
}

// From the posting (new tab, embedded form, two pages) to the submit, reported over the run token.
func TestRunFromPostingToSubmit(t *testing.T) {
	g, a, site, subs := setup(t)
	r := run(site)
	r.Answers = []*runnerv1.Answer{{Id: "0192f3a4-5b6c-7d8e-9f01-000000000003",
		Question: "Ich willige in die Verarbeitung meiner Daten gemäß Datenschutzerklärung ein*", Answer: "Ja"}}
	if err := a.RunOne(t.Context(), r); err != nil {
		t.Fatalf("run: %v (calls %v)", err, g.calls)
	}
	s := subs()
	if len(s) != 1 || s[0]["vorname"] != "Max" || s[0]["cv"] != "Max_Mustermann_CV.pdf" || s[0]["datenschutz"] != true {
		t.Fatalf("submissions = %v (calls %v)", s, g.calls)
	}
	begin, done := slices.Index(g.calls, "begin_submit"), slices.Index(g.calls, "submitted:confirmed")
	if !slices.Contains(g.calls, "document") || begin < 0 || done < begin || g.shots == 0 {
		t.Fatalf("calls = %v, screenshots %d", g.calls, g.shots)
	}
}

func TestRunAsksForUnknownFields(t *testing.T) {
	g, a, site, subs := setup(t)
	if err := a.RunOne(t.Context(), run(site)); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(g.asked) != 1 || slices.Contains(g.calls, "begin_submit") || len(subs()) != 0 {
		t.Fatalf("asked %v, calls %v", g.asked, g.calls)
	}
	// The known fields of the page are filled before the question (#196).
	ask := slices.Index(g.calls, "ask")
	extract := ask - 1
	for extract >= 0 && g.calls[extract] != "checkpoint:extract" {
		extract--
	}
	if !slices.Contains(g.calls[extract:ask], "checkpoint:execute") {
		t.Fatalf("no fill between the last extract and the ask: %v", g.calls)
	}
}

func TestRunHandoffResumesWithAnswerAndSubmits(t *testing.T) {
	g, a, site, subs := setup(t)
	g.handoff = func(req *runnerv1.HandoffRequest) (*runnerv1.HandoffResponse, error) {
		if !req.GetWait() {
			return &runnerv1.HandoffResponse{}, nil
		}
		return &runnerv1.HandoffResponse{Events: []*runnerv1.HandoffEvent{{
			Msg: &runnerv1.HandoffEvent_Resume{Resume: true},
			Answers: []*runnerv1.Answer{{Id: "0192f3a4-5b6c-7d8e-9f01-000000000003",
				Question: "Ich willige in die Verarbeitung meiner Daten gemäß Datenschutzerklärung ein*", Answer: "Ja"}},
		}}}, nil
	}
	if err := a.RunOne(t.Context(), run(site)); err != nil {
		t.Fatalf("run: %v (calls %v)", err, g.calls)
	}
	if len(g.asked) != 1 || len(subs()) != 1 || !slices.Contains(g.calls, "submitted:confirmed") {
		t.Fatalf("asked %v, subs %v, calls %v", g.asked, subs(), g.calls)
	}
}

// The live view streams frames during a normal run, not only while the run waits for the user (#163).
func TestRunStreamsFramesWithoutHandoff(t *testing.T) {
	g, a, site, subs := setup(t)
	var mu sync.Mutex
	frames, done := 0, 0
	g.handoff = func(req *runnerv1.HandoffRequest) (*runnerv1.HandoffResponse, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(req.GetFrame()) > 0 {
			frames++
		}
		if req.GetDone() {
			done++
		}
		if req.GetWait() { // the fixture asks for the privacy consent; answer it like a user would
			return &runnerv1.HandoffResponse{Events: []*runnerv1.HandoffEvent{{
				Msg: &runnerv1.HandoffEvent_Resume{Resume: true},
				Answers: []*runnerv1.Answer{{Id: "0192f3a4-5b6c-7d8e-9f01-000000000003",
					Question: "Ich willige in die Verarbeitung meiner Daten gemäß Datenschutzerklärung ein*", Answer: "Ja"}},
			}}}, nil
		}
		return &runnerv1.HandoffResponse{}, nil
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if err := a.RunOne(ctx, run(site)); err != nil {
		t.Fatalf("run: %v (calls %v)", err, g.calls)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(subs()) != 1 || frames == 0 || done != 1 {
		t.Fatalf("subs %d, frames %d, done %d, calls %v", len(subs()), frames, done, g.calls)
	}
	t.Logf("%d frames during the run", frames)
}

// leasing lets the fake gateway hand out queued runs to Agent.Run.
type leasing struct {
	*gateway
	mu     sync.Mutex
	queue  []*runnerv1.Run
	leased int
}

func (l *leasing) Register(context.Context, *runnerv1.RegisterRequest) (*runnerv1.RegisterResponse, error) {
	return &runnerv1.RegisterResponse{RunnerId: "runner-test"}, nil
}

func (l *leasing) Lease(context.Context, *runnerv1.LeaseRequest) (*runnerv1.LeaseResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.queue) == 0 {
		return &runnerv1.LeaseResponse{}, nil
	}
	r := l.queue[0]
	l.queue = l.queue[1:]
	l.leased++
	return &runnerv1.LeaseResponse{Run: r}, nil
}

// A run waiting for the user frees its slot; open browsers stay below twice the slots (#165).
func TestWaitingRunsDoNotBlockOthers(t *testing.T) {
	g, a, site, subs := setup(t)
	release := make(chan struct{})
	g.handoff = func(req *runnerv1.HandoffRequest) (*runnerv1.HandoffResponse, error) {
		if !req.GetWait() {
			return &runnerv1.HandoffResponse{}, nil
		}
		select {
		case <-release:
			return &runnerv1.HandoffResponse{Events: []*runnerv1.HandoffEvent{{
				Msg: &runnerv1.HandoffEvent_Resume{Resume: true},
				Answers: []*runnerv1.Answer{{Id: "0192f3a4-5b6c-7d8e-9f01-000000000003",
					Question: "Ich willige in die Verarbeitung meiner Daten gemäß Datenschutzerklärung ein*", Answer: "Ja"}},
			}}}, nil
		case <-time.After(time.Second):
			return &runnerv1.HandoffResponse{}, nil
		}
	}
	l := &leasing{gateway: g}
	for i := range 3 {
		r := run(site)
		r.RunId = "r" + string(rune('1'+i))
		l.queue = append(l.queue, r)
	}
	srv := connect.NewServer()
	runnerv1connect.RegisterRunnerServiceHandler(srv, l)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, srv)
	gs := httptest.NewServer(mux)
	t.Cleanup(gs.Close)
	a.Client = runnerv1connect.NewRunnerServiceClient(connect.NewClient(connecthttp.NewTransport(gs.Client(), gs.URL)))
	a.Parallel, a.Poll = 1, 100*time.Millisecond

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	asks := func() int {
		g.mu.Lock()
		defer g.mu.Unlock()
		return countCalls(g.calls, "ask")
	}
	waitFor(t, 60*time.Second, func() bool { return asks() == 2 })
	time.Sleep(2 * time.Second)
	l.mu.Lock()
	leased := l.leased
	l.mu.Unlock()
	if leased != 2 || asks() != 2 {
		t.Fatalf("with one slot, two runs wait for the user (cap 2 browsers): leased %d, asks %d", leased, asks())
	}
	close(release)
	waitFor(t, 90*time.Second, func() bool { return len(subs()) == 3 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("agent: %v", err)
	}
}

func countCalls(calls []string, name string) int {
	n := 0
	for _, c := range calls {
		if c == name {
			n++
		}
	}
	return n
}

func waitFor(t *testing.T, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached in time")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
