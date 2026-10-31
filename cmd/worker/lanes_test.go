package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
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
	single, err := plan(config.Worker{ID: "worker-0", Lanes: config.LanesSingle}, hs)
	if err != nil || len(single) != 1 || single[0].id != "worker-0" || !sameHandlers(single[0].handlers, hs) {
		t.Fatalf("single: %+v %v", single, err)
	}
	split, err := plan(config.Worker{ID: "worker-0", Lanes: config.LanesSplit, RestLanes: 1}, hs)
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
	split, err = plan(config.Worker{ID: "worker-0", Lanes: config.LanesSplit, RestLanes: 1}, more)
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
		for n := 1; n <= config.MaxRestLanes; n++ {
			if lanes, err := plan(config.Worker{ID: "worker-0", Lanes: config.LanesSplit, RestLanes: n}, hs); err == nil || !strings.Contains(err.Error(), "WORKER_LANES") {
				t.Errorf("%s, %d rest lanes: %+v %v", name, n, lanes, err)
			}
		}
		if lanes, err := plan(config.Worker{ID: "worker-0", Lanes: config.LanesSingle}, hs); err != nil || len(lanes) != 1 {
			t.Errorf("%s, single: %+v %v", name, lanes, err)
		}
	}
}

// The plan of every WORKER_LANES and WORKER_REST_LANES setting, read as
// the worker reads it: single is the one loop under WORKER_ID with every
// handler (no rest lane, and a rest count refused); split is the sim lane,
// then WORKER_REST_LANES rest lanes (1 by default), <id>:rest, <id>:rest2,
// ..., each with every type but simulations, the same handlers in each
// (one AI client and one spend meter for all). Several lanes claiming the
// same types are safe as several workers are: jobs.Claim skips a job
// another transaction holds (FOR UPDATE SKIP LOCKED), and each lane holds
// its leases under its own name (the lease fence).
func TestThePlanOfEveryLaneSetting(t *testing.T) {
	hs, err := handlers(deps{worker: config.Worker{ID: "worker-0"}, log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	rest := maps.Clone(hs)
	delete(rest, jobs.Simulation)
	restLanes := func(n int) []string {
		ids := []string{"worker-0:sim"}
		for i := 1; i <= n; i++ {
			ids = append(ids, "worker-0:rest"+map[bool]string{true: "", false: strconv.Itoa(i)}[i == 1])
		}
		return ids
	}
	want := map[[2]string][]string{ // nil: refused at start
		{"", ""}: {"worker-0"}, {"single", ""}: {"worker-0"},
		{"split", ""}: restLanes(1), {"split", "1"}: restLanes(1), {"split", "2"}: restLanes(2), {"split", "3"}: restLanes(3), {"split", "4"}: restLanes(4),
	}
	for _, mode := range []string{"", "single", "split"} {
		for _, n := range []string{"", "1", "2", "3", "4", "0", "5"} {
			name, ids := mode+"/"+n, want[[2]string{mode, n}]
			w, err := config.LoadWorker(with("WORKER_LANES", mode, "WORKER_REST_LANES", n))
			if ids == nil {
				if err == nil || !strings.Contains(err.Error(), "WORKER_REST_LANES (invalid)") {
					t.Errorf("%s: %+v %v", name, w, err)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			lanes, err := plan(w, hs)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			var got []string
			for _, l := range lanes {
				got = append(got, l.id)
			}
			if !slices.Equal(got, ids) {
				t.Errorf("%s: lanes %v, want %v", name, got, ids)
				continue
			}
			if len(lanes) == 1 {
				if !sameHandlers(lanes[0].handlers, hs) {
					t.Errorf("%s: the one loop has %v", name, types(lanes[0].handlers))
				}
				continue
			}
			if !slices.Equal(types(lanes[0].handlers), []jobs.Type{jobs.Simulation}) || lanes[0].handlers[jobs.Simulation] != hs[jobs.Simulation] {
				t.Errorf("%s: the sim lane has %v", name, types(lanes[0].handlers))
			}
			for _, l := range lanes[1:] {
				if !sameHandlers(l.handlers, rest) {
					t.Errorf("%s: %s has %v, want %v", name, l.id, types(l.handlers), types(rest))
				}
			}
		}
	}
}

// plan never runs a lane count LoadWorker would refuse: a Worker made
// elsewhere with split and no rest lane, or more than four, is refused.
func TestThePlanRefusesARestCountOutOfRange(t *testing.T) {
	hs, err := handlers(deps{worker: config.Worker{ID: "worker-0"}, log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{-1, 0, config.MaxRestLanes + 1} {
		if lanes, err := plan(config.Worker{ID: "worker-0", Lanes: config.LanesSplit, RestLanes: n}, hs); err == nil || !strings.Contains(err.Error(), "WORKER_REST_LANES") {
			t.Errorf("%d rest lanes: %+v %v", n, lanes, err)
		}
	}
}

// H-065 review: the rest lanes share the worker's AI clients (the text
// client of proposals and analyses, and the photo path's), each of which
// lets one organization have at most MaxConcurrentPerOrganization calls in
// flight (AI_MAX_CONCURRENT_PER_ORGANIZATION, 2 by default) and refuses
// one more with concurrency_limited, which ends a Muse Wonder or research
// proposal (one attempt). Each lane runs one job at a time and a job makes
// its calls one after another, so the worker refuses more rest lanes than
// that limit, and nothing else.
func TestRestLanesFitTheAILimit(t *testing.T) {
	for limit := 1; limit <= provider.BudgetCaps.MaxConcurrentPerOrganization; limit++ {
		if err := checkRestLanes(config.Worker{Lanes: config.LanesSingle}, limit); err != nil {
			t.Errorf("single, limit %d: %v", limit, err)
		}
		for n := 1; n <= config.MaxRestLanes; n++ {
			err := checkRestLanes(config.Worker{Lanes: config.LanesSplit, RestLanes: n}, limit)
			if n <= limit && err != nil {
				t.Errorf("%d rest lanes, limit %d: %v", n, limit, err)
			}
			var ce *config.Error
			if n > limit && (!errors.As(err, &ce) || ce.Var != "WORKER_REST_LANES" || ce.Code != config.CodeInvalid ||
				!strings.Contains(ce.Msg, "AI_MAX_CONCURRENT_PER_ORGANIZATION") || !strings.Contains(ce.Msg, "at most "+strconv.Itoa(limit))) {
				t.Errorf("%d rest lanes, limit %d: %v", n, limit, err)
			}
		}
	}
	if got := aiBudget(config.AIBudget{}).MaxConcurrentPerOrganization; got != 2 {
		t.Errorf("the default limit is %d, this test and deploy/railway/variables.json assume 2", got)
	}
}

// A lane setting the worker cannot use stops it with the variable's name,
// before any connection: an unknown mode, a WORKER_ID too long for the
// lanes' suffixes, split on a build without both lanes, a rest count
// without split or out of range, and more rest lanes than the AI calls one
// organization may have in flight.
func TestRunRefusesALaneSettingItCannotUse(t *testing.T) {
	noop := handler.Func(func(context.Context, handler.Run) error { return nil })
	for name, c := range map[string]struct {
		getenv func(string) string
		build  builder
		names  string
	}{
		"an unknown mode":            {with("WORKER_LANES", "both"), someHandler, "WORKER_LANES"},
		"a WORKER_ID too long":       {with("WORKER_LANES", "split", "WORKER_ID", strings.Repeat("w", 124)), handlers, "WORKER_ID"},
		"no simulation handler":      {with("WORKER_LANES", "split"), someHandler, "WORKER_LANES"},
		"only a simulation handler":  {with("WORKER_LANES", "split"), fixed(map[jobs.Type]handler.Handler{jobs.Simulation: noop}), "WORKER_LANES"},
		"a rest count without split": {with("WORKER_REST_LANES", "2"), handlers, "WORKER_REST_LANES"},
		"five rest lanes":            {with("WORKER_LANES", "split", "WORKER_REST_LANES", "5"), handlers, "WORKER_REST_LANES"},
		"a WORKER_ID too long for :rest2": {with("WORKER_LANES", "split", "WORKER_REST_LANES", "2", "WORKER_ID", strings.Repeat("w", 123)),
			handlers, "WORKER_ID"},
		"three rest lanes over the default limit": {with("WORKER_LANES", "split", "WORKER_REST_LANES", "3"), handlers, "AI_MAX_CONCURRENT_PER_ORGANIZATION"},
		"two rest lanes over a limit of 1": {with("WORKER_LANES", "split", "WORKER_REST_LANES", "2", "AI_MAX_CONCURRENT_PER_ORGANIZATION", "1"),
			handlers, "AI_MAX_CONCURRENT_PER_ORGANIZATION"},
		"four rest lanes over a limit of 3": {with("WORKER_LANES", "split", "WORKER_REST_LANES", "4", "AI_MAX_CONCURRENT_PER_ORGANIZATION", "3"),
			handlers, "WORKER_REST_LANES (invalid): at most 3"},
	} {
		var stderr bytes.Buffer
		if code := run(context.Background(), c.getenv, &stderr, c.build); code != exitConfig || !strings.Contains(stderr.String(), c.names) ||
			strings.Contains(stderr.String(), "database") || strings.Contains(stderr.String(), "db-pass-123") {
			t.Errorf("%s: exit %d: %s", name, code, stderr.String())
		}
	}
}

// Within the limit, every rest lane's loop is built before the database is
// checked, as with one: a signal before the start is a stop (exit 0).
func TestRestLanesWithinTheAILimitStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, getenv := range map[string]func(string) string{
		"two, the default limit": with("WORKER_LANES", "split", "WORKER_REST_LANES", "2"),
		"three, a limit of 3":    with("WORKER_LANES", "split", "WORKER_REST_LANES", "3", "AI_MAX_CONCURRENT_PER_ORGANIZATION", "3"),
		"four, a limit of 16":    with("WORKER_LANES", "split", "WORKER_REST_LANES", "4", "AI_MAX_CONCURRENT_PER_ORGANIZATION", "16"),
		"one, a limit of 1":      with("WORKER_LANES", "split", "AI_MAX_CONCURRENT_PER_ORGANIZATION", "1"),
		"single, a limit of 1":   with("AI_MAX_CONCURRENT_PER_ORGANIZATION", "1"),
	} {
		var stderr bytes.Buffer
		if code := run(ctx, getenv, &stderr, handlers); code != 0 {
			t.Errorf("%s: exit = %d, want 0: %s", name, code, stderr.String())
		} else if out := stderr.String(); !strings.Contains(out, "stopped before starting") || strings.Contains(out, "database unavailable") {
			t.Errorf("%s: stderr: %s", name, out)
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

// With every rest lane WORKER_REST_LANES allows (the sim lane and four rest
// lanes), a stop still ends every lane, and runLanes returns only once each
// has finished its attempt.
func TestAStopDrainsEveryRestLane(t *testing.T) {
	ids := []string{"worker-0:sim", "worker-0:rest", "worker-0:rest2", "worker-0:rest3", "worker-0:rest4"}
	var logs syncBuffer
	log := newLogger(&logs)
	started := make(chan string, len(ids))
	drained := make([]atomic.Bool, len(ids))
	lanes := make([]running, len(ids))
	for i, id := range ids {
		lanes[i] = running{id, draining(started, id, &drained[i])}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startLanes(ctx, log, lanes)
	for range ids {
		<-started
	}
	cancel()
	ok := waitLanes(t, done)
	for i, id := range ids {
		if !drained[i].Load() {
			t.Errorf("%s returned before it drained", id)
		}
	}
	if !ok || strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("ok %v: %s", ok, logs.String())
	}
}

// A rest lane's panic (not only the sim lane's) stops every other lane,
// each drained, and is reported once, with that lane's name.
func TestARestLanesPanicStopsEveryOtherLane(t *testing.T) {
	var logs syncBuffer
	log := newLogger(&logs)
	others := []string{"worker-0:sim", "worker-0:rest", "worker-0:rest3"}
	started := make(chan string, len(others))
	drained := make([]atomic.Bool, len(others))
	var lanes []running
	for i, id := range others {
		lanes = append(lanes, running{id, draining(started, id, &drained[i])})
	}
	lanes = slices.Insert(lanes, 2, running{"worker-0:rest2", fakeLoop(func(context.Context) {
		for range others {
			<-started
		}
		panic(struct{ Row string }{"CASE-SENTINEL,4242"})
	})})
	if ok := waitLanes(t, startLanes(context.Background(), log, lanes)); ok {
		t.Fatalf("a panic was not a failure: %s", logs.String())
	}
	for i, id := range others {
		if !drained[i].Load() {
			t.Errorf("%s was not drained", id)
		}
	}
	if line := laneReport(t, logs.String()); line["worker_id"] != "worker-0:rest2" || line["code"] != "WORKER_LANE_PANICKED" {
		t.Errorf("the report: %v", line)
	}
	if strings.Contains(logs.String(), "SENTINEL") {
		t.Errorf("the log holds the panic's value: %s", logs.String())
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
