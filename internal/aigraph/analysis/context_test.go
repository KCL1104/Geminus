package analysis_test

// H-059 (E6 review round 1, finding 1; E3 review round 1's pattern for the
// proposal graph): every step runs under the attempt's own context (its
// values, its cancellation, its cause and its deadline) and meets a stop
// itself, as the analyzer's steps did before the graph. Eino, which ends a
// run between two nodes once its context is done, runs on one that never
// is.

import (
	"context"
	"encoding/json/v2"
	"errors"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/aigraph/analysis"
)

var errStop = errors.New("the worker is stopping")

// stopped is an attempt already stopped, with errStop as its cause, the
// caller's value and a deadline.
func stopped(t *testing.T) (context.Context, time.Time) {
	t.Helper()
	deadline := time.Now().Add(time.Hour)
	ctx, cancel := context.WithDeadline(context.WithValue(context.Background(), callerKey{}, "job_sentinel"), deadline)
	t.Cleanup(cancel)
	ctx, stop := context.WithCancelCause(ctx)
	stop(errStop)
	return ctx, deadline
}

// seen is what the observer saw of the context of a call.
type seen struct {
	value    any
	err      error
	cause    error
	deadline time.Time
	callErr  error
}

// An attempt that has stopped when the run starts: each step still runs
// and meets the stop itself. What the server stored that cannot be read,
// runs it does not compare and a context that cannot be built end with
// their own kinds; good input reaches the client, which refuses the call
// before the backend, and the observer is told of that call under the
// attempt's context (its value, its error and cause, its deadline).
func TestAStoppedAttemptIsSeenByItsSteps(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*analysis.Input)
		kind analysis.Kind
	}{
		{"the version", func(in *analysis.Input) { in.Version.IR = []byte(`{"model_id":`) }, analysis.VersionUndecodable},
		{"the scenario's result", func(in *analysis.Input) { in.Scenario.Result = []byte(`[]`) }, analysis.ResultUndecodable},
		{"the patch", func(in *analysis.Input) { in.ScenarioPatch = []byte(`[`) }, analysis.PatchUndecodable},
		{"runs the server does not compare", func(in *analysis.Input) { in.Compare = pairCompare("mv_01M4YDQK00FRYWV7GS7X1QCXZS") }, analysis.NotComparable},
		{"the locale", func(in *analysis.Input) { in.OutputLocale = "fr" }, analysis.ContextRefused},
		{"good input", func(*analysis.Input) {}, analysis.CallFailed},
		{"good input alone", func(in *analysis.Input) { *in = alone(t) }, analysis.CallFailed},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, deadline := stopped(t)
			f := fake.New()
			var calls []seen
			g, err := analysis.New(analysis.Config{Provider: client(t, f), Budget: provider.DefaultBudget(), Packs: packs(t),
				Called: func(ctx context.Context, _ int, _ provider.Metadata, err error) {
					d, _ := ctx.Deadline()
					calls = append(calls, seen{value: ctx.Value(callerKey{}), err: ctx.Err(), cause: context.Cause(ctx), deadline: d, callErr: err})
				}})
			if err != nil {
				t.Fatal(err)
			}
			in := input(t)
			c.edit(&in)
			_, err = g.Run(ctx, in)
			var e *analysis.Error
			if !errors.As(err, &e) || e.Kind != c.kind {
				t.Fatalf("%v, want an *analysis.Error of kind %s", err, c.kind)
			}
			if len(f.Requests()) != 0 {
				t.Errorf("%d requests", len(f.Requests()))
			}
			if c.kind != analysis.CallFailed {
				if len(calls) != 0 {
					t.Errorf("observed %+v before any call", calls)
				}
				return
			}
			if provider.CodeOf(e.Err) != provider.CodeCancelled || !errors.Is(e.Err, context.Canceled) || e.Metadata == nil {
				t.Errorf("the call's failure: %v %+v", e.Err, e.Metadata)
			}
			if len(calls) != 1 {
				t.Fatalf("observed %d calls, want 1", len(calls))
			}
			s := calls[0]
			if s.value != "job_sentinel" || s.err != context.Canceled || s.cause != errStop || !s.deadline.Equal(deadline) || s.callErr != e.Err {
				t.Errorf("the observer saw %+v under the attempt (cause %v, deadline %v)", s, errStop, deadline)
			}
		})
	}
}

// The call runs under the attempt's context: a backend that holds the call
// until its context ends returns as soon as the attempt stops (not at its
// fallback), seeing the attempt's value and cause; the run ends with the
// client's cancelled call, observed once.
func TestTheCallRunsUnderTheAttemptsContext(t *testing.T) {
	ctx, stop := context.WithCancelCause(context.WithValue(context.Background(), callerKey{}, "job_sentinel"))
	defer stop(nil)
	f := fake.New()
	b := &holding{Backend: f, stop: func() { stop(errStop) }}
	obs := &calls{}
	g := graph(t, client(t, b), packs(t), provider.DefaultBudget(), obs)
	_, err := g.Run(ctx, input(t))
	var e *analysis.Error
	if !errors.As(err, &e) || e.Kind != analysis.CallFailed || provider.CodeOf(e.Err) != provider.CodeCancelled || !errors.Is(e.Err, context.Canceled) {
		t.Errorf("%v, want the client's cancelled call", err)
	}
	if b.fellBack || b.value != "job_sentinel" || b.cause != errStop {
		t.Errorf("the backend fell back %t, saw %v and cause %v", b.fellBack, b.value, b.cause)
	}
	if seen := obs.all(); len(seen) != 1 || e == nil || seen[0].err != e.Err {
		t.Errorf("observed %+v", seen)
	}
}

// holding stops the attempt as it is asked, then holds the call until the
// call's context ends, or 10 s.
type holding struct {
	*fake.Backend
	stop     func()
	fellBack bool
	value    any
	cause    error
}

func (h *holding) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	h.value = ctx.Value(callerKey{})
	h.stop()
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
		h.fellBack = true
	}
	h.cause = context.Cause(ctx)
	return h.Backend.Complete(ctx, req)
}

// invented is an answer that states a number nobody supplied (as
// TestFailuresAreTyped's ungrounded answer).
func invented(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(provider.AnalysisOutput{
		Baseline: &provider.AnalysisSummary{Summary: "Baseline."}, Scenario: provider.AnalysisSummary{Summary: "Throughput rose by 4242.5."},
		Delta: []provider.AnalysisDelta{}, Bottleneck: []provider.Bottleneck{}, Assumptions: []string{}, Uncertainty: []string{"Only one replication was run."},
		SuggestedNextExperiments: []provider.Experiment{}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
