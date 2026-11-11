// Package views holds the templ components of the web UI. Every text comes from the message catalogs.
package views

//go:generate go tool templ generate

import (
	"context"

	"github.com/1xxn/getafjob/internal/platform/i18n"
)

type localeKey struct{}

// WithLocale stores the reader's locale for T.
func WithLocale(ctx context.Context, l i18n.Locale) context.Context {
	return context.WithValue(ctx, localeKey{}, l)
}

// Locale returns the reader's locale (default English).
func Locale(ctx context.Context) i18n.Locale {
	if l, ok := ctx.Value(localeKey{}).(i18n.Locale); ok {
		return l
	}
	return i18n.Default
}

// T renders a catalog text in the reader's locale; params come in name/value pairs.
func T(ctx context.Context, key string, params ...any) string {
	var m map[string]any
	if len(params) > 1 {
		m = make(map[string]any, len(params)/2)
		for i := 0; i+1 < len(params); i += 2 {
			if k, ok := params[i].(string); ok {
				m[k] = params[i+1]
			}
		}
	}
	return i18n.T(Locale(ctx), key, m)
}

// Page is the frame data of every page.
type Page struct {
	Title    string
	UserName string // empty: signed out
	// Passkeys loads the passkey script (pages with passkey buttons).
	Passkeys bool
	// Handoff loads the remote browser screencast script.
	Handoff bool
	// Theme forces "light" or "dark"; empty follows the system setting.
	Theme string
	// StyleGuide loads the style guide styles (development builds only).
	StyleGuide bool
}
