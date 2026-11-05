//go:build linux

package cvimport

import "syscall"

// limit caps the child: address space, CPU seconds and file size 0 (it may write only to its stdout pipe).
func limit() error {
	for _, l := range []struct {
		res int
		max uint64
	}{
		{syscall.RLIMIT_AS, 2 << 30},
		{syscall.RLIMIT_CPU, 15},
		{syscall.RLIMIT_FSIZE, 0},
	} {
		if err := syscall.Setrlimit(l.res, &syscall.Rlimit{Cur: l.max, Max: l.max}); err != nil {
			return err
		}
	}
	return nil
}
