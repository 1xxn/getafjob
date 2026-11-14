// Package engine runs one application in the browser: prepare, extract, plan, execute and verify per form page,
// with a checkpoint after every step, until the final submit. The server keeps the state (ADR 0014): the engine
// asks it before the submit click and reports every outcome.
package engine

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod"

	"github.com/1xxn/getafjob/internal/runner/browser"
	"github.com/1xxn/getafjob/internal/runner/executor"
)

// Step of a run.
type Step string

// Steps.
const (
	StepPrepare Step = "prepare"
	StepExtract Step = "extract"
	StepPlan    Step = "plan"
	StepExecute Step = "execute"
	StepVerify  Step = "verify"
	StepSubmit  Step = "submit"
)

// Checkpoint is what the server keeps of a step: the page as data and a screenshot.
type Checkpoint struct {
	Page       int               `json:"page"` // form page of a multi-page flow, from 1
	Step       Step              `json:"step"`
	URL        string            `json:"url"`
	Fields     []browser.Field   `json:"fields,omitempty"`
	Filled     []executor.Filled `json:"filled,omitempty"`
	Effect     *browser.Effect   `json:"effect,omitempty"`
	Errors     []string          `json:"errors,omitempty"`
	Dialogs    []browser.Dialog  `json:"dialogs,omitempty"`
	Screenshot []byte            `json:"-"`
	At         time.Time         `json:"at"`
}

// Question is something only the user can answer (an unknown field, a CAPTCHA, a code).
type Question struct {
	Kind    string `json:"kind"` // field, captcha
	FieldID string `json:"field_id,omitempty"`
	Label   string `json:"label"`
	// Input is the field kind (checkbox, radio, select, date, file, text, ...) and Options its choices, so the user
	// answers with a matching control instead of free text (#158).
	Input   string   `json:"input,omitempty"`
	Options []string `json:"options,omitempty"`
}

// Coordinator is the server side of a run (through the gateway).
type Coordinator interface {
	Checkpoint(ctx context.Context, c Checkpoint) error
	// Ask hands the run to the user; the run stops and resumes later.
	Ask(ctx context.Context, qs []Question) error
	// BeginSubmit registers the submit; only a nil answer allows the click.
	BeginSubmit(ctx context.Context) error
	// Submitted reports a submit click with an effect; confirmed when the page confirms the application.
	Submitted(ctx context.Context, confirmed bool, effect browser.Effect) error
	// Park stops the run for a human look.
	Park(ctx context.Context, reason string, screenshot []byte) error
}

// Planner decides the values for a form page; questions stop the run (no guessing).
type Planner interface {
	Plan(ctx context.Context, st *browser.State) (executor.Plan, []Question, error)
}

// StationExpander is optionally implemented by planners that can trigger repeating station groups.
type StationExpander interface {
	NextStationButton(st *browser.State) (buttonID string, ok bool)
}

// Run outcomes besides errors.
var (
	ErrAsked  = errors.New("run handed to the user")
	ErrParked = errors.New("run parked")
	// ErrNeedsApproval is the coordinator's answer to BeginSubmit when the user approves the submit first.
	ErrNeedsApproval = errors.New("the submit waits for the user's approval")
)

// ParkApproval is the park reason of a run that filled everything and waits for the user's approval; the approval
// queues the application again.
const ParkApproval = "waiting for your approval"

// maxPages bounds a multi-page flow (hostile pages can loop).
const maxPages = 12

// Engine runs applications.
type Engine struct {
	Exec    *executor.Executor
	Planner Planner
	Coord   Coordinator
	Wait    time.Duration // per click; 0: 8 s (submits load the next page)
	// FillBeforeAsk fills the known fields of a page before asking for the rest, so the user takes over a nearly
	// complete form (#196). Off for ATS that keep drafts of what is typed (candidate accounts), so a parked
	// application leaves no data with the employer.
	FillBeforeAsk bool
	now           func() time.Time
	page          int // form page of the last Run; Run continues there after the user answers
}

func (e *Engine) wait() time.Duration {
	if e.Wait > 0 {
		return e.Wait
	}
	return 8 * time.Second
}

func (e *Engine) clock() time.Time {
	if e.now != nil {
		return e.now()
	}
	return time.Now()
}

// finalRx names a button that sends the application, nextRx one that moves to the next form page. A form button
// that matches neither parks the run: a click whose effect is unclear must never bypass the submit register.
var finalRx = regexp.MustCompile(`(?i)\b(submit|send|apply|senden|bewerben)\b|absenden|abschicken|einreichen`)

// confirmRx recognizes a confirmation page.
var confirmRx = regexp.MustCompile(`(?i)thank you for (your )?appl|application (has been )?(received|submitted|sent)|` +
	`vielen dank für (ihre|deine) bewerbung|bewerbung (ist )?(eingegangen|erhalten|versendet|übermittelt)|` +
	`we have received your application|wir haben (ihre|deine) bewerbung erhalten`)

