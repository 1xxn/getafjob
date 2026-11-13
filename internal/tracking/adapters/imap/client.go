// Package imap implements a strictly read-only IMAP client (RFC 3501).
// It accesses mailboxes exclusively via EXAMINE and fetches message content via BODY.PEEK,
// ensuring no server-side flags (\Seen) or states are ever altered.
package imap

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/1xxn/getafjob/internal/platform/netguard"
	"github.com/1xxn/getafjob/internal/tracking"
)

// DialFunc establishes a connection to the IMAP server.
type DialFunc func(ctx context.Context, network, addr string, tlsConfig *tls.Config) (net.Conn, error)

// ErrPlaintext reports a mailbox without TLS; getafjob only logs in over TLS (IMAPS).
var ErrPlaintext = errors.New("imap: TLS is required")

// Client is a read-only IMAP client.
type Client struct {
	dial DialFunc
}

// NewClient returns an IMAP client that reaches mail servers like every foreign service: through the egress proxy
// when one is set, otherwise directly to public addresses only (netguard), never into the own network.
func NewClient(proxy *url.URL) *Client {
	return &Client{
		dial: func(ctx context.Context, _, addr string, tlsConfig *tls.Config) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			conn, err := netguard.Dial(ctx, proxy, addr)
			if err != nil || tlsConfig == nil {
				return conn, err
			}
			tc := tls.Client(conn, tlsConfig)
			if err := tc.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return tc, nil
		},
	}
}

// NewClientWithDialer returns an IMAP client using a custom dialer (used in tests).
func NewClientWithDialer(dial DialFunc) *Client {
	return &Client{dial: dial}
}

// TestConnection validates that credentials and connectivity are functional.
func (c *Client) TestConnection(ctx context.Context, acc tracking.MailboxAccount, password string) error {
	conn, err := c.connect(ctx, acc)
	if err != nil {
		return err
	}
	defer conn.Close()

	session := newSession(conn)
	if err := session.readGreeting(); err != nil {
		return fmt.Errorf("reading IMAP greeting: %w", err)
	}

	if err := session.login(acc.Username, password); err != nil {
		return fmt.Errorf("IMAP login: %w", err)
	}

	if _, err := session.examineInbox(); err != nil {
		return fmt.Errorf("examining INBOX (read-only): %w", err)
	}

	_ = session.logout()
	return nil
}

// FetchNewMessages opens INBOX strictly read-only (EXAMINE) and fetches new messages using BODY.PEEK.
func (c *Client) FetchNewMessages(ctx context.Context, acc tracking.MailboxAccount, password string) ([]tracking.EmailMessage, uint32, uint32, error) {
	conn, err := c.connect(ctx, acc)
	if err != nil {
		return nil, acc.LastUID, acc.UIDValidity, err
	}
	defer conn.Close()

	session := newSession(conn)
	if err := session.readGreeting(); err != nil {
		return nil, acc.LastUID, acc.UIDValidity, fmt.Errorf("reading IMAP greeting: %w", err)
	}

	if err := session.login(acc.Username, password); err != nil {
		return nil, acc.LastUID, acc.UIDValidity, fmt.Errorf("IMAP login: %w", err)
	}
	defer func() { _ = session.logout() }()

	validity, err := session.examineInbox()
	if err != nil {
		return nil, acc.LastUID, acc.UIDValidity, fmt.Errorf("examining INBOX (read-only): %w", err)
	}

	lastUID := acc.LastUID
	// If server validity changed, existing UIDs are invalid; restart from 0
	if acc.UIDValidity != 0 && validity != 0 && validity != acc.UIDValidity {
		lastUID = 0
	}

	uids, err := session.searchNewUIDs(lastUID)
	if err != nil {
		return nil, lastUID, validity, fmt.Errorf("searching UIDs: %w", err)
	}

	if len(uids) == 0 {
		return nil, lastUID, validity, nil
	}

	var messages []tracking.EmailMessage
	highestUID := lastUID

	for _, uid := range uids {
		msg, err := session.fetchMessagePeek(uid)
		if err != nil {
			// Skip single message fetch error and continue
			continue
		}
		messages = append(messages, msg)
		if uid > highestUID {
			highestUID = uid
		}
	}

	return messages, highestUID, validity, nil
}

