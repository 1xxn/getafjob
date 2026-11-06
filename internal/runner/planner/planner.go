// Package planner decides the value of every form field from the run's knowledge: profile facts, the answer
// bank, rendered text blocks and documents. It never guesses: a required field it cannot answer becomes a
// question for the user, an optional one stays empty.
package planner

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/1xxn/getafjob/internal/runner/browser"
	"github.com/1xxn/getafjob/internal/runner/engine"
	"github.com/1xxn/getafjob/internal/runner/executor"
)

// Knowledge is what the gateway hands to a run: only what filling this application needs.
type Knowledge struct {
	// Profile holds facts by key (personal.first_name, work.notice_period, preferences.diversity ...). Yes/no
	// facts are "true" or "false".
	Profile   map[string]string
	Answers   []Answer
	Texts     []Text     // text blocks with the placeholders already filled for this job
	Documents []Document // files in the run's document directory
	CV        string     // ID of the CV chosen for this job
	Language  string     // en or de: the form's language
	Origins   []string   // origins the values may go to
}

// Answer is an answer bank entry.
type Answer struct {
	ID, Question, Answer string
}

// Text is a rendered text block.
type Text struct {
	ID, Name, Language, Body string
}

// Document is a file of the user for this run.
type Document struct {
	ID, File, Kind, Language string // Kind: cv, cover_letter, certificate
}

// Drafter optionally proposes answers for fields the planner cannot answer (off by default). A draft is only a
// suggestion attached to the question; it is used after the user approved it.
type Drafter interface {
	Draft(ctx context.Context, f browser.Field) (string, error)
}

// Planner is the deterministic planner.
type Planner struct {
	K       Knowledge
	Drafter Drafter // nil: no AI
}

var _ engine.Planner = (*Planner)(nil)

// Plan returns values for the fields it can answer and questions for required fields it cannot.
func (p *Planner) Plan(ctx context.Context, st *browser.State) (executor.Plan, []engine.Question, error) {
	plan := executor.Plan{Origins: p.K.Origins}
	var qs []engine.Question
	for _, f := range st.Fields() {
		if f.Kind == "password" {
			continue
		}
		v, ok := p.answer(f)
		if ok && optionsKnown(f) && !hasOption(f, v) {
			ok = false
		}
		if ok {
			v.FieldID, v.Label = f.ID, f.Label
			plan.Values = append(plan.Values, v)
			continue
		}
		if !f.Required {
			continue
		}
		q := engine.Question{Kind: "field", FieldID: f.ID, Label: f.Label}
		if p.Drafter != nil {
			if d, err := p.Drafter.Draft(ctx, f); err == nil {
				q.Draft = d
			}
		}
		qs = append(qs, q)
	}
	return plan, qs, nil
}

// answer finds the value of one field: the answer bank for the same question first, then the rules.
func (p *Planner) answer(f browser.Field) (executor.Value, bool) {
	label := executor.Normalize(f.Label)
	for _, a := range p.K.Answers {
		if executor.Normalize(a.Question) != label {
			continue
		}
		v := executor.Value{Text: a.Answer, Options: split(f, a.Answer), Source: "answer#" + a.ID}
		if f.Kind == "checkbox" || f.Kind == "aria_checkbox" || f.Kind == "aria_radio" {
			i, ok := executor.Match(a.Answer, []string{"Yes", "No"})
			if !ok {
				return executor.Value{}, false
			}
			v.Text = yesNo(f, i == 0)
		}
		return v, true
	}
	for _, r := range rules {
		if !r.rx.MatchString(label) || !fits(r.kind, f.Kind) {
			continue
		}
		switch r.kind {
		case "file":
			return p.file(r.key)
		case "text":
			return p.text(r.key)
		case "bool":
			fact, ok := p.K.Profile[r.key]
			if !ok || (fact != "true" && fact != "false") {
				return executor.Value{}, false
			}
			return executor.Value{Text: yesNo(f, fact == "true"), Source: "profile." + r.key}, true
		case "diversity":
			if p.K.Profile[r.key] != "decline" {
				return executor.Value{}, false // answering diversity questions needs the user's own answers
			}
			return executor.Value{Text: "Prefer not to say", Source: "profile." + r.key}, true
		default:
			v, ok := p.K.Profile[r.key]
			if !ok || strings.TrimSpace(v) == "" {
				return executor.Value{}, false
			}
			return executor.Value{Text: v, Source: "profile." + r.key}, true
		}
	}
	return executor.Value{}, false
}

