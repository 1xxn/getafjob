package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"
)

// Effect is what a click changed. A click without any effect did nothing a person could see.
type Effect struct {
	Navigated  bool     `json:"navigated,omitempty"`   // a frame changed its URL or loaded another document
	NewTargets []string `json:"new_targets,omitempty"` // target IDs of new tabs or popups
	DOM        bool     `json:"dom,omitempty"`         // elements or attributes changed
	Input      bool     `json:"input,omitempty"`       // an input or change event fired (checkboxes, radios)
	Request    bool     `json:"request,omitempty"`     // the page loaded a resource or sent a request
	Scrolled   bool     `json:"scrolled,omitempty"`
	Invalid    []string `json:"invalid,omitempty"` // fields the browser's own validation rejected
}

// Any reports whether the click had a visible effect.
func (e Effect) Any() bool {
	return e.Navigated || len(e.NewTargets) > 0 || e.DOM || e.Input || e.Request || e.Scrolled || len(e.Invalid) > 0
}

// ErrNotVisible reports an element without a visible area (hidden, collapsed or detached).
var ErrNotVisible = errors.New("element is not visible")

// NoEffectError reports a submit that changed nothing, also on the retry; the screenshot shows the page.
type NoEffectError struct {
	Screenshot []byte
}

func (e *NoEffectError) Error() string { return "submit had no effect, also on the retry" }

// probeJS counts what happens in a frame from the first call on: DOM mutations, input and change events,
// scrolling, invalid events (native validation) and loaded resources. It changes nothing on the page.
const probeJS = `() => {
  if (!window.__gajProbe) {
    const s = { dom: 0, input: 0, scroll: 0, invalid: [] };
    new MutationObserver((rs) => { s.dom += rs.length; })
      .observe(document, { subtree: true, childList: true, attributes: true, characterData: true });
    addEventListener("input", () => { s.input++; }, true);
    addEventListener("change", () => { s.input++; }, true);
    addEventListener("scroll", () => { s.scroll++; }, true);
    addEventListener("invalid", (e) => {
      s.invalid.push(e.target.getAttribute("data-gaj") || e.target.name || "");
    }, true);
    window.__gajProbe = s;
  }
  const s = window.__gajProbe;
  return { dom: s.dom, input: s.input, scroll: s.scroll, invalid: s.invalid.slice(),
    href: location.href, origin: performance.timeOrigin,
    res: performance.getEntriesByType("resource").length };
}`

type probe struct {
	DOM     int      `json:"dom"`
	Input   int      `json:"input"`
	Scroll  int      `json:"scroll"`
	Invalid []string `json:"invalid"`
	Href    string   `json:"href"`
	Origin  float64  `json:"origin"`
	Res     int      `json:"res"`
}

// pointJS scrolls an element into the middle of its frame and returns its center in frame coordinates.
var pointJS = `(id) => {
  const el = (` + findJS + `)(id);
  if (!el) return null;
  el.scrollIntoView({ block: "center", inline: "center" });
  const r = el.getBoundingClientRect();
  return { x: r.left + r.width / 2, y: r.top + r.height / 2, w: r.width, h: r.height };
}`

// offsetJS returns where an iframe's content starts in its own frame, after scrolling it into view.
const offsetJS = `function () {
  this.scrollIntoView({ block: "center", inline: "center" });
  const r = this.getBoundingClientRect();
  const cs = getComputedStyle(this);
  return { x: r.left + this.clientLeft + parseFloat(cs.paddingLeft), y: r.top + this.clientTop + parseFloat(cs.paddingTop) };
}`

type point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// run evaluates a JS function with one argument in the frame and returns the value.
func (f *Frame) run(fn string, arg any) (gson.JSON, error) {
	if f.sess != nil {
		res, err := f.sess.eval(call(fn, arg), true)
		if err != nil {
			return gson.JSON{}, err
		}
		return res.Value, nil
	}
	res, err := f.page.Eval(fn, arg)
	if err != nil {
		return gson.JSON{}, err
	}
	return res.Value, nil
}

func (f *Frame) probe() (probe, error) {
	var p probe
	v, err := f.run(probeJS, nil)
	if err != nil {
		return p, err
	}
	return p, decode(v, &p)
}

// decode copies a JS value into a Go value.
func decode(v gson.JSON, out any) error { return json.Unmarshal([]byte(v.JSON("", "")), out) }

// point returns where an element is on the top page. Scrolling inside an out-of-process iframe moves its
// parents asynchronously, so it measures until two results agree.
func (s *State) point(id string) (point, error) {
	prev, err := s.measure(id)
	for range 10 {
		if err != nil {
			return point{}, err
		}
		time.Sleep(50 * time.Millisecond)
		var next point
		if next, err = s.measure(id); err == nil && next == prev {
			return next, nil
		}
		prev = next
	}
	return prev, err
}

