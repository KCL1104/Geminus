package patch

import (
	"bytes"
	"encoding/json/v2"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	return contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "contracts", name))
}

// minimal IR: nodes start(source)/work(process)/end(sink); resource worker;
// flows start_work, work_end.
func base(t testing.TB) []byte { return fixture(t, "valid-minimal-ir.json") }

func mustApply(t *testing.T, doc []byte, patch string, mode Mode) Result {
	t.Helper()
	r, err := Apply(doc, []byte(patch), mode)
	if err != nil {
		t.Fatalf("internal error: %v", err)
	}
	return r
}

func codesAt(r Result) map[string][]string {
	out := map[string][]string{}
	for _, i := range r.PatchIssues {
		out[i.Path] = append(out[i.Path], i.Code)
	}
	return out
}

func wantIssue(t *testing.T, r Result, code, path string) {
	t.Helper()
	if !slices.Contains(codesAt(r)[path], code) {
		t.Fatalf("want %s at %q, got %v", code, path, r.PatchIssues)
	}
	if r.Applied() {
		t.Fatal("a rejected patch must not produce a candidate")
	}
}

// The shared fixtures agree with the Python checks: the scenario patch
// applies to the model it was written for; move/root/escape patches fail at
// their pinned paths.
func TestSharedFixtures(t *testing.T) {
	ir := fixture(t, "valid-model-ir.json")
	for _, name := range []string{"valid-patch.json", "valid-baseline-patch.json"} {
		r := mustApply(t, ir, string(fixture(t, name)), Scenario)
		if !r.Valid() {
			t.Errorf("%s: %v %v", name, r.PatchIssues, r.Validation.Issues)
		}
	}
	for name, path := range map[string]string{"invalid-patch-move.json": "/0/op", "invalid-patch-root.json": "/0/path",
		"invalid-patch-escape.json": "/0/path"} {
		r := mustApply(t, ir, string(fixture(t, name)), ManualDraft)
		if !slices.Contains(codesAt(r)[path], SchemaInvalid) || r.Applied() {
			t.Errorf("%s: want %s at %s, got %v", name, SchemaInvalid, path, r.PatchIssues)
		}
	}
}

// TEST-003: identity members are never patchable, in any mode.
func TestIdentityIsNeverPatchable(t *testing.T) {
	for _, member := range []string{"model_id", "schema_version", "domain", "time_unit"} {
		for _, mode := range []Mode{ManualDraft, AIEdit, Scenario} {
			r := mustApply(t, base(t), `[{"op":"replace","path":"/`+member+`","value":"x"}]`, mode)
			wantIssue(t, r, PathForbidden, "/0/path")
		}
	}
}

func TestAllowlists(t *testing.T) {
	const testWorker = `{"op":"test","path":"/resources/0/id","value":"worker"},`
	const testWork = `{"op":"test","path":"/nodes/1/id","value":"work"},`
	cases := []struct {
		name  string
		mode  Mode
		patch string
		ok    bool // patch applies (candidate validity is checked separately)
	}{
		{"scenario capacity", Scenario, `[` + testWorker + `{"op":"replace","path":"/resources/0/capacity","value":2}]`, true},
		{"scenario distribution member", Scenario, `[` + testWork + `{"op":"replace","path":"/nodes/1/service_time/distribution/value","value":3}]`, true},
		{"scenario routing probability", Scenario, `[{"op":"test","path":"/flows/0/id","value":"start_work"},{"op":"replace","path":"/flows/0/routing/probability","value":1}]`, true},
		{"scenario cannot rename", Scenario, `[` + testWork + `{"op":"replace","path":"/nodes/1/name","value":"x"}]`, false},
		{"scenario cannot add nodes", Scenario, `[{"op":"add","path":"/nodes/-","value":{"id":"x","type":"sink"}}]`, false},
		{"scenario cannot remove flows", Scenario, `[{"op":"test","path":"/flows/0/id","value":"start_work"},{"op":"remove","path":"/flows/0"}]`, false},
		{"scenario cannot touch views", Scenario, `[{"op":"add","path":"/views/2d/positions","value":{}}]`, false},
		{"decoded ~1 is not the capacity token", Scenario, `[` + testWorker + `{"op":"add","path":"/resources/0/capacity~1x","value":2}]`, false},
		{"decoded ~0 is not the capacity token", Scenario, `[` + testWorker + `{"op":"add","path":"/resources/0/cap~0acity","value":2}]`, false},
		{"leading-zero index is not an index", Scenario, `[` + testWorker + `{"op":"replace","path":"/resources/00/capacity","value":2}]`, false},
		{"AI may not edit views", AIEdit, `[{"op":"add","path":"/views/2d/positions","value":{}}]`, false},
		{"AI may not edit metadata", AIEdit, `[{"op":"add","path":"/metadata","value":{}}]`, false},
		{"AI may not drop a kept node's position", AIEdit, `[{"op":"remove","path":"/views/2d/positions/work"}]`, false},
		{"manual may edit views", ManualDraft, `[{"op":"add","path":"/views/2d/positions","value":{"work":{"x":1,"y":2}}}]`, true},
		{"manual may edit metadata", ManualDraft, `[{"op":"add","path":"/metadata","value":{"note":"x"}}]`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := mustApply(t, base(t), c.patch, c.mode)
			if r.Applied() != c.ok {
				t.Fatalf("applied = %v, want %v: %v", r.Applied(), c.ok, r.PatchIssues)
			}
		})
	}
}

