package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// H-056 design §9 (W1): with WORKER_LANES=split the build's handlers are
// split into two disjoint lanes, simulations alone in one and every other
// type (AI proposals, analyses, imports, and any type added later) in the
// other; single keeps the one loop under WORKER_ID with every handler. The
// handlers are built once: both lanes share them (one AI client and one
// spend meter).
func TestTheLanesSplitTheBuildsHandlers(t *testing.T) {
	hs, err := handlers(deps{worker: config.Worker{ID: "worker-0"}, log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	single, err := plan(config.LanesSingle, "worker-0", hs)
	if err != nil || len(single) != 1 || single[0].id != "worker-0" || !sameHandlers(single[0].handlers, hs) {
		t.Fatalf("single: %+v %v", single, err)
	}
	split, err := plan(config.LanesSplit, "worker-0", hs)
	if err != nil || len(split) != 2 {
		t.Fatalf("split: %+v %v", split, err)
	}
	sim, rest := split[0], split[1]
	if sim.id != "worker-0:sim" || !slices.Equal(types(sim.handlers), []jobs.Type{jobs.Simulation}) {
		t.Errorf("the sim lane: %s %v", sim.id, types(sim.handlers))
	}
	if want := []jobs.Type{jobs.Analysis, jobs.ImportProfile, jobs.ImportValidate, jobs.ModelProposal}; rest.id != "worker-0:rest" ||
		!slices.Equal(types(rest.handlers), want) {
		t.Errorf("the rest lane: %s %v, want %v", rest.id, types(rest.handlers), want)
	}
	// Disjoint, and together exactly the build's, each the same handler.
	union := map[jobs.Type]handler.Handler{}
	for _, l := range split {
		for typ, h := range l.handlers {
			if _, twice := union[typ]; twice {
				t.Errorf("%s is in both lanes", typ)
			}
			union[typ] = h
		}
	}
	if !sameHandlers(union, hs) {
		t.Errorf("the lanes hold %v, the build %v", types(union), types(hs))
	}
	// A type added later (an investigation's, say) goes to the rest lane;
	// only simulations have a lane of their own.
	more := maps.Clone(hs)
	more[jobs.ImportMapping] = hs[jobs.ImportProfile]
	more["investigation"] = hs[jobs.Analysis]
	split, err = plan(config.LanesSplit, "worker-0", more)
	if err != nil || !slices.Equal(types(split[0].handlers), []jobs.Type{jobs.Simulation}) ||
		!slices.Contains(types(split[1].handlers), "investigation") || !slices.Contains(types(split[1].handlers), jobs.ImportMapping) {
		t.Errorf("a later type: %+v %v", split, err)
	}
}

// Split needs both lanes: a build without a simulation handler, or with
// nothing else, cannot be split, and the worker says so instead of running
// a lane that could claim nothing.
func TestSplitNeedsBothLanes(t *testing.T) {
	noop := handler.Func(func(context.Context, handler.Run) error { return nil })
	for name, hs := range map[string]map[jobs.Type]handler.Handler{
		"no simulation handler": {jobs.ImportProfile: noop, jobs.ModelProposal: noop},
		"only simulations":      {jobs.Simulation: noop},
	} {
		if lanes, err := plan(config.LanesSplit, "worker-0", hs); err == nil || !strings.Contains(err.Error(), "WORKER_LANES") {
			t.Errorf("%s: %+v %v", name, lanes, err)
		}
		if lanes, err := plan(config.LanesSingle, "worker-0", hs); err != nil || len(lanes) != 1 {
			t.Errorf("%s, single: %+v %v", name, lanes, err)
		}
	}
}

// A lane setting the worker cannot use stops it with the variable's name,
// before any connection: an unknown mode, a WORKER_ID too long for the
// lanes' suffixes, and split on a build without both lanes.
func TestRunRefusesALaneSettingItCannotUse(t *testing.T) {
	noop := handler.Func(func(context.Context, handler.Run) error { return nil })
	for name, c := range map[string]struct {
		getenv func(string) string
		build  builder
		names  string
	}{
		"an unknown mode":           {with("WORKER_LANES", "both"), someHandler, "WORKER_LANES"},
		"a WORKER_ID too long":      {with("WORKER_LANES", "split", "WORKER_ID", strings.Repeat("w", 124)), handlers, "WORKER_ID"},
		"no simulation handler":     {with("WORKER_LANES", "split"), someHandler, "WORKER_LANES"},
		"only a simulation handler": {with("WORKER_LANES", "split"), fixed(map[jobs.Type]handler.Handler{jobs.Simulation: noop}), "WORKER_LANES"},
	} {
		var stderr bytes.Buffer
		if code := run(context.Background(), c.getenv, &stderr, c.build); code != exitConfig || !strings.Contains(stderr.String(), c.names) ||
			strings.Contains(stderr.String(), "database") || strings.Contains(stderr.String(), "db-pass-123") {
			t.Errorf("%s: exit %d: %s", name, code, stderr.String())
		}
	}
}

// Split builds both lanes' loops before the database is checked: a signal
// before the loops began is a stop (exit 0), as in single mode.
func TestSplitStopsBeforeStarting(t *testing.T) {
	var stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := run(ctx, with("WORKER_LANES", "split"), &stderr, handlers); code != 0 {
		t.Fatalf("exit = %d, want 0: %s", code, stderr.String())
	}
	if out := stderr.String(); !strings.Contains(out, "stopped before starting") || strings.Contains(out, "database unavailable") {
		t.Fatalf("stderr: %s", out)
	}
}

// fakeLoop is a lane's loop whose Run is the function itself.
type fakeLoop func(ctx context.Context)

func (f fakeLoop) Run(ctx context.Context) { f(ctx) }

// draining is a loop that runs until its context ends, then takes a while
// to finish its attempt (as worker.Run records WORKER_STOPPED) before it
// returns and marks drained.
func draining(started chan<- string, id string, drained *atomic.Bool) fakeLoop {
	return func(ctx context.Context) {
		started <- id
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		drained.Store(true)
	}
}

// startLanes runs runLanes in the background; waitLanes reads what it
// returned, failing the test if it does not return.
func startLanes(ctx context.Context, log *slog.Logger, lanes []running) <-chan bool {
	done := make(chan bool, 1)
	go func() { done <- runLanes(ctx, log, lanes) }()
	return done
}

func waitLanes(t *testing.T, done <-chan bool) bool {
	t.Helper()
	select {
	case ok := <-done:
		return ok
	case <-time.After(10 * time.Second):
		t.Fatal("the lanes did not return")
	}
	return false
}

// A stop (SIGINT or SIGTERM) ends every lane, and runLanes returns only
// once each has finished its attempt: the worker never exits with a lane
// still writing.
func TestAStopDrainsEveryLane(t *testing.T) {
	var logs syncBuffer
	log := newLogger(&logs)
	started := make(chan string, 2)
	var simDrained, restDrained atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startLanes(ctx, log, []running{{"worker-0:sim", draining(started, "sim", &simDrained)},
		{"worker-0:rest", draining(started, "rest", &restDrained)}})
	<-started
	<-started
	cancel()
	if ok := waitLanes(t, done); !ok || !simDrained.Load() || !restDrained.Load() {
		t.Errorf("ok %v, drained sim %v rest %v", ok, simDrained.Load(), restDrained.Load())
	}
	if strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("a stop was reported as a failure: %s", logs.String())
	}
}

// A lane whose loop panics (outside a handler, which the loop recovers
// itself) is reported with its lane's worker ID and stack, the other lane
// is stopped and drained, and runLanes says the worker failed, so the
// process exits non-zero. The panic's value may be data the loop worked
// on: only a runtime error's message or the value's type is logged.
func TestALanesPanicStopsTheOtherLoudly(t *testing.T) {
	for name, c := range map[string]struct {
		value any
		want  string
	}{
		"a value":         {struct{ Row string }{"CASE-SENTINEL,4242"}, "struct"},
		"a runtime error": {nil, "assignment to entry in nil map"},
	} {
		t.Run(name, func(t *testing.T) {
			var logs syncBuffer
			log := newLogger(&logs)
			started := make(chan string, 2)
			var restDrained atomic.Bool
			rest := draining(started, "rest", &restDrained)
			sim := fakeLoop(func(ctx context.Context) {
				if <-started; c.value != nil {
					panic(c.value)
				}
				var m map[string]int
				m["x"] = 1
			})
			done := startLanes(context.Background(), log, []running{{"worker-0:sim", sim}, {"worker-0:rest", rest}})
			if ok := waitLanes(t, done); ok || !restDrained.Load() {
				t.Fatalf("ok %v, the other lane drained %v: %s", ok, restDrained.Load(), logs.String())
			}
			line := laneReport(t, logs.String())
			if line["worker_id"] != "worker-0:sim" || line["code"] != "WORKER_LANE_PANICKED" || !strings.Contains(line["panic"].(string), c.want) ||
				!strings.Contains(line["stack"].(string), "lanes_test.go") {
				t.Errorf("the report: %v", line)
			}
			if strings.Contains(logs.String(), "SENTINEL") || strings.Contains(logs.String(), "4242") {
				t.Errorf("the log holds the panic's value: %s", logs.String())
			}
		})
	}
}

// A lane whose loop returns while the worker still runs (worker.Run returns
// only when its context ends, so this is a bug) is reported, and stops the
// other lane, as a panic does: one lane never runs on alone unnoticed.
func TestALaneThatEndsEarlyStopsTheOther(t *testing.T) {
	var logs syncBuffer
	log := newLogger(&logs)
	started := make(chan string, 2)
	var simDrained atomic.Bool
	sim := draining(started, "sim", &simDrained)
	rest := fakeLoop(func(context.Context) { <-started })
	done := startLanes(context.Background(), log, []running{{"worker-0:sim", sim}, {"worker-0:rest", rest}})
	if ok := waitLanes(t, done); ok || !simDrained.Load() {
		t.Fatalf("ok %v, the other lane drained %v: %s", ok, simDrained.Load(), logs.String())
	}
	if line := laneReport(t, logs.String()); line["worker_id"] != "worker-0:rest" || line["code"] != "WORKER_LANE_ENDED" {
		t.Errorf("the report: %v", line)
	}
}

// laneReport is the one line of logs that reports a lane's failure (an
// ERROR line whose code is WORKER_LANE_...).
func laneReport(t *testing.T, logs string) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(logs), "\n") {
		var line map[string]any
		if err := json.Unmarshal([]byte(l), &line); err != nil {
			t.Fatalf("%v: %s", err, l)
		}
		if code, _ := line["code"].(string); strings.HasPrefix(code, "WORKER_LANE_") {
			if line["level"] != "ERROR" {
				t.Errorf("a lane's failure is not an error: %v", line)
			}
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d lane reports: %s", len(found), logs)
	}
	return found[0]
}

func types(hs map[jobs.Type]handler.Handler) []jobs.Type { return slices.Sorted(maps.Keys(hs)) }

// sameHandlers reports whether a and b map the same types to the same
// handlers (the build's are pointers, so they compare by identity).
func sameHandlers(a, b map[jobs.Type]handler.Handler) bool {
	if !slices.Equal(types(a), types(b)) {
		return false
	}
	for typ, h := range a {
		if h != b[typ] {
			return false
		}
	}
	return true
}
