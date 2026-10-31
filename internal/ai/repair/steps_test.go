package repair_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/repair"
	"github.com/kcl1104/geminus/internal/ai/review"
)

// Slice E5 (H-059): a role graph makes the repair's second call through its
// own model node (the photo graph's metered Eino ChatModel), so Once is
// Prepare, the client's call and Settle, and nothing else. For every case
// Once has, the three steps over the same client give exactly Once's
// outcome, record, error, observed calls, checks and requests.
func TestOnceIsPrepareCallAndSettle(t *testing.T) {
	refused := refusedBy(routing("/nodes/45"))
	ready := review.Outcome{Status: review.Ready, Summary: "fixed"}
	cases := []struct {
		name    string
		setup   func(r *rig)
		ctx     func() context.Context
		refused review.Outcome
		noPrior bool
		check   func(r *rig) repair.Check
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
		{name: "the check fails", check: func(r *rig) repair.Check {
			return func(provider.ProposalOutput) (review.Outcome, error) {
				return review.Outcome{}, errors.New("the server's own fault")
			}
		}},
		{name: "nothing to repair", refused: ready, check: func(r *rig) repair.Check { return r.check(ready) }},
		{name: "no answer to send back", noPrior: true, check: func(r *rig) repair.Check { return r.check(ready) }},
		{name: "stopping before the second call", ctx: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, check: func(r *rig) repair.Check { return r.check(ready) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			if c.ctx != nil {
				ctx = c.ctx()
			}
			out := refused
			if c.refused.Status != "" {
				out = c.refused
			}
			run := func(steps bool) (review.Outcome, *repair.Record, error, *rig) {
				r := newRig(t)
				if c.setup != nil {
					c.setup(r)
				}
				prior := r.prior
				if c.noPrior {
					prior = provider.PriorAnswer{}
				}
				answered := func(m provider.Metadata, err error) { r.calls = append(r.calls, err) }
				if !steps {
					o, rec, err := repair.Once(ctx, r.c, r.pc, prior, out, c.check(r), answered)
					return o, rec, err, r
				}
				rc, rec, err := repair.Prepare(ctx, r.pc, prior, out)
				if rec == nil {
					if rc.Prompt.Revision != nil {
						t.Error("Prepare built a follow-up and no record")
					}
					return out, nil, err, r
				}
				if err != nil {
					t.Fatalf("Prepare: a record and an error %v", err)
				}
				res, err := r.c.GenerateModelProposal(ctx, rc)
				o, rec, err := repair.Settle(ctx, rec, out, res, err, c.check(r), answered)
				return o, rec, err, r
			}
			wo, wrec, werr, wr := run(false)
			go_, grec, gerr, gr := run(true)
			if !reflect.DeepEqual(go_, wo) {
				t.Errorf("outcome %+v, Once's %+v", go_, wo)
			}
			if (wrec == nil) != (grec == nil) {
				t.Fatalf("record %+v, Once's %+v", grec, wrec)
			}
			if wrec != nil {
				w, g := *wrec, *grec
				if (w.Call == nil) != (g.Call == nil) {
					t.Fatalf("record call %+v, Once's %+v", g.Call, w.Call)
				}
				if w.Call != nil {
					wc, gc := *w.Call, *g.Call
					wc.LatencyMillis, gc.LatencyMillis = 0, 0
					w.Call, g.Call = &wc, &gc
				}
				if !reflect.DeepEqual(g, w) {
					t.Errorf("record %+v, Once's %+v", g, w)
				}
			}
			if provider.CodeOf(gerr) != provider.CodeOf(werr) || (gerr == nil) != (werr == nil) || (gerr != nil && gerr.Error() != werr.Error()) ||
				errors.Is(gerr, context.Canceled) != errors.Is(werr, context.Canceled) {
				t.Errorf("error %v, Once's %v", gerr, werr)
			}
			if len(gr.calls) != len(wr.calls) || len(gr.checks) != len(wr.checks) || !reflect.DeepEqual(gr.checks, wr.checks) {
				t.Errorf("calls %v checks %d, Once's %v %d", gr.calls, len(gr.checks), wr.calls, len(wr.checks))
			}
			for i := range wr.calls {
				if provider.CodeOf(gr.calls[i]) != provider.CodeOf(wr.calls[i]) {
					t.Errorf("call %d: %v, Once's %v", i, gr.calls[i], wr.calls[i])
				}
			}
			wq, gq := wr.f.Requests(), gr.f.Requests()
			if len(wq) != len(gq) {
				t.Fatalf("%d requests, Once's %d", len(gq), len(wq))
			}
			for i := range wq {
				if wq[i].Prompt.InputHash == "" || !reflect.DeepEqual(gq[i], wq[i]) {
					t.Errorf("request %d differs from Once's", i)
				}
			}
		})
	}
}
