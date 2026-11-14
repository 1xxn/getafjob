package views

import (
	"strings"
	"testing"
)

func TestQuestionText(t *testing.T) {
	for in, want := range map[string]string{
		"Earliest start * | (placeholder: e.g. 01.01.2027)": "Earliest start *",
		"(placeholder: Your city)":                          "Your city",
		"Salary | per year":                                 "Salary per year",
		"Plain question?":                                   "Plain question?",
	} {
		if got := questionText(in); got != want {
			t.Errorf("questionText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSourceKey(t *testing.T) {
	for in, want := range map[string]string{"profile.email": "apps.source.profile", "answer#01ab": "apps.source.answer",
		"profile:personal.first_name": "apps.source.profile", "document#x": "apps.source.document", "": "apps.source.other"} {
		if got := sourceKey(in); got != want {
			t.Errorf("sourceKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// After the answers the next waiting run is offered only when one waits.
func TestAnswerSentOffersTheNextRunOnlyWhenOneWaits(t *testing.T) {
	for more, want := range map[bool]bool{true: true, false: false} {
		var b strings.Builder
		if err := AnswerSent("01a1d6ff-79fa-7698-aad6-97d2bcc038f0", true, more).Render(t.Context(), &b); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(b.String(), `href="/needs-input"`); got != want {
			t.Errorf("more %v: next link %v", more, got)
		}
	}
}
