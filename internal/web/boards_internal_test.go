package web

import (
	"errors"
	"io"
	"testing"

	"github.com/1xxn/getafjob/internal/discovery/urlimport"
)

func TestBoardErrorKey(t *testing.T) {
	for err, want := range map[error]string{
		urlimport.ErrInvalidURL:                       "jobs.board_add_error.invalid",
		errors.Join(urlimport.ErrFetchFailed, io.EOF): "jobs.import_error.fetch",
		urlimport.ErrUnsupportedBoard:                 "jobs.board_add_error",
		errors.New("database is gone"):                "",
	} {
		if got := boardErrorKey(err); got != want {
			t.Errorf("boardErrorKey(%v) = %q, want %q", err, got, want)
		}
	}
}
