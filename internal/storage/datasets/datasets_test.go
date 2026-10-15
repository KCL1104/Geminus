package datasets_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/datasets"
)

const (
	org      = "org_01M4YDQK00Y5H2929M9WWZWTVH"
	otherOrg = "org_01M4YDQK00ZZZZZZZZZZZZZZZZ"
	dsv      = "dsv_01M4YDQK00NMKD3J34KSJZVY40"
	ref      = "dataset:review_duration"
)

// store is a test double keyed by (organization, dataset version), the way a
// tenant-scoped repository answers. ignoreTenant simulates a broken Store.
type store struct {
	objects      map[[2]string][]byte
	ignoreTenant bool
	err          error
	mu           sync.Mutex
	reads        int
}

func (s *store) Snapshot(ctx context.Context, organizationID, datasetVersionID string) ([]byte, error) {
	s.mu.Lock()
	s.reads++
	s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	for k, v := range s.objects {
		if k[1] == datasetVersionID && (k[0] == organizationID || s.ignoreTenant) {
			return v, nil
		}
	}
	return nil, datasets.ErrNotFound
}

func root(t *testing.T, rel string) []byte {
	t.Helper()
	return contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), rel))
}

// reportIR is the report example: node 1 ("review") samples dataset:review_duration.
func reportIR(t *testing.T) *model.ModelIR {
	t.Helper()
	r, err := validate.IR(root(t, "testdata/golden-models/report-example.json"))
	if err != nil || !r.Valid() {
		t.Fatalf("report example: %v %v", err, r.Issues)
	}
	return r.IR
}

