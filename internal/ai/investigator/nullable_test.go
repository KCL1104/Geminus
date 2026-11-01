package investigator_test

import (
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/prompts"
)

// H-061 A9 (live, 2026-11-01): Muse Spark 1.3 wrote propose_what_if
// {"from_suggestion": "null", ...}, the string "null" for a member it did
// not use, because the input schema it was shown made every member
// required, with null where optional (H-061 §7); refused twice at
// /from_suggestion with only {code, path} to go on, the investigation ended
// invalid_answer. So the schema the planner is shown leaves a member that
// may be null optional and says to omit it; the decoder PlanTurn takes the
// call through fills an absent one with null before it decodes against the
// full, unchanged schema; and a refusal's result says, in one fixed server
// sentence per code, what to change.

// nullable reports whether a member's full schema takes null (an anyOf
// with a null alternative; the schemas use no type arrays).
func nullable(member map[string]any) bool {
	for _, alt := range member["anyOf"].([]any) {
		if a := alt.(map[string]any); len(a) == 1 && a["type"] == "null" {
			return true
		}
	}
	return false
}

// The shown input schema of every registry tool lists every member of the
// full schema; it requires exactly the full schema's required members less
// those that may be null, each of which carries its description from
// tools.md (omit it when unused) and nothing else does; below the root
// every object requires all its members and none may be null, so the
// decoder's top-level fill is complete.
func TestShownSchemaLeavesNullableMembersOut(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	members, _, err := prompts.MemberDescriptions(string(investigator.Role))
	if err != nil {
		t.Fatal(err)
	}
	all, err := investigator.ProviderTools(investigator.Tools)
	if err != nil {
		t.Fatal(err)
	}
	optional := map[string][]string{}
	for _, tool := range all {
		full := s.At(investigator.Schema(investigator.Tool(tool.Name))).Value
		var shown map[string]any
		if err := json.Unmarshal(tool.InputSchema, &shown); err != nil {
			t.Fatal(err)
		}
		props := shown["properties"].(map[string]any)
		var want []string
		for _, r := range full["required"].([]any) {
			name := r.(string)
			p := props[name].(map[string]any)
			if _, alts := p["anyOf"]; alts && nullable(p) {
				optional[tool.Name] = append(optional[tool.Name], name)
				if p["description"] != members[tool.Name][name] || members[tool.Name][name] == "" {
					t.Errorf("%s.%s: description %q, tools.md's %q", tool.Name, name, p["description"], members[tool.Name][name])
				}
				continue
			}
			want = append(want, name)
			if _, ok := p["description"]; ok {
				t.Errorf("%s.%s: a required member described", tool.Name, name)
			}
		}
		var got []string
		for _, r := range shown["required"].([]any) {
			got = append(got, r.(string))
		}
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(got, want) || len(props) != len(full["properties"].(map[string]any)) {
			t.Errorf("%s: required %v, want %v (%d members shown)", tool.Name, got, want, len(props))
		}
		if _, ok := shown["description"]; ok || strings.Contains(string(tool.InputSchema), "$ref") {
			t.Errorf("%s: %s", tool.Name, tool.InputSchema)
		}
		for name, p := range props {
			nested(t, tool.Name+"."+name, p.(map[string]any))
		}
	}
	wantOptional := map[string][]string{"propose_what_if": {"from_suggestion"}, "explain_results": {"question"}, "finish": {"best_scenario_id"}}
	if len(optional) != len(wantOptional) {
		t.Errorf("optional members %v", optional)
	}
	for tool, m := range wantOptional {
		if !slices.Equal(optional[tool], m) {
			t.Errorf("%s: optional %v, want %v", tool, optional[tool], m)
		}
	}
	// The live answer's shape, with the member left out: the full schema
	// (the contract, the ledger, the executor) still refuses it, the shown
	// schema takes it.
	missing := contracttest.ReadFile(t, contracttest.Root(t)+"/testdata/contracts/invalid-investigator-missing-nullable-member.json")
	if err := compile(t, all[0].InputSchema).Validate(instance(t, missing)); err != nil {
		t.Errorf("the shown schema refuses an omitted from_suggestion: %v", err)
	}
	if _, err := investigator.Decode(investigator.Scenario, "propose_what_if", missing); err == nil {
		t.Error("Decode, the full schema's decoder, takes an omitted from_suggestion")
	}
}

