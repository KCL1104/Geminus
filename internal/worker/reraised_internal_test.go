package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// raisedAgain is a handler.Reraised: a panic raised again, with the value
// and the stack it was first raised with. (E6's merge of E2 and E3: E2's
// reraised, in outcome_internal_test.go, is the value-receiver one; one
// handler.Reraised, E2's, with the stack as a string.)
type raisedAgain struct {
	value any
	stack string
}

func (r *raisedAgain) PanicValue() any    { return r.value }
func (r *raisedAgain) PanicStack() string { return r.stack }

var _ handler.Reraised = (*raisedAgain)(nil)

// E3 review round 1 (finding 3): a panic a handler recovered and raised
// again on another frame (handler.Reraised: the proposal role graph raises
// a node's panic again on the handler's goroutine) is recorded as the panic
// it was first: its message only when the Go runtime raised it, its own
// type otherwise (review L3, unchanged), and the stack it was first raised
// on in place of the worker's own. One that cannot be read (its methods
// panic), or carries no value or no stack, is recorded as the value it is,
// with the worker's stack (E2's contract, kept by E6's merge: E3's "no
// stack" row recorded the first value with the worker's stack).
func TestAPanicRaisedAgainIsRecordedAsTheFirst(t *testing.T) {
	var runtimeErr any
	func() {
		defer func() { runtimeErr = recover() }()
		var m map[string]int
		m["x"] = 1
	}()
	const origin = "goroutine 7 [running]:\nexample.com/handler.fails()\n"
	const own = "" // the worker's own stack
	for name, c := range map[string]struct {
		v       any
		message string
		stack   string
	}{
		"a runtime error":  {&raisedAgain{value: runtimeErr, stack: origin}, "handler panicked: assignment to entry in nil map", origin},
		"a value":          {&raisedAgain{value: "cell CASE-SENTINEL", stack: origin}, "handler panicked with a string", origin},
		"no stack":         {&raisedAgain{value: "cell CASE-SENTINEL"}, "handler panicked with a *worker.raisedAgain", own},
		"no value":         {&raisedAgain{stack: origin}, "handler panicked with a *worker.raisedAgain", own},
		"unreadable":       {(*raisedAgain)(nil), "handler panicked with a *worker.raisedAgain", own},
		"not raised again": {"cell CASE-SENTINEL", "handler panicked with a string", own},
	} {
		w := &Worker{cfg: Config{Handlers: map[jobs.Type]handler.Handler{
			jobs.ModelProposal: handler.Func(func(context.Context, handler.Run) error { panic(c.v) }),
		}}}
		err := w.handle(context.Background(), &attempt{job: jobs.Job{Type: jobs.ModelProposal}})
		var p panicked
		if !errors.As(err, &p) || p.Error() != c.message || strings.Contains(p.Error(), "SENTINEL") {
			t.Errorf("%s: %v, want %q", name, err, c.message)
			continue
		}
		switch {
		case c.stack != own && p.stack != c.stack:
			t.Errorf("%s: stack %q, want the first one's", name, p.stack)
		case c.stack == own && (!strings.HasPrefix(p.stack, "goroutine ") || !strings.Contains(p.stack, "worker.(*Worker).handle")):
			t.Errorf("%s: stack %q, want the worker's own", name, p.stack)
		}
		// What the loop reads of it is the same verdict as for any panic.
		if v := read(err); v.panicked == nil || v.failure != nil || v.broken || v.large {
			t.Errorf("%s: read as %+v", name, v)
		}
	}
}
