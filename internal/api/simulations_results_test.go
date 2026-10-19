package api_test

import (
	"context"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/runs/runner"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/tenant"
)

// TEST-019: the same frozen input gives the same result digest, run after
// run and whatever the worker's replication pool; a sampled trace does not
// change the metrics (only the config hash, so the digest).
func TestRunDeterminism(t *testing.T) {
	e := setupRuns(t)
	editor := e.a.Members[tenant.Editor]
	_, mv := e.version(t, e.a, golden(t, "default-delivery.json"), nil)
	week := func(trace string) string {
		return runRequest(mv, 4, trace, map[string]any{"config": map[string]any{"horizon": map[string]any{"value": 7, "unit": "day"},
			"warmup": map[string]any{"value": 1, "unit": "day"}, "replications": 4, "seed": 2024, "trace_level": trace}})
	}
	var ids []string
	for i, w := range []int{1, 4, 2} {
		r := e.createRun(editor.Subject, e.a.ID, "run-det-000"+string(rune('a'+i)), week("sampled"))
		if r.status != 202 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		ids = append(ids, decode[runBody](t, r).ID)
		drain(t, e.loop(t, runner.Config{Workers: w}, nil), time.Minute)
	}
	none := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-det-none", week("none"))).ID
	drain(t, e.loop(t, runner.Config{}, nil), time.Minute)
	first := e.do("GET", "/v1/simulations/"+ids[0]+"/results", editor.Subject, e.a.ID, nil, "")
	var want map[string]any
	if err := json.Unmarshal(first.body, &want); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids[1:] {
		got := e.results(t, editor.Subject, e.a.ID, id)
		if got.Reproducibility.ResultDigest != want["reproducibility"].(map[string]any)["result_digest"] {
			t.Errorf("%s: digest %s, want %v", id, got.Reproducibility.ResultDigest, want["reproducibility"])
		}
	}
	r := e.do("GET", "/v1/simulations/"+none+"/results", editor.Subject, e.a.ID, nil, "")
	var plain map[string]any
	if err := json.Unmarshal(r.body, &plain); err != nil {
		t.Fatal(err)
	}
	if !equalJSON(t, plain["metrics"], want["metrics"]) || !equalJSON(t, plain["replication_summaries"], want["replication_summaries"]) {
		t.Error("a sampled trace changed the metrics")
	}
	if plain["reproducibility"].(map[string]any)["config_hash"] == want["reproducibility"].(map[string]any)["config_hash"] ||
		plain["trace"].(map[string]any)["available"] != false {
		t.Errorf("trace_level none: %v %v", plain["reproducibility"], plain["trace"])
	}
	// The digest is the one the engine gives for the frozen input with the
	// software-delivery pack's metrics: the worker adds only run identity
	// and the trace's artifact ID, which the digest does not cover.
	digest := want["reproducibility"].(map[string]any)["result_digest"].(string)
	var stored string
	if err := e.admin.QueryRow(context.Background(), `SELECT result_digest FROM twin.simulation_results WHERE run_id=$1`, ids[0]).Scan(&stored); err != nil || stored != digest {
		t.Errorf("stored digest %s, served %s (%v)", stored, digest, err)
	}
	var res simulation.Result
	if err := json.Unmarshal(first.body, &res); err != nil {
		t.Fatal(err)
	}
	if again, err := hashing.Result(res); err != nil || again != digest {
		t.Errorf("the served result hashes to %s, its digest is %s (%v)", again, digest, err)
	}
}

