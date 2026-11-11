package web

import (
	"errors"
	"io"
	"testing"

	"github.com/1xxn/getafjob/internal/discovery/urlimport"
)

func TestImportErrorKeyNamesTheCause(t *testing.T) {
	cases := map[error]string{
		urlimport.ErrInvalidURL: "jobs.import_error.invalid",
		urlimport.ErrNoJobFound: "jobs.import_error.no_job",
		errors.Join(urlimport.ErrFetchFailed, io.ErrUnexpectedEOF): "jobs.import_error.fetch",
		errors.New("other"): "jobs.import_error",
	}
	for err, want := range cases {
		if got := importErrorKey(err); got != want {
			t.Errorf("%v: %s, want %s", err, got, want)
		}
	}
}
