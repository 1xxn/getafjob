package portals

import (
	"testing"

	"github.com/google/uuid"
)

func TestValidatePortal(t *testing.T) {
	if err := ValidatePortal("linkedin"); err != nil {
		t.Errorf("ValidatePortal(linkedin): %v", err)
	}
	if err := ValidatePortal("INDEED"); err != nil {
		t.Errorf("ValidatePortal(INDEED): %v", err)
	}
	if err := ValidatePortal("glassdoor"); err == nil {
		t.Errorf("ValidatePortal(glassdoor): expected error, got nil")
	}
}

func TestValidateDailyLimit(t *testing.T) {
	if err := ValidateDailyLimit(5); err != nil {
		t.Errorf("ValidateDailyLimit(5): %v", err)
	}
	if err := ValidateDailyLimit(0); err == nil {
		t.Errorf("ValidateDailyLimit(0): expected error")
	}
	if err := ValidateDailyLimit(25); err == nil {
		t.Errorf("ValidateDailyLimit(25): expected error")
	}
}

func TestCanApply(t *testing.T) {
	s := Session{
		UserID:           uuid.New(),
		Portal:           PortalLinkedIn,
		Status:           StatusActive,
		EncryptedCookies: []byte("encrypted"),
		DailyLimit:       5,
	}

	// 1. Clean case: allowed
	ok, reason := CanApply(s, 2, 0)
	if !ok || reason != "" {
		t.Fatalf("CanApply(2, 0) = (%v, %q), want (true, \"\")", ok, reason)
	}

	// 2. Another run already active: blocked (one run per account)
	ok, reason = CanApply(s, 2, 1)
	if ok || reason == "" {
		t.Fatalf("CanApply(2, 1) = (%v, %q), want (false, non-empty)", ok, reason)
	}

	// 3. Daily limit reached: blocked
	ok, reason = CanApply(s, 5, 0)
	if ok || reason == "" {
		t.Fatalf("CanApply(5, 0) = (%v, %q), want (false, non-empty)", ok, reason)
	}

	// 4. Session expired or missing cookies: blocked
	sExpired := s
	sExpired.Status = StatusExpired
	ok, _ = CanApply(sExpired, 0, 0)
	if ok {
		t.Errorf("CanApply on expired session = true, want false")
	}

	sNoCookies := s
	sNoCookies.EncryptedCookies = nil
	ok, _ = CanApply(sNoCookies, 0, 0)
	if ok {
		t.Errorf("CanApply without cookies = true, want false")
	}
}

func TestIsSessionExpired(t *testing.T) {
	cases := []struct {
		url  string
		html string
		want bool
	}{
		{"https://www.linkedin.com/jobs/view/123", "<html>Apply now</html>", false},
		{"https://www.linkedin.com/login?from=feed", "", true},
		{"https://www.linkedin.com/checkpoint/challenge", "", true},
		{"https://www.linkedin.com/authwall?trk=rip", "", true},
		{"https://www.linkedin.com/jobs/view/123", "<div class='authwall'>Sign in to LinkedIn</div>", true},
		{"https://de.indeed.com/viewjob?jk=abc", "<html>Direct apply</html>", false},
		{"https://secure.indeed.com/account/login", "", true},
		{"https://de.indeed.com/viewjob?jk=abc", "<h1>Bei Indeed anmelden</h1>", true},
	}

	for _, c := range cases {
		if got := IsSessionExpired(c.url, c.html); got != c.want {
			t.Errorf("IsSessionExpired(%q, %q) = %v, want %v", c.url, c.html, got, c.want)
		}
	}
}