func equalJSON(t *testing.T, a, b any) bool {
	t.Helper()
	x, err := json.Marshal(a, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	y, err := json.Marshal(b, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return string(x) == string(y)
}

// A run of a saved scenario (T-028 makes them; here a fixture) freezes the
// scenario's patch and names it; it runs the patched model, whose effective
// hash differs from the version's.
func TestScenarioRun(t *testing.T) {
	e := setupRuns(t)
	editor := e.a.Members[tenant.Editor]
	_, mv := e.version(t, e.a, golden(t, "congested-queue.json"), nil)
	patch := `[{"op":"test","path":"/resources/0/id","value":"server"},{"op":"replace","path":"/resources/0/capacity","value":2}]`
	scn := ids.New("scn")
	hash, err := hashing.Patch([]byte(patch))
	if err != nil {
		t.Fatal(err)
	}
	e.ownerTx(t, e.a.ID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO twin.scenarios(id,organization_id,model_id,base_version_id,name,patch,patch_hash,created_by)
			VALUES ($1,$2,(SELECT model_id FROM twin.model_versions WHERE id=$3),$3,'two servers',$4,$5,$6)`, scn, e.a.ID, mv, patch, hash, editor.UserID)
		return err
	})
	base := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-scn-base", runRequest(mv, 1, "none", map[string]any{"config": map[string]any{
		"horizon": map[string]any{"value": 205, "unit": "minute"}, "warmup": map[string]any{"value": 0, "unit": "minute"}, "replications": 1, "seed": 1, "trace_level": "none"}})))
	r := e.createRun(editor.Subject, e.a.ID, "run-scn-0001", runRequest(mv, 1, "none", map[string]any{"scenario_patch": nil, "scenario_id": scn, "config": map[string]any{
		"horizon": map[string]any{"value": 205, "unit": "minute"}, "warmup": map[string]any{"value": 0, "unit": "minute"}, "replications": 1, "seed": 1, "trace_level": "none"}}))
	if r.status != 202 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	id := decode[runBody](t, r).ID
	drain(t, e.loop(t, runner.Config{}, nil), 30*time.Second)
	run := e.getRun(t, editor.Subject, e.a.ID, id)
	baseRun := e.getRun(t, editor.Subject, e.a.ID, base.ID)
	canonical, _ := hashing.Canonical([]byte(patch))
	if run.Status != "completed" || run.ScenarioID == nil || *run.ScenarioID != scn || string(run.ScenarioPatch) != string(canonical) ||
		run.EffectiveModelHash == run.ModelHash || run.ModelHash != baseRun.ModelHash || baseRun.EffectiveModelHash != baseRun.ModelHash {
		t.Errorf("scenario run %+v, baseline %+v", run, baseRun)
	}
	type waits struct {
		Metrics struct {
			Queues map[string]struct {
				Mean *float64 `json:"wait_mean_minutes"`
			} `json:"queues"`
		} `json:"metrics"`
	}
	b := decode[waits](t, e.do("GET", "/v1/simulations/"+base.ID+"/results", editor.Subject, e.a.ID, nil, ""))
	s := decode[waits](t, e.do("GET", "/v1/simulations/"+id+"/results", editor.Subject, e.a.ID, nil, ""))
	if b.Metrics.Queues["service"].Mean == nil || *b.Metrics.Queues["service"].Mean != 47.5 || s.Metrics.Queues["service"].Mean == nil || *s.Metrics.Queues["service"].Mean >= 47.5 {
		t.Errorf("waits: baseline %v (want 47.5, SIMULATION_SPEC §10), two servers %v", b.Metrics.Queues, s.Metrics.Queues)
	}
}

// A run that exceeds one of its frozen limits fails without a result and
// is not retried; its error is the contract's envelope, the failure code
// in details[0] at the limit's place (CB-38), under the request that
// created the run.
func TestRunFailures(t *testing.T) {
	e := setupRuns(t, api.WithRunLimits(simulation.ExecutionLimits{WallTimeoutSeconds: 300, MaxEvents: 150, MaxActiveEntities: 100_000,
		MaxEntityVisits: 10_000, MaxTraceEvents: 100_000}))
	editor := e.a.Members[tenant.Editor]
	_, mv := e.version(t, e.a, golden(t, "deterministic-queue.json"), nil)
	r := e.createRun(editor.Subject, e.a.ID, "run-fail-0001", runRequest(mv, 2, "none", nil))
	if r.status != 202 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	id := decode[runBody](t, r).ID
	drain(t, e.loop(t, runner.Config{}, nil), 30*time.Second)
	run := e.getRun(t, editor.Subject, e.a.ID, id)
	if run.Status != "failed" || run.Error == nil || run.Error.Error.Code != "MODEL_VALIDATION_FAILED" || run.Error.Error.RequestID != r.header.Get("X-Request-ID") ||
		len(run.Error.Error.Details) != 1 || run.Error.Error.Details[0].Code != "EVENT_BUDGET_EXCEEDED" || run.Error.Error.Details[0].Path != "/execution_limits/max_events" {
		t.Fatalf("run %+v (%+v)", run, run.Error)
	}
	if n := e.count(`SELECT count(*) FROM twin.jobs WHERE simulation_run_id=$1 AND status='failed' AND attempt=1`, id); n != 1 {
		t.Error("a budget failure was retried")
	}
	if n := e.count(`SELECT count(*) FROM twin.simulation_results WHERE run_id=$1`, id); n != 0 {
		t.Error("a failed run has a result")
	}
	if n := e.count(`SELECT count(*) FROM twin.simulation_runs WHERE id=$1 AND event_budget_reserved<=150`, id); n != 1 {
		t.Error("the run reserved more than its budget")
	}
	if r := e.do("GET", "/v1/simulations/"+id+"/results", editor.Subject, e.a.ID, nil, ""); r.status != 409 || code(t, r) != "RESULT_NOT_READY" {
		t.Errorf("results of a failed run: %d %s", r.status, r.body)
	}
	if r := e.cancelRun(editor.Subject, e.a.ID, "run-fail-cancel", id); r.status != 409 || code(t, r) != "LIFECYCLE_CONFLICT" {
		t.Errorf("cancel a failed run: %d %s", r.status, r.body)
	}
}
