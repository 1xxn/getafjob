// Package ui holds the design system of the web UI: templ components that take ready texts as props (callers
// translate through the catalogs), plus the static asset registry and the htmx nonce helper they need.
package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
	"io/fs"
	"strconv"
	"strings"
	"sync"

	"github.com/a-h/templ"
)

var (
	assetsMu sync.RWMutex
	assets   = map[string]string{}
)

// SetAssets records a content hash per static file (paths relative to /static/), so pages link versioned URLs and
// a deploy never pairs new HTML with a cached old script.
func SetAssets(fsys fs.FS) error {
	m := map[string]string{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		m[p] = hex.EncodeToString(sum[:6])
		return nil
	})
	if err != nil {
		return err
	}
	assetsMu.Lock()
	assets = m
	assetsMu.Unlock()
	return nil
}

// Asset returns the URL of a static file with its content version, e.g. /static/app.css?v=1a2b3c4d5e6f.
func Asset(p string) string {
	assetsMu.RLock()
	v := assets[p]
	assetsMu.RUnlock()
	if v == "" {
		return "/static/" + p
	}
	return "/static/" + p + "?v=" + v
}

// AssetVersion returns the current content version of a static file, empty if unknown.
func AssetVersion(p string) string {
	assetsMu.RLock()
	defer assetsMu.RUnlock()
	return assets[p]
}

// HX returns htmx attributes as name/value pairs plus the response's hx-nonce; elements without it are inert
// under hx-csp. Use it for every element with hx-* attributes.
func HX(ctx context.Context, kv ...string) templ.Attributes {
	a := templ.Attributes{"hx-nonce": templ.GetNonce(ctx)}
	for i := 0; i+1 < len(kv); i += 2 {
		a[kv[i]] = kv[i+1]
	}
	return a
}

// Tone is the meaning of a colored element.
type Tone string

// Tones of badges, pills, notices and KPI tiles.
const (
	Neutral Tone = "neutral"
	Accent  Tone = "accent"
	Success Tone = "success"
	Warning Tone = "warning"
	Danger  Tone = "danger"
	Info    Tone = "info"
)

func (t Tone) or(d Tone) Tone {
	if t == "" {
		return d
	}
	return t
}

func iconHref(name string) string { return Asset("icons.svg") + "#" + name }

func itoa(n int) string { return strconv.Itoa(n) }

func joinIDs(ids ...string) string {
	var out []string
	for _, id := range ids {
		if id != "" {
			out = append(out, id)
		}
	}
	return strings.Join(out, " ")
}

// Initials returns up to two letters of a display name for an avatar.
func Initials(name string) string {
	var out []rune
	for _, w := range strings.Fields(name) {
		for _, r := range w {
			out = append(out, r)
			break
		}
		if len(out) == 2 {
			break
		}
	}
	return strings.ToUpper(string(out))
}

// avatarHue picks one of six token colors from the name, so a person keeps the same color.
func avatarHue(name string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return "ui-avatar-" + itoa(int(h.Sum32()%6))
}

func percent(value, maxValue int) int {
	if maxValue <= 0 || value <= 0 {
		return 0
	}
	if value >= maxValue {
		return 100
	}
	return value * 100 / maxValue
}