// measure scrolls an element and every iframe around it into view and returns its center on the top page.
func (s *State) measure(id string) (point, error) {
	f, err := s.FrameOf(id)
	if err != nil {
		return point{}, err
	}
	var chain []*Frame
	for fr := f; fr.parent != nil; fr = fr.parent {
		chain = append(chain, fr)
	}
	for i := len(chain) - 1; i >= 0; i-- { // outermost iframe first
		if _, err := chain[i].iframe.Eval(offsetJS); err != nil {
			return point{}, err
		}
	}
	v, err := f.run(pointJS, id)
	if err != nil {
		return point{}, err
	}
	if v.Nil() {
		return point{}, fmt.Errorf("%w: %q", ErrUnknownID, id)
	}
	var pt point
	if err := decode(v, &pt); err != nil {
		return point{}, err
	}
	if pt.W < 1 || pt.H < 1 {
		return point{}, fmt.Errorf("%w: %q", ErrNotVisible, id)
	}
	for _, fr := range chain {
		ov, err := fr.iframe.Eval(offsetJS)
		if err != nil {
			return point{}, err
		}
		pt.X += ov.Value.Get("x").Num()
		pt.Y += ov.Value.Get("y").Num()
	}
	return pt, nil
}

// pollEvery is how often a click's effect is checked.
const pollEvery = 100 * time.Millisecond

// Click moves the mouse to an element in any frame (also out-of-process iframes and shadow roots) and clicks
// where a person would. It then watches the element's frame and the top frame for up to wait and reports the
// first effects it sees.
func (s *State) Click(id string, wait time.Duration) (Effect, error) {
	f, err := s.FrameOf(id)
	if err != nil {
		return Effect{}, err
	}
	top := s.Frames[0]
	watched := []*Frame{top}
	if f != top {
		watched = append(watched, f)
	}
	pt, err := s.point(id)
	if err != nil {
		return Effect{}, err
	}
	before := make([]probe, len(watched))
	for i, w := range watched {
		if before[i], err = w.probe(); err != nil {
			return Effect{}, fmt.Errorf("probe frame %d: %w", w.Index, err)
		}
	}

	newTargets, stop := watchTargets(top.page)
	defer stop()
	page := top.page
	if err := page.Mouse.MoveTo(proto.Point{X: pt.X, Y: pt.Y}); err != nil {
		return Effect{}, err
	}
	if err := page.Mouse.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return Effect{}, err
	}

	var e Effect
	deadline := time.Now().Add(wait)
	for {
		time.Sleep(pollEvery)
		e = Effect{NewTargets: newTargets()}
		for i, w := range watched {
			after, err := w.probe()
			if err != nil || after.Origin != before[i].Origin || after.Href != before[i].Href {
				e.Navigated = true // the old document is gone or was replaced
				continue
			}
			e.DOM = e.DOM || after.DOM > before[i].DOM
			e.Input = e.Input || after.Input > before[i].Input
			e.Scrolled = e.Scrolled || after.Scroll > before[i].Scroll
			e.Request = e.Request || after.Res > before[i].Res
			for _, inv := range after.Invalid[len(before[i].Invalid):] {
				if !slices.Contains(e.Invalid, inv) {
					e.Invalid = append(e.Invalid, inv)
				}
			}
		}
		if e.Any() || time.Now().After(deadline) {
			return e, nil
		}
	}
}

// Submit clicks a submit button. Without any effect it tries once more; if that changes nothing either it
// returns a NoEffectError with a screenshot, and the caller parks the application.
func (s *State) Submit(id string, wait time.Duration) (Effect, error) {
	for range 2 {
		e, err := s.Click(id, wait)
		if err != nil || e.Any() {
			return e, err
		}
	}
	shot, err := s.Frames[0].page.Screenshot(false, nil)
	if err != nil {
		return Effect{}, fmt.Errorf("screenshot after a submit without effect: %w", err)
	}
	return Effect{}, &NoEffectError{Screenshot: shot}
}

// watchTargets collects the target IDs of pages (tabs, popups) opened from now on until stop is called; their
// URL is often still empty at that moment.
func watchTargets(p *rod.Page) (list func() []string, stop func()) {
	b := p.Browser()
	_ = proto.TargetSetDiscoverTargets{Discover: true}.Call(b)
	ctx, cancel := context.WithCancel(p.GetContext())
	var mu sync.Mutex
	var urls []string
	wait := b.Context(ctx).EachEvent(func(e *proto.TargetTargetCreated) {
		if e.TargetInfo.Type == proto.TargetTargetInfoTypePage {
			mu.Lock()
			urls = append(urls, string(e.TargetInfo.TargetID))
			mu.Unlock()
		}
	})
	done := make(chan struct{})
	go func() { wait(); close(done) }()
	return func() []string {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(urls)
		}, func() {
			cancel()
			<-done
		}
}
