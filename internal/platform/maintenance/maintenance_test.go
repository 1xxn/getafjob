package maintenance

import (
	"strings"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
)

func TestCleanMessage(t *testing.T) {
	if got := CleanMessage("  back at\n18:00\t "); got != "back at 18:00" {
		t.Errorf("got %q", got)
	}
	if got := CleanMessage(strings.Repeat("ä", MaxMessage+10)); len([]rune(got)) != MaxMessage {
		t.Errorf("len = %d", len([]rune(got)))
	}
}

func TestSwitch(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	sw, other := New(pool), New(pool)
	if st, err := sw.State(ctx); err != nil || st.On {
		t.Fatalf("fresh state = %+v, %v", st, err)
	}
	if err := sw.Set(ctx, true, "back at 18:00"); err != nil {
		t.Fatal(err)
	}
	st, err := sw.State(ctx)
	if err != nil || !st.On || st.Message != "back at 18:00" || st.Since.IsZero() {
		t.Fatalf("on = %+v, %v", st, err)
	}
	if err := sw.Set(ctx, true, "later"); err != nil {
		t.Fatal(err)
	}
	if again, _ := sw.State(ctx); again.Message != "later" || !again.Since.Equal(st.Since) {
		t.Errorf("second on = %+v, want message replaced and start kept", again)
	}

	// Another process sees the switch after its cache expires.
	if !other.On(ctx) {
		t.Fatal("other process does not see maintenance")
	}
	if err := sw.Set(ctx, false, ""); err != nil {
		t.Fatal(err)
	}
	if !other.On(ctx) {
		t.Error("cached state expired too early")
	}
	other.now = func() time.Time { return time.Now().Add(cacheFor) }
	if other.On(ctx) {
		t.Error("other process still sees maintenance after the cache expired")
	}
}
