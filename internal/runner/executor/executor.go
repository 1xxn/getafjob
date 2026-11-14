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
	Kind    string `json:"kind,omitempty"` // field kind, so a checkbox shows as yes or no
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
		label, shown, kind := v.Label, v.Text, ""
		if len(v.Files) > 0 {
			shown = strings.Join(v.Files, ", ")
		}
		if f, ok := st.Field(v.FieldID); ok {
			label, kind = f.Label, f.Kind
			if f.Value != "" || len(v.Files) == 0 {
				shown = f.Value
			}
		}
		done = append(done, Filled{FieldID: v.FieldID, Label: label, Shown: shown, Source: v.Source, Kind: kind})
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
	if !ok {
		if b, isButton := st.Button(v.FieldID); isButton {
			return x.toggle(st, p, b, v)
		}
	}
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
	case "richtext":
		err = st.Type(v.FieldID, v.Text)
	case "combobox":
		if want, err = x.combobox(st, v); err != nil {
			return st, err
		}
		return x.verify(st, v.FieldID, want, sameChoice)
	case "checkbox", "aria_checkbox", "aria_radio":
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
	if f.Kind == "file" {
		return x.verifyFile(st, v.FieldID, v.Files)
	}
	if f.Kind == "richtext" { // the field reports at most its first 500 characters
		return x.verify(st, v.FieldID, want, func(shown, want string) bool {
			return strings.HasPrefix(strings.TrimSpace(want), strings.TrimSpace(shown)) && shown != ""
		})
	}
	return x.verify(st, v.FieldID, want, func(shown, want string) bool {
		return strings.TrimSpace(shown) == strings.TrimSpace(want)
	})
}

// verifyFile checks that a file upload is reflected either on the file input or as a file chip in the frame text.
func (x *Executor) verifyFile(st *browser.State, id string, files []string) (*browser.State, error) {
	want := strings.Join(files, ", ")
	deadline := time.Now().Add(x.wait())
	for {
		next, err := st.Refresh()
		if err != nil {
			return st, err
		}
		got, ok := next.Field(id)
		if ok && strings.TrimSpace(got.Value) == strings.TrimSpace(want) {
			return next, nil
		}
		if fr, err := next.FrameOf(id); err == nil && hasAllFiles(fr.Text, files) {
			return next, nil
		}
		if time.Now().After(deadline) {
			return next, fmt.Errorf("%w: shows %q, want %q", ErrNotVerified, got.Value, want)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func hasAllFiles(text string, files []string) bool {
	for _, fn := range files {
		if !strings.Contains(text, fn) {
			return false
		}
	}
	return len(files) > 0
}

// verify extracts the page again and checks what the field shows; some widgets render their value late.
func (x *Executor) verify(st *browser.State, id, want string, same func(shown, want string) bool) (*browser.State, error) {
	deadline := time.Now().Add(x.wait())
	for {
		next, err := st.Refresh()
		if err != nil {
			return st, err
		}
		got, ok := next.Field(id)
		if ok && same(got.Value, want) {
			return next, nil
		}
		if time.Now().After(deadline) {
			return next, fmt.Errorf("%w: shows %q, want %q", ErrNotVerified, got.Value, want)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// combobox opens the widget, finds the option that means the value (typing it when the list does not show it
// yet) and picks it. It returns the chosen option's label.
func (x *Executor) combobox(st *browser.State, v Value) (string, error) {
	if _, err := st.Click(v.FieldID, x.wait()); err != nil {
		return "", err
	}
	typed := false
	deadline := time.Now().Add(2 * x.wait())
	for {
		opts, err := st.ListOptions(v.FieldID)
		if err != nil {
			return "", err
		}
		if i, ok := Match(v.Text, labels(opts)); ok {
			if _, err := st.Click(opts[i].ID, x.wait()); err == nil {
				return opts[i].Label, nil
			} else if !errors.Is(err, browser.ErrUnknownID) && !errors.Is(err, browser.ErrNotVisible) {
				return "", err
			} // the list was rendered again: read it once more
		}
		if !typed {
			typed = true
			if f, ok := st.Field(v.FieldID); ok && isTypable(f) {
				if err := st.Type(v.FieldID, v.Text); err != nil {
					return "", err
				}
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("%w: %q among %q", ErrNoOption, v.Text, labels(opts))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// isTypable reports a combobox that is a text input (react-select, autocompletes), not a listbox button.
func isTypable(f browser.Field) bool { return f.InputType != "" }

// sameChoice compares what a combobox shows with the chosen option: widgets often show a short form ("+49" for
// "Germany +49").
func sameChoice(shown, chosen string) bool {
	s, c := normalize(shown), normalize(chosen)
	return s != "" && (s == c || strings.Contains(c, s) || strings.Contains(s, c))
}

// toggle presses or releases a button with aria-pressed (yes/no answers as buttons).
func (x *Executor) toggle(st *browser.State, p Plan, b browser.Button, v Value) (*browser.State, error) {
	if normalize(b.Text) != normalize(v.Label) || (v.Text != "true" && v.Text != "false") {
		return st, ErrFieldChanged
	}
	if b.Kind == "submit" { // a toggle never sends a form: only the engine's submit path may click those
		return st, ErrRefused
	}
	fr, err := st.FrameOf(b.ID)
	if err != nil {
		return st, err
	}
	if !slices.Contains(p.Origins, fr.Origin()) {
		return st, fmt.Errorf("%w: %s", ErrForeignOrigin, fr.Origin())
	}
	on := v.Text == "true"
	if b.Pressed != on {
		if _, err := st.Click(b.ID, x.wait()); err != nil {
			return st, err
		}
	}
	deadline := time.Now().Add(x.wait())
	for {
		next, err := st.Refresh()
		if err != nil {
			return st, err
		}
		if got, ok := next.Button(b.ID); ok && got.Pressed == on {
			return next, nil
		}
		if time.Now().After(deadline) {
			return next, fmt.Errorf("%w: button %q not pressed=%v", ErrNotVerified, b.Text, on)
		}
		time.Sleep(150 * time.Millisecond)
	}
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
