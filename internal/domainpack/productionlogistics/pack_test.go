package productionlogistics_test

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/productionlogistics"
	"github.com/kcl1104/geminus/internal/model"
)

const newModel = "mdl_01M4YDQK00E1ECTR0N1CS00077"

var pack productionlogistics.Pack

// tmpl is the named template in English for a new model, fully validated.
func tmpl(t *testing.T, name string) *model.ModelIR {
	t.Helper()
	ir, err := productionlogistics.Template(name, newModel, "My plant", domainpack.English)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
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

// docTable returns the backticked kinds in the first column of the table
// under heading in docs/domains/PRODUCTION_LOGISTICS.md, with the second
// column's first word (the generic type).
func docTable(t *testing.T, heading string) map[string]string {
	t.Helper()
	doc := string(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "docs/domains/PRODUCTION_LOGISTICS.md")))
	start := strings.Index(doc, heading)
	if start < 0 {
		t.Fatalf("PRODUCTION_LOGISTICS.md has no %q", heading)
	}
	section := doc[start+len(heading):]
	if end := strings.Index(section, "\n#"); end >= 0 {
		section = section[:end]
	}
	out := map[string]string{}
	row := regexp.MustCompile("^\\| `([a-z_]+)` \\| ([a-z]+)")
	for _, line := range strings.Split(section, "\n") {
		if m := row.FindStringSubmatch(line); m != nil {
			out[m[1]] = m[2]
		}
	}
	return out
}

