package application

import (
	"errors"
	"testing"
	"time"
)

func TestTransitions(t *testing.T) {
	path := []struct {
		ev Event
		to State
	}{{EvMatch, Matched}, {EvQueue, Queued}, {EvStart, Running}, {EvAsk, NeedsInput}, {EvAnswer, Running},
		{EvBeginSubmit, Submitting}, {EvSubmitted, Submitted}, {EvConfirm, Confirmed}, {EvInterview, Interview},
		{EvOffer, Offer}, {EvWithdraw, Withdrawn}}
	s := Discovered
	for _, p := range path {
		next, err := Next(s, p.ev)
		if err != nil || next != p.to {
			t.Fatalf("%s on %s = %s, %v; want %s", p.ev, s, next, err, p.to)
		}
		s = next
	}
	refused := []struct {
		from State
		ev   Event
	}{
		{SubmitUnknown, EvRequeue},  // never retried automatically
		{SubmitUnknown, EvStart},    // not even by starting a run
		{Submitting, EvBeginSubmit}, // no second submit
		{Submitted, EvRequeue},      // a sent application is never sent again
		{Parked, EvBeginSubmit},     // only a running application submits
		{Queued, EvSubmitted},
		{Withdrawn, EvRequeue},
	}
	for _, r := range refused {
		if _, err := Next(r.from, r.ev); !errors.Is(err, ErrTransition) {
			t.Errorf("%s on %s allowed", r.ev, r.from)
		}
	}
	if s, _ := Next(Submitting, EvCrash); s != SubmitUnknown {
		t.Errorf("crash while submitting = %s", s)
	}
	if s, _ := Next(Submitting, EvFail); s != SubmitUnknown {
		t.Errorf("failure while submitting = %s; the click may have gone out", s)
	}
}

func TestEveryStateIsReachableAndEventsLeadToKnownStates(t *testing.T) {
	reached := map[State]bool{Discovered: true}
	for from, evs := range transitions {
		for _, to := range evs {
			if _, known := transitions[to]; !known && !terminal(to) {
				t.Errorf("%s leads to %s, which is neither in the table nor terminal", from, to)
			}
			reached[to] = true
		}
	}
	for _, s := range []State{Matched, Queued, Running, NeedsInput, Submitting, Submitted, Confirmed, Interview,
		Rejected, Offer, Withdrawn, Skipped, Parked, Failed, SubmitUnknown} {
		if !reached[s] {
			t.Errorf("%s unreachable", s)
		}
	}
}

func terminal(s State) bool { return s == Rejected || s == Withdrawn || s == Skipped }

func TestGuard(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name string
		c    Candidate
		want error
	}{
		{"fresh", Candidate{}, nil},
		{"excluded", Candidate{Excluded: true}, ErrExcluded},
		{"same job sent elsewhere", Candidate{SameJob: []State{Submitted}}, ErrDuplicate},
		{"same job unknown elsewhere", Candidate{SameJob: []State{SubmitUnknown}}, ErrDuplicate},
		{"same job running elsewhere", Candidate{SameJob: []State{Running}}, ErrDuplicate},
		{"same job skipped elsewhere", Candidate{SameJob: []State{Skipped, Parked}}, nil},
		{"one recent at the company", Candidate{CompanyApplied: []time.Time{now.Add(-5 * day)}}, nil},
		{"two recent at the company", Candidate{CompanyApplied: []time.Time{now.Add(-5 * day), now.Add(-20 * day)}}, ErrCompanyWindow},
		{"two, one old", Candidate{CompanyApplied: []time.Time{now.Add(-5 * day), now.Add(-40 * day)}}, nil},
	}
	for _, tc := range cases {
		if err := Guard(tc.c, now); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
}
