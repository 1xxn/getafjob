package browser

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

//go:embed extract.js
var extractJS string

// findJS resolves a data-gaj ID, also inside open shadow roots.
const findJS = `(id) => {
  const walk = (root) => {
    const hit = root.querySelector('[data-gaj="' + CSS.escape(id) + '"]');
    if (hit) return hit;
    for (const el of root.querySelectorAll("*")) {
      if (el.shadowRoot) { const f = walk(el.shadowRoot); if (f) return f; }
    }
    return null;
  };
  return walk(document);
}`

// Limits for frame traversal (hostile pages can nest endlessly).
const (
	maxFrames     = 20
	maxFrameDepth = 4
)

// Option is a choice of a select, radio group or combobox.
type Option struct {
	ID      string `json:"id,omitempty"`
	Label   string `json:"label"`
	Value   string `json:"value,omitempty"`
	Checked bool   `json:"checked,omitempty"`
}

// Field is an operable form element.
type Field struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Label        string   `json:"label"`
	Value        string   `json:"value"`
	Required     bool     `json:"required"`
	InputType    string   `json:"input_type,omitempty"`
	Name         string   `json:"name,omitempty"`
	HTMLID       string   `json:"html_id,omitempty"`
	Hint         string   `json:"hint,omitempty"`
	Accept       string   `json:"accept,omitempty"`
	Autocomplete string   `json:"autocomplete,omitempty"`
	Form         string   `json:"form,omitempty"`
	Multiple     bool     `json:"multiple,omitempty"`
	Invalid      bool     `json:"invalid,omitempty"`
	MaxLength    int      `json:"max_length,omitempty"`
	Options      []Option `json:"options,omitempty"`
}

