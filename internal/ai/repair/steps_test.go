package repair_test

// H-059 (slice E3): the proposal role graph runs the one repair as nodes of
// its own (repair_context, generate_2, review_2), so Once is split into its
// two steps around the second call, Plan and Settle, which the graph calls
// with the second call made through the metered Eino ChatModel. These tests
// pin the steps; every test of Once (repair_test.go) is unchanged.

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/repair"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/model"
)

// Plan asks for nothing when there is nothing to repair: an outcome that is
// not invalid, an invalid one with no error, or no answer handed back.
func TestPlanAsksForNothingToRepair(t *testing.T) {
	r := newRig(t)
	warning := model.NewIssue("UNREACHABLE_NODE", "/nodes/46", "no source reaches this node", model.SeverityWarning)
	for name, c := range map[string]struct {
		outcome review.Outcome
		prior   provider.PriorAnswer
	}{
		"ready":          {review.Outcome{Status: review.Ready}, r.prior},
		"needs_input":    {review.Outcome{Status: review.NeedsInput}, r.prior},
		"warnings only":  {review.Outcome{Status: review.Invalid, Validation: &review.Validation{Issues: []model.ValidationIssue{warning}}}, r.prior},
		"no prior":       {refusedBy(routing("/nodes/45")), provider.PriorAnswer{}},
		"invalid, empty": {review.Outcome{Status: review.Invalid}, r.prior},
	} {
		rc, rec, err := repair.Plan(context.Background(), r.pc, c.prior, c.outcome)
		if rec != nil || err != nil || rc.Prompt.Revision != nil {
			t.Errorf("%s: %+v %v", name, rec, err)
		}
	}
	if n := len(r.f.Requests()); n != 1 {
		t.Errorf("%d requests", n)
	}
}

// Plan's follow-up is Revise's, and its record lists the errors it sends
// and how many it left out; a cancelled attempt gets no follow-up.
func TestPlanIsTheFollowUp(t *testing.T) {
	r := newRig(t)
	var issues []model.ValidationIssue
	for i := range aicontext.MaxRepairIssues + 2 {
		issues = append(issues, routing("/nodes/"+strconv.Itoa(i)))
	}
	refused := refusedBy(issues...)
	rc, rec, err := repair.Plan(context.Background(), r.pc, r.prior, refused)
	if err != nil || rec == nil || rec.Issues != aicontext.MaxRepairIssues || rec.NotListed != 2 || rec.Call != nil || rec.Outcome != "" {
		t.Fatalf("%+v %v", rec, err)
	}
	listed, notListed := repair.Issues(refused)
	want, err := aicontext.Revise(r.pc, r.prior, listed, notListed)
	if err != nil || rc.Prompt.InputHash != want.Prompt.InputHash || rc.Prompt.Revision == nil {
		t.Errorf("the follow-up is not Revise's: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, rec, err = repair.Plan(ctx, r.pc, r.prior, refused); rec != nil || provider.CodeOf(err) != provider.CodeCancelled || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %+v %v", rec, err)
	}
	// A first request that cannot be revised: the follow-up's own error.
	bad := r.pc
	bad.Prompt = provider.Prompt{}
	if _, rec, err = repair.Plan(context.Background(), bad, r.prior, refused); rec != nil || aicontext.CodeOf(err) == "" {
		t.Errorf("unrevisable: %+v %v", rec, err)
	}
}

// Settle is what came of the second call, as Once decides it: the review of
// an answer; the client's refusal of an answer it handed back; a failed
// call leaves the first outcome; a cancelled one is the attempt's error.
func TestSettleIsWhatCameOfTheSecondCall(t *testing.T) {
	r := newRig(t)
	refused := refusedBy(routing("/nodes/45"))
	rec := func() *repair.Record { return &repair.Record{Issues: 1} }
	ready := review.Outcome{Status: review.Ready, PatchHash: "sha256:x"}
	out, got, err := repair.Settle(context.Background(), rec(), refused, provider.ProposalResult{Output: provider.ProposalOutput{Summary: "s"}}, nil, r.check(ready))
	if err != nil || out.Status != review.Ready || got.Outcome != repair.Ready || len(r.checks) != 1 || r.checks[0].Summary != "s" {
		t.Errorf("answered: %+v %+v %v", out, got, err)
	}
	// The client refused an answer it handed back: its issues.
	r.checks = nil
	invalid := provider.NewError(provider.CodeInvalidOutput, 0, "")
	invalid.Issues = []model.ValidationIssue{routing("/nodes/46")}
	out, got, err = repair.Settle(context.Background(), rec(), refused, provider.ProposalResult{Prior: r.prior}, invalid, r.check(ready))
	if err != nil || out.Status != review.Invalid || got.Outcome != repair.Invalid || len(r.checks) != 0 {
		t.Errorf("refused by the client: %+v %+v %v", out, got, err)
	}
	// Refused with no answer handed back, or failed otherwise: the first
	// outcome, failed with the code.
	for name, c := range map[string]struct {
		err  error
		code string
	}{
		"invalid_output, no answer": {provider.NewError(provider.CodeInvalidOutput, 0, ""), "AI_INVALID_OUTPUT"},
		"rate limited":              {provider.NewError(provider.CodeRateLimited, 429, "x"), "AI_RATE_LIMITED"},
		"not the client's":          {errors.New("boom"), "INTERNAL"},
	} {
		out, got, err = repair.Settle(context.Background(), rec(), refused, provider.ProposalResult{}, c.err, r.check(ready))
		if err != nil || out.Status != review.Invalid || out.Validation.Issues[0].Path != "/nodes/45" || got.Outcome != repair.Failed || got.Code != c.code {
			t.Errorf("%s: %+v %+v %v", name, out, got, err)
		}
	}
	// The server's own fault in the check: its error, the first outcome.
	fault := errors.New("the review could not run")
	out, got, err = repair.Settle(context.Background(), rec(), refused, provider.ProposalResult{}, nil,
		func(provider.ProposalOutput) (review.Outcome, error) { return review.Outcome{}, fault })
	if !errors.Is(err, fault) || out.Status != review.Invalid || got.Outcome != "" {
		t.Errorf("a check that failed: %+v %+v %v", out, got, err)
	}
	// Cancelled during the call: the attempt's error, nothing decided.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stopped := provider.NewError(provider.CodeCancelled, 0, "").WithCause(context.Canceled)
	out, got, err = repair.Settle(ctx, rec(), refused, provider.ProposalResult{}, stopped, r.check(ready))
	if !errors.Is(err, context.Canceled) || out.Status != review.Invalid || got.Outcome != "" || got.Code != "" {
		t.Errorf("cancelled: %+v %+v %v", out, got, err)
	}
}
