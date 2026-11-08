package imap_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
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

	commandsReceived := make(chan string, 10)

	// Mock IMAP Server goroutine
	go func() {
		reader := bufio.NewReader(serverConn)
		// 1. Send greeting
		_, _ = fmt.Fprintf(serverConn, "* OK IMAP4rev1 Server Ready\r\n")

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			trimmed := strings.TrimSpace(line)
			commandsReceived <- trimmed

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

	// Verify all protocol commands sent by client:
	// - Must use EXAMINE (not SELECT)
	// - Must use BODY.PEEK (never plain BODY)
	hasExamine := false
	hasPeek := false
	hasSelect := false

	close(commandsReceived)
	for cmd := range commandsReceived {
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
