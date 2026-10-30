package productionlogistics_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
	"github.com/kcl1104/geminus/internal/domainpack/productionlogistics"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// expected is what PRODUCTION_LOGISTICS §3 says of each template.
type expected struct {
	primitives []string // line-flow features, sorted
	families   []string // entity types
	daily      [7]int   // items released each day, Monday first
	bottleneck string   // resource
	flow       []string // the main line, source to completed sink
	calendar   string
	workdays   int           // days with shifts, from Monday
	shifts     []model.Shift // each working day's shifts, as on Monday
}

var templates = map[string]expected{
	productionlogistics.ShoeFactory: {
		// The lasts travel with each pair: no changeover, so no setups.
		primitives: []string{"batch", "breakdowns", "calendars", "finite_buffer", "release_schedule"},
		families:   []string{"high_top", "low_top"}, daily: [7]int{600, 600, 600, 600, 600}, bottleneck: "stitcher",
		flow:     []string{"release_high_top", "cutting", "stitching", "lasting", "sole_attaching", "vulcanizing", "finishing", "inspection", "packing", "packed"},
		calendar: "day_shift", workdays: 5,
		shifts: []model.Shift{{Start: 480, End: 720, Label: "morning"}, {Start: 780, End: 1020, Label: "afternoon"}},
	},
	productionlogistics.FoodPlant: {
		primitives: []string{"batch", "breakdowns", "calendars", "finite_buffer", "release_schedule", "setups"},
		// 32 dough orders of 30 lots a day, Monday to Saturday; on Friday the second shift's are sesame bread.
		families: []string{"white_bread", "sesame_bread"}, daily: [7]int{960, 960, 960, 960, 960, 960}, bottleneck: "tunnel_oven",
		flow:     []string{"release_white", "mixing", "floor_time", "makeup", "final_proof", "baking", "cooling", "slicing", "detection", "dispatched"},
		calendar: "two_ten_hour_shifts", workdays: 6,
		shifts: []model.Shift{{Start: 240, End: 840, Label: "first"}, {Start: 840, End: 1440, Label: "second"}},
	},
	productionlogistics.CartonPlant: {
		// One load per item; no batch step.
		primitives: []string{"breakdowns", "calendars", "finite_buffer", "release_schedule", "setups"},
		families:   []string{"plain_rsc", "printed_rsc", "die_cut_tray", "die_cut_display"}, daily: [7]int{148, 148, 148, 148, 148}, bottleneck: "flexo_folder_gluer",
		flow:     []string{"orders_plain", "corrugate_rsc", "ffg", "strapping", "palletizing", "shipped"},
		calendar: "two_shift_weekdays", workdays: 5,
		shifts: []model.Shift{{Start: 360, End: 840, Label: "early"}, {Start: 840, End: 1320, Label: "late"}},
	},
	productionlogistics.Warehouse: {
		// One entity type: no family changes, so no setups.
		primitives: []string{"batch", "breakdowns", "calendars", "finite_buffer", "release_schedule"},
		families:   []string{"case"}, daily: [7]int{6000, 6000, 6000, 6000, 6000}, bottleneck: "picker",
		flow:     []string{"inbound_trailers", "receiving", "putaway", "picking", "packing", "loading", "shipped"},
		calendar: "two_shift_weekdays", workdays: 5,
		shifts: []model.Shift{{Start: 360, End: 840, Label: "early"}, {Start: 840, End: 1320, Label: "late"}},
	},
}

func TestTemplatesList(t *testing.T) {
	want := []string{"shoe_factory", "food_plant", "carton_plant", "warehouse"}
	if got := productionlogistics.Templates(); !slices.Equal(got, want) || len(templates) != len(want) {
		t.Errorf("templates %v", got)
	}
	var provider domainpack.TemplateProvider = pack
	if !slices.Equal(provider.Templates(), want) {
		t.Errorf("Pack.Templates %v", provider.Templates())
	}
}

