package jobs_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
)

const issuer = "https://test.invalid"

// fast is the tests' policy. Leases and backoffs are long, so nothing
// expires or becomes due by itself while a test runs; expire and due move
// a job there explicitly (what a crash or the passing time would do), so
// no assertion depends on timing.
// handled are the types the tests' worker takes.
var handled = []jobs.Type{jobs.ImportProfile}

var fast = jobs.Policy{Lease: time.Minute, MaxAttempts: 3, BackoffBase: time.Hour, BackoffMax: 4 * time.Hour}

// expire ends the job's lease now, as when its worker stopped renewing it.
func (e *env) expire(t *testing.T, id string) {
	t.Helper()
	if _, err := e.admin.Exec(context.Background(), `UPDATE twin.jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1 AND status='running'`, id); err != nil {
		t.Fatal(err)
	}
}

// due makes a queued job available now, as when its backoff has passed.
func (e *env) due(t *testing.T, id string) {
	t.Helper()
	if _, err := e.admin.Exec(context.Background(), `UPDATE twin.jobs SET available_at=now()-interval '1 second' WHERE id=$1 AND status='queued'`, id); err != nil {
		t.Fatal(err)
	}
}

// waitsFor checks that a requeued job is not due before about d from now.
func waitsFor(t *testing.T, j jobs.Job, d time.Duration) {
	t.Helper()
	if wait := time.Until(j.AvailableAt); wait < d-time.Minute || wait > d+time.Minute {
		t.Errorf("available in %v, want about %v", wait, d)
	}
}

type env struct {
	admin  *pgxpool.Pool
	api    *tenant.DB
	worker *workerscope.Worker
	org    dbtest.Org
	other  dbtest.Org
	model  string
	base   string
	prj    string
}

func setup(t *testing.T) *env {
	t.Helper()
	admin := dbtest.Admin(t)
	e := &env{admin: admin, org: dbtest.NewOrg(t, admin, issuer, tenant.Owner, tenant.Viewer), other: dbtest.NewOrg(t, admin, issuer, tenant.Owner)}
	e.api = tenant.New(dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 4, true))
	wp := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.WorkerLogin), 8, true)
	if err := tenant.CheckRuntimeRole(context.Background(), wp, tenant.WorkerRole); err != nil {
		t.Fatal(err)
	}
	w, err := workerscope.New(wp, []string{e.org.ID, e.other.ID})
	if err != nil {
		t.Fatal(err)
	}
	e.worker = w
	e.prj, e.model, e.base = e.newModel(t, e.org)
	return e
}

// newModel inserts a project, a model and its first version (a canonical
// IR with its real hash) as the provisioning superuser.
func (e *env) newModel(t *testing.T, o dbtest.Org) (prj, mdl, mv string) {
	t.Helper()
	prj, mdl, mv = ids.New("prj"), ids.New("mdl"), ids.New("mv")
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "contracts", "valid-minimal-ir.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["model_id"] = mdl
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	ir, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	user := o.Members[tenant.Owner].UserID
	err = postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, []any{prj, o.ID, user}},
			{`INSERT INTO twin.models(id,organization_id,project_id,name,domain,created_by) VALUES ($1,$2,$3,'M','software_delivery',$4)`, []any{mdl, o.ID, prj, user}},
			{`INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,ir,hash,created_by) VALUES ($1,$2,$3,1,'0.1',$4,$5,$6)`,
				[]any{mv, o.ID, mdl, string(ir), hashing.Sum(ir), user}},
			{`UPDATE twin.models SET latest_version_id=$1 WHERE id=$2`, []any{mv, mdl}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return prj, mdl, mv
}

// insertImport is the resource row of an import job, written in tx.
func insertImport(ctx context.Context, tx pgx.Tx, org, prj, mdl, mv, user string) (string, error) {
	id := ids.New("imp")
	_, err := tx.Exec(ctx, `INSERT INTO twin.imports(id,organization_id,project_id,model_id,base_version_id,kind,filename,upload_object_key,upload_content_hash,size_bytes,status,created_by)
		VALUES ($1,$2,$3,$4,$5,'csv_events','events.csv',$6,$7,10,'UPLOADED',$8)`,
		id, org, prj, mdl, mv, "tenant/"+org+"/imports/"+id+".csv", hashing.Sum([]byte(id)), user)
	return id, err
}

