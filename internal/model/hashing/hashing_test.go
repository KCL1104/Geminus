package hashing_test

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gowebpki/jcs"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/simulation"
)

var hashFormat = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func read(t testing.TB, rel string) []byte {
	t.Helper()
	return contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), rel))
}

func decode[T any](t testing.TB, data []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(data, &v, json.RejectUnknownMembers(true)); err != nil {
		t.Fatal(err)
	}
	return v
}

type vectors struct {
	Source       string `json:"source"`
	Canonicalize []struct {
		Name        string `json:"name"`
		Input       string `json:"input"`
		ExpectedHex string `json:"expected_hex"`
	} `json:"canonicalize"`
	PropertyOrder []struct {
		Name          string   `json:"name"`
		Input         string   `json:"input"`
		ExpectedOrder []string `json:"expected_value_order"`
	} `json:"property_order"`
	Numbers []struct {
		IEEE754 string `json:"ieee754"`
		JSON    string `json:"json"`
		Error   string `json:"error"`
		Comment string `json:"comment"`
	} `json:"numbers"`
}

// TEST-014: the RFC 8785 examples, byte for byte.
func TestRFC8785Examples(t *testing.T) {
	v := decode[vectors](t, read(t, "testdata/hashing/rfc8785-vectors.json"))
	if len(v.Canonicalize) == 0 || len(v.PropertyOrder) == 0 || len(v.Numbers) < 20 {
		t.Fatal("RFC vector file is incomplete")
	}
	for _, c := range v.Canonicalize {
		want, err := hex.DecodeString(c.ExpectedHex)
		if err != nil {
			t.Fatal(err)
		}
		got, err := hashing.Canonical([]byte(c.Input))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s:\n got %s (%v)\nwant %s", c.Name, got, err, want)
		}
	}
	for _, c := range v.PropertyOrder {
		got, err := hashing.Canonical([]byte(c.Input))
		if err != nil {
			t.Fatal(err)
		}
		if order := memberValues(t, got); !slices.Equal(order, c.ExpectedOrder) {
			t.Errorf("%s: value order %q, want %q", c.Name, order, c.ExpectedOrder)
		}
	}
	for _, n := range v.Numbers {
		f := float(t, n.IEEE754)
		if n.Error != "" {
			if s, err := jcs.NumberToJSON(f); err == nil {
				t.Errorf("%s (%s): serialized as %s, want an error", n.IEEE754, n.Comment, s)
			}
			continue
		}
		checkNumber(t, n.IEEE754, f, n.JSON)
	}
}

// memberValues returns the string values of a flat object in document order.
func memberValues(t *testing.T, obj []byte) []string {
	t.Helper()
	var members []jsontext.Token
	dec := jsontext.NewDecoder(bytes.NewReader(obj))
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			break
		}
		members = append(members, tok.Clone()) // tokens are only valid until the next read
	}
	if len(members) < 2 || members[0].Kind() != '{' {
		t.Fatalf("not an object: %s", obj)
	}
	var out []string
	for i := 2; i < len(members)-1; i += 2 { // '{', name, value, name, value, ..., '}'
		out = append(out, members[i].String())
	}
	return out
}

func float(t testing.TB, bits string) float64 {
	t.Helper()
	u, err := strconv.ParseUint(bits, 16, 64)
	if err != nil {
		t.Fatal(err)
	}
	return math.Float64frombits(u)
}

// checkNumber feeds f through Canonical as an exponent literal (17 significant
// digits round-trip exactly and never look like an integer literal).
func checkNumber(t *testing.T, bits string, f float64, want string) {
	t.Helper()
	in := "[" + strconv.FormatFloat(f, 'e', 16, 64) + "]"
	got, err := hashing.Canonical([]byte(in))
	if err != nil || string(got) != "["+want+"]" {
		t.Errorf("%s: %s canonicalized to %s (%v), want [%s]", bits, in, got, err, want)
	}
}

