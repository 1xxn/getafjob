package browser

import (
	"errors"
	"strings"
	"testing"
)

func TestFrameOf(t *testing.T) {
	s := &State{Frames: []*Frame{{Index: 0}, {Index: 1}}}
	for id, want := range map[string]int{"0-1": 0, "1-17": 1} {
		f, err := s.FrameOf(id)
		if err != nil || f.Index != want {
			t.Errorf("FrameOf(%q) = %v, %v", id, f, err)
		}
	}
	for _, id := range []string{"", "x", "2-1", "-1-3", "a-1"} {
		if _, err := s.FrameOf(id); !errors.Is(err, ErrUnknownID) {
			t.Errorf("FrameOf(%q) err = %v", id, err)
		}
	}
}

func TestCallQuotesArguments(t *testing.T) {
	got := call("(x) => x", `a"b</script>`)
	if !strings.HasPrefix(got, "((x) => x)(") || strings.Contains(got, `a"b`) {
		t.Fatalf("call = %s", got)
	}
}

func TestExtractScriptIsAFunction(t *testing.T) {
	if !strings.Contains(extractJS, "(frameIndex) =>") || !strings.Contains(extractJS, "data-gaj") {
		t.Fatal("extract.js not embedded")
	}
}
