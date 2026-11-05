package identity

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTokens(t *testing.T) {
	a, ha := NewToken()
	b, _ := NewToken()
	if a == b || len(a) != 43 {
		t.Fatalf("tokens %q %q", a, b)
	}
	h, ok := ParseToken(string(a))
	if !ok || h != ha {
		t.Fatal("ParseToken does not match NewToken")
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 44), strings.Repeat("+", 43), string(a) + "\n"} {
		if _, ok := ParseToken(bad); ok {
			t.Errorf("ParseToken(%q) accepted", bad)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	cases := []struct {
		in, want string
		err      bool
	}{
		{"Max.Mustermann@Example.com", "max.mustermann@example.com", false},
		{"  max@example.org ", "max@example.org", false},
		{"Max <max@example.com>", "", true},
		{"max@example", "", true},
		{"max@.example.com", "", true},
		{"@example.com", "", true},
		{"max@example.com\r\nBcc: x@y.z", "", true},
		{"max example@example.com", "", true},
		{strings.Repeat("a", 250) + "@x.de", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeEmail(c.in)
		if c.err != (err != nil) || got != c.want {
			t.Errorf("NormalizeEmail(%q) = %q, %v", c.in, got, err)
		}
		if err != nil && !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("error kind: %v", err)
		}
	}
}

func TestSessionValidity(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s := Session{CreatedAt: now, LastSeen: now, ExpiresAt: now.Add(SessionMaxAge)}
	cases := []struct {
		at    time.Time
		valid bool
	}{
		{now.Add(time.Hour), true},
		{now.Add(SessionIdle - time.Second), true},
		{now.Add(SessionIdle), false},
		{now.Add(SessionMaxAge), false},
	}
	for _, c := range cases {
		if got := s.Valid(c.at); got != c.valid {
			t.Errorf("Valid(+%v) = %v", c.at.Sub(now), got)
		}
	}
	active := Session{LastSeen: now.Add(29 * 24 * time.Hour), ExpiresAt: now.Add(SessionMaxAge)}
	if active.Valid(now.Add(SessionMaxAge + time.Hour)) {
		t.Error("absolute limit ignored for an active session")
	}
	if s.NeedsTouch(now.Add(time.Minute)) || !s.NeedsTouch(now.Add(10*time.Minute)) {
		t.Error("NeedsTouch")
	}
}

func TestCleanDisplayName(t *testing.T) {
	if got := CleanDisplayName("  Max \n  Mustermann "); got != "Max Mustermann" {
		t.Errorf("got %q", got)
	}
	if got := CleanDisplayName(strings.Repeat("ä", 300)); len([]rune(got)) != 200 {
		t.Errorf("not capped: %d", len([]rune(got)))
	}
}

func TestMsg(t *testing.T) {
	if Msg(ErrSessionExpired).Code != "identity.session_expired" || Msg(ErrUserDisabled).Code != "identity.user_disabled" ||
		Msg(ErrInvalidToken).Code != "identity.magic_link_invalid" {
		t.Error("Msg codes")
	}
}

func FuzzParseToken(f *testing.F) {
	tok, _ := NewToken()
	f.Add(string(tok))
	f.Fuzz(func(t *testing.T, s string) {
		if _, ok := ParseToken(s); ok && len(s) != 43 {
			t.Fatalf("accepted %q", s)
		}
	})
}

func FuzzNormalizeEmail(f *testing.F) {
	f.Add("max.mustermann@example.com")
	f.Fuzz(func(t *testing.T, s string) {
		got, err := NormalizeEmail(s)
		if err != nil {
			return
		}
		if strings.ContainsAny(got, " <>\r\n\"") || !strings.Contains(got, "@") || len(got) > 254 {
			t.Fatalf("NormalizeEmail(%q) = %q", s, got)
		}
		if again, err := NormalizeEmail(got); err != nil || again != got {
			t.Fatalf("not idempotent: %q -> %q (%v)", got, again, err)
		}
	})
}

func TestSessionRotationRule(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s := Session{CreatedAt: now, LastSeen: now, ExpiresAt: now.Add(SessionMaxAge)}
	if s.NeedsRotation(now.Add(SessionRotate-time.Second)) || !s.NeedsRotation(now.Add(SessionRotate)) {
		t.Error("rotation after SessionRotate since sign-in")
	}
	s.RotatedAt = now.Add(2 * time.Hour)
	if s.NeedsRotation(now.Add(2*time.Hour+30*time.Minute)) || !s.NeedsRotation(now.Add(3*time.Hour)) {
		t.Error("rotation after SessionRotate since the last rotation")
	}
	s.ReplacedAt = now.Add(3 * time.Hour)
	if s.NeedsRotation(now.Add(4 * time.Hour)) {
		t.Error("a replaced token rotates again")
	}
	if !s.Valid(s.ReplacedAt.Add(sessionGrace-time.Second)) || s.Valid(s.ReplacedAt.Add(sessionGrace)) {
		t.Error("grace period of a replaced token")
	}
}

func TestRecoveryWindow(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	r := NewRecovery(now)
	for _, c := range []struct {
		at    time.Duration
		ready bool
	}{
		{0, false}, {RecoveryDelay - time.Second, false}, {RecoveryDelay, true},
		{RecoveryDelay + RecoveryWindow - time.Second, true}, {RecoveryDelay + RecoveryWindow, false},
	} {
		if r.Ready(now.Add(c.at)) != c.ready {
			t.Errorf("Ready(+%v) = %v", c.at, !c.ready)
		}
	}
}
