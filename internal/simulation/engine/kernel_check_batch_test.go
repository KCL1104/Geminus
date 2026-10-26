package engine

// The observation-only model of batch processes (ADR-026 D-054, D-048,
// D-049, D-050; T-050 interpretations D-096, PROPOSED), part of bchecker
// (kernel_check_blocking_test.go). From the IR alone it keeps each batch
// process's buffer in entry order, makes the batch request itself by the
// rule (max_size waiting, or min_size once the oldest has waited max_wait;
// at most one ungranted), puts it in the literal FIFO with every other
// request, and checks against the observations:
//   - a batch is granted only as the literal resource-wise FIFO fixpoint
//     says, takes min(waiting, max_size) members, the oldest first:
//     ObsResource, BatchStart (members, number), each member's
//     ServiceStart (with the number), and as many blocked entities move in
//     as places it freed; no tick ends with a ready batch that fits;
//   - a batch ends in one event: no member blocked, the bundle's release
//     first, then each member's ServiceEnd and route; some blocked, no
//     release, the bundle is released by the last of them moving in (and
//     only then);
//   - a constant service time is one draw for the whole batch;
//   - a tick with no observation at all (a BATCH_TIMEOUT that readies
//     nothing, or readies a batch that does not fit) is still a tick:
//     nothing is grantable at its end and D-050 B2 counts it (silentTicks);
//   - D-050 B1 waits for the BATCH_TIMEOUTs still to come, one after the
//     horizon included.
// It never reads the kernel's batch state.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
)

// batchKey is the waiting-request key of process p's batch request: a
// batch request has no entity, and there is at most one per process.
func batchKey(p int) uint64 { return 1<<63 | uint64(p) }

func isBatchKey(e uint64) bool { return e&(1<<63) != 0 }

type bwaiter struct {
	entity uint64
	enter  int64
	serial uint64
}

// btimeout is a BATCH_TIMEOUT the kernel schedules: its tick, process and
// the serial of the oldest member it was scheduled for (live while that
// member is still the oldest).
type btimeout struct {
	tick   int64
	p      int
	serial uint64
}

type brun struct {
	members []uint64
	blocked int
	start   int64
}

type batCheck struct {
	min, max []int   // per node, 0 for a process without a batch
	wait     []int64 // ticks
	timed    []bool  // min < max: a wait limit can start a batch
	buf      [][]bwaiter
	pend     []bool
	number   []uint64
	oldest   []uint64
	serial   uint64
	runs     map[[2]uint64]*brun // (process, number)
	memberOf map[uint64]uint64   // entity in service or blocked -> its batch number
	timeouts []btimeout          // every BATCH_TIMEOUT the kernel schedules, in that order, stale ones included
	swept    int64               // the last tick whose priority-15 point was passed
	service  []int64             // per batch process: a constant service time in ticks, -1 otherwise
	stats    struct{ batches, members, readyAtEntry, readyAtLimit, timeouts, silent, blockedMembers int }
}

func newBatCheck(p *Program, ir *model.ModelIR) *batCheck {
	n := len(p.nodes)
	b := &batCheck{min: make([]int, n), max: make([]int, n), wait: make([]int64, n), timed: make([]bool, n), buf: make([][]bwaiter, n),
		pend: make([]bool, n), number: make([]uint64, n), oldest: make([]uint64, n), runs: map[[2]uint64]*brun{},
		memberOf: map[uint64]uint64{}, swept: -1, service: make([]int64, n)}
	any := false
	for i, nd := range p.nodes {
		b.service[i] = -1
		for _, x := range ir.Nodes {
			if x.ID != nd.id || x.Batch == nil {
				continue
			}
			any = true
			b.min[i], b.max[i], b.timed[i] = x.Batch.MinSize, x.Batch.MaxSize, x.Batch.MinSize < x.Batch.MaxSize
			if x.Batch.MaxWait != nil {
				b.wait[i], _ = model.Ticks(*x.Batch.MaxWait, model.TimeUnit(ir.TimeUnit))
			}
			if d := x.ServiceTime.Distribution; d.Type == model.DistConstant {
				b.service[i], _ = model.Ticks(*d.Value, model.TimeUnit(ir.TimeUnit))
			}
		}
	}
	if !any {
		return nil
	}
	return b
}

func (c *bchecker) isBatch(p int) bool { return c.bat != nil && c.bat.max[p] > 0 }

