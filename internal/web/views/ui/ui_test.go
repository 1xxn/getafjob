package ui_test

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/web/views"
)

var tokenRx = regexp.MustCompile(`--([a-z0-9-]+):\s*(#[0-9a-f]{6})\s*;`)

// themeBlocks returns the color tokens of the light theme, the system dark theme and the manual dark theme.
func themeBlocks(t *testing.T) (light, sysDark, dark map[string]string) {
	t.Helper()
	b, err := os.ReadFile("../../static/ui.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	block := func(start string) map[string]string {
		i := strings.Index(css, start)
		if i < 0 {
			t.Fatalf("ui.css: no %q", start)
		}
		j := strings.Index(css[i:], "}")
		m := map[string]string{}
		for _, g := range tokenRx.FindAllStringSubmatch(css[i:i+j], -1) {
			m[g[1]] = g[2]
		}
		return m
	}
	return block(":root {"), block(`:root:not([data-theme="light"]) {`), block(`:root[data-theme="dark"] {`)
}

func luminance(hex string) float64 {
	ch := func(s string) float64 {
		v, _ := strconv.ParseUint(s, 16, 8)
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(hex[1:3]) + 0.7152*ch(hex[3:5]) + 0.0722*ch(hex[5:7])
}

func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// WCAG 2.2 AA: 4.5:1 for text, 3:1 for borders, focus rings and other UI parts.
func TestTokenContrast(t *testing.T) {
	light, sysDark, dark := themeBlocks(t)
	if len(sysDark) != len(dark) {
		t.Fatalf("dark themes differ: %d system tokens, %d manual tokens", len(sysDark), len(dark))
	}
	for k, v := range dark {
		if sysDark[k] != v {
			t.Errorf("dark token --%s: system %s, manual %s", k, sysDark[k], v)
		}
	}
	text := [][2]string{
		{"text", "bg"}, {"text", "surface"}, {"text", "surface-2"}, {"text-2", "surface"}, {"text-2", "surface-2"},
		{"text-3", "bg"}, {"text-3", "surface"}, {"text-3", "surface-2"}, {"accent-text", "bg"}, {"accent-text", "surface"},
		{"on-accent", "accent"}, {"on-accent", "accent-hover"}, {"surface", "danger"}, {"surface", "danger-hover"},
		{"accent-text", "accent-soft"}, {"success-text", "success-soft"}, {"warning-text", "warning-soft"},
		{"danger-text", "danger-soft"}, {"info-text", "info-soft"}, {"success-text", "surface"},
		{"warning-text", "surface"}, {"danger-text", "surface"}, {"info-text", "surface"}, {"text", "warning-soft"},
		{"text", "danger-soft"}, {"text", "success-soft"}, {"text", "info-soft"}, {"bg", "text"},
	}
	ui := [][2]string{
		{"border-strong", "surface"}, {"border-strong", "bg"}, {"focus", "bg"}, {"focus", "surface"},
		{"accent", "surface"}, {"danger", "surface"}, {"success", "success-soft"}, {"warning", "warning-soft"},
		{"danger", "danger-soft"}, {"info", "info-soft"}, {"accent", "accent-soft"},
	}
	for name, m := range map[string]map[string]string{"light": light, "dark": dark} {
		check := func(pairs [][2]string, minRatio float64) {
			for _, p := range pairs {
				fg, bg := m[p[0]], m[p[1]]
				if fg == "" || bg == "" {
					t.Errorf("%s: token missing in %v", name, p)
					continue
				}
				if r := contrast(fg, bg); r < minRatio {
					t.Errorf("%s: --%s on --%s is %.2f:1, want %.1f:1", name, p[0], p[1], r, minRatio)
				}
			}
		}
		check(text, 4.5)
		check(ui, 3)
		for i := range 6 {
			a := m["avatar-"+strconv.Itoa(i)]
			if r := contrast("#ffffff", a); r < 4.5 {
				t.Errorf("%s: avatar-%d %.2f:1", name, i, r)
			}
		}
	}
}

// Every icon used in a template and listed in the style guide exists in the sprite, and the other way round.
func TestIconSprite(t *testing.T) {
	b, err := os.ReadFile("../../static/icons.svg")
	if err != nil {
		t.Fatal(err)
	}
	sprite := map[string]bool{}
	for _, m := range regexp.MustCompile(`<symbol id="([a-z0-9-]+)"`).FindAllStringSubmatch(string(b), -1) {
		sprite[m[1]] = true
	}
	listed := map[string]bool{}
	for _, n := range views.SGIcons() {
		listed[n] = true
		if !sprite[n] {
			t.Errorf("style guide icon %q not in sprite", n)
		}
	}
	for n := range sprite {
		if !listed[n] {
			t.Errorf("sprite icon %q missing from the style guide", n)
		}
	}
	files, _ := filepath.Glob("../*.templ")
	more, _ := filepath.Glob("*.templ")
	use := regexp.MustCompile(`(?:Icon\(|Icon: )"([a-z0-9-]+)"`)
	for _, f := range append(files, more...) {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range use.FindAllStringSubmatch(string(src), -1) {
			if !sprite[m[1]] {
				t.Errorf("%s: icon %q not in sprite", f, m[1])
			}
		}
	}
}
