package approval

// T-054 round 2: H-022 Q4 ("一律改為 503"), which the coordinator extended
// to the draft sync of an approval.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/httpapi"
	modelsvc "github.com/kcl1104/geminus/internal/models"
)

// An edit approval moves a clean draft to the new version
// (models.Service.Advance), which checks the draft's dataset bindings with
// the snapshots read once more. Snapshots that cannot be read then are a
// models.DatasetStorageError; the approval answers it exactly as it
// answers the revalidation's (503 DATASET_STORAGE_FAILED), never with the
// cause and never as a server error. A request that ended keeps its
// context's error (models' bindingFailure), which passes to httpapi.
func TestRefusedDraftSyncStorage(t *testing.T) {
	const secret = "open /srv/objects/tenant/secret: input/output error"
	revalidation := Refused(&DatasetStorageError{Err: errors.New(secret)})
	var want *httpapi.Error
	if !errors.As(revalidation, &want) || want.Code != httpapi.ServiceUnavailable ||
		!slices.Equal(want.Details, []httpapi.Detail{{Path: "", Reason: "the dataset storage failed", Code: "DATASET_STORAGE_FAILED"}}) {
		t.Fatalf("the revalidation's storage failure: %+v", revalidation)
	}
	draft := &modelsvc.DatasetStorageError{Err: errors.New(secret)}
	for _, err := range []error{draft, fmt.Errorf("advance: %w", draft)} {
		got := Refused(err)
		if !reflect.DeepEqual(got, revalidation) {
			t.Errorf("%v: %+v, not the revalidation's %+v", err, got, revalidation)
		}
		if strings.Contains(fmt.Sprint(got), "secret") {
			t.Errorf("the cause is sent: %v", got)
		}
		// The handler logs it: the cause is the store's error.
		if cause, ok := storageCause(err); !ok || cause.Error() != secret {
			t.Errorf("%v: the logged cause is %v %v", err, cause, ok)
		}
	}
	if cause, ok := storageCause(&DatasetStorageError{Err: errors.New(secret)}); !ok || cause.Error() != secret {
		t.Errorf("the revalidation's logged cause is %v %v", cause, ok)
	}
	for _, err := range []error{context.Canceled, fmt.Errorf("sync: %w", context.DeadlineExceeded), errors.New("a server fault")} {
		if got := Refused(err); got != err {
			t.Errorf("%v was mapped: %v", err, got)
		}
		if _, ok := storageCause(err); ok {
			t.Errorf("%v is not a storage failure", err)
		}
	}
}