// TEST-014: number serialization agrees with V8's JSON.stringify, the live
// reference RFC 8785 §3.2.2.3 names (scripts/gen_number_vectors.mjs). Set
// HASHING_V8_VECTORS to a larger generated file for an extended run.
func TestNumbersMatchV8(t *testing.T) {
	data := read(t, "testdata/hashing/v8-number-vectors.txt")
	if p := os.Getenv("HASHING_V8_VECTORS"); p != "" {
		data = contracttest.ReadFile(t, p)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	var n int
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		bits, want, ok := strings.Cut(line, ",")
		if !ok {
			t.Fatalf("bad vector line %q", line)
		}
		checkNumber(t, bits, float(t, bits), want)
		n++
	}
	if n < 5000 {
		t.Fatalf("only %d V8 vectors", n)
	}
	t.Logf("%d V8 vectors match", n)
}

// TEST-014: random documents canonicalize byte for byte like Python rfc8785,
// including member names where UTF-16 order differs from code point order
// (scripts/check_hashes.py). Set HASHING_PY_DOCUMENTS for an extended corpus.
func TestDocumentsMatchPython(t *testing.T) {
	data := read(t, "testdata/hashing/rfc8785-documents.jsonl")
	if p := os.Getenv("HASHING_PY_DOCUMENTS"); p != "" {
		data = contracttest.ReadFile(t, p)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, 1<<24)
	var n int
	for sc.Scan() {
		var doc struct {
			Input        string `json:"input"`
			CanonicalHex string `json:"canonical_hex"`
		}
		if err := json.Unmarshal(sc.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		want, err := hex.DecodeString(doc.CanonicalHex)
		if err != nil {
			t.Fatal(err)
		}
		got, err := hashing.Canonical([]byte(doc.Input))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("document %d %q:\n got %q (%v)\nwant %q", n, doc.Input, got, err, want)
		}
		n++
	}
	if err := sc.Err(); err != nil || n < 400 {
		t.Fatalf("read %d documents: %v", n, err)
	}
	t.Logf("%d documents match", n)
}

// TEST-014 negatives: what Canonical refuses instead of guessing.
func TestCanonicalRejects(t *testing.T) {
	for name, in := range map[string]string{
		"empty":                     "",
		"whitespace only":           " \n",
		"two values":                "{} {}",
		"trailing garbage":          `{"a":1}x`,
		"duplicate member":          `{"a":1,"a":2}`,
		"duplicate after unescape":  `{"a":1,"\u0061":2}`,
		"nested duplicate":          `{"x":[{"k":1,"k":1}]}`,
		"invalid UTF-8":             "\"\xff\"",
		"lone high surrogate":       `"\ud800"`,
		"lone low surrogate":        `"\udead"`,
		"overflow to infinity":      `[1e400]`,
		"negative overflow":         `[-1e400]`,
		"NaN literal":               `NaN`,
		"single quotes":             `{'a':1}`,
		"leading zero":              `[01]`,
		"plus sign":                 `[+1]`,
		"hex number":                `[0x10]`,
		"unterminated":              `{"a":`,
		"control character literal": "\"a\x01\"",
	} {
		if out, err := hashing.Canonical([]byte(in)); err == nil || !errors.Is(err, hashing.ErrInvalid) {
			t.Errorf("%s: got %q, %v; want ErrInvalid", name, out, err)
		}
	}
	for in, kind := range map[string]string{
		`{"a":1,"a":2}`:      "duplicate member name at byte 7",
		`{"a":1,"\u0061":2}`: "duplicate member name at byte 7",
		`{} {}`:              "data after the first JSON value",
		``:                   "no JSON value",
		"[\"\xff\"]":         "invalid JSON at byte 2",
		`[1e400]`:            "a number or string has no RFC 8785 form",
	} {
		if _, err := hashing.Canonical([]byte(in)); err == nil || !strings.HasSuffix(err.Error(), kind) {
			t.Errorf("%q: error %v, want it to end with %q", in, err, kind)
		}
	}
	for name, c := range map[string]struct{ in, want string }{
		"max safe integer": {`9007199254740991`, `9007199254740991`},
		// Beyond binary64 precision, distinct literals share a canonical form
		// (RFC 8785 numbers are doubles); schemas cap exact-integer members.
		"2^53+1 is the double 2^53":   {`9007199254740993`, `9007199254740992`},
		"long integer":                {`123456789012345678901234567890`, `1.2345678901234568e+29`},
		"exponent integer round trip": {`1e17`, `100000000000000000`},
		"its output is accepted":      {`100000000000000000`, `100000000000000000`},
		"min safe integer":            {`-9007199254740991`, `-9007199254740991`},
		"large exponent ok":           {`1E30`, `1e+30`},
		"minus zero":                  {`-0`, `0`},
		"minus zero float":            {`-0.0`, `0`},
		"scalar string":               {` "x" `, `"x"`},
		"nested sort":                 {`{"b":[{"d":1,"c":2}],"a":null}`, `{"a":null,"b":[{"c":2,"d":1}]}`},
		"escapes":                     {`"\u00e9\/\t"`, `"é/\t"`},
	} {
		if got, err := hashing.Canonical([]byte(c.in)); err != nil || string(got) != c.want {
			t.Errorf("%s: %q -> %q (%v), want %q", name, c.in, got, err, c.want)
		}
	}
}

