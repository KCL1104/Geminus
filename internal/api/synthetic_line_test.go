package api_test

// T-052 (hackathon slice, H-029): the synthetic station log of the
// electronics line (testdata/datasets/electronics-line, generated from the
// pack's template by scripts/gen-electronics-line-data) through the whole
// stack: the import routes and the profile and validation jobs (T-016,
// T-017) with the packs the worker serves, whose data-quality rules of
// D-059 are warnings (D-072 (b)); and runs of the template, each bound to
// its immutable version, the version's dataset manifest and a seed. Every
// file here is synthetic.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/import/mapping"
	importprofile "github.com/kcl1104/geminus/internal/import/profile"
	"github.com/kcl1104/geminus/internal/import/profiler"
	"github.com/kcl1104/geminus/internal/import/validation"
	"github.com/kcl1104/geminus/internal/import/validator"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/packs"
	"github.com/kcl1104/geminus/internal/runs/runner"
	"github.com/kcl1104/geminus/internal/tenant"
)

// lineData reads a file of the synthetic data set.
func lineData(t *testing.T, name string) []byte {
	t.Helper()
	return contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "datasets", "electronics-line", filepath.FromSlash(name)))
}

// lineColumns are the log's columns (its README); lineMapping maps them as
// D-059 says: the operator is not imported, synthetic goes to metadata.
var lineColumns = []string{"serial", "record_type", "station", "equipment", "start", "end", "result", "shift", "product", "operator", "synthetic"}

func lineMapping(t *testing.T, rev int64) string {
	t.Helper()
	return mappingBody(t, rev, nil, col{"serial", "case_id", ""}, col{"record_type", "event_type", ""}, col{"station", "stage", ""},
		col{"equipment", "resource_id", ""}, col{"start", "timestamp", "rfc3339"}, col{"end", "end_timestamp", "rfc3339"},
		col{"result", "outcome", ""}, col{"shift", "shift", ""}, col{"product", "product", ""}, col{"operator", "", ""},
		col{"synthetic", "metadata", ""})
}

// lineModel creates a model from the electronics line template over HTTP
// as the editor: its version 1 is the ground truth of the data set.
func (e *runEnv) lineModel(t *testing.T) modelSummary {
	t.Helper()
	editor := e.a.Members[tenant.Editor]
	r := e.create(editor.Subject, e.a.ID, "key-"+t.Name()+"-prj", `{"name":"Synthetic line"}`)
	if r.status != 201 {
		t.Fatalf("project: %d %s", r.status, r.body)
	}
	prj := decode[project](t, r)
	r = e.do("POST", "/v1/projects/"+prj.ID+"/models", editor.Subject, e.a.ID, map[string]string{"Idempotency-Key": "key-" + t.Name() + "-mdl"},
		`{"name":"Synthetic line","domain":"electronics_line","template":"electronics_assembly_test_line"}`)
	if r.status != 201 {
		t.Fatalf("model: %d %s", r.status, r.body)
	}
	return decode[modelSummary](t, r)
}

// importLog uploads data as the editor, profiles it, saves lineMapping
// and validates it with the packs the worker serves; it returns the
// import as the viewer reads it.
func (e *runEnv) importLog(t *testing.T, m modelSummary, key, filename string, data []byte) (string, mappedImport) {
	t.Helper()
	served, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	editor, viewer := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	id := e.uploaded(t, m, key, "csv_events", filename, string(data))
	drain(t, e.profileLoop(t, profiler.Config{Objects: e.objects}, nil), 120*time.Second)
	if got := e.getMapped(t, viewer.Subject, id); got.Status != "AWAITING_MAPPING" {
		t.Fatalf("%s after profiling: %+v", filename, got)
	}
	if r := e.saveMapping(editor.Subject, id, key+"-map", lineMapping(t, 0)); r.status != 202 {
		t.Fatalf("%s mapping: %d %s", filename, r.status, r.body)
	}
	drain(t, e.validateLoop(t, validator.Config{Objects: e.objects, Packs: served}, nil), 120*time.Second)
	return id, e.getMapped(t, viewer.Subject, id)
}

