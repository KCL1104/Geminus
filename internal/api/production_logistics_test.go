package api_test

import (
	"encoding/json/jsontext"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/packs"
	"github.com/kcl1104/geminus/internal/runs/runner"
	"github.com/kcl1104/geminus/internal/tenant"
)

// H-045: the production and logistics templates through the whole stack.
// An editor creates a model from each of the four over HTTP, in English
// and in Traditional Chinese; the draft has no issue and keeps its
// synthetic labelling; the same Idempotency-Key replays the answer; a
// viewer may not create one; a template under another domain, and another
// domain's template under this one, are refused by the contract; a run of
// one goes through the job loop with the packs the deployment serves, and
// the Result has the engine's generic metrics and no pack metrics. Without
// GEMINUS_TEST_DATABASE_URL it skips (NOT RUN).
func TestProductionLogisticsTemplatesClosedLoop(t *testing.T) {
	e := setupRuns(t)
	editor, viewer := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	r := e.create(editor.Subject, e.a.ID, "key-"+t.Name()+"-prj", `{"name":"Plants"}`)
	if r.status != 201 {
		t.Fatalf("project: %d %s", r.status, r.body)
	}
	prj := decode[project](t, r)
	models := "/v1/projects/" + prj.ID + "/models"
	for name, body := range map[string]string{
		"another domain's template": `{"name":"x","domain":"production_logistics","template":"electronics_assembly_test_line"}`,
		"under another domain":      `{"name":"x","domain":"electronics_line","template":"shoe_factory"}`,
		"an unknown template":       `{"name":"x","domain":"production_logistics","template":"steel_mill"}`,
	} {
		r = e.do("POST", models, editor.Subject, e.a.ID, map[string]string{"Idempotency-Key": "key-" + t.Name() + "-" + strings.ReplaceAll(name, " ", "-")}, body)
		if r.status != 422 || code(t, r) != "REQUEST_VALIDATION_FAILED" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	r = e.do("POST", models, viewer.Subject, e.a.ID, map[string]string{"Idempotency-Key": "key-" + t.Name() + "-viewer"},
		`{"name":"x","domain":"production_logistics","template":"warehouse"}`)
	if r.status != 403 || code(t, r) != "FORBIDDEN" {
		t.Errorf("a viewer: %d %s", r.status, r.body)
	}

	// The node each template's zh-Hant names file names, as the draft must show it.
	zhNode := map[string][2]string{"shoe_factory": {"stitching", "針車（鞋面縫合）"}, "food_plant": {"baking", "烘烤（隧道爐）"},
		"carton_plant": {"ffg", "印刷、開槽、折疊與糊箱"}, "warehouse": {"picking", "整箱揀貨"}}
	var first modelSummary
	for _, template := range []string{"shoe_factory", "food_plant", "carton_plant", "warehouse"} {
		for _, locale := range []string{"", "zh-Hant"} {
			body := fmt.Sprintf(`{"name":%q,"domain":"production_logistics","template":%q}`, template, template)
			if locale != "" {
				body = fmt.Sprintf(`{"name":%q,"domain":"production_logistics","template":%q,"locale":%q}`, template+" "+locale, template, locale)
			}
			key := map[string]string{"Idempotency-Key": "key-" + t.Name() + "-" + template + "-" + locale}
			r = e.do("POST", models, editor.Subject, e.a.ID, key, body)
			if r.status != 201 {
				t.Fatalf("%s %q: %d %s", template, locale, r.status, r.body)
			}
			valid(t, r, "http.schema.json#/$defs/Model")
			m := decode[modelSummary](t, r)
			if again := e.do("POST", models, editor.Subject, e.a.ID, key, body); again.status != 201 || decode[modelSummary](t, again).ID != m.ID {
				t.Errorf("%s %q: the replay %d %s", template, locale, again.status, again.body)
			}
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
			node := zhNode[template]
			if draft.IR.Domain != "production_logistics" || len(draft.Issues) != 0 || draft.IR.Metadata["synthetic"] != true || draft.IR.Metadata["template"] != template ||
				(locale == "zh-Hant") != (names[node[0]] == node[1]) {
				t.Errorf("%s %q: draft domain %q, issues %s, %s %q, metadata %v", template, locale, draft.IR.Domain, draft.Issues, node[0], names[node[0]], draft.IR.Metadata)
			}
			if first.ID == "" {
				first = m
			}
		}
	}

	served, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	body := runRequest(first.LatestVersion.ID, 2, "none", map[string]any{"config": map[string]any{
		"horizon": map[string]any{"value": 1, "unit": "day"}, "warmup": map[string]any{"value": 0, "unit": "day"},
		"replications": 2, "seed": 1, "trace_level": "none"}})
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
	result := decode[struct {
		Metrics struct {
			Throughput struct {
				Status string   `json:"status"`
				Mean   *float64 `json:"mean"`
			} `json:"throughput"`
			Resources map[string]jsontext.Value `json:"resources"`
			Domain    map[string]jsontext.Value `json:"domain"`
		} `json:"metrics"`
	}](t, res)
	if m := result.Metrics; m.Throughput.Status != "available" || m.Throughput.Mean == nil || *m.Throughput.Mean <= 0 || m.Resources["stitcher"] == nil || m.Domain != nil {
		t.Errorf("metrics %+v", m)
	}
}