// batchEnter puts entity e in batch process p's buffer (an entry always
// checks the batch, D-054).
func (c *bchecker) batchEnter(e uint64, p int) {
	b := c.bat
	b.serial++
	b.buf[p] = append(b.buf[p], bwaiter{e, c.lastTick, b.serial})
	c.occupied[p]++
	c.state[e] = "queue"
	c.batchCheck(p, true)
}

// batchCheck follows a change to p's buffer: when its oldest member
// changed, the kernel schedules that member's wait limit unless it has
// passed (D-096 (c)); then the batch is checked.
func (c *bchecker) batchCheck(p int, entry bool) {
	b := c.bat
	serial := uint64(0)
	if len(b.buf[p]) > 0 {
		serial = b.buf[p][0].serial
	}
	if serial != b.oldest[p] {
		b.oldest[p] = serial
		if serial != 0 && b.timed[p] && b.buf[p][0].enter+b.wait[p] > c.lastTick {
			b.timeouts = append(b.timeouts, btimeout{b.buf[p][0].enter + b.wait[p], p, serial})
			b.stats.timeouts++
		}
	}
	if c.batchReady(p) {
		if entry {
			b.stats.readyAtEntry++
		}
	}
}

// batchReady makes p's batch request when the batch is ready by D-054's
// rule and none is waiting: it joins the literal FIFO now.
func (c *bchecker) batchReady(p int) bool {
	b := c.bat
	n := len(b.buf[p])
	if b.pend[p] || n == 0 {
		return false
	}
	if !(n >= b.max[p] || (b.timed[p] && n >= b.min[p] && c.lastTick-b.buf[p][0].enter >= b.wait[p])) {
		return false
	}
	b.pend[p] = true
	delete(c.cal.since, batchKey(p)) // a new request: B2's since is its own
	c.waiting = append(c.waiting, wreq{batchKey(p), p})
	return true
}

// sweep is the priority-15 point of tick t (D-048): the BATCH_TIMEOUTs
// due now fire in the order they were scheduled, before the tick's
// dispatch; a live one (its member is still the oldest) checks its batch
// and a stale one does nothing (D-096 (c)). Nothing at priority 15 or
// later in a tick makes a request but these, so the point is the tick's
// first priority-15 or dispatch group, or its end.
func (c *bchecker) sweep(t int64) {
	b := c.bat
	if b == nil || b.swept >= t {
		return
	}
	b.swept = t
	for _, x := range b.timeouts {
		if x.tick == t && b.oldest[x.p] == x.serial && c.batchReady(x.p) {
			b.stats.readyAtLimit++
		}
	}
}

// batchBoundary checks, at a tick's end, that every batch D-054's rule
// calls ready has its request: none is missed between the checks.
func (c *bchecker) batchBoundary() {
	b := c.bat
	if b == nil {
		return
	}
	for p, list := range b.buf {
		n := len(list)
		if b.max[p] == 0 || b.pend[p] || n == 0 {
			continue
		}
		if n >= b.max[p] || (b.timed[p] && n >= b.min[p] && c.lastTick-list[0].enter >= b.wait[p]) {
			c.fail("tick %d ended with a ready batch of %d at %s (oldest since %d) and no request", c.lastTick, n, c.p.nodes[p].id, list[0].enter)
		}
	}
}

// pendingTimeouts counts the BATCH_TIMEOUTs after the current tick (D-050
// B1 waits for them; the ones after the horizon never fire).
func (c *bchecker) pendingTimeouts() int {
	if c.bat == nil {
		return 0
	}
	n := 0
	for _, x := range c.bat.timeouts {
		if x.tick > c.lastTick {
			n++
		}
	}
	return n
}

// silentTicks runs, before a group at tick next, the ticks in between at
// which a BATCH_TIMEOUT fired without any observation: each is a tick end
// (nothing grantable, B2's state recorded). With through, tick next itself
// is run up to its end as well (an error reported at a silent tick).
func (c *bchecker) silentTicks(next int64, through bool) {
	if c.bat == nil {
		return
	}
	for {
		t := int64(-1)
		for _, x := range c.bat.timeouts {
			if x.tick > c.lastTick && (x.tick < next || (through && x.tick == next)) && x.tick <= c.p.horizon && (t < 0 || x.tick < t) {
				t = x.tick
			}
		}
		if t < 0 {
			return
		}
		c.lastTick = t
		c.sweep(t)
		c.bat.stats.silent++
		if t == next {
			return // its end is the caller's
		}
		c.tickBoundary()
	}
}

