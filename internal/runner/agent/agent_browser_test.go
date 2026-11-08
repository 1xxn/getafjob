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

	"connectrpc.com/connect"
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

func (g *gateway) Handoff(ctx context.Context, r *connect.Request[runnerv1.HandoffRequest]) (*connect.Response[runnerv1.HandoffResponse], error) {
	if err := check(r.Header()); err != nil {
		return nil, err
	}
	if g.handoff == nil {
		return g.UnimplementedRunnerServiceHandler.Handoff(ctx, r)
	}
	res, err := g.handoff(r.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (g *gateway) log(s string) { g.mu.Lock(); g.calls = append(g.calls, s); g.mu.Unlock() }

func check(h http.Header) error {
	if h.Get("Authorization") != "Bearer "+token {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("token"))
	}
	return nil
}

func (g *gateway) FetchDocument(_ context.Context, r *connect.Request[runnerv1.FetchDocumentRequest]) (*connect.Response[runnerv1.FetchDocumentResponse], error) {
	if err := check(r.Header()); err != nil {
		return nil, err
	}
	g.log("document")
	return connect.NewResponse(&runnerv1.FetchDocumentResponse{Content: []byte("%PDF-1.4\n")}), nil
}

func (g *gateway) Checkpoint(_ context.Context, r *connect.Request[runnerv1.CheckpointRequest]) (*connect.Response[runnerv1.CheckpointResponse], error) {
	if err := check(r.Header()); err != nil {
		return nil, err
	}
	if !json.Valid(r.Msg.GetDataJson()) {
		return nil, errors.New("invalid json")
	}
	g.mu.Lock()
	if len(r.Msg.GetScreenshot()) > 0 {
		g.shots++
	}
	g.mu.Unlock()
	g.log("checkpoint:" + r.Msg.GetStep())
	return connect.NewResponse(&runnerv1.CheckpointResponse{}), nil
}

func (g *gateway) Ask(_ context.Context, r *connect.Request[runnerv1.AskRequest]) (*connect.Response[runnerv1.AskResponse], error) {
	if err := check(r.Header()); err != nil {
		return nil, err
	}
	g.asked = r.Msg.GetQuestions()
	g.log("ask")
	return connect.NewResponse(&runnerv1.AskResponse{}), nil
}

func (g *gateway) BeginSubmit(_ context.Context, r *connect.Request[runnerv1.BeginSubmitRequest]) (*connect.Response[runnerv1.BeginSubmitResponse], error) {
	if err := check(r.Header()); err != nil {
		return nil, err
	}
	g.log("begin_submit")
	return connect.NewResponse(&runnerv1.BeginSubmitResponse{}), nil
}

func (g *gateway) Submitted(_ context.Context, r *connect.Request[runnerv1.SubmittedRequest]) (*connect.Response[runnerv1.SubmittedResponse], error) {
	if err := check(r.Header()); err != nil {
		return nil, err
	}
	state := "unconfirmed"
	if r.Msg.GetConfirmed() {
		state = "confirmed"
	}
	g.log("submitted:" + state)
	return connect.NewResponse(&runnerv1.SubmittedResponse{}), nil
}

func (g *gateway) Park(_ context.Context, r *connect.Request[runnerv1.ParkRequest]) (*connect.Response[runnerv1.ParkResponse], error) {
	if err := check(r.Header()); err != nil {
		return nil, err
	}
	g.log("park:" + r.Msg.GetReason())
	return connect.NewResponse(&runnerv1.ParkResponse{}), nil
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
	gmux := http.NewServeMux()
	gmux.Handle(runnerv1connect.NewRunnerServiceHandler(g))
	gs := httptest.NewServer(gmux)
	t.Cleanup(gs.Close)
	a := &Agent{Client: runnerv1connect.NewRunnerServiceClient(gs.Client(), gs.URL), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
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
