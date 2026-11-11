package agent

import (
	"context"
	"testing"
)

func TestSlotsFreeWhileRunsWaitButCapBrowsers(t *testing.T) {
	s := newSlots(2)
	ctx := context.WithValue(t.Context(), waitKey{}, s)
	if !s.free() {
		t.Fatal("empty runner must lease")
	}
	s.update(1, 0)
	s.update(1, 0)
	if s.free() {
		t.Fatal("two working runs fill two slots")
	}
	markWaiting(ctx, true)
	if !s.free() {
		t.Fatal("a run waiting for the user frees its slot")
	}
	s.update(1, 0)
	markWaiting(ctx, true)
	s.update(1, 0)
	if s.free() {
		t.Fatal("four open browsers reach the cap of twice the slots")
	}
	markWaiting(ctx, false)
	s.update(-1, 0)
	s.update(-1, 0)
	if !s.free() {
		t.Fatal("finished runs free slots")
	}
	if newSlots(0).parallel != 1 {
		t.Fatal("zero means one slot")
	}
}
