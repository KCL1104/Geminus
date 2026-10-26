package api_test

import (
	"encoding/json/jsontext"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/packs"
	"github.com/kcl1104/geminus/internal/runs/runner"
	"github.com/kcl1104/geminus/internal/tenant"
)

// T-051 (H-029): the electronics line template through the whole stack.
// An editor creates a model from it over HTTP with Traditional Chinese
// names; the draft has no issue; a template of another domain is refused
// by the contract; a run of version 1 goes through the job loop with the
// packs the deployment serves, and the Result the API serves carries the
// pack's line metrics (metrics.domain), schema-valid, the ones this build
// cannot compute unavailable with their reason.
func TestElectronicsLineTemplateClosedLoop(t *testing.T) {
	e := setupRuns(t)
	editor, viewer := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	r := e.create(editor.Subject, e.a.ID, "key-"+t.Name()+"-prj", `{"name":"Lines"}`)
	if r.status != 201 {
		t.Fatalf("project: %d %s", r.status, r.body)
	}
	prj := decode[project](t, r)
	models := "/v1/projects/" + prj.ID + "/models"
	r = e.do("POST", models, editor.Subject, e.a.ID, map[string]string{"Idempotency-Key": "key-" + t.Name() + "-other"},
		`{"name":"x","domain":"electronics_line","template":"default_delivery_pipeline"}`)
	if r.status != 422 || code(t, r) != "REQUEST_VALIDATION_FAILED" {
		t.Errorf("another domain's template: %d %s", r.status, r.body)
	}
	r = e.do("POST", models, editor.Subject, e.a.ID, map[string]string{"Idempotency-Key": "key-" + t.Name() + "-mdl"},
		`{"name":"電子產線","domain":"electronics_line","template":"electronics_assembly_test_line","locale":"zh-Hant"}`)
	if r.status != 201 {
		t.Fatalf("model: %d %s", r.status, r.body)
	}
	valid(t, r, "http.schema.json#/$defs/Model")
	m := decode[modelSummary](t, r)
	d := e.do("GET", "/v1/models/"+m.ID+"/draft", viewer.Subject, e.a.ID, nil, "")
	if d.status != 200 {
		t.Fatalf("draft: %d %s", d.status, d.body)
	}
	valid(t, d, "http.schema.json#/$defs/Draft")
	draft := decode[struct {
		IR struct {
			Domain string `json:"domain"`
			Nodes  []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"nodes"`
			Metadata map[string]any `json:"metadata"`
		} `json:"ir"`
		Issues []jsontext.Value `json:"issues"`
	}](t, d)
	names := map[string]string{}
	for _, n := range draft.IR.Nodes {
		names[n.ID] = n.Name
	}
	if draft.IR.Domain != "electronics_line" || len(draft.Issues) != 0 || names["fct"] != "FCT 功能測試" || draft.IR.Metadata["synthetic"] != true {
		t.Errorf("draft domain %q, issues %s, fct %q, metadata %v", draft.IR.Domain, draft.Issues, names["fct"], draft.IR.Metadata)
	}

	served, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	body := runRequest(m.LatestVersion.ID, 2, "none", map[string]any{"config": map[string]any{
		"horizon": map[string]any{"value": 7, "unit": "day"}, "warmup": map[string]any{"value": 1, "unit": "day"},
		"replications": 2, "seed": 2026, "trace_level": "none"}})
	r = e.createRun(editor.Subject, e.a.ID, "run-key-"+t.Name(), body)
	if r.status != 202 {
		t.Fatalf("create run: %d %s", r.status, r.body)
	}
	accepted := decode[struct {
		ID string `json:"id"`
	}](t, r)
	drain(t, e.loop(t, runner.Config{Packs: served}, nil), 60*time.Second)
	if run := e.getRun(t, viewer.Subject, e.a.ID, accepted.ID); run.Status != "completed" || run.Error != nil {
		t.Fatalf("run %+v", run)
	}
	res := e.do("GET", "/v1/simulations/"+accepted.ID+"/results", viewer.Subject, e.a.ID, nil, "")
	if res.status != 200 {
		t.Fatalf("results: %d %s", res.status, res.body)
	}
	valid(t, res, "simulation.schema.json#/$defs/Result")
	type summary struct {
		Status string   `json:"status"`
		Mean   *float64 `json:"mean"`
		Reason *string  `json:"reason"`
	}
	result := decode[struct {
		Metrics struct {
			Domain map[string]summary `json:"domain"`
		} `json:"metrics"`
	}](t, res)
	dm := result.Metrics.Domain
	for _, k := range []string{"uph", "uph_scheduled", "line_balance", "work_ratio.fct", "blocking_ratio.aoi", "setups_started.smt", "mean_batch_size.burn_in"} {
		if m := dm[k]; m.Status != "available" || m.Mean == nil {
			t.Errorf("%s: %+v", k, m)
		}
	}
	if m := dm["fpy_input.fct"]; m.Mean == nil || *m.Mean != 0.93 {
		t.Errorf("fpy_input.fct %+v", m)
	}
	for k, reason := range map[string]string{"line_wip": "WIP_NOT_DECLARED", "scrap_rate": "SINK_COUNT_NOT_DECLARED",
		"retest_ratio.ict": "FLOW_COUNT_NOT_DECLARED", "starved_ratio.burn_in": "STARVATION_UNDEFINED_BATCH"} {
		if m := dm[k]; m.Status != "unavailable" || m.Mean != nil || m.Reason == nil || *m.Reason != reason {
			t.Errorf("%s: %+v, want unavailable %s", k, m, reason)
		}
	}
}
