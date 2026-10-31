package generator

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/repair"
	"github.com/kcl1104/geminus/internal/ai/review"
	proposalgraph "github.com/kcl1104/geminus/internal/aigraph/proposal"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/storage/jobs"
)

// H-059 (slice E3): what ends an attempt on the proposal graph is the job
// failure the handler's own pipeline returned before the graph, path by path
// (the old generateAsked): the code, the message and whether it is retried;
// what the attempt keeps for its audit (nothing before the call, the call's
// metadata after a failed first call or a first answer that could not be
// checked, everything once the repair was asked for); and the log line. A
// stopping attempt keeps nothing and returns the step's own error, but for a
// review that could not run and what the server stored, which are failures
// whatever the context. Several of these paths (a review or a binding stage
// that cannot run, a follow-up that cannot be built) are the server's own
// faults the context builder keeps unreachable, so they are pinned here.
func TestWhatEndsAnAttempt(t *testing.T) {
	meta := &provider.Metadata{Provider: "fake", Role: provider.RoleModeler, InputHash: "sha256:x"}
	first := review.InvalidOutput([]model.ValidationIssue{model.NewIssue("ROUTING_INVALID", "/nodes/3", "x", model.SeverityError)})
	manifest := []model.DatasetBinding{{LogicalRef: "r"}}
	rec := &repair.Record{Issues: 1, Call: &provider.Metadata{Provider: "fake"}}
	asked := proposalgraph.Result{Outcome: first, Call: meta, Manifest: manifest, Repair: rec}
	called := proposalgraph.Result{Call: meta, Manifest: manifest}
	unrepaired := proposalgraph.Result{Outcome: first, Call: meta, Manifest: manifest}
	rateLimited := provider.NewError(provider.CodeRateLimited, 429, "org-a")
	stopped := provider.NewError(provider.CodeCancelled, 0, "").WithCause(context.Canceled)
	own := &jobs.Failure{Code: DatasetStorageFailed, Message: "the dataset snapshots could not be read", Retryable: true}
	plain := errors.New("plain")
	ctxErr := func(code aicontext.Code, repair bool) error {
		return &proposalgraph.ContextError{Code: code, Repair: repair, Err: &aicontext.Error{Code: code, Path: "/x"}}
	}
	const (
		nothing = "nothing"
		call    = "the call"
		all     = "everything"
	)
	for _, c := range []struct {
		name      string
		res       proposalgraph.Result
		err       error
		stopping  bool
		code      string // "" for an error returned as it is (want)
		message   string
		retryable bool
		want      error
		keep      string
		log       string
	}{
		{name: "the base", err: &proposalgraph.InputError{Input: proposalgraph.InputBase}, code: InputInvalid, message: "the base version does not decode", keep: nothing},
		{name: "the domain", err: &proposalgraph.InputError{Input: proposalgraph.InputDomain}, code: InputInvalid, message: "the model's domain is not served by this worker", keep: nothing},
		{name: "the pool", err: &proposalgraph.InputError{Input: proposalgraph.InputPool}, code: InputInvalid, message: "the stored ID pool does not decode", keep: nothing},
		{name: "the pool, stopping", err: &proposalgraph.InputError{Input: proposalgraph.InputPool}, stopping: true, code: InputInvalid, message: "the stored ID pool does not decode", keep: nothing},
		{name: "a context too large", err: ctxErr(aicontext.TooLarge, false), code: "AI_CONTEXT_TOO_LARGE", message: providerMessages[provider.CodeContextTooLarge], keep: nothing},
		{name: "a context refused", err: ctxErr(aicontext.InvalidInput, false), code: InputInvalid, message: "the proposal's stored request cannot be built into a context",
			keep: nothing, log: `"msg":"building a proposal context failed","component":"worker","organization_id":"org_01M4YDQK00Y5H2929M9WWZWTVH","proposal_id":"aip_01M4YDQK00ADN261CME3WV5HZ6","code":`},
		{name: "a snapshot read failed", err: own, want: own, keep: nothing},
		{name: "a snapshot read failed, stopping", err: own, stopping: true, want: own, keep: nothing},
		{name: "a call failed", res: called, err: &proposalgraph.CallError{Err: rateLimited}, code: "AI_RATE_LIMITED", message: providerMessages[provider.CodeRateLimited], retryable: true, keep: call},
		{name: "a final call failed", res: called, err: &proposalgraph.CallError{Err: rateLimited, Final: true}, code: "AI_RATE_LIMITED", message: providerMessages[provider.CodeRateLimited], keep: call},
		{name: "a call failed, not the client's", res: called, err: &proposalgraph.CallError{Err: plain}, want: plain, keep: call},
		{name: "a call failed, stopping", res: called, err: &proposalgraph.CallError{Err: stopped}, stopping: true, want: stopped, keep: nothing},
		{name: "the first review", res: called, err: &proposalgraph.ReviewError{Err: plain}, code: InputInvalid, message: "the server could not review the answer against the stored base",
			keep: call, log: `"msg":"reviewing a proposal failed","component":"worker","organization_id":"org_01M4YDQK00Y5H2929M9WWZWTVH","proposal_id":"aip_01M4YDQK00ADN261CME3WV5HZ6","error":"plain"`},
		{name: "the repair's review", res: asked, err: &proposalgraph.ReviewError{Repair: true, Err: plain}, code: InputInvalid, message: "the server could not review the answer against the stored base",
			keep: all, log: `"msg":"reviewing a proposal failed"`},
		{name: "a review, stopping", res: asked, err: &proposalgraph.ReviewError{Repair: true, Err: plain}, stopping: true, code: InputInvalid,
			message: "the server could not review the answer against the stored base", keep: nothing, log: `"msg":"reviewing a proposal failed"`},
		{name: "the first binding stage", res: called, err: &proposalgraph.BindError{Err: plain}, code: DatasetStorageFailed, message: "the dataset binding stage failed after the provider answered",
			keep: call, log: `"msg":"the dataset binding stage failed after the provider answered","component":"worker","organization_id":"org_01M4YDQK00Y5H2929M9WWZWTVH","proposal_id":"aip_01M4YDQK00ADN261CME3WV5HZ6","error":"plain"`},
		{name: "the repair's binding stage", res: asked, err: &proposalgraph.BindError{Repair: true, Err: plain}, code: DatasetStorageFailed,
			message: "the dataset binding stage failed after the provider answered", keep: all, log: `"msg":"the dataset binding stage failed after the provider answered"`},
		{name: "a binding stage, stopping", res: called, err: &proposalgraph.BindError{Err: context.Canceled}, stopping: true, want: context.Canceled, keep: nothing},
		{name: "the repair's context", res: unrepaired, err: ctxErr(aicontext.InvalidInput, true), code: InputInvalid, message: "the server could not build the follow-up of the refused answer",
			keep: all, log: `"msg":"building a proposal's repair failed","component":"worker","organization_id":"org_01M4YDQK00Y5H2929M9WWZWTVH","proposal_id":"aip_01M4YDQK00ADN261CME3WV5HZ6","code":`},
		{name: "the repair's context, stopping", res: unrepaired, err: ctxErr(aicontext.InvalidInput, true), stopping: true, want: &aicontext.Error{Code: aicontext.InvalidInput, Path: "/x"}, keep: nothing},
		{name: "the repair stopped", res: asked, err: stopped, stopping: true, want: stopped, keep: nothing},
	} {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := &Handler{cfg: Config{Log: observability.NewLogger(&buf, "worker")}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if c.stopping {
				cancel()
			}
			full := generation{outcome: c.res.Outcome, meta: c.res.Call, manifest: c.res.Manifest, sources: c.res.Sources, repair: c.res.Repair}
			gen, err := h.ended(ctx, "org_01M4YDQK00Y5H2929M9WWZWTVH", "aip_01M4YDQK00ADN261CME3WV5HZ6", c.res, full, c.err)
			var f *jobs.Failure
			switch {
			case c.code != "":
				if !errors.As(err, &f) || f.Code != c.code || f.Message != c.message || f.Retryable != c.retryable {
					t.Errorf("failure %v, want %s %q retryable %v", err, c.code, c.message, c.retryable)
				}
			case errors.As(c.want, new(*aicontext.Error)):
				var ae *aicontext.Error
				if !errors.As(err, &ae) || ae.Code != aicontext.InvalidInput || errors.As(err, &f) {
					t.Errorf("error %v, want the follow-up's own", err)
				}
			case err != c.want:
				t.Errorf("error %v, want %v as it is", err, c.want)
			}
			switch c.keep {
			case nothing:
				if gen.meta != nil || gen.repair != nil || gen.outcome.Status != "" || gen.manifest != nil {
					t.Errorf("kept %+v", gen)
				}
			case call:
				if gen.meta != c.res.Call || gen.repair != nil || gen.outcome.Status != "" || gen.manifest != nil {
					t.Errorf("kept %+v, want the call only", gen)
				}
			case all:
				if gen.meta != c.res.Call || gen.repair != c.res.Repair || gen.outcome.Status != review.Invalid || len(gen.manifest) != 1 {
					t.Errorf("kept %+v, want everything", gen)
				}
			}
			logs := buf.String()
			if c.log == "" && logs != "" || c.log != "" && !strings.Contains(logs, c.log) {
				t.Errorf("log %q, want %q", logs, c.log)
			}
		})
	}
}