func snapshotBytes(t *testing.T, edit func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(root(t, "testdata/contracts/valid-dataset-snapshot.json"), &m); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(m)
	}
	out, err := json.Marshal(m, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func bindingFor(raw []byte) model.DatasetBinding {
	return model.DatasetBinding{LogicalRef: ref, DatasetVersionID: dsv, ContentHash: hashing.ContentHash(raw), Unit: model.Minute, SampleCount: 3}
}

func resolve(t *testing.T, s datasets.Store, ir *model.ModelIR, manifest ...model.DatasetBinding) datasets.Resolution {
	t.Helper()
	r, err := datasets.Resolve(context.Background(), s, org, ir, manifest)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return r
}

func single(raw []byte) *store {
	return &store{objects: map[[2]string][]byte{{org, dsv}: raw}}
}

// TEST-019 happy path: the fixture snapshot, stored bytes and binding agree.
func TestResolve(t *testing.T) {
	raw := root(t, "testdata/contracts/valid-dataset-snapshot.json") // the exact stored bytes
	r := resolve(t, single(raw), reportIR(t), bindingFor(raw))
	if !r.Valid() || len(r.Issues) != 0 {
		t.Fatalf("issues: %v", r.Issues)
	}
	got := r.Datasets[ref]
	if !slices.Equal(got.Samples, []float64{120, 240, 360}) || got.Binding != bindingFor(raw) {
		t.Errorf("resolved %+v", got)
	}
	want, _ := hashing.Manifest([]model.DatasetBinding{bindingFor(raw)})
	if r.ManifestHash != want || len(r.Manifest) != 1 {
		t.Errorf("manifest %v hash %s, want %s", r.Manifest, r.ManifestHash, want)
	}
	if got := hashing.ContentHash(raw); got != `sha256:ae1b933ab08289e9fdccc56a26602bfb27297dfbbb56c686a0382ce8b7da22fd` {
		t.Errorf("fixture content hash %s no longer matches testdata/hashing/expected-hashes.json", got)
	}
}

func onlyIssue(t *testing.T, r datasets.Resolution, code, path string) {
	t.Helper()
	if r.Valid() || len(r.Issues) != 1 || r.Issues[0].Code != code || r.Issues[0].Path != path {
		t.Errorf("issues %v, want exactly %s at %s", r.Issues, code, path)
	}
	if r.Datasets != nil || r.Manifest != nil || r.ManifestHash != "" {
		t.Errorf("an invalid resolution must not carry datasets: %+v", r)
	}
}

type rejectCase struct {
	store    *store
	manifest []model.DatasetBinding
	code     string
	path     string
	noReads  bool // a manifest-shape failure stops before any object is read (D-023)
}

// TEST-019 negatives: each broken link is reported at its exact path.
func TestResolveRejects(t *testing.T) {
	good := root(t, "testdata/contracts/valid-dataset-snapshot.json")
	notJSON := []byte(`{"samples":`)
	B := validate.DatasetBindingInvalid
	one := func(b model.DatasetBinding) []model.DatasetBinding { return []model.DatasetBinding{b} }
	cases := map[string]rejectCase{
		"no binding for the model's dataset": {single(good), nil, B, "/ir/nodes/1/service_time/distribution/samples_ref", true},
		"version missing":                    {&store{}, one(bindingFor(good)), B, "/dataset_bindings/0/dataset_version_id", false},
		"version of another tenant": {&store{objects: map[[2]string][]byte{{otherOrg, dsv}: good}},
			one(bindingFor(good)), B, "/dataset_bindings/0/dataset_version_id", false},
		"stored bytes changed":                     {single(append(slices.Clone(good), ' ')), one(bindingFor(good)), B, "/dataset_bindings/0/content_hash", false},
		"bytes are not parsed when the hash fails": {single(notJSON), one(bindingFor(good)), B, "/dataset_bindings/0/content_hash", false},
		"stored object is not JSON":                {single(notJSON), one(bindingFor(notJSON)), B, "/dataset_bindings/0", false},
		"logical_ref bound twice":                  {single(good), []model.DatasetBinding{bindingFor(good), bindingFor(good)}, B, "/dataset_bindings/1/logical_ref", true},
	}
	// Schema failures of the binding itself, each next to a valid binding.
	for name, e := range map[string]struct {
		edit   func(b *model.DatasetBinding)
		member string
	}{
		"logical_ref outside the D-034 charset":    {func(b *model.DatasetBinding) { b.LogicalRef = "dataset:Review Duration" }, "logical_ref"},
		"logical_ref without prefix":               {func(b *model.DatasetBinding) { b.LogicalRef = "review_duration" }, "logical_ref"},
		"dataset version outside Crockford base32": {func(b *model.DatasetBinding) { b.DatasetVersionID = "dsv_01M4YDQK00NMKD3J34KSJZVYIL" }, "dataset_version_id"},
		"content hash in upper case hex":           {func(b *model.DatasetBinding) { b.ContentHash = "sha256:" + strings.Repeat("A", 64) }, "content_hash"},
		"unknown unit":                             {func(b *model.DatasetBinding) { b.Unit = "week" }, "unit"},
		"sample count zero":                        {func(b *model.DatasetBinding) { b.SampleCount = 0 }, "sample_count"},
	} {
		b := bindingFor(good)
		b.LogicalRef = "dataset:z_extra"
		e.edit(&b)
		cases["schema: "+name] = rejectCase{single(good), []model.DatasetBinding{bindingFor(good), b}, B, "/dataset_bindings/1/" + e.member, true}
	}
	for name, e := range map[string]struct {
		edit       func(m map[string]any)
		code, path string
	}{
		"snapshot carries its own hash (pre-D-019 shape)": {func(m map[string]any) { m["content_hash"] = "sha256:" + strings.Repeat("0", 64) }, B, "/dataset_bindings/0"},
		"snapshot of another dataset version":             {func(m map[string]any) { m["dataset_version_id"] = "dsv_01M4YDQK00ZZZZZZZZZZZZZZZZ" }, B, "/dataset_bindings/0/dataset_version_id"},
		"snapshot of another logical ref":                 {func(m map[string]any) { m["logical_ref"] = "dataset:other" }, B, "/dataset_bindings/0/logical_ref"},
		"snapshot in another unit":                        {func(m map[string]any) { m["unit"] = "hour" }, B, "/dataset_bindings/0/unit"},
		"sample count differs":                            {func(m map[string]any) { m["samples"] = []any{120.0, 240.0} }, B, "/dataset_bindings/0/sample_count"},
		"sample above 365 days":                           {func(m map[string]any) { m["samples"] = []any{120.0, 240.0, 525601.0} }, validate.LimitExceeded, "/dataset_bindings/0"},
	} {
		raw := snapshotBytes(t, e.edit)
		cases[name] = rejectCase{single(raw), one(bindingFor(raw)), e.code, e.path, false} // bindingFor still says 3 minute samples
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			onlyIssue(t, resolve(t, c.store, reportIR(t), c.manifest...), c.code, c.path)
			if c.noReads && c.store.reads != 0 {
				t.Errorf("read %d objects; a shape failure must stop before the snapshot stage", c.store.reads)
			}
		})
	}
}

