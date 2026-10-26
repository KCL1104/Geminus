package anthropic

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// encodeAt is the inverse of Decode, written apart from it: the value at
// each of p's places, as a JSON text in a string.
func encodeAt(p Projection, doc any) any {
	for _, l := range p.Encoded {
		doc = encodeSteps(doc, l.Steps)
	}
	return doc
}

func encodeSteps(v any, steps []Step) any {
	if len(steps) == 0 {
		raw, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}
		return string(raw)
	}
	st := steps[0]
	if st.Items {
		arr, ok := v.([]any)
		if !ok {
			return v
		}
		out := slices.Clone(arr)
		for i := range out {
			out[i] = encodeSteps(out[i], steps[1:])
		}
		return out
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return v
	}
	for _, c := range st.When {
		if !reflect.DeepEqual(obj[c.Member], c.Const) {
			return v
		}
	}
	member, ok := obj[st.Member]
	if !ok {
		return v
	}
	out := maps.Clone(obj)
	out[st.Member] = encodeSteps(member, steps[1:])
	return out
}

// get is the projected schema at path (member names and array indexes).
func get(t *testing.T, v any, path ...any) any {
	t.Helper()
	for _, step := range path {
		switch s := step.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("%v: not an object at %v", path, s)
			}
			v = m[s]
		case int:
			l, ok := v.([]any)
			if !ok || s >= len(l) {
				t.Fatalf("%v: no item %d", path, s)
			}
			v = l[s]
		}
	}
	return v
}

