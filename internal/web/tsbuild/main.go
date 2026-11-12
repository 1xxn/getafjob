// Command tsbuild bundles the TypeScript islands of the web UI with the esbuild Go API (no Node, no npm). It runs
// from go generate in internal/web; the output in static/js is committed and checked by generate:check.
package main

import (
	"fmt"
	"os"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	res := api.Build(api.BuildOptions{
		EntryPoints:   []string{"ts/passkey.ts", "ts/handoff.ts", "ts/events.ts", "ts/shell.ts", "ts/jobs.ts", "ts/pipeline.ts"},
		Outdir:        "static/js",
		Bundle:        true,
		Format:        api.FormatESModule,
		Target:        api.ES2020,
		Platform:      api.PlatformBrowser,
		Charset:       api.CharsetUTF8,
		LegalComments: api.LegalCommentsNone,
		Write:         true,
		LogLevel:      api.LogLevelWarning,
	})
	if len(res.Errors) > 0 {
		for _, e := range res.Errors {
			fmt.Fprintln(os.Stderr, e.Text)
		}
		os.Exit(1)
	}
}
