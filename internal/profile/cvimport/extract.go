package cvimport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/1xxn/getafjob/internal/profile"
)

// ChildCommand is the hidden command of the binary that extracts text in the child process.
const ChildCommand = "extract-text"

// Exit codes of the child.
const (
	exitOK       = 0
	exitNoText   = 3
	exitFileType = 4
	exitTooLarge = 5
	exitUsage    = 2
)

// Extractor runs Text in a child process of the same binary: no environment (no secrets), limited memory, CPU
// time and output, no file writes (Linux), and a timeout. A hostile file can crash or stall only that process.
type Extractor struct {
	exe     string
	timeout time.Duration
}

// NewExtractor uses the running binary.
func NewExtractor() (*Extractor, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &Extractor{exe: exe, timeout: 20 * time.Second}, nil
}

// NewExtractorFor uses exe (tests build the binary themselves).
func NewExtractorFor(exe string, timeout time.Duration) *Extractor {
	return &Extractor{exe: exe, timeout: timeout}
}

// boundedBuffer keeps at most max bytes and drops the rest.
type boundedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// Text extracts the text of a PDF or DOCX in the child process.
func (e *Extractor) Text(ctx context.Context, mime string, data []byte) (string, error) {
	if len(data) > profile.MaxDocument {
		return "", profile.ErrFileTooLarge
	}
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.exe, ChildCommand, mime) //nolint:gosec // our own binary, fixed arguments
	cmd.Env = []string{"GOMEMLIMIT=512MiB", "GOMAXPROCS=1"}
	cmd.Stdin = bytes.NewReader(data)
	out := &boundedBuffer{max: 4 * MaxText}
	cmd.Stdout, cmd.Stderr = out, io.Discard
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return out.buf.String(), nil
	case ctx.Err() != nil:
		return "", profile.ErrFileType // took too long: treat as unreadable
	case errors.As(err, &exit):
		switch exit.ExitCode() {
		case exitNoText:
			return "", ErrNoText
		case exitTooLarge:
			return "", profile.ErrFileTooLarge
		default: // exitFileType, a crash or a limit
			return "", profile.ErrFileType
		}
	default:
		return "", err
	}
}

// RunChild is the child process: it limits itself, reads the file from stdin and writes the text to stdout.
func RunChild(args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) != 1 || (args[0] != profile.MimePDF && args[0] != profile.MimeDOCX) {
		return exitUsage
	}
	if err := limit(); err != nil {
		return exitUsage
	}
	data, err := io.ReadAll(io.LimitReader(stdin, profile.MaxDocument+1))
	if err != nil {
		return exitFileType
	}
	if len(data) > profile.MaxDocument {
		return exitTooLarge
	}
	text, err := Text(args[0], data)
	switch {
	case errors.Is(err, ErrNoText):
		return exitNoText
	case errors.Is(err, profile.ErrFileTooLarge):
		return exitTooLarge
	case err != nil:
		return exitFileType
	}
	if _, err := io.WriteString(stdout, text); err != nil {
		return exitFileType
	}
	return exitOK
}
