package repair_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/repair"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/model"
)

// onceBefore is Once before slice E5 (repair.go at 31ab222), verbatim but
// for the package's names: the reference TestOnceIsPrepareCallAndSettle
// holds both Once and its steps to, so a change to the repair's rules in
// either fails it (E5 review round 1). It is no production path.
func onceBefore(ctx context.Context, client provider.LLMProvider, first provider.ProposalContext, prior provider.PriorAnswer, refused review.Outcome,
	check repair.Check, answered func(provider.Metadata, error)) (review.Outcome, *repair.Record, error) {
	issues, notListed := repair.Issues(refused)
	if len(issues) == 0 || !prior.Present() {
		return refused, nil, nil
	}
	rc, err := aicontext.Revise(first, prior, issues, notListed)
	if err != nil {
		return refused, nil, err
	}
	if err := ctx.Err(); err != nil {
		// Stopping between the two calls: no second call.
		return refused, nil, provider.NewError(provider.CodeCancelled, 0, "").WithCause(err)
	}
	rec := &repair.Record{Issues: len(issues), NotListed: notListed}
	res, err := client.GenerateModelProposal(ctx, rc)
	meta := res.Metadata
	rec.Call = &meta
	if answered != nil {
		answered(meta, err)
	}
	if err != nil {
		if ctx.Err() != nil {
			return refused, rec, err
		}
		var pe *provider.Error
		if errors.As(err, &pe) && pe.Code == provider.CodeInvalidOutput && res.Prior.Present() {
			// The second answer refused for what it says (the client handed
			// it back, as it does only then): its own issues, cleaned like
			// every other, and nothing of it.
			rec.Outcome = repair.Invalid
			return review.InvalidOutput(pe.Issues), rec, nil
		}
		// Every other failure, and invalid_output with no answer handed back
		// (no text block, a stop reason this build does not know, another
		// model's answer, an empty or oversized one, a web search it was not
		// offered: H-057 review round 1): no second answer the server
		// reviewed, as the same refusal of a first call is not repaired. The
		// first outcome stays; AI_INVALID_OUTPUT here is what shows an
		// endpoint that does not take the repair's turns.
		rec.Outcome, rec.Code = repair.Failed, codeOfBefore(err)
		return refused, rec, nil
	}
	outcome, err := check(res.Output)
	if err != nil {
		return refused, rec, err
	}
	rec.Outcome = string(outcome.Status)
	return outcome, rec, nil
}

// codeOfBefore is codeOf at 31ab222.
func codeOfBefore(err error) string {
	if c := provider.CodeOf(err); c != "" {
		return "AI_" + strings.ToUpper(string(c))
	}
	return "INTERNAL"
}

