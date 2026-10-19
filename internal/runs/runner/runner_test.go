package runner

// DB-backed tests of the simulation job handler through the job loop as
// the plain worker login (runs created through internal/runs as the plain
// API login): budgets across retries (D-007), a crashed attempt and the
// reaper, a lost lease at the finish, a cancellation after the last
// checkpoint (TEST-019, TEST-020, TEST-021).

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/runs"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/des"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	store "github.com/kcl1104/geminus/internal/storage/runs"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
	"github.com/kcl1104/geminus/internal/worker"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

var fast = jobs.Policy{Lease: 3 * time.Second, MaxAttempts: 3, BackoffBase: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond}

type env struct {
	admin   *postgres.DB
	org     dbtest.Org
	runs    *runs.Service
	scope   *workerscope.Worker
	objects *objects.FS
	packs   *domainpack.Registry
	log     *slog.Logger
}

func setup(t *testing.T, limits simulation.ExecutionLimits) *env {
	t.Helper()
	admin := dbtest.Admin(t)
	org := dbtest.NewOrg(t, admin, "https://test.invalid", tenant.Owner, tenant.Editor)
	fs, err := objects.NewFS(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	packs, err := domainpack.NewRegistry(softwaredelivery.Pack{})
	if err != nil {
		t.Fatal(err)
	}
	api := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 4, true)
	wpool := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.WorkerLogin), 8, true)
	if err := tenant.CheckRuntimeRole(context.Background(), wpool, tenant.WorkerRole); err != nil {
		t.Fatal(err)
	}
	scope, err := workerscope.New(wpool, []string{org.ID})
	if err != nil {
		t.Fatal(err)
	}
	return &env{admin: postgres.OpenPool(admin), org: org, scope: scope, objects: fs, packs: packs,
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		runs: &runs.Service{DB: tenant.New(api), Packs: packs, Objects: fs, Limits: limits, Policy: fast}}
}

// owner runs fn as the schema owner in the organization's scope.
func (e *env) owner(t *testing.T, fn func(ctx context.Context, tx pgx.Tx) error) {
	t.Helper()
	if err := e.admin.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,true)`, e.org.ID); err != nil {
			return err
		}
		return fn(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
}

// version commits a model whose version 1 is the golden model name.
func (e *env) version(t *testing.T, name string) string {
	t.Helper()
	var ir map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "golden-models", name)), &ir); err != nil {
		t.Fatal(err)
	}
	prj, mdl, mv := ids.New("prj"), ids.New("mdl"), ids.New("mv")
	ir["model_id"] = mdl
	raw, _ := json.Marshal(ir)
	canonical, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	user := e.org.Members[tenant.Owner].UserID
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, []any{prj, e.org.ID, user}},
			{`INSERT INTO twin.models(id,organization_id,project_id,name,domain,created_by) VALUES ($1,$2,$3,'M','software_delivery',$4)`, []any{mdl, e.org.ID, prj, user}},
			{`INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,ir,hash,created_by) VALUES ($1,$2,$3,1,'0.1',$4,$5,$6)`,
				[]any{mv, e.org.ID, mdl, string(canonical), hashing.Sum(canonical), user}},
			{`UPDATE twin.models SET latest_version_id=$1 WHERE id=$2`, []any{mv, mdl}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	return mv
}

var week = simulation.RunConfig{Horizon: simulation.Duration{Value: 7, Unit: model.Day}, Warmup: simulation.Duration{Value: 1, Unit: model.Day},
	Replications: 4, Seed: 11, TraceLevel: simulation.TraceNone}

// create queues a run of version mv with config as the organization's editor.
func (e *env) create(t *testing.T, mv string, config simulation.RunConfig) string {
	t.Helper()
	req := runs.CreateRequest{ModelVersionID: mv, Name: "run", ScenarioPatch: []byte("[]"), Config: config}
	body, _ := json.Marshal(req)
	editor := e.org.Members[tenant.Editor]
	status, out, err := e.runs.Create(context.Background(), tenant.Scope{OrganizationID: e.org.ID, UserID: editor.UserID}, ids.New("req"), ids.New("key"), body, req)
	if err != nil || status != 202 {
		t.Fatalf("create: %d %v", status, err)
	}
	var accepted runs.Accepted
	if err := json.Unmarshal(out, &accepted); err != nil {
		t.Fatal(err)
	}
	return accepted.ID
}

// loop is the job loop with h (the simulation handler, or what wraps it).
func (e *env) loop(t *testing.T, h handler.Handler) *worker.Worker {
	t.Helper()
	w, err := worker.New(e.scope, worker.Config{ID: "test-worker", Policy: fast, Heartbeat: time.Second, Idle: 20 * time.Millisecond,
		ReapLimit: 10, Handlers: map[jobs.Type]handler.Handler{jobs.Simulation: h}, Log: e.log})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func (e *env) handler(t *testing.T, cfg Config) *Handler {
	t.Helper()
	cfg.Packs, cfg.Objects, cfg.Log = e.packs, e.objects, e.log
	if cfg.Actor == "" {
		cfg.Actor = "test-worker"
	}
	if cfg.Poll == 0 {
		cfg.Poll = time.Hour // no progress checkpoint unless a test wants one
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// until runs passes until run id is final (or fails the test after d).
func (e *env) until(t *testing.T, w *worker.Worker, id string, d time.Duration) store.Run {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		w.Pass(context.Background())
		r := e.run(t, id)
		if r.Status.Final() {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s is still %s", id, r.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (e *env) run(t *testing.T, id string) store.Run {
	t.Helper()
	var r store.Run
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		r, err = store.Get(ctx, tx, e.org.ID, id)
		return err
	})
	return r
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error { return tx.QueryRow(ctx, sql, args...).Scan(&n) })
	return n
}

func (e *env) failure(t *testing.T, id string) jobs.Failure {
	t.Helper()
	var f jobs.Failure
	if err := json.Unmarshal(e.run(t, id).Failure, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func (e *env) digest(t *testing.T, id string) string {
	t.Helper()
	var d string
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT result_digest FROM twin.simulation_results WHERE run_id=$1`, id).Scan(&d)
	})
	return d
}

