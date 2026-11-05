package httpx

import (
	"net/http"
)

// DefaultCSP allows only own scripts, styles and images; no framing, no inline scripts.
const DefaultCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; manifest-src 'self'; worker-src 'self'; form-action 'self'; " +
	"base-uri 'none'; frame-ancestors 'none'; require-trusted-types-for 'script'"

// SecurityHeaders sets the security headers on every response. Enable hsts only behind HTTPS.
func SecurityHeaders(next http.Handler, hsts bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", DefaultCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()")
		if hsts {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// Upload allows a larger body for POST requests to one exact path.
type Upload struct {
	Path string
	Max  int64
}

// Protect combines the standard protection layers of the public web role: security headers,
// cross-origin protection for state-changing requests (CSRF) and a body limit (larger for the given uploads).
func Protect(next http.Handler, maxBody int64, hsts bool, uploads ...Upload) http.Handler {
	cop := http.NewCrossOriginProtection()
	limited := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := maxBody
		for _, u := range uploads {
			if r.Method == http.MethodPost && r.URL.Path == u.Path {
				limit = u.Max
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
	return SecurityHeaders(cop.Handler(limited), hsts)
}