// Run fills the application on page p (already at the employer's form) and submits it.
func (e *Engine) Run(ctx context.Context, p *rod.Page) error {
	p = p.Context(ctx) // every browser call below runs under ctx
	dialogs := browser.GuardDialogs(p)
	defer dialogs.Stop()
	for page := max(e.page, 1); page <= maxPages; page++ {
		e.page = page
		st, err := browser.Extract(p)
		if err != nil {
			return err
		}
		if se, ok := e.Planner.(StationExpander); ok {
			for range 10 {
				btnID, needMore := se.NextStationButton(st)
				if !needMore || btnID == "" {
					break
				}
				eff, err := st.Click(btnID, e.wait()) //nolint:contextcheck // the page carries ctx (bound in Run)
				if err != nil || !eff.Any() {
					break
				}
				_ = p.Timeout(e.wait()).WaitLoad()
				time.Sleep(150 * time.Millisecond)
				newSt, err := browser.Extract(p)
				if err != nil {
					break
				}
				st = newSt
			}
		}
		if err := e.checkpoint(ctx, p, page, StepExtract, st, nil, nil, dialogs); err != nil {
			return err
		}
		if qs := captcha(st); len(qs) > 0 {
			return e.ask(ctx, qs)
		}
		if qs := securityCode(st); len(qs) > 0 {
			return e.ask(ctx, qs)
		}
		plan, qs, err := e.Planner.Plan(ctx, st)
		if err != nil {
			return err
		}
		if len(qs) > 0 {
			// Fill what is known first, so the user sees a nearly complete form and answers only the rest (#196).
			// A failed fill does not matter here: the page is planned and filled again after the answers.
			if e.FillBeforeAsk && len(plan.Values) > 0 {
				if filledSt, filled, err := e.Exec.Fill(st, plan); err == nil {
					_ = e.checkpoint(ctx, p, page, StepExecute, filledSt, filled, nil, dialogs)
				}
			}
			return e.ask(ctx, qs)
		}
		if err := e.checkpoint(ctx, p, page, StepPlan, st, nil, nil, dialogs); err != nil {
			return err
		}
		st, filled, err := e.Exec.Fill(st, plan)
		if err != nil {
			return e.park(ctx, p, fmt.Sprintf("fill: %v", err))
		}
		if err := e.checkpoint(ctx, p, page, StepExecute, st, filled, nil, dialogs); err != nil {
			return err
		}
		btn, final, err := formButton(st, plan)
		if err != nil {
			return e.park(ctx, p, err.Error())
		}
		if final {
			return e.submit(ctx, p, page, st, btn, dialogs)
		}
		eff, err := st.Click(btn.ID, e.wait()) //nolint:contextcheck // the page carries ctx (bound in Run)
		if err != nil {
			return err
		}
		if !eff.Any() || len(eff.Invalid) > 0 {
			return e.park(ctx, p, fmt.Sprintf("next page: %q did not move on (invalid %v)", btn.Text, eff.Invalid))
		}
		if err := e.checkpoint(ctx, p, page, StepVerify, st, nil, &eff, dialogs); err != nil {
			return err
		}
		_ = p.Timeout(e.wait()).WaitLoad()
	}
	return e.park(ctx, p, fmt.Sprintf("more than %d form pages", maxPages))
}

// formButton picks the button that continues the form the plan filled: a submit button inside that form. A
// button of the same name elsewhere (page headers) never counts. When the page has no <form> around its fields
// (e.g. Ashby or Workday), non-cookie, non-autofill buttons on the page are eligible.
func formButton(st *browser.State, plan executor.Plan) (browser.Button, bool, error) {
	form := ""
	for _, v := range plan.Values {
		if f, ok := st.Field(v.FieldID); ok && f.Form != "" {
			form = f.Form
			break
		}
	}
	if form == "" {
		for _, f := range st.Fields() {
			if f.Form != "" {
				form = f.Form
				break
			}
		}
	}
	var candidates []browser.Button
	for _, b := range st.Buttons() {
		if b.Disabled || b.Cookie || b.Autofill || (form != "" && b.Form != form) {
			continue
		}
		if b.Kind == "submit" || (b.Kind == "button" && (finalRx.MatchString(b.Text) || nextRx.MatchString(b.Text))) {
			candidates = append(candidates, b)
		}
	}
	if len(candidates) == 0 {
		return browser.Button{}, false, errors.New("no submit or next button inside the form")
	}
	for _, b := range candidates {
		if finalRx.MatchString(b.Text) {
			return b, true, nil
		}
	}
	for _, b := range candidates {
		if nextRx.MatchString(b.Text) {
			return b, false, nil
		}
	}
	return browser.Button{}, false, fmt.Errorf("unclear form button %q: neither send nor next", candidates[0].Text)
}

var nextRx = regexp.MustCompile(`(?i)\b(next|continue)\b|weiter|fortfahren`)