// H-045, REQ-004: each template is a valid model of the pack's domain
// without a single issue, built from the generic primitives PRODUCTION_LOGISTICS
// §3 lists, with its main line, its release volume, two weekday shifts,
// resources whose roles are the catalog's, and an unlimited buffer after
// every failure, so no full buffer blocks in a loop (D-050).
func TestTemplates(t *testing.T) {
	roles := map[string]string{}
	for _, r := range pack.ResourceCatalog() {
		roles[r.Kind] = r.Type
	}
	for name, w := range templates {
		ir := tmpl(t, name)
		if ir.ModelID != newModel || ir.Name != "My plant" || ir.Domain != productionlogistics.ID || ir.TimeUnit != model.ModelMinute || ir.SchemaVersion != "0.1" {
			t.Errorf("%s: identity %s %q %s %s", name, ir.ModelID, ir.Name, ir.Domain, ir.TimeUnit)
		}
		data, err := model.Encode(ir)
		if err != nil {
			t.Fatal(err)
		}
		res, err := validate.IR(data)
		if err != nil || !res.Valid() || len(res.Issues) != 0 {
			t.Errorf("%s is not valid without issues: %v %v", name, err, res.Issues)
		}
		var used []string
		for _, u := range validate.Uses(ir) {
			if !slices.Contains(used, string(u.Primitive)) {
				used = append(used, string(u.Primitive))
			}
		}
		slices.Sort(used)
		if !slices.Equal(used, w.primitives) {
			t.Errorf("%s: line-flow features %v, want %v", name, used, w.primitives)
		}
		var families []string
		for _, e := range ir.EntityTypes {
			families = append(families, e.ID)
		}
		if !slices.Equal(families, w.families) {
			t.Errorf("%s: entity types %v", name, families)
		}

		byID := map[string]model.Node{}
		next := map[string][]string{}
		for _, n := range ir.Nodes {
			byID[n.ID] = n
		}
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
		for i := 1; i < len(w.flow); i++ {
			if !reach(w.flow[i-1], w.flow[i]) {
				t.Errorf("%s: no path %s -> %s", name, w.flow[i-1], w.flow[i])
			}
		}
		if last := byID[w.flow[len(w.flow)-1]]; last.Type != model.NodeSink || last.Outcome != model.SinkCompleted {
			t.Errorf("%s: the line ends at %+v", name, last)
		}
		// The first buffer after a failure is unlimited.
		for _, n := range ir.Nodes {
			if n.Type != model.NodeFailure {
				continue
			}
			for frontier, seen := []string{n.ID}, map[string]bool{}; len(frontier) > 0; {
				id := frontier[0]
				frontier = frontier[1:]
				if seen[id] {
					continue
				}
				seen[id] = true
				switch m := byID[id]; m.Type {
				case model.NodeQueue:
					if m.Capacity != 0 {
						t.Errorf("%s: %s after the failure %s has capacity %d", name, id, n.ID, m.Capacity)
					}
				case model.NodeSink:
				default:
					frontier = append(frontier, next[id]...)
				}
			}
		}
		finite := 0
		for _, n := range ir.Nodes {
			if n.Type == model.NodeQueue && n.Capacity > 0 {
				finite++
			}
			if n.Type == model.NodeProcess && len(n.Requires) != 1 {
				t.Errorf("%s: %s needs %d resources, not one", name, n.ID, len(n.Requires))
			}
		}
		if finite < 3 {
			t.Errorf("%s: %d finite buffers", name, finite)
		}

		// The release volume of each day, as §3 says.
		released := map[int]int{}
		for _, n := range ir.Nodes {
			if n.Type != model.NodeSource {
				continue
			}
			sc := n.Arrival.Schedule
			if sc == nil || sc.Process != model.ReleaseTable || sc.Period != 10080 {
				t.Fatalf("%s: %s arrival %+v", name, n.ID, n.Arrival)
			}
			for _, r := range sc.Releases {
				released[int(r.At)/1440] += r.Quantity
			}
		}
		for day := range 7 {
			if released[day] != w.daily[day] {
				t.Errorf("%s: day %d releases %d, want %d", name, day, released[day], w.daily[day])
			}
		}
		// The same shifts every working day, and no other calendar.
		if len(ir.Calendars) != 1 || ir.Calendars[0].ID != w.calendar || ir.Calendars[0].Period != 10080 || len(ir.Calendars[0].Shifts) != w.workdays*len(w.shifts) {
			t.Fatalf("%s: calendars %+v", name, ir.Calendars)
		}
		for d := range w.workdays {
			for k, want := range w.shifts {
				want.Start, want.End = want.Start+float64(d)*1440, want.End+float64(d)*1440
				if got := ir.Calendars[0].Shifts[d*len(w.shifts)+k]; got != want {
					t.Errorf("%s: day %d shift %d %+v, want %+v", name, d, k, got, want)
				}
			}
		}
		bottleneck := false
		for _, r := range ir.Resources {
			if typ, ok := roles[r.Role]; !ok || typ != string(r.Type) {
				t.Errorf("%s: resource %s has role %q of type %q, the catalog's is %q", name, r.ID, r.Role, r.Type, typ)
			}
			bottleneck = bottleneck || r.ID == w.bottleneck
		}
		if !bottleneck {
			t.Errorf("%s: no resource %s", name, w.bottleneck)
		}
		var metrics []string
		for _, m := range ir.Metrics {
			metrics = append(metrics, string(m.Type)+":"+m.ResourceID)
		}
		for _, m := range []string{"entity_cycle_time:", "sink_throughput:", "resource_utilization:" + w.bottleneck} {
			if !slices.Contains(metrics, m) {
				t.Errorf("%s: metrics %v have no %s", name, metrics, m)
			}
		}
	}
}