// D-022: an AI edit that deletes a node may also delete that node's position.
func TestAIDeletesNodeWithItsPosition(t *testing.T) {
	doc := bytes.Replace(base(t), []byte(`"2d": {}`), []byte(`"2d": {"positions": {"start": {"x": 0, "y": 0},
		"work": {"x": 1, "y": 0}, "end": {"x": 2, "y": 0}}}`), 1)
	bypass := `[{"op":"test","path":"/nodes/1/id","value":"work"},{"op":"remove","path":"/nodes/1"},
		{"op":"test","path":"/flows/1/id","value":"work_end"},{"op":"remove","path":"/flows/1"},
		{"op":"test","path":"/flows/0/id","value":"start_work"},{"op":"replace","path":"/flows/0/to","value":"end"},
		{"op":"remove","path":"/views/2d/positions/work"}]`
	r := mustApply(t, doc, bypass, AIEdit)
	if !r.Valid() {
		t.Fatalf("%v %v", r.PatchIssues, r.Validation.Issues)
	}
	// Without the position removal the candidate keeps a dangling position.
	r = mustApply(t, doc, strings.Replace(bypass, `,
		{"op":"remove","path":"/views/2d/positions/work"}`, "", 1), AIEdit)
	if !r.Applied() || r.Validation.Valid() {
		t.Fatalf("want an applied but invalid candidate, got %v %v", r.PatchIssues, r.Validation.Issues)
	}
	if !slices.ContainsFunc(r.Validation.Issues, func(i model.ValidationIssue) bool { return i.Code == validate.ViewReferenceInvalid }) {
		t.Fatalf("want VIEW_REFERENCE_INVALID, got %v", r.Validation.Issues)
	}
}

// D-020: changing an array element requires a prior test of its identity,
// and any index shift in between voids that test.
func TestTestBeforeModify(t *testing.T) {
	cases := []struct {
		name, patch, code, path string
	}{
		{"no test", `[{"op":"replace","path":"/resources/0/capacity","value":2}]`, TestRequired, "/0/path"},
		{"test of another element", `[{"op":"test","path":"/nodes/0/id","value":"start"},{"op":"replace","path":"/nodes/1/name","value":"x"}]`, TestRequired, "/1/path"},
		{"index shift voids the test", `[{"op":"test","path":"/nodes/2/id","value":"end"},{"op":"add","path":"/nodes/0","value":{"id":"q","type":"queue"}},{"op":"replace","path":"/nodes/2/name","value":"x"}]`, TestRequired, "/2/path"},
		{"remove needs a test", `[{"op":"remove","path":"/flows/1"}]`, TestRequired, "/0/path"},
		{"constraints are identified by node_id", `[{"op":"add","path":"/constraints/-","value":{"type":"outgoing_probability_sum","node_id":"work","equals":1}},{"op":"replace","path":"/constraints/0/equals","value":1}]`, TestRequired, "/1/path"},
		{"dash is only for add", `[{"op":"remove","path":"/nodes/-"}]`, PathInvalid, "/0/path"},
		{"non-numeric index", `[{"op":"replace","path":"/nodes/abc/name","value":"x"}]`, PathInvalid, "/0/path"},
		{"pointer too deep", `[{"op":"add","path":"` + strings.Repeat("/metadata", MaxPointerTokens+1) + `","value":1}]`, validate.LimitExceeded, "/0/path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantIssue(t, mustApply(t, base(t), c.patch, ManualDraft), c.code, c.path)
		})
	}
	// Appending needs no test; a test followed by the change passes.
	for _, ok := range []string{
		`[{"op":"add","path":"/nodes/-","value":{"id":"q","type":"queue"}}]`,
		`[{"op":"test","path":"/constraints/0/node_id","value":"work"},{"op":"replace","path":"/constraints/0/equals","value":1}]`,
	} {
		doc := base(t)
		if strings.Contains(ok, "constraints/0") {
			doc = bytes.Replace(doc, []byte(`"constraints": []`), []byte(`"constraints": [{"type":"outgoing_probability_sum","node_id":"work","equals":1}]`), 1)
		}
		if r := mustApply(t, doc, ok, ManualDraft); !r.Applied() {
			t.Errorf("%s: %v", ok, r.PatchIssues)
		}
	}
}

