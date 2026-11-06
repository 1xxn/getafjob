package executor

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/1xxn/getafjob/internal/runner/browser"
)

// Errors of the executor; each stops the run before or at the field concerned.
var (
	ErrNoProvenance  = errors.New("value without provenance")
	ErrFieldChanged  = errors.New("field is gone or not the planned one")
	ErrForeignOrigin = errors.New("field is on a foreign origin")
	ErrRefused       = errors.New("field kind is never filled")
	ErrUnsupported   = errors.New("field kind not supported yet")
	ErrNoOption      = errors.New("no option matches the value")
	ErrTooLong       = errors.New("value is longer than the field allows")
	ErrNotVerified   = errors.New("field does not show the value after filling")
)

// Value is one planned field value.
type Value struct {
	FieldID string
	Label   string   // label at planning time; the field must still carry it
	Text    string   // text, ISO date, option label or "true"/"false" for a single checkbox
	Options []string // choices of a checkbox group
	Files   []string // documents for a file field
	Source  string   // provenance (ADR 0013)
}

// Plan is what to fill and where it may go.
type Plan struct {
	Origins []string // origins (scheme://host) of the employer's application forms
	Values  []Value
}

// Filled records one filled field for the evidence page.
type Filled struct {
	FieldID string `json:"field_id"`
	Label   string `json:"label"`
	Shown   string `json:"shown"` // what the field shows afterwards
	Source  string `json:"source"`
}

// Executor fills plans into the page of one run.
type Executor struct {
	Docs *browser.Documents
	Wait time.Duration // per click and upload; 0: 3 s
}

// Fill checks the whole plan first (provenance, origins), then fills and verifies field by field. It stops at
// the first error and returns what was filled so far together with the latest page state.
func (x *Executor) Fill(st *browser.State, p Plan) (*browser.State, []Filled, error) {
	for _, v := range p.Values {
		if !ValidSource(v.Source) {
			return st, nil, fmt.Errorf("%w: field %q (%q)", ErrNoProvenance, v.Label, v.Source)
		}
	}
	if len(p.Origins) == 0 {
		return st, nil, fmt.Errorf("%w: plan names no origin", ErrForeignOrigin)
	}
	var done []Filled
	for _, v := range p.Values {
		var err error
		if st, err = x.fill(st, p, v); err != nil {
			return st, done, fmt.Errorf("field %q: %w", v.Label, err)
		}
		f, _ := st.Field(v.FieldID)
		done = append(done, Filled{FieldID: v.FieldID, Label: f.Label, Shown: f.Value, Source: v.Source})
	}
	return st, done, nil
}

func (x *Executor) wait() time.Duration {
	if x.Wait > 0 {
		return x.Wait
	}
	return 3 * time.Second
}

// fill fills one value and returns the page state after it, with the value verified.
func (x *Executor) fill(st *browser.State, p Plan, v Value) (*browser.State, error) {
	f, ok := st.Field(v.FieldID)
	if !ok || normalize(f.Label) != normalize(v.Label) {
		return st, ErrFieldChanged
	}
	fr, err := st.FrameOf(v.FieldID)
	if err != nil {
		return st, err
	}
	if !slices.Contains(p.Origins, fr.Origin()) {
		return st, fmt.Errorf("%w: %s", ErrForeignOrigin, fr.Origin())
	}
	want := v.Text
	switch f.Kind {
	case "password":
		return st, ErrRefused
	case "text", "textarea":
		if f.MaxLength > 0 && utf8.RuneCountInString(v.Text) > f.MaxLength {
			return st, fmt.Errorf("%w: %d > %d", ErrTooLong, utf8.RuneCountInString(v.Text), f.MaxLength)
		}
		err = st.Type(v.FieldID, v.Text)
	case "date":
		err = st.SetValue(v.FieldID, v.Text)
	case "select":
		i, ok := Match(v.Text, labels(f.Options))
		if !ok {
			return st, fmt.Errorf("%w: %q", ErrNoOption, v.Text)
		}
		want = f.Options[i].Label
		err = st.SetValue(v.FieldID, f.Options[i].Value)
	case "checkbox":
		on := v.Text == "true"
		if v.Text != "true" && v.Text != "false" {
			return st, fmt.Errorf("%w: checkbox takes true or false", ErrNoOption)
		}
		if (f.Value == "checked") != on {
			_, err = st.Click(v.FieldID, x.wait())
		}
		want = map[bool]string{true: "checked", false: ""}[on]
	case "radio":
		i, ok := Match(v.Text, labels(f.Options))
		if !ok {
			return st, fmt.Errorf("%w: %q", ErrNoOption, v.Text)
		}
		want = f.Options[i].Label
		if !f.Options[i].Checked {
			_, err = st.Click(f.Options[i].ID, x.wait())
		}
	case "checkbox_group":
		want, err = x.group(st, f, v)
	case "file":
		err = st.Upload(x.Docs, v.FieldID, v.Files, x.wait())
		want = strings.Join(v.Files, ", ")
	default:
		return st, fmt.Errorf("%w: %s", ErrUnsupported, f.Kind)
	}
	if err != nil {
		return st, err
	}
	next, err := st.Refresh()
	if err != nil {
		return st, err
	}
	got, ok := next.Field(v.FieldID)
	if !ok || strings.TrimSpace(got.Value) != strings.TrimSpace(want) {
		return next, fmt.Errorf("%w: shows %q, want %q", ErrNotVerified, got.Value, want)
	}
	return next, nil
}

// group sets a checkbox group to exactly the wanted choices.
func (x *Executor) group(st *browser.State, f browser.Field, v Value) (string, error) {
	opts := labels(f.Options)
	wanted := map[int]bool{}
	for _, o := range v.Options {
		i, ok := Match(o, opts)
		if !ok {
			return "", fmt.Errorf("%w: %q", ErrNoOption, o)
		}
		wanted[i] = true
	}
	var shown []string
	for i, o := range f.Options {
		if wanted[i] {
			shown = append(shown, o.Label)
		}
		if wanted[i] != o.Checked {
			if _, err := st.Click(o.ID, x.wait()); err != nil {
				return "", err
			}
		}
	}
	return strings.Join(shown, "; "), nil
}

func labels(opts []browser.Option) []string {
	out := make([]string, len(opts))
	for i, o := range opts {
		out[i] = o.Label
	}
	return out
}
