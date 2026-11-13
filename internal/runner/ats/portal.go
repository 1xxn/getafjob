package ats

import "strings"

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