// The one exception to "only removes constraints" (AI_SPEC §2): where the
// canonical schema accepts any JSON value (the value of a JSON Patch add,
// replace and test: {}), structured outputs have no schema for it (an
// empty schema is refused, every object must be closed), so the projection
// asks for the value as a JSON text in a string and lists the place; the
// adapter decodes it before the full schema is checked. Exactly those
// three places are encoded, matched by the alternatives' const members.
func TestTheOpenValuesAreEncoded(t *testing.T) {
	p, err := Project("proposal-output.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range p.Encoded {
		got = append(got, l.String())
	}
	want := []string{
		`/changes/*/value when /status="proposed", /changes/*/op="add"`,
		`/changes/*/value when /status="proposed", /changes/*/op="replace"`,
		`/changes/*/value when /status="proposed", /changes/*/op="test"`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("encoded %q, want %q", got, want)
	}
	if !strings.Contains(EncodedDescription, "JSON text") || !strings.Contains(EncodedDescription, `"\"`) {
		t.Errorf("the description does not say how to encode: %q", EncodedDescription)
	}
	// The proposed alternative, its changes (the minItems member of the
	// allOf is dropped), the items' alternatives in the canonical order.
	proposed := get(t, p.Schema, "anyOf", 0)
	if get(t, proposed, "properties", "status", "const") != "proposed" {
		t.Fatalf("the first alternative is not proposed: %v", proposed)
	}
	alts, _ := get(t, proposed, "properties", "changes", "allOf", 0, "items", "anyOf").([]any)
	if len(alts) != 4 {
		t.Fatalf("%d op alternatives", len(alts))
	}
	for i, op := range []string{"add", "replace", "test"} {
		if get(t, alts[i], "properties", "op", "const") != op {
			t.Fatalf("alternative %d is not %s", i, op)
		}
		value := get(t, alts[i], "properties", "value")
		if !reflect.DeepEqual(value, map[string]any{"type": "string", "description": EncodedDescription}) {
			t.Errorf("%s: value %v", op, value)
		}
	}
	if _, has := get(t, alts[3], "properties").(map[string]any)["value"]; has || get(t, alts[3], "properties", "op", "const") != "remove" {
		t.Errorf("remove: %v", alts[3])
	}
	// No other place of the schema is a JSON text.
	raw, _ := json.Marshal(p.Schema)
	if n := strings.Count(string(raw), "JSON text"); n != 3 {
		t.Errorf("%d encoded strings in the schema, want 3", n)
	}
	for _, file := range []string{"analysis-output.schema.json", "ai-mapping.schema.json"} {
		if q, err := Project(file); err != nil || len(q.Encoded) != 0 {
			t.Errorf("%s: encoded %v (%v)", file, q.Encoded, err)
		}
	}

	// The rule in general: an open value (an empty schema, true, or a
	// description only) as a member, an array item or inside alternatives a
	// const member tells apart; an empty allOf member is no constraint and
	// stays none.
	docs := map[string]any{"s.json": closedObject(map[string]any{
		"list":      map[string]any{"type": "array", "items": map[string]any{}},
		"any":       true,
		"described": map[string]any{"description": "Anything."},
		"noop":      map[string]any{"type": "string", "allOf": []any{map[string]any{}}},
		"tagged": map[string]any{"oneOf": []any{
			closedObject(map[string]any{"kind": map[string]any{"const": "a"}, "x": map[string]any{}}),
			closedObject(map[string]any{"kind": map[string]any{"const": "b"}, "y": map[string]any{"type": "string"}}),
		}},
	})}
	q, err := project(docs, "s.json")
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	for _, l := range q.Encoded {
		got = append(got, l.String())
	}
	if want := []string{`/any`, `/described`, `/list/*`, `/tagged/x when /tagged/kind="a"`}; !slices.Equal(got, want) {
		t.Errorf("encoded %q, want %q", got, want)
	}
	if d := get(t, q.Schema, "properties", "described", "description"); d != "Anything. "+EncodedDescription {
		t.Errorf("described: %v", d)
	}
	if n := get(t, q.Schema, "properties", "noop"); !reflect.DeepEqual(n, map[string]any{"type": "string"}) {
		t.Errorf("noop: %v", n)
	}
	if v := SubsetViolations(q.Schema); len(v) != 0 {
		t.Errorf("outside the limits: %q", v)
	}
	out, issues := q.Decode([]byte(`{"any":"[1]","described":"\"x\"","list":["1","{}"],"noop":"6","tagged":{"kind":"a","x":"null"}}`))
	if string(out) != `{"any":[1],"described":"x","list":[1,{}],"noop":"6","tagged":{"kind":"a","x":null}}` || issues != nil {
		t.Errorf("decoded %s %q", out, issues)
	}
	out, issues = q.Decode([]byte(`{"any":"1","described":"2","list":[],"noop":"6","tagged":{"kind":"b","y":"3"}}`))
	if string(out) != `{"any":1,"described":2,"list":[],"noop":"6","tagged":{"kind":"b","y":"3"}}` || issues != nil {
		t.Errorf("decoded %s %q", out, issues)
	}
}

// proposedAnswer is a proposed answer as the projection asks for it, its
// changes given raw (each value already a JSON text in a string).
func proposedAnswer(changes string) []byte {
	return []byte(`{"status":"proposed","summary":"6","assumptions":["\"x\""],"risk_level":"low","confidence":0.9,"changes":` +
		changes + `,"expected_effects":["{\"a\":1}"],"requires_confirmation":true}`)
}