// Slice E5 (H-059): a role graph makes the repair's second call through its
// own model node (the photo graph's metered Eino ChatModel), so the repair
// is Prepare, the client's call and Settle, and Once is their composition.
// For every case, both Once and the three steps over the same client give
// exactly what Once gave before E5 (onceBefore): the outcome, the record,
// the error, the observed calls, the checks and the requests.
func TestOnceIsPrepareCallAndSettle(t *testing.T) {
	refused := refusedBy(routing("/nodes/45"))
	ready := review.Outcome{Status: review.Ready, Summary: "fixed"}
	// The client's refusal of the second answer with no answer handed back
	// (H-057 review round 1), with an issue of its own that must never be
	// the outcome.
	noAnswer := func() error {
		e := provider.NewError(provider.CodeInvalidOutput, 0, "")
		e.Issues = []model.ValidationIssue{model.NewIssue(provider.OutputInvalid, "", "the answer is not from the Standard-mode model the request named", model.SeverityError)}
		return e
	}
	cases := []struct {
		name  string
		setup func(r *rig)
		// cancelled: the attempt is stopping before the repair; during: it
		// stops while the second call is in flight.
		cancelled, during bool
		refused           review.Outcome
		noPrior           bool
		check             func(r *rig) repair.Check
	}{
		{name: "repaired", setup: func(r *rig) { r.f.ScriptRevision(provider.RoleModeler, intent, []byte(second)) },
			check: func(r *rig) repair.Check { return r.check(ready) }},
		{name: "repaired and refused again", check: func(r *rig) repair.Check { return r.check(refusedBy(routing("/nodes/46"))) }},
		{name: "the second call fails", setup: func(r *rig) {
			r.f.FailRevision(provider.RoleModeler, intent, provider.NewError(provider.CodeRateLimited, 429, "meta"))
		}, check: func(r *rig) repair.Check { return r.check(ready) }},
		{name: "the second answer refused for what it says", setup: func(r *rig) {
			r.f.ScriptRevision(provider.RoleModeler, intent, []byte(`{"status":"proposed","summary":"SECOND-SENTINEL"}`))
		}, check: func(r *rig) repair.Check { return r.check(ready) }},
		{name: "the second call refused with no answer handed back", setup: func(r *rig) {
			r.f.FailRevision(provider.RoleModeler, intent, noAnswer())
		}, check: func(r *rig) repair.Check { return r.check(ready) }},
		{name: "the check fails", check: func(r *rig) repair.Check {
			return func(provider.ProposalOutput) (review.Outcome, error) {
				return review.Outcome{}, errors.New("the server's own fault")
			}
		}},
		{name: "nothing to repair", refused: ready, check: func(r *rig) repair.Check { return r.check(ready) }},
		{name: "only warnings", refused: review.Outcome{Status: review.Invalid, Validation: &review.Validation{Issues: []model.ValidationIssue{
			model.NewIssue("TEST_RESULT_NOT_IDENTIFIED", "/nodes/1", "x", model.SeverityWarning)}}},
			check: func(r *rig) repair.Check { return r.check(ready) }},
		{name: "no answer to send back", noPrior: true, check: func(r *rig) repair.Check { return r.check(ready) }},
		{name: "the follow-up cannot be built", setup: func(r *rig) { r.pc.Prompt.System += " tampered" },
			check: func(r *rig) repair.Check { return r.check(ready) }},
		{name: "stopping before the second call", cancelled: true, check: func(r *rig) repair.Check { return r.check(ready) }},
		{name: "stopping during the second call", during: true, setup: func(r *rig) {
			r.f.FailRevision(provider.RoleModeler, intent, provider.NewError(provider.CodeUnavailable, 0, ""))
		}, check: func(r *rig) repair.Check { return r.check(ready) }},
		{name: "stopping during a second call that answered", during: true, setup: func(r *rig) {
			r.f.ScriptRevision(provider.RoleModeler, intent, []byte(second))
		}, check: func(r *rig) repair.Check { return r.check(ready) }},
	}
	type ran struct {
		out review.Outcome
		rec *repair.Record
		err error
		r   *rig
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := refused
			if c.refused.Status != "" {
				out = c.refused
			}
			// how: "before" (onceBefore), "once" (repair.Once) or "steps"
			// (Prepare, the client's call and Settle), each on a fresh rig.
			run := func(how string) ran {
				r := newRig(t)
				if c.setup != nil {
					c.setup(r)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if c.cancelled {
					cancel()
				}
				var client provider.LLMProvider = r.c
				if c.during {
					client = cancelling{LLMProvider: r.c, cancel: cancel}
				}
				prior := r.prior
				if c.noPrior {
					prior = provider.PriorAnswer{}
				}
				answered := func(m provider.Metadata, err error) { r.calls = append(r.calls, err) }
				switch how {
				case "before":
					o, rec, err := onceBefore(ctx, client, r.pc, prior, out, c.check(r), answered)
					return ran{o, rec, err, r}
				case "once":
					o, rec, err := repair.Once(ctx, client, r.pc, prior, out, c.check(r), answered)
					return ran{o, rec, err, r}
				}
				rc, rec, err := repair.Prepare(ctx, r.pc, prior, out)
				if rec == nil {
					if rc.Prompt.Revision != nil {
						t.Error("Prepare built a follow-up and no record")
					}
					return ran{out, nil, err, r}
				}
				if err != nil {
					t.Fatalf("Prepare: a record and an error %v", err)
				}
				res, err := client.GenerateModelProposal(ctx, rc)
				o, rec, err := repair.Settle(ctx, rec, out, res, err, c.check(r), answered)
				return ran{o, rec, err, r}
			}
			want := run("before")
			for _, how := range []string{"once", "steps"} {
				got := run(how)
				if !reflect.DeepEqual(got.out, want.out) {
					t.Errorf("%s: outcome %+v, before %+v", how, got.out, want.out)
				}
				if (want.rec == nil) != (got.rec == nil) {
					t.Fatalf("%s: record %+v, before %+v", how, got.rec, want.rec)
				}
				if want.rec != nil {
					w, g := *want.rec, *got.rec
					if (w.Call == nil) != (g.Call == nil) {
						t.Fatalf("%s: record call %+v, before %+v", how, g.Call, w.Call)
					}
					if w.Call != nil {
						wc, gc := *w.Call, *g.Call
						wc.LatencyMillis, gc.LatencyMillis = 0, 0
						w.Call, g.Call = &wc, &gc
					}
					if !reflect.DeepEqual(g, w) {
						t.Errorf("%s: record %+v, before %+v", how, g, w)
					}
				}
				gerr, werr := got.err, want.err
				var gae, wae *aicontext.Error
				if provider.CodeOf(gerr) != provider.CodeOf(werr) || (gerr == nil) != (werr == nil) || (gerr != nil && gerr.Error() != werr.Error()) ||
					errors.Is(gerr, context.Canceled) != errors.Is(werr, context.Canceled) || errors.As(gerr, &gae) != errors.As(werr, &wae) {
					t.Errorf("%s: error %v, before %v", how, gerr, werr)
				}
				gr, wr := got.r, want.r
				if len(gr.calls) != len(wr.calls) || len(gr.checks) != len(wr.checks) || !reflect.DeepEqual(gr.checks, wr.checks) {
					t.Errorf("%s: calls %v checks %d, before %v %d", how, gr.calls, len(gr.checks), wr.calls, len(wr.checks))
				}
				for i := range min(len(wr.calls), len(gr.calls)) {
					if provider.CodeOf(gr.calls[i]) != provider.CodeOf(wr.calls[i]) {
						t.Errorf("%s: call %d: %v, before %v", how, i, gr.calls[i], wr.calls[i])
					}
				}
				wq, gq := wr.f.Requests(), gr.f.Requests()
				if len(wq) != len(gq) {
					t.Fatalf("%s: %d requests, before %d", how, len(gq), len(wq))
				}
				for i := range wq {
					if wq[i].Prompt.InputHash == "" || !reflect.DeepEqual(gq[i], wq[i]) {
						t.Errorf("%s: request %d differs from before's", how, i)
					}
				}
			}
		})
	}
}
