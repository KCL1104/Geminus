package electronicsline_test

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

const newModel = "mdl_01M4YDQK00E1ECTR0N1CS00077"

var pack electronicsline.Pack

// tmpl is the template in English for a new model, fully validated.
func tmpl(t *testing.T) *model.ModelIR {
	t.Helper()
	ir, err := electronicsline.Template(electronicsline.DefaultTemplate, newModel, "My line", domainpack.English)
	if err != nil {
		t.Fatal(err)
	}
	return ir
}

// clone is a deep copy of ir through its encoding.
func clone(t *testing.T, ir *model.ModelIR) *model.ModelIR {
	t.Helper()
	data, err := model.Encode(ir)
	if err != nil {
		t.Fatal(err)
	}
	var out model.ModelIR
	if err := model.Decode(data, &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func nodeIndex(t *testing.T, ir *model.ModelIR, id string) int {
	t.Helper()
	i := slices.IndexFunc(ir.Nodes, func(n model.Node) bool { return n.ID == id })
	if i < 0 {
		t.Fatalf("no node %s", id)
	}
	return i
}

func resourceIndex(t *testing.T, ir *model.ModelIR, id string) int {
	t.Helper()
	i := slices.IndexFunc(ir.Resources, func(r model.Resource) bool { return r.ID == id })
	if i < 0 {
		t.Fatalf("no resource %s", id)
	}
	return i
}

func attrs(t *testing.T, v any) jsontext.Value {
	t.Helper()
	data, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// specTable returns the backticked kinds in the first column of the table
// under heading in docs/domains/ELECTRONICS_LINE.md.
func specTable(t *testing.T, heading string) []string {
	t.Helper()
	spec := string(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "docs/domains/ELECTRONICS_LINE.md")))
	start := strings.Index(spec, heading)
	if start < 0 {
		t.Fatalf("ELECTRONICS_LINE.md has no %q", heading)
	}
	section := spec[start+len(heading):]
	if end := strings.Index(section, "\n#"); end >= 0 {
		section = section[:end]
	}
	var out []string
	cell := regexp.MustCompile("`([a-z_]+)`")
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		first := strings.SplitN(line[1:], "|", 2)[0]
		for _, m := range cell.FindAllStringSubmatch(first, -1) {
			out = append(out, m[1])
		}
	}
	slices.Sort(out)
	return out
}

// The catalogs are ELECTRONICS_LINE §3.1 and §3.2: every node kind on its
// generic type, every resource role with its type.
func TestCatalogsMatchSpec(t *testing.T) {
	var kinds, roles []string
	types := map[string]model.NodeType{}
	for _, n := range pack.NodeCatalog() {
		if n.Kind == "" || n.Label == "" || n.Description == "" || !slices.Contains(model.NodeTypes, n.Type) {
			t.Errorf("bad node kind %+v", n)
		}
		kinds = append(kinds, n.Kind)
		types[n.Kind] = n.Type
		for _, rt := range n.ResourceTypes {
			if !slices.Contains([]string{"human", "machine", "generic"}, rt) {
				t.Errorf("%s requires resource type %q", n.Kind, rt)
			}
		}
	}
	slices.Sort(kinds)
	if want := specTable(t, "### 3.1 Node kinds"); !slices.Equal(kinds, want) || len(slices.Compact(slices.Clone(kinds))) != len(kinds) {
		t.Errorf("node kinds %v, spec §3.1 lists %v", kinds, want)
	}
	for kind, typ := range map[string]model.NodeType{"board_release": model.NodeSource, "line_buffer": model.NodeQueue,
		"smt_station": model.NodeProcess, "aoi_station": model.NodeProcess, "ict_station": model.NodeProcess, "fct_station": model.NodeProcess,
		"pack_station": model.NodeProcess, "retest_station": model.NodeProcess, "debug_station": model.NodeProcess, "burn_in_station": model.NodeProcess,
		"test_result": model.NodeDecision, "debug_result": model.NodeDecision, "defect_observation": model.NodeFailure,
		"rework_observation": model.NodeRework, "transport": model.NodeDelay, "shipped": model.NodeSink, "scrap": model.NodeSink} {
		if types[kind] != typ {
			t.Errorf("%s is built on %q, spec §3.1 says %q", kind, types[kind], typ)
		}
	}
	resTypes := map[string]string{}
	for _, r := range pack.ResourceCatalog() {
		if r.Kind == "" || r.Label == "" || r.Description == "" {
			t.Errorf("bad resource kind %+v", r)
		}
		roles = append(roles, r.Kind)
		resTypes[r.Kind] = r.Type
	}
	slices.Sort(roles)
	if want := specTable(t, "### 3.2 Resource roles"); !slices.Equal(roles, want) {
		t.Errorf("resource roles %v, spec §3.2 lists %v", roles, want)
	}
	for role, typ := range map[string]string{"smt_line": "machine", "aoi": "machine", "ict_tester": "machine", "fct_tester": "machine",
		"burn_in_chamber": "machine", "debug_operator": "human", "pack_station": "human", "test_slot": "machine", "assembler": "human",
		"debug_engineer": "human", "generic": "generic"} {
		if resTypes[role] != typ {
			t.Errorf("%s is %q, spec §3.2 says %q", role, resTypes[role], typ)
		}
	}
}

