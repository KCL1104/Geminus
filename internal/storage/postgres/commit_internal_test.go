package postgres

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// A COMMIT that failed in transit may still have committed on the server.
// Only a server answer or an error that guarantees nothing was sent is a
// known outcome (review of T-006).
func TestCommitOutcome(t *testing.T) {
	if err := commitError(&pgconn.PgError{Code: "40001"}); !errors.Is(err, ErrRetryable) || errors.Is(err, ErrOutcomeUnknown) {
		t.Errorf("server rejected the commit: %v", err)
	}
	if err := commitError(io.ErrUnexpectedEOF); !errors.Is(err, ErrOutcomeUnknown) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("connection lost during commit: %v", err)
	}
	if err := commitError(context.Canceled); !errors.Is(err, ErrOutcomeUnknown) || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled during commit: %v", err)
	}
}
