package web

import (
	"testing"

	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/web/views"
)

func TestSimilarAnswer(t *testing.T) {
	bank := []profile.Answer{
		{Question: "Do you need a visa sponsorship to work in Germany?", Answer: "No"},
		{Question: "What are your salary expectations?", Answer: "70000"},
	}
	for _, c := range []struct {
		q, want string
		ok      bool
	}{
		{"Will you need visa sponsorship to work in Germany?", "No", true},
		{"Salary expectations (EUR per year)", "70000", true},
		{"What is your favorite color?", "", false},
		{"", "", false},
	} {
		got, ok := similarAnswer(c.q, bank)
		if ok != c.ok || (ok && got.Answer != c.want) {
			t.Errorf("%q: got %q %v, want %q %v", c.q, got.Answer, ok, c.want, c.ok)
		}
	}
}

func TestFitsQuestion(t *testing.T) {
	if !fitsQuestion(views.HandoffQuestion{Input: "checkbox"}, "Yes") || fitsQuestion(views.HandoffQuestion{Input: "checkbox"}, "maybe") {
		t.Error("checkbox suggestions must be Yes or No")
	}
	q := views.HandoffQuestion{Input: "select", Options: []string{"Germany", "Austria"}}
	if !fitsQuestion(q, "Austria") || fitsQuestion(q, "France") {
		t.Error("choice suggestions must be a listed option")
	}
	if !fitsQuestion(views.HandoffQuestion{Input: "text"}, "anything") {
		t.Error("free text takes any suggestion")
	}
}