// enqueue creates an import and its profiling job as the organization's
// owner through the API scope, the way an upload will (T-016).
func (e *env) enqueue(t *testing.T, o dbtest.Org, prj, mdl, mv string, priority int) jobs.Job {
	t.Helper()
	var j jobs.Job
	owner := o.Members[tenant.Owner]
	err := e.api.Run(context.Background(), tenant.Scope{OrganizationID: o.ID, UserID: owner.UserID}, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		imp, err := insertImport(ctx, tx, o.ID, prj, mdl, mv, owner.UserID)
		if err != nil {
			return err
		}
		j, err = jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: o.ID, Type: jobs.ImportProfile,
			Resource: jobs.Resource{ImportID: imp}, Payload: map[string]any{"import_id": imp}, Priority: priority}, fast)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func (e *env) claim(t *testing.T, org, worker string) (jobs.Job, jobs.Lease, bool) {
	t.Helper()
	var j jobs.Job
	var l jobs.Lease
	var ok bool
	err := e.worker.Run(context.Background(), org, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		j, l, ok, err = jobs.Claim(ctx, tx, org, worker, handled, fast)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return j, l, ok
}

func (e *env) as(t *testing.T, org string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	t.Helper()
	return e.worker.Run(context.Background(), org, pgx.TxOptions{}, fn)
}

func (e *env) get(t *testing.T, org, id string) jobs.Job {
	t.Helper()
	var j jobs.Job
	if err := e.as(t, org, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		j, err = jobs.Get(ctx, tx, org, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return j
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.admin.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The resource, its job and the audit event commit together or not at all
// (API_DATABASE §6), and a job the database could not take is refused
// before it gets there.
func TestEnqueueWithTheResource(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	owner := e.org.Members[tenant.Owner]
	sc := tenant.Scope{OrganizationID: e.org.ID, UserID: owner.UserID}
	var imp, job string
	write := func(fail bool) error {
		return e.api.Run(ctx, sc, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
			var err error
			if imp, err = insertImport(ctx, tx, e.org.ID, e.prj, e.model, e.base, owner.UserID); err != nil {
				return err
			}
			job = ids.New("job")
			if _, err := jobs.Enqueue(ctx, tx, jobs.New{ID: job, OrganizationID: e.org.ID, Type: jobs.ImportProfile,
				Resource: jobs.Resource{ImportID: imp}, Payload: map[string]any{"import_id": imp}}, fast); err != nil {
				return err
			}
			if err := auditlog.AppendUser(ctx, tx, e.org.ID, owner.UserID, auditlog.Event{Action: "import.create", TargetType: "import", TargetID: imp, RequestID: ids.New("req")}); err != nil {
				return err
			}
			if fail {
				return errors.New("the request failed after the writes")
			}
			return nil
		})
	}
	if err := write(true); err == nil {
		t.Fatal("the failing write succeeded")
	}
	if n := e.count(t, `SELECT (SELECT count(*) FROM twin.imports WHERE id=$1)+(SELECT count(*) FROM twin.jobs WHERE id=$2)+(SELECT count(*) FROM twin.audit_events WHERE target_id=$1)`, imp, job); n != 0 {
		t.Errorf("a rolled-back request left %d rows", n)
	}
	if err := write(false); err != nil {
		t.Fatal(err)
	}
	j := e.get(t, e.org.ID, job)
	var payload map[string]string
	if err := json.Unmarshal(j.Payload, &payload); err != nil || payload["import_id"] != imp || len(payload) != 1 {
		t.Errorf("payload %s: %v", j.Payload, err)
	}
	if j.Status != jobs.Queued || j.Attempt != 0 || j.MaxAttempts != fast.MaxAttempts || j.LeaseToken != 0 || j.Resource.ImportID != imp {
		t.Errorf("enqueued %+v", j)
	}
	err := e.api.Run(ctx, sc, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		for name, n := range map[string]jobs.New{
			"no resource":          {ID: ids.New("job"), OrganizationID: e.org.ID, Type: jobs.ImportProfile, Payload: map[string]any{}},
			"two resources":        {ID: ids.New("job"), OrganizationID: e.org.ID, Type: jobs.ImportProfile, Resource: jobs.Resource{ImportID: imp, SimulationRunID: ids.New("sim")}, Payload: map[string]any{}},
			"the type's other one": {ID: ids.New("job"), OrganizationID: e.org.ID, Type: jobs.Simulation, Resource: jobs.Resource{ImportID: imp}, Payload: map[string]any{}},
			"unknown type":         {ID: ids.New("job"), OrganizationID: e.org.ID, Type: "export", Resource: jobs.Resource{ImportID: imp}, Payload: map[string]any{}},
			"no payload":           {ID: ids.New("job"), OrganizationID: e.org.ID, Type: jobs.ImportProfile, Resource: jobs.Resource{ImportID: imp}},
			"eleven attempts":      {ID: ids.New("job"), OrganizationID: e.org.ID, Type: jobs.ImportProfile, Resource: jobs.Resource{ImportID: imp}, Payload: map[string]any{}, MaxAttempts: 11},
		} {
			if _, err := jobs.Enqueue(ctx, tx, n, fast); !errors.Is(err, jobs.ErrInvalidJob) {
				t.Errorf("%s: %v", name, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Only the API enqueues: the worker role has no INSERT on jobs.
	if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: e.org.ID, Type: jobs.ImportProfile,
			Resource: jobs.Resource{ImportID: imp}, Payload: map[string]any{}}, fast)
		return err
	}); !errors.Is(err, postgres.ErrPermission) {
		t.Errorf("the worker enqueued: %v", err)
	}
}

// A claim takes the highest priority, then the oldest job, only in its
// organization, and not before the job is available; it starts an attempt
// with a new lease token.
func TestClaimOrderAndScope(t *testing.T) {
	e := setup(t)
	low := e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	high := e.enqueue(t, e.org, e.prj, e.model, e.base, 5)
	later := e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	oprj, omdl, omv := e.newModel(t, e.other)
	foreign := e.enqueue(t, e.other, oprj, omdl, omv, 9)

	// A worker takes only the types it handles; the others stay queued.
	if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
		if _, _, ok, err := jobs.Claim(ctx, tx, e.org.ID, "worker-sim", []jobs.Type{jobs.Simulation, jobs.Analysis}, fast); err != nil || ok {
			t.Errorf("a worker without the import handler claimed an import job: %v %v", ok, err)
		}
		if _, _, _, err := jobs.Claim(ctx, tx, e.org.ID, "worker-none", nil, fast); err == nil {
			t.Error("a claim for no type was accepted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for range 3 {
		j, l, ok := e.claim(t, e.org.ID, "worker-1")
		if !ok {
			t.Fatal("nothing to claim")
		}
		if j.Status != jobs.Running || j.Attempt != 1 || j.LeaseToken != 1 || j.LockedBy != "worker-1" || j.LeaseExpiresAt == nil || l.Token != 1 || l.JobID != j.ID {
			t.Errorf("claimed %+v %+v", j, l)
		}
		got = append(got, j.ID)
	}
	if want := []string{high.ID, low.ID, later.ID}; !slices.Equal(got, want) {
		t.Errorf("claim order %v, want %v", got, want)
	}
	if _, _, ok := e.claim(t, e.org.ID, "worker-1"); ok {
		t.Error("claimed with nothing queued")
	}
	if j := e.get(t, e.other.ID, foreign.ID); j.Status != jobs.Queued {
		t.Errorf("the other organization's job is %s", j.Status)
	}
	// Not before available_at.
	if _, err := e.admin.Exec(context.Background(), `UPDATE twin.jobs SET available_at=now()+interval '1 hour' WHERE id=$1`, foreign.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := e.claim(t, e.other.ID, "worker-1"); ok {
		t.Error("claimed a job that is not available yet")
	}
}

// SKIP LOCKED: concurrent claims never take the same job.
func TestConcurrentClaims(t *testing.T) {
	e := setup(t)
	const n = 12
	for range n {
		e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	}
	var mu sync.Mutex
	claimed := map[string]int{}
	var wg sync.WaitGroup
	for w := range 6 {
		wg.Go(func() {
			for {
				var j jobs.Job
				var ok bool
				err := e.worker.Run(context.Background(), e.org.ID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
					var err error
					j, _, ok, err = jobs.Claim(ctx, tx, e.org.ID, "worker-"+string(rune('a'+w)), handled, fast)
					return err
				})
				if err != nil {
					t.Error(err)
					return
				}
				if !ok {
					return
				}
				mu.Lock()
				claimed[j.ID]++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(claimed) != n {
		t.Errorf("%d of %d jobs claimed", len(claimed), n)
	}
	for id, c := range claimed {
		if c != 1 {
			t.Errorf("%s claimed %d times", id, c)
		}
	}
}

// TEST-020: only the current lease writes. A heartbeat renews it; another
// worker, an old token, an expired lease or a finished job get
// ErrLeaseLost and change nothing.
func TestTheLeaseFencesEveryWrite(t *testing.T) {
	e := setup(t)
	e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	j, l, _ := e.claim(t, e.org.ID, "worker-1")
	before := *j.LeaseExpiresAt
	time.Sleep(50 * time.Millisecond)
	if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error { return jobs.Heartbeat(ctx, tx, l, fast) }); err != nil {
		t.Fatal(err)
	}
	if after := e.get(t, e.org.ID, j.ID).LeaseExpiresAt; !after.After(before) {
		t.Errorf("the heartbeat did not renew the lease: %v → %v", before, after)
	}
	writes := map[string]func(ctx context.Context, tx pgx.Tx, l jobs.Lease) error{
		"heartbeat": func(ctx context.Context, tx pgx.Tx, l jobs.Lease) error { return jobs.Heartbeat(ctx, tx, l, fast) },
		"lock":      func(ctx context.Context, tx pgx.Tx, l jobs.Lease) error { _, err := jobs.Lock(ctx, tx, l); return err },
		"complete":  func(ctx context.Context, tx pgx.Tx, l jobs.Lease) error { return jobs.Complete(ctx, tx, l) },
		"cancel":    func(ctx context.Context, tx pgx.Tx, l jobs.Lease) error { return jobs.Cancel(ctx, tx, l) },
		"fail": func(ctx context.Context, tx pgx.Tx, l jobs.Lease) error {
			_, err := jobs.Fail(ctx, tx, l, jobs.Failure{Code: "X", Message: "x", Retryable: true}, fast)
			return err
		},
	}
	other, old := l, l
	other.WorkerID = "worker-2"
	old.Token--
	wrongOrg := l
	wrongOrg.OrganizationID = e.other.ID
	for name, write := range writes {
		for what, lease := range map[string]jobs.Lease{"another worker": other, "an old token": old} {
			if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error { return write(ctx, tx, lease) }); !errors.Is(err, jobs.ErrLeaseLost) {
				t.Errorf("%s with %s: %v", name, what, err)
			}
		}
		if err := e.as(t, e.other.ID, func(ctx context.Context, tx pgx.Tx) error { return write(ctx, tx, wrongOrg) }); !errors.Is(err, jobs.ErrLeaseLost) {
			t.Errorf("%s in another organization: %v", name, err)
		}
	}
	if got := e.get(t, e.org.ID, j.ID); got.Status != jobs.Running || got.LeaseToken != 1 || got.Error != nil {
		t.Errorf("refused writes changed the job: %+v", got)
	}
	if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := jobs.Lock(ctx, tx, l); err != nil {
			return err
		}
		return jobs.Complete(ctx, tx, l)
	}); err != nil {
		t.Fatal(err)
	}
	for name, write := range writes {
		if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error { return write(ctx, tx, l) }); !errors.Is(err, jobs.ErrLeaseLost) {
			t.Errorf("%s after completing: %v", name, err)
		}
	}
	if got := e.get(t, e.org.ID, j.ID); got.Status != jobs.Completed || got.LockedBy != "" || got.LeaseExpiresAt != nil {
		t.Errorf("completed job %+v", got)
	}
}

// TEST-020 crash recovery: a lease that expires is reaped and the job runs
// again under a new token after the backoff; the crashed worker's writes
// are refused; when the attempts are spent the job fails.
func TestReapAfterACrash(t *testing.T) {
	e := setup(t)
	queued := e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	_, first, _ := e.claim(t, e.org.ID, "worker-1")
	reap := func() (int, int) {
		var r, f int
		if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			r, f, err = jobs.Reap(ctx, tx, e.org.ID, 10, fast)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return r, f
	}
	if r, f := reap(); r != 0 || f != 0 {
		t.Errorf("reaped a live lease: %d %d", r, f)
	}
	e.expire(t, queued.ID) // worker-1 crashed
	if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error { return jobs.Heartbeat(ctx, tx, first, fast) }); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Errorf("a heartbeat after expiry: %v", err)
	}
	if r, f := reap(); r != 1 || f != 0 {
		t.Fatalf("reap: %d requeued, %d failed", r, f)
	}
	j := e.get(t, e.org.ID, queued.ID)
	var failure jobs.Failure
	if err := json.Unmarshal(j.Error, &failure); err != nil || failure.Code != jobs.LeaseExpired || j.Status != jobs.Queued || j.Attempt != 1 || j.LockedBy != "" {
		t.Errorf("reaped job %+v %v", j, err)
	}
	waitsFor(t, j, fast.Backoff(1))
	if _, _, ok := e.claim(t, e.org.ID, "worker-2"); ok {
		t.Error("claimed during the backoff")
	}
	e.due(t, queued.ID)
	_, second, ok := e.claim(t, e.org.ID, "worker-2")
	if !ok || second.Token != 2 {
		t.Fatalf("the retry: %v %+v", ok, second)
	}
	// The crashed worker comes back: it cannot finish the job.
	if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error { return jobs.Complete(ctx, tx, first) }); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Errorf("the crashed worker completed the retried job: %v", err)
	}
	// Two more expiries spend the attempts: then the job fails.
	e.expire(t, queued.ID)
	reap()
	waitsFor(t, e.get(t, e.org.ID, queued.ID), fast.Backoff(2))
	e.due(t, queued.ID)
	if _, third, ok := e.claim(t, e.org.ID, "worker-3"); !ok || third.Token != 3 {
		t.Fatalf("the third attempt: %v %+v", ok, third)
	}
	e.expire(t, queued.ID)
	if r, f := reap(); r != 0 || f != 1 {
		t.Errorf("the last expiry: %d requeued, %d failed", r, f)
	}
	if j := e.get(t, e.org.ID, queued.ID); j.Status != jobs.Failed || j.Attempt != 3 {
		t.Errorf("after three attempts %+v", j)
	}
	if _, _, ok := e.claim(t, e.org.ID, "worker-4"); ok {
		t.Error("claimed a failed job")
	}
}

