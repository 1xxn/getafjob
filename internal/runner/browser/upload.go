package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// MaxUpload bounds one file; documents are far smaller.
const MaxUpload = 20 << 20

// Upload errors.
var (
	ErrNotADocument  = errors.New("not one of the user's documents")
	ErrNotFileField  = errors.New("not a file field")
	ErrNoFileChooser = errors.New("the drop zone opened no file chooser")
	ErrUploadMissed  = errors.New("the field did not take the files")
)

// Documents is the directory with the user's documents for one run. Uploads come only from here: plain files
// directly inside it, never links, other directories or anything a page offers.
type Documents struct {
	root string
}

// NewDocuments opens dir as the run's document directory.
func NewDocuments(dir string) (*Documents, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("documents %q: not a directory", dir)
	}
	return &Documents{root: real}, nil
}

// path returns the absolute path of a document by its file name.
func (d *Documents) path(name string) (string, error) {
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("%w: %q", ErrNotADocument, name)
	}
	p := filepath.Join(d.root, name)
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() { // no symlinks, devices or directories
		return "", fmt.Errorf("%w: %q", ErrNotADocument, name)
	}
	if fi.Size() > MaxUpload {
		return "", fmt.Errorf("%w: %q is larger than %d bytes", ErrNotADocument, name, MaxUpload)
	}
	return p, nil
}

// uploadJS inspects a file field: visible inputs take files directly; a hidden one sits in a drop zone, whose
// nearest visible ancestor (or its trigger button or label) gets the ID zoneID to be clicked.
var uploadJS = `(arg) => {
  const el = (` + findJS + `)(arg.id);
  if (!el || el.tagName !== "INPUT" || el.type !== "file") return null;
  const shown = (e) => { const r = e.getBoundingClientRect(); const cs = getComputedStyle(e);
    return r.width >= 1 && r.height >= 1 && cs.visibility !== "hidden" && cs.display !== "none"; };
  if (shown(el)) return { direct: true, multiple: el.multiple };
  let zone = el.id ? document.querySelector('label[for="' + CSS.escape(el.id) + '"]') : null;
  if (!zone || !shown(zone)) {
    zone = el.parentElement;
    while (zone && !shown(zone)) zone = zone.parentElement;
    if (zone) {
      const btn = zone.querySelector('button, [role="button"], label');
      if (btn && shown(btn)) zone = btn;
    }
  }
  if (!zone) return { direct: true, multiple: el.multiple };
  zone.setAttribute("data-gaj", arg.zone);
  return { direct: false, multiple: el.multiple };
}`

// filesJS lists the names of the files a field holds.
const filesJS = `function () { return Array.from(this.files || [], (f) => f.name); }`

// cdp is what proto requests are sent through: a rod page or an out-of-process iframe session.
type cdp interface {
	Call(ctx context.Context, sessionID, method string, params any) ([]byte, error)
	GetSessionID() proto.TargetSessionID
	GetContext() context.Context
}

func (f *Frame) client() cdp {
	if f.sess != nil {
		return f.sess
	}
	return f.page
}

// Upload puts documents into a file field of any frame. A visible input takes them directly; a hidden input
// inside a drop zone gets them through the zone's own file chooser, as if a person picked them, because many
// zones ignore files set from outside. It checks that the field holds exactly these files.
func (s *State) Upload(docs *Documents, fieldID string, names []string, wait time.Duration) error {
	if docs == nil || len(names) == 0 {
		return fmt.Errorf("%w: nothing to upload", ErrNotADocument)
	}
	paths := make([]string, len(names))
	for i, n := range names {
		p, err := docs.path(n)
		if err != nil {
			return err
		}
		paths[i] = p
	}
	f, err := s.FrameOf(fieldID)
	if err != nil {
		return err
	}
	zoneID := fieldID + "-zone"
	v, err := f.run(uploadJS, map[string]string{"id": fieldID, "zone": zoneID})
	if err != nil {
		return err
	}
	if v.Nil() {
		return fmt.Errorf("%w: %q", ErrNotFileField, fieldID)
	}
	var info struct {
		Direct   bool `json:"direct"`
		Multiple bool `json:"multiple"`
	}
	if err := decode(v, &info); err != nil {
		return err
	}
	if len(paths) > 1 && !info.Multiple {
		return fmt.Errorf("%w: %q takes one file", ErrNotFileField, fieldID)
	}

	c := f.client()
	obj, err := f.object(fieldID)
	if err != nil {
		return err
	}
	var node proto.DOMBackendNodeID
	if !info.Direct {
		node, err = s.chooseFiles(f, zoneID, paths, min(wait, 1500*time.Millisecond))
		if err != nil && !errors.Is(err, ErrNoFileChooser) {
			return err
		}
	}
	if info.Direct || node == 0 {
		if err := (proto.DOMSetFileInputFiles{Files: paths, ObjectID: obj}).Call(c); err != nil {
			return err
		}
	}
	return s.checkFiles(f, obj, node, names)
}

