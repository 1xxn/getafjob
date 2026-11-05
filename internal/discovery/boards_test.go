package discovery_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/discovery"
)

func hit(company, title string) discovery.Hit {
	return discovery.Hit{Company: company, Title: title, MatchKey: discovery.MatchKey(company, title)}
}

func TestGroupCandidates(t *testing.T) {
	hits := []discovery.Hit{
		hit("Acme GmbH", "Go Developer"), hit("Muster AG", "SRE"), hit("ACME", "Designer"), hit("", "Nobody"),
		hit("Known Corp", "Ops"), hit("Third", "QA"),
	}
	for range 6 {
		hits = append(hits, hit("Acme", "More"))
	}
	got := discovery.GroupCandidates(hits, map[string]bool{"known corp": true}, 2)
	if len(got) != 2 || got[0].Key != "acme" || got[0].Company != "Acme GmbH" || got[1].Key != "muster" {
		t.Fatalf("candidates = %+v", got)
	}
	if len(got[0].Hits) != 5 || !got[0].MatchKeys()["acme|designer"] {
		t.Errorf("acme hits = %+v", got[0].Hits)
	}
}

type fakeChecks struct {
	hits     []discovery.Hit
	recorded []discovery.BoardCheck
}

func (f *fakeChecks) BoardCandidates(context.Context, time.Time, int) ([]discovery.Hit, error) {
	return f.hits, nil
}

func (f *fakeChecks) RecordBoardChecks(_ context.Context, cs []discovery.BoardCheck, _ time.Time) (int, error) {
	f.recorded = cs
	n := 0
	for _, c := range cs {
		if c.Board != nil {
			n++
		}
	}
	return n, nil
}

type fakeKnown []discovery.Board

func (fakeKnown) ImportBoards(context.Context, []discovery.Board) (int, int, error) { return 0, 0, nil }

func (f fakeKnown) EnabledBoards(context.Context) ([]discovery.Board, error) { return f, nil }

type fakeFinder struct {
	mu    sync.Mutex
	asked []string
}

func (f *fakeFinder) FindBoard(_ context.Context, c discovery.Candidate) (discovery.Board, bool, error) {
	f.mu.Lock()
	f.asked = append(f.asked, c.Key)
	f.mu.Unlock()
	switch c.Key {
	case "acme":
		return discovery.Board{ATS: "ashby", Slug: "acme", Company: c.Company}, true, nil
	case "offline":
		return discovery.Board{}, false, errors.New("network")
	}
	return discovery.Board{}, false, nil
}

func TestBoardDiscovery(t *testing.T) {
	checks := &fakeChecks{hits: []discovery.Hit{
		hit("Acme", "Go Developer"), hit("Muster AG", "SRE"), hit("Offline", "Ops"), hit("Beispiel GmbH", "QA"),
		hit("Beispiel", "QA Lead"),
	}}
	finder := &fakeFinder{}
	d := discovery.NewBoardDiscovery(checks, fakeKnown{{ATS: "greenhouse", Slug: "beispiel", Company: "Beispiel"}}, finder)
	st, err := d.Run(t.Context())
	if err != nil || st != (discovery.DiscoveryStats{Checked: 3, Found: 1, Added: 1, Errors: 1}) {
		t.Fatalf("stats = %+v, %v", st, err)
	}
	slices.Sort(finder.asked)
	if !slices.Equal(finder.asked, []string{"acme", "muster", "offline"}) {
		t.Errorf("asked = %v", finder.asked) // the known company costs no request
	}
	got := map[string]discovery.CheckResult{}
	for _, c := range checks.recorded {
		got[c.CompanyKey] = c.Result
	}
	want := map[string]discovery.CheckResult{"beispiel": discovery.CheckFound, "acme": discovery.CheckFound,
		"muster": discovery.CheckNone, "offline": discovery.CheckError}
	if len(got) != len(want) || len(checks.recorded) != len(want) {
		t.Fatalf("recorded = %+v", checks.recorded)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if discovery.CheckError.RecheckAfter() != discovery.BoardRetry || discovery.CheckNone.RecheckAfter() != discovery.BoardRecheck {
		t.Error("recheck periods")
	}
}

func TestBoardDiscoveryCanceled(t *testing.T) {
	checks := &fakeChecks{hits: []discovery.Hit{hit("Acme", "Go Developer")}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := discovery.NewBoardDiscovery(checks, fakeKnown{}, &fakeFinder{}).Run(ctx); !errors.Is(err, context.Canceled) || checks.recorded != nil {
		t.Errorf("err = %v, recorded = %v", err, checks.recorded)
	}
}