// lineRows is the log's data rows.
func lineRows(t *testing.T, data []byte) [][]string {
	t.Helper()
	recs, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(recs[0], lineColumns) {
		t.Fatalf("header %v", recs[0])
	}
	return recs[1:]
}

// The synthetic log through the import path (TEST-012, TEST-013 on T-052's
// data): profiled as 22,739 rows of eleven columns, the two times as
// timestamps; mapped as D-059 says; every row valid and no data-quality
// warning (the log keeps the pack's rules for its own model), so the import
// is VALIDATED with its canonical dataset: one schema-valid event per row in
// row order, the operator nowhere in it, every event marked synthetic, the
// bytes the validation package writes for that file and mapping. The
// import never changes the model (D-009). Its commit, which freezes the
// dataset as a dataset version, is T-018's and not served yet.
func TestSyntheticStationLogImport(t *testing.T) {
	e := setupRuns(t)
	ctx := context.Background()
	m := e.lineModel(t)
	before := e.twin(t, m.ID)
	data := lineData(t, "station-log-synthetic.csv")
	rows := lineRows(t, data)
	id, got := e.importLog(t, m, "key-line-log", "station-log-synthetic.csv", data)
	if got.Status != "VALIDATED" || got.TotalRows != len(rows) || got.InvalidRows != 0 || got.Validation == nil || !got.Validation.Valid ||
		len(got.Validation.Issues) != 0 || got.Error != nil || got.DatasetVersionID != nil {
		t.Fatalf("after validation: %+v", got)
	}
	var prof importprofile.Profile
	var profText []byte
	if err := e.admin.QueryRow(ctx, `SELECT profile FROM twin.imports WHERE id=$1`, id).Scan(&profText); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(profText, &prof); err != nil {
		t.Fatal(err)
	}
	if prof.Rows != len(rows) || len(prof.Columns) != len(lineColumns) || prof.Columns[4].Inferred != "timestamp" || prof.Columns[5].Inferred != "timestamp" {
		t.Errorf("profile %+v", prof)
	}
	var key, hash string
	var size int64
	if err := e.admin.QueryRow(ctx, `SELECT canonical_object_key, canonical_content_hash, canonical_size_bytes FROM twin.imports WHERE id=$1`, id).
		Scan(&key, &hash, &size); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(key)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(stored)
	if hash != "sha256:"+hex.EncodeToString(sum[:]) || size != int64(len(stored)) {
		t.Errorf("canonical dataset %s %d, stored %x %d", hash, size, sum, len(stored))
	}
	// The same bytes as the validation package writes for the file and the
	// mapping (deterministic: a re-import gives the same dataset).
	var saved struct {
		Mapping mapping.Mapping `json:"mapping"`
	}
	if err := json.Unmarshal([]byte(lineMapping(t, 0)), &saved); err != nil {
		t.Fatal(err)
	}
	plan, err := mapping.Compile(saved.Mapping, lineColumns)
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if _, err := validation.Write(ctx, bytes.NewReader(data), importprofile.CSV, plan, validation.DefaultLimits, &want); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, want.Bytes()) {
		t.Error("the stored canonical dataset is not the validation's output for the file and the mapping")
	}
	var events []jsontext.Value
	if err := json.Unmarshal(stored, &events); err != nil || len(events) != len(rows) {
		t.Fatalf("%d events, %d rows: %v", len(events), len(rows), err)
	}
	if bytes.Contains(stored, []byte(`"op-`)) || bytes.Count(stored, []byte(`"metadata":{"synthetic":"true"}`)) != len(rows) {
		t.Error("the operator is in the dataset, or an event is not marked synthetic")
	}
	for _, i := range []int{0, len(rows) / 2, len(rows) - 1} {
		var ev map[string]any
		if err := json.Unmarshal(events[i], &ev); err != nil {
			t.Fatal(err)
		}
		r := rows[i]
		if ev["case_id"] != r[0] || ev["event_type"] != r[1] || ev["stage"] != r[2] || ev["timestamp"] != r[4] || ev["end_timestamp"] != r[5] {
			t.Errorf("event %d %v, row %v", i, ev, r)
		}
		if issues, err := validate.Document(events[i], "canonical-event.schema.json", "EVENT"); err != nil || len(issues) > 0 {
			t.Errorf("event %d is not canonical: %v %v", i, issues, err)
		}
	}
	var meta map[string]any
	if err := e.admin.QueryRow(ctx, `SELECT metadata FROM twin.audit_events WHERE target_id=$1 AND action='import.validate'`, id).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if meta["outcome"] != "VALIDATED" || meta["rows"] != float64(len(rows)) || meta["warnings"] != nil {
		t.Errorf("audit %v", meta)
	}
	if after := e.twin(t, m.ID); after != before {
		t.Errorf("the import changed the model: %s -> %s", before, after)
	}
}