// Errors may name a location, never a value: hashed documents hold customer data.
func TestErrorsDoNotQuoteInput(t *testing.T) {
	for _, in := range []string{
		`{"k":"TOPSECRET","k":"TOPSECRET"}`,
		`{"metadata":{"TOPSECRET":1,"TOPSECRET":2}}`,
		`{"metadata":{"TOPSECRET":[1e400]}}`,
		`{"metadata":{"TOPSECRET":[-1e400]}}`,
		`{"k":"TOPSECRET" TOPSECRET}`,
		`{"TOPSECRET":"x" "y"}`,
		`{"k":"TOPSECRET","s":"\ud800TOPSECRET"}`,
		"{\"TOPSECRET\":\"\xff\"}",
	} {
		_, err := hashing.Canonical([]byte(in))
		if err == nil {
			t.Fatalf("%s: no error", in)
		}
		if msg := err.Error(); strings.Contains(msg, "TOPSECRET") || strings.Contains(msg, "1e400") {
			t.Errorf("error quotes the input: %s", msg)
		}
	}
}

type expectedHashes struct {
	Comment            string            `json:"comment"`
	ModelHash          map[string]string `json:"model_hash"`
	PatchHash          map[string]string `json:"patch_hash"`
	EffectiveModelHash map[string]string `json:"effective_model_hash"`
	ConfigHash         map[string]string `json:"config_hash"`
	DatasetManifest    map[string]string `json:"dataset_manifest_hash"`
	ResultDigest       map[string]string `json:"result_digest"`
	ContentHash        map[string]string `json:"content_hash"`
}

