package generator

import (
	"context"
	"strings"
	"testing"

	proposalgraph "github.com/kcl1104/geminus/internal/aigraph/proposal"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// The graph's re-raised panic is the worker's handler.Reraised.
var _ handler.Reraised = (*proposalgraph.Panic)(nil)

// E3 review round 1 (finding 3): a panic in one of the handler's own steps
// on the graph (here its log line of the call, on a handler left without a
// logger) reaches the worker as a handler.Reraised: the Go runtime's own
// error, so the worker logs its message and does not retry the job as
// before, and the stack it was raised on, which names the handler's function
// that failed, not only the graph's re-raise.
func TestAPanicInAStepReachesTheWorkerWithItsStack(t *testing.T) {
	h, err := New(Config{Provider: client(t, scripted(t, "Six runners.", reference(`[]`))), Packs: packs(t), Actor: "worker-1", Log: discard()})
	if err != nil {
		t.Fatal(err)
	}
	h.cfg.Log = nil
	st := stored(t, "edit", "Six runners.")
	got := func() (v any) {
		defer func() { v = recover() }()
		_, err := h.generateAsked(context.Background(), st.proposal.OrganizationID, st, testAttribution, false, "")
		t.Errorf("no panic: %v", err)
		return nil
	}()
	r, ok := got.(handler.Reraised)
	if !ok {
		t.Fatalf("%#v", got)
	}
	if re, ok := observability.RuntimeError(r.PanicValue()); !ok || !strings.Contains(re.Error(), "nil pointer dereference") {
		t.Errorf("the value %#v", r.PanicValue())
	}
	if stack := string(r.PanicStack()); !strings.Contains(stack, "generator.(*Handler).logCall") || !strings.Contains(stack, "generator.(*Handler).generateAsked") {
		t.Errorf("the stack does not name the frame that failed:\n%s", stack)
	}
}