// events is how many events the frozen input of a run of mv with config
// processes, through the engine alone.
func (e *env) events(t *testing.T, mv string, config simulation.RunConfig) int64 {
	t.Helper()
	var v struct {
		ir   []byte
		hash string
	}
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT ir, hash FROM twin.model_versions WHERE id=$1`, mv).Scan(&v.ir, &v.hash)
	})
	var ir model.ModelIR
	if err := model.Decode(v.ir, &ir); err != nil {
		t.Fatal(err)
	}
	out, err := (&des.Engine{}).RunWith(context.Background(), simulation.RunInput{Model: ir, ModelHash: v.hash, EffectiveModelHash: v.hash, Config: config,
		Limits: runs.DefaultLimits}, des.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return out.Events
}

// flaky passes the attempt to h, but its Checkpoint fails from call n on,
// as when the worker loses the database midway: the attempt ends with an
// error the loop retries.
type flaky struct {
	h     handler.Handler
	n     int32
	calls atomic.Int32
	once  atomic.Bool // only the first attempt fails
}

type flakyRun struct {
	handler.Run
	f *flaky
}

var errCrash = errors.New("test: the connection is gone")

func (r flakyRun) Checkpoint(ctx context.Context, fn func(ctx context.Context, tx tenant.Tx) error) error {
	if r.f.calls.Add(1) >= r.f.n {
		return errCrash
	}
	return r.Run.Checkpoint(ctx, fn)
}

func (f *flaky) Handle(ctx context.Context, run handler.Run) error {
	if f.once.Swap(true) {
		return f.h.Handle(ctx, run)
	}
	return f.h.Handle(ctx, flakyRun{Run: run, f: f})
}

// D-007: the event budget is the logical run's. An attempt reserves its
// first batch as it starts and each later batch before spending it; an
// attempt that fails keeps what it reserved as spent. With room for that
// the retry completes with the same result as an uninterrupted run; without
// it the retry fails EVENT_BUDGET_EXCEEDED, not retried. Never is more than
// max_events reserved.
func TestEventBudgetAcrossRetries(t *testing.T) {
	probe := setup(t, runs.DefaultLimits)
	need := probe.events(t, probe.version(t, "default-delivery.json"), week)
	batch := (need + 3) / 4
	t.Logf("%d events; batches of %d", need, batch)

	for _, c := range []struct {
		name   string
		max    int64
		status store.Status
	}{
		{"room for the retry", need + 2*batch + 1, store.Completed},
		{"no room", need + batch, store.Failed},
	} {
		limits := runs.DefaultLimits
		limits.MaxEvents = c.max
		e := setup(t, limits)
		mv := e.version(t, "default-delivery.json")
		// An uninterrupted run of the same version first.
		reference := e.create(t, mv, week)
		if r := e.until(t, e.loop(t, e.handler(t, Config{Batch: func(int64) int64 { return batch }})), reference, time.Minute); r.Status != store.Completed {
			t.Fatalf("%s: the uninterrupted run %+v", c.name, r)
		}
		want := e.digest(t, reference)
		id := e.create(t, mv, week)
		// The first attempt starts (a checkpoint, reserving one batch), is
		// granted one more batch (a second), and loses the database asking
		// for the third.
		w := e.loop(t, &flaky{h: e.handler(t, Config{Batch: func(int64) int64 { return batch }}), n: 3})
		r := e.until(t, w, id, time.Minute)
		if r.Status != c.status || r.EventBudgetReserved > c.max {
			t.Errorf("%s: %s, reserved %d of %d", c.name, r.Status, r.EventBudgetReserved, c.max)
		}
		if n := e.count(t, `SELECT attempt FROM twin.jobs WHERE simulation_run_id=$1`, id); n != 2 {
			t.Errorf("%s: %d attempts", c.name, n)
		}
		switch c.status {
		case store.Completed:
			// Two batches for the failed attempt, then the retry's own.
			if got := e.digest(t, id); got != want {
				t.Errorf("%s: digest %s, an uninterrupted run's %s", c.name, got, want)
			}
			if min := 2*batch + need; r.EventBudgetReserved < min {
				t.Errorf("%s: reserved %d, at least %d were (the failed attempt's two batches and the retry's events)", c.name, r.EventBudgetReserved, min)
			}
		case store.Failed:
			if f := e.failure(t, id); f.Code != store.EventBudgetExceeded || f.Retryable {
				t.Errorf("%s: failure %+v", c.name, f)
			}
			if n := e.count(t, `SELECT count(*) FROM twin.simulation_results WHERE run_id=$1`, id); n != 0 {
				t.Errorf("%s: a result", c.name)
			}
		}
	}
}

// D-007: the wall clock is the logical run's: it starts at the first start
// and no retry resets it, so a retry after the deadline fails at once,
// WALL_TIMEOUT_EXCEEDED, not retried.
func TestWallClockAcrossRetries(t *testing.T) {
	limits := runs.DefaultLimits
	limits.WallTimeoutSeconds = 1
	e := setup(t, limits)
	mv := e.version(t, "default-delivery.json")
	id := e.create(t, mv, week)
	f := &flaky{h: e.handler(t, Config{Batch: func(int64) int64 { return 1000 }}), n: 2}
	w := e.loop(t, f)
	w.Pass(context.Background())
	first := e.run(t, id)
	if first.Status != store.Queued || first.StartedAt == nil || first.ExecutionDeadlineAt == nil {
		t.Fatalf("after the failed attempt: %+v", first)
	}
	time.Sleep(time.Until(*first.ExecutionDeadlineAt) + 100*time.Millisecond)
	r := e.until(t, w, id, 30*time.Second)
	if r.Status != store.Failed || !r.ExecutionDeadlineAt.Equal(*first.ExecutionDeadlineAt) || !r.StartedAt.Equal(*first.StartedAt) {
		t.Errorf("after the deadline: %+v (first attempt %+v)", r, first)
	}
	// The late attempt started nothing: it reserved no events.
	if r.EventBudgetReserved != first.EventBudgetReserved {
		t.Errorf("reserved %d after the late attempt, %d before it", r.EventBudgetReserved, first.EventBudgetReserved)
	}
	if f := e.failure(t, id); f.Code != store.WallTimeoutExceeded || f.Retryable {
		t.Errorf("failure %+v", f)
	}
}

// TEST-020: a worker that crashes after starting an attempt writes nothing
// more; once its lease expires the reaper puts the job back, the run
// follows (queued, progress reset), and the next attempt completes it with
// the single result. The crashed lease cannot finish the job afterwards.
func TestACrashedAttemptIsRunAgain(t *testing.T) {
	e := setup(t, runs.DefaultLimits)
	mv := e.version(t, "deterministic-queue.json")
	id := e.create(t, mv, simulation.RunConfig{Horizon: simulation.Duration{Value: 1010, Unit: model.Minute}, Replications: 2, Seed: 1,
		TraceLevel: simulation.TraceNone, Warmup: simulation.Duration{Unit: model.Minute}})
	short := jobs.Policy{Lease: 500 * time.Millisecond, MaxAttempts: 3, BackoffBase: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond}
	var lease jobs.Lease
	if err := e.scope.Run(context.Background(), e.org.ID, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
		_, l, ok, err := jobs.Claim(ctx, tx, e.org.ID, "crashed-worker", []jobs.Type{jobs.Simulation}, short)
		if err != nil || !ok {
			return errors.Join(err, errors.New("no claim"))
		}
		lease = l
		_, err = store.Start(ctx, tx, e.org.ID, id, 40)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if r := e.run(t, id); r.Status != store.Running || r.EventBudgetReserved != 40 {
		t.Fatalf("the crashed attempt's start: %+v", r)
	}
	time.Sleep(600 * time.Millisecond)
	r := e.until(t, e.loop(t, e.handler(t, Config{})), id, 30*time.Second)
	if r.Status != store.Completed || r.EventBudgetReserved < 40+r.EventsProcessed {
		t.Errorf("run %+v", r)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.simulation_results WHERE run_id=$1`, id); n != 1 {
		t.Errorf("%d results", n)
	}
	if n := e.count(t, `SELECT attempt FROM twin.jobs WHERE simulation_run_id=$1 AND status='completed'`, id); n != 2 {
		t.Errorf("the job completed in attempt %d, want 2", n)
	}
	if err := e.scope.Run(context.Background(), e.org.ID, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
		if _, err := jobs.Lock(ctx, tx, lease); !errors.Is(err, jobs.ErrLeaseLost) {
			return errors.Join(err, errors.New("the crashed lease locked the job"))
		}
		return nil
	}); err != nil {
		t.Error(err)
	}
}