// TEST-003 atomicity: when a later op fails nothing from earlier ops survives
// and the base document is untouched.
func TestAtomic(t *testing.T) {
	doc := base(t)
	before := bytes.Clone(doc)
	cases := []struct {
		name, patch, code, path string
	}{
		{"second op targets a missing element", `[{"op":"test","path":"/resources/0/id","value":"worker"},{"op":"replace","path":"/resources/0/capacity","value":5},{"op":"test","path":"/nodes/1/id","value":"work"},{"op":"replace","path":"/nodes/1/missing/x","value":1}]`, TargetMissing, "/3/path"},
		{"wrong identity test", `[{"op":"test","path":"/resources/0/id","value":"someone_else"},{"op":"replace","path":"/resources/0/capacity","value":5}]`, TestFailed, "/0/value"},
		{"replace of a missing member", `[{"op":"replace","path":"/metadata","value":{}}]`, TargetMissing, "/0/path"},
		{"test of a whole array element", `[{"op":"test","path":"/entity_types/0","value":{"id":"change","name":"Other"}}]`, TestFailed, "/0/value"},
		{"index past the end", `[{"op":"test","path":"/flows/1/id","value":"work_end"},{"op":"add","path":"/flows/3","value":{}}]`, TargetMissing, "/1/path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantIssue(t, mustApply(t, doc, c.patch, ManualDraft), c.code, c.path)
			if !bytes.Equal(doc, before) {
				t.Fatal("the base document was modified")
			}
		})
	}
}

// Array removal and insertion keep every other element in order (regression
// for a lost-update bug in an earlier draft of apply).
func TestArrayEditsKeepOrder(t *testing.T) {
	r := mustApply(t, base(t), `[
		{"op":"add","path":"/entity_types/0","value":{"id":"first","name":"First"}},
		{"op":"add","path":"/entity_types/-","value":{"id":"last","name":"Last"}},
		{"op":"test","path":"/entity_types/1/id","value":"change"},
		{"op":"remove","path":"/entity_types/1"}]`, ManualDraft)
	if !r.Applied() {
		t.Fatalf("%v", r.PatchIssues)
	}
	var got struct {
		EntityTypes []struct {
			ID string `json:"id"`
		} `json:"entity_types"`
	}
	if err := json.Unmarshal(r.Candidate, &got, json.RejectUnknownMembers(false)); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range got.EntityTypes {
		ids = append(ids, e.ID)
	}
	if !slices.Equal(ids, []string{"first", "last"}) {
		t.Fatalf("entity_types = %v", ids)
	}
}

// RFC 6902 test compares JSON values: 1 equals 1.0, member order is irrelevant.
func TestTestComparesJSONValues(t *testing.T) {
	for _, p := range []string{
		`[{"op":"test","path":"/resources/0/capacity","value":1.0}]`,
		`[{"op":"test","path":"/entity_types/0","value":{"name":"Change","id":"change"}}]`,
		`[{"op":"test","path":"/routing_never","value":null}]`,
	} {
		r := mustApply(t, base(t), p, ManualDraft)
		if strings.Contains(p, "routing_never") {
			wantIssue(t, r, TargetMissing, "/0/path")
			continue
		}
		if !r.Applied() {
			t.Errorf("%s: %v", p, r.PatchIssues)
		}
	}
}

