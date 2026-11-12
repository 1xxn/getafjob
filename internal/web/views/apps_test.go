package views

import "testing"

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
