package repair_test

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ai/repair"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/model"
)

const (
	org    = "org_01M4YDQK00TA00000000000000"
	intent = "Add a second ICT tester."
	// first and second stand for the model's two answers; their texts are
	// sentinels that must never reach the follow-up's rules or blocks.
	first  = `{"status":"proposed","summary":"FIRST-SENTINEL","assumptions":[],"risk_level":"low","confidence":0.9,"changes":[{"op":"test","path":"/name","value":"x"}],"expected_effects":["e"],"requires_confirmation":true}`
	second = `{"status":"needs_input","summary":"SECOND-SENTINEL","assumptions":[],"questions":["Which station?"],"requires_confirmation":true}`
)

func firstContext(t *testing.T) provider.ProposalContext {
	t.Helper()
	b, _ := jsontext.AppendQuote(nil, intent)
	p, err := provider.Seal(provider.Prompt{OrganizationID: org, Role: provider.RoleModeler, TemplateVersion: "modeler-test", Locale: locale.English,
		System: "rules", Blocks: []provider.Block{{Kind: provider.UntrustedData, Name: fake.IntentBlock, Content: b}}})
	if err != nil {
		t.Fatal(err)
	}
	return provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: p}
}

// refusedBy is the invalid outcome of issues, as the review leaves it.
func refusedBy(issues ...model.ValidationIssue) review.Outcome { return review.InvalidOutput(issues) }

func routing(path string) model.ValidationIssue {
	return model.NewIssue("ROUTING_INVALID", path, "the outgoing routing probabilities are not valid", model.SeverityError)
}

type rig struct {
	f      *fake.Backend
	c      *provider.Client
	pc     provider.ProposalContext
	prior  provider.PriorAnswer
	checks []provider.ProposalOutput
	calls  []error
}

