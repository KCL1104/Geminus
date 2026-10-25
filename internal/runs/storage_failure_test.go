package runs

// T-054 (H-022, D-069 (b)): createSimulation when the dataset binding
// stage cannot read a snapshot the version binds; review round 1 (L1, L2,
// L3): one shared rule (internal/storage/datasets: Failure,
// StorageFailure, ReadError) and one 503 (httpapi.DatasetStorageFailed).

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/storage/datasets"
	"github.com/kcl1104/geminus/internal/tenant"
)

const secret = "open /srv/objects/tenant/secret: input/output error"

// createSimulation's answer to each kind of binding-stage error, as freeze
// returns it (datasets.Failure): a snapshot that could not be read while
// the request was alive is 503 DATASET_STORAGE_FAILED; any other error of
// the stage is a server fault, 500 INTERNAL_ERROR logged at ERROR by
// httpapi (L1); a request that ended, or a read that says it did, is the
// ended request's 503 without details, whatever the store said (L2). The
// shared rule is internal/storage/datasets' TestFailure.
func TestBindingFailure(t *testing.T) {
	read := &datasets.ReadError{DatasetVersionID: "dsv_01M4YDQK00NMKD3J34KSJZVY40", Err: errors.New(secret)}
	fault := fmt.Errorf("hashing a shape-checked manifest: %w", errors.New("invalid"))
	decode := fmt.Errorf("schema-valid snapshot does not decode: %w", io.ErrUnexpectedEOF)
	live := context.Background()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, c := range []struct {
		name     string
		ctx      context.Context
		err      error
		status   int
		code     string
		detail   string
		errorLog bool
	}{
		{"a read failure", live, read, 503, "SERVICE_UNAVAILABLE", "DATASET_STORAGE_FAILED", false},
		{"a server fault", live, fault, 500, "INTERNAL_ERROR", "", true},
		{"a snapshot that does not decode", live, decode, 500, "INTERNAL_ERROR", "", true},
		{"a read past its deadline", live, &datasets.ReadError{Err: fmt.Errorf("get: %w", context.DeadlineExceeded)}, 503, "SERVICE_UNAVAILABLE", "", false},
		{"a cancelled request, the store's own error", cancelled, read, 503, "SERVICE_UNAVAILABLE", "", false},
		{"a cancelled request, a server fault", cancelled, fault, 503, "SERVICE_UNAVAILABLE", "", false},
		{"a request past its deadline, the store's own error", expired, read, 503, "SERVICE_UNAVAILABLE", "", false},
	} {
		var logs bytes.Buffer
		w := httptest.NewRecorder()
		httpapi.WriteError(w, httptest.NewRequest("POST", "/v1/simulations", nil), slog.New(slog.NewJSONHandler(&logs, nil)),
			Refused(datasets.Failure(c.ctx, c.err)))
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Details []struct {
					Code string `json:"code"`
				} `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		detail := ""
		if len(env.Error.Details) > 0 {
			detail = env.Error.Details[0].Code
		}
		if w.Code != c.status || env.Error.Code != c.code || detail != c.detail || len(env.Error.Details) > 1 {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "hashing") {
			t.Errorf("%s: the cause is sent: %s", c.name, w.Body)
		}
		if got := strings.Contains(logs.String(), `"level":"ERROR"`); got != c.errorLog {
			t.Errorf("%s: ERROR log %v, want %v: %s", c.name, got, c.errorLog, logs.String())
		}
	}
}

// H-022, D-069 (b): createSimulation answers a snapshot that could not be
// read (datasets.ReadError) with 503 DATASET_STORAGE_FAILED, as
// approveProposal and createManualScenario do, never with the cause; a
// server fault, an ended request, a tenant or a transport error pass to
// httpapi.
func TestRefusedDatasetStorage(t *testing.T) {
	storage := &datasets.ReadError{DatasetVersionID: "dsv_01M4YDQK00NMKD3J34KSJZVY40", Err: errors.New(secret)}
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
		tenant.ErrRetryable, errors.New("database down"), fmt.Errorf("hashing a shape-checked manifest: %w", errors.New("invalid")),
		errors.Join(context.Canceled, storage)} {
		if got := Refused(err); got != err {
			t.Errorf("%v became %v", err, got)
		}
	}
}
