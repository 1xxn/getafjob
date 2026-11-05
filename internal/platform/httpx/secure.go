package httpx

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

// CSP returns the policy of one response: only scripts carrying its nonce run, no inline code, no eval, own
// styles and images, no framing. Trusted Types allow only the policy of htmx's hx-csp extension, which in turn
// only accepts elements carrying the nonce (ADR 0024).
func CSP(nonce string) string {
	return "default-src 'none'; script-src 'nonce-" + nonce + "'; style-src 'self'; img-src 'self' data:; " +
		"font-src 'self'; connect-src 'self'; manifest-src 'self'; worker-src 'self'; form-action 'self'; " +
		"base-uri 'none'; frame-ancestors 'none'; require-trusted-types-for 'script'; trusted-types htmx"
}

type nonceKey struct{}

// Nonce returns the CSP nonce of the current response; empty outside SecurityHeaders.
func Nonce(ctx context.Context) string {
	n, _ := ctx.Value(nonceKey{}).(string)
	return n
}

func newNonce() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b) // never fails (crypto/rand)
	return base64.RawURLEncoding.EncodeToString(b)
}

// SecurityHeaders sets the security headers on every response, with a fresh CSP nonce in the request context.
// Enable hsts only behind HTTPS.
func SecurityHeaders(next http.Handler, hsts bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce := newNonce()
		r = r.WithContext(context.WithValue(r.Context(), nonceKey{}, nonce))
		h := w.Header()
		h.Set("Content-Security-Policy", CSP(nonce))
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
