package api_test

// Integration tests of the simulation routes (T-023) through the whole
// stack: the handler cmd/api serves as the plain API login, and the job
// loop cmd/worker runs as the plain worker login with the simulation
// handler, on the disposable database and a filesystem object store
// (TEST-014, TEST-019, TEST-020, TEST-021, TEST-026, TEST-009 for runs).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/runs"
	"github.com/kcl1104/geminus/internal/runs/runner"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
	"github.com/kcl1104/geminus/internal/worker"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// fastPolicy retries soon, so retry tests do not wait for ADR-024's 5 s.
var fastPolicy = jobs.Policy{Lease: 3 * time.Second, MaxAttempts: 3, BackoffBase: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond}

type runEnv struct {
	*env
	objects *objects.FS
	root    string
	scope   *workerscope.Worker
}

// setupRuns is setup with object storage, a short stream poll and the
// worker side: a scope over both organizations as the worker login.
func setupRuns(t *testing.T, opts ...api.Option) *runEnv {
	t.Helper()
	root := t.TempDir()
	fs, err := objects.NewFS(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	opts = append([]api.Option{api.WithObjects(fs), api.WithStream(runs.Stream{Poll: 20 * time.Millisecond, KeepAlive: 200 * time.Millisecond})}, opts...)
	e := setup(t, opts...)
	pool := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.WorkerLogin), 8, true)
	if err := tenant.CheckRuntimeRole(context.Background(), pool, tenant.WorkerRole); err != nil {
		t.Fatal(err)
	}
	scope, err := workerscope.New(pool, []string{e.a.ID, e.b.ID})
	if err != nil {
		t.Fatal(err)
	}
	return &runEnv{env: e, objects: fs, root: root, scope: scope}
}

// loop is a job loop with the simulation handler (and h, if given, in its
// place).
func (e *runEnv) loop(t *testing.T, cfg runner.Config, wrap func(handler.Handler) handler.Handler) *worker.Worker {
	t.Helper()
	packs, err := domainpack.NewRegistry(softwaredelivery.Pack{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Packs == nil {
		cfg.Packs = packs
	}
	if cfg.Objects == nil {
		cfg.Objects = e.objects
	}
	if cfg.Actor == "" {
		cfg.Actor = "test-worker"
	}
	if cfg.Poll == 0 {
		cfg.Poll = 20 * time.Millisecond
	}
	cfg.Log = slog.New(slog.NewJSONHandler(lockedWriter{e.logs, e.logsMu}, nil))
	sim, err := runner.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var h handler.Handler = sim
	if wrap != nil {
		h = wrap(sim)
	}
	w, err := worker.New(e.scope, worker.Config{ID: cfg.Actor, Policy: fastPolicy, Heartbeat: time.Second, Idle: 20 * time.Millisecond,
		ReapLimit: 10, Handlers: map[jobs.Type]handler.Handler{jobs.Simulation: h}, Log: cfg.Log})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// drain passes until a pass runs no job (or d passes).
func drain(t *testing.T, w *worker.Worker, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	idle := 0
	for ctx.Err() == nil && idle < 3 {
		if w.Pass(ctx) {
			idle = 0
		} else {
			idle++
			time.Sleep(30 * time.Millisecond) // a retry's backoff
		}
	}
}

func golden(t *testing.T, name string) map[string]any {
	t.Helper()
	var ir map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "golden-models", name)), &ir); err != nil {
		t.Fatal(err)
	}
	return ir
}