// TEST-014 / TEST-019: every hash family agrees with the independent Python
// rfc8785 implementation (scripts/check_hashes.py writes the expected file and
// `make spec-hashes` re-checks it). Unknown members fail the decode, so a new
// family cannot be added to the file without a Go check.
func TestAgreesWithIndependentImplementation(t *testing.T) {
	want := decode[expectedHashes](t, read(t, "testdata/hashing/expected-hashes.json"))
	check := func(family, key, got string, err error) {
		t.Helper()
		w := map[string]map[string]string{
			"model": want.ModelHash, "patch": want.PatchHash, "effective": want.EffectiveModelHash,
			"config": want.ConfigHash, "manifest": want.DatasetManifest, "result": want.ResultDigest, "content": want.ContentHash,
		}[family][key]
		if err != nil || got != w || !hashFormat.MatchString(got) {
			t.Errorf("%s[%s]: Go %s (%v), Python %s", family, key, got, err, w)
		}
	}
	for path := range want.ModelHash {
		got, err := hashing.Model(read(t, path))
		check("model", path, got, err)
	}
	for path := range want.PatchHash {
		data := []byte("[]")
		if path != "[]" {
			data = read(t, path)
		}
		got, err := hashing.Patch(data)
		check("patch", path, got, err)
	}

	version := decode[map[string]jsontext.Value](t, read(t, "testdata/contracts/valid-model-version.json"))
	scenario := decode[map[string]jsontext.Value](t, read(t, "testdata/contracts/valid-scenario.json"))
	for key, p := range map[string][]byte{
		"valid-model-version.json ir + []":                        []byte("[]"),
		"valid-model-version.json ir + valid-scenario.json patch": scenario["patch"],
	} {
		res, err := patch.Apply(version["ir"], p, patch.Scenario)
		if err != nil || len(res.PatchIssues) > 0 || !res.Validation.Valid() {
			t.Fatalf("%s: apply: %v %v %v", key, err, res.PatchIssues, res.Validation.Issues)
		}
		got, err := hashing.Model(res.Candidate)
		check("effective", key, got, err)
	}

	configs := decode[configFile](t, read(t, "testdata/hashing/configs.json"))
	for _, c := range configs.Configs {
		got, err := hashing.Config(c.Config)
		check("config", c.Name, got, err)
	}

	manifest := decode[manifestFile](t, read(t, "testdata/hashing/manifest.json"))
	sorted, err := hashing.SortManifest(manifest.Bindings)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, b := range sorted {
		order = append(order, b.LogicalRef)
	}
	if !slices.Equal(order, manifest.ExpectedOrder) {
		t.Errorf("manifest order %v, want %v (D-034 byte order)", order, manifest.ExpectedOrder)
	}
	got, err := hashing.Manifest(manifest.Bindings)
	check("manifest", "testdata/hashing/manifest.json", got, err)
	got, err = hashing.Manifest(nil)
	check("manifest", "[]", got, err)

	for path := range want.ResultDigest {
		got, err := hashing.Result(decode[simulation.Result](t, read(t, path)))
		check("result", path, got, err)
	}
	for path := range want.ContentHash {
		check("content", path, hashing.ContentHash(read(t, path)), nil)
	}
}

type configFile struct {
	Comment string `json:"comment"`
	Configs []struct {
		Name   string               `json:"name"`
		Config simulation.RunConfig `json:"config"`
		SameAs string               `json:"same_as"`
	} `json:"configs"`
}

type manifestFile struct {
	Comment       string                 `json:"comment"`
	ExpectedOrder []string               `json:"expected_order"`
	Bindings      []model.DatasetBinding `json:"bindings"`
}

// Hash members stored in contract fixtures are the hash of their own content.
func TestFixtureHashMembers(t *testing.T) {
	version := decode[map[string]jsontext.Value](t, read(t, "testdata/contracts/valid-model-version.json"))
	if got, err := hashing.Model(version["ir"]); err != nil || `"`+got+`"` != string(version["hash"]) {
		t.Errorf("valid-model-version.json: hash %s, model_hash(ir) %s (%v)", version["hash"], got, err)
	}
	scenario := decode[map[string]jsontext.Value](t, read(t, "testdata/contracts/valid-scenario.json"))
	if got, err := hashing.Patch(scenario["patch"]); err != nil || `"`+got+`"` != string(scenario["patch_hash"]) {
		t.Errorf("valid-scenario.json: patch_hash %s, computed %s (%v)", scenario["patch_hash"], got, err)
	}
	result := decode[simulation.Result](t, read(t, "testdata/contracts/valid-result.json"))
	if got, err := hashing.Result(result); err != nil || got != result.Reproducibility.ResultDigest {
		t.Errorf("valid-result.json: result_digest %s, computed %s (%v)", result.Reproducibility.ResultDigest, got, err)
	}
}

