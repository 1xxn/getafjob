package views

import (
	"strings"
	"testing"
)

// A finished application has no live tab: no run will fill it again.
func TestLiveTabOnlyWhileARunMayCome(t *testing.T) {
	has := func(state string) bool {
		for _, tb := range appTabs(t.Context(), AppDetailData{AppID: "a", State: state, Tab: "overview"}) {
			if strings.HasSuffix(tb.Href, "?tab=live") {
				return true
			}
		}
		return false
	}
	for state, want := range map[string]bool{"queued": true, "running": true, "needs_input": true, "parked": true,
		"confirmed": false, "submit_unknown": false, "interview": false} {
		if got := has(state); got != want {
			t.Errorf("%s: live tab %v, want %v", state, got, want)
		}
	}
}
