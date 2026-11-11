// Package applymode tells how a posting takes applications (#159): through a form the runner can fill, on an
// external job portal that only shows the posting, or by email. It knows hosts only, so runner, modules and web
// can share it.
package applymode

import (
	"net/url"
	"strings"
)

// Mode is how a posting takes applications.
type Mode string

// Modes.
const (
	Form   Mode = "form"   // an application form, maybe behind a link the runner follows
	Portal Mode = "portal" // a job board page; the application happens elsewhere, often by email or on the employer's site
	Email  Mode = "email"  // a mailto address
)

// portals are job boards and aggregators whose pages show postings but rarely an application form of their own.
// LinkedIn and Indeed are not here: their Easy Apply runs through the portals module.
var portals = []string{
	"arbeitsagentur.de", "arbeitnow.com", "arbeitnow.co.uk", "arbeitnow.fr", "arbeitnow.ch", "arbeitnow.nl",
	"arbeitnow.de", "himalayas.app", "stepstone.de", "stepstone.at", "stepstone.com", "xing.com", "monster.de",
	"monster.com", "jobware.de", "stellenanzeigen.de", "jobexport.de", "germantechjobs.de", "gute-jobs.de",
	"get-in-it.de", "stellenonline.de", "jobvector.de", "meinestadt.de", "kimeta.de", "jobs.de", "heyjobs.co",
	"glassdoor.com", "glassdoor.de", "kununu.com", "empfehlungsbund.de", "finest-jobs.com",
}

// Of returns the mode of a posting from its page URL and its application URL (may be empty). An application URL
// that leaves the portal counts as a form.
func Of(pageURL, applyURL string) Mode {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(applyURL)), "mailto:") {
		return Email
	}
	target := applyURL
	if strings.TrimSpace(target) == "" {
		target = pageURL
	}
	if IsPortal(Host(target)) {
		return Portal
	}
	return Form
}

// Host returns the lower-case host of a URL, empty if it has none.
func Host(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// IsPortal reports a job board host (also its subdomains, like www.).
func IsPortal(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, p := range portals {
		if host == p || strings.HasSuffix(host, "."+p) {
			return true
		}
	}
	return false
}