// ADR-024: a result commits with the job's end under the lease, never
// after the lease is lost. Here the lease expires just before the finish:
// nothing is stored; the reaper puts the job back and the next attempt
// stores the single result.
func TestNoResultAfterALostLease(t *testing.T) {
	e := setup(t, runs.DefaultLimits)
	mv := e.version(t, "deterministic-queue.json")
	id := e.create(t, mv, simulation.RunConfig{Horizon: simulation.Duration{Value: 1010, Unit: model.Minute}, Replications: 1, Seed: 1,
		TraceLevel: simulation.TraceSampled, Warmup: simulation.Duration{Unit: model.Minute}})
	h := e.handler(t, Config{})
	var expired atomic.Bool
	h.beforeFinish = func(context.Context) {
		if expired.Swap(true) {
			return
		}
		e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE twin.jobs SET lease_expires_at=now()-interval '1 second' WHERE simulation_run_id=$1`, id)
			return err
		})
	}
	w := e.loop(t, h)
	w.Pass(context.Background())
	if n := e.count(t, `SELECT count(*) FROM twin.simulation_results WHERE run_id=$1`, id); n != 0 {
		t.Fatal("a result committed after the lease was lost")
	}
	r := e.until(t, w, id, 30*time.Second)
	if r.Status != store.Completed {
		t.Fatalf("run %+v", r)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.simulation_results WHERE run_id=$1`, id); n != 1 {
		t.Errorf("%d results", n)
	}
	// The lost attempt's trace object stays an orphan for maintenance; the
	// result names the second attempt's.
	if n := e.count(t, `SELECT count(*) FROM twin.simulation_results WHERE run_id=$1 AND trace_artifact_id IS NOT NULL`, id); n != 1 {
		t.Error("the result names no trace")
	}
}