// version commits, as provisioning would, a project and a model whose
// version 1 is ir (with bindings, if any) in org, and returns the model
// and version IDs. Models are created through the API elsewhere (T-013);
// here the IR must be exactly a golden model.
func (e *runEnv) version(t *testing.T, org dbtest.Org, ir map[string]any, bindings []map[string]any) (string, string) {
	t.Helper()
	owner := org.Members[tenant.Owner]
	prj, mdl, mv := ids.New("prj"), ids.New("mdl"), ids.New("mv")
	ir["model_id"] = mdl
	raw, err := json.Marshal(ir)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bindings == nil {
		bindings = []map[string]any{}
	}
	manifest, err := json.Marshal(bindings)
	if err != nil {
		t.Fatal(err)
	}
	e.ownerTx(t, org.ID, func(ctx context.Context, tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, []any{prj, org.ID, owner.UserID}},
			{`INSERT INTO twin.models(id,organization_id,project_id,name,domain,created_by) VALUES ($1,$2,$3,'M','software_delivery',$4)`, []any{mdl, org.ID, prj, owner.UserID}},
			{`INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,ir,hash,dataset_bindings,created_by) VALUES ($1,$2,$3,1,'0.1',$4,$5,$6,$7)`,
				[]any{mv, org.ID, mdl, string(canonical), hashing.Sum(canonical), string(manifest), owner.UserID}},
			{`UPDATE twin.models SET latest_version_id=$1 WHERE id=$2`, []any{mv, mdl}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		for _, b := range bindings {
			if _, err := tx.Exec(ctx, `INSERT INTO twin.model_version_datasets(organization_id,model_id,model_version_id,logical_ref,dataset_version_id,content_hash,unit,sample_count)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, org.ID, mdl, mv, b["logical_ref"], b["dataset_version_id"], b["content_hash"], b["unit"], b["sample_count"]); err != nil {
				return err
			}
		}
		return nil
	})
	return mdl, mv
}

// ownerTx runs fn as the schema owner in org's scope.
func (e *runEnv) ownerTx(t *testing.T, org string, fn func(ctx context.Context, tx pgx.Tx) error) {
	t.Helper()
	if err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,true)`, org); err != nil {
			return err
		}
		return fn(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
}

func runRequest(version string, replications int, trace string, extra map[string]any) string {
	req := map[string]any{"model_version_id": version, "name": "baseline", "scenario_patch": []any{},
		"config": map[string]any{"horizon": map[string]any{"value": 1010, "unit": "minute"}, "warmup": map[string]any{"value": 0, "unit": "minute"},
			"replications": replications, "seed": 7, "trace_level": trace}}
	for k, v := range extra {
		if v == nil {
			delete(req, k)
		} else {
			req[k] = v
		}
	}
	data, _ := json.Marshal(req)
	return string(data)
}

func (e *runEnv) createRun(subject, org, key, body string) resp {
	return e.do("POST", "/v1/simulations", subject, org, map[string]string{"Idempotency-Key": key}, body)
}

func (e *runEnv) cancelRun(subject, org, key, id string) resp {
	return e.do("POST", "/v1/simulations/"+id+"/cancel", subject, org, map[string]string{"Idempotency-Key": key}, "{}")
}

type runBody struct {
	ID                    string         `json:"id"`
	Status                string         `json:"status"`
	ModelVersionID        string         `json:"model_version_id"`
	ScenarioID            *string        `json:"scenario_id"`
	ScenarioPatch         jsontext.Value `json:"scenario_patch"`
	ConfigHash            string         `json:"config_hash"`
	ModelHash             string         `json:"model_hash"`
	EffectiveModelHash    string         `json:"effective_model_hash"`
	CancellationRequested bool           `json:"cancellation_requested"`
	Progress              struct {
		CompletedReplications int   `json:"completed_replications"`
		TotalReplications     int   `json:"total_replications"`
		EventsProcessed       int64 `json:"events_processed"`
		Revision              int64 `json:"revision"`
	} `json:"progress"`
	Error *struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
			Details   []struct {
				Path string `json:"path"`
				Code string `json:"code"`
			} `json:"details"`
		} `json:"error"`
	} `json:"error"`
}

func (e *runEnv) getRun(t *testing.T, subject, org, id string) runBody {
	t.Helper()
	r := e.do("GET", "/v1/simulations/"+id, subject, org, nil, "")
	if r.status != 200 {
		t.Fatalf("GET run: %d %s", r.status, r.body)
	}
	valid(t, r, "simulation.schema.json#/$defs/Run")
	return decode[runBody](t, r)
}

type resultBody struct {
	RunID   string `json:"run_id"`
	Metrics struct {
		Throughput struct {
			Mean *float64 `json:"mean"`
		} `json:"throughput"`
	} `json:"metrics"`
	Reproducibility struct {
		ResultDigest string `json:"result_digest"`
		ConfigHash   string `json:"config_hash"`
	} `json:"reproducibility"`
	Trace struct {
		Available  bool    `json:"available"`
		ArtifactID *string `json:"artifact_id"`
	} `json:"trace"`
}

func (e *runEnv) results(t *testing.T, subject, org, id string) resultBody {
	t.Helper()
	r := e.do("GET", "/v1/simulations/"+id+"/results", subject, org, nil, "")
	if r.status != 200 {
		t.Fatalf("results: %d %s", r.status, r.body)
	}
	valid(t, r, "simulation.schema.json#/$defs/Result")
	return decode[resultBody](t, r)
}

// The closed loop of one run (REQ-011, REQ-012): 202 once the run, its job
// and its audit event are committed; queued; the worker runs it; completed
// with progress done; the Result, digest and sampled trace are served to
// members; the run is listed under its model.
func TestRunClosedLoop(t *testing.T) {
	e := setupRuns(t)
	editor, viewer := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	mdl, mv := e.version(t, e.a, golden(t, "deterministic-queue.json"), nil)
	r := e.createRun(editor.Subject, e.a.ID, "run-key-0001", runRequest(mv, 2, "sampled", nil))
	if r.status != 202 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	valid(t, r, "http.schema.json#/$defs/RunAccepted")
	accepted := decode[struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}](t, r)
	if accepted.Status != "queued" {
		t.Errorf("accepted %+v", accepted)
	}
	run := e.getRun(t, viewer.Subject, e.a.ID, accepted.ID)
	if run.Status != "queued" || run.ModelVersionID != mv || run.ModelHash != run.EffectiveModelHash || run.Progress.TotalReplications != 2 ||
		run.Progress.Revision != 0 || run.ScenarioID != nil || string(run.ScenarioPatch) != "[]" {
		t.Errorf("queued run %+v", run)
	}
	if n := e.count(`SELECT count(*) FROM twin.jobs WHERE simulation_run_id=$1 AND status='queued' AND type='simulation'
		AND payload->>'run_id'=$1 AND payload->>'request_id'=$2`, accepted.ID, r.header.Get("X-Request-ID")); n != 1 {
		t.Errorf("%d queued jobs with the run and request", n)
	}
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE action='simulation.create' AND target_id=$1 AND actor_id=$2 AND request_id=$3`,
		accepted.ID, editor.UserID, r.header.Get("X-Request-ID")); n != 1 {
		t.Errorf("%d create audit events", n)
	}
	if r := e.do("GET", "/v1/simulations/"+accepted.ID+"/results", viewer.Subject, e.a.ID, nil, ""); r.status != 409 || code(t, r) != "RESULT_NOT_READY" {
		t.Errorf("results while queued: %d %s", r.status, r.body)
	}
	if r := e.do("GET", "/v1/simulations/"+accepted.ID+"/trace", viewer.Subject, e.a.ID, nil, ""); r.status != 409 || code(t, r) != "RESULT_NOT_READY" {
		t.Errorf("trace while queued: %d %s", r.status, r.body)
	}

	drain(t, e.loop(t, runner.Config{}, nil), 30*time.Second)
	run = e.getRun(t, viewer.Subject, e.a.ID, accepted.ID)
	if run.Status != "completed" || run.Progress.CompletedReplications != 2 || run.Progress.EventsProcessed == 0 || run.Error != nil {
		t.Fatalf("completed run %+v", run)
	}
	res := e.results(t, viewer.Subject, e.a.ID, accepted.ID)
	if res.RunID != accepted.ID || res.Metrics.Throughput.Mean == nil || *res.Metrics.Throughput.Mean != 100 || res.Reproducibility.ConfigHash != run.ConfigHash ||
		!res.Trace.Available || res.Trace.ArtifactID == nil {
		t.Errorf("result %+v", res)
	}
	tr := e.do("GET", "/v1/simulations/"+accepted.ID+"/trace", viewer.Subject, e.a.ID, nil, "")
	if tr.status != 200 || tr.header.Get("Content-Disposition") != `attachment; filename="`+accepted.ID+`-trace.json"` || tr.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("trace: %d %v %s", tr.status, tr.header, tr.body)
	}
	valid(t, tr, "simulation.schema.json#/$defs/TraceArtifact")
	var stored string
	if err := e.admin.QueryRow(context.Background(), `SELECT trace_content_hash FROM twin.simulation_results WHERE run_id=$1`, accepted.ID).Scan(&stored); err != nil || stored != hashing.ContentHash(tr.body) {
		t.Errorf("served trace hash %s, stored %s (%v)", hashing.ContentHash(tr.body), stored, err)
	}
	// A stored trace that is not the one its row names is never served:
	// altered bytes of the same size, then a missing object, are 500.
	var key string
	if err := e.admin.QueryRow(context.Background(), `SELECT trace_object_key FROM twin.simulation_results WHERE run_id=$1`, accepted.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.root, filepath.FromSlash(key))
	altered := bytes.Replace(tr.body, []byte(`"`), []byte(`'`), 1)
	if err := os.WriteFile(path, altered, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"altered", "missing"} {
		if step == "missing" {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		if r := e.do("GET", "/v1/simulations/"+accepted.ID+"/trace", viewer.Subject, e.a.ID, nil, ""); r.status != 500 || code(t, r) != "INTERNAL_ERROR" || bytes.Contains(r.body, []byte(`"events"`)) {
			t.Errorf("a %s trace: %d %s", step, r.status, r.body)
		}
	}
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE action='simulation.complete' AND target_id=$1 AND actor_type='worker' AND actor_id='test-worker'
		AND request_id=$2 AND metadata->>'result_digest'=$3`, accepted.ID, r.header.Get("X-Request-ID"), res.Reproducibility.ResultDigest); n != 1 {
		t.Errorf("%d complete audit events", n)
	}
	list := e.do("GET", "/v1/models/"+mdl+"/simulations", viewer.Subject, e.a.ID, nil, "")
	if list.status != 200 {
		t.Fatalf("list: %d %s", list.status, list.body)
	}
	valid(t, list, "http.schema.json#/$defs/Runs")
	if page := decode[struct {
		Items []runBody `json:"items"`
	}](t, list); len(page.Items) != 1 || page.Items[0].ID != accepted.ID || page.Items[0].Status != "completed" {
		t.Errorf("list %+v", page)
	}
}

// Who may do what with runs (TEST-009 for runs): writers create and
// cancel, every member reads; another organization's member and a stranger
// see 404 everywhere, the run's routes included; a viewer's write is 403.
func TestRunRoleMatrix(t *testing.T) {
	e := setupRuns(t)
	owner, editor, viewer := e.a.Members[tenant.Owner], e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	outsider := e.b.Members[tenant.Owner]
	stranger := dbtest.NewUser(t, e.admin, e.p.Issuer)
	mdl, mv := e.version(t, e.a, golden(t, "deterministic-queue.json"), nil)
	_, mvB := e.version(t, e.b, golden(t, "deterministic-queue.json"), nil)
	for i, c := range []struct {
		name, subject, org, version string
		status                      int
		code                        string
	}{
		{"owner", owner.Subject, e.a.ID, mv, 202, ""},
		{"editor", editor.Subject, e.a.ID, mv, 202, ""},
		{"viewer", viewer.Subject, e.a.ID, mv, 403, "FORBIDDEN"},
		{"member of another organization", outsider.Subject, e.a.ID, mv, 404, "NOT_FOUND"},
		{"another organization's version", owner.Subject, e.a.ID, mvB, 404, "NOT_FOUND"},
		{"a version no one has", owner.Subject, e.a.ID, ids.New("mv"), 404, "NOT_FOUND"},
		{"stranger", stranger.Subject, e.a.ID, mv, 404, "NOT_FOUND"},
		{"no token", "", e.a.ID, mv, 401, "UNAUTHENTICATED"},
	} {
		r := e.createRun(c.subject, c.org, fmt.Sprintf("run-role-%02d", i), runRequest(c.version, 1, "none", nil))
		if r.status != c.status || (c.code != "" && code(t, r) != c.code) {
			t.Errorf("create, %s: %d %s", c.name, r.status, r.body)
		}
	}
	if n := e.count(`SELECT count(*) FROM twin.simulation_runs WHERE organization_id=$1`, e.a.ID); n != 2 {
		t.Errorf("%d runs in a, want 2", n)
	}
	if n := e.count(`SELECT count(*) FROM twin.simulation_runs WHERE organization_id=$1`, e.b.ID); n != 0 {
		t.Errorf("%d runs in b", n)
	}
	run := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-role-read", runRequest(mv, 1, "sampled", nil)))
	drain(t, e.loop(t, runner.Config{}, nil), 30*time.Second)
	reads := []string{"/v1/simulations/" + run.ID, "/v1/simulations/" + run.ID + "/results", "/v1/simulations/" + run.ID + "/trace",
		"/v1/models/" + mdl + "/simulations", "/v1/simulations/" + run.ID + "/events"}
	for _, s := range []string{owner.Subject, editor.Subject, viewer.Subject} {
		for _, path := range reads {
			if r := e.do("GET", path, s, e.a.ID, nil, ""); r.status != 200 {
				t.Errorf("GET %s as a member: %d %s", path, r.status, r.body)
			}
		}
	}
	for name, c := range map[string][2]string{
		"other org's member in its own org": {outsider.Subject, e.b.ID},
		"other org's member naming a":       {outsider.Subject, e.a.ID},
		"stranger":                          {stranger.Subject, e.a.ID},
	} {
		for _, path := range reads {
			if r := e.do("GET", path, c[0], c[1], nil, ""); r.status != 404 || code(t, r) != "NOT_FOUND" {
				t.Errorf("GET %s, %s: %d %s", path, name, r.status, r.body)
			}
		}
		if r := e.cancelRun(c[0], c[1], "run-cancel-"+c[1][4:12], run.ID); r.status != 404 || code(t, r) != "NOT_FOUND" {
			t.Errorf("cancel, %s: %d %s", name, r.status, r.body)
		}
	}
	if r := e.cancelRun(viewer.Subject, e.a.ID, "run-cancel-viewer", run.ID); r.status != 403 || code(t, r) != "FORBIDDEN" {
		t.Errorf("cancel as a viewer: %d %s", r.status, r.body)
	}
	for _, path := range []string{"/v1/simulations/sim_bad", "/v1/simulations/" + ids.New("mv"), "/v1/models/sim_bad/simulations"} {
		if r := e.do("GET", path, viewer.Subject, e.a.ID, nil, ""); r.status != 400 || code(t, r) != "INVALID_REQUEST" {
			t.Errorf("GET %s: %d %s", path, r.status, r.body)
		}
	}
	if r := e.do("GET", "/v1/simulations/"+ids.New("sim"), viewer.Subject, e.a.ID, nil, ""); r.status != 404 {
		t.Errorf("a run no one has: %d %s", r.status, r.body)
	}
	if r := e.do("GET", "/v1/models/"+ids.New("mdl")+"/simulations", viewer.Subject, e.a.ID, nil, ""); r.status != 404 {
		t.Errorf("the runs of a model no one has: %d %s", r.status, r.body)
	}
}

// A run request is refused before anything is written when it names a
// draft or something malformed (schema: 422 at the field), a configuration
// the engine cannot run (422 REQUEST_VALIDATION_FAILED at the field), a
// scenario patch outside the scenario allowlist or one that makes the model
// invalid (422 MODEL_VALIDATION_FAILED with the patch's or the model's
// paths), or a scenario that does not exist (404) or patches another
// version (422 at /scenario_id). Without object storage a run that records
// a trace is 503, one that does not runs.
func TestRunRequestRefusals(t *testing.T) {
	e := setupRuns(t)
	editor := e.a.Members[tenant.Editor]
	mdl, mv := e.version(t, e.a, golden(t, "deterministic-queue.json"), nil)
	_, other := e.version(t, e.a, golden(t, "deterministic-queue.json"), nil)
	scn := ids.New("scn")
	e.ownerTx(t, e.a.ID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO twin.scenarios(id,organization_id,model_id,base_version_id,name,patch,patch_hash,created_by)
			VALUES ($1,$2,(SELECT model_id FROM twin.model_versions WHERE id=$3),$3,'other version','[]',$4,$5)`,
			scn, e.a.ID, other, "sha256:4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945", editor.UserID)
		return err
	})
	config := func(h, w float64, unit string) map[string]any {
		return map[string]any{"horizon": map[string]any{"value": h, "unit": unit}, "warmup": map[string]any{"value": w, "unit": "minute"},
			"replications": 1, "seed": 7, "trace_level": "none"}
	}
	for i, c := range []struct {
		name   string
		body   string
		status int
		code   string
		path   string
	}{
		{"a draft: the model's ID", runRequest(mdl, 1, "none", nil), 422, "REQUEST_VALIDATION_FAILED", "/model_version_id"},
		{"replications over 1000", runRequest(mv, 1001, "none", nil), 422, "REQUEST_VALIDATION_FAILED", "/config/replications"},
		{"no replication", runRequest(mv, 0, "none", nil), 422, "REQUEST_VALIDATION_FAILED", "/config/replications"},
		{"both a patch and a scenario", runRequest(mv, 1, "none", map[string]any{"scenario_id": scn}), 422, "REQUEST_VALIDATION_FAILED", ""},
		{"neither", runRequest(mv, 1, "none", map[string]any{"scenario_patch": nil}), 422, "REQUEST_VALIDATION_FAILED", ""},
		{"an unknown member", runRequest(mv, 1, "none", map[string]any{"limits": map[string]any{"max_events": 1}}), 422, "REQUEST_VALIDATION_FAILED", ""},
		{"warmup as long as the horizon", runRequest(mv, 1, "none", map[string]any{"config": config(60, 60, "minute")}), 422, "REQUEST_VALIDATION_FAILED", "/config/warmup"},
		{"a horizon over 365 days", runRequest(mv, 1, "none", map[string]any{"config": config(366, 0, "day")}), 422, "REQUEST_VALIDATION_FAILED", "/config/horizon"},
		{"a patch outside the scenario allowlist", runRequest(mv, 1, "none", map[string]any{"scenario_patch": []any{
			map[string]any{"op": "replace", "path": "/name", "value": "x"}}}), 422, "MODEL_VALIDATION_FAILED", "/scenario_patch/0/path"},
		{"a patch that breaks the model", runRequest(mv, 1, "none", map[string]any{"scenario_patch": []any{
			map[string]any{"op": "test", "path": "/flows/0/id", "value": "a_s"},
			map[string]any{"op": "replace", "path": "/flows/0/routing/probability", "value": 0.5}}}), 422, "MODEL_VALIDATION_FAILED", ""},
		{"a scenario no one has", runRequest(mv, 1, "none", map[string]any{"scenario_patch": nil, "scenario_id": ids.New("scn")}), 404, "NOT_FOUND", ""},
		{"a scenario of another version", runRequest(mv, 1, "none", map[string]any{"scenario_patch": nil, "scenario_id": scn}), 422, "REQUEST_VALIDATION_FAILED", "/scenario_id"},
	} {
		r := e.createRun(editor.Subject, e.a.ID, fmt.Sprintf("run-refuse-%02d", i), c.body)
		if r.status != c.status || code(t, r) != c.code {
			t.Errorf("%s: %d %s", c.name, r.status, r.body)
			continue
		}
		if c.path != "" && !strings.Contains(string(r.body), `"path":"`+c.path+`"`) {
			t.Errorf("%s: no detail at %s: %s", c.name, c.path, r.body)
		}
	}
	if r := e.do("POST", "/v1/simulations", editor.Subject, e.a.ID, nil, runRequest(mv, 1, "none", nil)); r.status != 400 || code(t, r) != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Errorf("no Idempotency-Key: %d %s", r.status, r.body)
	}
	if r := e.do("POST", "/v1/simulations", editor.Subject, e.a.ID, map[string]string{"Idempotency-Key": "run-refuse-ct", "Content-Type": "text/plain"}, runRequest(mv, 1, "none", nil)); r.status != 415 {
		t.Errorf("text/plain: %d %s", r.status, r.body)
	}
	if n := e.count(`SELECT count(*) FROM twin.simulation_runs WHERE organization_id=$1`, e.a.ID); n != 0 {
		t.Errorf("%d runs were written by refused requests", n)
	}
	if n := e.count(`SELECT count(*) FROM twin.jobs WHERE organization_id=$1`, e.a.ID); n != 0 {
		t.Errorf("%d jobs were written by refused requests", n)
	}

	plain := setup(t) // no object storage
	_, mvPlain := (&runEnv{env: plain}).version(t, plain.a, golden(t, "deterministic-queue.json"), nil)
	if r := plain.do("POST", "/v1/simulations", plain.a.Members[tenant.Editor].Subject, plain.a.ID, map[string]string{"Idempotency-Key": "run-no-store-1"},
		runRequest(mvPlain, 1, "sampled", nil)); r.status != 503 || code(t, r) != "SERVICE_UNAVAILABLE" {
		t.Errorf("a sampled trace without object storage: %d %s", r.status, r.body)
	}
	if r := plain.do("POST", "/v1/simulations", plain.a.Members[tenant.Editor].Subject, plain.a.ID, map[string]string{"Idempotency-Key": "run-no-store-2"},
		runRequest(mvPlain, 1, "none", nil)); r.status != 202 {
		t.Errorf("no trace without object storage: %d %s", r.status, r.body)
	}
}

