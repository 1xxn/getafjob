package web

import (
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/tracking"
)

// The integrations page says what the last inbox check found, so a mailbox that never works does not fail silently.
func TestMailboxState(t *testing.T) {
	at := time.Date(2026, 11, 23, 9, 30, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		mb   tracking.MailboxAccount
		want string
	}{
		{"no mailbox", tracking.MailboxAccount{}, ""},
		{"turned off", tracking.MailboxAccount{Host: "imap.example.org", LastCheckedAt: &at}, ""},
		{"not checked yet", tracking.MailboxAccount{Host: "imap.example.org", Enabled: true}, "waiting"},
		{"fine", tracking.MailboxAccount{Host: "imap.example.org", Enabled: true, LastCheckedAt: &at}, "ok"},
		{"failed", tracking.MailboxAccount{Host: "imap.example.org", Enabled: true, LastCheckedAt: &at, LastError: "imap: TLS is required"}, "failed"},
	} {
		state, checked := mailboxState(t.Context(), c.mb)
		if state != c.want || (c.want == "ok" || c.want == "failed") && checked == "" {
			t.Errorf("%s: state %q, checked %q; want %q", c.name, state, checked, c.want)
		}
	}
}