// Button is a button or link that drives the flow.
type Button struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Kind     string `json:"kind"`
	Href     string `json:"href,omitempty"`
	Form     string `json:"form,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	Autofill bool   `json:"autofill,omitempty"`
	Cookie   bool   `json:"cookie,omitempty"`
	Pressed  bool   `json:"pressed,omitempty"`
}

// Captcha is a CAPTCHA signal (widget or challenge frame).
type Captcha struct {
	Src       string `json:"src"`
	Cls       string `json:"cls"`
	Visible   bool   `json:"visible"`
	Challenge bool   `json:"challenge"`
}

// Frame is the extraction result of one frame.
type Frame struct {
	Index        int       `json:"-"`
	URL          string    `json:"url"`
	Title        string    `json:"title"`
	Fields       []Field   `json:"fields"`
	Buttons      []Button  `json:"buttons"`
	Errors       []string  `json:"errors"`
	Captcha      []Captcha `json:"captcha"`
	CaptchaToken bool      `json:"captcha_token,omitempty"`
	Headings     []string  `json:"headings"`
	Text         string    `json:"text"`

	page   *rod.Page // in-process frame of a rod page
	sess   *session  // frame reached over a CDP session (out-of-process iframes and the frames inside them)
	parent *Frame    // nil for the top frame
	iframe iframeRef // the iframe element in the parent frame
}

// iframeRef is an iframe element in its parent frame.
type iframeRef interface {
	// offset scrolls the iframe into view and returns where its content starts in the parent frame.
	offset() (x, y float64, err error)
}

type rodIframe struct{ el *rod.Element }

func (r rodIframe) offset() (float64, float64, error) {
	v, err := r.el.Eval(offsetJS)
	if err != nil {
		return 0, 0, err
	}
	return v.Value.Get("x").Num(), v.Value.Get("y").Num(), nil
}

type sessionIframe struct {
	s   *session
	obj proto.RuntimeRemoteObjectID
}

func (r sessionIframe) offset() (float64, float64, error) {
	res, err := proto.RuntimeCallFunctionOn{FunctionDeclaration: offsetJS, ObjectID: r.obj, ReturnByValue: true}.Call(r.s)
	if err != nil {
		return 0, 0, err
	}
	if res.ExceptionDetails != nil {
		return 0, 0, fmt.Errorf("js exception: %s", res.ExceptionDetails.Text)
	}
	return res.Result.Value.Get("x").Num(), res.Result.Value.Get("y").Num(), nil
}

// State is the extraction result of a page and all its frames (index 0 = top).
type State struct {
	Frames []*Frame
}

// ErrUnknownID reports an element ID that is not on the page (any more).
var ErrUnknownID = errors.New("unknown element id")

// session talks to an out-of-process iframe over its own CDP session. go-rod's Element.Frame and
// Browser.PageFromTarget only handle in-process frames and top-level targets.
type session struct {
	b   *rod.Browser
	id  proto.TargetSessionID
	ctx context.Context
	// world is the execution context of a same-site frame inside an out-of-process iframe (an isolated world
	// created for it); 0 means the main frame of the session.
	world proto.RuntimeExecutionContextID
}

func (s *session) Call(ctx context.Context, sessionID, method string, params any) ([]byte, error) {
	return s.b.Call(ctx, sessionID, method, params)
}

func (s *session) GetSessionID() proto.TargetSessionID { return s.id }
func (s *session) GetContext() context.Context         { return s.ctx }

// eval runs a JS expression in the iframe's main world.
func (s *session) eval(expr string, byValue bool) (*proto.RuntimeRemoteObject, error) {
	res, err := proto.RuntimeEvaluate{Expression: expr, ReturnByValue: byValue, ContextID: s.world}.Call(s)
	if err != nil {
		return nil, err
	}
	if res.ExceptionDetails != nil {
		return nil, fmt.Errorf("js exception: %s", res.ExceptionDetails.Text)
	}
	return res.Result, nil
}

// Extract reads every frame of p, including nested and out-of-process iframes and open shadow roots.
func Extract(p *rod.Page) (*State, error) {
	s := &State{}
	if err := s.extract(p, nil, nil, nil, 0); err != nil {
		return nil, err
	}
	return s, nil
}

func call(fn string, arg any) string {
	b, _ := json.Marshal(arg)
	return "(" + fn + ")(" + string(b) + ")"
}

// evalExtract runs the extraction script; a panic inside go-rod (seen with unusual frames) becomes an error.
func evalExtract(p *rod.Page, sess *session, idx int) (res *proto.RuntimeRemoteObject, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("eval frame %d: %v", idx, r)
		}
	}()
	if sess != nil {
		return sess.eval(call(extractJS, idx), true)
	}
	return p.Eval(extractJS, idx)
}

// childFrame opens an iframe: in-process frames as rod pages, out-of-process iframes (other site, site
// isolation) as CDP sessions on their own target, whose ID equals the frame ID.
func childFrame(p *rod.Page, el *rod.Element) (*rod.Page, *session, error) {
	node, err := el.Describe(1, false)
	if err != nil {
		return nil, nil, err
	}
	if node.FrameID != "" {
		targets, err := proto.TargetGetTargets{}.Call(p.Browser())
		if err == nil {
			for _, ti := range targets.TargetInfos {
				if ti.Type == "iframe" && string(ti.TargetID) == string(node.FrameID) {
					res, err := proto.TargetAttachToTarget{TargetID: ti.TargetID, Flatten: true}.Call(p.Browser())
					if err != nil {
						return nil, nil, err
					}
					return nil, &session{b: p.Browser(), id: res.SessionID, ctx: p.GetContext()}, nil
				}
			}
		}
	}
	fp, err := el.Frame()
	return fp, nil, err
}

func (s *State) extract(p *rod.Page, sess *session, parent *Frame, iframe iframeRef, depth int) error {
	idx := len(s.Frames)
	res, err := evalExtract(p, sess, idx)
	if err != nil {
		if idx == 0 {
			return fmt.Errorf("extract top frame: %w", err)
		}
		return nil // detached or not yet loaded frame: skip
	}
	f := &Frame{Index: idx, page: p, sess: sess, parent: parent, iframe: iframe}
	if err := res.Value.Unmarshal(f); err != nil {
		return fmt.Errorf("decode frame %d: %w", idx, err)
	}
	s.Frames = append(s.Frames, f)
	if depth >= maxFrameDepth {
		return nil
	}
	if sess != nil {
		return s.extractSessionChildren(f, sess, depth)
	}
	iframes, _ := p.Elements("iframe") // a frame that navigates away mid-read has none to follow
	for _, el := range iframes {
		if len(s.Frames) >= maxFrames {
			return nil
		}
		fp, fs, err := childFrame(p, el)
		if err != nil {
			continue
		}
		if err := s.extract(fp, fs, f, rodIframe{el}, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// extractSessionChildren follows the iframes of a frame reached over a CDP session: another site gets its own
// target, a same-site frame an isolated world in the same session.
func (s *State) extractSessionChildren(f *Frame, sess *session, depth int) error {
	for _, obj := range sess.iframes() {
		if len(s.Frames) >= maxFrames {
			return nil
		}
		child, err := sess.childFrame(obj)
		if err != nil {
			continue // not loaded yet or gone
		}
		if err := s.extract(nil, child, f, sessionIframe{sess, obj}, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// iframes returns the iframe elements of the session's frame; none if the frame cannot be read (navigating).
func (sess *session) iframes() []proto.RuntimeRemoteObjectID {
	list, err := sess.eval(`Array.from(document.querySelectorAll("iframe"))`, false)
	if err != nil || list.ObjectID == "" {
		return nil
	}
	props, err := proto.RuntimeGetProperties{ObjectID: list.ObjectID, OwnProperties: true}.Call(sess)
	if err != nil {
		return nil
	}
	var out []proto.RuntimeRemoteObjectID
	for _, prop := range props.Result {
		if prop.Value != nil && prop.Value.ObjectID != "" && prop.Value.Subtype == proto.RuntimeRemoteObjectSubtypeNode {
			out = append(out, prop.Value.ObjectID)
		}
	}
	return out
}

// childFrame opens the frame of an iframe element of the session.
func (sess *session) childFrame(obj proto.RuntimeRemoteObjectID) (*session, error) {
	node, err := proto.DOMDescribeNode{ObjectID: obj}.Call(sess)
	if err != nil {
		return nil, err
	}
	frameID := node.Node.FrameID
	if frameID == "" {
		return nil, errors.New("iframe without frame")
	}
	targets, err := proto.TargetGetTargets{}.Call(sess.b)
	if err != nil {
		return nil, err
	}
	for _, ti := range targets.TargetInfos {
		if ti.Type == "iframe" && string(ti.TargetID) == string(frameID) {
			res, err := proto.TargetAttachToTarget{TargetID: ti.TargetID, Flatten: true}.Call(sess.b)
			if err != nil {
				return nil, err
			}
			return &session{b: sess.b, id: res.SessionID, ctx: sess.ctx}, nil
		}
	}
	world, err := proto.PageCreateIsolatedWorld{FrameID: frameID, WorldName: "getafjob"}.Call(sess)
	if err != nil {
		return nil, err
	}
	return &session{b: sess.b, id: sess.id, ctx: sess.ctx, world: world.ExecutionContextID}, nil
}

// Fields returns all fields of all frames.
func (s *State) Fields() []Field {
	var out []Field
	for _, f := range s.Frames {
		out = append(out, f.Fields...)
	}
	return out
}

// Buttons returns all buttons of all frames.
func (s *State) Buttons() []Button {
	var out []Button
	for _, f := range s.Frames {
		out = append(out, f.Buttons...)
	}
	return out
}

// FrameOf returns the frame an element ID belongs to.
func (s *State) FrameOf(id string) (*Frame, error) {
	prefix, _, ok := strings.Cut(id, "-")
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownID, id)
	}
	n, err := strconv.Atoi(prefix)
	if err != nil || n < 0 || n >= len(s.Frames) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownID, id)
	}
	return s.Frames[n], nil
}

// ErrOutOfProcess reports an element in an out-of-process iframe, which has no rod.Element.
var ErrOutOfProcess = errors.New("element is in an out-of-process iframe")

// Element resolves an element ID from the extraction, also inside shadow roots (in-process frames only).
func (s *State) Element(id string) (*rod.Element, error) {
	f, err := s.FrameOf(id)
	if err != nil {
		return nil, err
	}
	if f.sess != nil {
		return nil, fmt.Errorf("%w: %q", ErrOutOfProcess, id)
	}
	el, err := f.page.Sleeper(rod.NotFoundSleeper).ElementByJS(rod.Eval(findJS, id))
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrUnknownID, id, err)
	}
	return el, nil
}

// Focus focuses an element in any frame; keyboard input on the top page then reaches it.
func (s *State) Focus(id string) error {
	f, err := s.FrameOf(id)
	if err != nil {
		return err
	}
	if f.sess == nil {
		el, err := s.Element(id)
		if err != nil {
			return err
		}
		return el.Focus()
	}
	obj, err := f.sess.eval(call(findJS, id), false)
	if err != nil {
		return err
	}
	if obj.ObjectID == "" {
		return fmt.Errorf("%w: %q", ErrUnknownID, id)
	}
	return proto.DOMFocus{ObjectID: obj.ObjectID}.Call(f.sess)
}
