package structured_test

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/structured"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
)

// H-068: Muse Wonder 1.0 is told the answer's schema in its prompt instead
// of a structured output format, with every JSON Patch value written as
// itself (Projection.Native), and its answer is read by DecodeNative: one
// strict JSON object and nothing around it, whose values at the encoded
// places are written back as the JSON texts the projection asks for and
// then decoded by Decode, so it comes back exactly as Decode returns the
// same answer in the projection's form, under the same bounds.

// nativeTypes is the type list of a native value: any JSON value.
var nativeTypes = []any{"string", "number", "boolean", "null", "object", "array"}

func project(t *testing.T, file string) structured.Projection {
	t.Helper()
	p, err := structured.Project(file)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return p
}

// The native schema is the projection, but at its encoded places: there it
// asks for any JSON value, written as itself, with the canonical schema's
// description and NativeDescription, never for a JSON text in a string.
// Derived in the same pass over the same canonical schema, so it cannot
// drift from what Muse Spark is asked; a schema without an encoded place
// has a native schema equal to its projection.
func TestTheNativeSchemaDiffersOnlyAtTheEncodedPlaces(t *testing.T) {
	for file, places := range map[string]int{"proposal-output.schema.json": 3, "analysis-output.schema.json": 0, "ai-mapping.schema.json": 0} {
		p := project(t, file)
		if p.Native == nil {
			t.Fatalf("%s: no native schema", file)
		}
		n := 0
		var swap func(v any) any
		swap = func(v any) any {
			switch x := v.(type) {
			case map[string]any:
				if d, ok := x["description"].(string); ok && len(x) == 2 && x["type"] == "string" && strings.HasSuffix(d, structured.EncodedDescription) {
					n++
					return map[string]any{"type": nativeTypes, "description": strings.TrimSuffix(d, structured.EncodedDescription) + structured.NativeDescription}
				}
				out := map[string]any{}
				for k, y := range x {
					out[k] = swap(y)
				}
				return out
			case []any:
				out := make([]any, len(x))
				for i, y := range x {
					out[i] = swap(y)
				}
				return out
			}
			return v
		}
		if got := swap(p.Schema); !reflect.DeepEqual(got, p.Native) {
			t.Errorf("%s: the native schema is not the projection with native values at its encoded places", file)
		}
		if n != places || len(p.Encoded) != places {
			t.Errorf("%s: %d encoded string schemas, %d encoded places; want %d", file, n, len(p.Encoded), places)
		}
		raw, err := json.Marshal(p.Native, json.Deterministic(true))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "as a JSON text (RFC 8259)") {
			t.Errorf("%s: the native schema still asks for a JSON text", file)
		}
		if got := strings.Count(string(raw), "Never a JSON text inside a string."); got != places {
			t.Errorf("%s: the native description appears %d times, want %d", file, got, places)
		}
		// Deterministic: the request a golden pins is the same every time.
		again := project(t, file)
		raw2, _ := json.Marshal(again.Native, json.Deterministic(true))
		if !bytes.Equal(raw, raw2) {
			t.Errorf("%s: the native schema is not deterministic", file)
		}
	}
}