// Idempotency-Key (D-011): the same key and body replay the first answer
// and create one run and one job, racing retries included; another body is
// 409; the key is the user's own.
func TestRunIdempotency(t *testing.T) {
	e := setupRuns(t)
	owner, editor := e.a.Members[tenant.Owner], e.a.Members[tenant.Editor]
	_, mv := e.version(t, e.a, golden(t, "deterministic-queue.json"), nil)
	body := runRequest(mv, 1, "none", nil)
	first := e.createRun(editor.Subject, e.a.ID, "run-idem-0001", body)
	if first.status != 202 {
		t.Fatalf("%d %s", first.status, first.body)
	}
	const n = 12
	answers := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			r := e.createRun(editor.Subject, e.a.ID, "run-idem-0001", body)
			answers[i] = fmt.Sprint(r.status, string(r.body))
		})
	}
	wg.Wait()
	for _, a := range answers {
		if a != fmt.Sprint(202, string(first.body)) {
			t.Errorf("a retry answered %s, want %d %s", a, 202, first.body)
		}
	}
	if r := e.createRun(editor.Subject, e.a.ID, "run-idem-0001", runRequest(mv, 2, "none", nil)); r.status != 409 || code(t, r) != "IDEMPOTENCY_KEY_CONFLICT" {
		t.Errorf("same key, other body: %d %s", r.status, r.body)
	}
	if r := e.createRun(owner.Subject, e.a.ID, "run-idem-0001", body); r.status != 202 || string(r.body) == string(first.body) {
		t.Errorf("another user's same key: %d %s", r.status, r.body)
	}
	if c := e.count(`SELECT count(*) FROM twin.simulation_runs WHERE organization_id=$1`, e.a.ID); c != 2 {
		t.Errorf("%d runs, want 2 (one per user)", c)
	}
	if c := e.count(`SELECT count(*) FROM twin.jobs WHERE organization_id=$1 AND type='simulation'`, e.a.ID); c != 2 {
		t.Errorf("%d jobs, want 2", c)
	}
}

