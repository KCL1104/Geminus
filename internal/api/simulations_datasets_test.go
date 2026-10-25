package api_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/runs/runner"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/tenant"
)

// dataset stores a snapshot of samples as a new version of dataset ds in
// org (its object in object storage, its row naming it) and returns the
// version's binding.
func (e *runEnv) dataset(t *testing.T, org datasetOwner, ds string, version int, samples []float64) map[string]any {
	t.Helper()
	dsv := ids.New("dsv")
	snap, err := json.Marshal(map[string]any{"dataset_version_id": dsv, "organization_id": org.id, "logical_ref": "dataset:service_minutes",
		"unit": "minute", "samples": samples})
	if err != nil {
		t.Fatal(err)
	}
	key, err := objects.NewKey(org.id, objects.Datasets, dsv+".json")
	if err != nil {
		t.Fatal(err)
	}
	obj, err := e.objects.Put(context.Background(), key, bytes.NewReader(snap), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	e.ownerTx(t, org.id, func(ctx context.Context, tx pgx.Tx) error {
		if version == 1 {
			var project string
			if err := tx.QueryRow(ctx, `SELECT id FROM twin.projects WHERE organization_id=$1 ORDER BY created_at LIMIT 1`, org.id).Scan(&project); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO twin.datasets(id,organization_id,project_id,kind,name,created_by)
				VALUES ($1,$2,$3,'empirical_samples','service minutes',$4)`, ds, org.id, project, org.owner); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO twin.dataset_versions(id,organization_id,dataset_id,version,object_key,content_hash,content_type,row_count,unit,created_by)
			VALUES ($1,$2,$3,$4,$5,$6,'application/json',$7,'minute',$8)`, dsv, org.id, ds, version, key, obj.SHA256, len(samples), org.owner)
		return err
	})
	return map[string]any{"logical_ref": "dataset:service_minutes", "dataset_version_id": dsv, "content_hash": obj.SHA256, "unit": "minute", "sample_count": len(samples)}
}

type datasetOwner struct{ id, owner string }

// TEST-014: a run binds its version's frozen dataset manifest. A later
// dataset version changes neither a stored result nor a new run of the
// same version; bytes that no longer match their hash are refused when a
// run is requested, and fail a queued run when the worker loads them; a
// scenario patch cannot point a distribution at a dataset the version
// does not bind.
func TestRunDatasets(t *testing.T) {
	e := setupRuns(t)
	editor := e.a.Members[tenant.Editor]
	org := datasetOwner{e.a.ID, e.a.Members[tenant.Owner].UserID}
	// A version without datasets first: it also gives the organization the
	// project the dataset belongs to.
	_, plainMV := e.version(t, e.a, golden(t, "deterministic-queue.json"), nil)
	ir := golden(t, "deterministic-queue.json")
	nodes := ir["nodes"].([]any)
	nodes[1].(map[string]any)["service_time"] = map[string]any{"distribution": map[string]any{"type": "empirical", "samples_ref": "dataset:service_minutes"}}
	ds := ids.New("ds")
	b1 := e.dataset(t, org, ds, 1, []float64{3, 5, 7})
	_, mv := e.version(t, e.a, ir, []map[string]any{b1})

	first := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-ds-0001", runRequest(mv, 2, "none", nil))).ID
	w := e.loop(t, runner.Config{}, nil)
	drain(t, w, 30*time.Second)
	run := e.getRun(t, editor.Subject, e.a.ID, first)
	if run.Status != "completed" {
		t.Fatalf("run %+v", run)
	}
	got := e.do("GET", "/v1/simulations/"+first, editor.Subject, e.a.ID, nil, "")
	if !strings.Contains(string(got.body), `"dataset_version_id":"`+b1["dataset_version_id"].(string)+`"`) {
		t.Errorf("the run does not show its frozen binding: %s", got.body)
	}
	if n := e.count(`SELECT count(*) FROM twin.run_datasets WHERE run_id=$1 AND dataset_version_id=$2`, first, b1["dataset_version_id"]); n != 1 {
		t.Error("the run's binding row is missing")
	}
	digest := e.results(t, editor.Subject, e.a.ID, first).Reproducibility.ResultDigest

	// Later data is a new dataset version; the version still binds the first.
	e.dataset(t, org, ds, 2, []float64{50, 60})
	second := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-ds-0002", runRequest(mv, 2, "none", nil))).ID
	queued := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-ds-0003", runRequest(mv, 2, "none", nil))).ID
	drainOne(t, w, second, e, editor.Subject)
	if d := e.results(t, editor.Subject, e.a.ID, second).Reproducibility.ResultDigest; d != digest {
		t.Errorf("a run after new data: digest %s, want %s", d, digest)
	}
	if d := e.results(t, editor.Subject, e.a.ID, first).Reproducibility.ResultDigest; d != digest {
		t.Errorf("the stored result changed: %s", d)
	}

	// The frozen bytes change under their key.
	key, _ := objects.NewKey(org.id, objects.Datasets, b1["dataset_version_id"].(string)+".json")
	path := filepath.Join(e.root, filepath.FromSlash(key))
	if err := os.WriteFile(path, []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := e.createRun(editor.Subject, e.a.ID, "run-ds-0004", runRequest(mv, 2, "none", nil))
	if r.status != 422 || code(t, r) != "MODEL_VALIDATION_FAILED" || !strings.Contains(string(r.body), `"path":"/dataset_bindings/0/content_hash"`) ||
		!strings.Contains(string(r.body), `"code":"DATASET_BINDING_INVALID"`) {
		t.Errorf("changed bytes: %d %s", r.status, r.body)
	}
	drain(t, w, 30*time.Second)
	failed := e.getRun(t, editor.Subject, e.a.ID, queued)
	if failed.Status != "failed" || failed.Error == nil || failed.Error.Error.Code != "MODEL_VALIDATION_FAILED" || failed.Error.Error.Details[0].Code != "RUN_INPUT_INVALID" {
		t.Errorf("a queued run whose datasets changed: %+v %+v", failed, failed.Error)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if r := e.createRun(editor.Subject, e.a.ID, "run-ds-0005", runRequest(mv, 2, "none", nil)); r.status != 422 || !strings.Contains(string(r.body), `"path":"/dataset_bindings/0/dataset_version_id"`) {
		t.Errorf("a missing object: %d %s", r.status, r.body)
	}
	// A patch may not bring in a dataset the version does not bind.
	if r := e.createRun(editor.Subject, e.a.ID, "run-ds-0006", runRequest(plainMV, 1, "none", map[string]any{"scenario_patch": []any{
		map[string]any{"op": "test", "path": "/nodes/1/id", "value": "service"},
		map[string]any{"op": "replace", "path": "/nodes/1/service_time/distribution", "value": map[string]any{"type": "empirical", "samples_ref": "dataset:service_minutes"}},
	}})); r.status != 422 || code(t, r) != "MODEL_VALIDATION_FAILED" || !strings.Contains(string(r.body), "/scenario_patch/") {
		t.Errorf("an unbound dataset through a patch: %d %s", r.status, r.body)
	}
}

// flakyObjects is object storage whose reads fail with err while fail has
// set one.
type flakyObjects struct {
	objects.Store
	mu    sync.Mutex
	err   error
	reads int
}

func (f *flakyObjects) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	f.reads++
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return f.Store.Get(ctx, key)
}

func (f *flakyObjects) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err, f.reads = err, 0
}

func (f *flakyObjects) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

// H-022, D-069 (b) (T-054), TEST-009: object storage that cannot read a
// snapshot the version binds, through the handler cmd/api serves. 503
// SERVICE_UNAVAILABLE with details[0].code DATASET_STORAGE_FAILED, as
// approveProposal and createManualScenario answer; the cause is logged,
// never sent; a read that says the request ended is the context's 503,
// without details; nothing is written either way (no run, job, binding
// row, audit event or answer kept for the key), so the same request with
// the same key is queued once storage answers again.
func TestRunSnapshotStorageFailure(t *testing.T) {
	root := t.TempDir()
	fs, err := objects.NewFS(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	flaky := &flakyObjects{Store: fs}
	e := setupRuns(t, api.WithObjects(flaky))
	e.objects, e.root = fs, root // the snapshots are written here; the API reads them through flaky
	editor := e.a.Members[tenant.Editor]
	org := datasetOwner{e.a.ID, e.a.Members[tenant.Owner].UserID}
	e.version(t, e.a, golden(t, "deterministic-queue.json"), nil) // the organization's project
	ir := golden(t, "deterministic-queue.json")
	ir["nodes"].([]any)[1].(map[string]any)["service_time"] = map[string]any{"distribution": map[string]any{"type": "empirical", "samples_ref": "dataset:service_minutes"}}
	b1 := e.dataset(t, org, ids.New("ds"), 1, []float64{3, 5, 7})
	_, mv := e.version(t, e.a, ir, []map[string]any{b1})
	const key = "run-storage-0001"
	nothing := func(when string) {
		t.Helper()
		for q, args := range map[string][]any{
			`SELECT count(*) FROM twin.simulation_runs WHERE organization_id=$1`:                                         {e.a.ID},
			`SELECT count(*) FROM twin.jobs WHERE organization_id=$1`:                                                    {e.a.ID},
			`SELECT count(*) FROM twin.run_datasets WHERE organization_id=$1`:                                            {e.a.ID},
			`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND action='simulation.create'`:             {e.a.ID},
			`SELECT count(*) FROM twin.idempotency_keys WHERE organization_id=$1 AND operation_scope='createSimulation'`: {e.a.ID},
		} {
			if n := e.count(q, args...); n != 0 {
				t.Errorf("%s: %s is %d", when, q, n)
			}
		}
	}

	const cause = "open /srv/objects/tenant/secret: input/output error"
	flaky.fail(errors.New(cause))
	r := e.createRun(editor.Subject, e.a.ID, key, runRequest(mv, 1, "none", nil))
	if r.status != 503 || code(t, r) != "SERVICE_UNAVAILABLE" {
		t.Fatalf("storage that fails: %d %s", r.status, r.body)
	}
	got := decode[struct {
		Error struct {
			Message string `json:"message"`
			Details []struct {
				Path   string `json:"path"`
				Reason string `json:"reason"`
				Code   string `json:"code"`
			} `json:"details"`
		} `json:"error"`
	}](t, r)
	if got.Error.Message != "the dataset snapshots could not be read; nothing was queued (retry later)" || len(got.Error.Details) != 1 ||
		got.Error.Details[0].Path != "" || got.Error.Details[0].Reason != "the dataset storage failed" || got.Error.Details[0].Code != "DATASET_STORAGE_FAILED" {
		t.Errorf("storage that fails: %s", r.body)
	}
	if flaky.count() == 0 {
		t.Error("the snapshot was not read")
	}
	if strings.Contains(string(r.body), "secret") {
		t.Errorf("the cause is sent: %s", r.body)
	}
	e.logsMu.Lock()
	logs := e.logs.String()
	e.logsMu.Unlock()
	if !slices.ContainsFunc(strings.Split(strings.TrimSpace(logs), "\n"), func(line string) bool {
		var entry map[string]any
		return json.Unmarshal([]byte(line), &entry) == nil && entry["level"] == "WARN" && entry["request_id"] == r.header.Get("X-Request-ID") &&
			entry["model_version_id"] == mv && strings.Contains(fmt.Sprint(entry["error"]), cause)
	}) {
		t.Errorf("the cause is not logged with the request: %s", logs)
	}
	nothing("storage that fails")

	// A read that says the request ended: the context's error, which
	// httpapi answers as it answers any ended request.
	flaky.fail(fmt.Errorf("reading: %w", context.DeadlineExceeded))
	r = e.createRun(editor.Subject, e.a.ID, key, runRequest(mv, 1, "none", nil))
	if r.status != 503 || code(t, r) != "SERVICE_UNAVAILABLE" || strings.Contains(string(r.body), "DATASET_STORAGE_FAILED") {
		t.Errorf("a read past its deadline: %d %s", r.status, r.body)
	}
	nothing("a read past its deadline")

	// Storage answers again: the same request with the same key is queued.
	flaky.fail(nil)
	r = e.createRun(editor.Subject, e.a.ID, key, runRequest(mv, 1, "none", nil))
	if r.status != 202 {
		t.Fatalf("after storage recovers: %d %s", r.status, r.body)
	}
	id := decode[runBody](t, r).ID
	if n := e.count(`SELECT count(*) FROM twin.simulation_runs WHERE organization_id=$1`, e.a.ID); n != 1 {
		t.Errorf("%d runs after recovery", n)
	}
	if n := e.count(`SELECT count(*) FROM twin.jobs WHERE simulation_run_id=$1 AND status='queued'`, id); n != 1 {
		t.Errorf("%d queued jobs after recovery", n)
	}
	if n := e.count(`SELECT count(*) FROM twin.run_datasets WHERE run_id=$1 AND dataset_version_id=$2`, id, b1["dataset_version_id"]); n != 1 {
		t.Errorf("%d binding rows after recovery", n)
	}
}

// drainOne runs passes until run id has finished.
func drainOne(t *testing.T, w interface{ Pass(context.Context) bool }, id string, e *runEnv, subject string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		w.Pass(context.Background())
		if s := e.getRun(t, subject, e.a.ID, id).Status; s == "completed" || s == "failed" || s == "cancelled" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not finish", id)
		}
	}
}
