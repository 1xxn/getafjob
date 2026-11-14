// Command staticpage writes the maintenance page Caddy serves while serve does not answer (#518). It runs from go
// generate in internal/web/views after templ; the output in deploy/maintenance is committed and checked by
// generate:check. The colors come from the tokens in ui.css, so the page follows the app's look.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/1xxn/getafjob/internal/web/views"
)

// tokens are the custom properties the page uses.
var tokens = []string{"bg", "surface", "border", "text", "text-2", "accent", "font"}

const layout = `:root{color-scheme:light dark;%s}
@media (prefers-color-scheme:dark){:root{%s}}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:grid;place-items:center;padding:16px;background:var(--bg);color:var(--text);
font-family:var(--font);line-height:1.5}
main{width:100%%;max-width:28rem;padding:24px;background:var(--surface);border:1px solid var(--border);
border-radius:12px;text-align:center}
.brand{margin:0 0 16px;font-weight:600;color:var(--accent)}
section+section{margin-top:20px;padding-top:20px;border-top:1px solid var(--border)}
h1{margin:0 0 8px;font-size:1.375rem;line-height:1.25}
section p{margin:0;color:var(--text-2)}
`

// Paths from internal/web/views, where go generate runs.
const (
	stylesheet = "../static/ui.css"
	out        = "../../../deploy/maintenance/index.html"
)

func main() {
	css, err := os.ReadFile(stylesheet)
	if err != nil {
		fail(err)
	}
	light, dark, err := colors(string(css))
	if err != nil {
		fail(err)
	}
	var b bytes.Buffer
	if err := views.StaticMaintenance(fmt.Sprintf(layout, light, dark)).Render(context.Background(), &b); err != nil {
		fail(err)
	}
	b.WriteString("\n")
	if err := os.WriteFile(out, b.Bytes(), 0o644); err != nil { //nolint:gosec // a public page, committed
		fail(err)
	}
}

// colors returns the declarations of the light tokens (first definition) and of those the dark scheme overrides.
func colors(css string) (light, dark string, err error) {
	darkAt := strings.Index(css, "prefers-color-scheme: dark")
	if darkAt < 0 {
		return "", "", fmt.Errorf("ui.css: no dark scheme")
	}
	var l, d strings.Builder
	for _, name := range tokens {
		rx := regexp.MustCompile(`--` + regexp.QuoteMeta(name) + `:\s*([^;]+);`)
		m := rx.FindStringSubmatch(css)
		if m == nil {
			return "", "", fmt.Errorf("ui.css: no token --%s", name)
		}
		fmt.Fprintf(&l, "--%s:%s;", name, m[1])
		if m := rx.FindStringSubmatch(css[darkAt:]); m != nil {
			fmt.Fprintf(&d, "--%s:%s;", name, m[1])
		}
	}
	return l.String(), d.String(), nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "staticpage:", err)
	os.Exit(1)
}