// Every catalog a caller receives is its own copy, PromptContext included.
func TestCatalogCopies(t *testing.T) {
	n := pack.NodeCatalog()
	n[0].Label = "changed"
	i := slices.IndexFunc(n, func(d domainpack.NodeDefinition) bool { return len(d.ResourceTypes) > 0 })
	n[i].ResourceTypes[0] = "changed"
	r := pack.ResourceCatalog()
	r[0].Label = "changed"
	p := pack.PromptContext()
	p.Nodes[0].Label, p.Resources[0].Label, p.Rules[0] = "changed", "changed", "changed"
	p.Nodes[i].ResourceTypes[0] = "changed"
	again := pack.PromptContext()
	if pack.NodeCatalog()[0].Label == "changed" || pack.NodeCatalog()[i].ResourceTypes[0] == "changed" || pack.ResourceCatalog()[0].Label == "changed" ||
		again.Nodes[0].Label == "changed" || again.Nodes[i].ResourceTypes[0] == "changed" || again.Resources[0].Label == "changed" || again.Rules[0] == "changed" {
		t.Error("a catalog is shared with callers")
	}
}

func TestValidateRejectsOtherDomains(t *testing.T) {
	ir := tmpl(t)
	ir.Domain = "software_delivery"
	got := pack.Validate(ir)
	if len(got) != 1 || got[0].Code != electronicsline.DomainMismatch || got[0].Path != "/domain" || got[0].Severity != model.SeverityError {
		t.Errorf("issues %v", got)
	}
	if got := pack.Validate(nil); len(got) != 1 || got[0].Code != electronicsline.DomainMismatch || got[0].Severity != model.SeverityError {
		t.Errorf("nil IR: %v", got)
	}
	long := pack.Validate(&model.ModelIR{Domain: strings.Repeat("x", 3000)})
	if len(long) != 1 || len([]rune(long[0].Message)) > model.MaxIssueMessage {
		t.Errorf("long domain: %v", long)
	}
}

// The template satisfies every pack rule: no issue at all.
func TestTemplatePassesThePackRules(t *testing.T) {
	if got := pack.Validate(tmpl(t)); len(got) != 0 {
		t.Errorf("issues %v", got)
	}
}

type want struct {
	code, path string
	severity   model.Severity
}

func check(t *testing.T, name string, ir *model.ModelIR, wants ...want) {
	t.Helper()
	got := pack.Validate(ir)
	if !slices.Equal(validate.Sort(slices.Clone(got)), got) {
		t.Errorf("%s: issues not in D-023 order: %v", name, got)
	}
	if again := pack.Validate(ir); !slices.Equal(got, again) {
		t.Errorf("%s: two calls disagree", name)
	}
	if len(got) != len(wants) {
		t.Errorf("%s: got %v, want %v", name, got, wants)
		return
	}
	for i, w := range wants {
		if got[i].Code != w.code || got[i].Path != w.path || got[i].Severity != w.severity || got[i].Message == "" {
			t.Errorf("%s: issue %d %+v, want %+v", name, i, got[i], w)
		}
	}
}

