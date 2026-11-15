package imap_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/tracking"
	"github.com/1xxn/getafjob/internal/tracking/adapters/imap"
)

func TestImapClientReadOnlyAndPeek(t *testing.T) {
	ctx := context.Background()

	// In-memory pipe between mock server and client
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	var (
		mu               sync.Mutex
		commandsReceived []string
		wg               sync.WaitGroup
	)

	wg.Add(1)
	// Mock IMAP Server goroutine
	go func() {
		defer wg.Done()
		reader := bufio.NewReader(serverConn)
		// 1. Send greeting
		_, _ = fmt.Fprintf(serverConn, "* OK IMAP4rev1 Server Ready\r\n")

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			trimmed := strings.TrimSpace(line)
			mu.Lock()
			commandsReceived = append(commandsReceived, trimmed)
			mu.Unlock()

			parts := strings.SplitN(trimmed, " ", 2)
			if len(parts) < 2 {
				continue
			}
			tag := parts[0]
			cmd := parts[1]

			switch {
			case strings.HasPrefix(cmd, "LOGIN"):
				_, _ = fmt.Fprintf(serverConn, "%s OK LOGIN completed\r\n", tag)
			case strings.HasPrefix(cmd, "EXAMINE"):
				// Verify mailbox is examined read-only!
				_, _ = fmt.Fprintf(serverConn, "* 5 EXISTS\r\n* OK [UIDVALIDITY 98765] UIDs valid\r\n%s OK [READ-ONLY] EXAMINE completed\r\n", tag)
			case strings.HasPrefix(cmd, "UID SEARCH"):
				_, _ = fmt.Fprintf(serverConn, "* SEARCH 101 102\r\n%s OK SEARCH completed\r\n", tag)
			case strings.HasPrefix(cmd, "UID FETCH 101"):
				rawEmail := "From: HR <recruiting@example.org>\r\n" +
					"To: Candidate <cand@example.org>\r\n" +
					"Subject: Eingangsbestaetigung Ihrer Bewerbung\r\n" +
					"Date: Sun, 08 Nov 2026 12:00:00 +0100\r\n" +
					"Message-ID: <msg-101@example.org>\r\n" +
					"Content-Type: text/plain; charset=utf-8\r\n" +
					"\r\n" +
					"Vielen Dank fuer Ihre Bewerbung. Ihre Unterlagen sind eingegangen."
				_, _ = fmt.Fprintf(serverConn, "* 1 FETCH (UID 101 RFC822.SIZE %d BODY[] {%d}\r\n%s\r\n)\r\n%s OK FETCH completed\r\n",
					len(rawEmail), len(rawEmail), rawEmail, tag)
			case strings.HasPrefix(cmd, "UID FETCH 102"):
				rawEmail := "From: Lead <lead@example.org>\r\n" +
					"To: Candidate <cand@example.org>\r\n" +
					"Subject: Einladung zum Vorstellungsgespraech\r\n" +
					"Date: Sun, 08 Nov 2026 14:00:00 +0100\r\n" +
					"Message-ID: <msg-102@example.org>\r\n" +
					"Content-Type: text/plain; charset=utf-8\r\n" +
					"\r\n" +
					"Wir moechten Sie gerne zu einem Interview einladen."
				_, _ = fmt.Fprintf(serverConn, "* 2 FETCH (UID 102 RFC822.SIZE %d BODY[] {%d}\r\n%s\r\n)\r\n%s OK FETCH completed\r\n",
					len(rawEmail), len(rawEmail), rawEmail, tag)
			case strings.HasPrefix(cmd, "LOGOUT"):
				_, _ = fmt.Fprintf(serverConn, "* BYE IMAP4rev1 Server logging out\r\n%s OK LOGOUT completed\r\n", tag)
				return
			default:
				_, _ = fmt.Fprintf(serverConn, "%s BAD command not recognized\r\n", tag)
			}
		}
	}()

	client := imap.NewClientWithDialer(func(_ context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
		return clientConn, nil
	})

	acc := tracking.MailboxAccount{
		UserID:      uuid.New(),
		Host:        "imap.example.org",
		Port:        993,
		TLS:         true,
		Username:    "cand@example.org",
		LastUID:     100,
		UIDValidity: 98765,
	}

	messages, highestUID, validity, err := client.FetchNewMessages(ctx, acc, "secret-app-password")
	if err != nil {
		t.Fatalf("FetchNewMessages err = %v", err)
	}

	if highestUID != 102 {
		t.Errorf("highestUID = %d, want 102", highestUID)
	}
	if validity != 98765 {
		t.Errorf("validity = %d, want 98765", validity)
	}
	if len(messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(messages))
	}

	if messages[0].UID != 101 || messages[0].Subject != "Eingangsbestaetigung Ihrer Bewerbung" {
		t.Errorf("messages[0] = %+v", messages[0])
	}
	if messages[1].UID != 102 || messages[1].Subject != "Einladung zum Vorstellungsgespraech" {
		t.Errorf("messages[1] = %+v", messages[1])
	}

	// Wait for server goroutine to terminate after LOGOUT
	wg.Wait()

	// Verify all protocol commands sent by client:
	// - Must use EXAMINE (not SELECT)
	// - Must use BODY.PEEK (never plain BODY)
	hasExamine := false
	hasPeek := false
	hasSelect := false

	mu.Lock()
	cmds := make([]string, len(commandsReceived))
	copy(cmds, commandsReceived)
	mu.Unlock()

	for _, cmd := range cmds {
		if strings.Contains(cmd, "EXAMINE") {
			hasExamine = true
		}
		if strings.Contains(cmd, "SELECT") && !strings.Contains(cmd, "EXAMINE") {
			hasSelect = true
		}
		if strings.Contains(cmd, "BODY.PEEK") {
			hasPeek = true
		}
	}

	if !hasExamine {
		t.Errorf("Client must use EXAMINE for read-only access")
	}
	if hasSelect {
		t.Errorf("Client must not use SELECT (which can write/modify mailbox state)")
	}
	if !hasPeek {
		t.Errorf("Client must use BODY.PEEK so flags like \\Seen are never modified")
	}
}

