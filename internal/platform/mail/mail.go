// Package mail sends plain-text mail over SMTP, optionally through the egress proxy (HTTP CONNECT).
package mail

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// Message is a plain-text mail.
type Message struct {
	To      string
	Subject string
	Text    string
}

// Sender delivers mail to one SMTP server.
type Sender struct {
	host     string // host:port
	tls      bool   // implicit TLS (smtps)
	starttls bool   // STARTTLS required (smtp)
	auth     *url.Userinfo
	from     string
	proxy    *url.URL
	timeout  time.Duration
}

// ErrHeaderInjection reports CR or LF in a header value.
var ErrHeaderInjection = errors.New("mail: line break in header")

// New parses smtps://user:pass@host:465, smtp://user:pass@host:587 (STARTTLS) or smtp+insecure://host:1025
// (development only, no TLS). proxy may be nil. smtp+insecure is the local mail catcher on the internal network,
// which the egress proxy refuses by design, so it is dialed directly.
func New(rawURL, from string, proxy *url.URL) (*Sender, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, errors.New("mail: invalid SMTP URL")
	}
	s := &Sender{host: u.Host, auth: u.User, from: from, proxy: proxy, timeout: 30 * time.Second}
	switch u.Scheme {
	case "smtps":
		s.tls = true
	case "smtp":
		s.starttls = true
	case "smtp+insecure":
		s.proxy = nil
	default:
		return nil, errors.New("mail: scheme must be smtps, smtp or smtp+insecure")
	}
	if _, _, err := net.SplitHostPort(s.host); err != nil {
		return nil, errors.New("mail: SMTP URL needs host:port")
	}
	if strings.ContainsAny(from, "\r\n") || !strings.Contains(from, "@") {
		return nil, errors.New("mail: invalid from address")
	}
	return s, nil
}

// Send delivers m.
func (s *Sender) Send(ctx context.Context, m Message) error {
	for _, v := range []string{m.To, m.Subject} {
		if strings.ContainsAny(v, "\r\n") {
			return ErrHeaderInjection
		}
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	conn, err := s.dial(ctx)
	if err != nil {
		return fmt.Errorf("mail: connect: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	host, _, _ := net.SplitHostPort(s.host)
	if s.tls {
		conn = tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mail: handshake: %w", err)
	}
	defer func() { _ = c.Close() }()
	if s.starttls {
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mail: starttls: %w", err)
		}
	}
	if s.auth != nil {
		pw, _ := s.auth.Password()
		if err := c.Auth(smtp.PlainAuth("", s.auth.Username(), pw, host)); err != nil {
			return errors.New("mail: authentication failed")
		}
	}
	if err := c.Mail(s.from); err != nil {
		return fmt.Errorf("mail: from: %w", err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return fmt.Errorf("mail: recipient: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	if _, err := w.Write(Compose(s.from, m, time.Now())); err != nil {
		return fmt.Errorf("mail: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: send: %w", err)
	}
	return c.Quit()
}

// dial connects directly or through an HTTP CONNECT proxy.
func (s *Sender) dial(ctx context.Context) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if s.proxy == nil {
		return d.DialContext(ctx, "tcp", s.host)
	}
	conn, err := d.DialContext(ctx, "tcp", s.proxy.Host)
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", s.host, s.host); err != nil {
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
		return nil, fmt.Errorf("proxy refused: %s", resp.Status)
	}
	// The server greeting may already sit in the buffer; read it from there first.
	return bufferedConn{Conn: conn, r: br}, nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// Compose renders an RFC 5322 message (UTF-8, quoted-printable).
func Compose(from string, m Message, now time.Time) []byte {
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domain := from[strings.LastIndex(from, "@")+1:]
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", m.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", now.UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), domain)
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n\r\n")
	qp := quotedprintable.NewWriter(&b)
	_, _ = qp.Write([]byte(strings.ReplaceAll(m.Text, "\n", "\r\n")))
	_ = qp.Close()
	b.WriteString("\r\n")
	return []byte(b.String())
}