// A retryable failure queues the job again while attempts remain; any
// other ends it.
func TestFailRetriesOnlyWhatMayBeRetried(t *testing.T) {
	e := setup(t)
	retry := e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	_, l, _ := e.claim(t, e.org.ID, "worker-1")
	fail := func(l jobs.Lease, retryable bool) bool {
		var retried bool
		if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			retried, err = jobs.Fail(ctx, tx, l, jobs.Failure{Code: "DEPENDENCY_UNAVAILABLE", Message: "object storage timed out", Retryable: retryable}, fast)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return retried
	}
	if !fail(l, true) {
		t.Error("a retryable first failure was not retried")
	}
	j := e.get(t, e.org.ID, retry.ID)
	if j.Status != jobs.Queued || j.LockedBy != "" || j.LeaseExpiresAt != nil {
		t.Errorf("retried job %+v", j)
	}
	waitsFor(t, j, fast.Backoff(1))
	if _, _, ok := e.claim(t, e.org.ID, "worker-1"); ok {
		t.Error("claimed during the backoff")
	}
	e.due(t, retry.ID)
	_, l, _ = e.claim(t, e.org.ID, "worker-1")
	if fail(l, false) {
		t.Error("a non-retryable failure was retried")
	}
	if j := e.get(t, e.org.ID, retry.ID); j.Status != jobs.Failed || j.Attempt != 2 {
		t.Errorf("failed job %+v", j)
	}
	last := e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	for attempt := 1; attempt <= fast.MaxAttempts; attempt++ {
		_, l, ok := e.claim(t, e.org.ID, "worker-1")
		if !ok {
			t.Fatalf("attempt %d: nothing to claim", attempt)
		}
		if retried := fail(l, true); retried != (attempt < fast.MaxAttempts) {
			t.Errorf("attempt %d retried: %v", attempt, retried)
		}
		e.due(t, last.ID)
	}
	if j := e.get(t, e.org.ID, last.ID); j.Status != jobs.Failed || j.Attempt != fast.MaxAttempts {
		t.Errorf("after the last retryable failure %+v", j)
	}
}