// The password never goes over an unencrypted connection: a mailbox without TLS is refused before any dial.
func TestPlaintextMailboxIsRefused(t *testing.T) {
	dialed := false
	c := imap.NewClientWithDialer(func(context.Context, string, string, *tls.Config) (net.Conn, error) {
		dialed = true
		return nil, fmt.Errorf("unexpected dial")
	})
	err := c.TestConnection(t.Context(), tracking.MailboxAccount{Host: "imap.example.org", Port: 143}, "secret")
	if !errors.Is(err, imap.ErrPlaintext) || dialed {
		t.Fatalf("err = %v, dialed = %v; want ErrPlaintext without a dial", err, dialed)
	}
}

func TestImapDecodesMIMEAndBoundedPeek(t *testing.T) {
	ctx := context.Background()
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	var fetchCmd string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		reader := bufio.NewReader(serverConn)
		_, _ = fmt.Fprintf(serverConn, "* OK Ready\r\n")
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			parts := strings.SplitN(strings.TrimSpace(line), " ", 2)
			if len(parts) < 2 {
				continue
			}
			tag, cmd := parts[0], parts[1]
			switch {
			case strings.HasPrefix(cmd, "LOGIN"), strings.HasPrefix(cmd, "EXAMINE"):
				_, _ = fmt.Fprintf(serverConn, "%s OK done\r\n", tag)
			case strings.HasPrefix(cmd, "UID SEARCH"):
				_, _ = fmt.Fprintf(serverConn, "* SEARCH 201 202\r\n%s OK done\r\n", tag)
			case strings.HasPrefix(cmd, "UID FETCH 201"):
				fetchCmd = cmd
				qpBody := "From: HR <hr@example.org>\r\n" +
					"Subject: Interview Einladung\r\n" +
					"Content-Type: multipart/mixed; boundary=\"mix\"\r\n" +
					"\r\n" +
					"--mix\r\n" +
					"Content-Type: multipart/alternative; boundary=\"alt\"\r\n" +
					"\r\n" +
					"--alt\r\n" +
					"Content-Type: text/plain; charset=utf-8\r\n" +
					"Content-Transfer-Encoding: quoted-printable\r\n" +
					"\r\n" +
					"Hallo Max,=0D=0AWir laden Sie ein.\r\n" +
					"--alt--\r\n" +
					"--mix--\r\n"
				_, _ = fmt.Fprintf(serverConn, "* 1 FETCH (UID 201 BODY[]<0> {%d}\r\n%s)\r\n%s OK done\r\n", len(qpBody), qpBody, tag)
			case strings.HasPrefix(cmd, "UID FETCH 202"):
				b64 := base64.StdEncoding.EncodeToString([]byte("<p>Absage f&uuml;r Ihre Bewerbung</p>"))
				htmlMail := "From: Jobs <jobs@example.org>\r\n" +
					"Subject: Update\r\n" +
					"Content-Type: multipart/alternative; boundary=\"alt2\"\r\n" +
					"\r\n" +
					"--alt2\r\n" +
					"Content-Type: text/html; charset=utf-8\r\n" +
					"Content-Transfer-Encoding: base64\r\n" +
					"\r\n" +
					b64 + "\r\n" +
					"--alt2--\r\n"
				_, _ = fmt.Fprintf(serverConn, "* 2 FETCH (UID 202 BODY[]<0> {%d}\r\n%s)\r\n%s OK done\r\n", len(htmlMail), htmlMail, tag)
			case strings.HasPrefix(cmd, "LOGOUT"):
				_, _ = fmt.Fprintf(serverConn, "%s OK bye\r\n", tag)
				return
			}
		}
	}()

	client := imap.NewClientWithDialer(func(_ context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
		return clientConn, nil
	})
	msgs, _, _, err := client.FetchNewMessages(ctx, tracking.MailboxAccount{Host: "imap.example.org", Port: 993, TLS: true, Username: "u"}, "p")
	wg.Wait()
	if err != nil {
		t.Fatalf("FetchNewMessages: %v", err)
	}
	if !strings.Contains(fetchCmd, "BODY.PEEK[]<0.65536>") {
		t.Fatalf("expected bounded BODY.PEEK[]<0.65536>, got %q", fetchCmd)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	if !strings.Contains(msgs[0].Body, "Hallo Max") || strings.Contains(msgs[0].Body, "=0D") {
		t.Errorf("decoded QP nested body = %q", msgs[0].Body)
	}
	if !strings.Contains(msgs[1].Body, "Absage") || strings.Contains(msgs[1].Body, "<p>") {
		t.Errorf("decoded base64 HTML fallback = %q", msgs[1].Body)
	}
}
