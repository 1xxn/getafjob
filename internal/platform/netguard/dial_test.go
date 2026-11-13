package netguard

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
)

// Dial never reaches the own machine or network, with or without the proxy.
func TestDialRefusesInternalTargets(t *testing.T) {
	proxy, _ := url.Parse("http://127.0.0.1:1") // never dialed: the host check comes first
	for _, addr := range []string{"127.0.0.1:993", "localhost:993", "postgres:5432", "10.0.0.5:993", "[::1]:993",
		"169.254.169.254:80", "mail.internal:993"} {
		for _, p := range []*url.URL{nil, proxy} {
			if _, err := Dial(t.Context(), p, addr); !errors.Is(err, ErrBlocked) {
				t.Errorf("Dial(%v, %s) = %v, want ErrBlocked", p, addr, err)
			}
		}
	}
}

// Connect tunnels through the proxy and keeps the greeting the server sent right after the CONNECT answer; a
// refusal of the proxy is ErrBlocked.
func TestConnectThroughProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				req, err := http.ReadRequest(bufio.NewReader(c))
				if err != nil {
					return
				}
				if req.Host == "denied.example.org:993" {
					_, _ = io.WriteString(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
					return
				}
				_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\n\r\n* OK IMAP ready\r\n")
			}(c)
		}
	}()
	proxy, _ := url.Parse("http://" + ln.Addr().String())
	conn, err := Dial(t.Context(), proxy, "imap.example.org:993")
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.Close()
	if err != nil || line != "* OK IMAP ready\r\n" {
		t.Fatalf("greeting = %q, %v", line, err)
	}
	if _, err := Dial(t.Context(), proxy, "denied.example.org:993"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("refused = %v, want ErrBlocked", err)
	}
}