func TestBackoff(t *testing.T) {
	p := jobs.DefaultPolicy
	for attempt, want := range map[int]time.Duration{1: 5 * time.Second, 2: 10 * time.Second, 3: 20 * time.Second, 7: 5 * time.Minute, 10: 5 * time.Minute} {
		if got := p.Backoff(attempt); got != want {
			t.Errorf("attempt %d: %v, want %v", attempt, got, want)
		}
	}
}

// A Failure is an error a handler returns; a nil one (a handler's bug)
// still answers, so the worker can log it.
func TestAFailureIsAnError(t *testing.T) {
	var err error = &jobs.Failure{Code: "INVALID_INPUT", Message: "the upload is not CSV"}
	if err.Error() != "INVALID_INPUT: the upload is not CSV" {
		t.Errorf("a failure: %q", err.Error())
	}
	var none *jobs.Failure
	if err = none; err.Error() != "jobs: a nil failure" {
		t.Errorf("a nil failure: %q", err.Error())
	}
}

// The reaper skips a job a finalizing transaction holds (SKIP LOCKED): it
// neither waits for that transaction nor takes the job from under it.
func TestReapSkipsAJobBeingFinalized(t *testing.T) {
	e := setup(t)
	j := e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	e.claim(t, e.org.ID, "worker-1")
	e.expire(t, j.ID)
	ctx := context.Background()
	held, err := e.admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(ctx)
	if _, err := held.Exec(ctx, `SELECT 1 FROM twin.jobs WHERE id=$1 FOR UPDATE`, j.ID); err != nil {
		t.Fatal(err)
	}
	quick, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var r, f int
	err = e.worker.Run(quick, e.org.ID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		r, f, err = jobs.Reap(ctx, tx, e.org.ID, 10, fast)
		return err
	})
	if err != nil || r != 0 || f != 0 {
		t.Errorf("reaping a held job: %d requeued, %d failed, %v", r, f, err)
	}
	if err := held.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
		r, f, err = jobs.Reap(ctx, tx, e.org.ID, 10, fast)
		return err
	}); err != nil || r != 1 {
		t.Errorf("reaping it once released: %d %d %v", r, f, err)
	}
}