// submit is the only path to the final click: register first, then click, then report.
func (e *Engine) submit(ctx context.Context, p *rod.Page, page int, st *browser.State, btn browser.Button,
	dialogs *browser.Dialogs,
) error {
	if err := e.checkpoint(ctx, p, page, StepSubmit, st, nil, nil, dialogs); err != nil {
		return err
	}
	if err := e.Coord.BeginSubmit(ctx); err != nil {
		switch {
		case errors.Is(err, ErrNeedsApproval):
			return e.park(ctx, p, ParkApproval)
		case errors.Is(err, ErrParked):
			return ErrParked
		}
		return fmt.Errorf("submit not registered, no click: %w", err)
	}
	eff, err := st.Submit(btn.ID, e.wait()) //nolint:contextcheck // the page carries ctx (bound in Run)
	var ne *browser.NoEffectError
	if errors.As(err, &ne) {
		_ = e.Coord.Park(ctx, "submit had no effect after one retry", ne.Screenshot)
		return ErrParked
	}
	if err != nil {
		return err // the server turns a submit that never reports into submit_unknown
	}
	if len(eff.Invalid) > 0 {
		shot, _ := p.Screenshot(false, nil)
		_ = e.Coord.Park(ctx, fmt.Sprintf("the form rejected the submit: %v", eff.Invalid), shot)
		return ErrParked
	}
	confirmed := false
	var after *browser.State
	deadline := e.clock().Add(e.wait())
	for !confirmed && e.clock().Before(deadline) {
		if stAfter, err := browser.Extract(p); err == nil {
			after = stAfter
			confirmed = confirmation(stAfter)
		}
		if !confirmed {
			time.Sleep(250 * time.Millisecond)
		}
	}
	_ = e.checkpoint(ctx, p, page, StepVerify, after, nil, &eff, dialogs)
	return e.Coord.Submitted(ctx, confirmed, eff)
}

// confirmation reports a page that confirms the application in any frame.
func confirmation(st *browser.State) bool {
	for _, f := range st.Frames {
		text := strings.Join(append(f.Headings, f.Text, f.Title), " ")
		if confirmRx.MatchString(text) || strings.Contains(strings.ToLower(f.URL), "confirmation") ||
			strings.Contains(strings.ToLower(f.URL), "thank") {
			return true
		}
	}
	return false
}

func captcha(st *browser.State) []Question {
	for _, f := range st.Frames {
		if f.CaptchaToken {
			return nil
		}
	}
	for _, f := range st.Frames {
		for _, c := range f.Captcha {
			if c.Visible || c.Challenge {
				return []Question{{Kind: "captcha", Label: cmp.Or(c.Src, c.Cls, "captcha")}}
			}
		}
	}
	return nil
}

var securityCodeRx = regexp.MustCompile(`(?i)security[_ -]?code|verification[_ -]?code|confirmation[_ -]?code|` +
	`sicherheitscode|best[aä]tigungscode|verifizierungscode|einmalcode|\botp\b|one[- ]time[- ]?(password|code)|passcode`)

// IsSecurityCode reports whether f is a one-time e-mail or SMS verification code field, which must always go to
// the human and is never answered from the answer bank or profile rules.
func IsSecurityCode(f browser.Field) bool {
	if strings.EqualFold(strings.TrimSpace(f.Autocomplete), "one-time-code") {
		return true
	}
	return securityCodeRx.MatchString(f.Label + " " + f.Name + " " + f.HTMLID + " " + f.Hint)
}

func securityCode(st *browser.State) []Question {
	var qs []Question
	for _, f := range st.Fields() {
		if IsSecurityCode(f) && strings.TrimSpace(f.Value) == "" {
			qs = append(qs, Question{
				Kind:    "security_code",
				FieldID: f.ID,
				Label:   cmp.Or(f.Label, f.Name, "security_code"),
			})
		}
	}
	return qs
}

func (e *Engine) ask(ctx context.Context, qs []Question) error {
	if err := e.Coord.Ask(ctx, qs); err != nil {
		return err
	}
	return ErrAsked
}

func (e *Engine) park(ctx context.Context, p *rod.Page, reason string) error {
	shot, _ := p.Screenshot(false, nil)
	if err := e.Coord.Park(ctx, reason, shot); err != nil {
		return err
	}
	return ErrParked
}

func (e *Engine) checkpoint(ctx context.Context, p *rod.Page, page int, step Step, st *browser.State,
	filled []executor.Filled, eff *browser.Effect, dialogs *browser.Dialogs,
) error {
	c := Checkpoint{Page: page, Step: step, Filled: filled, Effect: eff, Dialogs: dialogs.Seen(), At: e.clock()}
	if st != nil {
		c.Fields = st.Fields()
		for _, f := range st.Frames {
			c.Errors = append(c.Errors, f.Errors...)
		}
		c.URL = st.Frames[0].URL
	}
	c.Screenshot, _ = p.Screenshot(false, nil)
	return e.Coord.Checkpoint(ctx, c)
}
