package web

import (
	"testing"
	"time"
)

// A thumbnail asking for 2 frames per second gets at most one frame per half second, and the newest frame held back
// arrives when the gap ends; without a frame rate every frame passes.
func TestFrameGate(t *testing.T) {
	start := time.Unix(0, 0)
	at := func(ms int) time.Time { return start.Add(time.Duration(ms) * time.Millisecond) }

	g := newFrameGate("2")
	defer g.stop()
	if g.tick() == nil {
		t.Fatal("no ticker for a frame rate")
	}
	sent := 0
	for ms := 0; ms < 1000; ms += 40 { // 25 frames per second from the browser
		if g.offer([]byte{byte(ms / 40)}, at(ms)) != nil {
			sent++
		}
	}
	if sent != 2 {
		t.Errorf("sent %d frames in one second, want 2", sent)
	}
	if f := g.flush(at(1100)); f == nil || f[0] != 24 {
		t.Errorf("flush sent %v, want the newest frame 24", f)
	}
	if f := g.flush(at(1700)); f != nil {
		t.Errorf("flush sent %v twice", f)
	}

	for _, fps := range []string{"", "0", "-1", "x", "30", "120"} {
		g := newFrameGate(fps)
		if g.tick() != nil || g.offer([]byte{1}, at(0)) == nil || g.offer([]byte{2}, at(1)) == nil {
			t.Errorf("fps %q drops frames", fps)
		}
	}
}
