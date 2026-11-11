package netguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// ClientOptions configure an outbound HTTP client.
type ClientOptions struct {
	// Proxy sends every request through the egress proxy, which enforces the address policy itself.
	Proxy *url.URL
	// AllowPrivate disables the address checks (tests with local servers only).
	AllowPrivate bool
	// Timeout bounds a whole request including the body; default 30s.
	Timeout time.Duration
	// MaxRedirects; default 5.
	MaxRedirects int
}

// control rejects connections to non-public addresses after DNS, so rebinding cannot slip through.
func control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !PublicIP(ap.Addr()) {
		return fmt.Errorf("%w: connection to %s", ErrBlocked, address)
	}
	return nil
}

// NewClient returns an HTTP client for foreign URLs: public targets only (checked per connection and redirect),
// no proxy from the environment, bounded redirects and time.
func NewClient(opts ClientOptions) *http.Client {
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.MaxRedirects == 0 {
		opts.MaxRedirects = 5
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		Proxy:                 nil, // never from the environment: the check would apply to the proxy instead
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}
	switch {
	case opts.Proxy != nil:
		tr.Proxy = http.ProxyURL(opts.Proxy)
	case !opts.AllowPrivate:
		dialer.Control = control
	}
	return &http.Client{
		Transport: tr,
		Timeout:   opts.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= opts.MaxRedirects {
				return fmt.Errorf("stopped after %d redirects", opts.MaxRedirects)
			}
			if !opts.AllowPrivate && !PublicURL(req.URL.String()) {
				return fmt.Errorf("%w: redirect", ErrBlocked)
			}
			return nil
		},
	}
}

// Get fetches a public URL (checked before connecting) with ctx.
func Get(ctx context.Context, c *http.Client, raw string, allowPrivate bool) (*http.Response, error) {
	if !allowPrivate && !PublicURL(raw) {
		return nil, ErrBlocked
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, browserURL(raw), nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// ReadPrefix reads at most limit bytes and drops the rest: for HTML whose useful part sits early (head, JSON-LD), a
// cut page is better than none.
func ReadPrefix(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit))
}

// ErrTooLarge reports a body above the limit.
var ErrTooLarge = errors.New("response body too large")

// ReadLimited reads at most limit bytes; a larger body is an error (and the rest is not read).
func ReadLimited(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, ErrTooLarge
	}
	return b, nil
}
