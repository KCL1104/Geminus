package softwaredelivery_test

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
)

const newModel = "mdl_01M4YDQK00TA00000000000077"

var pack softwaredelivery.Pack

func TestCatalogs(t *testing.T) {
	kinds := map[string]bool{}
	covered := map[model.NodeType]bool{}
	for _, n := range pack.NodeCatalog() {
		if kinds[n.Kind] || n.Kind == "" || n.Label == "" || n.Description == "" {
			t.Errorf("bad or duplicate node kind %+v", n)
		}
		kinds[n.Kind] = true
		if !slices.Contains(model.NodeTypes, n.Type) {
			t.Errorf("%s uses unknown node type %q", n.Kind, n.Type)
		}
		covered[n.Type] = true
		for _, rt := range n.ResourceTypes {
			if !slices.Contains([]string{"human", "machine", "generic"}, rt) {
				t.Errorf("%s requires unknown resource type %q", n.Kind, rt)
			}
		}
	}
	for _, typ := range model.NodeTypes {
		if !covered[typ] {
			t.Errorf("no catalog entry builds a %s node", typ)
		}
	}
	for _, r := range pack.ResourceCatalog() {
		if !slices.Contains([]string{"human", "machine", "generic"}, r.Type) {
			t.Errorf("resource %s has type %q", r.Kind, r.Type)
		}
	}
	// Callers get copies.
	c := pack.NodeCatalog()
	c[2].ResourceTypes[0] = "changed"
	c[0].Label = "changed"
	if pack.NodeCatalog()[2].ResourceTypes[0] == "changed" || pack.NodeCatalog()[0].Label == "changed" {
		t.Error("catalog is shared with callers")
	}
}

// The embedded template is the reviewed D-017 fixture, byte-for-byte in content.
func TestTemplateMatchesGoldenFixture(t *testing.T) {
	var golden, embedded jsontext.Value
	golden = contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/golden-models/default-delivery.json"))
	embedded = contracttest.ReadFile(t, "templates/default_delivery_pipeline.json")
	if err := golden.Canonicalize(); err != nil {
		t.Fatal(err)
	}
	if err := embedded.Canonicalize(); err != nil {
		t.Fatal(err)
	}
	if string(golden) != string(embedded) {
		t.Error("templates/default_delivery_pipeline.json differs from testdata/golden-models/default-delivery.json")
	}
}

// REQ-004 / TEST-002: the template is a valid full pipeline
// Source→Review→CI→Test→Deploy→Sink whose numbers are labelled synthetic.
func TestTemplate(t *testing.T) {
	ir, err := softwaredelivery.Template(softwaredelivery.DefaultTemplate, newModel, "My pipeline")
	if err != nil {
		t.Fatal(err)
	}
	if ir.ModelID != newModel || ir.Name != "My pipeline" || ir.Domain != softwaredelivery.ID {
		t.Errorf("identity %s %q %s", ir.ModelID, ir.Name, ir.Domain)
	}
	if issues := pack.Validate(ir); len(issues) != 0 {
		t.Errorf("domain issues: %v", issues)
	}
	order := []string{"change_arrival", "code_review", "ci", "test", "deploy", "deployed"}
	next := map[string][]string{}
	for _, f := range ir.Flows {
		if f.Routing.Probability > 0 {
			next[f.From] = append(next[f.From], f.To)
		}
	}
	reach := func(from, to string) bool {
		seen, stack := map[string]bool{}, []string{from}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if n == to {
				return true
			}
			if !seen[n] {
				seen[n] = true
				stack = append(stack, next[n]...)
			}
		}
		return false
	}
	for i := 1; i < len(order); i++ {
		if !reach(order[i-1], order[i]) {
			t.Errorf("no path %s -> %s", order[i-1], order[i])
		}
	}
	var meta struct {
		Fixture     bool     `json:"fixture"`
		Origin      string   `json:"origin"`
		Assumptions []string `json:"assumptions"`
	}
	if err := json.Unmarshal(ir.Metadata, &meta); err != nil || !meta.Fixture || !strings.Contains(meta.Origin, "NOT customer data") || len(meta.Assumptions) < 3 {
		t.Errorf("synthetic labelling missing: %+v %v", meta, err)
	}

	for _, c := range []struct{ tmpl, id, name string }{
		{"unknown", newModel, "x"}, {softwaredelivery.DefaultTemplate, "mdl_bad", "x"}, {softwaredelivery.DefaultTemplate, newModel, ""},
	} {
		if _, err := softwaredelivery.Template(c.tmpl, c.id, c.name); err == nil {
			t.Errorf("Template(%q, %q, %q) accepted", c.tmpl, c.id, c.name)
		}
	}
}

