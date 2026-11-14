package views

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// A trigger "from:input" listens on every input of the page, and htmx splits "from:" at the first space: ticking a
// row checkbox or typing an answer elsewhere would reload a list and drop the selection. A form listens to its own
// fields with "target:input".
func TestTriggersAreScopedToTheirForm(t *testing.T) {
	files, err := filepath.Glob("*.templ")
	if err != nil || len(files) == 0 {
		t.Fatalf("templates: %v", err)
	}
	rx := regexp.MustCompile(`from:[^,"]*\b(input|select|textarea)\b`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if m := rx.Find(b); m != nil {
			t.Errorf("%s: unscoped trigger %q", f, m)
		}
	}
}
