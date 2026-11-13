package netguard

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Dial opens a TCP connection to addr (host:port) of a foreign service such as a mail server. With proxy set it
// tunnels through the egress proxy (HTTP CONNECT), whose deny list applies; without it, it connects directly to
// public addresses only, checked after DNS. The host must be a public host name or address either way.
func Dial(ctx context.Context, proxy *url.URL, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if !PublicHost(host) {
		return nil, fmt.Errorf("%w: %s", ErrBlocked, host)
	}
	d := &net.Dialer{Timeout: 15 * time.Second}
	if proxy == nil {
		d.Control = control
		return d.DialContext(ctx, "tcp", addr)
	}
	return Connect(ctx, d, proxy, addr)
}

// Connect tunnels a TCP connection to addr through the HTTP proxy with CONNECT. The answer of the target may
// already sit in the read buffer; the returned connection reads it from there first.
func Connect(ctx context.Context, d *net.Dialer, proxy *url.URL, addr string) (net.Conn, error) {
	conn, err := d.DialContext(ctx, "tcp", proxy.Host)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", addr, addr); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: proxy refused %s: %s", ErrBlocked, addr, resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	return bufferedConn{Conn: conn, r: br}, nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
