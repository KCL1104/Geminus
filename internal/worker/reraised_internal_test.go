package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// reraised is a handler.Reraised: a panic raised again, with the value and
// the stack it was first raised with.
type reraised struct {
	value any
	stack []byte
}

func (r *reraised) PanicValue() any    { return r.value }
func (r *reraised) PanicStack() []byte { return r.stack }

var _ handler.Reraised = (*reraised)(nil)

// E3 review round 1 (finding 3): a panic a handler recovered and raised
// again on another frame (handler.Reraised: the proposal role graph raises
// a node's panic again on the handler's goroutine) is recorded as the panic
// it was first: its message only when the Go runtime raised it, its own
// type otherwise (review L3, unchanged), and the stack it was first raised
// on in place of the worker's own. One that cannot be read (its methods
// panic) or carries no value is recorded as the value it is, with the
// worker's stack; one that carries no stack keeps the worker's stack.
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
		"a runtime error":  {&reraised{value: runtimeErr, stack: []byte(origin)}, "handler panicked: assignment to entry in nil map", origin},
		"a value":          {&reraised{value: "cell CASE-SENTINEL", stack: []byte(origin)}, "handler panicked with a string", origin},
		"no stack":         {&reraised{value: "cell CASE-SENTINEL"}, "handler panicked with a string", own},
		"no value":         {&reraised{stack: []byte(origin)}, "handler panicked with a *worker.reraised", own},
		"unreadable":       {(*reraised)(nil), "handler panicked with a *worker.reraised", own},
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