func (c *Client) connect(ctx context.Context, acc tracking.MailboxAccount) (net.Conn, error) {
	if !acc.TLS {
		return nil, ErrPlaintext // the password never goes over an unencrypted connection
	}
	addr := net.JoinHostPort(acc.Host, strconv.Itoa(acc.Port))
	return c.dial(ctx, "tcp", addr, &tls.Config{ServerName: acc.Host, MinVersion: tls.VersionTLS12})
}

type session struct {
	conn   net.Conn
	reader *bufio.Reader
	tagNum int
}

func newSession(conn net.Conn) *session {
	return &session{
		conn:   conn,
		reader: bufio.NewReader(conn),
	}
}

func (s *session) nextTag() string {
	s.tagNum++
	return fmt.Sprintf("A%04d", s.tagNum)
}

func (s *session) sendCommand(cmd string) error {
	_, err := fmt.Fprintf(s.conn, "%s\r\n", cmd)
	return err
}

func (s *session) readGreeting() error {
	line, err := s.reader.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(strings.TrimSpace(line), "* OK") {
		return fmt.Errorf("unexpected greeting: %s", strings.TrimSpace(line))
	}
	return nil
}

func (s *session) login(username, password string) error {
	tag := s.nextTag()
	// Escape quotes and backslashes in credentials
	escapedUser := strings.ReplaceAll(strings.ReplaceAll(username, `\`, `\\`), `"`, `\"`)
	escapedPass := strings.ReplaceAll(strings.ReplaceAll(password, `\`, `\\`), `"`, `\"`)

	if err := s.sendCommand(fmt.Sprintf(`%s LOGIN "%s" "%s"`, tag, escapedUser, escapedPass)); err != nil {
		return err
	}

	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return err
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, tag+" OK") {
			return nil
		}
		if strings.HasPrefix(trimmed, tag+" NO") || strings.HasPrefix(trimmed, tag+" BAD") {
			return errors.New("authentication failed: " + trimmed)
		}
	}
}

// examineInbox opens INBOX strictly in read-only mode (RFC 3501 Section 6.3.2).
func (s *session) examineInbox() (uint32, error) {
	tag := s.nextTag()
	if err := s.sendCommand(tag + ` EXAMINE "INBOX"`); err != nil {
		return 0, err
	}

	var uidValidity uint32
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return 0, err
		}
		trimmed := strings.TrimSpace(line)

		// Check for * OK [UIDVALIDITY 12345]
		if idx := strings.Index(trimmed, "[UIDVALIDITY "); idx >= 0 {
			rest := trimmed[idx+len("[UIDVALIDITY "):]
			if end := strings.Index(rest, "]"); end >= 0 {
				valStr := strings.TrimSpace(rest[:end])
				if val, err := strconv.ParseUint(valStr, 10, 32); err == nil {
					uidValidity = uint32(val)
				}
			}
		}

		if strings.HasPrefix(trimmed, tag+" OK") {
			return uidValidity, nil
		}
		if strings.HasPrefix(trimmed, tag+" NO") || strings.HasPrefix(trimmed, tag+" BAD") {
			return 0, errors.New("EXAMINE failed: " + trimmed)
		}
	}
}

func (s *session) searchNewUIDs(lastUID uint32) ([]uint32, error) {
	tag := s.nextTag()
	var cmd string
	if lastUID > 0 {
		cmd = fmt.Sprintf("%s UID SEARCH UID %d:*", tag, lastUID+1)
	} else {
		cmd = tag + " UID SEARCH ALL"
	}

	if err := s.sendCommand(cmd); err != nil {
		return nil, err
	}

	var uids []uint32
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "* SEARCH") {
			fields := strings.Fields(trimmed)
			for _, f := range fields[2:] { // skip "*", "SEARCH"
				if val, err := strconv.ParseUint(f, 10, 32); err == nil {
					u := uint32(val)
					if u > lastUID {
						uids = append(uids, u)
					}
				}
			}
		}

		if strings.HasPrefix(trimmed, tag+" OK") {
			// If lastUID was 0 (first fetch), limit to most recent 50 messages
			if lastUID == 0 && len(uids) > 50 {
				uids = uids[len(uids)-50:]
			}
			return uids, nil
		}
		if strings.HasPrefix(trimmed, tag+" NO") || strings.HasPrefix(trimmed, tag+" BAD") {
			return nil, errors.New("UID SEARCH failed: " + trimmed)
		}
	}
}