// API_DATABASE §7: the role is checked before a replay, so an earlier
// success is no pass after the member was demoted or removed. The same key
// and body that answered 202 (create) or 200 (cancel) answer 403 to a
// viewer and 404 to a former member, and nothing more is written.
func TestARevokedMemberGetsNoReplay(t *testing.T) {
	e := setupRuns(t)
	editor, owner := e.a.Members[tenant.Editor], e.a.Members[tenant.Owner]
	_, mv := e.version(t, e.a, golden(t, "deterministic-queue.json"), nil)
	body := runRequest(mv, 1, "none", nil)
	created := e.createRun(editor.Subject, e.a.ID, "run-revoke-0001", body)
	if created.status != 202 {
		t.Fatalf("%d %s", created.status, created.body)
	}
	id := decode[runBody](t, created).ID
	cancelled := e.cancelRun(editor.Subject, e.a.ID, "cancel-revoke-0001", id)
	if cancelled.status != 200 {
		t.Fatalf("%d %s", cancelled.status, cancelled.body)
	}
	ownerRun := decode[runBody](t, e.createRun(owner.Subject, e.a.ID, "run-revoke-0002", body)).ID
	ownerCancel := e.cancelRun(owner.Subject, e.a.ID, "cancel-revoke-0002", ownerRun)
	if ownerCancel.status != 200 {
		t.Fatalf("%d %s", ownerCancel.status, ownerCancel.body)
	}
	before := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1`, e.a.ID)
	if _, err := e.admin.Exec(context.Background(), `UPDATE twin.memberships SET role='viewer' WHERE organization_id=$1 AND user_id=$2`, e.a.ID, editor.UserID); err != nil {
		t.Fatal(err)
	}
	if r := e.createRun(editor.Subject, e.a.ID, "run-revoke-0001", body); r.status != 403 || code(t, r) != "FORBIDDEN" {
		t.Errorf("a demoted member's create replay: %d %s", r.status, r.body)
	}
	if r := e.cancelRun(editor.Subject, e.a.ID, "cancel-revoke-0001", id); r.status != 403 || code(t, r) != "FORBIDDEN" {
		t.Errorf("a demoted member's cancel replay: %d %s", r.status, r.body)
	}
	if _, err := e.admin.Exec(context.Background(), `DELETE FROM twin.memberships WHERE organization_id=$1 AND user_id=$2`, e.a.ID, owner.UserID); err != nil {
		t.Fatal(err)
	}
	if r := e.createRun(owner.Subject, e.a.ID, "run-revoke-0002", body); r.status != 404 || code(t, r) != "NOT_FOUND" {
		t.Errorf("a removed member's create replay: %d %s", r.status, r.body)
	}
	if r := e.cancelRun(owner.Subject, e.a.ID, "cancel-revoke-0002", ownerRun); r.status != 404 || code(t, r) != "NOT_FOUND" {
		t.Errorf("a removed member's cancel replay: %d %s", r.status, r.body)
	}
	if n := e.count(`SELECT count(*) FROM twin.simulation_runs WHERE organization_id=$1`, e.a.ID); n != 2 {
		t.Errorf("%d runs, want the 2 created before the revocations", n)
	}
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1`, e.a.ID); n != before {
		t.Errorf("%d audit events after the refused replays, %d before", n, before)
	}
}

// keep the imports the later tests use
var (
	_ = bufio.NewReader
	_ = fmt.Sprintf
	_ = io.ReadAll
	_ = http.MethodGet
	_ = os.Getenv
	_ = strings.Contains
	_ = sync.Mutex{}
	_ simulation.TraceLevel
	_ *pgxpool.Pool
)