// fits keeps file rules to file fields and text rules to long text fields.
func fits(ruleKind, fieldKind string) bool {
	switch ruleKind {
	case "file":
		return fieldKind == "file"
	case "text":
		return fieldKind == "textarea" || fieldKind == "richtext"
	default:
		return fieldKind != "file"
	}
}

func (p *Planner) file(kind string) (executor.Value, bool) {
	var files []string
	var id string
	for _, d := range p.K.Documents {
		switch {
		case kind == "cv" && d.ID == p.K.CV,
			kind == "cover_letter" && d.Kind == "cover_letter" && (d.Language == "" || d.Language == p.K.Language),
			kind == "certificates" && d.Kind == "certificate":
			files = append(files, d.File)
			if id == "" {
				id = d.ID
			}
		}
	}
	if len(files) == 0 || (kind != "certificates" && len(files) > 1) {
		return executor.Value{}, false
	}
	return executor.Value{Files: files, Source: "document#" + id}, true
}

func (p *Planner) text(kind string) (executor.Value, bool) {
	var best *Text
	for i, t := range p.K.Texts {
		name := executor.Normalize(t.Name)
		if kind == "cover_letter" && (strings.Contains(name, "cover letter") || strings.Contains(name, "anschreiben") ||
			strings.Contains(name, "motivation")) && (t.Language == "" || t.Language == p.K.Language) {
			if best == nil || (best.Language == "" && t.Language != "") {
				best = &p.K.Texts[i]
			}
		}
	}
	if best == nil {
		return executor.Value{}, false
	}
	return executor.Value{Text: best.Body, Source: "textblock#" + best.ID}, true
}

// yesNo writes a yes/no fact the way the field takes it.
func yesNo(f browser.Field, yes bool) string {
	if f.Kind == "checkbox" || f.Kind == "aria_checkbox" || f.Kind == "aria_radio" {
		if yes {
			return "true"
		}
		return "false"
	}
	if yes {
		return "Yes"
	}
	return "No"
}

// optionsKnown reports fields whose options the extraction lists (comboboxes load theirs on opening).
func optionsKnown(f browser.Field) bool {
	return (f.Kind == "select" || f.Kind == "radio") && len(f.Options) > 0
}

func hasOption(f browser.Field, v executor.Value) bool {
	labels := make([]string, len(f.Options))
	for i, o := range f.Options {
		labels[i] = o.Label
	}
	_, ok := executor.Match(v.Text, labels)
	return ok
}

// split turns a stored answer into choices for a checkbox group ("German; English").
func split(f browser.Field, answer string) []string {
	if f.Kind != "checkbox_group" {
		return nil
	}
	var out []string
	for _, s := range strings.FieldsFunc(answer, func(r rune) bool { return r == ';' || r == ',' }) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Precheck answers the questions an ATS publishes for a job before the run; the required ones it cannot answer
// are asked before the browser starts.
func (p *Planner) Precheck(ctx context.Context, fields []browser.Field) []engine.Question {
	fs := slices.Clone(fields)
	for i := range fs {
		fs[i].ID = fmt.Sprintf("0-pre%d", i)
	}
	_, qs, _ := p.Plan(ctx, &browser.State{Frames: []*browser.Frame{{Fields: fs}}})
	for i := range qs {
		qs[i].FieldID = "" // not on a page yet
	}
	return qs
}