func TestParsePointer(t *testing.T) {
	ok := map[string][]string{"/a": {"a"}, "/a~0b~1c": {"a~b/c"}, "/a/": {"a", ""}, "/0/-": {"0", "-"}}
	for in, want := range ok {
		got, err := parsePointer(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "a", "/a~2", "/a~", "/~" + "x", strings.Repeat("/a", MaxPointerTokens+1)} {
		if _, err := parsePointer(bad); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
	for _, good := range []string{"0", "7", "10"} {
		if _, err := arrayIndex(good); err != nil {
			t.Errorf("index %q rejected", good)
		}
	}
	for _, bad := range []string{"", "01", "-1", "+1", " 1", "1e3", "99999999999999999999999"} {
		if _, err := arrayIndex(bad); err == nil {
			t.Errorf("index %q accepted", bad)
		}
	}
}

func TestConcurrentApply(t *testing.T) {
	doc := base(t)
	const p = `[{"op":"test","path":"/resources/0/id","value":"worker"},{"op":"replace","path":"/resources/0/capacity","value":3}]`
	want := mustApply(t, doc, p, Scenario)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			r, err := Apply(doc, []byte(p), Scenario)
			if err != nil || !bytes.Equal(r.Candidate, want.Candidate) {
				t.Errorf("concurrent result differs: %v", err)
			}
		})
	}
	wg.Wait()
}

// Any patch either applies completely or leaves no candidate, never panics,
// and never modifies the base.
func FuzzApply(f *testing.F) {
	for _, s := range []string{
		`[{"op":"test","path":"/resources/0/id","value":"worker"},{"op":"replace","path":"/resources/0/capacity","value":2}]`,
		`[{"op":"add","path":"/nodes/-","value":{"id":"q","type":"queue"}}]`,
		`[{"op":"remove","path":"/flows/0"}]`, `[{"op":"move","from":"/a","path":"/b"}]`, `[]`,
	} {
		f.Add([]byte(s), uint8(0))
	}
	doc := base(f)
	f.Fuzz(func(t *testing.T, p []byte, m uint8) {
		before := bytes.Clone(doc)
		r, err := Apply(doc, p, Mode(m%3))
		if err != nil {
			t.Fatalf("internal error: %v", err)
		}
		if !bytes.Equal(doc, before) {
			t.Fatal("base modified")
		}
		hasError := slices.ContainsFunc(r.PatchIssues, func(i model.ValidationIssue) bool { return i.Severity == "error" })
		if hasError == r.Applied() {
			t.Fatalf("applied=%v with patch issues %v", r.Applied(), r.PatchIssues)
		}
		for _, i := range append(slices.Clone(r.PatchIssues), r.Validation.Issues...) {
			if !utf8.ValidString(i.Message) || !utf8.ValidString(i.Path) ||
				utf8.RuneCountInString(i.Message) > model.MaxIssueMessage || utf8.RuneCountInString(i.Path) > model.MaxIssuePath {
				t.Fatalf("issue does not fit the ValidationIssue schema: %q", i.Path)
			}
		}
	})
}

// Review of T-004 / T-005: a Mode outside the three defined modes is a
// programming error, reported as an error rather than a panic.
func TestUnknownMode(t *testing.T) {
	_, err := Apply(base(t), []byte(`[{"op":"replace","path":"/name","value":"x"}]`), Mode(3))
	if err == nil {
		t.Fatal("unknown mode accepted")
	}
	if got := Mode(7).String(); got != "mode(7)" {
		t.Errorf("String() = %q", got)
	}
}

// The patch document is bounded before it is parsed (D-029 limits).
func TestPatchSizeLimit(t *testing.T) {
	big := `[{"op":"add","path":"/metadata","value":{"k":"` + strings.Repeat("x", validate.MaxIRBytes) + `"}}]`
	r := mustApply(t, base(t), big, ManualDraft)
	if len(r.PatchIssues) != 1 || r.PatchIssues[0].Code != validate.LimitExceeded || r.PatchIssues[0].Path != "" {
		t.Fatalf("want one LIMIT_EXCEEDED at the document root, got %v", r.PatchIssues)
	}
}