// batchGrant replays the grant of p's batch request (its bundle's levels
// are read already) and compares it with the dispatch group.
func (c *bchecker) batchGrant(u *cursor, p int) {
	b := c.bat
	b.pend[p] = false
	delete(c.cal.since, batchKey(p))
	k := min(len(b.buf[p]), b.max[p])
	b.number[p]++
	num := b.number[p]
	start, ok := u.next(fmt.Sprintf("BatchStart %d of %s with %d members", num, c.p.nodes[p].id, k), func(o Observation) bool {
		return o.Kind == ObsBatchStart && o.Node == p && o.Value == int64(k) && o.Batch == num
	})
	if !ok || k == 0 {
		return
	}
	run := &brun{start: start.Tick}
	for _, m := range b.buf[p][:k] {
		u.next(fmt.Sprintf("ServiceStart of member %d of batch %d at %s", m.entity, num, c.p.nodes[p].id), func(o Observation) bool {
			return o.Kind == ObsServiceStart && o.Entity == m.entity && o.Node == p && o.Batch == num
		})
		run.members = append(run.members, m.entity)
		c.state[m.entity], c.where[m.entity] = "service", p
		b.memberOf[m.entity] = num
	}
	b.buf[p] = b.buf[p][k:]
	b.runs[[2]uint64{uint64(p), num}] = run
	c.occupied[p] -= k
	c.nService++
	b.stats.batches++
	b.stats.members += k
	// D-049, D-096 (d): as many blocked entities move in as places the
	// members left.
	for range k {
		if len(c.blocked[p]) == 0 {
			break
		}
		c.unblockOne(u, p)
	}
	c.batchCheck(p, false)
}

// batchEnd replays the end of a batch (one SERVICE_COMPLETE): the
// release when no member is blocked, then each member's ServiceEnd and
// route, in FIFO order.
func (c *bchecker) batchEnd(u *cursor, end Observation) {
	b := c.bat
	p, num := end.Node, end.Batch
	key := [2]uint64{uint64(p), num}
	run := b.runs[key]
	if run == nil {
		c.fail("tick %d: the end of an unknown batch %d at %s", end.Tick, num, c.p.nodes[p].id)
		u.at = len(u.g)
		return
	}
	if s := b.service[p]; s >= 0 && end.Tick-run.start != s {
		c.fail("tick %d: batch %d of %s served %d ticks, its constant service is %d", end.Tick, num, c.p.nodes[p].id, end.Tick-run.start, s)
	}
	c.nService--
	blocked := slices.ContainsFunc(u.g, func(o Observation) bool { return o.Kind == ObsBlocked && o.Batch == num && o.Node == p })
	if !blocked {
		c.release(u, p, -1)
		delete(b.runs, key)
	}
	for _, m := range run.members {
		if _, ok := u.next(fmt.Sprintf("ServiceEnd of member %d of batch %d", m, num), func(o Observation) bool {
			return o.Kind == ObsServiceEnd && o.Entity == m && o.Node == p && o.Batch == num
		}); !ok {
			return
		}
		c.state[m] = "route"
		c.route(u, m, p)
		if c.state[m] != "blocked" {
			delete(b.memberOf, m)
		}
	}
	if blocked && run.blocked == 0 {
		c.fail("tick %d: batch %d of %s kept its bundle with no member blocked", end.Tick, num, c.p.nodes[p].id)
	}
}

// unblockOne moves the first entity blocked by p's buffer in: its
// departing process's bundle is released (a batch's only with its last
// blocked member), Unblocked, QueueEnter at the end of p's buffer.
func (c *bchecker) unblockOne(u *cursor, p int) {
	bl := c.blocked[p][0]
	c.blocked[p] = c.blocked[p][1:]
	if bl.batch != 0 {
		key := [2]uint64{uint64(bl.from), bl.batch}
		run := c.bat.runs[key]
		if run.blocked--; run.blocked == 0 {
			c.release(u, bl.from, -1)
			delete(c.bat.runs, key)
		}
		delete(c.bat.memberOf, bl.entity)
	} else if c.p.nodes[bl.from].typ == model.NodeProcess {
		c.suFree(bl.entity, bl.from) // its unit of a resource with setups (D-055)
		c.release(u, bl.from, -1)
	}
	u.next(fmt.Sprintf("unblocked %d at %d", bl.entity, bl.from), func(o Observation) bool {
		return o.Kind == ObsUnblocked && o.Entity == bl.entity && o.Node == bl.from && o.Batch == bl.batch
	})
	u.next(fmt.Sprintf("queue enter of %d at %d via %d", bl.entity, p, bl.at), func(o Observation) bool {
		return o.Kind == ObsQueueEnter && o.Entity == bl.entity && o.Node == p && o.At == bl.at
	})
	c.enter(bl.entity, p)
	c.stats.unblocks++
}