// The baseline patch [] leaves the effective model hash equal to the model
// hash for every golden model (TWIN_IR_SPEC §5).
func TestBaselineEffectiveHashEqualsModelHash(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(contracttest.Root(t), "testdata/golden-models/*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatal("no golden models", err)
	}
	for _, p := range paths {
		base := contracttest.ReadFile(t, p)
		res, err := patch.Apply(base, []byte("[]"), patch.Scenario)
		if err != nil || res.Candidate == nil {
			t.Fatalf("%s: %v %v", p, err, res.PatchIssues)
		}
		m, err1 := hashing.Model(base)
		e, err2 := hashing.Model(res.Candidate)
		if err1 != nil || err2 != nil || m != e {
			t.Errorf("%s: model %s, effective %s (%v %v)", filepath.Base(p), m, e, err1, err2)
		}
	}
}

// Regression (FuzzCanonical): Go re-marshals 1e17 as 100000000000000000, so
// hashing a patched candidate must accept every number spelling it emits.
func TestEffectiveHashOfRemarshaledNumbers(t *testing.T) {
	var ir map[string]any
	if err := json.Unmarshal(read(t, "testdata/contracts/valid-minimal-ir.json"), &ir); err != nil {
		t.Fatal(err)
	}
	ir["metadata"] = map[string]any{"big": 1e17, "huge": 1.5e300, "tiny": 5e-324}
	base, err := json.Marshal(ir)
	if err != nil {
		t.Fatal(err)
	}
	spelled := bytes.Replace(base, []byte("100000000000000000"), []byte("1e17"), 1)
	res, err := patch.Apply(spelled, []byte("[]"), patch.Scenario)
	if err != nil || res.Candidate == nil {
		t.Fatalf("apply: %v %v %v", err, res.PatchIssues, res.Validation.Issues)
	}
	m, err1 := hashing.Model(spelled)
	e, err2 := hashing.Model(res.Candidate)
	if err1 != nil || err2 != nil || m != e {
		t.Errorf("model %s (%v), effective %s (%v)", m, err1, e, err2)
	}
}

func TestModelHashIsContentHash(t *testing.T) {
	a, _ := hashing.Model([]byte(`{"b":[1,2],"a":{"y":1,"x":2.50}}`))
	b, _ := hashing.Model([]byte("{\n \"a\": {\"x\": 2.5, \"y\": 1e0},\n \"b\": [1, 2]\n}"))
	c, _ := hashing.Model([]byte(`{"b":[2,1],"a":{"y":1,"x":2.5}}`))
	if a != b {
		t.Errorf("member order, whitespace and number spelling changed the hash: %s vs %s", a, b)
	}
	if a == c {
		t.Error("array order must change the hash")
	}
}

func TestPatchHash(t *testing.T) {
	empty, err := hashing.Patch([]byte(" [ ] "))
	if err != nil || empty != "sha256:4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945" { // SHA-256("[]")
		t.Errorf("baseline patch hash %s (%v)", empty, err)
	}
	for _, in := range []string{`{}`, `null`, `"[]"`, ``, `[] []`} {
		if _, err := hashing.Patch([]byte(in)); err == nil {
			t.Errorf("Patch(%q) accepted a non-array", in)
		}
	}
}

func binding(ref string) model.DatasetBinding {
	return model.DatasetBinding{LogicalRef: ref, DatasetVersionID: "dsv_01M4YDQK00NMKD3J34KSJZVY40",
		ContentHash: "sha256:" + strings.Repeat("a", 64), Unit: model.Minute, SampleCount: 1}
}

