//go:build browser

package executor

import (
	"strings"
	"testing"
	"time"
)

func (f *form) goTo(page string) {
	f.t.Helper()
	if err := f.page.Navigate(f.url + "/" + page); err != nil {
		f.t.Fatal(err)
	}
	if err := f.page.WaitLoad(); err != nil {
		f.t.Fatal(err)
	}
}

// field finds a field by kind and label prefix in a fresh extraction.
func (f *form) field(kind, label, text, source string) Value {
	f.t.Helper()
	st, err := extract(f)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, fd := range st.Fields() {
		if (kind == "" || fd.Kind == kind) && strings.HasPrefix(fd.Label, label) {
			return Value{FieldID: fd.ID, Label: fd.Label, Text: text, Source: source}
		}
	}
	f.t.Fatalf("no %s field %q", kind, label)
	return Value{}
}

func TestFillComboboxes(t *testing.T) {
	f := open(t)
	f.goTo("greenhouse_select.html")
	x := &Executor{Docs: f.docs, Wait: 4 * time.Second}
	plan := Plan{Origins: []string{f.url}, Values: []Value{
		f.field("combobox", "Land", "Deutschland", "profile.personal.country"),
		f.field("combobox", "Falls du nicht in Berlin lebst", "Ja", "profile.work.willing_to_relocate"),
		f.field("combobox", "Welche Kommunikationssprache", "Deutsch", "answer#11"),
		f.field("combobox", "Standort", "Berlin, Germany", "profile.personal.city"),
	}}
	st, err := extract(f)
	if err != nil {
		t.Fatal(err)
	}
	_, done, err := x.Fill(st, plan)
	if err != nil {
		t.Fatalf("fill: %v (done %+v)", err, done)
	}
	want := []string{"+49", "Yes", "German", "Berlin, Germany"}
	for i, d := range done {
		if d.Shown != want[i] {
			t.Errorf("%s shows %q, want %q", d.Label, d.Shown, want[i])
		}
	}
}

func TestFillListboxButtons(t *testing.T) {
	f := open(t)
	f.goTo("workday_listbox.html")
	x := &Executor{Docs: f.docs}
	st, _ := extract(f)
	_, done, err := x.Fill(st, Plan{Origins: []string{f.url}, Values: []Value{
		f.field("combobox", "State", "Berlin", "profile.personal.state"),
		f.field("combobox", "Phone", "Mobile", "answer#2"),
	}})
	if err != nil || len(done) != 2 || done[0].Shown != "Berlin" || done[1].Shown != "Mobile" {
		t.Fatalf("done %+v, %v", done, err)
	}
}

func TestFillAriaWidgetsTogglesAndRichText(t *testing.T) {
	f := open(t)
	f.goTo("widgets.html")
	x := &Executor{Docs: f.docs}
	st, _ := extract(f)
	_, done, err := x.Fill(st, Plan{Origins: []string{f.url}, Values: []Value{
		f.field("aria_radio", "Late", "true", "answer#21"),
		f.field("aria_checkbox", "Share my profile", "false", "answer#22"),
		f.field("aria_checkbox", "Email updates", "false", "answer#23"),
		f.field("richtext", "Motivation", "I build reliable backends in Go.", "textblock#2"),
	}})
	if err != nil {
		t.Fatalf("fill: %v (done %+v)", err, done)
	}
	if got := f.page.MustEval(`() => [...document.querySelectorAll("[aria-checked=true]")].map((e) => e.textContent).join("|")`).Str(); got != "Late" {
		t.Fatalf("checked = %q", got)
	}
	if got := f.page.MustEval(`() => document.querySelector("[contenteditable]").innerText.trim()`).Str(); got != "I build reliable backends in Go." {
		t.Fatalf("rich text = %q", got)
	}

	f.goTo("ashby_yesno.html")
	st, _ = extract(f)
	var yes Value
	for _, b := range st.Buttons() {
		if b.Text == "Yes" {
			yes = Value{FieldID: b.ID, Label: b.Text, Text: "true", Source: "answer#24"}
		}
	}
	if _, _, err := x.Fill(st, Plan{Origins: []string{f.url}, Values: []Value{yes}}); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if again, _ := extract(f); !mustButton(t, again, yes.FieldID).Pressed {
		t.Fatal("yes not pressed")
	}
}