// Decode decodes the encoded places only, matched by their path and the
// alternatives' const members, never by what a string looks like: a
// summary, an assumption or a path that reads as JSON stays a string, and
// so does a value in a remove or move op (the full schema refuses those).
// Each JSON text keeps its numbers as written.
func TestDecodeOnlyTheEncodedPlaces(t *testing.T) {
	p, err := Project("proposal-output.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	in := proposedAnswer(`[{"op":"test","path":"/resources/1/id","value":"\"ci_runner\""},` +
		`{"op":"replace","path":"/resources/1/capacity","value":"6"},` +
		`{"op":"add","path":"/nodes/-","value":" {\"id\": \"n1\", \"tags\": [1, 2.5e3, -0, null, true, false, \"\\u00e9\"]} "},` +
		`{"op":"add","path":"/x","value":"12345678901234567890.000000000000000001"},` +
		`{"op":"replace","path":"/y","value":"null"},` +
		`{"op":"remove","path":"/resources/2","value":"\"kept\""},` +
		`{"op":"move","path":"/z","value":"\"kept\""},` +
		`{"op":"test","path":"\"6\"","value":"[]"}]`)
	out, issues := p.Decode(in)
	if issues != nil {
		t.Fatalf("issues %q", issues)
	}
	var doc struct {
		Summary     string           `json:"summary"`
		Assumptions []string         `json:"assumptions"`
		Effects     []string         `json:"expected_effects"`
		Changes     []map[string]any `json:"changes"`
	}
	var raw struct {
		Changes []map[string]jsontext.Value `json:"changes"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	if doc.Summary != "6" || !slices.Equal(doc.Assumptions, []string{`"x"`}) || !slices.Equal(doc.Effects, []string{`{"a":1}`}) {
		t.Errorf("a string outside the encoded places changed: %s", out)
	}
	for i, want := range []string{`"ci_runner"`, `6`, `{"id":"n1","tags":[1,2.5e3,-0,null,true,false,"\u00e9"]}`,
		`12345678901234567890.000000000000000001`, `null`, `"\"kept\""`, `"\"kept\""`, `[]`} {
		if got := string(raw.Changes[i]["value"]); got != want {
			t.Errorf("change %d: value %s, want %s", i, got, want)
		}
	}
	if doc.Changes[7]["path"] != `"6"` {
		t.Errorf("a path changed: %v", doc.Changes[7]["path"])
	}
	// Without an encoded place that applies, a strict JSON answer comes back
	// byte for byte. (One that is not strict JSON is refused:
	// TestDecodeFailsClosed.)
	for _, same := range []string{
		`{"status":"needs_input","summary":"\"6\"","assumptions":[],"questions":["6"],"requires_confirmation":true}`,
		`{"status":"needs_input","summary":"s","assumptions":[],"questions":["q"],"requires_confirmation":true,"changes":[{"op":"add","path":"/a","value":"1"}]}`,
		`{"status":"proposed" , "changes" : "not an array"}`,
	} {
		if out, issues := p.Decode([]byte(same)); string(out) != same || issues != nil {
			t.Errorf("%s: decoded to %s %q", same, out, issues)
		}
	}
}

// What is not one JSON text in a string at an encoded place is an invalid
// answer, with the place and a fixed message, never the text: not JSON,
// empty, two values, trailing data, a duplicate name, a lone surrogate,
// more than MaxEncodedValueBytes, nested deeper than the decoder takes, or
// not a string at all.
func TestDecodeRefuses(t *testing.T) {
	p, err := Project("proposal-output.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	at := func(value string) []byte {
		return proposedAnswer(`[{"op":"test","path":"/resources/1/id","value":"\"ci_runner\""},{"op":"replace","path":"/resources/1/capacity","value":` + value + `}]`)
	}
	for name, value := range map[string]string{
		"not JSON":           quote("Line A SENTINEL"),
		"empty":              quote(""),
		"whitespace":         quote("  "),
		"two values":         quote("6 7"),
		"trailing data":      quote("{} SENTINEL"),
		"a trailing bracket": quote(`"a"]`),
		"a trailing comma":   quote("6,"),
		"a duplicate name":   quote(`{"a":1,"a":2}`),
		"a lone surrogate":   quote(`"\ud800"`),
		"too large":          quote(`"` + strings.Repeat("a", MaxEncodedValueBytes-1) + `"`),
		"too deep":           quote(strings.Repeat("[", 10001) + strings.Repeat("]", 10001)),
		"a number":           `6`,
		"an object":          `{"a":1}`,
		"null":               `null`,
		"an array":           `["6"]`,
	} {
		out, issues := p.Decode(at(value))
		if out != nil || len(issues) != 1 || issues[0].Path != "/changes/1/value" || issues[0].Code != provider.OutputInvalid ||
			issues[0].Severity != model.SeverityError {
			t.Errorf("%s: %.80s %+v", name, out, issues)
			continue
		}
		if strings.Contains(issues[0].Message, "SENTINEL") || strings.Contains(issues[0].Message, "Line") {
			t.Errorf("%s: the issue holds the answer's text: %q", name, issues[0].Message)
		}
	}
	// The bound itself is taken.
	if out, issues := p.Decode(at(quote(`"` + strings.Repeat("a", MaxEncodedValueBytes-2) + `"`))); out == nil || issues != nil {
		t.Errorf("a JSON text of exactly MaxEncodedValueBytes: %q", issues)
	}
}