// The derived fixtures (TEST-013 negative, D-059, D-072 (b)): an end before
// its start is an invalid row, so the import awaits a new mapping with the
// issue at the row's end column and an error report; each rule of the
// pack is a warning at its cell, counted in the audit event, and the
// import is still VALIDATED with its dataset.
func TestSyntheticStationLogRules(t *testing.T) {
	e := setupRuns(t)
	ctx := context.Background()
	m := e.lineModel(t)
	for name, want := range map[string]struct{ code, column string }{
		"end-before-start.csv":    {mapping.CodeEndBeforeStart, "end"},
		"unknown-station.csv":     {"EVENT_STAGE_UNKNOWN", "station"},
		"unknown-product.csv":     {"EVENT_PRODUCT_UNKNOWN", "product"},
		"missing-product.csv":     {"EVENT_PRODUCT_MISSING", "product"},
		"unknown-record-type.csv": {"EVENT_TYPE_UNKNOWN", "record_type"},
		"missing-test-result.csv": {"EVENT_OUTCOME_MISSING", "result"},
		"unknown-test-result.csv": {"EVENT_OUTCOME_UNKNOWN", "result"},
	} {
		data := lineData(t, "negative/"+name)
		base := lineRows(t, lineData(t, "station-log-synthetic.csv"))
		row := -1
		for i, r := range lineRows(t, data) {
			if !slices.Equal(r, base[i]) {
				row = i
			}
		}
		id, got := e.importLog(t, m, "key-"+strings.TrimSuffix(name, ".csv"), name, data)
		path := "/" + strconv.Itoa(row) + "/" + want.column
		if got.Validation == nil || len(got.Validation.Issues) != 1 || got.Validation.Issues[0].Code != want.code || got.Validation.Issues[0].Path != path ||
			strings.Contains(got.Validation.Issues[0].Message, "xray") || strings.Contains(got.Validation.Issues[0].Message, "board_c") {
			t.Errorf("%s: %+v, want %s at %s", name, got.Validation, want.code, path)
			continue
		}
		var reports, datasets int
		if err := e.admin.QueryRow(ctx, `SELECT count(error_report_object_key), count(canonical_object_key) FROM twin.imports WHERE id=$1`, id).
			Scan(&reports, &datasets); err != nil {
			t.Fatal(err)
		}
		var meta map[string]any
		if err := e.admin.QueryRow(ctx, `SELECT metadata FROM twin.audit_events WHERE target_id=$1 AND action='import.validate'`, id).Scan(&meta); err != nil {
			t.Fatal(err)
		}
		if name == "end-before-start.csv" {
			if got.Status != "AWAITING_MAPPING" || got.InvalidRows != 1 || got.Validation.Valid || got.Validation.Issues[0].Severity != "error" ||
				reports != 1 || datasets != 0 || meta["warnings"] != nil {
				t.Errorf("%s: %+v, %d reports, %d datasets, audit %v", name, got, reports, datasets, meta)
			}
			continue
		}
		if got.Status != "VALIDATED" || got.InvalidRows != 0 || !got.Validation.Valid || got.Validation.Issues[0].Severity != "warning" ||
			reports != 0 || datasets != 1 || meta["warnings"] != float64(1) {
			t.Errorf("%s: %+v, %d reports, %d datasets, audit %v", name, got, reports, datasets, meta)
		}
	}
}

