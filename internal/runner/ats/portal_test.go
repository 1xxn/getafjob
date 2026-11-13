package ats

import "testing"

func TestPortalHelpers(t *testing.T) {
	if !IsPortal(LinkedIn) || !IsPortal(Indeed) {
		t.Errorf("IsPortal failed for linkedin/indeed")
	}
	if IsPortal(Greenhouse) || IsPortal(Generic) {
		t.Errorf("IsPortal true for greenhouse/generic")
	}

	if !IsSessionExpired(LinkedIn, "https://www.linkedin.com/login", "") {
		t.Errorf("expected session expired for linkedin login url")
	}
	if !IsSessionExpired(Indeed, "https://secure.indeed.com/account/login", "") {
		t.Errorf("expected session expired for indeed login url")
	}
	if IsSessionExpired(LinkedIn, "https://www.linkedin.com/jobs/view/123", "<html>Welcome</html>") {
		t.Errorf("expected session active")
	}
}