// batchStalled maps an ErrStalled's entity to the request it names: the
// batch request of the buffer whose oldest member it is (D-096 (g)).
func (c *bchecker) batchStalled(e uint64) uint64 {
	if c.bat == nil {
		return e
	}
	for p, list := range c.bat.buf {
		if c.bat.pend[p] && len(list) > 0 && list[0].entity == e {
			return batchKey(p)
		}
	}
	return e
}

// withBatches makes about half the processes of ir batch processes, at
// least one when ir has a process: min 1-3, max min-5, a wait limit
// (sometimes 0) when min < max and now and then when min = max; a finite
// buffer in front is raised to max_size.
func withBatches(ir model.ModelIR, rng *rand.Rand) (model.ModelIR, int) {
	ir.Nodes = slices.Clone(ir.Nodes)
	feeds := map[string]int{} // process ID -> index of the queue in front
	var processes, picked []int
	for i, x := range ir.Nodes {
		switch x.Type {
		case model.NodeQueue:
			for _, f := range ir.Flows {
				if f.From == x.ID {
					feeds[f.To] = i
				}
			}
		case model.NodeProcess:
			processes = append(processes, i)
			if rng.IntN(2) == 1 {
				picked = append(picked, i)
			}
		}
	}
	if len(picked) == 0 && len(processes) > 0 {
		picked = append(picked, processes[rng.IntN(len(processes))])
	}
	for _, i := range picked {
		x := &ir.Nodes[i]
		b := &model.Batch{MinSize: 1 + rng.IntN(3)}
		b.MaxSize = b.MinSize + rng.IntN(4)
		if b.MinSize < b.MaxSize || rng.IntN(4) == 0 {
			w := []float64{0, 0.5, 1, 2, 3.25, 5, 10}[rng.IntN(7)]
			b.MaxWait = &w
		}
		x.Batch = b
		if q, ok := feeds[x.ID]; ok && ir.Nodes[q].Capacity > 0 && ir.Nodes[q].Capacity < b.MaxSize {
			ir.Nodes[q].Capacity = b.MaxSize
		}
	}
	return ir, len(picked)
}

// randomBatchModel is a random model with batch processes (on all but a
// few seeds), finite buffers (seed%3 != 1, when the model has a queue and
// is valid), breakdowns with constant
// clocks (seed%4 == 1) and calendars (seed%5 == 2), and its horizon in
// minutes.
func randomBatchModel(base model.ModelIR, seed int, stream uint64) (model.ModelIR, float64, bool) {
	rng := rand.New(rand.NewPCG(uint64(seed), stream))
	ir := randomModelFrom(base, rng)
	horizon := float64(120 + rng.IntN(600))
	if seed%3 != 1 {
		if finite, ok := withFiniteBuffers(ir, rng); ok {
			ir = finite
		}
	}
	if seed%4 == 1 {
		ir, _ = withBreakdowns(ir, rng, true, true)
	}
	if seed%5 == 2 {
		ir, _ = withCalendars(ir, rng, horizon)
	}
	ir, n := withBatches(ir, rng)
	return ir, horizon, n > 0
}