// ELECTRONICS_LINE §3.4 rule 1: a main-line station process has exactly
// one resource whose role is its station's (an operator may be added).
func TestRuleStationResource(t *testing.T) {
	base := tmpl(t)
	fct := nodeIndex(t, base, "fct")
	path := "/nodes/" + itoa(fct) + "/requires"

	ir := clone(t, base)
	ir.Resources[resourceIndex(t, ir, "fct_tester")].Role = "ict_tester"
	// The retest shares the resource, so it loses its role too.
	check(t, "role changed", ir, want{electronicsline.StationResourceInvalid, path, model.SeverityError},
		want{electronicsline.StationResourceInvalid, "/nodes/" + itoa(nodeIndex(t, ir, "fct_retest")) + "/requires", model.SeverityError})

	// The retest is not also reported: its first test has no primary
	// resource to compare with until rule 1 holds (no cascade).
	ir = clone(t, base)
	ir.Nodes[fct].Requires = []model.ResourceRequirement{{ResourceID: "ict_tester", Units: 1}}
	check(t, "another station's resource", ir, want{electronicsline.StationResourceInvalid, path, model.SeverityError})

	ir = clone(t, base)
	ir.Nodes[fct].Requires = nil
	check(t, "no resource", ir, want{electronicsline.StationResourceInvalid, path, model.SeverityError})

	// Two resources of the role: which one is the station's is ambiguous.
	ir = clone(t, base)
	second := ir.Resources[resourceIndex(t, ir, "fct_tester")]
	second.ID, second.Breakdowns = "fct_tester_2", nil
	ir.Resources = append(ir.Resources, second)
	ir.Nodes[fct].Requires = append(ir.Nodes[fct].Requires, model.ResourceRequirement{ResourceID: "fct_tester_2", Units: 1})
	check(t, "two of the role", ir, want{electronicsline.StationResourceInvalid, path, model.SeverityError})

	// An operator beside the tester is allowed.
	ir = clone(t, base)
	ir.Nodes[fct].Requires = append(ir.Nodes[fct].Requires, model.ResourceRequirement{ResourceID: "debug_operator", Units: 1})
	check(t, "with an operator", ir)
}

// Rule 3: a retest shares its station's first-test primary resource.
func TestRuleRetestResource(t *testing.T) {
	base := tmpl(t)
	retest := nodeIndex(t, base, "ict_retest")
	path := "/nodes/" + itoa(retest) + "/requires"

	ir := clone(t, base)
	other := ir.Resources[resourceIndex(t, ir, "ict_tester")]
	other.ID, other.Name = "ict_retester", "Retest tester"
	ir.Resources = append(ir.Resources, other)
	ir.Nodes[retest].Requires = []model.ResourceRequirement{{ResourceID: "ict_retester", Units: 1}}
	check(t, "another tester of the role", ir, want{electronicsline.RetestResourceMismatch, path, model.SeverityError})

	ir = clone(t, base)
	ict := nodeIndex(t, ir, "ict")
	ir.Nodes[ict].DomainAttributes = attrs(t, map[string]string{"station": "ict", "pass": "retest"})
	// With both ICT passes retests, neither has a first test to share with.
	check(t, "no first test", ir, want{electronicsline.RetestResourceMismatch, "/nodes/" + itoa(ict) + "/requires", model.SeverityError},
		want{electronicsline.RetestResourceMismatch, path, model.SeverityError})
}

// Rule 2: a test result decision has exactly one flow to a failure node;
// otherwise a warning (the station's first-pass yield is unavailable).
func TestRuleTestResult(t *testing.T) {
	base := tmpl(t)
	d := nodeIndex(t, base, "fct_result")
	path := "/nodes/" + itoa(d)

	ir := clone(t, base)
	for i, f := range ir.Flows {
		if f.From == "fct_result" && f.To == "fct_fail" {
			ir.Flows[i].To = "q_fct_debug" // a fail branch that skips the failure node
		}
	}
	check(t, "no flow to a failure node", ir, want{electronicsline.TestResultNotIdentified, path, model.SeverityWarning})

	ir = clone(t, base)
	for i, f := range ir.Flows {
		if f.From == "fct_result" && f.To == "q_burnin" {
			ir.Flows[i].To = "burnin_fail" // two flows to failure nodes
		}
	}
	check(t, "two flows to failure nodes", ir, want{electronicsline.TestResultNotIdentified, path, model.SeverityWarning})
}

