package electronicsline_test

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
)

// H-062: a new model can start from a photo or floor plan. The server
// creates it from the pack's photo_start template, the smallest line a photo
// proposal can build on, and the page then sends the photo as the model's
// first photo proposal. The template is the bare line the live photo runs
// passed on (testdata/photos/bare-line.json, H-054 §11): a source, a buffer,
// one SMT step and the sink. Smaller does not work: a photo proposal may only
// copy numbers the base already has (review.Photo), so without a process
// step a new station would have no service time to copy.

func photoStart(t *testing.T, name string, locale domainpack.Locale) *model.ModelIR {
	t.Helper()
	ir, err := electronicsline.Template(electronicsline.PhotoStartTemplate, newModel, name, locale)
	if err != nil {
		t.Fatal(err)
	}
	return ir
}

// encodedWithout is ir's canonical encoding without its identity's name and
// model ID and without its metadata.
func encodedWithout(t *testing.T, ir *model.ModelIR) string {
	t.Helper()
	c := clone(t, ir)
	c.ModelID, c.Name, c.Metadata = "", "", nil
	return canonical(t, c)
}

// canonical is ir's RFC 8785 encoding (model.Encode does not order maps).
func canonical(t *testing.T, ir *model.ModelIR) string {
	t.Helper()
	data, err := model.Encode(ir)
	if err != nil {
		t.Fatal(err)
	}
	v := jsontext.Value(data)
	if err := v.Canonicalize(); err != nil {
		t.Fatal(err)
	}
	return string(v)
}

// The pack offers the full line first, then the photo start.
func TestTemplatesListed(t *testing.T) {
	want := []string{electronicsline.DefaultTemplate, electronicsline.PhotoStartTemplate}
	if got := electronicsline.Templates(); !slices.Equal(got, want) {
		t.Errorf("Templates %v, want %v", got, want)
	}
	if electronicsline.PhotoStartTemplate != "photo_start" {
		t.Errorf("the contract names the template photo_start, not %q", electronicsline.PhotoStartTemplate)
	}
}

// The template is valid without any issue, generic or the pack's, is the
// model asked for, and is the live-tested bare line apart from its identity
// and metadata, the same on every call.
func TestPhotoStartTemplate(t *testing.T) {
	ir := photoStart(t, "Line from a photo", domainpack.English)
	if ir.ModelID != newModel || ir.Name != "Line from a photo" || ir.Domain != electronicsline.ID || ir.TimeUnit != model.ModelMinute {
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
	if issues := pack.Validate(ir); len(issues) != 0 {
		t.Errorf("the pack's rules: %v", issues)
	}
	var bare model.ModelIR
	if err := model.Decode(contracttest.BareLine(t), &bare); err != nil {
		t.Fatal(err)
	}
	if encodedWithout(t, ir) != encodedWithout(t, &bare) {
		t.Error("the photo start template is not the bare line of testdata/photos/bare-line.json apart from its identity and metadata")
	}
	if again := photoStart(t, "Line from a photo", domainpack.English); canonical(t, again) != canonical(t, ir) {
		t.Error("two calls give two models")
	}
	// What a photo proposal needs from its base: one process step whose
	// service time a new station can copy, on one resource of capacity 1,
	// and probability 1 on every flow (a single route's), nothing more.
	var steps []string
	for _, n := range ir.Nodes {
		if n.Type == model.NodeProcess {
			steps = append(steps, n.ID)
			if n.ServiceTime == nil || n.ServiceTime.Distribution.Type != model.DistTriangular || len(n.Requires) != 1 || n.Requires[0].ResourceID != "smt_line" {
				t.Errorf("step %s: %+v %+v", n.ID, n.ServiceTime, n.Requires)
			}
		}
	}
	if !slices.Equal(steps, []string{"smt"}) || len(ir.Nodes) != 4 || len(ir.Resources) != 1 || ir.Resources[0].Capacity != 1 || len(ir.EntityTypes) != 1 {
		t.Errorf("steps %v, %d nodes, resources %+v, %d entity types", steps, len(ir.Nodes), ir.Resources, len(ir.EntityTypes))
	}
	for _, f := range ir.Flows {
		if f.Routing.Probability != 1 {
			t.Errorf("%s: probability %v", f.ID, f.Routing.Probability)
		}
	}
	if len(ir.Calendars) != 0 || len(ir.Constraints) != 0 || len(ir.Metrics) != 0 {
		t.Errorf("calendars %v, constraints %v, metrics %v", ir.Calendars, ir.Constraints, ir.Metrics)
	}
}

// ELECTRONICS_LINE §3.4 rule 4: the numbers are labelled synthetic, and a
// model created from the template names it.
func TestPhotoStartMetadata(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("templates", electronicsline.PhotoStartTemplate+".json"))
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
	if err := json.Unmarshal(photoStart(t, "x", domainpack.English).Metadata, &meta); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]map[string]any{"the template file": file.Metadata, "a created model": meta} {
		if m["synthetic"] != true || m["source"] != "synthetic, for demonstration" {
			t.Errorf("%s: synthetic labelling %v", name, m)
		}
		origin, _ := m["origin"].(string)
		if !strings.Contains(origin, "NOT customer data") || !strings.Contains(origin, "ELECTRONICS_LINE") || !strings.Contains(origin, "bare-line.json") {
			t.Errorf("%s: origin %q", name, origin)
		}
		if a, _ := m["assumptions"].([]any); len(a) < 2 {
			t.Errorf("%s: assumptions %v", name, m["assumptions"])
		}
		if _, isFixture := m["fixture"]; isFixture {
			t.Errorf("%s is marked a fixture", name)
		}
	}
	if meta["template"] != electronicsline.PhotoStartTemplate {
		t.Errorf("a created model does not name its template: %v", meta)
	}
}

