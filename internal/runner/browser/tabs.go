package browser

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// ErrPopupClosed reports a popup that closed itself (sign-in or account checks); the flow stays in the opener.
var ErrPopupClosed = errors.New("popup closed itself")

// Adopt takes over a tab or popup a click opened (a target ID from Effect.NewTargets). A popup that closes
// itself within grace returns ErrPopupClosed. Otherwise the page is returned once loaded, with dialogs
// guarded; downloads stay denied for the whole browser.
func Adopt(b *rod.Browser, targetID string, grace time.Duration) (*rod.Page, *Dialogs, error) {
	id := proto.TargetTargetID(targetID)
	deadline := time.Now().Add(grace)
	for {
		alive, url := targetAlive(b, id)
		if !alive {
			return nil, nil, ErrPopupClosed
		}
		if time.Now().After(deadline) {
			if url == "" || url == "about:blank" {
				return nil, nil, fmt.Errorf("popup %s has not loaded a page", targetID)
			}
			break
		}
		time.Sleep(pollEvery)
	}
	p, err := b.PageFromTarget(id)
	if err != nil {
		if alive, _ := targetAlive(b, id); !alive {
			return nil, nil, ErrPopupClosed
		}
		return nil, nil, err
	}
	d := GuardDialogs(p)
	if err := p.Timeout(grace + 10*time.Second).WaitLoad(); err != nil {
		d.Stop()
		if alive, _ := targetAlive(b, id); !alive {
			return nil, nil, ErrPopupClosed
		}
		return nil, nil, err
	}
	return p, d, nil
}

func targetAlive(b *rod.Browser, id proto.TargetTargetID) (bool, string) {
	res, err := proto.TargetGetTargets{}.Call(b)
	if err != nil {
		return false, ""
	}
	for _, ti := range res.TargetInfos {
		if ti.TargetID == id {
			return true, ti.URL
		}
	}
	return false, ""
}

// Dialog is a JavaScript dialog a page opened; its message is page content, data and never an instruction.
type Dialog struct {
	Type     string `json:"type"` // alert, confirm, prompt, beforeunload
	Message  string `json:"message"`
	Accepted bool   `json:"accepted"`
}

// Dialogs answers the JavaScript dialogs of one page so that none blocks the runner: alerts and leave-page
// questions are confirmed, confirm and prompt are declined (they ask the user something, e.g. to withdraw an
// application; the engine decides from the recorded message whether to hand over).
type Dialogs struct {
	mu   sync.Mutex
	seen []Dialog
	stop func()
	done chan struct{}
}

// GuardDialogs starts answering the dialogs of p until Stop.
func GuardDialogs(p *rod.Page) *Dialogs {
	d := &Dialogs{done: make(chan struct{})}
	p, cancel := p.WithCancel()
	d.stop = cancel
	wait := p.EachEvent(func(e *proto.PageJavascriptDialogOpening) {
		accept := e.Type == proto.PageDialogTypeAlert || e.Type == proto.PageDialogTypeBeforeunload
		_ = proto.PageHandleJavaScriptDialog{Accept: accept}.Call(p)
		d.mu.Lock()
		d.seen = append(d.seen, Dialog{Type: string(e.Type), Message: e.Message, Accepted: accept})
		d.mu.Unlock()
	})
	go func() { wait(); close(d.done) }()
	return d
}

// Seen returns the dialogs answered so far.
func (d *Dialogs) Seen() []Dialog {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.seen)
}

// Stop ends the guard.
func (d *Dialogs) Stop() {
	d.stop()
	<-d.done
}