// The kernel agrees with the observation-only model on random valid
// models with batch processes (some with finite buffers, breakdowns and
// calendars): no valid model deadlocks or stalls (D-050), and the same
// replication runs identically in parallel.
func TestRandomBatchModelsAgainstSpec(t *testing.T) {
	const n = 3000
	base := minimal(t)
	compiled, finite, broken, calendared := 0, 0, 0, 0
	var total batCheck
	unblocks := 0
	for seed := range n {
		ir, horizon, ok := randomBatchModel(base, seed, 5001)
		if !ok {
			continue
		}
		in := input(ir)
		in.Config = config(horizon, model.Minute)
		in.Config.Replications = 3
		p, err := Compile(in)
		if err != nil {
			continue // R1, R2, R4 or another rule
		}
		compiled++
		if p.HasFiniteBuffers() {
			finite++
		}
		if p.HasBreakdowns() {
			broken++
		}
		if p.HasCalendars() {
			calendared++
		}
		c := newBChecker(p, &ir, uint32(seed%3))
		_, err = RunReplication(context.Background(), p, uint32(seed%3), NewBudget(400_000), c)
		if errors.Is(err, ErrDeadlock) || errors.Is(err, ErrStalled) {
			t.Fatalf("seed %d: a valid model failed: %v", seed, err)
		}
		if err != nil && !errors.Is(err, ErrEventBudget) && !errors.Is(err, ErrEntityVisits) && !errors.Is(err, ErrActiveEntities) {
			t.Fatalf("seed %d: %v", seed, err)
		}
		c.finish()
		if len(c.errs) > 0 {
			t.Fatalf("seed %d: %d problems, first: %v", seed, len(c.errs), c.errs)
		}
		s := c.bat.stats
		total.stats.batches += s.batches
		total.stats.members += s.members
		total.stats.readyAtEntry += s.readyAtEntry
		total.stats.readyAtLimit += s.readyAtLimit
		total.stats.timeouts += s.timeouts
		total.stats.silent += s.silent
		total.stats.blockedMembers += s.blockedMembers
		unblocks += c.stats.unblocks
		if seed%7 == 0 {
			res := make([][]Observation, 3)
			done := make(chan int, 3)
			for i := range res {
				go func() {
					col := &collect{}
					RunReplication(context.Background(), p, uint32(seed%3), NewBudget(400_000), col)
					res[i] = col.all
					done <- i
				}()
			}
			for range res {
				<-done
			}
			for i := 1; i < len(res); i++ {
				if !slices.Equal(res[0], res[i]) {
					t.Fatalf("seed %d: concurrent run %d differs", seed, i)
				}
			}
		}
	}
	t.Logf("%d of %d random models compiled: %d with finite buffers, %d with breakdowns, %d with calendars; %d batches of %d members (%d ready at an entry or a grant, %d at a wait limit), %d BATCH_TIMEOUTs, %d silent ticks, %d members blocked, %d unblocks",
		compiled, n, finite, broken, calendared, total.stats.batches, total.stats.members, total.stats.readyAtEntry, total.stats.readyAtLimit,
		total.stats.timeouts, total.stats.silent, total.stats.blockedMembers, unblocks)
	if compiled < n/5 || finite < n/20 || broken < n/20 || calendared < n/50 || total.stats.batches < 50_000 || total.stats.readyAtLimit < 1000 ||
		total.stats.silent < 1000 || total.stats.blockedMembers < 1000 {
		t.Fatalf("generator too weak: %d compiled, %d finite, %d with breakdowns, %d with calendars, %+v", compiled, finite, broken, calendared, total.stats)
	}
}

