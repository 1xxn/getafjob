package views

import (
	"io/fs"

	"github.com/1xxn/getafjob/internal/web/views/ui"
)

// SetAssets records a content hash per static file; see ui.SetAssets.
func SetAssets(fsys fs.FS) error { return ui.SetAssets(fsys) }

// Asset returns the URL of a static file with its content version, e.g. /static/app.css?v=1a2b3c4d5e6f.
func Asset(p string) string { return ui.Asset(p) }

// AssetVersion returns the current content version of a static file, empty if unknown.
func AssetVersion(p string) string { return ui.AssetVersion(p) }
