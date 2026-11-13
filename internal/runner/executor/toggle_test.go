package executor

import (
	"errors"
	"testing"

	"github.com/1xxn/getafjob/internal/runner/browser"
)

// A planned yes/no toggle that names a submit button is refused before any click: only the engine's submit path
// may press those.
func TestToggleRefusesSubmitButtons(t *testing.T) {
	st := &browser.State{Frames: []*browser.Frame{{URL: "https://jobs.example.org/apply",
		Buttons: []browser.Button{{ID: "0-1", Text: "Yes", Kind: "submit"}}}}}
	p := Plan{Origins: []string{"https://jobs.example.org"},
		Values: []Value{{FieldID: "0-1", Label: "Yes", Text: "true", Source: "answer#1"}}}
	if _, _, err := (&Executor{}).Fill(st, p); !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
}