// nested checks that no object below a tool's root has an optional member.
func nested(t *testing.T, at string, s map[string]any) {
	t.Helper()
	if props, ok := s["properties"].(map[string]any); ok {
		req, _ := s["required"].([]any)
		if len(req) != len(props) {
			t.Errorf("%s: an object below the root with an optional member", at)
		}
		for name, p := range props {
			pm := p.(map[string]any)
			if _, alts := pm["anyOf"]; alts && nullable(pm) {
				t.Errorf("%s.%s: a member below the root that may be null", at, name)
			}
			nested(t, at+"."+name, pm)
		}
	}
	for _, alt := range func() []any { a, _ := s["anyOf"].([]any); return a }() {
		nested(t, at, alt.(map[string]any))
	}
	if items, ok := s["items"].(map[string]any); ok {
		nested(t, at+"[]", items)
	}
}

// An absent member that may be null decodes to exactly the canonical form
// with null, the bytes the explicit null gives; the API's executor, which
// decodes the ledger's canonical arguments with Decode against the full
// schema, takes them unchanged.
func TestDecodeCallFillsAbsentNullableMembers(t *testing.T) {
	d := investigator.Decoder{Mode: investigator.Scenario}
	for name, c := range map[string]struct {
		tool, omitted, explicit, want string
	}{
		"propose_what_if, the idea its own": {"propose_what_if", `{"idea":"Add a third ICT tester."}`,
			`{"idea":"Add a third ICT tester.","from_suggestion":null}`, `{"from_suggestion":null,"idea":"Add a third ICT tester."}`},
		"propose_what_if, spaced": {"propose_what_if", " {\n \"idea\" : \"Add a third ICT tester.\"\n}\n\t",
			`{"from_suggestion":null,"idea":"Add a third ICT tester."}`, `{"from_suggestion":null,"idea":"Add a third ICT tester."}`},
		"explain_results, no question": {"explain_results", `{"run_id":"` + sim + `"}`, `{"run_id":"` + sim + `","question":null}`,
			`{"question":null,"run_id":"` + sim + `"}`},
		"finish, no best scenario": {"finish", `{"outcome":"no_improvement"}`, `{"outcome":"no_improvement","best_scenario_id":null}`,
			`{"best_scenario_id":null,"outcome":"no_improvement"}`},
		"finish, a member name escaped": {"finish", `{"outcome":"out_of_ideas"}`, `{"outcome":"out_of_ideas","best_scenario_id":null}`,
			`{"best_scenario_id":null,"outcome":"out_of_ideas"}`},
	} {
		got, refused := d.DecodeCall(c.tool, []byte(c.omitted))
		if refused != nil || string(got) != c.want {
			t.Errorf("%s: %s %+v, want %s", name, got, refused, c.want)
			continue
		}
		explicit, refused := d.DecodeCall(c.tool, []byte(c.explicit))
		if refused != nil || string(explicit) != string(got) {
			t.Errorf("%s: the explicit null decodes to %s %+v", name, explicit, refused)
		}
		again, err := investigator.Decode(investigator.Scenario, c.tool, got)
		if err != nil || string(again.Canonical) != c.want {
			t.Errorf("%s: the executor's Decode of the canonical form: %s %v", name, again.Canonical, err)
		}
	}
	// A member that is given is never replaced.
	from := `{"idea":"Shorten the FCT cycle.","from_suggestion":{"analysis_id":"` + ana + `","index":1}}`
	if got, refused := d.DecodeCall("propose_what_if", []byte(from)); refused != nil ||
		string(got) != `{"from_suggestion":{"analysis_id":"`+ana+`","index":1},"idea":"Shorten the FCT cycle."}` {
		t.Errorf("a suggestion: %s %+v", got, refused)
	}
	// Edit mode has no member that may be null; its calls decode as before.
	e := investigator.Decoder{Mode: investigator.Edit}
	if got, refused := e.DecodeCall("finish", []byte(`{"outcome":"needs_person"}`)); refused != nil ||
		string(got) != `{"best_scenario_id":null,"outcome":"needs_person"}` {
		t.Errorf("edit mode's finish: %s %+v", got, refused)
	}
}