// TEST-021: a cancellation requested after the engine's last checkpoint
// still wins over the completion: the finish sees it under the run's lock
// and ends the job cancelled, storing nothing.
func TestALateCancellationWins(t *testing.T) {
	e := setup(t, runs.DefaultLimits)
	mv := e.version(t, "deterministic-queue.json")
	id := e.create(t, mv, simulation.RunConfig{Horizon: simulation.Duration{Value: 1010, Unit: model.Minute}, Replications: 1, Seed: 1,
		TraceLevel: simulation.TraceNone, Warmup: simulation.Duration{Unit: model.Minute}})
	h := e.handler(t, Config{})
	h.beforeFinish = func(context.Context) {
		editor := e.org.Members[tenant.Editor]
		if status, _, err := e.runs.Cancel(context.Background(), tenant.Scope{OrganizationID: e.org.ID, UserID: editor.UserID}, ids.New("req"), ids.New("key"), id, []byte(`{}`)); err != nil || status != 200 {
			t.Errorf("cancel: %d %v", status, err)
		}
	}
	r := e.until(t, e.loop(t, h), id, 30*time.Second)
	if r.Status != store.Cancelled || !r.CancellationRequested {
		t.Errorf("run %+v", r)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.simulation_results WHERE run_id=$1`, id); n != 0 {
		t.Error("a result despite the cancellation")
	}
}

// A run frozen for another engine, or a job whose payload does not name
// its run, fails at once and is not retried.
func TestRefusedAttempts(t *testing.T) {
	e := setup(t, runs.DefaultLimits)
	mv := e.version(t, "deterministic-queue.json")
	config := simulation.RunConfig{Horizon: simulation.Duration{Value: 1010, Unit: model.Minute}, Replications: 1, Seed: 1,
		TraceLevel: simulation.TraceNone, Warmup: simulation.Duration{Unit: model.Minute}}
	id := e.create(t, mv, config)
	r := e.until(t, e.loop(t, e.handler(t, Config{EngineVersion: "des-go-9.9.9"})), id, 30*time.Second)
	if f := e.failure(t, id); r.Status != store.Failed || f.Code != store.EngineVersionMismatch || f.Retryable {
		t.Errorf("another engine: %+v %+v", r, f)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.jobs WHERE simulation_run_id=$1 AND attempt=1 AND status='failed'`, id); n != 1 {
		t.Error("the mismatch was retried")
	}
	// The worker's name must be an audit actor.
	if _, err := New(Config{Packs: e.packs, Log: e.log, Actor: "1-not-an-actor"}); err == nil {
		t.Error("a worker name that is no audit actor was accepted")
	}
}

