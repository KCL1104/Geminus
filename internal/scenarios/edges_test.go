package scenarios

// Edge cases found by T-028 review round 1: warnings never refuse a
// scenario (L1), a request that ended keeps its context error (L2), and
// the issue cap of Error.details (I1). Pure: no database.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
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

// Review L2: when the request itself ended (cancelled, or past its
// deadline), the dataset binding stage's failure is the request's context
// error, which httpapi answers as a request that did not complete, never
// 503 DATASET_STORAGE_FAILED; a store that fails while the request is alive
// is DatasetStorageError. The pattern of the approval's TestBindingFailure.
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