// lineRunResult is the part of a run's Result these tests read.
type lineRunResult struct {
	ReplicationSummaries []struct {
		ReplicationIndex int   `json:"replication_index"`
		CompletedCount   int64 `json:"completed_count"`
		Failures         int64 `json:"failures"`
		Reworks          int64 `json:"reworks"`
	} `json:"replication_summaries"`
	MeasurementWindow struct {
		WarmupTicks  int64 `json:"warmup_ticks"`
		HorizonTicks int64 `json:"horizon_ticks"`
	} `json:"measurement_window"`
	Reproducibility struct {
		Seed                int64  `json:"seed"`
		Replications        int    `json:"replications"`
		ModelHash           string `json:"model_hash"`
		DatasetManifestHash string `json:"dataset_manifest_hash"`
	} `json:"reproducibility"`
}

// lineRun runs version mv of the template as the editor for two weeks, the
// first warm-up (the data set's window), and returns the run and its Result.
func (e *runEnv) lineRun(t *testing.T, mv, key string, reps int, seed int64) (string, lineRunResult) {
	t.Helper()
	editor, viewer := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	served, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	body := runRequest(mv, reps, "none", map[string]any{"config": map[string]any{
		"horizon": map[string]any{"value": 14, "unit": "day"}, "warmup": map[string]any{"value": 7, "unit": "day"},
		"replications": reps, "seed": seed, "trace_level": "none"}})
	r := e.createRun(editor.Subject, e.a.ID, key, body)
	if r.status != 202 {
		t.Fatalf("create run: %d %s", r.status, r.body)
	}
	id := decode[struct {
		ID string `json:"id"`
	}](t, r).ID
	drain(t, e.loop(t, runner.Config{Packs: served}, nil), 240*time.Second)
	if run := e.getRun(t, viewer.Subject, e.a.ID, id); run.Status != "completed" || run.Error != nil {
		t.Fatalf("run %+v", run)
	}
	res := e.do("GET", "/v1/simulations/"+id+"/results", viewer.Subject, e.a.ID, nil, "")
	if res.status != 200 {
		t.Fatalf("results: %d %s", res.status, res.body)
	}
	valid(t, res, "simulation.schema.json#/$defs/Result")
	return id, decode[lineRunResult](t, res)
}