// REQ-022: every named element has a name of its own in both languages, and
// the two are the same model apart from the names; an unknown locale or an
// unusable name is refused as for the full template.
func TestPhotoStartLocales(t *testing.T) {
	en := photoStart(t, "Line from a photo", domainpack.English)
	zh := photoStart(t, "照片建立的產線", domainpack.TraditionalChinese)
	ne, nz := names(en), names(zh)
	if len(ne) != len(en.EntityTypes)+len(en.Resources)+len(en.Nodes) {
		t.Errorf("IDs repeat across catalogs: %d names", len(ne))
	}
	for id, e := range ne {
		if z := nz[id]; e == "" || z == "" || e == z {
			t.Errorf("%s: en %q, zh-Hant %q", id, e, z)
		}
	}
	if ne["smt"] != "SMT line" || nz["smt"] != "SMT 線" || nz["shipped"] != "出貨" {
		t.Errorf("names %q %q %q", ne["smt"], nz["smt"], nz["shipped"])
	}
	if stripNames(t, en) != stripNames(t, zh) {
		t.Error("the zh-Hant template differs from the en one in more than its names")
	}
	if issues := pack.Validate(zh); len(issues) != 0 {
		t.Errorf("zh-Hant: domain issues %v", issues)
	}
	if _, err := electronicsline.Template(electronicsline.PhotoStartTemplate, newModel, "x", "fr"); !errors.Is(err, electronicsline.ErrUnknownLocale) {
		t.Errorf("an unknown locale: %v", err)
	}
	_, err := electronicsline.Template(electronicsline.PhotoStartTemplate, newModel, "", domainpack.English)
	var invalid *electronicsline.InvalidError
	if !errors.As(err, &invalid) || !slices.ContainsFunc(invalid.Issues, func(i model.ValidationIssue) bool { return i.Path == "/name" }) {
		t.Errorf("an empty name: %v", err)
	}
	if _, err := electronicsline.Template("photo_start ", newModel, "x", domainpack.English); !errors.Is(err, electronicsline.ErrUnknownTemplate) {
		t.Errorf("a padded name: %v", err)
	}
}

