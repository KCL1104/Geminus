package electronicsline_test

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
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

func tri(lo, mode, hi float64) model.Distribution {
	return model.Distribution{Type: model.DistTriangular, Min: &lo, Mode: &mode, Max: &hi}
}

func exp(mean float64) model.Distribution {
	return model.Distribution{Type: model.DistExponential, Mean: &mean}
}

func constant(v float64) model.Distribution {
	return model.Distribution{Type: model.DistConstant, Value: &v}
}

func sameDist(a, b model.Distribution) bool {
	x, _ := json.Marshal(a, json.Deterministic(true))
	y, _ := json.Marshal(b, json.Deterministic(true))
	return string(x) == string(y)
}

// REQ-004 / TEST-002: the template is a valid full electronics line with
// every line-flow feature of H-016 that this build runs, from the
// generic validator through the pack's own rules.
func TestTemplate(t *testing.T) {
	ir := tmpl(t)
	if ir.ModelID != newModel || ir.Name != "My line" || ir.Domain != electronicsline.ID || ir.TimeUnit != model.ModelMinute {
		t.Errorf("identity %s %q %s %s", ir.ModelID, ir.Name, ir.Domain, ir.TimeUnit)
	}
	data, err := model.Encode(ir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := validate.IR(data)
	if err != nil || !res.Valid() || len(res.Issues) != 0 {
		t.Fatalf("the template is not valid without issues: %v %v", err, res.Issues)
	}
	var used []string
	for _, u := range validate.Uses(ir) {
		if !slices.Contains(used, string(u.Primitive)) {
			used = append(used, string(u.Primitive))
		}
	}
	slices.Sort(used)
	// Finite buffers (T-047), breakdowns (T-048), shift calendars and
	// release schedules (T-049), the burn-in batch and changeovers (T-050);
	// no declared metric (D-058), which this build does not run yet.
	if want := []string{"batch", "breakdowns", "calendars", "finite_buffer", "release_schedule", "setups"}; !slices.Equal(used, want) {
		t.Errorf("line-flow features %v, want %v", used, want)
	}
	// SMT → AOI → ICT → FCT → burn-in → pack and ship, through positive
	// probability paths, with test failures, debug, retest and scrap.
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
	for _, path := range [][]string{
		{"release_a", "smt", "aoi", "ict", "fct", "burn_in", "pack_ship", "shipped"},
		{"release_b", "smt"},
		{"aoi_result", "aoi_touchup", "ict"},
		{"ict_result", "ict_debug", "ict_retest", "fct"},
		{"ict_debug_result", "scrap"},
		{"fct_result", "fct_debug", "fct_retest", "burn_in"},
		{"fct_debug_result", "scrap"},
		{"burnin_result", "fct_debug"},
	} {
		for i := 1; i < len(path); i++ {
			if !reach(path[i-1], path[i]) {
				t.Errorf("no path %s -> %s", path[i-1], path[i])
			}
		}
	}
}

// The parameters are the synthetic values of ELECTRONICS_LINE §4.2 and
// §4.4 (H-018), tuned to the cited magnitudes of §4.5 (H-046): a change
// to them is a change to the reviewed template.
func TestTemplateParameters(t *testing.T) {
	ir := tmpl(t)
	type res struct {
		typ      model.ResourceType
		role     string
		capacity int
		calendar bool
		ttf, ttr *model.Distribution
	}
	ptr := func(d model.Distribution) *model.Distribution { return &d }
	wantRes := map[string]res{
		"smt_line":        {model.ResourceMachine, "smt_line", 1, true, ptr(exp(600)), ptr(tri(10, 20, 90))},
		"aoi_machine":     {model.ResourceMachine, "aoi", 1, true, ptr(exp(1200)), ptr(tri(5, 10, 30))},
		"ict_tester":      {model.ResourceMachine, "ict_tester", 2, true, ptr(exp(900)), ptr(tri(10, 20, 60))},
		"fct_tester":      {model.ResourceMachine, "fct_tester", 3, true, ptr(exp(900)), ptr(tri(10, 30, 90))},
		"burn_in_chamber": {model.ResourceMachine, "burn_in_chamber", 9, false, ptr(exp(10080)), ptr(tri(60, 120, 480))},
		"debug_operator":  {model.ResourceHuman, "debug_operator", 4, true, nil, nil},
		"pack_station":    {model.ResourceHuman, "pack_station", 2, true, nil, nil},
	}
	if len(ir.Resources) != len(wantRes) {
		t.Errorf("%d resources, want %d", len(ir.Resources), len(wantRes))
	}
	for _, r := range ir.Resources {
		w, ok := wantRes[r.ID]
		if !ok {
			t.Errorf("unexpected resource %s", r.ID)
			continue
		}
		if r.Type != w.typ || r.Role != w.role || r.Capacity != w.capacity || (r.CalendarID == "two_shift_weekdays") != w.calendar {
			t.Errorf("%s: %+v", r.ID, r)
		}
		if (r.Breakdowns == nil) != (w.ttf == nil) || (r.Breakdowns != nil && (!sameDist(r.Breakdowns.TimeToFailure.Distribution, *w.ttf) ||
			!sameDist(r.Breakdowns.TimeToRepair.Distribution, *w.ttr))) {
			t.Errorf("%s breakdowns %+v", r.ID, r.Breakdowns)
		}
	}
	// Changeovers on the SMT line only (§4.4).
	for _, r := range ir.Resources {
		if (r.Setups != nil) != (r.ID == "smt_line") {
			t.Errorf("%s setups %+v", r.ID, r.Setups)
		}
	}
	s := ir.Resources[resourceIndex(t, ir, "smt_line")].Setups
	if s.Initial != "board_a" || s.Default != nil || len(s.Transitions) != 2 ||
		s.Transitions[0].From != "board_a" || s.Transitions[0].To != "board_b" || !sameDist(s.Transitions[0].Distribution, tri(20, 35, 60)) ||
		s.Transitions[1].From != "board_b" || s.Transitions[1].To != "board_a" || !sameDist(s.Transitions[1].Distribution, tri(20, 35, 60)) {
		t.Errorf("SMT changeovers %+v", s)
	}
	service := map[string]model.Distribution{
		"smt": tri(0.75, 0.85, 1.15), "aoi": tri(0.2, 0.25, 0.35), "aoi_touchup": tri(3, 5, 10),
		"ict": tri(1.4, 1.7, 2.3), "ict_retest": tri(1.4, 1.7, 2.3), "ict_debug": tri(5, 10, 30),
		"fct": tri(2.8, 3.2, 4.0), "fct_retest": tri(2.8, 3.2, 4.0), "fct_debug": tri(10, 20, 60),
		"burn_in": constant(480), "pack_ship": tri(0.8, 1.0, 1.5),
	}
	resource := map[string]string{"smt": "smt_line", "aoi": "aoi_machine", "aoi_touchup": "debug_operator", "ict": "ict_tester",
		"ict_retest": "ict_tester", "ict_debug": "debug_operator", "fct": "fct_tester", "fct_retest": "fct_tester", "fct_debug": "debug_operator",
		"burn_in": "burn_in_chamber", "pack_ship": "pack_station"}
	queues := map[string]int{"q_smt": 0, "q_aoi": 20, "q_touchup": 0, "q_ict": 10, "q_ict_debug": 0, "q_ict_retest": 0,
		"q_fct": 10, "q_fct_debug": 0, "q_fct_retest": 0, "q_burnin": 120, "q_pack": 500}
	for _, n := range ir.Nodes {
		switch n.Type {
		case model.NodeProcess:
			d, ok := service[n.ID]
			if !ok || !sameDist(n.ServiceTime.Distribution, d) || len(n.Requires) != 1 || n.Requires[0] != (model.ResourceRequirement{ResourceID: resource[n.ID], Units: 1}) {
				t.Errorf("process %s: %+v %+v", n.ID, n.ServiceTime, n.Requires)
			}
			if (n.Batch != nil) != (n.ID == "burn_in") {
				t.Errorf("%s batch %+v", n.ID, n.Batch)
			}
		case model.NodeQueue:
			c, ok := queues[n.ID]
			if !ok || n.Capacity != c {
				t.Errorf("queue %s capacity %d", n.ID, n.Capacity)
			}
		}
	}
	b := ir.Nodes[nodeIndex(t, ir, "burn_in")].Batch
	if b.MinSize != 20 || b.MaxSize != 40 || b.MaxWait == nil || *b.MaxWait != 120 {
		t.Errorf("burn-in batch %+v", b)
	}
	// Routing probabilities of §4.1.
	prob := map[[2]string]float64{
		{"aoi_result", "q_ict"}: 0.97, {"aoi_result", "aoi_defect"}: 0.03,
		{"ict_result", "q_fct"}: 0.95, {"ict_result", "ict_fail"}: 0.05,
		{"ict_debug_result", "ict_rework"}: 0.97, {"ict_debug_result", "scrap"}: 0.03,
		{"ict_retest_result", "q_fct"}: 0.90, {"ict_retest_result", "ict_fail"}: 0.10,
		{"fct_result", "q_burnin"}: 0.93, {"fct_result", "fct_fail"}: 0.07,
		{"fct_debug_result", "fct_rework"}: 0.95, {"fct_debug_result", "scrap"}: 0.05,
		{"fct_retest_result", "q_burnin"}: 0.90, {"fct_retest_result", "fct_fail"}: 0.10,
		{"burnin_result", "q_pack"}: 0.995, {"burnin_result", "burnin_fail"}: 0.005,
	}
	found := 0
	for _, f := range ir.Flows {
		if p, ok := prob[[2]string{f.From, f.To}]; ok {
			found++
			if f.Routing.Probability != p {
				t.Errorf("%s -> %s: %v, want %v", f.From, f.To, f.Routing.Probability, p)
			}
		} else if f.Routing.Probability != 1 {
			t.Errorf("%s -> %s: %v", f.From, f.To, f.Routing.Probability)
		}
	}
	if found != len(prob) {
		t.Errorf("%d of %d decision flows", found, len(prob))
	}
	// Two families released in whole lots on weekdays (§4.4), Monday 00:00 is t=0.
	for _, src := range []struct {
		id, entity string
		at         float64
	}{{"release_a", "board_a", 360}, {"release_b", "board_b", 840}} {
		n := ir.Nodes[nodeIndex(t, ir, src.id)]
		sc := n.Arrival.Schedule
		if n.EntityType != src.entity || sc == nil || sc.Process != model.ReleaseTable || sc.Period != 10080 || len(sc.Releases) != 5 {
			t.Fatalf("%s: %+v", src.id, n.Arrival)
		}
		for d, r := range sc.Releases {
			if r.At != src.at+float64(d)*1440 || r.Quantity != 360 {
				t.Errorf("%s release %d: %+v", src.id, d, r)
			}
		}
	}
	// Two shifts 06:00-14:00 and 14:00-22:00, Monday to Friday (§4.2).
	if len(ir.Calendars) != 1 || ir.Calendars[0].ID != "two_shift_weekdays" || ir.Calendars[0].Period != 10080 || len(ir.Calendars[0].Shifts) != 10 {
		t.Fatalf("calendars %+v", ir.Calendars)
	}
	for d := range 5 {
		day := float64(d) * 1440
		if s := ir.Calendars[0].Shifts[2*d]; s != (model.Shift{Start: 360 + day, End: 840 + day, Label: "early"}) {
			t.Errorf("day %d early shift %+v", d, s)
		}
		if s := ir.Calendars[0].Shifts[2*d+1]; s != (model.Shift{Start: 840 + day, End: 1320 + day, Label: "late"}) {
			t.Errorf("day %d late shift %+v", d, s)
		}
	}
	// The people on each shift are the whole crew: listed so that a
	// scenario may change one shift's crew (D-057), the same as capacity.
	for id, c := range map[string]int{"debug_operator": 4, "pack_station": 2} {
		if got := ir.Resources[resourceIndex(t, ir, id)].ShiftCapacity; !maps.Equal(got, map[string]int{"early": c, "late": c}) {
			t.Errorf("%s shift capacity %v", id, got)
		}
	}
	// Station and test result attributes (§3.3).
	station := map[string][2]string{"smt": {"smt", "first"}, "aoi": {"aoi", "first"}, "ict": {"ict", "first"}, "ict_retest": {"ict", "retest"},
		"fct": {"fct", "first"}, "fct_retest": {"fct", "retest"}, "burn_in": {"burn_in", "first"}, "pack_ship": {"pack", "first"}}
	result := map[string]string{"aoi_result": "aoi", "ict_result": "ict", "ict_retest_result": "ict_retest", "fct_result": "fct",
		"fct_retest_result": "fct_retest", "burnin_result": "burn_in"}
	for _, n := range ir.Nodes {
		var a struct {
			Station  string `json:"station"`
			Pass     string `json:"pass"`
			ResultOf string `json:"result_of"`
		}
		if len(n.DomainAttributes) > 0 {
			if err := json.Unmarshal(n.DomainAttributes, &a); err != nil {
				t.Fatal(err)
			}
		}
		if s := station[n.ID]; s != [2]string{a.Station, a.Pass} {
			t.Errorf("%s station attributes %+v", n.ID, a)
		}
		if result[n.ID] != a.ResultOf {
			t.Errorf("%s result_of %q", n.ID, a.ResultOf)
		}
	}
}

// ELECTRONICS_LINE §3.4 rule 4: the template's numbers are labelled
// synthetic, and a model created from it says so but is not a fixture.
func TestTemplateMetadata(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("templates", electronicsline.DefaultTemplate+".json"))
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
	if err := json.Unmarshal(tmpl(t).Metadata, &meta); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]map[string]any{"the template file": file.Metadata, "a created model": meta} {
		if m["synthetic"] != true || m["source"] != "synthetic, for demonstration" {
			t.Errorf("%s: synthetic labelling %v", name, m)
		}
		origin, _ := m["origin"].(string)
		if !strings.Contains(origin, "NOT customer data") || !strings.Contains(origin, "ELECTRONICS_LINE") {
			t.Errorf("%s: origin %q", name, origin)
		}
		if a, _ := m["assumptions"].([]any); len(a) < 5 {
			t.Errorf("%s: assumptions %v", name, m["assumptions"])
		}
		if _, isFixture := m["fixture"]; isFixture {
			t.Errorf("%s is marked a fixture", name)
		}
	}
	if meta["template"] != electronicsline.DefaultTemplate {
		t.Errorf("a created model does not name its template: %v", meta)
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

// REQ-022: the template is the same model in every locale; every entity
// type, resource and node has a name in both, the Traditional Chinese one
// its own, and the name the user gave is kept as given.
func TestTemplateLocales(t *testing.T) {
	en := tmpl(t)
	zh, err := electronicsline.Template(electronicsline.DefaultTemplate, newModel, "我的產線", domainpack.TraditionalChinese)
	if err != nil {
		t.Fatal(err)
	}
	if zh.Name != "我的產線" || en.Name != "My line" {
		t.Errorf("model names %q %q", en.Name, zh.Name)
	}
	ne, nz := names(en), names(zh)
	if len(ne) != len(en.EntityTypes)+len(en.Resources)+len(en.Nodes) {
		t.Errorf("IDs repeat across catalogs: %d names", len(ne))
	}
	for id, e := range ne {
		z := nz[id]
		if e == "" || z == "" || e == z {
			t.Errorf("%s: en %q, zh-Hant %q", id, e, z)
		}
	}
	if ne["smt"] != "SMT line (printer, SPI, placement, reflow)" || nz["smt"] != "SMT 線（印刷、SPI、置件、迴焊）" || nz["board_a"] != "A 系列板" {
		t.Errorf("names %q %q %q", ne["smt"], nz["smt"], nz["board_a"])
	}
	if stripNames(t, en) != stripNames(t, zh) {
		t.Error("the zh-Hant template differs from the en one in more than its names")
	}
	if issues := pack.Validate(zh); len(issues) != 0 {
		t.Errorf("zh-Hant template: domain issues %v", issues)
	}
	for _, l := range []domainpack.Locale{"", "fr", "zh-hant", "zh-Hans", "EN", "en-US"} {
		if _, err := electronicsline.Template(electronicsline.DefaultTemplate, newModel, "x", l); !errors.Is(err, electronicsline.ErrUnknownLocale) {
			t.Errorf("locale %q: %v", l, err)
		}
	}
	var provider domainpack.TemplateProvider = electronicsline.Pack{}
	if got, err := provider.Template(electronicsline.DefaultTemplate, newModel, "x", domainpack.TraditionalChinese); err != nil || names(got)["board_b"] != "B 系列板" {
		t.Errorf("Pack.Template: %v %v", got, err)
	}
	if !slices.Equal(provider.Templates(), electronicsline.Templates()) || !slices.Equal(electronicsline.Templates(), []string{electronicsline.DefaultTemplate, electronicsline.PhotoStartTemplate}) {
		t.Errorf("Pack.Templates %v", provider.Templates())
	}
}

// Template errors are typed; the issues carry JSON Pointers; nothing the
// caller sent is echoed into the error text.
func TestTemplateErrors(t *testing.T) {
	if _, err := electronicsline.Template("default_delivery_pipeline", newModel, "x", domainpack.English); !errors.Is(err, electronicsline.ErrUnknownTemplate) {
		t.Errorf("another pack's template: %v", err)
	}
	if _, err := electronicsline.Template(electronicsline.DefaultTemplate, "mdl_bad", "x", domainpack.English); !errors.Is(err, electronicsline.ErrInvalidModelID) {
		t.Errorf("bad model ID: %v", err)
	}
	for _, name := range []string{"", strings.Repeat("😀", 201), "bad \xff"} {
		_, err := electronicsline.Template(electronicsline.DefaultTemplate, newModel, name, domainpack.English)
		var te *electronicsline.InvalidError
		if !errors.As(err, &te) || !errors.Is(err, electronicsline.ErrInvalidTemplateInput) || len(te.Issues) == 0 {
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

// A names file must name exactly the template's named elements, so a
// locale never shows a mix of languages.
func TestNamesFileStrict(t *testing.T) {
	en := tmpl(t)
	raw, err := os.ReadFile(filepath.Join("templates", electronicsline.DefaultTemplate+".zh-Hant.names.json"))
	if err != nil {
		t.Fatal(err)
	}
	var complete map[string]map[string]string
	if err := json.Unmarshal(raw, &complete); err != nil {
		t.Fatal(err)
	}
	with := func(section, id string, name any) []byte {
		doc := map[string]map[string]string{}
		for k, v := range complete {
			doc[k] = maps.Clone(v)
		}
		switch n := name.(type) {
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
	if err := electronicsline.ApplyNames(clone(t, en), electronicsline.DefaultTemplate, domainpack.TraditionalChinese, raw); err != nil {
		t.Fatalf("the embedded names: %v", err)
	}
	for name, data := range map[string][]byte{
		"missing entity type": with("entity_types", "board_a", nil),
		"missing resource":    with("resources", "fct_tester", nil),
		"missing node":        with("nodes", "burn_in", nil),
		"empty name":          with("nodes", "smt", ""),
		"blank name":          with("nodes", "smt", " 　 "),
		"control character":   with("nodes", "smt", "SMT\u0007線"),
		"over-long name":      with("nodes", "smt", strings.Repeat("長", 201)),
		"extra node":          with("nodes", "xray", "X 光"),
		"extra resource":      with("resources", "robot", "機器人"),
		"unknown member":      []byte(strings.Replace(string(raw), `"nodes"`, `"labels": {}, "nodes"`, 1)),
		"broken JSON":         []byte(`{"entity_types":`),
	} {
		if err := electronicsline.ApplyNames(clone(t, en), electronicsline.DefaultTemplate, domainpack.TraditionalChinese, data); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A broken names file is the pack's fault, not the caller's input, and
	// gives no model.
	base, err := os.ReadFile(filepath.Join("templates", electronicsline.DefaultTemplate+".json"))
	if err != nil {
		t.Fatal(err)
	}
	tmplFile, namesFile := "templates/"+electronicsline.DefaultTemplate+".json", "templates/"+electronicsline.DefaultTemplate+".zh-Hant.names.json"
	files := func(m map[string][]byte) func(string) ([]byte, error) {
		return func(name string) ([]byte, error) {
			if data, ok := m[name]; ok {
				return data, nil
			}
			return nil, os.ErrNotExist
		}
	}
	for name, read := range map[string]func(string) ([]byte, error){
		"no names file":  files(map[string][]byte{tmplFile: base}),
		"a name missing": files(map[string][]byte{tmplFile: base, namesFile: with("nodes", "q_fct", nil)}),
	} {
		ir, err := electronicsline.TemplateFrom(read, electronicsline.DefaultTemplate, newModel, "x", domainpack.TraditionalChinese)
		var invalid *electronicsline.InvalidError
		if err == nil || ir != nil || errors.As(err, &invalid) || errors.Is(err, electronicsline.ErrInvalidTemplateInput) {
			t.Errorf("%s: %v %v", name, ir, err)
		}
	}
	got, err := electronicsline.TemplateFrom(files(map[string][]byte{tmplFile: base, namesFile: raw}), electronicsline.DefaultTemplate, newModel, "x", domainpack.TraditionalChinese)
	want, _ := electronicsline.Template(electronicsline.DefaultTemplate, newModel, "x", domainpack.TraditionalChinese)
	if err != nil || want == nil || stripNames(t, got) != stripNames(t, want) || !maps.Equal(names(got), names(want)) {
		t.Errorf("TemplateFrom with the embedded files differs from Template: %v", err)
	}
}
