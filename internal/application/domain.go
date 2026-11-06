// Package application drives applications through their states: every transition is an event, the submit
// register makes a second submission impossible (ADR 0014), and guards run before every application.
package application

import (
	"errors"
	"fmt"
	"time"
)

// State of an application.
type State string

// States.
const (
	Discovered    State = "discovered"
	Matched       State = "matched"
	Queued        State = "queued"
	Running       State = "running"
	NeedsInput    State = "needs_input"
	Submitting    State = "submitting"
	Submitted     State = "submitted"
	Confirmed     State = "confirmed"
	Interview     State = "interview"
	Rejected      State = "rejected"
	Offer         State = "offer"
	Withdrawn     State = "withdrawn"
	Skipped       State = "skipped"
	Parked        State = "parked"
	Failed        State = "failed"
	SubmitUnknown State = "submit_unknown"
)

// Event moves an application from one state to another.
type Event string

// Events.
const (
	EvMatch       Event = "match"
	EvQueue       Event = "queue"
	EvStart       Event = "start"
	EvAsk         Event = "ask"    // the run needs the user (question, CAPTCHA, code)
	EvAnswer      Event = "answer" // the user answered, the run continues
	EvBeginSubmit Event = "begin_submit"
	EvSubmitted   Event = "submitted" // the submit click had an effect
	EvConfirm     Event = "confirm"   // confirmation page or mail
	EvInterview   Event = "interview"
	EvReject      Event = "reject"
	EvOffer       Event = "offer"
	EvWithdraw    Event = "withdraw"
	EvSkip        Event = "skip"
	EvPark        Event = "park" // stopped for a human look (no effect, unknown field, guard)
	EvFail        Event = "fail"
	EvRequeue     Event = "requeue" // a parked or failed application is tried again by the user's decision
	EvCrash       Event = "crash"   // the run died while submitting
	EvReconciled  Event = "reconciled"
	EvNotSent     Event = "not_sent" // reconciliation found no submission; a human decides what follows
)

// transitions is the complete state machine; anything not listed is refused.
var transitions = map[State]map[Event]State{
	Discovered: {EvMatch: Matched, EvSkip: Skipped},
	Matched:    {EvQueue: Queued, EvSkip: Skipped, EvPark: Parked},
	Queued:     {EvStart: Running, EvSkip: Skipped, EvPark: Parked},
	Running: {EvAsk: NeedsInput, EvBeginSubmit: Submitting, EvPark: Parked, EvFail: Failed, EvSkip: Skipped,
		EvSubmitted: Submitted}, // EvSubmitted only for flows whose last step is not ours (e.g. a portal)
	NeedsInput:    {EvAnswer: Running, EvPark: Parked, EvSkip: Skipped, EvFail: Failed},
	Submitting:    {EvSubmitted: Submitted, EvCrash: SubmitUnknown, EvPark: Parked, EvFail: SubmitUnknown},
	Submitted:     {EvConfirm: Confirmed, EvReject: Rejected, EvInterview: Interview, EvOffer: Offer, EvWithdraw: Withdrawn},
	Confirmed:     {EvInterview: Interview, EvReject: Rejected, EvOffer: Offer, EvWithdraw: Withdrawn},
	Interview:     {EvReject: Rejected, EvOffer: Offer, EvWithdraw: Withdrawn},
	Offer:         {EvWithdraw: Withdrawn},
	Parked:        {EvRequeue: Queued, EvSkip: Skipped},
	Failed:        {EvRequeue: Queued, EvSkip: Skipped},
	SubmitUnknown: {EvReconciled: Submitted, EvNotSent: Parked, EvConfirm: Confirmed},
}

// ErrTransition reports an event that the current state does not allow.
var ErrTransition = errors.New("transition not allowed")

// Next returns the state an event leads to.
func Next(from State, ev Event) (State, error) {
	if to, ok := transitions[from][ev]; ok {
		return to, nil
	}
	return "", fmt.Errorf("%w: %s on %s", ErrTransition, ev, from)
}

// Sent reports states in which an application may have reached the employer: it must never be sent again.
func (s State) Sent() bool {
	switch s {
	case Submitting, Submitted, Confirmed, Interview, Rejected, Offer, Withdrawn, SubmitUnknown:
		return true
	default:
		return false
	}
}

// Open reports states in which an application is still on its way (queued or in a run).
func (s State) Open() bool {
	switch s {
	case Queued, Running, NeedsInput:
		return true
	default:
		return false
	}
}

// Guard results: why an application may not start.
var (
	ErrExcluded      = errors.New("excluded by the user's filters")
	ErrDuplicate     = errors.New("the same job is already applied for through another source")
	ErrCompanyWindow = errors.New("too many applications to this company in the window")
)

// CompanyWindow limits applications to one company: at most CompanyLimit within CompanyDays.
const (
	CompanyDays  = 30
	CompanyLimit = 2
)

// Candidate is what the guards look at before an application starts.
type Candidate struct {
	Excluded bool // the user's filters reject the job (matching decision skip with a hard filter)
	// SameJob are the states of the user's applications for the same job from other sources (same match key).
	SameJob []State
	// CompanyApplied are the times the user's applications to the company were sent.
	CompanyApplied []time.Time
}

// Guard returns nil when an application may start.
func Guard(c Candidate, now time.Time) error {
	if c.Excluded {
		return ErrExcluded
	}
	for _, s := range c.SameJob {
		if s.Sent() || s.Open() {
			return ErrDuplicate
		}
	}
	n := 0
	for _, at := range c.CompanyApplied {
		if now.Sub(at) < CompanyDays*24*time.Hour {
			n++
		}
	}
	if n >= CompanyLimit {
		return ErrCompanyWindow
	}
	return nil
}
