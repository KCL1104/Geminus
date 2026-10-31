package main

// DB-backed tests of WORKER_LANES (H-056 design §9, W1) through run, as
// the plain worker login on the disposable database: the two lanes claim
// only their own types, a simulation does not wait for a long job of
// another type, a stop drains both lanes, a lane's failure stops the
// worker loudly, and single mode keeps its one loop under WORKER_ID.

import (
	"context"
	"encoding/json/v2"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/packs"
	"github.com/kcl1104/geminus/internal/runs"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

type laneEnv struct {
	pool    *pgxpool.Pool // the provisioning login
	admin   *postgres.DB
	org     dbtest.Org
	runs    *runs.Service
	version string
	getenv  func(lanes string) func(string) string
}

// laneSetup provisions an organization with an owner and an editor and a
// committed model version (the software delivery golden model) that runs
// are queued for, as the API queues them.
func laneSetup(t *testing.T) *laneEnv {
	t.Helper()
	admin := dbtest.Admin(t) // skips unless the disposable database is configured
	org := dbtest.NewOrg(t, admin, "https://test.invalid", tenant.Owner, tenant.Editor)
	served, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	api := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 4, true)
	workerURL := dbtest.LoginURL(t, dbtest.WorkerLogin)
	e := &laneEnv{pool: admin, admin: postgres.OpenPool(admin), org: org, runs: &runs.Service{DB: tenant.New(api), Packs: served},
		getenv: func(lanes string) func(string) string {
			return with("DATABASE_URL", workerURL, "WORKER_ORGANIZATION_IDS", org.ID, "WORKER_LANES", lanes)
		}}
	var ir map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "golden-models", "default-delivery.json")), &ir); err != nil {
		t.Fatal(err)
	}
	prj, mdl, mv := ids.New("prj"), ids.New("mdl"), ids.New("mv")
	ir["model_id"] = mdl
	raw, _ := json.Marshal(ir)
	canonical, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	user := org.Members[tenant.Owner].UserID
	if err := e.admin.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`SET LOCAL ROLE twin_owner`, nil},
			{`SELECT set_config('app.organization_id',$1,true)`, []any{org.ID}},
			{`INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, []any{prj, org.ID, user}},
			{`INSERT INTO twin.models(id,organization_id,project_id,name,domain,created_by) VALUES ($1,$2,$3,'M','software_delivery',$4)`, []any{mdl, org.ID, prj, user}},
			{`INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,ir,hash,created_by) VALUES ($1,$2,$3,1,'0.1',$4,$5,$6)`,
				[]any{mv, org.ID, mdl, string(canonical), hashing.Sum(canonical), user}},
			{`UPDATE twin.models SET latest_version_id=$1 WHERE id=$2`, []any{mv, mdl}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	e.version = mv
	return e
}

// simulation queues a run (and its simulation job) as the editor, through
// internal/runs as the API does; it returns the run's ID.
func (e *laneEnv) simulation(t *testing.T) string {
	t.Helper()
	req := runs.CreateRequest{ModelVersionID: e.version, Name: "run", ScenarioPatch: []byte("[]"), Config: simulation.RunConfig{
		Horizon: simulation.Duration{Value: 1, Unit: model.Day}, Warmup: simulation.Duration{Value: 0, Unit: model.Day},
		Replications: 1, Seed: 1, TraceLevel: simulation.TraceNone}}
	body, _ := json.Marshal(req)
	status, out, err := e.runs.Create(context.Background(), tenant.Scope{OrganizationID: e.org.ID, UserID: e.org.Members[tenant.Editor].UserID},
		ids.New("req"), ids.New("key"), body, req)
	if err != nil || status != 202 {
		t.Fatalf("create: %d %v", status, err)
	}
	var accepted runs.Accepted
	if err := json.Unmarshal(out, &accepted); err != nil {
		t.Fatal(err)
	}
	return accepted.ID
}

// profile queues an import_profile job (a type of the rest lane) on a new
// import; it returns the job's ID.
func (e *laneEnv) profile(t *testing.T) string {
	t.Helper()
	imp := dbtest.NewImport(t, e.pool, e.org)
	var j jobs.Job
	if err := e.admin.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		j, err = jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: e.org.ID, Type: jobs.ImportProfile,
			Resource: jobs.Resource{ImportID: imp}, Payload: map[string]any{"import_id": imp, "request_id": ids.New("req")}}, jobs.DefaultPolicy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return j.ID
}

// stored is a job as the database holds it.
type stored struct {
	status   string
	attempt  int
	lockedBy string // "" when no lane holds it
	code     string // the recorded failure's code, "" when none
}

// job is the stored job id.
func (e *laneEnv) job(t *testing.T, id string) stored {
	t.Helper()
	return e.query(t, `SELECT status, attempt, coalesce(locked_by,''), coalesce(error->>'code','') FROM twin.jobs
		WHERE organization_id=$1 AND id=$2`, id)
}

// runJob is the simulation job of run.
func (e *laneEnv) runJob(t *testing.T, run string) stored {
	t.Helper()
	return e.query(t, `SELECT status, attempt, coalesce(locked_by,''), coalesce(error->>'code','') FROM twin.jobs
		WHERE organization_id=$1 AND simulation_run_id=$2`, run)
}

func (e *laneEnv) query(t *testing.T, sql, id string) stored {
	t.Helper()
	var s stored
	if err := e.pool.QueryRow(context.Background(), sql, e.org.ID, id).Scan(&s.status, &s.attempt, &s.lockedBy, &s.code); err != nil {
		t.Fatal(err)
	}
	return s
}

// claimed receives what the handlers report claiming, until each of want
// (a job ID or run ID) has been seen; it fails the test after 20 s or if
// run exits first.
func claimed(t *testing.T, claims <-chan jobs.Job, done <-chan int, logs *syncBuffer, want ...string) map[string]jobs.Job {
	t.Helper()
	got := map[string]jobs.Job{}
	timeout := time.After(20 * time.Second)
	for len(got) < len(want) {
		select {
		case j := <-claims:
			for _, w := range want {
				if j.ID == w || j.Resource.SimulationRunID == w {
					got[w] = j
				}
			}
		case code := <-done:
			t.Fatalf("the worker exited %d: %s", code, logs.String())
		case <-timeout:
			t.Fatalf("claimed %d of %v: %s", len(got), want, logs.String())
		}
	}
	return got
}

func exited(t *testing.T, done <-chan int, logs *syncBuffer) int {
	t.Helper()
	select {
	case code := <-done:
		return code
	case <-time.After(20 * time.Second):
		t.Fatalf("the worker did not stop: %s", logs.String())
	}
	return -1
}

// With WORKER_LANES=split, a simulation queued behind a long job of another
// type (an AI proposal takes 50-90 s) runs at once in the sim lane; the sim
// lane never claims another type, even while it is idle and the rest lane
// is busy; a handler's panic in one lane is recorded as before and that
// lane goes on; a stop drains both lanes, each attempt recorded as
// WORKER_STOPPED, and the worker exits 0.
func TestSplitLanesRunSimulationsBesideOtherJobs(t *testing.T) {
	e := laneSetup(t)
	long := e.profile(t) // the oldest job: a single loop would take it first
	first := e.simulation(t)
	claims := make(chan jobs.Job, 8)
	release := make(chan struct{})
	var sims atomic.Int32
	hs := map[jobs.Type]handler.Handler{
		jobs.Simulation: handler.Func(func(ctx context.Context, r handler.Run) error {
			claims <- r.Job()
			if sims.Add(1) == 1 {
				<-release
				panic("a bug in the simulation handler") // recorded INTERNAL, not retried; the lane goes on
			}
			<-ctx.Done()
			return ctx.Err()
		}),
		jobs.ImportProfile: handler.Func(func(ctx context.Context, r handler.Run) error {
			claims <- r.Job()
			<-ctx.Done() // holds the rest lane until the worker stops
			return ctx.Err()
		}),
	}
	var logs syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- run(ctx, e.getenv("split"), &logs, fixed(hs)) }()

	got := claimed(t, claims, done, &logs, long, first)
	if by := got[long].LockedBy; by != "worker-0:rest" {
		t.Errorf("the import job is held by %q", by)
	}
	if by := got[first].LockedBy; by != "worker-0:sim" {
		t.Errorf("the simulation is held by %q", by)
	}

	// The sim lane's first job ends (its handler panics); then, idle, the
	// sim lane must leave another import job to the busy rest lane.
	waiting := e.profile(t)
	close(release)
	deadline := time.Now().Add(20 * time.Second)
	for {
		if j := e.runJob(t, first); j.status == "failed" {
			if j.code != "INTERNAL" || j.attempt != 1 {
				t.Errorf("the panicked simulation: %+v", j)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the first simulation did not end: %s", logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(2500 * time.Millisecond) // more than two of the idle sim lane's passes (idle = 1 s)
	if j := e.job(t, waiting); j != (stored{status: "queued"}) {
		t.Errorf("the idle sim lane touched an import job: %+v", j)
	}
	second := e.simulation(t)
	if by := claimed(t, claims, done, &logs, second)[second].LockedBy; by != "worker-0:sim" {
		t.Errorf("the second simulation is held by %q", by)
	}

	cancel()
	if code := exited(t, done, &logs); code != 0 {
		t.Fatalf("exit = %d: %s", code, logs.String())
	}
	stopped := stored{status: "queued", attempt: 1, code: "WORKER_STOPPED"}
	for name, j := range map[string]stored{"the long import job": e.job(t, long), "the second simulation": e.runJob(t, second)} {
		if j != stopped {
			t.Errorf("%s after the stop: %+v", name, j)
		}
	}
	if j := e.job(t, waiting); j != (stored{status: "queued"}) {
		t.Errorf("the waiting import job: %+v", j)
	}
	out := logs.String()
	if got := runningIDs(t, out); !slices.Equal(got, []string{"worker-0:sim", "worker-0:rest"}) || !strings.Contains(out, `"msg":"stopped"`) {
		t.Errorf("running as %v: %s", got, out)
	}
	if strings.Contains(out, "WORKER_LANE_") {
		t.Errorf("a stop was reported as a lane failure: %s", out)
	}
}

// panicky is the worker's stderr that panics once, in the sim lane's loop
// outside any handler (the line it writes after recording a simulation
// attempt's end), as a bug in the loop would; the panic must stop the rest
// lane (its attempt recorded as WORKER_STOPPED) and the worker must exit
// non-zero, saying which lane failed.
type panicky struct {
	syncBuffer
	once atomic.Bool
}

func (p *panicky) Write(b []byte) (int, error) {
	if strings.Contains(string(b), `"msg":"job attempt ended"`) && strings.Contains(string(b), `"job_type":"simulation"`) && p.once.CompareAndSwap(false, true) {
		panic("the log writer failed")
	}
	return p.syncBuffer.Write(b)
}

func TestALanesPanicStopsTheWorker(t *testing.T) {
	e := laneSetup(t)
	long := e.profile(t)
	sim := e.simulation(t)
	restBusy := make(chan struct{})
	var restOnce sync.Once
	hs := map[jobs.Type]handler.Handler{
		jobs.Simulation: handler.Func(func(ctx context.Context, r handler.Run) error {
			<-restBusy // the rest lane holds its job when the sim lane fails
			return &jobs.Failure{Code: "TEST_SIMULATION_ENDED", Message: "the test ended this run"}
		}),
		jobs.ImportProfile: handler.Func(func(ctx context.Context, r handler.Run) error {
			restOnce.Do(func() { close(restBusy) })
			<-ctx.Done()
			return ctx.Err()
		}),
	}
	logs := &panicky{}
	done := make(chan int, 1)
	go func() { done <- run(context.Background(), e.getenv("split"), logs, fixed(hs)) }()
	if code := exited(t, done, &logs.syncBuffer); code != exitLane {
		t.Fatalf("exit = %d, want %d: %s", code, exitLane, logs.String())
	}
	if j := e.runJob(t, sim); j != (stored{status: "failed", attempt: 1, code: "TEST_SIMULATION_ENDED"}) {
		t.Errorf("the simulation: %+v", j)
	}
	if j := e.job(t, long); j != (stored{status: "queued", attempt: 1, code: "WORKER_STOPPED"}) {
		t.Errorf("the rest lane's job was not drained: %+v", j)
	}
	line := laneReport(t, logs.String())
	if line["worker_id"] != "worker-0:sim" || line["code"] != "WORKER_LANE_PANICKED" || line["panic"] != "lane panicked with a string" {
		t.Errorf("the report: %v", line)
	}
}

// Single mode (the default) keeps one loop that holds every lease under
// WORKER_ID itself and runs every type, as before WORKER_LANES.
func TestASingleLoopHoldsLeasesUnderItsID(t *testing.T) {
	e := laneSetup(t)
	imp := e.profile(t)
	sim := e.simulation(t)
	claims := make(chan jobs.Job, 4)
	end := handler.Func(func(ctx context.Context, r handler.Run) error {
		claims <- r.Job()
		return &jobs.Failure{Code: "TEST_ENDED", Message: "the test ended this job"}
	})
	var logs syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, e.getenv(""), &logs, fixed(map[jobs.Type]handler.Handler{jobs.Simulation: end, jobs.ImportProfile: end}))
	}()
	for id, j := range claimed(t, claims, done, &logs, imp, sim) {
		if j.LockedBy != "worker-0" {
			t.Errorf("%s is held by %q", id, j.LockedBy)
		}
	}
	cancel()
	if code := exited(t, done, &logs); code != 0 {
		t.Fatalf("exit = %d: %s", code, logs.String())
	}
	if out := logs.String(); !slices.Equal(runningIDs(t, out), []string{"worker-0"}) || strings.Contains(out, "worker-0:") {
		t.Errorf("stderr: %s", out)
	}
}

// runningIDs is the worker_id of each "running" line of logs, in order.
func runningIDs(t *testing.T, logs string) []string {
	t.Helper()
	var ids []string
	for _, l := range strings.Split(strings.TrimSpace(logs), "\n") {
		var line struct {
			Msg           string `json:"msg"`
			WorkerID      string `json:"worker_id"`
			Organizations int    `json:"organizations"`
		}
		if err := json.Unmarshal([]byte(l), &line); err != nil {
			t.Fatalf("%v: %s", err, l)
		}
		if line.Msg == "running" {
			if line.Organizations != 1 {
				t.Errorf("a running line with %d organizations: %s", line.Organizations, l)
			}
			ids = append(ids, line.WorkerID)
		}
	}
	return ids
}
