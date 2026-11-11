package agent

import (
	"context"
	"testing"

	"github.com/1xxn/getafjob/internal/runner/engine"
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

func TestNoFormReason(t *testing.T) {
	if r := noFormReason("https://www.arbeitsagentur.de/jobsuche/jobdetail/1", engine.ErrNoForm); r !=
		"external: www.arbeitsagentur.de shows the posting but no application form; apply there or mark it as applied" {
		t.Errorf("portal: %q", r)
	}
	if r := noFormReason("https://acme.example/careers/1", engine.ErrNoForm); r != "no application form found on acme.example" {
		t.Errorf("other: %q", r)
	}
}
