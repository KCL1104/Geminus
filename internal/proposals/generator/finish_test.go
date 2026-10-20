package generator

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"testing"

	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
)

// What the database still refuses to store when Finish writes the outcome
// (a text with U+0000, a jsonb with \u0000, a content check) is the
// attempt's failure for good, not an INTERNAL error the worker retries by
// asking the provider again (H-003 review M2).
func TestFinishFailures(t *testing.T) {
	var f *jobs.Failure
	err := fmt.Errorf("writing: %w", store.ErrUnstorable)
	if !errors.As(finishFailure(err), &f) || f.Code != OutputUnstorable || f.Retryable || f.Message == "" {
		t.Fatalf("%v: %v", err, finishFailure(err))
	}
	// The API shows it as INTERNAL_ERROR, with the failure's own code.
	stored, _ := json.Marshal(f)
	p, perr := proposals.Public(store.Proposal{ID: "aip_01M4YDQK00ADN261CME3WV5HZ6", Status: store.Failed, Mode: "edit",
		Failure: stored, RequestID: "req_01M4YDQK00V6H8S0ST4F7510KR"}, "")
	if perr != nil || p.Error.Error.Code != "INTERNAL_ERROR" || p.Error.Error.Details[0].Code != OutputUnstorable {
		t.Errorf("shown as %+v %v", p.Error, perr)
	}
	other := errors.New("connection reset")
	if finishFailure(other) != other {
		t.Error("another error became a failure")
	}
	if finishFailure(nil) != nil {
		t.Error("nil became an error")
	}
}