// A Store that ignores the tenant must still not hand another organization's
// snapshot to a run; the answer is the same as for a missing version.
func TestTenantDefenceInDepth(t *testing.T) {
	foreign := snapshotBytes(t, func(m map[string]any) { m["organization_id"] = otherOrg })
	s := &store{objects: map[[2]string][]byte{{otherOrg, dsv}: foreign}, ignoreTenant: true}
	r := resolve(t, s, reportIR(t), bindingFor(foreign))
	onlyIssue(t, r, validate.DatasetBindingInvalid, "/dataset_bindings/0/dataset_version_id")
	missing := resolve(t, &store{}, reportIR(t), bindingFor(foreign))
	if r.Issues[0].Message != missing.Issues[0].Message {
		t.Errorf("a foreign snapshot is distinguishable from a missing one: %q vs %q", r.Issues[0].Message, missing.Issues[0].Message)
	}
}

// D-033 at binding time: a 0 sample makes the empirical stage zero-time.
func TestZeroSample(t *testing.T) {
	zero := snapshotBytes(t, func(m map[string]any) { m["samples"] = []any{0.0, 240.0, 360.0} })
	if r := resolve(t, single(zero), reportIR(t), bindingFor(zero)); !r.Valid() {
		t.Errorf("a zero-time stage outside any cycle is allowed: %v", r.Issues)
	}
	var loop map[string]any
	if err := json.Unmarshal(root(t, "testdata/golden-models/report-example.json"), &loop); err != nil {
		t.Fatal(err)
	}
	// Route review back to itself through a decision so the cycle is review -> d -> review.
	nodes := loop["nodes"].([]any)
	flows := loop["flows"].([]any)
	review := nodes[1].(map[string]any)["id"].(string)
	var out []any
	for _, f := range flows {
		fm := f.(map[string]any)
		if fm["from"] == review {
			next := fm["to"]
			fm["to"] = "rework_check"
			out = append(out, fm,
				map[string]any{"id": "rework_back", "from": "rework_check", "to": review, "routing": map[string]any{"type": "probability", "probability": 0.25}},
				map[string]any{"id": "rework_on", "from": "rework_check", "to": next, "routing": map[string]any{"type": "probability", "probability": 0.75}})
			continue
		}
		out = append(out, f)
	}
	loop["nodes"] = append(nodes, map[string]any{"id": "rework_check", "type": "decision"})
	loop["flows"] = out
	data, _ := json.Marshal(loop)
	v, err := validate.IR(data)
	if err != nil || !v.Valid() {
		t.Fatalf("looped report example: %v %v", err, v.Issues)
	}
	onlyIssue(t, resolve(t, single(zero), v.IR, bindingFor(zero)), validate.ZeroTimeCycle, "/ir/nodes/1")
	good := root(t, "testdata/contracts/valid-dataset-snapshot.json")
	if r := resolve(t, single(good), v.IR, bindingFor(good)); !r.Valid() {
		t.Errorf("positive samples in a loop: %v", r.Issues)
	}
}

// A binding the effective model no longer uses is still verified and frozen.
func TestUnusedBindingIsVerified(t *testing.T) {
	good := root(t, "testdata/contracts/valid-dataset-snapshot.json")
	ir := reportIR(t)
	extra := snapshotBytes(t, func(m map[string]any) {
		m["dataset_version_id"] = "dsv_01M4YDQK00C1WZ5JZ1K8M4Q2RG"
		m["logical_ref"] = "dataset:a_old"
	})
	b2 := model.DatasetBinding{LogicalRef: "dataset:a_old", DatasetVersionID: "dsv_01M4YDQK00C1WZ5JZ1K8M4Q2RG",
		ContentHash: hashing.ContentHash(extra), Unit: model.Minute, SampleCount: 3}
	s := &store{objects: map[[2]string][]byte{{org, dsv}: good, {org, b2.DatasetVersionID}: extra}}
	r := resolve(t, s, ir, bindingFor(good), b2)
	if !r.Valid() || len(r.Datasets) != 2 || r.Manifest[0].LogicalRef != "dataset:a_old" {
		t.Fatalf("resolution %+v", r)
	}
	reversed := resolve(t, s, ir, b2, bindingFor(good))
	if reversed.ManifestHash != r.ManifestHash {
		t.Error("manifest hash depends on the caller's order")
	}
	b2.ContentHash = "sha256:" + strings.Repeat("e", 64)
	onlyIssue(t, resolve(t, s, ir, bindingFor(good), b2), validate.DatasetBindingInvalid, "/dataset_bindings/1/content_hash")
}