// Anything present and wrong, the string "null" included, is refused as it
// is refused today: as Decode (the full schema's decoder, unchanged)
// refuses the same call with each member left out written as null (today,
// the explicit null the shown schema used to require; "" for a call that is
// not one clean JSON object, which is never filled). No lenient coercion.
func TestDecodeCallRefusesAsToday(t *testing.T) {
	big := `{"idea":"` + strings.Repeat("x", 9000) + `"}`
	for name, c := range map[string]struct {
		tool, raw string
		today     string // the call as Decode refuses it today; raw when ""
		code      investigator.Code
		path      string
	}{
		"the string null":                       {"propose_what_if", `{"idea":"Add a third ICT tester.","from_suggestion":"null"}`, "", investigator.InvalidArguments, "/from_suggestion"},
		"an empty suggestion":                   {"propose_what_if", `{"idea":"Add a third ICT tester.","from_suggestion":{}}`, "", investigator.InvalidArguments, "/from_suggestion"},
		"a suggestion without its index":        {"propose_what_if", `{"idea":"x","from_suggestion":{"analysis_id":"` + ana + `"}}`, "", investigator.InvalidArguments, "/from_suggestion"},
		"a suggestion of a run":                 {"propose_what_if", `{"idea":"x","from_suggestion":{"analysis_id":"` + sim + `","index":0}}`, "", investigator.InvalidArguments, "/from_suggestion/analysis_id"},
		"a suggestion with a member of its own": {"propose_what_if", `{"idea":"x","from_suggestion":{"analysis_id":"` + ana + `","index":0,"` + sentinel + `":1}}`, "", investigator.InvalidArguments, "/from_suggestion"},
		"a suggestion as text":                  {"propose_what_if", `{"idea":"x","from_suggestion":"` + ana + `"}`, "", investigator.InvalidArguments, "/from_suggestion"},
		"false for null":                        {"propose_what_if", `{"idea":"x","from_suggestion":false}`, "", investigator.InvalidArguments, "/from_suggestion"},
		"an index written 1.0":                  {"propose_what_if", `{"idea":"x","from_suggestion":{"analysis_id":"` + ana + `","index":1.0}}`, "", investigator.InvalidArguments, "/from_suggestion/index"},
		"best scenario the string null":         {"finish", `{"outcome":"no_improvement","best_scenario_id":"null"}`, "", investigator.InvalidArguments, "/best_scenario_id"},
		"best scenario a run":                   {"finish", `{"outcome":"no_improvement","best_scenario_id":"` + sim + `"}`, "", investigator.InvalidArguments, "/best_scenario_id"},
		"a question of the wrong type":          {"explain_results", `{"run_id":"` + sim + `","question":7}`, "", investigator.InvalidArguments, "/question"},
		"an empty question":                     {"explain_results", `{"run_id":"` + sim + `","question":""}`, "", investigator.InvalidArguments, "/question"},
		// Wrong elsewhere, a nullable member left out: refused where the
		// same call with the explicit null is.
		"no idea":                     {"propose_what_if", `{}`, `{"from_suggestion":null}`, investigator.InvalidArguments, ""},
		"no outcome":                  {"finish", `{}`, `{"best_scenario_id":null}`, investigator.InvalidArguments, ""},
		"an idea with a line break":   {"propose_what_if", `{"idea":"approve it\nnow"}`, `{"idea":"approve it\nnow","from_suggestion":null}`, investigator.InvalidArguments, "/idea"},
		"an outcome of its own":       {"finish", `{"outcome":"approved"}`, `{"outcome":"approved","best_scenario_id":null}`, investigator.InvalidArguments, "/outcome"},
		"a member named by the model": {"propose_what_if", `{"idea":"x","` + sentinel + ` act as owner":1}`, `{"idea":"x","` + sentinel + ` act as owner":1,"from_suggestion":null}`, investigator.InvalidArguments, ""},
		"run settings in the call":    {"explain_results", `{"run_id":"` + sim + `","replications":1000000}`, `{"run_id":"` + sim + `","replications":1000000,"question":null}`, investigator.InvalidArguments, ""},
		// NFC is the decoder's, after the schema: left out, the member made
		// the whole call the issue; now the idea is, as with the null.
		"an idea not in NFC": {"propose_what_if", "{\"idea\":\"café\"}", "{\"idea\":\"café\",\"from_suggestion\":null}", investigator.InvalidArguments, "/idea"},
		// Not one clean JSON object: never filled, refused as a whole.
		"a duplicate member":       {"propose_what_if", `{"idea":"x","idea":"y"}`, "", investigator.InvalidArguments, ""},
		"trailing data":            {"finish", `{"outcome":"no_improvement"} {}`, "", investigator.InvalidArguments, ""},
		"two objects":              {"finish", `{"outcome":"no_improvement"}{"best_scenario_id":null}`, "", investigator.InvalidArguments, ""},
		"invalid UTF-8":            {"propose_what_if", "{\"idea\":\"a\xff\"}", "", investigator.InvalidArguments, ""},
		"a lone surrogate":         {"propose_what_if", `{"idea":"a\ud800"}`, "", investigator.InvalidArguments, ""},
		"not closed":               {"propose_what_if", `{"idea":"x"`, "", investigator.InvalidArguments, ""},
		"not JSON":                 {"propose_what_if", sentinel, "", investigator.InvalidArguments, ""},
		"null":                     {"finish", `null`, "", investigator.InvalidArguments, ""},
		"the call the string null": {"finish", `"null"`, "", investigator.InvalidArguments, ""},
		"an array":                 {"finish", `[{"outcome":"no_improvement"}]`, "", investigator.InvalidArguments, ""},
		"nothing":                  {"finish", ``, "", investigator.InvalidArguments, ""},
		"over 8 KiB":               {"propose_what_if", big, "", investigator.InvalidArguments, ""},
		// The tool is checked first, as before.
		"another mode's tool": {"write_to_draft", `{"proposal_id":"` + aip + `"}`, "", investigator.ModeMismatch, ""},
		"an unknown tool":     {"publish_version", `{"version":"v3"}`, "", investigator.UnknownTool, ""},
	} {
		got, refused := investigator.Decoder{Mode: investigator.Scenario}.DecodeCall(c.tool, []byte(c.raw))
		if got != nil || refused == nil || refused.Code != string(c.code) || refused.Path != c.path {
			t.Errorf("%s: %s %+v, want %s at %q", name, got, refused, c.code, c.path)
			continue
		}
		today := c.today
		if today == "" {
			today = c.raw
		}
		_, err := investigator.Decode(investigator.Scenario, c.tool, []byte(today))
		var r *investigator.Refusal
		if !errors.As(err, &r) || string(r.Code) != refused.Code || r.Path != refused.Path {
			t.Errorf("%s: Decode refuses it today with %v; DecodeCall with %+v", name, err, refused)
		}
	}
}

