package runner

import (
	"context"
	"errors"
	"testing"
)

// T-035 (D-095 (a)): the engine line names how the attempt's engine run
// ended: completed, cancelled by a request (the attempt itself still
// live), stopped (the attempt's context ended: lease, shutdown) or failed.
func TestEngineOutcome(t *testing.T) {
	live := context.Background()
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	boom := errors.New("boom")
	for _, c := range []struct {
		err, cause error
		attempt    context.Context
		want       string
	}{
		{nil, nil, live, "completed"},
		{nil, errCancelRequested, ended, "completed"},
		{boom, errCancelRequested, live, "cancelled"},
		{boom, errCancelRequested, ended, "stopped"},
		{boom, context.Canceled, ended, "stopped"},
		{boom, nil, live, "failed"},
		{boom, context.DeadlineExceeded, live, "failed"},
	} {
		if got := engineOutcome(c.err, c.cause, c.attempt); got != c.want {
			t.Errorf("engineOutcome(%v, %v, attempt err %v) = %s, want %s", c.err, c.cause, c.attempt.Err(), got, c.want)
		}
	}
}