// TEST-014: new data for the same logical_ref is a new dataset version; a
// manifest frozen on the old version keeps resolving to the old samples.
func TestLateDataDoesNotChangeFrozenBinding(t *testing.T) {
	v1 := root(t, "testdata/contracts/valid-dataset-snapshot.json")
	const dsv2 = "dsv_01M4YDQK00C1WZ5JZ1K8M4Q2RG"
	v2 := snapshotBytes(t, func(m map[string]any) {
		m["dataset_version_id"] = dsv2
		m["samples"] = []any{1.0, 2.0, 3.0}
	})
	s := single(v1)
	frozen := bindingFor(v1)
	before := resolve(t, s, reportIR(t), frozen)
	s.objects[[2]string{org, dsv2}] = v2 // the import pipeline commits a newer version
	after := resolve(t, s, reportIR(t), frozen)
	if !after.Valid() || !slices.Equal(after.Datasets[ref].Samples, []float64{120, 240, 360}) || after.ManifestHash != before.ManifestHash {
		t.Errorf("frozen binding changed: before %+v after %+v", before, after)
	}
	rebound := bindingFor(v2)
	rebound.DatasetVersionID = dsv2
	next := resolve(t, s, reportIR(t), rebound)
	if !next.Valid() || next.ManifestHash == before.ManifestHash || next.Datasets[ref].Samples[0] != 1 {
		t.Errorf("only an explicit new binding picks up the new version: %+v", next)
	}
}

// Infrastructure failures and cancellation are errors, never issues or success.
func TestResolveErrors(t *testing.T) {
	good := root(t, "testdata/contracts/valid-dataset-snapshot.json")
	boom := errors.New("object storage unavailable")
	_, err := datasets.Resolve(context.Background(), &store{err: boom}, org, reportIR(t), []model.DatasetBinding{bindingFor(good)})
	if !errors.Is(err, boom) {
		t.Errorf("store failure: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := single(good)
	if _, err := datasets.Resolve(ctx, s, org, reportIR(t), []model.DatasetBinding{bindingFor(good)}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	if s.reads != 0 {
		t.Errorf("a cancelled resolve read %d objects", s.reads)
	}
	// Cancellation wins even when there is nothing to read.
	minimal, err := validate.IR(root(t, "testdata/contracts/valid-minimal-ir.json"))
	if err != nil || !minimal.Valid() {
		t.Fatal(err, minimal.Issues)
	}
	if _, err := datasets.Resolve(ctx, s, org, minimal.IR, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled with an empty manifest: %v", err)
	}
	if r := resolve(t, s, minimal.IR); !r.Valid() || r.ManifestHash != hashing.Sum([]byte("[]")) || r.Manifest == nil {
		t.Errorf("empty manifest: %+v", r)
	}

	// A snapshot over the size cap is refused even though it is valid JSON
	// whose hash matches (trailing whitespace is legal JSON).
	big := append(slices.Clone(good), bytes.Repeat([]byte{' '}, datasets.MaxSnapshotBytes)...)
	onlyIssue(t, resolve(t, single(big), reportIR(t), bindingFor(big)), validate.DatasetBindingInvalid, "/dataset_bindings/0")
	atCap := append(slices.Clone(good), bytes.Repeat([]byte{' '}, datasets.MaxSnapshotBytes-len(good))...)
	if r := resolve(t, single(atCap), reportIR(t), bindingFor(atCap)); !r.Valid() {
		t.Errorf("a snapshot of exactly MaxSnapshotBytes: %v", r.Issues)
	}
}

func TestConcurrentResolve(t *testing.T) {
	good := root(t, "testdata/contracts/valid-dataset-snapshot.json")
	ir := reportIR(t)
	s := single(good)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			r, err := datasets.Resolve(context.Background(), s, org, ir, []model.DatasetBinding{bindingFor(good)})
			if err != nil || !r.Valid() {
				t.Errorf("%v %v", err, r.Issues)
			}
		})
	}
	wg.Wait()
}

