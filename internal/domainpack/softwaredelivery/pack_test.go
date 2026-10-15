package softwaredelivery_test

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"maps"
	"path/filepath"
	"regexp"
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
	// Synthetic labelling (details in TestTemplateMetadataForUserModels).
	var meta struct {
		Origin      string   `json:"origin"`
		Assumptions []string `json:"assumptions"`
	}
	if err := json.Unmarshal(ir.Metadata, &meta); err != nil || !strings.Contains(meta.Origin, "not customer data") || len(meta.Assumptions) < 3 {
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
		if m.Reason == nil || *m.Reason != softwaredelivery.ReasonMissingDORAMapping {
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

// Review of T-011: the resource catalog is the one TWIN_IR_SPEC §2 lists,
// and each kind keeps its resource type.
func TestResourceCatalogMatchesSpec(t *testing.T) {
	spec := string(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "docs/TWIN_IR_SPEC.md")))
	m := regexp.MustCompile(`Software Delivery catalog 包含 ([a-z_、]+)；`).FindStringSubmatch(spec)
	if m == nil {
		t.Fatal("TWIN_IR_SPEC no longer lists the Software Delivery catalog")
	}
	want := strings.Split(m[1], "、")
	slices.Sort(want)
	types := map[string]string{}
	for _, r := range pack.ResourceCatalog() {
		types[r.Kind] = r.Type
	}
	if got := slices.Sorted(maps.Keys(types)); !slices.Equal(got, want) {
		t.Errorf("resource kinds %v, spec lists %v", got, want)
	}
	for kind, typ := range map[string]string{"developer": "human", "reviewer": "human", "ci_runner": "machine",
		"test_environment": "machine", "deployment_slot": "machine", "generic_resource": "generic"} {
		if types[kind] != typ {
			t.Errorf("%s is %q, want %q", kind, types[kind], typ)
		}
	}
	nodes := map[string]domainpack.NodeDefinition{}
	for _, n := range pack.NodeCatalog() {
		nodes[n.Kind] = n
	}
	for kind, want := range map[string][]string{"code_review": {"human"}, "ci_pipeline": {"machine"}, "test_stage": {"machine"}, "deploy_stage": {"machine"}} {
		if n := nodes[kind]; n.Type != model.NodeProcess || !slices.Equal(n.ResourceTypes, want) {
			t.Errorf("%s: %+v", kind, n)
		}
	}
}

// Every catalog a caller receives is its own copy, PromptContext included.
func TestCatalogCopies(t *testing.T) {
	r := pack.ResourceCatalog()
	r[0].Label = "changed"
	p := pack.PromptContext()
	p.Nodes[0].Label = "changed"
	p.Nodes[2].ResourceTypes[0] = "changed"
	p.Resources[0].Label = "changed"
	p.Rules[0] = "changed"
	again := pack.PromptContext()
	if pack.ResourceCatalog()[0].Label == "changed" || again.Nodes[0].Label == "changed" || again.Nodes[2].ResourceTypes[0] == "changed" ||
		again.Resources[0].Label == "changed" || again.Rules[0] == "changed" {
		t.Error("a catalog is shared with callers")
	}
}

