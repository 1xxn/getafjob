//go:build e2e

// Package e2e drives the running stack (go tool task test:e2e) with the runner's Chromium: sandbox and site
// isolation on, no certificate errors ignored. Chromium reaches Caddy as https://localhost through a host resolver
// rule and trusts only the key of Caddy's local intermediate CA (SPKI pin). One invitation signs in a fresh user
// per run; every journey opens its own tab and fails on console errors, CSP violations and script exceptions.
package e2e

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/1xxn/getafjob/internal/runner/browser"
)

var (
	base   = "https://localhost"
	outDir string
	shared *browser.Browser
)

func TestMain(m *testing.M) {
	invite := os.Getenv("GETAFJOB_E2E_INVITE")
	if invite == "" {
		_, _ = fmt.Fprintln(os.Stderr, "e2e: GETAFJOB_E2E_INVITE not set, run through go tool task test:e2e")
		os.Exit(0)
	}
	code, err := run(m, invite)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "e2e:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func run(m *testing.M, invite string) (int, error) {
	if v := os.Getenv("GETAFJOB_E2E_BASE"); v != "" {
		base = strings.TrimRight(v, "/")
	}
	outDir = os.Getenv("GETAFJOB_E2E_OUT")
	if outDir == "" {
		outDir = filepath.Join(os.TempDir(), "getafjob-e2e")
	}
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return 1, err
	}
	pin, err := spkiPin(os.Getenv("GETAFJOB_E2E_CA"))
	if err != nil {
		return 1, err
	}
	host := os.Getenv("GETAFJOB_E2E_HOST")
	if host == "" {
		host = "caddy"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	b, err := browser.Launch(ctx, browser.Options{
		Bin: os.Getenv("GETAFJOB_BROWSER_BIN"),
		ExtraFlags: map[string]string{
			"host-resolver-rules":                 "MAP localhost " + host,
			"ignore-certificate-errors-spki-list": pin,
		},
	})
	if err != nil {
		return 1, err
	}
	shared = b
	defer func() { _ = b.Close(context.Background()) }()
	if err := signIn(shared.Browser, invite); err != nil {
		return 1, fmt.Errorf("sign in: %w", err)
	}
	code := m.Run()
	if demoBrowser != nil {
		saveDemo(demoBrowser) // the session cookie rotates while it is used
	}
	return code, nil
}

