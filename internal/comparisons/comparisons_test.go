package comparisons

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/results/compare"
	"github.com/kcl1104/geminus/internal/simulation"
	store "github.com/kcl1104/geminus/internal/storage/runs"
	"github.com/kcl1104/geminus/internal/tenant"
)

// The comparison route is the contract's: method, path and roles
// (planning/api-operations.json, from openapi.yaml); any member compares.
func TestRoutesMatchContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "planning", "api-operations.json"))
	if err != nil {
		t.Fatal(err)
	}
	type operation struct {
		Method      string   `json:"method"`
		Path        string   `json:"path"`
		OperationID string   `json:"operation_id"`
		Roles       []string `json:"roles"`
	}
	var doc struct {
		Operations []operation `json:"operations"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(Routes) != 1 {
		t.Fatalf("routes %v, want compareSimulations only", Routes)
	}
	r := Routes[0]
	i := slices.IndexFunc(doc.Operations, func(o operation) bool { return o.OperationID == r.OperationID })
	if r.OperationID != "compareSimulations" || i < 0 {
		t.Fatalf("route %+v is not the contract's compareSimulations", r)
	}
	o := doc.Operations[i]
	var roles []string
	for _, x := range r.Roles {
		roles = append(roles, string(x))
	}
	if o.Method != r.Method || o.Path != r.Path || !slices.Equal(slices.Sorted(slices.Values(o.Roles)), slices.Sorted(slices.Values(roles))) {
		t.Errorf("route %s %s %v, contract %s %s %v", r.Method, r.Path, roles, o.Method, o.Path, o.Roles)
	}
	if !slices.Contains(r.Roles, tenant.Viewer) || !slices.Equal(readRoles, tenant.Members) {
		t.Errorf("roles %v: every member compares", r.Roles)
	}
}

// The query's own checks come before any database work: each parameter
// once, as a run ID, and two different runs; each refusal names its
// parameter (D-089 (a)).
func TestRunQuery(t *testing.T) {
	const a, b = "sim_01M4YDQK00FGZZWH0PP15XPNFA", "sim_01M4YDQK00FGZZWH0PP15XPNFB"
	for _, c := range []struct {
		query, reason string
	}{
		{"baseline_run_id=" + a + "&scenario_run_id=" + b, ""},
		{"scenario_run_id=" + b + "&baseline_run_id=" + a + "&other=1", ""},
		{"scenario_run_id=" + b, BaselineParam},
		{"baseline_run_id=" + a, ScenarioParam},
		{"", BaselineParam},
		{"baseline_run_id=&scenario_run_id=" + b, BaselineParam},
		{"baseline_run_id=" + a + "&baseline_run_id=" + a + "&scenario_run_id=" + b, BaselineParam},
		{"baseline_run_id=" + a + "&scenario_run_id=" + b + "&scenario_run_id=" + b, ScenarioParam},
		{"baseline_run_id=mdl_01M4YDQK00FGZZWH0PP15XPNFA&scenario_run_id=" + b, BaselineParam},
		{"baseline_run_id=" + a + "&scenario_run_id=sim_01m4ydqk00fgzzwh0pp15xpnfb", ScenarioParam},
		{"baseline_run_id=" + a + "&scenario_run_id=sim_81M4YDQK00FGZZWH0PP15XPNFB", ScenarioParam},
		{"baseline_run_id=" + a + "&scenario_run_id=" + a, ScenarioParam},
		// Review round 1 L4: the ID is the whole value (RunID has 30
		// characters), nothing after it, not even a newline.
		{"baseline_run_id=" + a + "&scenario_run_id=" + b + "X", ScenarioParam},
		{"baseline_run_id=" + a + "0&scenario_run_id=" + b, BaselineParam},
		{"baseline_run_id=" + a + "%0A&scenario_run_id=" + b, BaselineParam},
		{"baseline_run_id=" + a + "&scenario_run_id=" + b + "%0A", ScenarioParam},
		{"baseline_run_id=%20" + a + "&scenario_run_id=" + b, BaselineParam},
		{"baseline_run_id=" + a + "&scenario_run_id=" + b + "&x=%zz", "query string"},
	} {
		r := httptest.NewRequest("GET", "/v1/simulation-comparisons?"+c.query, nil)
		base, scen, err := runQuery(r)
		if c.reason == "" {
			if err != nil || base != a || scen != b {
				t.Errorf("%q: %s %s %v", c.query, base, scen, err)
			}
			continue
		}
		var e *httpapi.Error
		if !errors.As(err, &e) || e.Code != httpapi.InvalidRequest || len(e.Details) != 1 || e.Details[0].Reason != c.reason || e.Details[0].Path != "" {
			t.Errorf("%q: %v, want 400 naming %s", c.query, err, c.reason)
		}
	}
}

// Each service error has its contract code and names the parameter of the
// run it is about; anything else (a stored result that does not match its
// run, the tenant's errors, the database) is httpapi's.
func TestRefused(t *testing.T) {
	for _, c := range []struct {
		err    error
		code   httpapi.Code
		reason string
	}{
		{&RunError{Param: BaselineParam, Err: ErrNotFound}, httpapi.NotFound, BaselineParam},
		{&RunError{Param: ScenarioParam, Err: ErrNotFound}, httpapi.NotFound, ScenarioParam},
		{fmt.Errorf("wrapped: %w", &RunError{Param: ScenarioParam, Err: ErrNotReady}), httpapi.ResultNotReady, ScenarioParam},
		{&RunError{Param: BaselineParam, Err: ErrNotReady}, httpapi.ResultNotReady, BaselineParam},
		{compare.ErrSameRun, httpapi.InvalidRequest, ScenarioParam},
	} {
		var e *httpapi.Error
		if !errors.As(Refused(c.err), &e) || e.Code != c.code || len(e.Details) != 1 || e.Details[0].Reason != c.reason {
			t.Errorf("%v: %v, want %s naming %s", c.err, Refused(c.err), c.code, c.reason)
		}
	}
	for _, err := range []error{compare.ErrInconsistent, fmt.Errorf("x: %w", compare.ErrInconsistent), tenant.ErrForbidden, tenant.ErrNoMembership,
		errors.New("database down")} {
		if got := Refused(err); got != err {
			t.Errorf("%v became %v", err, got)
		}
	}
}

// The comparison reads exactly the frozen input of each run (D-088 (a)).
func TestInput(t *testing.T) {
	scn := "scn_01M4YDQK00J4MJS82YRG0PCEC0"
	r := store.Run{ID: "sim_01M4YDQK00FGZZWH0PP15XPNFA", ModelID: "mdl_01M4YDQK004AGVNEGJQJ1NMM0N", ModelVersionID: "mv_01M4YDQK00FRYWV7GS7X1QCXZR",
		ScenarioID: &scn, ScenarioPatch: []byte(`[{"op":"test","path":"/name","value":"x"}]`), EngineVersion: "des-go-0.4.0",
		DatasetManifestHash: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		ModelHash:           "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		EffectiveModelHash:  "sha256:3333333333333333333333333333333333333333333333333333333333333333",
		ConfigHash:          "sha256:4444444444444444444444444444444444444444444444444444444444444444",
		Config: simulation.RunConfig{Horizon: simulation.Duration{Value: 2, Unit: model.Day}, Warmup: simulation.Duration{Value: 1, Unit: model.Hour},
			Replications: 3, Seed: 11, TraceLevel: simulation.TraceSampled}, Status: store.Completed}
	res := &simulation.Result{RunID: r.ID}
	got := input(r, res)
	want := compare.Run{ID: r.ID, ModelVersionID: r.ModelVersionID, EngineVersion: r.EngineVersion, DatasetManifestHash: r.DatasetManifestHash,
		ModelHash: r.ModelHash, EffectiveModelHash: r.EffectiveModelHash, ConfigHash: r.ConfigHash,
		ScenarioPatch: r.ScenarioPatch, Config: r.Config, Result: res}
	if got.ID != want.ID || got.ModelVersionID != want.ModelVersionID || got.EngineVersion != want.EngineVersion ||
		got.DatasetManifestHash != want.DatasetManifestHash || string(got.ScenarioPatch) != string(want.ScenarioPatch) || got.Config != want.Config ||
		got.ModelHash != want.ModelHash || got.EffectiveModelHash != want.EffectiveModelHash || got.ConfigHash != want.ConfigHash ||
		got.Result != res {
		t.Errorf("input %+v, want %+v", got, want)
	}
}

// A stored result is decoded strictly: an unknown member or another shape
// is a server fault (ErrInconsistent), never compared.
func TestDecodeResult(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "contracts", "valid-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := decodeResult(data)
	if err != nil || r.RunID != "sim_01M4YDQK00FGZZWH0PP15XPNFA" {
		t.Fatalf("%+v %v", r, err)
	}
	for _, bad := range []string{`{}x`, `[]`, `{"run_id":"x","extra":1}`, string(data[:len(data)-3])} {
		if _, err := decodeResult([]byte(bad)); !errors.Is(err, compare.ErrInconsistent) {
			t.Errorf("%.40q: %v", bad, err)
		}
	}
}
