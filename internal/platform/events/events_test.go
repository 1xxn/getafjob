package events

import (
	"testing"

	"github.com/google/uuid"
)

func TestHubDeliversPerUserAndReplays(t *testing.T) {
	h := NewHub()
	max, erika := uuid.New(), uuid.New()
	_, mine, cancel := h.Subscribe(max, 0)
	defer cancel()
	_, theirs, cancel2 := h.Subscribe(erika, 0)
	defer cancel2()

	h.Publish(Event{User: max, App: uuid.New(), Event: "queue", From: "matched", To: "queued"})
	first := <-mine
	if first.To != "queued" || first.ID == 0 {
		t.Fatalf("event %+v", first)
	}
	select {
	case e := <-theirs:
		t.Fatalf("other user got %+v", e)
	default:
	}
	h.Publish(Event{User: max, To: "running"})
	h.Publish(Event{User: max, To: "needs_input"})
	past, _, cancel3 := h.Subscribe(max, first.ID)
	defer cancel3()
	if len(past) != 2 || past[0].To != "running" || past[1].ID <= past[0].ID {
		t.Fatalf("replay after %d = %+v", first.ID, past)
	}
	for range replay + 10 {
		h.Publish(Event{User: max})
	}
	if all, _, c := h.Subscribe(max, 1); len(all) != replay {
		t.Fatalf("replay keeps %d, want %d", len(all), replay)
	} else {
		c()
	}
}
