package api_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/runs/runner"
	"github.com/kcl1104/geminus/internal/tenant"
)

// longRun is a run of the default template that takes the engine seconds
// (a year, n replications).
func longRun(mv string, n int) string {
	return runRequest(mv, n, "none", map[string]any{"config": map[string]any{"horizon": map[string]any{"value": 365, "unit": "day"},
		"warmup": map[string]any{"value": 0, "unit": "day"}, "replications": n, "seed": 3, "trace_level": "none"}})
}

// TEST-021: a queued run is cancelled at once with its job, and no worker
// runs it; a cancelled run cancelled again is the same run; the key of a
// cancellation is its run's.
func TestCancelQueued(t *testing.T) {
	e := setupRuns(t)
	editor, owner := e.a.Members[tenant.Editor], e.a.Members[tenant.Owner]
	_, mv := e.version(t, e.a, golden(t, "deterministic-queue.json"), nil)
	id := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-cq-0001", runRequest(mv, 1, "none", nil))).ID
	other := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-cq-0002", runRequest(mv, 1, "none", nil))).ID
	first := e.cancelRun(owner.Subject, e.a.ID, "cancel-q-0001", id)
	if first.status != 200 {
		t.Fatalf("%d %s", first.status, first.body)
	}
	valid(t, first, "simulation.schema.json#/$defs/Run")
	run := decode[runBody](t, first)
	if run.Status != "cancelled" || run.CancellationRequested || run.Progress.Revision != 1 {
		t.Errorf("cancelled run %+v", run)
	}
	if n := e.count(`SELECT count(*) FROM twin.jobs WHERE simulation_run_id=$1 AND status='cancelled'`, id); n != 1 {
		t.Error("the queued job was not cancelled with its run")
	}
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE action='simulation.cancel' AND target_id=$1 AND actor_id=$2 AND metadata->>'effect'='cancelled'`, id, owner.UserID); n != 1 {
		t.Errorf("%d cancel audit events", n)
	}
	if r := e.cancelRun(owner.Subject, e.a.ID, "cancel-q-0001", id); r.status != 200 || string(r.body) != string(first.body) {
		t.Errorf("a replay: %d %s", r.status, r.body)
	}
	if r := e.cancelRun(owner.Subject, e.a.ID, "cancel-q-0001", other); r.status != 409 || code(t, r) != "IDEMPOTENCY_KEY_CONFLICT" {
		t.Errorf("the key for another run: %d %s", r.status, r.body)
	}
	again := e.cancelRun(editor.Subject, e.a.ID, "cancel-q-0002", id)
	if again.status != 200 || decode[runBody](t, again).Status != "cancelled" || decode[runBody](t, again).Progress.Revision != 1 {
		t.Errorf("cancelling a cancelled run: %d %s", again.status, again.body)
	}
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE action='simulation.cancel' AND target_id=$1`, id); n != 1 {
		t.Errorf("%d cancel audit events after a no-op cancellation", n)
	}
	for _, body := range []string{`{"force":true}`, `[]`} {
		if r := e.do("POST", "/v1/simulations/"+id+"/cancel", owner.Subject, e.a.ID, map[string]string{"Idempotency-Key": "cancel-q-body"}, body); r.status != 422 {
			t.Errorf("cancel body %s: %d %s", body, r.status, r.body)
		}
	}
	drain(t, e.loop(t, runner.Config{}, nil), 30*time.Second)
	if run := e.getRun(t, editor.Subject, e.a.ID, id); run.Status != "cancelled" {
		t.Errorf("a cancelled run ran: %+v", run)
	}
	if run := e.getRun(t, editor.Subject, e.a.ID, other); run.Status != "completed" {
		t.Errorf("the other run: %+v", run)
	}
	if r := e.cancelRun(editor.Subject, e.a.ID, "cancel-q-0003", other); r.status != 409 || code(t, r) != "LIFECYCLE_CONFLICT" {
		t.Errorf("cancel a completed run: %d %s", r.status, r.body)
	}
}