// A claim skips a job another transaction holds (SKIP LOCKED) and takes
// the next one at once, instead of waiting for that transaction.
func TestClaimSkipsAHeldJob(t *testing.T) {
	e := setup(t)
	first := e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	second := e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	ctx := context.Background()
	held, err := e.admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(ctx)
	if _, err := held.Exec(ctx, `SELECT 1 FROM twin.jobs WHERE id=$1 FOR UPDATE`, first.ID); err != nil {
		t.Fatal(err)
	}
	quick, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var j jobs.Job
	var ok bool
	err = e.worker.Run(quick, e.org.ID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		j, _, ok, err = jobs.Claim(ctx, tx, e.org.ID, "worker-1", handled, fast)
		return err
	})
	if err != nil || !ok || j.ID != second.ID {
		t.Errorf("claiming past a held job: %v %v %s", err, ok, j.ID)
	}
}

// Lock takes the job's row lock until the finalizing transaction ends, so
// nothing (the reaper included) can change the job in between.
func TestLockHoldsTheJob(t *testing.T) {
	e := setup(t)
	j := e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	_, l, _ := e.claim(t, e.org.ID, "worker-1")
	ctx := context.Background()
	tryLock := func() error {
		tx, err := e.admin.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		_, err = tx.Exec(ctx, `SELECT 1 FROM twin.jobs WHERE id=$1 FOR UPDATE NOWAIT`, j.ID)
		return err
	}
	if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := jobs.Lock(ctx, tx, l); err != nil {
			return err
		}
		if err := tryLock(); err == nil || !strings.Contains(err.Error(), "55P03") {
			t.Errorf("the job was not locked while finalizing: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := tryLock(); err != nil {
		t.Errorf("the lock outlived the transaction: %v", err)
	}
}

// cancelQueued cancels a queued job as the organization's owner through the
// API scope, the way a cancel request will (T-023).
func (e *env) cancelQueued(t *testing.T, o dbtest.Org, id string) bool {
	t.Helper()
	var ok bool
	owner := o.Members[tenant.Owner]
	if err := e.api.Run(context.Background(), tenant.Scope{OrganizationID: o.ID, UserID: owner.UserID}, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		var err error
		ok, err = jobs.CancelQueued(ctx, tx, o.ID, id)
		return err
	}); err != nil {
		t.Error(err) // not Fatal: TestCancelRacesTheClaim calls it from another goroutine
	}
	return ok
}