// The binding stage does no I/O of its own: no DB driver, network or exec.
func TestDependencies(t *testing.T) {
	contracttest.OnlyDeps(t,
		"github.com/kcl1104/geminus/internal/model",
		"github.com/kcl1104/geminus/internal/model/hashing",
		"github.com/kcl1104/geminus/internal/model/validate",
		"github.com/kcl1104/geminus/internal/simulation",
		"github.com/kcl1104/geminus/schemas",
		"github.com/gowebpki/jcs",
		"github.com/santhosh-tekuri/jsonschema/v6",
		"github.com/santhosh-tekuri/jsonschema/v6/kind",
		"golang.org/x/text/feature/plural",
		"golang.org/x/text/internal",
		"golang.org/x/text/internal/catmsg",
		"golang.org/x/text/internal/format",
		"golang.org/x/text/internal/language",
		"golang.org/x/text/internal/language/compact",
		"golang.org/x/text/internal/number",
		"golang.org/x/text/internal/stringset",
		"golang.org/x/text/internal/tag",
		"golang.org/x/text/language",
		"golang.org/x/text/message",
		"golang.org/x/text/message/catalog",
	)
}

// FuzzStoredSnapshot: whatever bytes sit in storage under a matching hash,
// Resolve never panics, never returns an error for data problems, and a
// valid result carries exactly the bound samples.
func FuzzStoredSnapshot(f *testing.F) {
	f.Add(contracttest.ReadFile(f, filepath.Join(contracttest.Root(f), "testdata/contracts/valid-dataset-snapshot.json")))
	f.Add([]byte(`{"samples":[0]}`))
	f.Add([]byte(`[]`))
	r, err := validate.IR(contracttest.ReadFile(f, filepath.Join(contracttest.Root(f), "testdata/golden-models/report-example.json")))
	if err != nil || !r.Valid() {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		b := bindingFor(raw)
		res, err := datasets.Resolve(context.Background(), single(raw), org, r.IR, []model.DatasetBinding{b})
		if err != nil {
			t.Fatalf("data problem returned as error: %v", err)
		}
		if res.Valid() && len(res.Datasets[ref].Samples) != b.SampleCount {
			t.Fatalf("valid resolution with %d samples, binding says %d", len(res.Datasets[ref].Samples), b.SampleCount)
		}
		if !res.Valid() && res.Datasets != nil {
			t.Fatal("invalid resolution carries datasets")
		}
	})
}

// BenchmarkResolveMaxSnapshot: one binding at the schema maximum of 100,000
// samples. A data point only, not a performance gate (T-037).
func BenchmarkResolveMaxSnapshot(b *testing.B) {
	samples := make([]float64, 100_000)
	for i := range samples {
		samples[i] = float64(i%997) + 0.25
	}
	raw, err := json.Marshal(datasets.Snapshot{DatasetVersionID: dsv, OrganizationID: org, LogicalRef: ref, Unit: model.Minute, Samples: samples})
	if err != nil {
		b.Fatal(err)
	}
	r, err := validate.IR(contracttest.ReadFile(b, filepath.Join(contracttest.Root(b), "testdata/golden-models/report-example.json")))
	if err != nil || !r.Valid() {
		b.Fatal(err)
	}
	bind := model.DatasetBinding{LogicalRef: ref, DatasetVersionID: dsv, ContentHash: hashing.ContentHash(raw), Unit: model.Minute, SampleCount: len(samples)}
	s := single(raw)
	b.SetBytes(int64(len(raw)))
	for b.Loop() {
		res, err := datasets.Resolve(context.Background(), s, org, r.IR, []model.DatasetBinding{bind})
		if err != nil || !res.Valid() {
			b.Fatal(err, res.Issues)
		}
	}
}
