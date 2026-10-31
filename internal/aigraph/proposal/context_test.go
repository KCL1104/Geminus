package proposal

// E3 review round 1 (findings 1 and 2): the attempt's context, and its
// cancellation, reach every step and the paid calls, and an attempt that
// stops is seen by its steps themselves, as the handler's own pipeline saw
// it before the graph (H-059): never by Eino between two nodes.

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/model"
)

// probe is a value only the attempt's context carries.
type probe struct{}

// seen is what a step saw of its context: the probe's value, whether the
// attempt had ended, and why.
type seen struct {
	value      any
	err, cause error
}

func saw(ctx context.Context) seen {
	return seen{value: ctx.Value(probe{}), err: ctx.Err(), cause: context.Cause(ctx)}
}

// errStopped is why a test stops its attempt (context.Cause).
var errStopped = errors.New("the worker is stopping")

// observing is a backend that notes what each call's context carried.
type observing struct {
	*fake.Backend
	mu    sync.Mutex
	calls []seen
}

func (o *observing) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	o.mu.Lock()
	o.calls = append(o.calls, saw(ctx))
	o.mu.Unlock()
	return o.Backend.Complete(ctx, req)
}

// Every step that does I/O runs under the attempt's own context: the
// snapshots' read, the base's verification, both binding stages and both
// paid calls see what it carries.
func TestEveryStepRunsUnderTheAttemptsContext(t *testing.T) {
	intent := "Six runners."
	f := fake.New()
	f.Script(provider.RoleModeler, intent, proposed(raise))
	f.ScriptRevision(provider.RoleModeler, intent, proposed(raise))
	b := &observing{Backend: f}
	var steps []string
	var seenBy []seen
	note := func(step string, ctx context.Context) {
		steps, seenBy = append(steps, step), append(seenBy, saw(ctx))
	}
	refusal := []model.ValidationIssue{model.NewIssue("DATASET_BINDING_INVALID", "/nodes/1", "x", model.SeverityError)}
	ctx := context.WithValue(context.Background(), probe{}, "the attempt")
	res, err := graphOf(t, client(t, b)).Run(ctx, request(t, "edit", intent), Hooks{
		Preload: func(ctx context.Context) error { note("preload", ctx); return nil },
		Verify: func(ctx context.Context, _ *model.ModelIR) ([]model.DatasetBinding, error) {
			note("verify", ctx)
			return nil, nil
		},
		Bind: func(ctx context.Context, _ *model.ModelIR) ([]model.ValidationIssue, error) {
			note("bind", ctx)
			return refusal, nil
		},
	})
	if err != nil || res.Repair == nil || res.Outcome.Status != review.Invalid {
		t.Fatalf("%v %+v %s", err, res.Repair, res.Outcome.Status)
	}
	if !slices.Equal(steps, []string{"preload", "verify", "bind", "bind"}) {
		t.Errorf("steps %v", steps)
	}
	for i, s := range append(seenBy, b.calls...) {
		if s.value != "the attempt" || s.err != nil || s.cause != nil {
			t.Errorf("step %d saw %+v", i, s)
		}
	}
	if len(b.calls) != 2 {
		t.Errorf("%d calls", len(b.calls))
	}
}