// fetchMessagePeek fetches the message headers and body text using BODY.PEEK so \Seen is NEVER set.
func (s *session) fetchMessagePeek(uid uint32) (tracking.EmailMessage, error) {
	tag := s.nextTag()
	// RFC 3501: BODY.PEEK does NOT set \Seen flag
	cmd := fmt.Sprintf("%s UID FETCH %d (UID RFC822.SIZE BODY.PEEK[])", tag, uid)
	if err := s.sendCommand(cmd); err != nil {
		return tracking.EmailMessage{}, err
	}

	var rawRFC822 []byte
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return tracking.EmailMessage{}, err
		}
		trimmed := strings.TrimSpace(line)

		// Look for literal size: {1234}
		if strings.Contains(line, "{") && strings.HasSuffix(trimmed, "}") {
			start := strings.LastIndex(line, "{")
			end := strings.LastIndex(line, "}")
			if start >= 0 && end > start {
				sizeStr := line[start+1 : end]
				if size, err := strconv.Atoi(sizeStr); err == nil && size > 0 {
					buf := make([]byte, size)
					if _, err := io.ReadFull(s.reader, buf); err == nil {
						rawRFC822 = buf
					}
				}
			}
		}

		if strings.HasPrefix(trimmed, tag+" OK") {
			break
		}
		if strings.HasPrefix(trimmed, tag+" NO") || strings.HasPrefix(trimmed, tag+" BAD") {
			return tracking.EmailMessage{}, errors.New("UID FETCH failed: " + trimmed)
		}
	}

	if len(rawRFC822) == 0 {
		return tracking.EmailMessage{UID: uid}, nil
	}

	return parseRawEmail(uid, rawRFC822)
}

func (s *session) logout() error {
	tag := s.nextTag()
	_ = s.sendCommand(tag + " LOGOUT")
	_ = s.conn.Close()
	return nil
}

// parseRawEmail parses an RFC 822 / MIME email.
func parseRawEmail(uid uint32, raw []byte) (tracking.EmailMessage, error) {
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		return tracking.EmailMessage{UID: uid}, err
	}

	dec := new(mime.WordDecoder)
	subject, _ := dec.DecodeHeader(msg.Header.Get("Subject"))
	from, _ := dec.DecodeHeader(msg.Header.Get("From"))
	messageID := strings.Trim(msg.Header.Get("Message-ID"), "<>")

	var date time.Time
	if dateStr := msg.Header.Get("Date"); dateStr != "" {
		if parsed, err := mail.ParseDate(dateStr); err == nil {
			date = parsed
		}
	}

	bodyText := extractPlainText(msg.Header, msg.Body)

	return tracking.EmailMessage{
		UID:       uid,
		MessageID: messageID,
		From:      from,
		Subject:   subject,
		Date:      date,
		Body:      bodyText,
	}, nil
}

// extractPlainText parses multipart or plain text email bodies.
func extractPlainText(header mail.Header, r io.Reader) string {
	contentType := header.Get("Content-Type")
	if contentType == "" {
		b, _ := io.ReadAll(io.LimitReader(r, 64*1024))
		return string(b)
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		b, _ := io.ReadAll(io.LimitReader(r, 64*1024))
		return string(b)
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(r, params["boundary"])
		var plainBuilder strings.Builder
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			partType, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
			if partType == "text/plain" {
				b, _ := io.ReadAll(io.LimitReader(p, 32*1024))
				plainBuilder.Write(b)
				plainBuilder.WriteString("\n")
			}
		}
		if plainBuilder.Len() > 0 {
			return plainBuilder.String()
		}
		return ""
	}

	b, _ := io.ReadAll(io.LimitReader(r, 64*1024))
	raw := string(b)
	if mediaType == "text/html" {
		return stripHTML(raw)
	}
	return raw
}

func stripHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			b.WriteByte(' ')
			continue
		}
		if !inTag {
			b.WriteRune(r)
		}
	}
	return textproto.TrimString(b.String())
}
