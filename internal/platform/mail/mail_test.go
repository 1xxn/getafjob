package mail

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeSMTP accepts one message and returns its DATA part.
func fakeSMTP(t *testing.T) (addr string, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got = make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		r := bufio.NewReader(c)
		w := func(s string) { _, _ = io.WriteString(c, s+"\r\n") }
		w("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					got <- data.String()
					w("250 queued")
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				w("250 fake")
			case strings.HasPrefix(cmd, "DATA"):
				inData = true
				w("354 go")
			case strings.HasPrefix(cmd, "QUIT"):
				w("221 bye")
				return
			default:
				w("250 ok")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestSendPlain(t *testing.T) {
	addr, got := fakeSMTP(t)
	s, err := New("smtp+insecure://"+addr, "getafjob@example.org", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Send(t.Context(), Message{To: "max.mustermann@example.com", Subject: "Dein Anmeldelink für getafjob", Text: "Hallo Max,\nöffne diesen Link."})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-got:
		for _, want := range []string{"To: max.mustermann@example.com", "Subject: =?utf-8?q?", "Content-Type: text/plain; charset=utf-8", "=C3=B6ffne"} {
			if !strings.Contains(data, want) {
				t.Errorf("message misses %q:\n%s", want, data)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no message")
	}
}

func TestSendThroughProxy(t *testing.T) {
	addr, got := fakeSMTP(t)
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxy.Close() }()
	target := make(chan string, 1)
	go func() {
		c, err := proxy.Accept()
		if err != nil {
			return
		}
		r := bufio.NewReader(c)
		line, _ := r.ReadString('\n')
		target <- strings.Fields(line)[1]
		for l, _ := r.ReadString('\n'); l != "\r\n"; l, _ = r.ReadString('\n') {
		}
		up, err := net.Dial("tcp", addr)
		if err != nil {
			return
		}
		_, _ = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
		go func() { _, _ = io.Copy(up, r) }()
		_, _ = io.Copy(c, up)
	}()
	pu, _ := url.Parse("http://" + proxy.Addr().String())
	s, err := New("smtp+insecure://"+addr, "getafjob@example.org", pu)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(t.Context(), Message{To: "max@example.com", Subject: "x", Text: "y"}); err != nil {
		t.Fatal(err)
	}
	if got := <-target; got != addr {
		t.Fatalf("CONNECT target %q", got)
	}
	<-got
}

func TestHeaderInjectionIsRejected(t *testing.T) {
	s, _ := New("smtp+insecure://127.0.0.1:1", "getafjob@example.org", nil)
	for _, m := range []Message{{To: "a@b.c\r\nBcc: evil@x.y", Subject: "s"}, {To: "a@b.c", Subject: "s\nBcc: evil@x.y"}} {
		if err := s.Send(t.Context(), m); !errors.Is(err, ErrHeaderInjection) {
			t.Errorf("%+v: %v", m, err)
		}
	}
}

func TestNewValidates(t *testing.T) {
	for _, u := range []string{"", "http://x:25", "smtp://nohostport", "smtps://"} {
		if _, err := New(u, "a@b.c", nil); err == nil {
			t.Errorf("New(%q) accepted", u)
		}
	}
	if _, err := New("smtp://h:587", "no-at-sign", nil); err == nil {
		t.Error("bad from accepted")
	}
	if s, err := New("smtps://u:p@h:465", "a@b.c", nil); err != nil || !s.tls || s.starttls {
		t.Errorf("smtps: %+v %v", s, err)
	}
}