// The templates' numbers are labelled synthetic, and a model created from
// one says so and names its template, but is not a fixture.
func TestTemplateMetadata(t *testing.T) {
	for _, name := range productionlogistics.Templates() {
		raw, err := os.ReadFile(filepath.Join("templates", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var file struct {
			Metadata map[string]any `json:"metadata"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			t.Fatal(err)
		}
		var meta map[string]any
		if err := json.Unmarshal(tmpl(t, name).Metadata, &meta); err != nil {
			t.Fatal(err)
		}
		for what, m := range map[string]map[string]any{"the template file": file.Metadata, "a created model": meta} {
			if m["synthetic"] != true || m["source"] != "synthetic, for demonstration" {
				t.Errorf("%s, %s: synthetic labelling %v", name, what, m)
			}
			origin, _ := m["origin"].(string)
			if !strings.Contains(origin, "NOT customer data") || !strings.Contains(origin, "PRODUCTION_LOGISTICS") {
				t.Errorf("%s, %s: origin %q", name, what, origin)
			}
			if a, _ := m["assumptions"].([]any); len(a) < 5 {
				t.Errorf("%s, %s: assumptions %v", name, what, m["assumptions"])
			}
			if _, isFixture := m["fixture"]; isFixture {
				t.Errorf("%s, %s is marked a fixture", name, what)
			}
		}
		if meta["template"] != name {
			t.Errorf("%s: a created model does not name its template: %v", name, meta)
		}
	}
}

func names(ir *model.ModelIR) map[string]string {
	out := map[string]string{}
	for _, e := range ir.EntityTypes {
		out[e.ID] = e.Name
	}
	for _, r := range ir.Resources {
		out[r.ID] = r.Name
	}
	for _, n := range ir.Nodes {
		out[n.ID] = n.Name
	}
	return out
}

func stripNames(t *testing.T, ir *model.ModelIR) string {
	t.Helper()
	c := clone(t, ir)
	c.Name = ""
	for i := range c.EntityTypes {
		c.EntityTypes[i].Name = ""
	}
	for i := range c.Resources {
		c.Resources[i].Name = ""
	}
	for i := range c.Nodes {
		c.Nodes[i].Name = ""
	}
	data, err := model.Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	v := jsontext.Value(data)
	if err := v.Canonicalize(); err != nil {
		t.Fatal(err)
	}
	return string(v)
}

// REQ-022: each template is the same model in every locale; every entity
// type, resource and node has a name in both, the Traditional Chinese one
// its own, and the name the user gave is kept as given. Nothing else in a
// template carries text a locale would have to translate.
func TestTemplateLocales(t *testing.T) {
	for _, name := range productionlogistics.Templates() {
		en := tmpl(t, name)
		zh, err := productionlogistics.Template(name, newModel, "我的工廠", domainpack.TraditionalChinese)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if zh.Name != "我的工廠" {
			t.Errorf("%s: model name %q", name, zh.Name)
		}
		ne, nz := names(en), names(zh)
		if len(ne) != len(en.EntityTypes)+len(en.Resources)+len(en.Nodes) {
			t.Errorf("%s: IDs repeat across catalogs: %d names", name, len(ne))
		}
		for id, e := range ne {
			if z := nz[id]; e == "" || z == "" || e == z {
				t.Errorf("%s, %s: en %q, zh-Hant %q", name, id, e, z)
			}
		}
		if stripNames(t, en) != stripNames(t, zh) {
			t.Errorf("%s: the zh-Hant template differs from the en one in more than its names", name)
		}
		for _, c := range en.Calendars {
			if c.Name != "" {
				t.Errorf("%s: calendar %s has an untranslated name %q", name, c.ID, c.Name)
			}
		}
		for _, n := range en.Nodes {
			if n.Reason != "" {
				t.Errorf("%s: %s has an untranslated reason %q", name, n.ID, n.Reason)
			}
		}
		if issues := pack.Validate(zh); len(issues) != 0 {
			t.Errorf("%s in zh-Hant: domain issues %v", name, issues)
		}
		for _, l := range []domainpack.Locale{"", "fr", "zh-hant", "zh-Hans", "EN", "en-US"} {
			if _, err := productionlogistics.Template(name, newModel, "x", l); !errors.Is(err, productionlogistics.ErrUnknownLocale) {
				t.Errorf("%s, locale %q: %v", name, l, err)
			}
		}
	}
	var provider domainpack.TemplateProvider = pack
	if got, err := provider.Template(productionlogistics.ShoeFactory, newModel, "x", domainpack.TraditionalChinese); err != nil || names(got)["lasting_machine"] != "攀幫機" {
		t.Errorf("Pack.Template: %v %v", got, err)
	}
}

// Template errors are typed; the issues carry JSON Pointers; nothing the
// caller sent is echoed into the error text. Another pack's template is
// unknown here, and these templates are unknown to the other packs.
func TestTemplateErrors(t *testing.T) {
	for _, other := range append(electronicsline.Templates(), softwaredelivery.Templates()...) {
		if _, err := productionlogistics.Template(other, newModel, "x", domainpack.English); !errors.Is(err, productionlogistics.ErrUnknownTemplate) {
			t.Errorf("another pack's template %s: %v", other, err)
		}
	}
	for _, name := range productionlogistics.Templates() {
		if _, err := electronicsline.Template(name, newModel, "x", domainpack.English); !errors.Is(err, electronicsline.ErrUnknownTemplate) {
			t.Errorf("the electronics pack builds %s: %v", name, err)
		}
		if _, err := softwaredelivery.Template(name, newModel, "x", domainpack.English); !errors.Is(err, softwaredelivery.ErrUnknownTemplate) {
			t.Errorf("the software delivery pack builds %s: %v", name, err)
		}
		for _, v := range []string{strings.ToUpper(name), " " + name, name + ".json", "templates/" + name} {
			if _, err := productionlogistics.Template(v, newModel, "x", domainpack.English); !errors.Is(err, productionlogistics.ErrUnknownTemplate) {
				t.Errorf("template %q: %v", v, err)
			}
		}
		if _, err := productionlogistics.Template(name, "mdl_bad", "x", domainpack.English); !errors.Is(err, productionlogistics.ErrInvalidModelID) {
			t.Errorf("%s, bad model ID: %v", name, err)
		}
		for _, modelName := range []string{"", strings.Repeat("😀", 201), "bad \xff"} {
			_, err := productionlogistics.Template(name, newModel, modelName, domainpack.English)
			var te *productionlogistics.InvalidError
			if !errors.As(err, &te) || !errors.Is(err, productionlogistics.ErrInvalidTemplateInput) || len(te.Issues) == 0 {
				t.Errorf("%s, name %q: %v", name, modelName, err)
				continue
			}
			if !slices.ContainsFunc(te.Issues, func(i model.ValidationIssue) bool { return strings.HasPrefix(i.Path, "/name") }) {
				t.Errorf("%s, name %q: issues %v do not point at /name", name, modelName, te.Issues)
			}
			if strings.Contains(err.Error(), "😀") || len(err.Error()) > 200 {
				t.Errorf("error echoes the input: %q", err.Error())
			}
		}
	}
}

// A names file must name exactly its template's named elements, so a
// locale never shows a mix of languages; each embedded one does.
func TestNamesFileStrict(t *testing.T) {
	for _, name := range productionlogistics.Templates() {
		raw, err := os.ReadFile(filepath.Join("templates", name+".zh-Hant.names.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := productionlogistics.ApplyNames(tmpl(t, name), name, domainpack.TraditionalChinese, raw); err != nil {
			t.Errorf("%s: the embedded names: %v", name, err)
		}
	}
	name := productionlogistics.FoodPlant
	en := tmpl(t, name)
	raw, err := os.ReadFile(filepath.Join("templates", name+".zh-Hant.names.json"))
	if err != nil {
		t.Fatal(err)
	}
	var complete map[string]map[string]string
	if err := json.Unmarshal(raw, &complete); err != nil {
		t.Fatal(err)
	}
	with := func(section, id string, n any) []byte {
		doc := map[string]map[string]string{}
		for k, v := range complete {
			doc[k] = maps.Clone(v)
		}
		switch n := n.(type) {
		case nil:
			delete(doc[section], id)
		case string:
			doc[section][id] = n
		}
		data, err := json.Marshal(doc, json.Deterministic(true))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for what, data := range map[string][]byte{
		"missing entity type": with("entity_types", "sesame_bread", nil),
		"missing resource":    with("resources", "tunnel_oven", nil),
		"missing node":        with("nodes", "baking", nil),
		"empty name":          with("nodes", "mixing", ""),
		"blank name":          with("nodes", "mixing", " 　 "),
		"control character":   with("nodes", "mixing", "攪拌\u0007"),
		"over-long name":      with("nodes", "mixing", strings.Repeat("長", 201)),
		"extra node":          with("nodes", "freezer", "冷凍庫"),
		"extra resource":      with("resources", "robot", "機器人"),
		"unknown member":      []byte(strings.Replace(string(raw), `"nodes"`, `"labels": {}, "nodes"`, 1)),
		"broken JSON":         []byte(`{"entity_types":`),
	} {
		if err := productionlogistics.ApplyNames(clone(t, en), name, domainpack.TraditionalChinese, data); err == nil {
			t.Errorf("%s: accepted", what)
		}
	}
	// A broken names file is the pack's fault, not the caller's input, and
	// gives no model.
	base, err := os.ReadFile(filepath.Join("templates", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	tmplFile, namesFile := "templates/"+name+".json", "templates/"+name+".zh-Hant.names.json"
	files := func(m map[string][]byte) func(string) ([]byte, error) {
		return func(n string) ([]byte, error) {
			if data, ok := m[n]; ok {
				return data, nil
			}
			return nil, os.ErrNotExist
		}
	}
	for what, read := range map[string]func(string) ([]byte, error){
		"no names file":  files(map[string][]byte{tmplFile: base}),
		"a name missing": files(map[string][]byte{tmplFile: base, namesFile: with("nodes", "q_oven", nil)}),
		"no template":    files(map[string][]byte{namesFile: raw}),
		"unlabelled":     files(map[string][]byte{tmplFile: []byte(strings.Replace(string(base), `"synthetic": true`, `"synthetic": false`, 1)), namesFile: raw}),
	} {
		ir, err := productionlogistics.TemplateFrom(read, name, newModel, "x", domainpack.TraditionalChinese)
		var invalid *productionlogistics.InvalidError
		if err == nil || ir != nil || errors.As(err, &invalid) || errors.Is(err, productionlogistics.ErrInvalidTemplateInput) {
			t.Errorf("%s: %v %v", what, ir, err)
		}
	}
	got, err := productionlogistics.TemplateFrom(files(map[string][]byte{tmplFile: base, namesFile: raw}), name, newModel, "x", domainpack.TraditionalChinese)
	want, _ := productionlogistics.Template(name, newModel, "x", domainpack.TraditionalChinese)
	if err != nil || want == nil || stripNames(t, got) != stripNames(t, want) || !maps.Equal(names(got), names(want)) {
		t.Errorf("TemplateFrom with the embedded files differs from Template: %v", err)
	}
}