// An attempt that has already stopped when it starts (finding 1) is seen by
// each step itself, as before the graph: what the server stored that cannot
// be read and a request the context builder refuses end the attempt with
// their own errors; otherwise the snapshots' read and the base's
// verification run and see the cancellation, and why (a hook's own error is
// the attempt's), and the call is refused by the client before the backend,
// with its cancelled error told to Answered and returned in a *CallError;
// nothing is reviewed or bound. The live eval's path (no hooks) ends with
// the same call error.
func TestAStoppedAttemptIsSeenByItsSteps(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errStopped)
	intent := "Six runners."
	for name, c := range map[string]struct {
		edit func(*Request)
		want Input
		code aicontext.Code
	}{
		"the base":   {edit: func(r *Request) { r.Base.IR = []byte(`{"not":"a model"`) }, want: InputBase},
		"the pool":   {edit: func(r *Request) { r.Pool = []byte(`[`) }, want: InputPool},
		"the locale": {edit: func(r *Request) { r.OutputLocale = "fr" }, code: aicontext.InvalidLocale},
	} {
		f := fake.New()
		h := &hooks{f: f}
		req := request(t, "edit", intent)
		c.edit(&req)
		_, err := graphOf(t, client(t, f)).Run(ctx, req, h.Hooks())
		var ie *InputError
		var ce *ContextError
		switch {
		case c.want != "" && (!errors.As(err, &ie) || ie.Input != c.want):
			t.Errorf("%s: %v", name, err)
		case c.code != "" && (!errors.As(err, &ce) || ce.Code != c.code):
			t.Errorf("%s: %v", name, err)
		}
		if len(f.Requests()) != 0 || h.preloaded != nil || h.answered != nil {
			t.Errorf("%s: %d requests, preloaded %v, answered %v", name, len(f.Requests()), h.preloaded, h.answered)
		}
	}

	// Good input: the hooks run and see that the attempt stopped.
	f := fake.New()
	f.Script(provider.RoleModeler, intent, proposed(raise))
	var steps []seen
	h := &hooks{f: f}
	hk := h.Hooks()
	preload, verify := hk.Preload, hk.Verify
	hk.Preload = func(ctx context.Context) error { steps = append(steps, saw(ctx)); return preload(ctx) }
	hk.Verify = func(ctx context.Context, base *model.ModelIR) ([]model.DatasetBinding, error) {
		steps = append(steps, saw(ctx))
		return verify(ctx, base)
	}
	res, err := graphOf(t, client(t, f)).Run(ctx, request(t, "edit", intent), hk)
	var ce *CallError
	if !errors.As(err, &ce) || ce.Final || provider.CodeOf(err) != provider.CodeCancelled || !errors.Is(err, context.Canceled) {
		t.Fatalf("good input: %v", err)
	}
	if len(steps) != 2 || steps[0].err != context.Canceled || steps[1].err != context.Canceled || steps[0].cause != errStopped || steps[1].cause != errStopped {
		t.Errorf("the steps saw %+v", steps)
	}
	if !slices.Equal(h.answered, []int{1}) || ce.Err != h.answerErrs[0] || len(f.Requests()) != 0 || h.bound != nil || res.Call == nil || res.Repair != nil {
		t.Errorf("answered %v %v, %d requests, bound %v, %+v", h.answered, h.answerErrs, len(f.Requests()), h.bound, res)
	}
	// A hook that ends on the cancellation (as the binding stage does) ends
	// the attempt with its own error, before any call.
	f = fake.New()
	h = &hooks{f: f}
	hk = h.Hooks()
	hk.Verify = func(ctx context.Context, _ *model.ModelIR) ([]model.DatasetBinding, error) { return nil, ctx.Err() }
	if _, err := graphOf(t, client(t, f)).Run(ctx, request(t, "edit", intent), hk); err != context.Canceled || h.answered != nil {
		t.Errorf("a hook's own error: %v, answered %v", err, h.answered)
	}
	// No hooks (the live eval): the client refuses the call.
	f = fake.New()
	f.Script(provider.RoleModeler, intent, proposed(raise))
	req := request(t, "edit", intent)
	req.Repair = false
	res, err = graphOf(t, client(t, f)).Run(ctx, req, Hooks{})
	if !errors.As(err, &ce) || provider.CodeOf(ce.Err) != provider.CodeCancelled || res.Call == nil || len(f.Requests()) != 0 {
		t.Errorf("no hooks: %v, %d requests", err, len(f.Requests()))
	}
}

