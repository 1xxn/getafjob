package ats

import (
	"regexp"
	"strings"

	"github.com/1xxn/getafjob/internal/runner/browser"
)

var (
	easyApplyRx = regexp.MustCompile(`(?i)easy apply|einfach bewerben|indeed bewerbung|apply now|jetzt bewerben`)
	reviewRx    = regexp.MustCompile(`(?i)\breview\b|überprüf|ueberpruef|zusammenfassung`)
	submitRx    = regexp.MustCompile(`(?i)submit application|bewerbung (einreichen|absenden|abschicken)|send application`)
)

// IsPortal reports whether the adapter name represents an Easy Apply portal.
func IsPortal(adapterName string) bool {
	return adapterName == LinkedIn || adapterName == Indeed
}

// IsSessionExpired checks whether the page indicates an unauthenticated session or login challenge.
func IsSessionExpired(portal, rawURL, htmlText string) bool {
	u := strings.ToLower(rawURL)
	html := strings.ToLower(htmlText)

	if portal == LinkedIn || strings.Contains(u, "linkedin.com") {
		if strings.Contains(u, "/login") || strings.Contains(u, "/checkpoint/") || strings.Contains(u, "/authwall") {
			return true
		}
		if strings.Contains(html, "sign in to linkedin") || strings.Contains(html, "join linkedin") ||
			strings.Contains(html, "anmelden, um fortzufahren") {
			return true
		}
	}

	if portal == Indeed || strings.Contains(u, "indeed.com") {
		if strings.Contains(u, "secure.indeed.com/account/login") || strings.Contains(u, "secure.indeed.com/auth") {
			return true
		}
		if strings.Contains(html, "sign in to indeed") || strings.Contains(html, "bei indeed anmelden") ||
			strings.Contains(html, "create an account or sign in") {
			return true
		}
	}

	return false
}

// FindEasyApplyButton locates the primary button that launches the Easy Apply flow on the portal.
func FindEasyApplyButton(st *browser.State) (browser.Button, bool) {
	for _, b := range st.Buttons() {
		if b.Disabled || b.Cookie {
			continue
		}
		if easyApplyRx.MatchString(b.Text) {
			return b, true
		}
	}
	return browser.Button{}, false
}

// IsReviewStep reports whether the current form page is the final review step before submitting.
func IsReviewStep(st *browser.State) bool {
	if st == nil {
		return false
	}
	for _, f := range st.Frames {
		for _, h := range f.Headings {
			if reviewRx.MatchString(h) {
				return true
			}
		}
	}
	for _, b := range st.Buttons() {
		if submitRx.MatchString(b.Text) {
			return true
		}
	}
	return false
}

// FindSubmitButton locates the final submit button on a portal review step.
func FindSubmitButton(st *browser.State) (browser.Button, bool) {
	for _, b := range st.Buttons() {
		if b.Disabled || b.Cookie {
			continue
		}
		if submitRx.MatchString(b.Text) {
			return b, true
		}
	}
	return browser.Button{}, false
}
