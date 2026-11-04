// Package browser starts Chromium for the runner, without disguise and with the sandbox on.
//
// go-rod's launcher defaults do not fit our threat model and are overridden explicitly here
// (see docs/adr/0011-browser-automation-go-rod.md):
//   - inside containers go-rod adds --no-sandbox → removed, the sandbox stays on (seccomp profile in Compose);
//   - go-rod turns site isolation off (disable-features=site-per-process) → back on, pages are
//     untrusted;
//   - leakless unpacks a helper binary into the temp dir → off, we stop the process ourselves;
//   - proxy without a loopback exception, so pages reach neither the runner itself nor the CDP port.
package browser

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
	"github.com/go-rod/rod/lib/proto"
)

// Options control the launch.
type Options struct {
	// Bin is the browser path; empty = look for an installed Chrome/Chromium/Edge (development only).
	Bin string
	// Proxy is the egress proxy; nil = direct (development and tests only).
	Proxy *url.URL
	// Show opens a visible window (local debugging).
	Show bool
	// ExtraFlags lets tests add switches (e.g. host-resolver-rules). Never for security switches.
	ExtraFlags map[string]string
}

// forbiddenFlags must never be set, neither through ExtraFlags nor through go-rod defaults.
var forbiddenFlags = []flags.Flag{
	"no-sandbox", "disable-web-security", "disable-site-isolation-trials", "allow-running-insecure-content",
	"ignore-certificate-errors", "disable-setuid-sandbox", "single-process", "no-zygote",
}

// Browser is a running Chromium plus its cleanup.
type Browser struct {
	*rod.Browser
	launcher *launcher.Launcher
	dataDir  string
}

// ErrForbiddenFlag reports a switch that would weaken the sandbox or isolation.
var ErrForbiddenFlag = errors.New("browser flag weakens the sandbox")

// Launch starts a fresh browser with its own temporary profile.
func Launch(ctx context.Context, opts Options) (*Browser, error) {
	for name := range opts.ExtraFlags {
		for _, f := range forbiddenFlags {
			if flags.Flag(name) == f {
				return nil, fmt.Errorf("%w: %s", ErrForbiddenFlag, name)
			}
		}
	}
	dataDir, err := os.MkdirTemp("", "getafjob-browser-*")
	if err != nil {
		return nil, fmt.Errorf("create profile dir: %w", err)
	}
	l := launcher.New().Context(ctx).Leakless(false).UserDataDir(dataDir).Headless(!opts.Show)
	if opts.Bin != "" {
		l = l.Bin(opts.Bin)
	} else if p, ok := launcher.LookPath(); ok {
		l = l.Bin(p)
	} else {
		_ = os.RemoveAll(dataDir)
		return nil, errors.New("no browser found: set GETAFJOB_BROWSER_BIN")
	}
	for _, f := range forbiddenFlags {
		l = l.Delete(f)
	}
	// Site isolation back on; the translate popup stays off.
	l = l.Set("disable-features", "Translate,TranslateUI").Set("site-per-process")
	// No non-proxied paths (WebRTC/UDP).
	l = l.Set("force-webrtc-ip-handling-policy", "disable_non_proxied_udp")
	if opts.Proxy != nil {
		l = l.Proxy(opts.Proxy.Host).Set("proxy-bypass-list", "<-loopback>")
	}
	for name, v := range opts.ExtraFlags {
		if v == "" {
			l = l.Set(flags.Flag(name))
		} else {
			l = l.Set(flags.Flag(name), v)
		}
	}

	u, err := l.Launch()
	if err != nil {
		l.Kill()
		_ = os.RemoveAll(dataDir)
		return nil, fmt.Errorf("launch browser: %w", err)
	}
	b := rod.New().ControlURL(u).Context(ctx)
	if err := b.Connect(); err != nil {
		l.Kill()
		_ = os.RemoveAll(dataDir)
		return nil, fmt.Errorf("connect browser: %w", err)
	}
	// The runner never downloads (uploads come from the user's documents, not from the web).
	if err := (proto.BrowserSetDownloadBehavior{Behavior: proto.BrowserSetDownloadBehaviorBehaviorDeny}).Call(b); err != nil {
		_ = b.Close()
		l.Kill()
		_ = os.RemoveAll(dataDir)
		return nil, fmt.Errorf("deny downloads: %w", err)
	}
	return &Browser{Browser: b, launcher: l, dataDir: dataDir}, nil
}

// Args returns the actual command line (tests use it to check that no security switch is missing).
func (b *Browser) Args() []string { return b.launcher.FormatArgs() }

// Version returns e.g. "HeadlessChrome/157.0.8103.27".
func (b *Browser) Version() (string, error) {
	v, err := b.Browser.Version()
	if err != nil {
		return "", err
	}
	return v.Product, nil
}

// Close stops the browser and deletes the temporary profile. It also works with an already canceled ctx
// (shutdown): it keeps the values of ctx but not its cancellation.
func (b *Browser) Close(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := b.Browser.Context(ctx).Close()
	b.launcher.Kill()
	// Chromium still writes to the profile while exiting; a few attempts instead of a fixed sleep.
	var rmErr error
	for range 10 {
		if rmErr = os.RemoveAll(b.dataDir); rmErr == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.Join(err, rmErr)
}

// HasFlag reports whether a switch is on the command line (e.g. "--no-sandbox").
func HasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--"+name || strings.HasPrefix(a, "--"+name+"=") {
			return true
		}
	}
	return false
}