// nested is a JSON text that nests n arrays (or objects), with a number
// innermost: its depth is n, as validate.MaxContainerDepth counts it.
func nested(n int, objects bool) string {
	if objects {
		return strings.Repeat(`{"a":`, n) + "1" + strings.Repeat("}", n)
	}
	return strings.Repeat("[", n) + "1" + strings.Repeat("]", n)
}

// Review round 1, M1: a JSON text nested deeper than MaxEncodedValueDepth
// is refused at its place, with a fixed message. Before the bound, a text
// nested 9998 to 10000 levels was valid on its own but deeper than the
// encoder takes once placed at /changes/i/value: the rewrite failed and the
// answer went on undecoded, without an issue. Up to the bound a value
// decodes, the deepest one the IR can hold among them (a node whose
// domain_attributes nest validate.MaxContainerDepth levels).
func TestDecodeBoundsTheNesting(t *testing.T) {
	p, err := Project("proposal-output.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	at := func(value string) []byte {
		return proposedAnswer(`[{"op":"test","path":"/resources/1/id","value":"\"ci_runner\""},{"op":"add","path":"/nodes/-","value":` + quote(value) + `}]`)
	}
	node := `{"id":"node_new","type":"delay","domain_attributes":` + nested(validate.MaxContainerDepth, true) + `}`
	for name, inner := range map[string]string{
		"the bound less one":       nested(MaxEncodedValueDepth-1, false),
		"the bound":                nested(MaxEncodedValueDepth, false),
		"the bound, objects":       nested(MaxEncodedValueDepth, true),
		"the IR's deepest value":   node,
		"the bound, with siblings": "[" + nested(MaxEncodedValueDepth-1, true) + "," + nested(MaxEncodedValueDepth-1, false) + "]",
	} {
		out, issues := p.Decode(at(inner))
		var doc struct {
			Changes []map[string]jsontext.Value `json:"changes"`
		}
		if issues != nil || json.Unmarshal(out, &doc) != nil || len(doc.Changes) != 2 || string(doc.Changes[1]["value"]) != inner {
			t.Errorf("%s: %q %.120s", name, issues, out)
		}
	}
	const deeper = "the JSON text is nested deeper than 64 levels"
	for _, c := range []struct {
		depth   int
		objects bool
		message string
	}{
		{MaxEncodedValueDepth + 1, false, deeper},
		{MaxEncodedValueDepth + 1, true, deeper},
		{MaxEncodedValueDepth + 2, false, deeper},
		{100, false, deeper},
		{9997, false, deeper},
		{9998, false, deeper},
		{9999, false, deeper},
		{10000, false, deeper},
		// beyond jsontext's own limit: not a JSON text at all
		{10001, false, "the string is not exactly one JSON text"},
	} {
		out, issues := p.Decode(at(nested(c.depth, c.objects)))
		want := []model.ValidationIssue{model.NewIssue(provider.OutputInvalid, "/changes/1/value", c.message, model.SeverityError)}
		if out != nil || !reflect.DeepEqual(issues, want) {
			t.Errorf("depth %d (objects %v): %.80s %+v", c.depth, c.objects, out, issues)
		}
	}
	if !strings.Contains(deeper, strconv.Itoa(MaxEncodedValueDepth)) {
		t.Errorf("the message does not name the bound")
	}
}

