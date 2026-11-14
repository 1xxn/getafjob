package gateway

import (
	"errors"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/1xxn/getafjob/internal/application"
)

// The runner tells an approval (the user decides) from a guard (the server parked) and from a failure by the code.
func TestCodeOfSubmitAnswers(t *testing.T) {
	for err, want := range map[error]connect.Code{
		&application.ApprovalError{}:   connect.CodeAborted,
		application.ErrExcluded:        connect.CodePermissionDenied,
		application.ErrDuplicate:       connect.CodePermissionDenied,
		application.ErrCompanyWindow:   connect.CodePermissionDenied,
		application.ErrTransition:      connect.CodeFailedPrecondition,
		errors.New("database is down"): connect.CodeUnknown,
	} {
		if got := connect.CodeOf(code(err)); got != want {
			t.Errorf("%v: %v, want %v", err, got, want)
		}
	}
}
