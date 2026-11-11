package views

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"sync"
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
