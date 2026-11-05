//go:build !linux

package cvimport

// limit is a no-op outside Linux (development machines); the timeout and the process boundary still apply.
func limit() error { return nil }
