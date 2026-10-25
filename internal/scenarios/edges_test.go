package scenarios

// Edge cases found by T-028 review round 1: warnings never refuse a
// scenario (L1), a request that ended keeps its context error (L2), and
// the issue cap of Error.details (I1). Pure: no database.

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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/datasets"
)

// warningPack is the delivery pack with one more rule, which only warns
// about every model.
type warningPack struct{ softwaredelivery.Pack }

func (warningPack) Validate(*model.ModelIR) []model.ValidationIssue {
	return []model.ValidationIssue{model.NewIssue(softwaredelivery.DomainMismatch, "/domain", "this pack warns about every model", model.SeverityWarning)}
}

// D-078 (a), review L1: warnings do not refuse a scenario, neither the
// domain pack's nor the patched model's own (a step no source reaches is a
// warning, TWIN_IR_SPEC §4); only errors do.
func TestCheckAcceptsWarnings(t *testing.T) {
	b := base(t)
	if _, err := check(b, []byte(raiseCI), lookup{warningPack{}}); err != nil {
		t.Errorf("a pack that only warns: %v", err)
	}
	// All of the CI result to the test step: the failure and rework steps
	// are no longer reached, a warning.
	unreached := `[{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/routing/probability","value":1},` +
		`{"op":"test","path":"/flows/4/id","value":"f_retry"},{"op":"replace","path":"/flows/4/routing/probability","value":0}]`
	res, err := patch.Apply(b, []byte(unreached), patch.Scenario)
	if err != nil || !res.Valid() {
		t.Fatalf("the patch: %v %+v", err, res.Validation.Issues)
	}
	if !slices.ContainsFunc(res.Validation.Issues, func(is model.ValidationIssue) bool {
		return is.Code == validate.UnreachableNode && is.Severity == model.SeverityWarning
	}) {
		t.Fatalf("the patched model has no UNREACHABLE_NODE warning: %+v", res.Validation.Issues)
	}
	if _, err := check(b, []byte(unreached), packs(t)); err != nil {
		t.Errorf("a patched model with a warning: %v", err)
	}
	// An error among the warnings still refuses (refusingPack, scenarios_test.go).
	var ie *InvalidError
	if _, err := check(b, []byte(raiseCI), lookup{refusingPack{}}); !errors.As(err, &ie) {
		t.Errorf("a pack error: %v", err)
	}
}

// Review L2 (T-028), T-054 review round 1 (L1, L2): createManualScenario's
// answer to each kind of binding-stage error, as Create returns it
// (datasets.Failure): a snapshot that could not be read while the request
// was alive is 503 DATASET_STORAGE_FAILED; any other error of the stage is
// a server fault, 500 INTERNAL_ERROR logged at ERROR by httpapi; a request
// that ended (cancelled, or past its deadline), or a read that says it
// did, is the request's context error, which httpapi answers as a request
// that did not complete (503, no details), whatever the store said. The
// shared rule is internal/storage/datasets' TestFailure.
func TestBindingFailure(t *testing.T) {
	read := &datasets.ReadError{DatasetVersionID: "dsv_01M4YDQK00NMKD3J34KSJZVY40", Err: errors.New("the object store did not answer")}
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
		{"a live request, a cancelled read", live, &datasets.ReadError{Err: fmt.Errorf("get: %w", context.Canceled)}, 503, "SERVICE_UNAVAILABLE", "", false},
		{"a live request, a read past its deadline", live, &datasets.ReadError{Err: fmt.Errorf("get: %w", context.DeadlineExceeded)}, 503, "SERVICE_UNAVAILABLE", "", false},
		{"a cancelled request, the store's own error", cancelled, read, 503, "SERVICE_UNAVAILABLE", "", false},
		{"a cancelled request, a server fault", cancelled, fault, 503, "SERVICE_UNAVAILABLE", "", false},
		{"a request past its deadline, the store's own error", expired, read, 503, "SERVICE_UNAVAILABLE", "", false},
	} {
		var logs bytes.Buffer
		w := httptest.NewRecorder()
		httpapi.WriteError(w, httptest.NewRequest("POST", "/v1/model-versions/x/scenarios", nil), slog.New(slog.NewJSONHandler(&logs, nil)),
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
		if strings.Contains(w.Body.String(), "did not answer") || strings.Contains(w.Body.String(), "hashing") {
			t.Errorf("%s: the cause is sent: %s", c.name, w.Body)
		}
		if got := strings.Contains(logs.String(), `"level":"ERROR"`); got != c.errorLog {
			t.Errorf("%s: ERROR log %v, want %v: %s", c.name, got, c.errorLog, logs.String())
		}
	}
}

func issues(n int, severity model.Severity) []model.ValidationIssue {
	out := make([]model.ValidationIssue, n)
	for i := range out {
		out[i] = model.NewIssue(validate.RoutingInvalid, "/nodes/"+strconv.Itoa(i), "issue "+strconv.Itoa(i), severity)
	}
	return out
}

// Review I1: Error.details holds at most 1000 entries. A list that fits is
// kept as it is; a longer one keeps its first 999 after one LIMIT_EXCEEDED
// issue that counts the rest, an error when any of the rest is one.
func TestCapped(t *testing.T) {
	for _, n := range []int{0, 1, 999, maxIssues} {
		in := issues(n, model.SeverityError)
		if out := capped(in); !slices.Equal(out, in) {
			t.Errorf("%d issues: %d kept, first %+v", n, len(out), out[:min(1, len(out))])
		}
	}
	for _, c := range []struct {
		n             int
		lastIsError   bool
		left          int
		wantSeverity  model.Severity
		firstErrorsAt int
	}{
		{maxIssues + 1, false, 2, model.SeverityWarning, -1},
		{maxIssues + 1, true, 2, model.SeverityError, -1},
		{2500, false, 1501, model.SeverityWarning, -1},
	} {
		in := issues(c.n, model.SeverityWarning)
		if c.lastIsError {
			in[len(in)-1].Severity = model.SeverityError
		}
		out := capped(in)
		if len(out) != maxIssues {
			t.Errorf("%d issues: %d kept, want %d", c.n, len(out), maxIssues)
			continue
		}
		head := out[0]
		if head.Code != validate.LimitExceeded || head.Path != "" || head.Severity != c.wantSeverity ||
			head.Message != fmt.Sprintf("%d more issues are not listed", c.left) {
			t.Errorf("%d issues: the first is %+v", c.n, head)
		}
		if !slices.Equal(out[1:], in[:maxIssues-1]) {
			t.Errorf("%d issues: the kept ones are not the first %d", c.n, maxIssues-1)
		}
	}
}