func TestManifest(t *testing.T) {
	in := []model.DatasetBinding{binding("dataset:b"), binding("dataset:a_x"), binding("dataset:a-x")}
	orig := slices.Clone(in)
	want, err := hashing.Manifest(in)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(in, orig) {
		t.Error("Manifest reordered its input")
	}
	for _, perm := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 0, 2}, {1, 2, 0}} {
		p := []model.DatasetBinding{in[perm[0]], in[perm[1]], in[perm[2]]}
		if got, _ := hashing.Manifest(p); got != want {
			t.Errorf("order %v changed the manifest hash", perm)
		}
	}
	if _, err := hashing.Manifest([]model.DatasetBinding{binding("dataset:a"), binding("dataset:b"), binding("dataset:a")}); err == nil {
		t.Error("a logical_ref bound twice must be rejected")
	}
	empty, _ := hashing.Manifest([]model.DatasetBinding{})
	null, _ := hashing.Manifest(nil)
	if empty != null || empty != hashing.Sum([]byte("[]")) {
		t.Errorf("empty manifests: %s %s", empty, null)
	}
	changed := slices.Clone(in)
	changed[0].SampleCount = 2
	if got, _ := hashing.Manifest(changed); got == want {
		t.Error("a binding change must change the manifest hash")
	}
}

func config() simulation.RunConfig {
	return simulation.RunConfig{
		Horizon: simulation.Duration{Value: 5, Unit: model.Day}, Warmup: simulation.Duration{Value: 1, Unit: model.Day},
		Replications: 30, Seed: 42, TraceLevel: simulation.TraceNone,
	}
}

func TestConfigHash(t *testing.T) {
	ref, err := hashing.Config(config())
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*simulation.RunConfig){
		"replications": func(c *simulation.RunConfig) { c.Replications = 31 },
		"seed":         func(c *simulation.RunConfig) { c.Seed = 43 },
		"trace level":  func(c *simulation.RunConfig) { c.TraceLevel = simulation.TraceSampled },
		"horizon":      func(c *simulation.RunConfig) { c.Horizon.Value = 6 },
		"warmup":       func(c *simulation.RunConfig) { c.Warmup = simulation.Duration{Value: 1, Unit: model.Hour} },
	} {
		c := config()
		mutate(&c)
		if got, _ := hashing.Config(c); got == ref {
			t.Errorf("changing %s did not change config_hash", name)
		}
	}
	for name, mutate := range map[string]func(*simulation.RunConfig){
		"negative warmup":  func(c *simulation.RunConfig) { c.Warmup.Value = -1 },
		"NaN horizon":      func(c *simulation.RunConfig) { c.Horizon.Value = math.NaN() },
		"infinite horizon": func(c *simulation.RunConfig) { c.Horizon.Value = math.Inf(1) },
		"unknown unit":     func(c *simulation.RunConfig) { c.Horizon.Unit = "week" },
		"over 365 days":    func(c *simulation.RunConfig) { c.Horizon.Value = 366 },
		"negative seed":    func(c *simulation.RunConfig) { c.Seed = -1 },
		"unsafe seed":      func(c *simulation.RunConfig) { c.Seed = hashing.MaxSafeInteger + 1 },
	} {
		c := config()
		mutate(&c)
		if got, err := hashing.Config(c); !errors.Is(err, hashing.ErrInvalid) {
			t.Errorf("%s: got %s, %v; want ErrInvalid", name, got, err)
		}
	}
}