// The catalogs are PRODUCTION_LOGISTICS §2.1 and §2.2: every node kind on
// its generic type, every resource role with its type, each with a label
// and a description.
func TestCatalogsMatchDoc(t *testing.T) {
	kinds := map[string]string{}
	for _, n := range pack.NodeCatalog() {
		if n.Kind == "" || n.Label == "" || n.Description == "" || !slices.Contains(model.NodeTypes, n.Type) {
			t.Errorf("bad node kind %+v", n)
		}
		if _, dup := kinds[n.Kind]; dup {
			t.Errorf("node kind %s twice", n.Kind)
		}
		kinds[n.Kind] = string(n.Type)
		for _, rt := range n.ResourceTypes {
			if !slices.Contains([]string{"human", "machine", "generic"}, rt) {
				t.Errorf("%s requires resource type %q", n.Kind, rt)
			}
		}
	}
	if want := docTable(t, "### 2.1 Node kinds"); len(want) < 10 || !equalMaps(kinds, want) {
		t.Errorf("node kinds %v, the doc's §2.1 lists %v", kinds, want)
	}
	roles := map[string]string{}
	for _, r := range pack.ResourceCatalog() {
		if r.Kind == "" || r.Label == "" || r.Description == "" {
			t.Errorf("bad resource role %+v", r)
		}
		roles[r.Kind] = r.Type
	}
	if want := docTable(t, "### 2.2 Resource roles"); len(want) < 5 || !equalMaps(roles, want) {
		t.Errorf("resource roles %v, the doc's §2.2 lists %v", roles, want)
	}
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
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

// REQ-003: the pack fails closed on a model of another domain or none,
// and has no rule of its own beyond that for its templates.
func TestValidateRejectsOtherDomains(t *testing.T) {
	if issues := pack.Validate(nil); len(issues) != 1 || issues[0].Code != productionlogistics.DomainMismatch || issues[0].Severity != model.SeverityError {
		t.Errorf("nil model: %v", issues)
	}
	for _, name := range productionlogistics.Templates() {
		ir := tmpl(t, name)
		if issues := pack.Validate(ir); len(issues) != 0 {
			t.Errorf("%s: %v", name, issues)
		}
		for _, other := range []string{"electronics_line", "software_delivery", "Production_logistics", "production_logistics ", ""} {
			c := clone(t, ir)
			c.Domain = other
			issues := pack.Validate(c)
			if len(issues) != 1 || issues[0].Code != productionlogistics.DomainMismatch || issues[0].Path != "/domain" || issues[0].Severity != model.SeverityError {
				t.Errorf("%s as %q: %v", name, other, issues)
			}
		}
	}
}

// The pack has no inference and no domain metrics: never a number it
// cannot back (REQ-015).
func TestNoInferenceNoDomainMetrics(t *testing.T) {
	if got, err := pack.InferFromEvents(context.Background(), domainpack.CanonicalEventDataset{}); got != nil || !errors.Is(err, domainpack.ErrUnavailable) {
		t.Errorf("inference: %v %v", got, err)
	}
	if got := pack.ComputeMetrics(nil); got != nil {
		t.Errorf("domain metrics %v", got)
	}
	var p domainpack.DomainPack = pack
	if _, ok := p.(domainpack.ModelMetricsProvider); ok {
		t.Error("the pack offers model metrics")
	}
	if _, ok := p.(domainpack.EventRulesProvider); ok {
		t.Error("the pack offers event rules")
	}
	// The photo-to-line path stays electronics only (H-045).
	if _, ok := p.(domainpack.PhotoVocabulary); ok {
		t.Error("the pack takes photo proposals")
	}
}

// The prompt context is static pack data: the domain, the catalog, and
// rules that say what the templates approximate and that their numbers
// are synthetic.
func TestPromptContext(t *testing.T) {
	p := pack.PromptContext()
	if p.Domain != productionlogistics.ID || !strings.HasPrefix(p.Summary, productionlogistics.Name+":") ||
		len(p.Nodes) != len(pack.NodeCatalog()) || len(p.Resources) != len(pack.ResourceCatalog()) {
		t.Errorf("prompt context %+v", p)
	}
	rules := strings.Join(p.Rules, "\n")
	for _, s := range []string{"minute", "never by product", "model inputs", "batch step", "on-shift time only", "unlimited",
		"Shelf life, temperature, recipes and pick paths are not modelled", "synthetic"} {
		if !strings.Contains(rules, s) {
			t.Errorf("the rules do not say %q", s)
		}
	}
}

// The template positions cover every node, and the default views keep them.
func TestDefaultViewsKeepTemplatePositions(t *testing.T) {
	for _, name := range productionlogistics.Templates() {
		ir := tmpl(t, name)
		got := pack.DefaultViews(ir).Positions
		if len(ir.Views.TwoD.Positions) != len(ir.Nodes) || len(got) != len(ir.Nodes) {
			t.Errorf("%s: %d template positions, %d default ones, %d nodes", name, len(ir.Views.TwoD.Positions), len(got), len(ir.Nodes))
		}
		seen := map[model.Position]string{}
		for _, n := range ir.Nodes {
			p, ok := ir.Views.TwoD.Positions[n.ID]
			if !ok || got[n.ID] != p {
				t.Errorf("%s: %s at %v, default %v", name, n.ID, p, got[n.ID])
			}
			if other, dup := seen[p]; dup {
				t.Errorf("%s: %s and %s share %v", name, n.ID, other, p)
			}
			seen[p] = n.ID
		}
	}
}

// The UI has a label in both languages for the domain, every node kind and
// every resource role (REQ-022); the English ones are the pack's own, and
// the UI labels nothing the pack does not have.
func TestCataloguesHaveUILabels(t *testing.T) {
	read := func(name string) map[string]string {
		src := string(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "apps/web/messages", name)))
		out := map[string]string{}
		for _, m := range regexp.MustCompile(`(?m)^\s*"(domain\.production_logistics\.[a-z_.]+)":\s*"([^"]*)",`).FindAllStringSubmatch(src, -1) {
			out[m[1]] = m[2]
		}
		return out
	}
	en, zh := read("en.ts"), read("zh-Hant.ts")
	want := map[string]string{"domain.production_logistics.name": productionlogistics.Name}
	for _, n := range pack.NodeCatalog() {
		want["domain.production_logistics.node."+n.Kind] = n.Label
	}
	for _, r := range pack.ResourceCatalog() {
		want["domain.production_logistics.resource."+r.Kind] = r.Label
	}
	for k, label := range want {
		if en[k] == "" || zh[k] == "" || en[k] == zh[k] || en[k] != label {
			t.Errorf("%s: en %q, zh-Hant %q, pack label %q", k, en[k], zh[k], label)
		}
	}
	for k := range en {
		if _, ok := want[k]; !ok {
			t.Errorf("the UI labels %s, which the pack does not have", k)
		}
	}
	if len(zh) != len(en) {
		t.Errorf("%d zh-Hant labels for %d en", len(zh), len(en))
	}
}