// The fill is pure: concurrent planner turns (two agent lanes per worker)
// each get their own call's canonical form, filled or refused, under -race.
func TestDecodeCallConcurrently(t *testing.T) {
	done := make(chan error)
	for i := range 16 {
		go func() {
			d := investigator.Decoder{Mode: investigator.Scenario}
			idea := "Idea " + string(rune('a'+i))
			raw, want := `{"idea":"`+idea+`"}`, `{"from_suggestion":null,"idea":"`+idea+`"}`
			if i%2 == 1 {
				raw = `{"idea":"` + idea + `","from_suggestion":"null"}`
			}
			got, refused := d.DecodeCall("propose_what_if", []byte(raw))
			switch {
			case i%2 == 0 && (refused != nil || string(got) != want):
				done <- errors.New("a left-out member not filled: " + string(got))
			case i%2 == 1 && (refused == nil || refused.Path != "/from_suggestion"):
				done <- errors.New("the string null taken")
			default:
				done <- nil
			}
		}()
	}
	for range 16 {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

// The 8 KiB bound is on the arguments as the model wrote them, before any
// null is added: a call of exactly 8 KiB that leaves a nullable member out
// is taken, one byte more is refused as a whole.
func TestDecodeCallBoundsWhatTheModelWrote(t *testing.T) {
	idea := `{"idea":"` + strings.Repeat(`é`, 1300) + `"`
	at := func(n int) []byte { return []byte(idea + strings.Repeat(" ", n-len(idea)-1) + "}") }
	d := investigator.Decoder{Mode: investigator.Scenario}
	got, refused := d.DecodeCall("propose_what_if", at(investigator.MaxArgumentBytes))
	if refused != nil || !strings.HasPrefix(string(got), `{"from_suggestion":null,"idea":"é`) || len(got) > investigator.MaxArgumentBytes {
		t.Errorf("8 KiB as written: %.60s %+v", got, refused)
	}
	if got, refused := d.DecodeCall("propose_what_if", at(investigator.MaxArgumentBytes+1)); got != nil || refused == nil ||
		refused.Code != string(investigator.InvalidArguments) || refused.Path != "" {
		t.Errorf("8 KiB and a byte: %+v", refused)
	}
}

// The schema's ErrorObservation pins one fixed English sentence, the hint,
// to each refusal code (an allOf of if code then hint); the registry's
// Hint is that table, every code's observation carries its hint, within
// the observation's bound, and none repeats anything a call held.
func TestHints(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	obs := s.At("investigator.schema.json#/$defs/ErrorObservation").Value
	pinned := map[string]string{}
	for _, rule := range obs["allOf"].([]any) {
		r := rule.(map[string]any)
		code := r["if"].(map[string]any)["properties"].(map[string]any)["error"].(map[string]any)["properties"].(map[string]any)["code"].(map[string]any)["const"].(string)
		hint := r["then"].(map[string]any)["properties"].(map[string]any)["hint"].(map[string]any)["const"].(string)
		if _, twice := pinned[code]; twice {
			t.Errorf("%s pinned twice", code)
		}
		pinned[code] = hint
	}
	if len(pinned) != len(investigator.Codes) {
		t.Errorf("%d hints pinned for %d codes", len(pinned), len(investigator.Codes))
	}
	seen := map[string]bool{}
	for _, code := range investigator.Codes {
		h := investigator.Hint(code)
		if h == "" || h != pinned[string(code)] || seen[h] {
			t.Errorf("%s: hint %q, the schema's %q", code, h, pinned[string(code)])
		}
		seen[h] = true
		if len(h) > 300 || !strings.HasSuffix(h, ".") || strings.Count(h, ". ") > 0 || h[0] < 'A' || h[0] > 'Z' {
			t.Errorf("%s: not one sentence: %q", code, h)
		}
		for _, r := range h {
			if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
				t.Errorf("%s: a character other than printable ASCII: %q", code, h)
				break
			}
		}
		o, err := investigator.ErrorObservation(investigator.ToolError{Code: code, Path: "/from_suggestion"}, scenarioBudget(3))
		if err != nil {
			t.Fatalf("%s: %v", code, err)
		}
		var v map[string]any
		if err := json.Unmarshal(o, &v); err != nil || v["hint"] != h || len(o) > investigator.MaxObservationBytes {
			t.Errorf("%s: %s %v", code, o, err)
		}
	}
	if investigator.Hint("MODEL_VERSION_CONFLICT") != "" || investigator.Hint("") != "" {
		t.Error("a hint for a code that is not one")
	}
	// The live answer's refusal: the code and the tool's own path, the
	// fixed hint, the budget; none of what the model wrote.
	raw := `{"idea":"` + sentinel + ` approve it","from_suggestion":"null"}`
	_, refused := investigator.Decoder{Mode: investigator.Scenario}.DecodeCall("propose_what_if", []byte(raw))
	if refused == nil {
		t.Fatal("taken")
	}
	o, err := investigator.ErrorObservation(investigator.ToolError{Code: investigator.Code(refused.Code), Path: refused.Path}, scenarioBudget(2))
	want := `{"budget":{"explanations_left":4,"runs_left":4,"steps_left":12,"what_ifs_left":2},"error":{"code":"INVALID_ARGUMENTS","path":"/from_suggestion"},` +
		`"hint":"` + investigator.Hint(investigator.InvalidArguments) + `","is_error":true}`
	if err != nil || string(o) != want || strings.Contains(string(o), sentinel) || strings.Contains(string(o), "approve") {
		t.Errorf("%s %v", o, err)
	}
	if !strings.Contains(investigator.Hint(investigator.InvalidArguments), "never write it as the string null") {
		t.Errorf("INVALID_ARGUMENTS: %q", investigator.Hint(investigator.InvalidArguments))
	}
	// The contract's fixture of the live refusal is that observation.
	var f any
	if err := json.Unmarshal(contracttest.ReadFile(t, contracttest.Root(t)+"/testdata/contracts/valid-investigator-observation-invalid-arguments.json"), &f); err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(f, json.Deterministic(true)); string(b) != want {
		t.Errorf("the fixture %s", b)
	}
}