func newRig(t *testing.T) *rig {
	t.Helper()
	f := fake.New()
	f.Script(provider.RoleModeler, intent, []byte(first))
	c, err := provider.New(f, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{f: f, c: c, pc: firstContext(t)}
	res, err := c.GenerateModelProposal(context.Background(), r.pc)
	if err != nil || !res.Prior.Present() {
		t.Fatalf("the first call: %v", err)
	}
	r.prior = res.Prior
	return r
}

// check stands for the server's review of the second answer: it records
// what it was given and makes it outcome.
func (r *rig) check(outcome review.Outcome) repair.Check {
	return func(out provider.ProposalOutput) (review.Outcome, error) {
		r.checks = append(r.checks, out)
		return outcome, nil
	}
}

func (r *rig) once(ctx context.Context, refused review.Outcome, check repair.Check) (review.Outcome, *repair.Record, error) {
	return repair.Once(ctx, r.c, r.pc, r.prior, refused, check, func(m provider.Metadata, err error) { r.calls = append(r.calls, err) })
}

// H-057: a refused answer gets one repair: one more call, with the
// revision, whose answer the server's check makes the outcome; the record
// says what was listed and what came of it.
func TestARefusedAnswerIsRepairedOnce(t *testing.T) {
	r := newRig(t)
	r.f.ScriptRevision(provider.RoleModeler, intent, []byte(second))
	ready := review.Outcome{Status: review.NeedsInput, Summary: "s", Questions: []string{"q"}}
	out, rec, err := r.once(context.Background(), refusedBy(routing("/nodes/45"), routing("/nodes/46")), r.check(ready))
	if err != nil || out.Status != review.NeedsInput {
		t.Fatalf("%+v %v", out, err)
	}
	if rec == nil || rec.Issues != 2 || rec.NotListed != 0 || rec.Outcome != repair.NeedsInput || rec.Code != "" || rec.Call == nil ||
		rec.Call.TemplateVersion != "modeler-test+repair-0.1.0" || !rec.Revised() {
		t.Errorf("record %+v", rec)
	}
	reqs := r.f.Requests()
	if len(reqs) != 2 || reqs[1].Prompt.Revision == nil || len(r.calls) != 1 || r.calls[0] != nil || len(r.checks) != 1 || r.checks[0].Summary != "SECOND-SENTINEL" {
		t.Fatalf("%d requests, %d calls, %d checks", len(reqs), len(r.calls), len(r.checks))
	}
	follow := reqs[1].Prompt.Revision.User()
	if !strings.Contains(follow, `{"code":"ROUTING_INVALID","message":"the outgoing routing probabilities are not valid","path":"/nodes/45"}`) ||
		strings.Contains(follow, "SENTINEL") {
		t.Errorf("the follow-up: %s", follow)
	}
	// Both answers refused: the second's issues are the outcome.
	r = newRig(t)
	again := refusedBy(model.NewIssue("UNREACHABLE_NODE", "/nodes/46", "no source reaches this node", model.SeverityError))
	out, rec, err = r.once(context.Background(), refusedBy(routing("/nodes/45")), r.check(again))
	if err != nil || out.Status != review.Invalid || out.Validation.Issues[0].Code != "UNREACHABLE_NODE" || rec.Outcome != repair.Invalid || !rec.Revised() {
		t.Errorf("refused twice: %+v %+v %v", out.Validation, rec, err)
	}
}

// The second answer refused by the client (invalid_output) is the outcome,
// its issues cleaned; a second call that fails otherwise leaves the first
// outcome, and the record says the repair failed and why, with the call's
// metadata (what it cost).
func TestAFailedRepairKeepsTheFirstOutcome(t *testing.T) {
	refused := refusedBy(routing("/nodes/45"))
	r := newRig(t)
	r.f.ScriptRevision(provider.RoleModeler, intent, []byte(`{"status":"proposed"}`))
	out, rec, err := r.once(context.Background(), refused, r.check(review.Outcome{}))
	if err != nil || out.Status != review.Invalid || rec.Outcome != repair.Invalid || len(r.checks) != 0 ||
		out.Validation.Issues[0].Code != review.OutputInvalid {
		t.Errorf("a schema-invalid repair: %+v %+v %v", out.Validation, rec, err)
	}
	for _, c := range []provider.Code{provider.CodeTimeout, provider.CodeTruncated, provider.CodeRefused, provider.CodeUnavailable} {
		r := newRig(t)
		r.f.FailRevision(provider.RoleModeler, intent, provider.NewError(c, 0, ""))
		out, rec, err := r.once(context.Background(), refused, r.check(review.Outcome{}))
		if err != nil || out.Status != review.Invalid || out.Validation.Issues[0].Path != "/nodes/45" || rec == nil || rec.Outcome != repair.Failed ||
			rec.Code != "AI_"+strings.ToUpper(string(c)) || rec.Call == nil || rec.Revised() || len(r.checks) != 0 {
			t.Errorf("%s: %+v %+v %v", c, out.Validation, rec, err)
		}
	}
	// A check that fails is the server's own fault: its error.
	r = newRig(t)
	bad := errors.New("the server could not review")
	if _, rec, err := r.once(context.Background(), refused, func(provider.ProposalOutput) (review.Outcome, error) { return review.Outcome{}, bad }); !errors.Is(err, bad) || rec == nil {
		t.Errorf("a failed check: %v", err)
	}
}

// No repair without a refusal it can follow: an outcome that is not invalid,
// no answer to send back; and none once the attempt is stopping.
func TestNoRepair(t *testing.T) {
	r := newRig(t)
	for name, o := range map[string]review.Outcome{
		"ready":       {Status: review.Ready, Validation: &review.Validation{Valid: true}},
		"needs_input": {Status: review.NeedsInput},
		"only warnings": {Status: review.Invalid, Validation: &review.Validation{Issues: []model.ValidationIssue{
			model.NewIssue("TEST_RESULT_NOT_IDENTIFIED", "/nodes/1", "x", model.SeverityWarning)}}},
	} {
		out, rec, err := r.once(context.Background(), o, r.check(review.Outcome{}))
		if err != nil || rec != nil || out.Status != o.Status {
			t.Errorf("%s: %+v %v", name, rec, err)
		}
	}
	if out, rec, err := repair.Once(context.Background(), r.c, r.pc, provider.PriorAnswer{}, refusedBy(routing("/nodes/1")), r.check(review.Outcome{}), nil); err != nil ||
		rec != nil || out.Status != review.Invalid {
		t.Errorf("no prior answer: %+v %v", rec, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, rec, err := r.once(ctx, refusedBy(routing("/nodes/1")), r.check(review.Outcome{})); provider.CodeOf(err) != provider.CodeCancelled ||
		!errors.Is(err, context.Canceled) || rec != nil {
		t.Errorf("cancelled: %+v %v", rec, err)
	}
	if n := len(r.f.Requests()); n != 1 {
		t.Errorf("%d requests, want the first only", n)
	}
}

// A cancellation during the second call is the attempt's: its error, with
// the record of the call (what it spent).
func TestACancelledRepair(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	r.f.FailRevision(provider.RoleModeler, intent, provider.NewError(provider.CodeUnavailable, 0, ""))
	c := cancelling{LLMProvider: r.c, cancel: cancel}
	_, rec, err := repair.Once(ctx, c, r.pc, r.prior, refusedBy(routing("/nodes/1")), r.check(review.Outcome{}), nil)
	if !errors.Is(err, context.Canceled) && provider.CodeOf(err) != provider.CodeUnavailable {
		t.Errorf("%v", err)
	}
	if rec == nil || rec.Call == nil || rec.Outcome != "" {
		t.Errorf("record %+v", rec)
	}
}

// cancelling cancels the attempt while its call is in flight.
type cancelling struct {
	provider.LLMProvider
	cancel func()
}

func (c cancelling) GenerateModelProposal(ctx context.Context, pc provider.ProposalContext) (provider.ProposalResult, error) {
	res, err := c.LLMProvider.GenerateModelProposal(ctx, pc)
	c.cancel()
	return res, err
}

// The follow-up lists the refusal's errors only, at most
// aicontext.MaxRepairIssues of them, and counts the rest.
func TestTheIssuesARepairLists(t *testing.T) {
	var all []model.ValidationIssue
	for range aicontext.MaxRepairIssues + 7 {
		all = append(all, routing("/nodes/1"))
	}
	all = append(all, model.NewIssue("TEST_RESULT_NOT_IDENTIFIED", "/nodes/2", "x", model.SeverityWarning))
	issues, more := repair.Issues(review.Outcome{Status: review.Invalid, Validation: &review.Validation{Issues: all}})
	if len(issues) != aicontext.MaxRepairIssues || more != 7 {
		t.Errorf("%d listed, %d not", len(issues), more)
	}
	for _, is := range issues {
		if is.Severity != model.SeverityError {
			t.Errorf("a warning is listed: %+v", is)
		}
	}
	if issues, more := repair.Issues(review.Outcome{Status: review.Ready, Validation: &review.Validation{Valid: true, Issues: all}}); issues != nil || more != 0 {
		t.Error("a ready outcome lists issues")
	}
}