func TestValidateRejectsOtherDomains(t *testing.T) {
	ir, err := softwaredelivery.Template(softwaredelivery.DefaultTemplate, newModel, "x")
	if err != nil {
		t.Fatal(err)
	}
	ir.Domain = "manufacturing"
	got := pack.Validate(ir)
	if len(got) != 1 || got[0].Code != softwaredelivery.DomainMismatch || got[0].Path != "/domain" || got[0].Severity != model.SeverityError {
		t.Errorf("issues %v", got)
	}
}

// TEST-023: without an approved mapping every DORA metric is unavailable,
// with a reason, even when generic failures were observed.
func TestDORAUnavailable(t *testing.T) {
	result := decode[simulation.Result](t, "testdata/contracts/valid-result.json")
	before := result.Metrics.FailureCount
	got := pack.ComputeMetrics(&result)
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := []string{"change_failure_percentage", "change_lead_time", "deployment_frequency", "deployment_rework_rate", "failed_deployment_recovery_time"}
	if !slices.Equal(keys, want) {
		t.Fatalf("DORA keys %v", keys)
	}
	for k, m := range got {
		if m.Status != simulation.Unavailable || m.Mean != nil || m.P50 != nil || m.P95 != nil || m.SampleCount != 0 || m.ConfidenceIntervalMean != nil {
			t.Errorf("%s carries a value: %+v", k, m)
		}
		if m.Reason == nil || !strings.Contains(*m.Reason, "D-010") || !strings.Contains(*m.Reason, "not DORA") {
			t.Errorf("%s reason %v", k, m.Reason)
		}
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		issues, err := validate.Document(data, "simulation.schema.json#/$defs/MetricSummary", "SCHEMA")
		if err != nil || len(issues) > 0 {
			t.Errorf("%s does not match MetricSummary: %v %v", k, issues, err)
		}
	}
	if result.Metrics.FailureCount != before {
		t.Error("ComputeMetrics changed the generic failure count")
	}
}

func TestInferenceIsUnavailable(t *testing.T) {
	r, err := pack.InferFromEvents(context.Background(), domainpack.CanonicalEventDataset{})
	if r != nil || !errors.Is(err, domainpack.ErrUnavailable) {
		t.Errorf("inference: %v %v", r, err)
	}
}

func TestDefaultViewsAndPrompt(t *testing.T) {
	ir, err := softwaredelivery.Template(softwaredelivery.DefaultTemplate, newModel, "x")
	if err != nil {
		t.Fatal(err)
	}
	kept := pack.DefaultViews(ir).Positions
	for id, p := range ir.Views.TwoD.Positions {
		if kept[id] != p {
			t.Errorf("template position of %s replaced", id)
		}
	}
	ir.Views.TwoD.Positions = nil
	laid := pack.DefaultViews(ir).Positions
	if len(laid) != len(ir.Nodes) {
		t.Errorf("%d positions for %d nodes", len(laid), len(ir.Nodes))
	}
	pc := pack.PromptContext()
	joined := strings.Join(pc.Rules, " ")
	if pc.Domain != softwaredelivery.ID || !strings.Contains(joined, "D-010") || !strings.Contains(joined, "D-017") || len(pc.Nodes) != len(pack.NodeCatalog()) {
		t.Errorf("prompt context %+v", pc)
	}
}

func decode[T any](t *testing.T, rel string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), rel)), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