// The data set is bound to runs of its ground-truth model: the version a
// model created from the template gets is the data set's model but for the
// model's ID and name; a run is bound to that immutable version (its hash),
// the version's dataset manifest (the template binds none) and its seed.
// With the generator's seed, replication 0 over the data set's window is
// the log itself: the boards it ships, its test failures and its repairs
// are the log's (the log is the engine's replication, never a second
// model). Another seed is another week of the same line.
func TestSyntheticStationLogIsTheTemplatesRun(t *testing.T) {
	e := setupRuns(t)
	ctx := context.Background()
	m := e.lineModel(t)
	viewer := e.a.Members[tenant.Viewer]
	v := e.do("GET", "/v1/model-versions/"+m.LatestVersion.ID, viewer.Subject, e.a.ID, nil, "")
	if v.status != 200 {
		t.Fatalf("version: %d %s", v.status, v.body)
	}
	version := decode[struct {
		IR              model.ModelIR `json:"ir"`
		Hash            string        `json:"hash"`
		DatasetBindings []any         `json:"dataset_bindings"`
	}](t, v)
	var truth model.ModelIR
	if err := model.Decode(lineData(t, "ground-truth-model.json"), &truth); err != nil {
		t.Fatal(err)
	}
	// Compared as model_hash compares (RFC 8785), with the data set's ID and name.
	served := version.IR
	served.ModelID, served.Name = truth.ModelID, truth.Name
	canonical := func(ir *model.ModelIR) string {
		t.Helper()
		data, err := model.Encode(ir)
		if err != nil {
			t.Fatal(err)
		}
		h, err := hashing.Model(data)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	if canonical(&served) != canonical(&truth) || len(version.DatasetBindings) != 0 {
		t.Fatalf("the template's version is not the data set's model (but for ID and name), or binds datasets: %d bindings", len(version.DatasetBindings))
	}
	var gt struct {
		Generator struct {
			Seed int64 `json:"seed"`
		} `json:"generator"`
	}
	if err := json.Unmarshal(lineData(t, "ground-truth.json"), &gt); err != nil {
		t.Fatal(err)
	}
	rows := lineRows(t, lineData(t, "station-log-synthetic.csv"))
	shipped, failures, reworks := 0, 0, 0
	for _, r := range rows {
		switch {
		case r[1] != "operation":
		case r[2] == "pack":
			shipped++
		case slices.Contains([]string{"aoi", "ict", "fct", "burn_in"}, r[2]) && r[6] == "fail":
			failures++
		case r[2] == "aoi_touchup", (r[2] == "ict_debug" || r[2] == "fct_debug") && r[6] == "pass":
			reworks++
		}
	}
	id, res := e.lineRun(t, m.LatestVersion.ID, "run-key-line-truth", 1, gt.Generator.Seed)
	rep := res.ReplicationSummaries[0]
	if rep.CompletedCount != int64(shipped) || rep.Failures != int64(failures) || rep.Reworks != int64(reworks) {
		t.Errorf("replication 0 with the generator's seed: %+v; the log: %d shipped, %d failures, %d repairs", rep, shipped, failures, reworks)
	}
	if res.MeasurementWindow.WarmupTicks != 7*24*3600*1000 || res.MeasurementWindow.HorizonTicks != 14*24*3600*1000 ||
		res.Reproducibility.Seed != gt.Generator.Seed || res.Reproducibility.ModelHash != version.Hash {
		t.Errorf("window %+v, reproducibility %+v", res.MeasurementWindow, res.Reproducibility)
	}
	// The run's frozen input: the version, its (empty) manifest and the seed.
	var mv, manifest, manifestHash string
	var seed int64
	if err := e.admin.QueryRow(ctx, `SELECT model_version_id, dataset_bindings::text, dataset_manifest_hash, seed
		FROM twin.simulation_runs WHERE id=$1`, id).Scan(&mv, &manifest, &manifestHash, &seed); err != nil {
		t.Fatal(err)
	}
	if mv != m.LatestVersion.ID || manifest != "[]" || manifestHash != res.Reproducibility.DatasetManifestHash || seed != gt.Generator.Seed {
		t.Errorf("frozen run input: version %s, manifest %s %s, seed %d", mv, manifest, manifestHash, seed)
	}
	if n := e.count(`SELECT count(*) FROM twin.run_datasets WHERE run_id=$1`, id); n != 0 {
		t.Errorf("%d bound datasets", n)
	}
	_, other := e.lineRun(t, m.LatestVersion.ID, "run-key-line-other", 1, 2026)
	if other.ReplicationSummaries[0].CompletedCount == rep.CompletedCount && other.ReplicationSummaries[0].Failures == rep.Failures {
		t.Errorf("another seed gives the log's week again: %+v", other.ReplicationSummaries[0])
	}
}