// spkiPin returns the base64 SHA-256 of the certificate's public key, the form Chromium's SPKI list expects.
func spkiPin(path string) (string, error) {
	if path == "" {
		return "", errors.New("GETAFJOB_E2E_CA (Caddy's intermediate certificate) not set")
	}
	raw, err := os.ReadFile(path) // test input path from the task
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return "", errors.New("GETAFJOB_E2E_CA: no PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}

// signIn accepts the invitation with a display name; the session cookie stays in the shared browser profile.
func signIn(b *rod.Browser, invite string) error {
	u, err := url.Parse(invite)
	if err != nil {
		return err
	}
	p, err := b.Page(proto.TargetCreateTarget{})
	if err != nil {
		return err
	}
	defer func() { _ = p.Close() }()
	p = p.Timeout(30 * time.Second)
	if err := p.Navigate(base + u.RequestURI()); err != nil {
		return err
	}
	name, err := p.Element(`input[name="name"]`)
	if err != nil {
		return err
	}
	if err := name.Input("Erika Musterfrau"); err != nil {
		return err
	}
	submit, err := p.Element(`main form button[type="submit"]`)
	if err != nil {
		return err
	}
	wait := p.WaitNavigation(proto.PageLifecycleEventNameNetworkAlmostIdle)
	if err := submit.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return err
	}
	wait()
	if _, err := p.Element(`body.shell`); err != nil {
		return fmt.Errorf("no app shell after the invitation: %w", err)
	}
	return nil
}

// actionTimeout bounds one step of a journey (navigation, element lookup, script).
const actionTimeout = 30 * time.Second

// Tab is one browser tab of a journey with the problems it saw.
type Tab struct {
	t    *testing.T
	Page *rod.Page

	mu       sync.Mutex
	problems []string
	status   int
}

// Open starts a journey tab of the invited user at a viewport size and color scheme; problems fail the test at
// its end.
func Open(t *testing.T, width, height int, scheme string) *Tab {
	t.Helper()
	return OpenIn(t, shared.Browser, width, height, scheme)
}

// OpenIn starts a journey tab in a browser context, e.g. the demo user's (Demo).
func OpenIn(t *testing.T, b *rod.Browser, width, height int, scheme string) *Tab {
	t.Helper()
	p, err := b.Page(proto.TargetCreateTarget{})
	if err != nil {
		t.Fatal(err)
	}
	tab := &Tab{t: t, Page: p}
	must(t, proto.RuntimeEnable{}.Call(p))
	must(t, proto.LogEnable{}.Call(p))
	must(t, proto.NetworkEnable{}.Call(p))
	// CSP reports in the log name no element, and htmx errors no request; these lines do (test only, added by the
	// harness).
	_, err = proto.PageAddScriptToEvaluateOnNewDocument{Source: `document.addEventListener('securitypolicyviolation', e => {
		const el = e.target && e.target.outerHTML ? e.target.outerHTML.slice(0, 160) : String(e.target);
		console.warn('csp-detail', e.violatedDirective, e.sourceFile + ':' + e.lineNumber, 'sample=' + e.sample, el);
	});
	let leaving = '';
	addEventListener('beforeunload', () => { leaving = 'beforeunload'; });
	addEventListener('pagehide', () => { leaving += '+pagehide'; });
	document.addEventListener('htmx:error', e => {
		const r = e.detail && e.detail.ctx && e.detail.ctx.request;
		console.warn('htmx-detail', r ? r.method + ' ' + r.action : '(no request)', 'page=' + location.pathname + location.search,
			'leaving=' + (leaving || 'no'), 'aborted=' + (r && r.signal ? r.signal.aborted : '?'));
	});`}.Call(p)
	must(t, err)
	go p.EachEvent(
		func(e *proto.RuntimeConsoleAPICalled) {
			switch {
			case e.Type == proto.RuntimeConsoleAPICalledTypeError:
				tab.problem("console: " + consoleText(e))
			case e.Type == proto.RuntimeConsoleAPICalledTypeWarning && (strings.HasPrefix(consoleText(e), "csp-detail") ||
				strings.HasPrefix(consoleText(e), "htmx-detail")):
				tab.problem(consoleText(e))
			}
		},
		func(e *proto.LogEntryAdded) {
			if e.Entry.Level == proto.LogLogEntryLevelError {
				tab.problem(fmt.Sprintf("%s: %s %s", e.Entry.Source, e.Entry.Text, e.Entry.URL))
			}
		},
		func(e *proto.RuntimeExceptionThrown) {
			tab.problem("exception: " + e.ExceptionDetails.Text + " " + exceptionText(e))
		},
		func(e *proto.NetworkResponseReceived) {
			if e.Type == proto.NetworkResourceTypeDocument {
				tab.mu.Lock()
				tab.status = e.Response.Status
				tab.mu.Unlock()
			}
		},
	)()
	must(t, p.SetViewport(&proto.EmulationSetDeviceMetricsOverride{Width: width, Height: height,
		DeviceScaleFactor: 1, Mobile: width < 768}))
	if scheme != "" {
		must(t, proto.EmulationSetEmulatedMedia{Features: []*proto.EmulationMediaFeature{
			{Name: "prefers-color-scheme", Value: scheme}}}.Call(p))
	}
	t.Cleanup(func() {
		tab.mu.Lock()
		defer tab.mu.Unlock()
		for _, pr := range tab.problems {
			t.Error(pr)
		}
		_ = p.Close()
	})
	return tab
}

func (tab *Tab) problem(s string) {
	tab.mu.Lock()
	tab.problems = append(tab.problems, s)
	tab.mu.Unlock()
}

// Go opens a path with a full page load and checks the document status.
func (tab *Tab) Go(path string) {
	tab.t.Helper()
	p := tab.Page.Timeout(actionTimeout)
	// A navigation through DevTools skips beforeunload, which a link click fires; the app aborts its running requests
	// there (live parts, the bell). Fire it, so a request cut by the navigation is not reported as an error.
	_, _ = p.Eval(`() => { window.dispatchEvent(new Event('beforeunload')); }`)
	wait := p.WaitNavigation(proto.PageLifecycleEventNameNetworkAlmostIdle)
	must(tab.t, p.Navigate(base+path))
	wait()
	// A cross-document view transition covers 100vw for a moment; measure layout after it.
	if _, err := p.Eval(`() => document.activeViewTransition?.finished.catch(() => {})`); err != nil {
		tab.t.Fatalf("%s: %v", path, err)
	}
	tab.mu.Lock()
	st := tab.status
	tab.mu.Unlock()
	if st != 200 {
		tab.t.Fatalf("%s: status %d", path, st)
	}
}

// El waits for an element.
func (tab *Tab) El(selector string) *rod.Element {
	tab.t.Helper()
	el, err := tab.Page.Timeout(actionTimeout).Element(selector)
	if err != nil {
		tab.t.Fatalf("%s: %v", selector, err)
	}
	return el
}

// Eval runs a JavaScript expression (a function body returning a value) and returns its JSON result as text.
func (tab *Tab) Eval(js string) string {
	tab.t.Helper()
	res, err := tab.Page.Timeout(actionTimeout).Eval("() => JSON.stringify((() => { " + js + " })())")
	if err != nil {
		tab.t.Fatalf("eval: %v", err)
	}
	return res.Value.Str()
}

// EvalAsync is Eval for an async function body (await allowed); it waits for the result.
func (tab *Tab) EvalAsync(js string) string {
	tab.t.Helper()
	res, err := tab.Page.Timeout(actionTimeout).Eval("() => (async () => { " + js + " })().then(v => JSON.stringify(v))")
	if err != nil {
		tab.t.Fatalf("eval: %v", err)
	}
	return res.Value.Str()
}

// NoOverflow fails the test when the page scrolls sideways and names the elements that stick out.
func (tab *Tab) NoOverflow(what string) {
	tab.t.Helper()
	got := tab.Eval(`const w = document.documentElement.clientWidth;
		if (document.documentElement.scrollWidth <= w) return '';
		return [...document.querySelectorAll('body *')].filter(el => {
			const r = el.getBoundingClientRect();
			if (r.width === 0 || r.right <= w + 1) return false;
			for (let a = el; a; a = a.parentElement) {
				const cs = getComputedStyle(a);
				if (cs.position === 'fixed') return false;
				if (a !== el && a !== document.body && a !== document.documentElement && cs.overflowX !== 'visible') return false; // clipped or scrolls on its own
			}
			return true;
		}).slice(0, 5).map(el => el.tagName.toLowerCase() + (el.id ? '#' + el.id : '') + '.' + [...el.classList].join('.')).join(', ') || 'unknown'`)
	if got != `""` {
		tab.Shot("overflow")
		tab.t.Errorf("%s: horizontal overflow from %s", what, got)
	}
}

// Shot saves a screenshot of the viewport under the test's name.
func (tab *Tab) Shot(name string) {
	tab.t.Helper()
	img, err := tab.Page.Timeout(actionTimeout).Screenshot(false, &proto.PageCaptureScreenshot{Format: proto.PageCaptureScreenshotFormatPng})
	if err != nil {
		tab.t.Errorf("screenshot %s: %v", name, err)
		return
	}
	file := filepath.Join(outDir, strings.NewReplacer("/", "_", " ", "_").Replace(tab.t.Name()+"_"+name)+".png")
	// Screenshots of test data; the CI artifact upload reads them as another user than the test container.
	if err := os.WriteFile(file, img, 0o644); err != nil { // test output, readable by the maintainer
		tab.t.Errorf("screenshot %s: %v", name, err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func consoleText(e *proto.RuntimeConsoleAPICalled) string {
	var parts []string
	for _, a := range e.Args {
		if s := a.Value.Str(); s != "" {
			parts = append(parts, s)
		} else if a.Description != "" {
			parts = append(parts, a.Description)
		}
	}
	return strings.Join(parts, " ")
}

func exceptionText(e *proto.RuntimeExceptionThrown) string {
	if e.ExceptionDetails.Exception != nil {
		return e.ExceptionDetails.Exception.Description
	}
	return ""
}