func TestResultDigest(t *testing.T) {
	base := decode[simulation.Result](t, read(t, "testdata/contracts/valid-result.json"))
	want, err := hashing.Result(base)
	if err != nil {
		t.Fatal(err)
	}
	clone := func() simulation.Result {
		return decode[simulation.Result](t, read(t, "testdata/contracts/valid-result.json"))
	}
	ignored := map[string]func(*simulation.Result){
		"run_id":        func(r *simulation.Result) { r.RunID = "sim_01M4YDQK00HCPPG1TPHJ3YXDQZ" },
		"status":        func(r *simulation.Result) { r.Status = "x" },
		"trace":         func(r *simulation.Result) { r.Trace.Available = !r.Trace.Available; r.Trace.SampledEvents++ },
		"result_digest": func(r *simulation.Result) { r.Reproducibility.ResultDigest = "sha256:" + strings.Repeat("f", 64) },
	}
	for name, mutate := range ignored {
		r := clone()
		mutate(&r)
		if got, _ := hashing.Result(r); got != want {
			t.Errorf("%s must not be part of result_digest", name)
		}
	}
	covered := map[string]func(*simulation.Result){
		"engine_version":     func(r *simulation.Result) { r.EngineVersion += "+1" },
		"metric":             func(r *simulation.Result) { r.Metrics.Throughput.SampleCount++ },
		"replication":        func(r *simulation.Result) { r.ReplicationSummaries[0].CompletedCount++ },
		"measurement window": func(r *simulation.Result) { r.MeasurementWindow.WarmupTicks++ },
		"config hash":        func(r *simulation.Result) { r.Reproducibility.ConfigHash = "sha256:" + strings.Repeat("1", 64) },
		"manifest hash": func(r *simulation.Result) {
			r.Reproducibility.DatasetManifestHash = "sha256:" + strings.Repeat("2", 64)
		},
		"seed": func(r *simulation.Result) { r.Reproducibility.Seed++ },
	}
	for name, mutate := range covered {
		r := clone()
		mutate(&r)
		if got, _ := hashing.Result(r); got == want {
			t.Errorf("changing %s did not change result_digest", name)
		}
	}
	r := clone()
	nan := math.NaN()
	r.Metrics.Throughput.Mean = &nan
	if got, err := hashing.Result(r); !errors.Is(err, hashing.ErrInvalid) {
		t.Errorf("a NaN metric produced %s, %v; unavailable must be null, never NaN", got, err)
	}
}

func TestContentHashIsRawBytes(t *testing.T) {
	a := hashing.ContentHash([]byte(`{"a":1}`))
	b := hashing.ContentHash([]byte(`{"a": 1}`))
	if a == b {
		t.Error("content_hash must cover the stored bytes, not a canonical form (D-019)")
	}
	if a != hashing.Sum([]byte(`{"a":1}`)) || !hashFormat.MatchString(a) {
		t.Errorf("content hash %s", a)
	}
}

func TestConcurrentHashing(t *testing.T) {
	data := read(t, "testdata/golden-models/default-delivery.json")
	want, err := hashing.Model(data)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 20 {
				if got, err := hashing.Model(data); err != nil || got != want {
					t.Errorf("concurrent hash %s (%v)", got, err)
				}
				if _, err := hashing.Config(config()); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
}

// FuzzCanonical: never panics; whatever it accepts canonicalizes to valid JSON
// that is its own canonical form.
func FuzzCanonical(f *testing.F) {
	v := decode[vectors](f, read(f, "testdata/hashing/rfc8785-vectors.json"))
	for _, c := range v.Canonicalize {
		f.Add([]byte(c.Input))
	}
	for _, c := range v.PropertyOrder {
		f.Add([]byte(c.Input))
	}
	f.Add(read(f, "testdata/golden-models/report-example.json"))
	f.Add([]byte(`[9007199254740991,-0,1e-7,"\u0000\ud83d\ude00"]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := hashing.Canonical(data)
		if err != nil {
			if !errors.Is(err, hashing.ErrInvalid) {
				t.Fatalf("error not wrapped: %v", err)
			}
			return
		}
		if !jsontext.Value(out).IsValid() {
			t.Fatalf("invalid output %q", out)
		}
		again, err := hashing.Canonical(out)
		if err != nil || !bytes.Equal(again, out) {
			t.Fatalf("not idempotent: %q -> %q (%v)", out, again, err)
		}
	})
}
