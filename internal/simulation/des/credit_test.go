package des

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/engine"
)

// batches grants batch events at a time and records each call, as the
// worker does with one durable reservation per grant (T-023, D-007).
type batches struct {
	batch, limit int64
	fail         error
	block        chan struct{} // if set, Grant waits for it or the context

	mu         sync.Mutex
	asked      []int64 // the granted total each call saw
	busy       bool
	concurrent bool
}

func (b *batches) Grant(ctx context.Context, granted int64) (int64, error) {
	b.mu.Lock()
	b.concurrent = b.concurrent || b.busy
	b.busy = true
	b.asked = append(b.asked, granted)
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.busy = false; b.mu.Unlock() }()
	if b.block != nil {
		select {
		case <-b.block:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	if b.fail != nil {
		return 0, b.fail
	}
	return min(granted+b.batch, b.limit), nil
}

func (b *batches) calls() []int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]int64(nil), b.asked...)
}

// eventsOf is how many events every replication of in processes together.
func eventsOf(t *testing.T, in simulation.RunInput) int64 {
	t.Helper()
	p, err := engine.Compile(in)
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	for i := range in.Config.Replications {
		out, err := engine.RunReplication(context.Background(), p, uint32(i), engine.NewBudget(1<<40), nil)
		if err != nil {
			t.Fatal(err)
		}
		n += out.Events
	}
	return n
}

// A budget granted in batches gives exactly the result of one granted at
// once, whatever the batch, the first grant and the worker count. Grants
// come one at a time, each asked once the last is spent, until the run's
// events are granted.
func TestCreditGivesTheSameResult(t *testing.T) {
	in := input(t, golden(t, "default-delivery.json"), 3*24*60, 60, 6, simulation.TraceSampled)
	events := eventsOf(t, in)
	want := run(t, &Engine{DomainMetrics: dora}, in)
	for _, c := range []struct {
		batch, initial int64
		workers        int
	}{{events / 7, 0, 1}, {events / 7, 0, 4}, {1000, 500, 3}, {events, 0, 2}, {events * 2, events, 4}, {1, events - 3, 2}, {3, events + 5, 2}} {
		credit := &batches{batch: c.batch, limit: in.Limits.MaxEvents}
		out, err := (&Engine{Workers: c.workers, DomainMetrics: dora}).RunWith(context.Background(), in, Options{Credit: credit, Granted: c.initial})
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		if out.Result.Reproducibility.ResultDigest != want.Result.Reproducibility.ResultDigest {
			t.Errorf("%+v: digest %s, want %s", c, out.Result.Reproducibility.ResultDigest, want.Result.Reproducibility.ResultDigest)
		}
		if out.Trace == nil || len(out.Trace.Events) != len(want.Trace.Events) {
			t.Errorf("%+v: the trace differs", c)
		}
		if out.Events != events || want.Events != events {
			t.Errorf("%+v: %d events reported (%d without credit), %d processed", c, out.Events, want.Events, events)
		}
		var wantCalls int64
		if events > c.initial {
			wantCalls = (events - c.initial + c.batch - 1) / c.batch
		}
		asked := credit.calls()
		if int64(len(asked)) != wantCalls || credit.concurrent {
			t.Errorf("%+v: %d grants (concurrent %v) for %d events, want %d", c, len(asked), credit.concurrent, events, wantCalls)
		}
		for i, g := range asked {
			if g != c.initial+int64(i)*c.batch {
				t.Errorf("%+v: grant %d asked after %d", c, i, g)
			}
		}
	}
}

// The run never spends an event beyond its budget: a credit that may grant
// up to max_events still ends one event short in ErrEventBudget, and a
// first grant outside 0..max_events is refused.
func TestCreditBoundsTheRun(t *testing.T) {
	in := input(t, golden(t, "deterministic-queue.json"), 1010, 0, 2, simulation.TraceNone)
	events := eventsOf(t, in)
	in.Limits.MaxEvents = events - 1
	credit := &batches{batch: 10, limit: in.Limits.MaxEvents}
	if _, err := (&Engine{Workers: 2}).RunWith(context.Background(), in, Options{Credit: credit, Granted: 10}); !errors.Is(err, engine.ErrEventBudget) {
		t.Fatalf("a budget one event short: %v", err)
	}
	asked := credit.calls()
	if len(asked) == 0 || asked[len(asked)-1] >= in.Limits.MaxEvents || asked[len(asked)-1]+10 < in.Limits.MaxEvents {
		t.Errorf("grants asked after %v of %d", asked, in.Limits.MaxEvents)
	}
	in.Limits.MaxEvents = 10_000_000
	for _, g := range []int64{-1, in.Limits.MaxEvents + 1} {
		if _, err := (&Engine{}).RunWith(context.Background(), in, Options{Credit: credit, Granted: g}); !errors.Is(err, ErrInvalidOptions) {
			t.Errorf("Granted %d: %v", g, err)
		}
	}
}

type grantFunc func(context.Context, int64) (int64, error)

func (f grantFunc) Grant(ctx context.Context, g int64) (int64, error) { return f(ctx, g) }

// A grant that fails, grants nothing more, goes back or goes beyond the
// budget fails the run with nothing partial.
func TestCreditFailures(t *testing.T) {
	in := input(t, golden(t, "deterministic-queue.json"), 1010, 0, 3, simulation.TraceNone)
	lost := errors.New("the reservation could not be written")
	for name, c := range map[string]struct {
		credit engine.Credit
		want   error
	}{
		"an error":         {&batches{batch: 10, limit: in.Limits.MaxEvents, fail: lost}, lost},
		"nothing more":     {grantFunc(func(_ context.Context, g int64) (int64, error) { return g, nil }), engine.ErrInternal},
		"beyond the limit": {grantFunc(func(context.Context, int64) (int64, error) { return in.Limits.MaxEvents + 1, nil }), engine.ErrInternal},
		"backwards":        {grantFunc(func(_ context.Context, g int64) (int64, error) { return g - 1, nil }), engine.ErrInternal},
	} {
		out, err := (&Engine{Workers: 3}).RunWith(context.Background(), in, Options{Credit: c.credit, Granted: 5})
		if !errors.Is(err, c.want) || out != nil {
			t.Errorf("%s: %v %v", name, out, err)
		}
	}
}

// A run waiting for credit stops when it is cancelled or its wall time
// ends: the wait is a checkpoint like any other.
func TestCreditWaitEndsWithTheRun(t *testing.T) {
	in := input(t, golden(t, "deterministic-queue.json"), 1010, 0, 2, simulation.TraceNone)
	credit := &batches{batch: 10, limit: in.Limits.MaxEvents, block: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := (&Engine{Workers: 2}).RunWith(ctx, in, Options{Credit: credit, Granted: 5})
		done <- err
	}()
	if !waitUntil(5*time.Second, func() bool { return len(credit.calls()) > 0 }) {
		t.Fatal("no grant was asked for")
	}
	cancel()
	if err := within(t, 5*time.Second, func() error { return <-done }); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled while waiting: %v", err)
	}

	e := &Engine{Workers: 2, timeout: func(simulation.ExecutionLimits) time.Duration { return 200 * time.Millisecond }}
	err := within(t, 5*time.Second, func() error {
		_, err := e.RunWith(context.Background(), in, Options{Credit: &batches{batch: 10, limit: in.Limits.MaxEvents, block: make(chan struct{})}, Granted: 5})
		return err
	})
	if !errors.Is(err, ErrWallTimeout) {
		t.Errorf("the wall time ended while waiting: %v", err)
	}
}