// object returns the remote object of an element of the frame.
func (f *Frame) object(id string) (proto.RuntimeRemoteObjectID, error) {
	if f.sess != nil {
		obj, err := f.sess.eval(call(findJS, id), false)
		if err != nil {
			return "", err
		}
		if obj.ObjectID == "" {
			return "", fmt.Errorf("%w: %q", ErrUnknownID, id)
		}
		return obj.ObjectID, nil
	}
	el, err := f.page.Sleeper(rod.NotFoundSleeper).ElementByJS(rod.Eval(findJS, id))
	if err != nil {
		return "", fmt.Errorf("%w: %q: %w", ErrUnknownID, id, err)
	}
	return el.Object.ObjectID, nil
}

// chooseFiles clicks the drop zone with the file chooser intercepted and answers it with paths.
func (s *State) chooseFiles(f *Frame, zoneID string, paths []string, wait time.Duration) (proto.DOMBackendNodeID, error) {
	c := f.client()
	if err := (proto.PageEnable{}).Call(c); err != nil {
		return 0, err
	}
	if err := (proto.PageSetInterceptFileChooserDialog{Enabled: true}).Call(c); err != nil {
		return 0, err
	}
	defer func() { _ = (proto.PageSetInterceptFileChooserDialog{Enabled: false}).Call(c) }()

	ctx, cancel := context.WithTimeout(s.Frames[0].page.GetContext(), wait)
	defer cancel()
	opened := make(chan proto.DOMBackendNodeID, 1)
	sid := c.GetSessionID()
	b := s.Frames[0].page.Browser()
	listen := b.Context(ctx).EachEvent(func(e *proto.PageFileChooserOpened, session proto.TargetSessionID) bool {
		if session != sid {
			return false
		}
		opened <- e.BackendNodeID
		return true
	})
	done := make(chan struct{})
	go func() { listen(); close(done) }()
	defer func() { cancel(); <-done }()

	// The zone ID carries the field's frame prefix, so Click finds it in the same frame.
	if _, err := s.Click(zoneID, 300*time.Millisecond); err != nil {
		return 0, err
	}
	select {
	case node := <-opened:
		if err := (proto.DOMSetFileInputFiles{Files: paths, BackendNodeID: node}).Call(c); err != nil {
			return 0, err
		}
		return node, nil
	case <-ctx.Done():
		return 0, ErrNoFileChooser
	}
}

// checkFiles verifies that the field (or the input the chooser belonged to) holds exactly names.
func (s *State) checkFiles(f *Frame, obj proto.RuntimeRemoteObjectID, node proto.DOMBackendNodeID, names []string) error {
	if node != 0 {
		res, err := proto.DOMResolveNode{BackendNodeID: node}.Call(f.client())
		if err != nil {
			return err
		}
		obj = res.Object.ObjectID
	}
	res, err := proto.RuntimeCallFunctionOn{FunctionDeclaration: filesJS, ObjectID: obj, ReturnByValue: true}.Call(f.client())
	if err != nil {
		return err
	}
	var got []string
	if err := decode(res.Result.Value, &got); err != nil {
		return err
	}
	if !slices.Equal(got, names) {
		return fmt.Errorf("%w: has %q, want %q", ErrUploadMissed, got, names)
	}
	return nil
}