// TEST-021 at the queue: a queued job is cancelled at once and never
// claimed; a running one is not (its handler stops at a checkpoint and
// ends it with Cancel); cancelling and completing exclude each other, and
// a finished job stays as it ended.
func TestCancellation(t *testing.T) {
	e := setup(t)
	queued := e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	if e.cancelQueued(t, e.other, queued.ID) {
		t.Error("another organization cancelled the job")
	}
	if !e.cancelQueued(t, e.org, queued.ID) {
		t.Fatal("a queued job was not cancelled")
	}
	if got := e.get(t, e.org.ID, queued.ID); got.Status != jobs.Cancelled || got.Attempt != 0 || got.LockedBy != "" {
		t.Errorf("the cancelled job is %+v", got)
	}
	if e.cancelQueued(t, e.org, queued.ID) {
		t.Error("a cancelled job was cancelled again")
	}
	if _, _, ok := e.claim(t, e.org.ID, "worker-1"); ok {
		t.Error("a cancelled job was claimed")
	}

	// Cancel ends a running job; nothing the lease could do follows.
	running := e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	_, l, _ := e.claim(t, e.org.ID, "worker-1")
	if e.cancelQueued(t, e.org, running.ID) {
		t.Error("a running job was cancelled without its handler")
	}
	if got := e.get(t, e.org.ID, running.ID); got.Status != jobs.Running {
		t.Errorf("the running job is %s", got.Status)
	}
	if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := jobs.Lock(ctx, tx, l); err != nil {
			return err
		}
		return jobs.Cancel(ctx, tx, l)
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error { return jobs.Complete(ctx, tx, l) }); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Errorf("completing a cancelled job: %v", err)
	}
	if got := e.get(t, e.org.ID, running.ID); got.Status != jobs.Cancelled || got.LockedBy != "" || got.LeaseExpiresAt != nil || got.Error != nil {
		t.Errorf("the cancelled job is %+v", got)
	}

	// And the other way round.
	done := e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	_, l, _ = e.claim(t, e.org.ID, "worker-1")
	if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error { return jobs.Complete(ctx, tx, l) }); err != nil {
		t.Fatal(err)
	}
	if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error { return jobs.Cancel(ctx, tx, l) }); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Errorf("cancelling a completed job: %v", err)
	}
	if e.cancelQueued(t, e.org, done.ID) {
		t.Error("a completed job was cancelled")
	}
	if got := e.get(t, e.org.ID, done.ID); got.Status != jobs.Completed {
		t.Errorf("the completed job is %s", got.Status)
	}
}

