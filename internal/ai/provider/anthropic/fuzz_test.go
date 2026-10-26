package anthropic

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// decodeMessages are the only messages an issue of Decode says.
var decodeMessages = map[string]bool{
	"the projection asks for a string holding a JSON text here":                             true,
	"the string does not decode":                                                            true,
	"the JSON text is larger than " + strconv.Itoa(MaxEncodedValueBytes) + " bytes":         true,
	"the string is not exactly one JSON text":                                               true,
	"the JSON text is nested deeper than " + strconv.Itoa(MaxEncodedValueDepth) + " levels": true,
	"the answer is not one strict JSON document":                                            true,
	"the answer cannot be decoded":                                                          true,
}

func fuzzAnswer(changes string) string {
	return `{"status":"proposed","summary":"s","assumptions":[],"risk_level":"low","confidence":0.9,"changes":` + changes +
		`,"expected_effects":[],"requires_confirmation":true}`
}

// FuzzDecode holds, for any answer, the invariants of Decode on
// proposal-output's projection (the review round 1 reviewer's fuzz, with
// its M1 seed, which failed before the fix):
//   - no panic;
//   - with issues, no answer, and each issue AI_OUTPUT_INVALID with a fixed
//     message; without, the answer unchanged or one valid JSON document;
//   - when the answer Decode returns passes the full schema, every value of
//     an add, replace or test in it came from a string in the input that
//     holds exactly one JSON text equal to it: nothing undecoded and nothing
//     decoded twice reaches the client's check as valid.
func FuzzDecode(f *testing.F) {
	deep := func(n int) string {
		v, err := json.Marshal(strings.Repeat("[", n) + strings.Repeat("]", n))
		if err != nil {
			f.Fatal(err)
		}
		return string(v)
	}
	for _, seed := range []string{
		fuzzAnswer(`[{"op":"test","path":"/a","value":"\"x\""},{"op":"replace","path":"/b","value":"6"}]`),
		fuzzAnswer(`[{"op":"add","path":"/a","value":"{\"k\":[1,2,{\"z\":null}]}"},{"op":"remove","path":"/c"}]`),
		fuzzAnswer(`[{"op":"add","path":"/a","value":" [ 1 , 2 ] "}]`),
		fuzzAnswer(`[{"op":"add","path":"/a","value":"\"\\\"6\\\"\""}]`),
		`{"status":"needs_input","summary":"s","assumptions":[],"questions":["q"],"requires_confirmation":true}`,
		fuzzAnswer(`[{"op":"add","path":"/a","value":"1e5"},{"op":"test","path":"/a","value":"true"}]`),
		// M1: nested 9998 deep, valid on its own, too deep once placed
		fuzzAnswer(`[{"op":"test","path":"/a","value":"x"},{"op":"add","path":"/b","value":` + deep(9998) + `}]`),
		fuzzAnswer(`[{"op":"add","path":"/b","value":` + deep(10000) + `}]`),
		fuzzAnswer(`[{"op":"add","path":"/b","value":` + deep(MaxEncodedValueDepth) + `}]`),
		fuzzAnswer(`[{"op":"add","path":"/b","value":` + deep(MaxEncodedValueDepth+1) + `}]`),
	} {
		f.Add([]byte(seed))
	}
	p, err := Project("proposal-output.schema.json")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		out, issues := p.Decode(in)
		if len(issues) > 0 {
			if out != nil {
				t.Fatalf("issues and an answer")
			}
			if len(issues) > maxIssues {
				t.Fatalf("%d issues", len(issues))
			}
			for _, is := range issues {
				if is.Code != provider.OutputInvalid || !decodeMessages[is.Message] {
					t.Fatalf("issue %+v", is)
				}
			}
			return
		}
		if string(out) != string(in) && !jsontext.Value(out).IsValid() {
			t.Fatalf("an invalid rewrite: %q", out)
		}
		if is, err := validate.Document(out, "proposal-output.schema.json", provider.OutputInvalid); err != nil || len(is) > 0 {
			return // the client refuses it
		}
		var after, before struct {
			Changes []map[string]jsontext.Value `json:"changes"`
		}
		if json.Unmarshal(out, &after) != nil || json.Unmarshal(in, &before) != nil || len(after.Changes) != len(before.Changes) {
			t.Fatalf("cannot compare: %q -> %q", in, out)
		}
		for i, op := range after.Changes {
			var name string
			_ = json.Unmarshal(op["op"], &name)
			if name != "add" && name != "replace" && name != "test" {
				continue
			}
			var s string
			if err := json.Unmarshal(before.Changes[i]["value"], &s); err != nil {
				t.Fatalf("change %d: the input's value is not a string: %.120q", i, before.Changes[i]["value"])
			}
			var want, got any
			if err := json.Unmarshal([]byte(s), &want); err != nil {
				t.Fatalf("change %d: the input's string is not a JSON text (%v), yet the answer passes: %.120q", i, err, s)
			}
			if err := json.Unmarshal(op["value"], &got); err != nil || !reflect.DeepEqual(want, got) {
				t.Fatalf("change %d: decoded %.120q, want %.120q", i, op["value"], s)
			}
		}
	})
}