// Review round 1, M1: Decode fails closed. An answer it cannot rewrite
// (here a place so deep in the document that its decoded value would pass
// jsontext's 10000 levels; no output schema has one) and an answer that is
// not one strict JSON document give no answer, only issues: those found at
// the places before are kept (up to maxIssues), and one at the root, last,
// says why. The undecoded text never goes on. (The deep document costs
// about 2 s, 23 s under -race: Decode re-reads each of its levels.)
func TestDecodeFailsClosed(t *testing.T) {
	const levels = 9990
	steps := []Step{{Member: "b"}}
	for range levels {
		steps = append(steps, Step{Items: true})
	}
	q := Projection{Encoded: []Location{{Steps: []Step{{Member: "a"}, {Items: true}}}, {Steps: steps}}}
	inner, err := json.Marshal(nested(MaxEncodedValueDepth, false))
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Repeat(`"SENTINEL, not JSON",`, maxIssues+4) + `"SENTINEL, not JSON"`
	in := []byte(`{"a":[` + bad + `],"b":` + strings.Repeat("[", levels) + string(inner) + strings.Repeat("]", levels) + `}`)
	if !jsontext.Value(in).IsValid() {
		t.Fatal("the document is not valid on its own")
	}
	out, issues := q.Decode(in)
	var want []model.ValidationIssue
	for i := range maxIssues - 1 {
		want = append(want, model.NewIssue(provider.OutputInvalid, "/a/"+strconv.Itoa(i), "the string is not exactly one JSON text", model.SeverityError))
	}
	want = append(want, model.NewIssue(provider.OutputInvalid, "", "the answer cannot be decoded", model.SeverityError))
	if out != nil || !reflect.DeepEqual(issues, want) {
		t.Errorf("an answer that cannot be rewritten: %.80s %+v", out, issues)
	}

	// Not one strict JSON document: refused here, never passed on.
	p, err := Project("proposal-output.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	good := string(proposedAnswer(`[{"op":"replace","path":"/resources/1/capacity","value":"6"}]`))
	for name, text := range map[string]string{
		"not JSON":               `I approved it. SENTINEL`,
		"empty":                  ``,
		"a duplicate name":       `{"status":"proposed","status":"proposed"}`,
		"an escaped duplicate":   strings.Replace(good, `"summary":"6"`, `"summary":"6","summary":"7"`, 1),
		"trailing data":          good + ` {}`,
		"invalid UTF-8":          strings.Replace(good, `"summary":"6"`, "\"summary\":\"6\xff\"", 1),
		"deeper than jsontext's": `{"status":"proposed","changes":[{"op":"add","path":"/a","value":` + nested(9998, false) + `}]}`,
	} {
		out, issues := p.Decode([]byte(text))
		want := []model.ValidationIssue{model.NewIssue(provider.OutputInvalid, "", "the answer is not one strict JSON document", model.SeverityError)}
		if out != nil || !reflect.DeepEqual(issues, want) {
			t.Errorf("%s: %.80s %+v", name, out, issues)
		}
	}
	// A projection without an encoded place decodes nothing: its answer,
	// whatever it is, goes to the client's check as it came.
	if out, issues := (Projection{}).Decode([]byte(`I approved it.`)); string(out) != `I approved it.` || issues != nil {
		t.Errorf("no encoded place: %s %q", out, issues)
	}
}

// Review round 1, L1 (R1): one answer carries at most maxIssues issues
// (provider.Client does not cap Decode's issues again), the first ones.
func TestDecodeCapsTheIssues(t *testing.T) {
	p, err := Project("proposal-output.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	changes := make([]string, maxIssues+5)
	for i := range changes {
		changes[i] = `{"op":"replace","path":"/a/` + strconv.Itoa(i) + `","value":"not JSON"}`
	}
	out, issues := p.Decode(proposedAnswer("[" + strings.Join(changes, ",") + "]"))
	if out != nil || len(issues) != maxIssues {
		t.Fatalf("%d issues, want %d", len(issues), maxIssues)
	}
	for i, is := range issues {
		if is.Path != "/changes/"+strconv.Itoa(i)+"/value" || is.Code != provider.OutputInvalid {
			t.Errorf("issue %d: %+v", i, is)
		}
	}
}