// One job per run: a second enqueue for the same run is refused.
func TestOneJobPerRun(t *testing.T) {
	e := setup(t, runs.DefaultLimits)
	mv := e.version(t, "deterministic-queue.json")
	id := e.create(t, mv, simulation.RunConfig{Horizon: simulation.Duration{Value: 1010, Unit: model.Minute}, Replications: 1, Seed: 1,
		TraceLevel: simulation.TraceNone, Warmup: simulation.Duration{Unit: model.Minute}})
	err := e.admin.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,true)`, e.org.ID); err != nil {
			return err
		}
		_, err := jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: e.org.ID, Type: jobs.Simulation,
			Resource: jobs.Resource{SimulationRunID: id}, Payload: map[string]any{"run_id": id}}, fast)
		return err
	})
	if !errors.Is(err, postgres.ErrConflict) {
		t.Errorf("a second job for the run: %v", err)
	}
}

// TEST-021 with TEST-020 (ADR-024, CB-19): a cancellation requested while
// an attempt is running survives that attempt's crash. The reaper puts the
// job back and the run follows to queued with the request kept; the next
// attempt sees the request before it starts anything and ends the job
// cancelled: no result, no further event reserved.
func TestACancellationOutlivesACrash(t *testing.T) {
	e := setup(t, runs.DefaultLimits)
	mv := e.version(t, "deterministic-queue.json")
	id := e.create(t, mv, simulation.RunConfig{Horizon: simulation.Duration{Value: 1010, Unit: model.Minute}, Replications: 2, Seed: 1,
		TraceLevel: simulation.TraceNone, Warmup: simulation.Duration{Unit: model.Minute}})
	short := jobs.Policy{Lease: 500 * time.Millisecond, MaxAttempts: 3, BackoffBase: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond}
	if err := e.scope.Run(context.Background(), e.org.ID, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
		if _, _, ok, err := jobs.Claim(ctx, tx, e.org.ID, "crashed-worker", []jobs.Type{jobs.Simulation}, short); err != nil || !ok {
			return errors.Join(err, errors.New("no claim"))
		}
		_, err := store.Start(ctx, tx, e.org.ID, id, 40)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	editor := e.org.Members[tenant.Editor]
	status, out, err := e.runs.Cancel(context.Background(), tenant.Scope{OrganizationID: e.org.ID, UserID: editor.UserID}, ids.New("req"), ids.New("key"), id, []byte(`{}`))
	var answered runs.Run
	if err == nil {
		err = json.Unmarshal(out, &answered)
	}
	if err != nil || status != 200 || answered.Status != "running" || !answered.CancellationRequested {
		t.Fatalf("cancel during the crashed attempt: %d %+v %v", status, answered, err)
	}
	time.Sleep(600 * time.Millisecond) // the crashed attempt's lease ends
	r := e.until(t, e.loop(t, e.handler(t, Config{})), id, 30*time.Second)
	if r.Status != store.Cancelled || !r.CancellationRequested || r.Failure != nil {
		t.Errorf("run %+v", r)
	}
	if r.EventBudgetReserved != 40 {
		t.Errorf("reserved %d events, want only the crashed attempt's 40", r.EventBudgetReserved)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.simulation_results WHERE run_id=$1`, id); n != 0 {
		t.Error("a result despite the cancellation")
	}
	if n := e.count(t, `SELECT count(*) FROM twin.jobs WHERE simulation_run_id=$1 AND status='cancelled' AND attempt=2`, id); n != 1 {
		t.Error("the job did not end cancelled in the attempt after the crash")
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='simulation.cancelled' AND actor_type='worker' AND (metadata->>'attempt')::int=2`, id); n != 1 {
		t.Errorf("%d worker cancellation audit events for attempt 2", n)
	}
}

// D-007, TEST-021: within one attempt the wall clock is a limit like the
// others: a run longer than wall_timeout_seconds fails at it,
// WALL_TIMEOUT_EXCEEDED, not retried, without a result.
func TestTheWallClockEndsAnAttempt(t *testing.T) {
	limits := runs.DefaultLimits
	limits.WallTimeoutSeconds = 1
	e := setup(t, limits)
	mv := e.version(t, "default-delivery.json")
	id := e.create(t, mv, simulation.RunConfig{Horizon: simulation.Duration{Value: 365, Unit: model.Day}, Warmup: simulation.Duration{Unit: model.Day},
		Replications: 400, Seed: 5, TraceLevel: simulation.TraceNone})
	w := e.loop(t, e.handler(t, Config{Workers: 1}))
	began := time.Now()
	w.Pass(context.Background())
	took := time.Since(began)
	r := e.run(t, id)
	if f := e.failure(t, id); r.Status != store.Failed || f.Code != store.WallTimeoutExceeded || f.Retryable {
		t.Fatalf("run %+v, failure %+v", r, f)
	}
	if took > 5*time.Second {
		t.Errorf("the attempt took %v with a 1 s wall time", took)
	}
	if r.StartedAt == nil || r.ExecutionDeadlineAt == nil || r.ExecutionDeadlineAt.Sub(*r.StartedAt) != time.Second {
		t.Errorf("deadline %v from start %v", r.ExecutionDeadlineAt, r.StartedAt)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.jobs WHERE simulation_run_id=$1 AND status='failed' AND attempt=1`, id); n != 1 {
		t.Error("the wall timeout was retried")
	}
	if n := e.count(t, `SELECT count(*) FROM twin.simulation_results WHERE run_id=$1`, id); n != 0 {
		t.Error("a timed-out run has a result")
	}
}