func compile(t *testing.T, schema map[string]any) *jsonschema.Schema {
	t.Helper()
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource("https://native.test/schema.json", doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("https://native.test/schema.json")
	if err != nil {
		t.Fatalf("the native schema is not a valid schema: %v", err)
	}
	return s
}

// Every valid proposal-output fixture, as it is written (its patch values
// native), is an answer under the native schema and decodes back to
// itself; every structurally invalid one (an extra member, a wrong status,
// a missing member) fails the native schema too, but those that break a
// dropped conditional, which the canonical schema still catches.
func TestEveryValidFixtureIsANativeAnswer(t *testing.T) {
	const file = "proposal-output.schema.json"
	p := project(t, file)
	s := compile(t, p.Native)
	root := contracttest.Root(t)
	n := 0
	for _, c := range contracttest.Cases(t) {
		if c.Schema != "schemas/"+file {
			continue
		}
		n++
		data := contracttest.ReadFile(t, filepath.Join(root, c.Instance))
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		err = s.Validate(inst)
		if c.Valid && err != nil {
			t.Errorf("valid fixture %s fails the native schema: %v", c.ID, err)
		}
		if !c.Valid && err == nil && !strings.Contains(c.ID, "confirmation") && !strings.Contains(c.ID, "low-confidence") {
			t.Errorf("invalid fixture %s passes the native schema", c.ID)
		}
		if !c.Valid {
			continue
		}
		decoded, issues := p.DecodeNative(data)
		var back, want any
		if len(issues) > 0 || json.Unmarshal(decoded, &back) != nil || json.Unmarshal(data, &want) != nil || !reflect.DeepEqual(back, want) {
			t.Errorf("valid fixture %s does not decode back to itself: %v", c.ID, issues)
		}
	}
	if n == 0 {
		t.Fatal("no fixture checked")
	}
}

func quote(s string) string {
	q, err := jsontext.AppendQuote(nil, s)
	if err != nil {
		panic(err)
	}
	return string(q)
}

// proposal is a proposal answer whose changes are changes.
func proposal(changes ...string) string {
	return `{"status":"proposed","summary":"Raise it.","assumptions":["Service times stay the same."],"risk_level":"low","confidence":0.9,` +
		`"changes":[` + strings.Join(changes, ",") + `],"expected_effects":["The queue may fall; simulate to know."],"requires_confirmation":true}`
}

// change is a change of op at path whose value is written as value.
func change(op, path, value string) string {
	return `{"op":"` + op + `","path":"` + path + `","value":` + value + `}`
}

// escaped is a JSON string with escapes: a quote, a backslash, a tab and a
// \u escape, written as the model may write it.
var escaped = `"q\"b\\s\t` + `\` + `u00e9"`

// nativeValues are values of every kind as a model may write them natively.
var nativeValues = map[string]string{
	"a string":                       `"fct_tester"`,
	"a string with escapes":          escaped,
	"an integer":                     `4`,
	"a fraction":                     `0.25`,
	"a number with an exponent":      `-1.5e3`,
	"true":                           `true`,
	"false":                          `false`,
	"null":                           `null`,
	"an object":                      `{"id":"node_9","kind":"process","name":"ICT tester","domain_attributes":{"station":"ict","pass":[1,2]}}`,
	"an object with white space":     `{ "id" : "node_9" , "tags" : [ 1 , 2 ] }`,
	"an empty object":                `{}`,
	"an array":                       `[1,"a",null,{"b":false},[]]`,
	"an empty array":                 `[]`,
	"a string that holds a number":   `"4"`,
	"a string that holds true":       `"true"`,
	"a string that holds null":       `"null"`,
	"a string that is not JSON":      `"[draft] line 2"`,
	"an object whose member is text": `{"value":"\"not a place\""}`,
}

// A native value of every kind decodes exactly as the same value written as
// the projection asks (a JSON text in a string) does: byte for byte the
// answer Decode returns, so the server's review, repair, storage, hash and
// audit see what they saw for Muse Spark. A string that holds a number,
// true, false, null or text that is not JSON is a string: in a native
// answer it cannot be told from a name or a label that reads so, and the
// server's review checks the patched model against the IR schema.
func TestNativeValuesDecodeAsTheirEncodedFormDoes(t *testing.T) {
	p := project(t, "proposal-output.schema.json")
	for name, v := range nativeValues {
		for op, path := range map[string]string{"add": "/nodes/-", "replace": "/resources/0/capacity", "test": "/resources/0/id"} {
			native := proposal(change("test", "/resources/0/id", `"ict_tester"`), change(op, path, v), `{"op":"remove","path":"/flows/3"}`)
			encoded := proposal(change("test", "/resources/0/id", quote(`"ict_tester"`)), change(op, path, quote(v)), `{"op":"remove","path":"/flows/3"}`)
			want, issues := p.Decode([]byte(encoded))
			if len(issues) > 0 {
				t.Fatalf("%s, %s: the encoded form does not decode: %v", name, op, issues)
			}
			got, issues := p.DecodeNative([]byte(native))
			if len(issues) > 0 {
				t.Errorf("%s, %s: %v", name, op, issues)
				continue
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s, %s: the native answer decodes to\n%s\nthe encoded one to\n%s", name, op, got, want)
			}
			var doc struct {
				Changes []struct {
					Value jsontext.Value `json:"value"`
				} `json:"changes"`
			}
			if err := json.Unmarshal(got, &doc); err != nil || len(doc.Changes) != 3 {
				t.Fatalf("%s, %s: %v", name, op, err)
			}
			var gotV, wantV any
			if json.Unmarshal(doc.Changes[1].Value, &gotV) != nil || json.Unmarshal([]byte(v), &wantV) != nil || !reflect.DeepEqual(gotV, wantV) {
				t.Errorf("%s, %s: the value is %s, want %s", name, op, doc.Changes[1].Value, v)
			}
		}
	}
	// An answer with no encoded place that applies comes back byte for byte,
	// white space around it included.
	for _, text := range []string{
		`{"status":"needs_input","summary":"Which station?","assumptions":[],"questions":["Which station?"],"requires_confirmation":true}`,
		proposal(`{"op":"remove","path":"/flows/3"}`),
		"\n " + proposal(`{"op":"remove","path":"/flows/3"}`) + " \n",
	} {
		got, issues := p.DecodeNative([]byte(text))
		if len(issues) > 0 || string(got) != text {
			t.Errorf("an answer without a place: %v %q", issues, got)
		}
	}
}

// What is not exactly one strict JSON object, or holds at an encoded place
// a value written as a JSON text in a string (a string, an array or an
// object: what the projection asks of Muse Spark, which no value of a
// model is), or a value over the projection's bounds, gives no answer and
// an issue with the place's JSON Pointer (the root for the answer as a
// whole) and a fixed message, never the text: H-057's repair takes it as
// it takes the projection's.
func TestNativeAnswersThatAreRefused(t *testing.T) {
	p := project(t, "proposal-output.schema.json")
	good := proposal(change("test", "/resources/0/id", `"ict_tester"`), change("replace", "/resources/0/capacity", `4`))
	deep := strings.Repeat("[", structured.MaxEncodedValueDepth+1) + strings.Repeat("]", structured.MaxEncodedValueDepth+1)
	large := quote(strings.Repeat("x", structured.MaxEncodedValueBytes))
	at := func(path string, v string) string {
		return proposal(change("test", "/resources/0/id", `"ict_tester"`), change("replace", path, v))
	}
	for name, c := range map[string]struct {
		text string
		path string
	}{
		"prose before":                   {"Here is the proposal: " + good, ""},
		"prose after":                    {good + " I hope this helps.", ""},
		"a code fence":                   {"```json\n" + good + "\n```", ""},
		"two objects":                    {good + good, ""},
		"two objects on two lines":       {good + "\n" + good, ""},
		"an array of the answer":         {"[" + good + "]", ""},
		"the answer as a string":         {quote(good), ""},
		"a number":                       {"4", ""},
		"nothing":                        {"", ""},
		"white space only":               {" \n", ""},
		"not JSON":                       {`{status: "proposed"}`, ""},
		"cut short":                      {good[:len(good)-1], ""},
		"duplicate members":              {strings.Replace(good, `"status":"proposed"`, `"status":"proposed","status":"needs_input"`, 1), ""},
		"duplicate members in a value":   {at("/nodes/-", `{"id":"a","id":"b"}`), ""},
		"invalid UTF-8":                  {strings.Replace(good, "Raise it.", "Raise \xff it.", 1), ""},
		"a JSON text of a string":        {at("/resources/0/name", quote(`"fct_tester"`)), "/changes/1/value"},
		"a JSON text of an object":       {at("/nodes/-", quote(`{"id":"node_1"}`)), "/changes/1/value"},
		"a JSON text of an empty object": {at("/nodes/-", quote(`{}`)), "/changes/1/value"},
		"a JSON text of an array":        {at("/nodes/-", quote(`[1,2]`)), "/changes/1/value"},
		"a JSON text after white space":  {at("/nodes/-", quote(` {"a":1} `)), "/changes/1/value"},
		"a test of a JSON text":          {proposal(change("test", "/resources/0/id", quote(`"ict_tester"`))), "/changes/0/value"},
		"too deep":                       {at("/nodes/-", deep), "/changes/1/value"},
		"too large":                      {at("/resources/0/name", large), "/changes/1/value"},
	} {
		got, issues := p.DecodeNative([]byte(c.text))
		if got != nil || len(issues) != 1 {
			t.Errorf("%s: answer %q, issues %v; want none and one issue", name, got, issues)
			continue
		}
		is := issues[0]
		if is.Code != provider.OutputInvalid || is.Path != c.path || is.Severity != model.SeverityError {
			t.Errorf("%s: issue %+v, want %s at %q", name, is, provider.OutputInvalid, c.path)
		}
		for _, x := range []string{"fct_tester", "node_1", "Raise", "proposal", "helps", "xxxx"} {
			if strings.Contains(is.Message, x) {
				t.Errorf("%s: the message quotes the answer: %q", name, is.Message)
			}
		}
	}
	// Every place is named, at most MaxIssues of them.
	var many []string
	for i := range structured.MaxIssues + 5 {
		many = append(many, change("test", fmt.Sprintf("/nodes/%d/id", i), quote(`"n"`)))
	}
	if got, issues := p.DecodeNative([]byte(proposal(many...))); got != nil || len(issues) != structured.MaxIssues || issues[3].Path != "/changes/3/value" {
		t.Errorf("%d places: %d issues", len(many), len(issues))
	}
	// A value just inside the bounds is an answer.
	inside := quote(strings.Repeat("x", structured.MaxEncodedValueBytes-2))
	if _, issues := p.DecodeNative([]byte(at("/resources/0/name", inside))); len(issues) > 0 {
		t.Errorf("a value of %d bytes: %v", structured.MaxEncodedValueBytes, issues)
	}
	ok := strings.Repeat("[", structured.MaxEncodedValueDepth) + strings.Repeat("]", structured.MaxEncodedValueDepth)
	if _, issues := p.DecodeNative([]byte(at("/nodes/-", ok))); len(issues) > 0 {
		t.Errorf("a value %d levels deep: %v", structured.MaxEncodedValueDepth, issues)
	}
}

// Muse Spark's path is unchanged: Decode still refuses a native value where
// the projection asks for a JSON text in a string, and DecodeNative is not
// Decode.
func TestDecodeStillAsksForAJSONText(t *testing.T) {
	p := project(t, "proposal-output.schema.json")
	native := proposal(change("replace", "/resources/0/capacity", `4`))
	if got, issues := p.Decode([]byte(native)); got != nil || len(issues) != 1 || issues[0].Path != "/changes/0/value" {
		t.Errorf("Decode of a native value: %q %v", got, issues)
	}
}
