// Package buildinfo reports version, commit and build time of the binary.
//
// Release builds set the values via -ldflags (goreleaser, Dockerfile); local builds fall back to the VCS
// information the Go compiler embeds on its own.
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"sync"
)

// Set via -ldflags "-X github.com/1xxn/getafjob/internal/platform/buildinfo.Version=…".
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Info describes the running build.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go"`
	Modified  bool   `json:"modified,omitempty"`
}

var (
	once sync.Once
	info Info
)

// Get returns the build information (computed once).
func Get() Info {
	once.Do(func() {
		info = Info{Version: Version, Commit: Commit, Date: Date, GoVersion: runtime.Version()}
		bi, ok := debug.ReadBuildInfo()
		if !ok {
			return
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = s.Value
				}
			case "vcs.time":
				if info.Date == "" {
					info.Date = s.Value
				}
			case "vcs.modified":
				info.Modified = s.Value == "true"
			}
		}
	})
	return info
}

// String returns a short one-line form, e.g. "v0.1.0 (abc1234, 2026-11-04T10:00:00Z, go1.27.3)".
func (i Info) String() string {
	c := i.Commit
	if len(c) > 7 {
		c = c[:7]
	}
	if i.Modified {
		c += "+dirty"
	}
	if c == "" {
		c = "unknown"
	}
	d := i.Date
	if d == "" {
		d = "unknown"
	}
	return i.Version + " (" + c + ", " + d + ", " + i.GoVersion + ")"
}