// TEST-021: a running run's cancellation is requested and honoured at the
// engine's next checkpoint, within seconds: the engine stops long before
// it would have finished, and no result is kept.
func TestCancelRunning(t *testing.T) {
	e := setupRuns(t)
	editor := e.a.Members[tenant.Editor]
	_, mv := e.version(t, e.a, golden(t, "default-delivery.json"), nil)
	w := e.loop(t, runner.Config{Workers: 1}, nil)
	// What one replication of the year costs, to know what 150 would.
	probe := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-cr-probe", longRun(mv, 1))).ID
	drain(t, w, time.Minute)
	perReplication := e.getRun(t, editor.Subject, e.a.ID, probe).Progress.EventsProcessed
	const n = 150
	id := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-cr-0001", longRun(mv, n))).ID
	done := make(chan time.Time, 1)
	go func() { w.Pass(context.Background()); done <- time.Now() }()
	deadline := time.Now().Add(20 * time.Second)
	for e.getRun(t, editor.Subject, e.a.ID, id).Status != "running" {
		if time.Now().After(deadline) {
			t.Fatal("the run did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	r := e.cancelRun(editor.Subject, e.a.ID, "cancel-r-0001", id)
	asked := time.Now()
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if run := decode[runBody](t, r); run.Status != "running" || !run.CancellationRequested {
		t.Errorf("a requested cancellation: %+v", run)
	}
	var ended time.Time
	select {
	case ended = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the worker did not stop the run")
	}
	if took := ended.Sub(asked); took > 5*time.Second {
		t.Errorf("the cancellation took %v (target ≤ 5 s)", took)
	}
	t.Logf("cancellation honoured %v after the request", ended.Sub(asked))
	run := e.getRun(t, editor.Subject, e.a.ID, id)
	if run.Status != "cancelled" || !run.CancellationRequested || run.Error != nil {
		t.Errorf("the run after its cancellation: %+v", run)
	}
	if n := e.count(`SELECT count(*) FROM twin.simulation_results WHERE run_id=$1`, id); n != 0 {
		t.Error("a cancelled run has a result")
	}
	// The engine stopped early: it reserved (and so spent) far less than
	// the whole run needs.
	if reserved := int64(e.count(`SELECT event_budget_reserved FROM twin.simulation_runs WHERE id=$1`, id)); perReplication == 0 || reserved >= n*perReplication/2 {
		t.Errorf("the run reserved %d events; uncancelled it needs about %d", reserved, n*perReplication)
	}
	if n := e.count(`SELECT count(*) FROM twin.jobs WHERE simulation_run_id=$1 AND status='cancelled' AND attempt=1`, id); n != 1 {
		t.Error("the job did not end cancelled in its attempt")
	}
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND ((action='simulation.cancel' AND metadata->>'effect'='cancellation_requested' AND actor_type='user')
		OR (action='simulation.cancelled' AND actor_type='worker'))`, id); n != 2 {
		t.Errorf("%d cancellation audit events, want the request and its end", n)
	}
	if r := e.do("GET", "/v1/simulations/"+id+"/results", editor.Subject, e.a.ID, nil, ""); r.status != 409 || code(t, r) != "RESULT_NOT_READY" {
		t.Errorf("results of a cancelled run: %d %s", r.status, r.body)
	}
}

// TEST-021: cancel and complete are exclusive. A cancellation the API
// answers 200 ends the run cancelled without a result; one it answers 409
// came after the completion. Never both, whatever the timing.
func TestCancelRacesCompletion(t *testing.T) {
	e := setupRuns(t)
	editor := e.a.Members[tenant.Editor]
	_, mv := e.version(t, e.a, golden(t, "deterministic-queue.json"), nil)
	w := e.loop(t, runner.Config{}, nil)
	seen := map[string]int{}
	for i := range 24 {
		id := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, fmt.Sprintf("run-race-%04d", i), runRequest(mv, 3, "sampled", nil))).ID
		done := make(chan struct{})
		go func() { drain(t, w, 30*time.Second); close(done) }()
		time.Sleep(time.Duration(rand.IntN(40)) * time.Millisecond)
		r := e.cancelRun(editor.Subject, e.a.ID, fmt.Sprintf("cancel-race-%04d", i), id)
		<-done
		run := e.getRun(t, editor.Subject, e.a.ID, id)
		results := e.count(`SELECT count(*) FROM twin.simulation_results WHERE run_id=$1`, id)
		switch {
		case r.status == 200 && run.Status == "cancelled" && results == 0:
			seen["cancelled"]++
		case r.status == 409 && code(t, r) == "LIFECYCLE_CONFLICT" && run.Status == "completed" && results == 1:
			seen["completed"]++
		default:
			t.Errorf("round %d: cancel %d %s, run %s, %d results", i, r.status, r.body, run.Status, results)
		}
	}
	t.Logf("outcomes: %v", seen)
}
