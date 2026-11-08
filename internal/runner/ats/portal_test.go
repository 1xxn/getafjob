package ats

import (
	"testing"

	"github.com/1xxn/getafjob/internal/runner/browser"
)

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

func TestReviewStepDetection(t *testing.T) {
	stReview := &browser.State{Frames: []*browser.Frame{{
		Headings: []string{"Review your application"},
		Buttons:  []browser.Button{{Text: "Submit application"}},
	}}}
	if !IsReviewStep(stReview) {
		t.Errorf("IsReviewStep = false, want true")
	}

	stForm := &browser.State{Frames: []*browser.Frame{{
		Headings: []string{"Contact info"},
		Buttons:  []browser.Button{{Text: "Next"}},
	}}}
	if IsReviewStep(stForm) {
		t.Errorf("IsReviewStep = true, want false")
	}
}
