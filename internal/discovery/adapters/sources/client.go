// Package sources implements discovery.Source for public job APIs: applicant tracking systems (one board per
// company), job boards and the career sites of large companies.
//
// Every request goes through Client: an honest User-Agent, the egress proxy, bounded bodies and retries, and short
// error codes instead of response bodies.
package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/platform/buildinfo"
	"github.com/1xxn/getafjob/internal/platform/netguard"
)

// MaxBody bounds one response.
const MaxBody = 32 << 20

// Client fetches source data.
type Client struct {
	http      *http.Client
	userAgent string
	attempts  int
	backoff   func(attempt int) time.Duration
	sleep     func(ctx context.Context, d time.Duration) error
}

// NewClient returns a client on hc (netguard.NewClient with the egress proxy in production).
func NewClient(hc *http.Client) *Client {
	return &Client{
		http:      hc,
		userAgent: "getafjob/" + buildinfo.Get().Version + " (+https://github.com/1xxn/getafjob)",
		attempts:  3,
		backoff:   func(a int) time.Duration { return min(time.Second<<a, 8*time.Second) },
		sleep:     sleep,
	}
}

// WithoutWaiting disables backoff pauses (tests).
func (c *Client) WithoutWaiting() *Client {
	c.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return c
}

// Once returns a client that does not retry (guessed addresses: a failure just means "not there").
func (c *Client) Once() *Client {
	cp := *c
	cp.attempts = 1
	return &cp
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// request describes one call.
type request struct {
	method  string
	url     string
	query   url.Values
	header  http.Header
	body    any // JSON
	accept  string
	maxBody int64
}

// GetJSON fetches u with query and decodes the JSON answer into out.
func (c *Client) GetJSON(ctx context.Context, src, u string, query url.Values, header http.Header, out any) error {
	b, err := c.do(ctx, src, request{method: http.MethodGet, url: u, query: query, header: header, accept: "application/json"})
	if err != nil {
		return err
	}
	return decode(src, b, out)
}

// PostJSON sends body as JSON and decodes the answer into out.
func (c *Client) PostJSON(ctx context.Context, src, u string, body, out any) error {
	b, err := c.do(ctx, src, request{method: http.MethodPost, url: u, body: body, accept: "application/json"})
	if err != nil {
		return err
	}
	return decode(src, b, out)
}

// GetBytes fetches u (XML feeds, HTML pages).
func (c *Client) GetBytes(ctx context.Context, src, u string, query url.Values, accept string) ([]byte, error) {
	return c.do(ctx, src, request{method: http.MethodGet, url: u, query: query, accept: accept})
}

func decode(src string, b []byte, out any) error {
	if err := json.Unmarshal(b, out); err != nil {
		return &discovery.SourceError{Source: src, Code: "decode"}
	}
	return nil
}

// statusError keeps the status for the retry decision.
type statusError struct {
	code       int
	retryAfter time.Duration
}

func (e *statusError) Error() string { return "http " + strconv.Itoa(e.code) }

func (c *Client) do(ctx context.Context, src string, r request) ([]byte, error) {
	if r.maxBody == 0 {
		r.maxBody = MaxBody
	}
	if len(r.query) > 0 {
		r.url += "?" + r.query.Encode()
	}
	var last error
	for attempt := range c.attempts {
		if attempt > 0 {
			wait := c.backoff(attempt - 1)
			var se *statusError
			if errors.As(last, &se) && se.retryAfter > 0 {
				wait = se.retryAfter
			}
			if err := c.sleep(ctx, wait); err != nil {
				return nil, err
			}
		}
		b, err := c.once(ctx, r)
		if err == nil {
			return b, nil
		}
		last = err
		if !retryable(err) || ctx.Err() != nil {
			break
		}
	}
	return nil, &discovery.SourceError{Source: src, Code: code(ctx, last)}
}

func (c *Client) once(ctx context.Context, r request) ([]byte, error) {
	if !netguard.PublicURL(r.url) {
		return nil, netguard.ErrBlocked
	}
	var body io.Reader
	if r.body != nil {
		b, err := json.Marshal(r.body)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, r.url, body)
	if err != nil {
		return nil, err
	}
	for k, v := range r.header {
		req.Header[k] = v
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", r.accept)
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, &statusError{code: resp.StatusCode, retryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	return netguard.ReadLimited(resp.Body, r.maxBody)
}

// retryAfter reads a Retry-After in seconds, capped at 30s (a longer wait means "try in the next crawl").
func retryAfter(v string) time.Duration {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return min(time.Duration(n)*time.Second, 30*time.Second)
}

func retryable(err error) bool {
	var se *statusError
	if errors.As(err, &se) {
		return se.code == http.StatusTooManyRequests || se.code >= 500
	}
	return !errors.Is(err, netguard.ErrBlocked) && !errors.Is(err, netguard.ErrTooLarge)
}

func code(ctx context.Context, err error) string {
	var se *statusError
	switch {
	case errors.As(err, &se):
		return fmt.Sprintf("http_%d", se.code)
	case errors.Is(err, netguard.ErrBlocked):
		return "blocked"
	case errors.Is(err, netguard.ErrTooLarge):
		return "too_large"
	case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
		return "timeout"
	default:
		return "network"
	}
}

// Status returns the HTTP status behind a source error from this client, or 0.
func Status(err error) int {
	var se *discovery.SourceError
	if !errors.As(err, &se) {
		return 0
	}
	var n int
	if _, e := fmt.Sscanf(se.Code, "http_%d", &n); e != nil {
		return 0
	}
	return n
}
