package worker

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// Review L5: how an attempt ended whose failure the loop tried to record,
// for its span and metric: requeued or failed when recorded; lease_lost
// when the job was no longer the attempt's; not_recorded when the record
// failed otherwise.
func TestFailureOutcome(t *testing.T) {
	for _, c := range []struct {
		err     error
		retried bool
		want    string
	}{
		{nil, false, "failed"},
		{nil, true, "requeued"},
		{jobs.ErrLeaseLost, false, "lease_lost"},
		{fmt.Errorf("commit: %w", jobs.ErrLeaseLost), true, "lease_lost"},
		{errors.New("the database is down"), false, "not_recorded"},
		{errors.New("the database is down"), true, "not_recorded"},
	} {
		if got := failureOutcome(c.err, c.retried); got != c.want {
			t.Errorf("failureOutcome(%v, %v) = %s, want %s", c.err, c.retried, got, c.want)
		}
	}
}

// The loop's event carries the failure's own retryable flag, and every job
// type's event fits the catalogue (a refusal would leave the failure
// unrecorded, D-091 (c)).
func TestTheAttemptsEventOfEveryType(t *testing.T) {
	w := &Worker{cfg: Config{ID: "worker-7"}}
	for _, typ := range []jobs.Type{jobs.Simulation, jobs.ModelProposal, jobs.ImportProfile, jobs.ImportValidate, jobs.ImportMapping, jobs.Analysis} {
		r := jobs.Resource{ImportID: ids.New("imp")}
		switch typ {
		case jobs.Simulation:
			r = jobs.Resource{SimulationRunID: ids.New("sim")}
		case jobs.ModelProposal:
			r = jobs.Resource{AIProposalID: ids.New("aip")}
		case jobs.Analysis:
			r = jobs.Resource{AIAnalysisID: ids.New("ana")}
		}
		var got tx
		if err := w.audit(context.Background(), &got, job(typ, r, `{}`), jobs.Failure{Code: "ENGINE_FAILED", Message: "x"}, false); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		var meta map[string]any
		if err := json.Unmarshal([]byte(got.execs[0][9].(string)), &meta); err != nil || meta["retryable"] != false || meta["job_type"] != string(typ) {
			t.Errorf("%s: %v %v", typ, meta, err)
		}
	}
}

type fakeRuntime string

func (f fakeRuntime) Error() string { return string(f) }
func (fakeRuntime) RuntimeError()   {}

// Review L3: a recovered panic's message is logged only when the Go runtime
// raised it; a value of any other type, even one with a RuntimeError
// method, is named by its type alone.
func TestPanickedTrustsOnlyTheRuntime(t *testing.T) {
	if got := (panicked{value: fakeRuntime("cell CASE-SENTINEL")}).Error(); got != "handler panicked with a worker.fakeRuntime" {
		t.Errorf("%q", got)
	}
	var got string
	func() {
		defer func() { got = (panicked{value: recover()}).Error() }()
		var m map[string]int
		m["x"] = 1
	}()
	if got != "handler panicked: assignment to entry in nil map" || strings.Contains(got, "SENTINEL") {
		t.Errorf("%q", got)
	}
}

// reraised is a handler.Reraised: a panic raised again after something
// between the handler and the code that panicked recovered it, as the
// analyst's graph raises a node's panic (internal/aigraph/analysis.Panic).
type reraised struct {
	value any
	stack string
}

var _ handler.Reraised = reraised{}

func (r reraised) PanicValue() any    { return r.value }
func (r reraised) PanicStack() string { return r.stack }

// brokenReraised's methods panic.
type brokenReraised struct{}

func (brokenReraised) PanicValue() any    { panic("PanicValue called") }
func (brokenReraised) PanicStack() string { panic("PanicStack called") }

// nilMapPanic is the runtime's panic of a nil map assignment.
func nilMapPanic() (v any) {
	defer func() { v = recover() }()
	var m map[string]int
	m["x"] = 1
	return nil
}

// panickingWith runs handle on a handler that panics with v and returns
// what the loop reads of it.
func panickingWith(t *testing.T, v any) *panicked {
	t.Helper()
	w := &Worker{cfg: Config{Handlers: map[jobs.Type]handler.Handler{jobs.Analysis: handler.Func(func(context.Context, handler.Run) error {
		panic(v)
	})}}}
	got := read(w.handle(context.Background(), &attempt{w: w, job: job(jobs.Analysis, jobs.Resource{AIAnalysisID: ids.New("ana")}, `{}`)}))
	if got.panicked == nil || got.failure != nil || got.broken || got.large {
		t.Fatalf("read %+v, want a panic", got)
	}
	return got.panicked
}

// Review round 1 (E2), finding 1: a handler.Reraised is recorded with its
// own value (only a runtime error's message logged, as for any panic) and
// the stack where the code panicked, not where the worker recovered it;
// any other panic with the worker's stack, as before. A Reraised whose
// methods panic, or that carries no value or no stack, is recorded as any
// other value, and the worker survives it.
func TestAReraisedPanicKeepsItsValueAndStack(t *testing.T) {
	const origin = "goroutine 7 [running]:\norigin.frame()\n\t/src/origin.go:12 +0x1d\n"
	if p := panickingWith(t, reraised{value: nilMapPanic(), stack: origin}); p.Error() != "handler panicked: assignment to entry in nil map" ||
		p.stack != origin {
		t.Errorf("a re-raised runtime panic: %q, stack %q", p.Error(), p.stack)
	}
	if p := panickingWith(t, reraised{value: "cell CASE-SENTINEL", stack: origin}); p.Error() != "handler panicked with a string" || p.stack != origin {
		t.Errorf("a re-raised value: %q, stack %q", p.Error(), p.stack)
	}
	for name, v := range map[string]any{
		"a value":                   "cell CASE-SENTINEL",
		"a runtime panic":           nilMapPanic(),
		"a reraised that panics":    brokenReraised{},
		"a reraised without stack":  reraised{value: "x"},
		"a reraised without value":  reraised{stack: origin},
		"a nil pointer to reraised": (*reraised)(nil),
	} {
		p := panickingWith(t, v)
		if p.value != v || !strings.Contains(p.stack, "worker.(*Worker).handle(") || strings.Contains(p.stack, "origin.frame") {
			t.Errorf("%s: recorded %#v with stack\n%s", name, p.value, p.stack)
		}
		if strings.Contains(p.Error(), "SENTINEL") {
			t.Errorf("%s: %q", name, p.Error())
		}
	}
}