// An attempt that stops after the first answer, between two steps, goes on
// as before the graph: the answer is reviewed and the binding stage runs
// and sees the cancellation (here it ends on it, as datasets.Resolve does);
// an answer the server refused is not repaired (repair.Plan sees it: no
// second call, nothing told to Answered).
func TestAStopAfterTheAnswerIsSeenByTheNextSteps(t *testing.T) {
	intent := "Six runners."
	// Stopped as the first call is told to Answered: the binding stage sees it.
	f := fake.New()
	f.Script(provider.RoleModeler, intent, proposed(raise))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &hooks{f: f}
	hk := h.Hooks()
	answered := hk.Answered
	hk.Answered = func(call int, m provider.Metadata, err error) { answered(call, m, err); cancel() }
	var bindSaw []seen
	hk.Bind = func(ctx context.Context, _ *model.ModelIR) ([]model.ValidationIssue, error) {
		bindSaw = append(bindSaw, saw(ctx))
		return nil, ctx.Err()
	}
	res, err := graphOf(t, client(t, f)).Run(ctx, request(t, "edit", intent), hk)
	var be *BindError
	if !errors.As(err, &be) || be.Err != context.Canceled || be.Repair || len(bindSaw) != 1 || bindSaw[0].err != context.Canceled || res.Call == nil {
		t.Errorf("stopped before the binding stage: %v, bind saw %+v", err, bindSaw)
	}
	// Stopped in the binding stage that refuses the answer: no repair.
	f = fake.New()
	f.Script(provider.RoleModeler, intent, proposed(raise))
	f.ScriptRevision(provider.RoleModeler, intent, proposed(raise))
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	h = &hooks{f: f}
	hk = h.Hooks()
	hk.Bind = func(_ context.Context, _ *model.ModelIR) ([]model.ValidationIssue, error) {
		cancel()
		return []model.ValidationIssue{model.NewIssue("DATASET_BINDING_INVALID", "/nodes/1", "x", model.SeverityError)}, nil
	}
	res, err = graphOf(t, client(t, f)).Run(ctx, request(t, "edit", intent), hk)
	if provider.CodeOf(err) != provider.CodeCancelled || !errors.Is(err, context.Canceled) || res.Repair != nil ||
		!slices.Equal(h.answered, []int{1}) || len(f.Requests()) != 1 {
		t.Errorf("stopped before the repair: %v, repair %+v, answered %v, %d requests", err, res.Repair, h.answered, len(f.Requests()))
	}
}

// blocking holds its n-th call until the call's context ends, then returns
// that context's error; it answers as the fake does after ten seconds if the
// context never ends (fellBack). started is closed once it holds the call.
type blocking struct {
	*fake.Backend
	n        int
	mu       sync.Mutex
	seen     int
	started  chan struct{}
	fellBack bool
}

func (b *blocking) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	b.mu.Lock()
	b.seen++
	hold := b.seen == b.n
	b.mu.Unlock()
	if !hold {
		return b.Backend.Complete(ctx, req)
	}
	close(b.started)
	select {
	case <-ctx.Done():
		return provider.Response{}, ctx.Err()
	case <-time.After(10 * time.Second):
		b.mu.Lock()
		b.fellBack = true
		b.mu.Unlock()
		return b.Backend.Complete(ctx, req)
	}
}

// Cancellation reaches the paid calls themselves (finding 2): an attempt
// cancelled while its first call, or its repair's, is in flight ends at
// once, with the client's cancelled error as Answered was told it (not one
// made up between two nodes): a first call's in a *CallError, the repair's
// as it is with the first outcome kept and the repair's record saying it
// stopped (no outcome).
func TestCancellationReachesThePaidCalls(t *testing.T) {
	intent := "Let more CI runs pass."
	for _, n := range []int{1, 2} {
		f := fake.New()
		f.Script(provider.RoleModeler, intent, proposed(broken))
		f.ScriptRevision(provider.RoleModeler, intent, proposed(fixed))
		b := &blocking{Backend: f, n: n, started: make(chan struct{})}
		ctx, cancel := context.WithCancel(context.Background())
		go func() { <-b.started; cancel() }()
		h := &hooks{f: f}
		begun := time.Now()
		res, err := graphOf(t, client(t, b)).Run(ctx, request(t, "edit", intent), h.Hooks())
		took := time.Since(begun)
		cancel()
		if b.fellBack || len(h.answerErrs) != n || provider.CodeOf(h.answerErrs[n-1]) != provider.CodeCancelled {
			t.Fatalf("call %d: fell back %v after %v, answered %v %v", n, b.fellBack, took, h.answered, h.answerErrs)
		}
		switch n {
		case 1:
			var ce *CallError
			if !errors.As(err, &ce) || ce.Err != h.answerErrs[0] || res.Repair != nil || res.Call == nil {
				t.Errorf("the first call: %v %+v", err, res.Repair)
			}
		case 2:
			if err != h.answerErrs[1] || res.Repair == nil || res.Repair.Outcome != "" || res.Repair.Call == nil || res.Outcome.Status != review.Invalid || h.bound != nil {
				t.Errorf("the repair's call: %v %+v %s, bound %v", err, res.Repair, res.Outcome.Status, h.bound)
			}
		}
		t.Logf("call %d: Run took %v in all", n, took)
	}
}