// TEST-023 with generic failures and rework observed: DORA stays unavailable,
// in the contract fixture's form (unit, population, reason code; CB-23).
func TestDORAUnavailableWithFailuresObserved(t *testing.T) {
	result := decode[simulation.Result](t, "testdata/contracts/valid-result.json")
	three, five := 3.0, 5.0
	result.Metrics.FailureCount = simulation.MetricSummary{Status: simulation.Available, Mean: &three, P50: &three, P95: &three,
		SampleCount: 1, Population: simulation.PopulationReplication, Unit: "count"}
	result.Metrics.ReworkCount = simulation.MetricSummary{Status: simulation.Available, Mean: &five, P50: &five, P95: &five,
		SampleCount: 1, Population: simulation.PopulationReplication, Unit: "count"}
	got := pack.ComputeMetrics(&result)
	want := map[string][2]string{
		"change_lead_time":                {"minute", "entity"},
		"deployment_frequency":            {"deployment_per_day", "replication"},
		"change_failure_percentage":       {"percent", "replication"},
		"failed_deployment_recovery_time": {"minute", "entity"},
		"deployment_rework_rate":          {"percent", "replication"},
	}
	for k, w := range want {
		m := got[k]
		if m.Status != simulation.Unavailable || m.Mean != nil || m.Unit != w[0] || string(m.Population) != w[1] ||
			m.Reason == nil || *m.Reason != softwaredelivery.ReasonMissingDORAMapping {
			t.Errorf("%s: %+v", k, m)
		}
	}
	fixture := decode[map[string]any](t, "testdata/contracts/valid-result.json")
	lead := fixture["metrics"].(map[string]any)["dora"].(map[string]any)["change_lead_time"].(map[string]any)
	if m := got["change_lead_time"]; lead["unit"] != m.Unit || lead["population"] != string(m.Population) || lead["reason"] != *m.Reason {
		t.Errorf("change_lead_time %+v differs from the contract fixture %v", m, lead)
	}
}

// Template errors are typed; the issues carry JSON Pointers; nothing the
// caller sent is echoed into the error text.
func TestTemplateErrors(t *testing.T) {
	if _, err := softwaredelivery.Template("unknown", newModel, "x"); !errors.Is(err, softwaredelivery.ErrUnknownTemplate) {
		t.Errorf("unknown template: %v", err)
	}
	if _, err := softwaredelivery.Template(softwaredelivery.DefaultTemplate, "mdl_bad", "x"); !errors.Is(err, softwaredelivery.ErrInvalidModelID) {
		t.Errorf("bad model ID: %v", err)
	}
	long := strings.Repeat("😀", 201)
	for _, name := range []string{"", long, "bad \xff"} {
		_, err := softwaredelivery.Template(softwaredelivery.DefaultTemplate, newModel, name)
		var te *softwaredelivery.InvalidError
		if !errors.As(err, &te) || !errors.Is(err, softwaredelivery.ErrInvalidTemplateInput) || len(te.Issues) == 0 {
			t.Errorf("name %q: %v", name, err)
			continue
		}
		if !slices.ContainsFunc(te.Issues, func(i model.ValidationIssue) bool { return strings.HasPrefix(i.Path, "/name") }) {
			t.Errorf("name %q: issues %v do not point at /name", name, te.Issues)
		}
		if strings.Contains(err.Error(), "😀") || len(err.Error()) > 200 {
			t.Errorf("error echoes the input: %q", err.Error())
		}
	}
}

// A model created from the template is labelled synthetic but is not a fixture.
func TestTemplateMetadataForUserModels(t *testing.T) {
	ir, err := softwaredelivery.Template(softwaredelivery.DefaultTemplate, newModel, "x")
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(ir.Metadata, &meta); err != nil {
		t.Fatal(err)
	}
	if _, isFixture := meta["fixture"]; isFixture || meta["template"] != softwaredelivery.DefaultTemplate ||
		!strings.Contains(meta["origin"].(string), "not customer data") || strings.Contains(meta["origin"].(string), "PROPOSED") {
		t.Errorf("metadata %v", meta)
	}
	if a, _ := meta["assumptions"].([]any); len(a) < 3 {
		t.Errorf("synthetic assumptions dropped: %v", meta["assumptions"])
	}
}

// Defensive inputs: an IR that skipped the generic validator, or none at all.
func TestValidateUnvalidatedInput(t *testing.T) {
	got := pack.Validate(&model.ModelIR{Domain: strings.Repeat("x", 3000)})
	if len(got) != 1 || len([]rune(got[0].Message)) > model.MaxIssueMessage {
		t.Errorf("long domain: %v", got)
	}
	if got := pack.Validate(nil); len(got) != 1 || got[0].Code != softwaredelivery.DomainMismatch {
		t.Errorf("nil IR: %v", got)
	}
	if v := pack.DefaultViews(nil); len(v.Positions) != 0 {
		t.Errorf("nil IR views: %v", v)
	}
}