// With the validator bypassed (R1, R2 and R4 refuse such models), random
// batch models may deadlock or stall: every ErrDeadlock is a real
// circular wait at that tick (B1 waits for the BATCH_TIMEOUTs to come)
// and every ErrStalled names a request in B2's state, a batch request by
// its oldest member; no run passes a tick that ended in either.
func TestRandomBatchFailuresAreReal(t *testing.T) {
	const n = 3000
	base := minimal(t)
	deadlocks, stalls, runs := 0, 0, 0
	for seed := range n {
		rng := rand.New(rand.NewPCG(uint64(seed), 5002))
		ir := randomModelFrom(base, rng)
		if _, err := Compile(input(ir)); err != nil {
			continue
		}
		horizon := float64(240 + rng.IntN(1200))
		ir.Nodes = slices.Clone(ir.Nodes)
		if seed%2 == 0 {
			for i := range ir.Nodes {
				if ir.Nodes[i].Type == model.NodeQueue {
					ir.Nodes[i].Capacity = 1 + rng.IntN(3)
				}
			}
		} else {
			ir, _ = withBreakdowns(ir, rng, seed%4 == 1, true)
			ir, _ = withCalendars(ir, rng, horizon)
		}
		ir, k := withBatches(ir, rng)
		if k == 0 {
			continue
		}
		in := input(ir)
		in.Config = config(horizon, model.Minute)
		in.Config.Replications = 2
		var p *Program
		var err error
		withoutValidator(t, func() { p, err = Compile(in) })
		if err != nil {
			continue // a shape the compiler refuses on its own
		}
		runs++
		c := newBChecker(p, &ir, uint32(seed%2))
		_, err = RunReplication(context.Background(), p, uint32(seed%2), NewBudget(400_000), c)
		var se *StalledError
		var de *DeadlockError
		switch {
		case errors.As(err, &se):
			stalls++
			if len(c.group) > 0 {
				c.finishGroup()
			}
			if problem := c.stallProblem(se, findEntity(t, c, se.Entity)); problem != "" {
				t.Fatalf("seed %d: ErrStalled %+v: %s (%v)", seed, *se, problem, c.errs)
			}
		case errors.As(err, &de):
			deadlocks++
			if len(c.group) > 0 {
				c.finishGroup()
			}
			if de.Tick > c.lastTick {
				c.tickBoundary()
				c.silentTicks(de.Tick, true)
			}
			if !c.deadlocked() || de.Tick != c.lastTick {
				t.Fatalf("seed %d: ErrDeadlock at %d without a circular wait in the reconstruction (%v)", seed, de.Tick, c.errs)
			}
		case err == nil || errors.Is(err, ErrEventBudget) || errors.Is(err, ErrEntityVisits) || errors.Is(err, ErrActiveEntities):
			c.finish()
		default:
			t.Fatalf("seed %d: %v", seed, err)
		}
		if len(c.errs) > 0 {
			t.Fatalf("seed %d: %d problems, first: %v", seed, len(c.errs), c.errs)
		}
	}
	t.Logf("%d runs with the validator bypassed, %d ended in ErrDeadlock, %d in ErrStalled", runs, deadlocks, stalls)
	if deadlocks < 50 || stalls < 20 || runs-deadlocks-stalls < 100 {
		t.Fatalf("too weak: %d runs, %d deadlocks, %d stalls", runs, deadlocks, stalls)
	}
}

// FuzzBatches (T-050): a random batch model (the generator's seed),
// validated in full (no run may deadlock or stall) or, with bypass,
// compiled past the validator (failures must be real); every run must
// agree with the observation-only model.
func FuzzBatches(f *testing.F) {
	for _, s := range []struct {
		seed   uint64
		bypass bool
	}{{0, false}, {3, false}, {6, false}, {10, true}, {12, true}, {4242, false}} {
		f.Add(s.seed, s.bypass)
	}
	base := minimal(f)
	f.Fuzz(func(t *testing.T, seed uint64, bypass bool) {
		ir, horizon, ok := randomBatchModel(base, int(seed%(1<<31)), 5003)
		if !ok {
			return
		}
		in := input(ir)
		in.Config = config(horizon, model.Minute)
		in.Config.Replications = 2
		var p *Program
		var err error
		if bypass {
			withoutValidator(t, func() { p, err = Compile(in) })
		} else {
			p, err = Compile(in)
		}
		if err != nil {
			return
		}
		c := newBChecker(p, &ir, uint32(seed%2))
		_, err = RunReplication(context.Background(), p, uint32(seed%2), NewBudget(100_000), c)
		var se *StalledError
		var de *DeadlockError
		switch {
		case errors.As(err, &se):
			if !bypass {
				t.Fatalf("a valid model stalled: %v", err)
			}
			if len(c.group) > 0 {
				c.finishGroup()
			}
			if problem := c.stallProblem(se, findEntity(t, c, se.Entity)); problem != "" {
				t.Fatalf("ErrStalled %+v: %s (%v)", *se, problem, c.errs)
			}
		case errors.As(err, &de):
			if !bypass {
				t.Fatalf("a valid model deadlocked: %v", err)
			}
			if len(c.group) > 0 {
				c.finishGroup()
			}
			if de.Tick > c.lastTick {
				c.tickBoundary()
				c.silentTicks(de.Tick, true)
			}
			if !c.deadlocked() {
				t.Fatalf("ErrDeadlock without a circular wait: %v", err)
			}
		case err == nil || errors.Is(err, ErrEventBudget) || errors.Is(err, ErrEntityVisits) || errors.Is(err, ErrActiveEntities):
			c.finish()
		default:
			t.Fatal(err)
		}
		if len(c.errs) > 0 {
			t.Fatalf("%d problems, first: %v", len(c.errs), c.errs)
		}
	})
}
