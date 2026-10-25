package approval

// T-054 round 2: the draft sync of an approval (D-069 (b)); review round 1
// (L3): it fails with the same datasets.ReadError as the revalidation, so
// one mapping answers both.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/storage/datasets"
)

// An edit approval moves a clean draft to the new version
// (models.Service.Advance), which checks the draft's dataset bindings with
// the snapshots read once more. A snapshot that cannot be read then is a
// datasets.ReadError, as in the revalidation, wrapped by models and the
// approval's own error context; the approval answers it exactly as it
// answers the revalidation's (503 DATASET_STORAGE_FAILED), never with the
// cause and never as a server error. A request that ended during the draft
// sync (models applies datasets.Failure) passes to httpapi.
func TestRefusedDraftSyncStorage(t *testing.T) {
	const secret = "open /srv/objects/tenant/secret: input/output error"
	read := &datasets.ReadError{DatasetVersionID: "dsv_01M4YDQK00NMKD3J34KSJZVY40", Err: errors.New(secret)}
	revalidation := Refused(read)
	var want *httpapi.Error
	if !errors.As(revalidation, &want) || want.Code != httpapi.ServiceUnavailable ||
		want.Message != "the dataset snapshots could not be read; nothing was changed (retry later)" ||
		!slices.Equal(want.Details, []httpapi.Detail{{Path: "", Reason: "the dataset storage failed", Code: "DATASET_STORAGE_FAILED"}}) {
		t.Fatalf("the revalidation's storage failure: %+v", revalidation)
	}
	for _, err := range []error{fmt.Errorf("advance: %w", read), fmt.Errorf("approve: %w", fmt.Errorf("sync the draft: %w", read))} {
		got := Refused(err)
		if !reflect.DeepEqual(got, revalidation) {
			t.Errorf("%v: %+v, not the revalidation's %+v", err, got, revalidation)
		}
		if strings.Contains(fmt.Sprint(got), "secret") {
			t.Errorf("the cause is sent: %v", got)
		}
		// The handler logs the read failure, with its cause.
		if logged, ok := datasets.StorageFailure(err); !ok || logged != read || !strings.Contains(logged.Error(), secret) {
			t.Errorf("%v: the logged failure is %v %v", err, logged, ok)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, err := range []error{context.Canceled, fmt.Errorf("sync: %w", context.DeadlineExceeded), errors.New("a server fault"),
		datasets.Failure(cancelled, fmt.Errorf("advance: %w", read))} {
		if got := Refused(err); got != err {
			t.Errorf("%v was mapped: %v", err, got)
		}
		if _, ok := datasets.StorageFailure(err); ok {
			t.Errorf("%v is not a storage failure", err)
		}
	}
}