// Attributes the pack cannot use are warnings at the attribute, and the
// node is then not a station or test result.
func TestRuleDomainAttributes(t *testing.T) {
	base := tmpl(t)
	at := func(id, member string) string {
		return "/nodes/" + itoa(nodeIndex(t, base, id)) + "/domain_attributes/" + member
	}
	for _, c := range []struct {
		name  string
		edit  func(ir *model.ModelIR)
		wants []want
	}{
		{"unknown station", func(ir *model.ModelIR) {
			ir.Nodes[nodeIndex(t, ir, "pack_ship")].DomainAttributes = attrs(t, map[string]string{"station": "xray", "pass": "first"})
		}, []want{{electronicsline.DomainAttributesInvalid, at("pack_ship", "station"), model.SeverityWarning}}},
		{"station not a string", func(ir *model.ModelIR) {
			ir.Nodes[nodeIndex(t, ir, "pack_ship")].DomainAttributes = attrs(t, map[string]any{"station": 5, "pass": "first"})
		}, []want{{electronicsline.DomainAttributesInvalid, at("pack_ship", "station"), model.SeverityWarning}}},
		{"unknown pass", func(ir *model.ModelIR) {
			ir.Nodes[nodeIndex(t, ir, "pack_ship")].DomainAttributes = attrs(t, map[string]string{"station": "pack", "pass": "third"})
		}, []want{{electronicsline.DomainAttributesInvalid, at("pack_ship", "pass"), model.SeverityWarning}}},
		{"no pass", func(ir *model.ModelIR) {
			ir.Nodes[nodeIndex(t, ir, "pack_ship")].DomainAttributes = attrs(t, map[string]string{"station": "pack"})
		}, []want{{electronicsline.DomainAttributesInvalid, at("pack_ship", "pass"), model.SeverityWarning}}},
		{"station on a queue", func(ir *model.ModelIR) {
			ir.Nodes[nodeIndex(t, ir, "q_pack")].DomainAttributes = attrs(t, map[string]string{"station": "pack", "pass": "first"})
		}, []want{{electronicsline.DomainAttributesInvalid, at("q_pack", "station"), model.SeverityWarning}}},
		{"result_of a queue", func(ir *model.ModelIR) {
			ir.Nodes[nodeIndex(t, ir, "aoi_result")].DomainAttributes = attrs(t, map[string]string{"result_of": "q_aoi"})
		}, []want{{electronicsline.DomainAttributesInvalid, at("aoi_result", "result_of"), model.SeverityWarning}}},
		{"result_of an unknown node", func(ir *model.ModelIR) {
			ir.Nodes[nodeIndex(t, ir, "aoi_result")].DomainAttributes = attrs(t, map[string]string{"result_of": "xray"})
		}, []want{{electronicsline.DomainAttributesInvalid, at("aoi_result", "result_of"), model.SeverityWarning}}},
		{"result_of a station it does not follow", func(ir *model.ModelIR) {
			ir.Nodes[nodeIndex(t, ir, "aoi_result")].DomainAttributes = attrs(t, map[string]string{"result_of": "ict"})
		}, []want{{electronicsline.DomainAttributesInvalid, at("aoi_result", "result_of"), model.SeverityWarning}}},
		{"result_of on a process", func(ir *model.ModelIR) {
			ir.Nodes[nodeIndex(t, ir, "aoi_touchup")].DomainAttributes = attrs(t, map[string]string{"result_of": "aoi"})
		}, []want{{electronicsline.DomainAttributesInvalid, at("aoi_touchup", "result_of"), model.SeverityWarning}}},
		{"other attributes are the user's", func(ir *model.ModelIR) {
			ir.Nodes[nodeIndex(t, ir, "aoi_touchup")].DomainAttributes = attrs(t, map[string]any{"note": "manual", "depth": map[string]any{"a": []int{1}}})
		}, nil},
	} {
		ir := clone(t, base)
		c.edit(ir)
		check(t, c.name, ir, c.wants...)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

// The pack's codes are in the contract's catalogue
// (common.schema.json#/$defs/ValidationCode), which the UI localizes.
func TestValidationCodesInContract(t *testing.T) {
	var doc struct {
		Defs struct {
			ValidationCode struct {
				Enum []string `json:"enum"`
			} `json:"ValidationCode"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "schemas/common.schema.json")), &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{electronicsline.DomainMismatch, electronicsline.StationResourceInvalid, electronicsline.RetestResourceMismatch,
		electronicsline.TestResultNotIdentified, electronicsline.DomainAttributesInvalid} {
		if !slices.Contains(doc.Defs.ValidationCode.Enum, c) {
			t.Errorf("%s is not in ValidationCode", c)
		}
	}
}

func TestInferenceIsUnavailable(t *testing.T) {
	r, err := pack.InferFromEvents(context.Background(), domainpack.CanonicalEventDataset{})
	if r != nil || !errors.Is(err, domainpack.ErrUnavailable) {
		t.Errorf("inference: %v %v", r, err)
	}
}

// The prompt context is static domain knowledge: the catalogs and the
// rules an AI proposal must respect.
func TestPromptContext(t *testing.T) {
	pc := pack.PromptContext()
	joined := strings.Join(pc.Rules, " ")
	if pc.Domain != electronicsline.ID || pc.Summary == "" || len(pc.Nodes) != len(pack.NodeCatalog()) || len(pc.Resources) != len(pack.ResourceCatalog()) {
		t.Errorf("prompt context %+v", pc)
	}
	for _, s := range []string{"station", "pass", "result_of", "synthetic", "OEE", "model input", "D-051", "D-058"} {
		if !strings.Contains(joined, s) {
			t.Errorf("the rules do not mention %q", s)
		}
	}
}

// DefaultViews keeps the template's positions and lays out a model
// without them; the template's positions are the 2D editor's layout and
// give the Marey strip its step order: the process steps by x, then y.
func TestDefaultViews(t *testing.T) {
	ir := tmpl(t)
	kept := pack.DefaultViews(ir).Positions
	if len(kept) != len(ir.Nodes) {
		t.Errorf("%d positions for %d nodes", len(kept), len(ir.Nodes))
	}
	for id, p := range ir.Views.TwoD.Positions {
		if kept[id] != p {
			t.Errorf("template position of %s replaced", id)
		}
	}
	seen := map[model.Position]string{}
	for _, n := range ir.Nodes {
		p, ok := ir.Views.TwoD.Positions[n.ID]
		if !ok {
			t.Errorf("the template has no position for %s", n.ID)
		}
		if other, dup := seen[p]; dup {
			t.Errorf("%s and %s share %v", n.ID, other, p)
		}
		seen[p] = n.ID
	}
	var steps []model.Node
	for _, n := range ir.Nodes {
		if n.Type == model.NodeProcess {
			steps = append(steps, n)
		}
	}
	pos := ir.Views.TwoD.Positions
	slices.SortStableFunc(steps, func(a, b model.Node) int {
		return cmp.Or(cmp.Compare(pos[a.ID].X, pos[b.ID].X), cmp.Compare(pos[a.ID].Y, pos[b.ID].Y))
	})
	var order []string
	for _, n := range steps {
		order = append(order, n.ID)
	}
	if want := []string{"smt", "aoi", "aoi_touchup", "ict", "ict_debug", "ict_retest", "fct", "fct_debug", "fct_retest", "burn_in", "pack_ship"}; !slices.Equal(order, want) {
		t.Errorf("Marey step order %v, want %v", order, want)
	}
	// The main line runs left to right on the top row.
	for _, id := range []string{"release_a", "q_smt", "smt", "q_aoi", "aoi", "q_ict", "ict", "q_fct", "fct", "q_burnin", "burn_in", "q_pack", "pack_ship", "shipped"} {
		if pos[id].Y != 0 {
			t.Errorf("%s is not on the main line's row: %v", id, pos[id])
		}
	}
	ir.Views.TwoD.Positions = nil
	laid := pack.DefaultViews(ir).Positions
	if len(laid) != len(ir.Nodes) {
		t.Errorf("layered layout: %d positions for %d nodes", len(laid), len(ir.Nodes))
	}
	if v := pack.DefaultViews(nil); len(v.Positions) != 0 {
		t.Errorf("nil IR views: %v", v)
	}
}
