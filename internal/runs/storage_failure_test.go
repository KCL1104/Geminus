package runs

// T-054 (H-022, D-069 (b)): createSimulation when the dataset binding
// stage cannot read or verify a snapshot the version binds.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/tenant"
)

// A binding stage that fails with an error rather than with issues is a
// storage failure (DatasetStorageError), unless the request ended or the
// error says it did: then the context's error stays, and httpapi answers
// it as it answers any ended request. The pattern of the approval's and
// the scenarios' TestBindingFailure.
func TestBindingFailure(t *testing.T) {
	cause := errors.New("the object store did not answer")
	var se *DatasetStorageError
	if err := bindingFailure(context.Background(), cause); !errors.As(err, &se) || !errors.Is(err, cause) {
		t.Errorf("a read failure: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for name, c := range map[string]struct {
		ctx  context.Context
		err  error
		want error
	}{
		"a cancelled request":                        {cancelled, fmt.Errorf("reading: %w", context.Canceled), context.Canceled},
		"a cancelled request, the store's own error": {cancelled, cause, cause},
		"a request past its deadline":                {expired, fmt.Errorf("reading: %w", context.DeadlineExceeded), context.DeadlineExceeded},
		"a request past its deadline, the store's":   {expired, cause, cause},
		// The request is alive, but the error says it ended: still the
		// context's error, not the store's failure.
		"a live request, a cancelled read": {context.Background(), fmt.Errorf("reading: %w", context.Canceled), context.Canceled},
		"a live request, a read past its deadline": {context.Background(), fmt.Errorf("reading: %w", context.DeadlineExceeded),
			context.DeadlineExceeded},
	} {
		err := bindingFailure(c.ctx, c.err)
		if errors.As(err, &se) || !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// H-022, D-069 (b): createSimulation answers snapshots that cannot be read
// or verified with 503 DATASET_STORAGE_FAILED, as approveProposal and
// createManualScenario do, never with the cause; an ended request, a
// tenant or a transport error pass to httpapi.
func TestRefusedDatasetStorage(t *testing.T) {
	storage := &DatasetStorageError{Err: errors.New("open /srv/objects/tenant/secret: input/output error")}
	for _, err := range []error{storage, fmt.Errorf("freeze: %w", storage)} {
		var e *httpapi.Error
		if !errors.As(Refused(err), &e) || e.Code != httpapi.ServiceUnavailable ||
			e.Message != "the dataset snapshots could not be read; nothing was queued (retry later)" ||
			!slices.Equal(e.Details, []httpapi.Detail{{Path: "", Reason: "the dataset storage failed", Code: "DATASET_STORAGE_FAILED"}}) {
			t.Errorf("%v: %+v", err, Refused(err))
			continue
		}
		// What the client is sent: the code, the message and the details.
		if sent := fmt.Sprintf("%s %s %+v", e.Code, e.Message, e.Details); strings.Contains(sent, "secret") {
			t.Errorf("the cause is sent: %s", sent)
		}
	}
	// No object storage at all is the other 503, without details.
	var e *httpapi.Error
	if !errors.As(Refused(ErrStorageUnavailable), &e) || e.Code != httpapi.ServiceUnavailable || len(e.Details) != 0 {
		t.Errorf("without object storage: %+v", Refused(ErrStorageUnavailable))
	}
	for _, err := range []error{context.Canceled, fmt.Errorf("reading: %w", context.DeadlineExceeded), tenant.ErrForbidden,
		tenant.ErrRetryable, errors.New("database down")} {
		if got := Refused(err); got != err {
			t.Errorf("%v became %v", err, got)
		}
	}
}