// The fake vision model's answer for the demo floor plan (testdata/photos,
// written for the bare line) applies to a model made from the template as
// an AI edit, and gives a valid line the pack's rules accept: the shape the
// live runs gave (H-054 §11), AOI, one ICT step with two testers, FCT and
// packing, after SMT. The server's photo rules on that base are
// internal/ai/review's tests on the same IR.
func TestPhotoStartTakesTheDemoPhotoAnswer(t *testing.T) {
	ir := photoStart(t, "Line from a photo", domainpack.English)
	pool := map[string][]string{}
	for _, kind := range []string{"node", "flow", "resource"} {
		for i := range 12 {
			pool[kind] = append(pool[kind], fmt.Sprintf("%s_photo_%02d", kind, i))
		}
	}
	var answer struct {
		Changes jsontext.Value `json:"changes"`
	}
	if err := json.Unmarshal(contracttest.PhotoAnswer(t, "electronics-line-demo", pool), &answer); err != nil {
		t.Fatal(err)
	}
	base, err := model.Encode(ir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := patch.Apply(base, answer.Changes, patch.AIEdit)
	if err != nil || len(res.PatchIssues) != 0 || !res.Valid() {
		t.Fatalf("the demo answer: %v %v %v", err, res.PatchIssues, res.Validation.Issues)
	}
	candidate := res.Validation.IR
	if issues := pack.Validate(candidate); slices.ContainsFunc(issues, func(i model.ValidationIssue) bool { return i.Severity == model.SeverityError }) {
		t.Errorf("the pack's rules on the candidate: %v", issues)
	}
	stations := map[string]int{}
	for _, n := range candidate.Nodes {
		if n.Type != model.NodeProcess {
			continue
		}
		var a struct {
			Station string `json:"station"`
		}
		if err := json.Unmarshal(n.DomainAttributes, &a); err != nil {
			t.Fatal(err)
		}
		r := candidate.Resources[slices.IndexFunc(candidate.Resources, func(r model.Resource) bool { return r.ID == n.Requires[0].ResourceID })]
		stations[a.Station] = r.Capacity
	}
	if want := map[string]int{"smt": 1, "aoi": 1, "ict": 2, "fct": 1, "pack": 1}; fmt.Sprint(stations) != fmt.Sprint(want) {
		t.Errorf("stations and capacities %v, want %v", stations, want)
	}
}

// The template runs through the engine with the pack's metrics: a
// schema-valid Result, the same digest for the same seed, locked in
// testdata/photo-start-run-lock.json (the engine's own output, not an
// independent oracle, SIMULATION_SPEC §9).
func TestPhotoStartThroughTheEngine(t *testing.T) {
	var lock runLock
	if err := json.Unmarshal(contractFile(t, "testdata/photo-start-run-lock.json"), &lock); err != nil {
		t.Fatal(err)
	}
	in := runInput(t, photoStart(t, "Line from a photo", domainpack.English), lock.HorizonDays, lock.WarmupDays, lock.Replications, lock.Seed)
	res, err := engine().Run(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	withRun := *res
	withRun.RunID = "sim_01M4YDQK00FGZZWH0PP15XPNFA"
	doc, err := json.Marshal(withRun)
	if err != nil {
		t.Fatal(err)
	}
	if issues, err := validate.Document(doc, "simulation.schema.json#/$defs/Result", validate.IRSchemaInvalid); err != nil || len(issues) > 0 {
		t.Fatalf("the Result is not schema-valid: %v %v", err, issues)
	}
	if m := res.Metrics.Throughput; m.Status != simulation.Available || m.Mean == nil || *m.Mean <= 0 {
		t.Errorf("throughput %+v", m)
	}
	if again, err := engine().Run(context.Background(), in, nil); err != nil || again.Reproducibility.ResultDigest != res.Reproducibility.ResultDigest {
		t.Errorf("a second run: %v", err)
	}
	if res.EngineVersion != lock.EngineVersion || res.Reproducibility.ResultDigest != lock.ResultDigest {
		t.Errorf("the run's digest is %s (%s); the lock has %s (%s). A change to the engine, the template or the pack's metrics must be reviewed before the lock is updated",
			res.Reproducibility.ResultDigest, res.EngineVersion, lock.ResultDigest, lock.EngineVersion)
	}
}