// A cancel request racing the claims: every job is either cancelled while
// queued or claimed, never both.
func TestCancelRacesTheClaim(t *testing.T) {
	e := setup(t)
	const n = 12
	var queued []jobs.Job
	for range n {
		queued = append(queued, e.enqueue(t, e.org, e.prj, e.model, e.base, 0))
	}
	var mu sync.Mutex
	claimed, cancelled := map[string]bool{}, map[string]bool{}
	var wg sync.WaitGroup
	for w := range 3 {
		wg.Go(func() {
			for {
				var j jobs.Job
				var ok bool
				if err := e.worker.Run(context.Background(), e.org.ID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
					var err error
					j, _, ok, err = jobs.Claim(ctx, tx, e.org.ID, "worker-"+string(rune('a'+w)), handled, fast)
					return err
				}); err != nil {
					t.Error(err)
					return
				}
				if !ok {
					return
				}
				mu.Lock()
				claimed[j.ID] = true
				mu.Unlock()
			}
		})
	}
	wg.Go(func() {
		for _, j := range slices.Backward(queued) {
			if e.cancelQueued(t, e.org, j.ID) {
				mu.Lock()
				cancelled[j.ID] = true
				mu.Unlock()
			}
		}
	})
	wg.Wait()
	for _, j := range queued {
		got := e.get(t, e.org.ID, j.ID)
		switch {
		case claimed[j.ID] == cancelled[j.ID]:
			t.Errorf("%s claimed %v, cancelled %v", j.ID, claimed[j.ID], cancelled[j.ID])
		case cancelled[j.ID] && got.Status != jobs.Cancelled, claimed[j.ID] && got.Status != jobs.Running:
			t.Errorf("%s is %s", j.ID, got.Status)
		}
	}
}

// leaseLeft is how long job id's lease has left by the database's clock.
func (e *env) leaseLeft(t *testing.T, id string) time.Duration {
	t.Helper()
	var ms float64
	if err := e.admin.QueryRow(context.Background(), `SELECT extract(epoch FROM lease_expires_at - clock_timestamp())*1000 FROM twin.jobs WHERE id=$1`, id).Scan(&ms); err != nil {
		t.Fatal(err)
	}
	return time.Duration(ms) * time.Millisecond
}