// D-020: replacing or removing a whole non-empty catalog array changes its
// existing elements without their id tests, so it is refused in every mode;
// creating a catalog that is absent or empty is allowed.
func TestWholeCatalogReplace(t *testing.T) {
	for _, mode := range []Mode{ManualDraft, AIEdit} {
		for _, p := range []string{
			`[{"op":"replace","path":"/resources","value":[{"id":"worker","name":"W","type":"human","capacity":9}]}]`,
			`[{"op":"add","path":"/resources","value":[{"id":"worker","name":"W","type":"human","capacity":9}]}]`,
			`[{"op":"remove","path":"/entity_types"}]`,
		} {
			r := mustApply(t, base(t), p, mode)
			wantIssue(t, r, TestRequired, "/0/path")
		}
	}
	// constraints is [] in the base, metrics is absent: both may be created whole.
	for _, p := range []string{
		`[{"op":"replace","path":"/constraints","value":[{"type":"outgoing_probability_sum","node_id":"work","equals":1}]}]`,
		`[{"op":"add","path":"/metrics","value":[]}]`,
	} {
		if r := mustApply(t, base(t), p, ManualDraft); !r.Applied() {
			t.Errorf("%s: %v", p, r.PatchIssues)
		}
	}
}

// D-022: the position may only be dropped for a node that is gone after the
// patch; removing and re-adding the same id keeps the node, so its position
// must stay.
func TestAIKeepsPositionOfReaddedNode(t *testing.T) {
	doc := bytes.Replace(base(t), []byte(`"2d": {}`), []byte(`"2d": {"positions": {"start": {"x": 0, "y": 0},
		"work": {"x": 1, "y": 0}, "end": {"x": 2, "y": 0}}}`), 1)
	p := `[{"op":"test","path":"/nodes/2/id","value":"end"},{"op":"remove","path":"/nodes/2"},
		{"op":"add","path":"/nodes/-","value":{"id":"end","type":"sink"}},
		{"op":"remove","path":"/views/2d/positions/end"}]`
	wantIssue(t, mustApply(t, doc, p, AIEdit), PathForbidden, "/3/path")
}

// D-021 / TWIN_IR_SPEC §6: a scenario may change distribution parameters but
// may not create a dataset binding; moving an existing one is allowed.
func TestScenarioCannotCreateDatasetBinding(t *testing.T) {
	doc := base(t)
	testWork := `{"op":"test","path":"/nodes/1/id","value":"work"},`
	newRef := `[` + testWork + `{"op":"replace","path":"/nodes/1/service_time/distribution","value":{"type":"empirical","samples_ref":"dataset:anything_new"}}]`
	wantIssue(t, mustApply(t, doc, newRef, Scenario), PathForbidden, "/1/value")
	member := `[` + testWork + `{"op":"replace","path":"/nodes/1/service_time/distribution","value":{"type":"empirical","samples_ref":"dataset:review_duration"}}]`
	// A base that already binds dataset:review_duration on another node.
	bound := mustApply(t, doc, `[{"op":"test","path":"/nodes/0/id","value":"start"},{"op":"replace","path":"/nodes/0/arrival/distribution","value":{"type":"empirical","samples_ref":"dataset:review_duration"}}]`, ManualDraft)
	if !bound.Applied() {
		t.Fatalf("setup: %v", bound.PatchIssues)
	}
	if r := mustApply(t, bound.Candidate, member, Scenario); !r.Applied() {
		t.Errorf("reusing a bound dataset: %v", r.PatchIssues)
	}
	pointer := `[` + testWork + `{"op":"replace","path":"/nodes/1/service_time/distribution","value":{"type":"empirical","samples_ref":"dataset:review_duration"}},
		{"op":"replace","path":"/nodes/1/service_time/distribution/samples_ref","value":"dataset:other_new"}]`
	wantIssue(t, mustApply(t, bound.Candidate, pointer, Scenario), PathForbidden, "/2/value")
}

// TEST-003: copy, like move, is outside the P0 profile in every mode.
func TestCopyIsRejected(t *testing.T) {
	for _, mode := range []Mode{ManualDraft, AIEdit, Scenario} {
		r := mustApply(t, base(t), `[{"op":"copy","from":"/name","path":"/metadata"}]`, mode)
		if r.Applied() || len(r.PatchIssues) == 0 || r.PatchIssues[0].Code != SchemaInvalid {
			t.Errorf("%v: %v", mode, r.PatchIssues)
		}
	}
}
