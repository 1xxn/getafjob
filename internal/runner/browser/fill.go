package browser

import (
	"fmt"
	"net/url"

	"github.com/go-rod/rod/lib/proto"
)

// focusJS focuses an element and selects its content, so that inserted text replaces it.
var focusJS = `(id) => {
  const el = (` + findJS + `)(id);
  if (!el) return false;
  el.focus();
  if (typeof el.select === "function") el.select();
  else if (el.isContentEditable) {
    const r = document.createRange(); r.selectNodeContents(el);
    const sel = getSelection(); sel.removeAllRanges(); sel.addRange(r);
  }
  return document.activeElement === el || el.contains(document.activeElement) ||
    (el.getRootNode().activeElement === el);
}`

// setJS sets the value of an input, textarea or select through the native setter (frameworks like React watch
// it) and fires input and change.
var setJS = `(arg) => {
  const el = (` + findJS + `)(arg.id);
  if (!el) return false;
  const proto = el.tagName === "SELECT" ? HTMLSelectElement.prototype
    : el.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, "value").set.call(el, arg.value);
  el.dispatchEvent(new Event("input", { bubbles: true }));
  el.dispatchEvent(new Event("change", { bubbles: true }));
  return true;
}`

// Type replaces the content of a text field in any frame with text, as a paste would: the field gets focus,
// its content is selected and the text is inserted in one step (no simulated keystrokes). Empty text clears it.
func (s *State) Type(id, text string) error {
	f, err := s.FrameOf(id)
	if err != nil {
		return err
	}
	if text == "" {
		return s.set(f, id, "")
	}
	v, err := f.run(focusJS, id)
	if err != nil {
		return err
	}
	if !v.Bool() {
		return fmt.Errorf("%w: %q did not take the focus", ErrUnknownID, id)
	}
	return proto.InputInsertText{Text: text}.Call(s.Frames[0].page)
}

// SetValue sets a select (by option value) or a date or time input (ISO form), which cannot be typed into
// reliably across locales.
func (s *State) SetValue(id, value string) error {
	f, err := s.FrameOf(id)
	if err != nil {
		return err
	}
	return s.set(f, id, value)
}

func (s *State) set(f *Frame, id, value string) error {
	v, err := f.run(setJS, map[string]string{"id": id, "value": value})
	if err != nil {
		return err
	}
	if !v.Bool() {
		return fmt.Errorf("%w: %q", ErrUnknownID, id)
	}
	return nil
}

// Refresh extracts the page again; element IDs of unchanged elements stay the same.
func (s *State) Refresh() (*State, error) { return Extract(s.Frames[0].page) }

// Field returns the field with an ID.
func (s *State) Field(id string) (Field, bool) {
	f, err := s.FrameOf(id)
	if err != nil {
		return Field{}, false
	}
	for _, fd := range f.Fields {
		if fd.ID == id {
			return fd, true
		}
	}
	return Field{}, false
}

// Origin returns the origin (scheme://host[:port]) whose content the frame shows; about:blank, about:srcdoc
// and similar frames show their parent's.
func (f *Frame) Origin() string {
	u, err := url.Parse(f.URL)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	if f.parent != nil {
		return f.parent.Origin()
	}
	return ""
}