// A claim and a heartbeat each hold the job for one Policy.Lease from the
// database's now() (T-019 review M4), not more and not less.
func TestTheLeaseLastsOneLease(t *testing.T) {
	e := setup(t)
	e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	j, l, _ := e.claim(t, e.org.ID, "worker-1")
	if left := e.leaseLeft(t, j.ID); left < fast.Lease-5*time.Second || left > fast.Lease {
		t.Errorf("after the claim the lease has %v left, want about %v", left, fast.Lease)
	}
	if _, err := e.admin.Exec(context.Background(), `UPDATE twin.jobs SET lease_expires_at=now()+interval '1 second' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error { return jobs.Heartbeat(ctx, tx, l, fast) }); err != nil {
		t.Fatal(err)
	}
	if left := e.leaseLeft(t, j.ID); left < fast.Lease-5*time.Second || left > fast.Lease {
		t.Errorf("after a heartbeat the lease has %v left, want about %v", left, fast.Lease)
	}
}

// Each retry waits the backoff of the attempt that failed: base, then
// twice the base (T-019 review L6).
func TestRetriesBackOffByAttempt(t *testing.T) {
	e := setup(t)
	j := e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	for attempt := 1; attempt <= 2; attempt++ {
		_, l, ok := e.claim(t, e.org.ID, "worker-1")
		if !ok {
			t.Fatalf("attempt %d was not claimed", attempt)
		}
		if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := jobs.Fail(ctx, tx, l, jobs.Failure{Code: "X", Message: "x", Retryable: true}, fast)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		waitsFor(t, e.get(t, e.org.ID, j.ID), fast.Backoff(attempt))
		e.due(t, j.ID)
	}
	if fast.Backoff(2) != 2*fast.Backoff(1) {
		t.Errorf("the policy's backoff does not double: %v, %v", fast.Backoff(1), fast.Backoff(2))
	}
}

// One reap takes back at most its limit; the next takes the rest (T-019
// review L6).
func TestReapTakesItsLimit(t *testing.T) {
	e := setup(t)
	var ids []string
	for range 3 {
		e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
		j, _, _ := e.claim(t, e.org.ID, "worker-1")
		ids = append(ids, j.ID)
	}
	for _, id := range ids {
		e.expire(t, id)
	}
	for _, want := range []int{2, 1, 0} {
		var requeued, failed int
		if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			requeued, failed, err = jobs.Reap(ctx, tx, e.org.ID, 2, fast)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if requeued+failed != want {
			t.Errorf("a reap with limit 2 took %d, want %d", requeued+failed, want)
		}
	}
}

// T-035: ReapJobs returns each job it took back as the reap left it, so
// the worker can audit it in the same transaction (D-091 (b)); Reap's
// counts are its summary.
func TestReapJobsReturnsWhatItTook(t *testing.T) {
	e := setup(t)
	owner := e.org.Members[tenant.Owner]
	var last jobs.Job
	if err := e.api.Run(context.Background(), tenant.Scope{OrganizationID: e.org.ID, UserID: owner.UserID}, tenant.ReadWrite,
		func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
			imp, err := insertImport(ctx, tx, e.org.ID, e.prj, e.model, e.base, owner.UserID)
			if err != nil {
				return err
			}
			last, err = jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: e.org.ID, Type: jobs.ImportProfile,
				Resource: jobs.Resource{ImportID: imp}, Payload: map[string]any{"import_id": imp}, MaxAttempts: 1}, fast)
			return err
		}); err != nil {
		t.Fatal(err)
	}
	retried := e.enqueue(t, e.org, e.prj, e.model, e.base, 0)
	for range 2 {
		if _, _, ok := e.claim(t, e.org.ID, "worker-1"); !ok {
			t.Fatal("nothing to claim")
		}
	}
	e.expire(t, last.ID)
	e.expire(t, retried.ID)
	var reaped []jobs.Reaped
	if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		reaped, err = jobs.ReapJobs(ctx, tx, e.org.ID, 10, fast)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(reaped) != 2 || reaped[0].Job.ID > reaped[1].Job.ID {
		t.Fatalf("%+v", reaped)
	}
	for _, r := range reaped {
		var f jobs.Failure
		if err := json.Unmarshal(r.Job.Error, &f); err != nil || f != jobs.ReapFailure {
			t.Errorf("%s: failure %+v %v", r.Job.ID, f, err)
		}
		want := r.Job.ID == retried.ID
		if r.Requeued != want || r.Job.Attempt != 1 || r.Job.OrganizationID != e.org.ID || r.Job.Resource.ImportID == "" ||
			r.Job.Type != jobs.ImportProfile || (want && r.Job.Status != jobs.Queued) || (!want && r.Job.Status != jobs.Failed) ||
			(!want && r.Job.MaxAttempts != 1) || len(r.Job.Payload) == 0 {
			t.Errorf("%+v", r)
		}
	}
	// Nothing is left to take.
	if err := e.as(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		reaped, err = jobs.ReapJobs(ctx, tx, e.org.ID, 10, fast)
		return err
	}); err != nil || len(reaped) != 0 {
		t.Errorf("a second reap: %+v %v", reaped, err)
	}
}
