package investigator_test

import (
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
)

const (
	aip = "aip_01M4YDQK00B8S5WQXJ3TQ7ZK30"
	scn = "scn_01M4YDQK00B8S5WQXJ3TQ7ZK40"
	sim = "sim_01M4YDQK00B8S5WQXJ3TQ7ZK51"
	ana = "ana_01M4YDQK00B8S5WQXJ3TQ7ZK60"
)

func schemaEnum(t *testing.T, s *contracttest.Schemas, ref string) []string {
	t.Helper()
	var out []string
	for _, v := range s.At(ref).Value["enum"].([]any) {
		out = append(out, v.(string))
	}
	return out
}

func strs[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}

// The registry is the schema's: the tools, each mode's tools, the refusal
// codes, the AI's outcomes and the caps (H-061 §7, §12).
func TestRegistryMatchesTheSchema(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	for _, c := range []struct {
		name string
		got  []string
		ref  string
	}{
		{"tools", strs(investigator.Tools), "investigator.schema.json#/$defs/InvestigatorTool"},
		{"scenario mode", strs(investigator.ToolsOf(investigator.Scenario)), "investigator.schema.json#/$defs/ScenarioModeTool"},
		{"edit mode", strs(investigator.ToolsOf(investigator.Edit)), "investigator.schema.json#/$defs/EditModeTool"},
		{"codes", strs(investigator.Codes), "investigator.schema.json#/$defs/InvestigationToolCode"},
		{"outcomes", strs(investigator.Outcomes), "investigator.schema.json#/$defs/AiOutcome"},
		{"modes", strs(investigator.Modes), "investigation.schema.json#/$defs/Investigation/properties/mode"},
	} {
		if want := schemaEnum(t, s, c.ref); !slices.Equal(c.got, want) {
			t.Errorf("%s: Go %v, schema %v", c.name, c.got, want)
		}
	}
	// invalid_call is the ledger's name for output that did not decode,
	// never a tool any mode offers.
	alts := s.At("investigation.schema.json#/$defs/InvestigationStepTool").Value["anyOf"].([]any)
	if got := alts[1].(map[string]any)["const"]; got != investigator.InvalidCall {
		t.Errorf("InvalidCall %q, schema %v", investigator.InvalidCall, got)
	}
	for _, m := range investigator.Modes {
		if investigator.Tool(investigator.InvalidCall).Offered(m) {
			t.Errorf("%s offers invalid_call", m)
		}
	}
	// The caps are the schema's bounds.
	maxOf := func(ref string) int { return int(s.At(ref).Value["maximum"].(float64)) }
	constOf := func(ref string) int { return int(s.At(ref).Value["const"].(float64)) }
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"steps", investigator.MaxSteps, maxOf("investigation.schema.json#/$defs/InvestigationStep/properties/seq")},
		{"steps cap", investigator.MaxSteps, constOf("investigation.schema.json#/$defs/InvestigationCaps/properties/steps")},
		{"steps left", investigator.MaxSteps, maxOf("investigator.schema.json#/$defs/Budget/properties/steps_left")},
		{"what-ifs", investigator.MaxWhatIfs, maxOf("investigator.schema.json#/$defs/Budget/properties/what_ifs_left")},
		{"runs", investigator.MaxRuns, maxOf("investigator.schema.json#/$defs/Budget/properties/runs_left")},
		{"explanations", investigator.MaxExplanations, maxOf("investigator.schema.json#/$defs/Budget/properties/explanations_left")},
		{"minutes", investigator.Minutes, constOf("investigation.schema.json#/$defs/InvestigationCaps/properties/minutes")},
		{"note", investigator.MaxNoteRunes, int(s.At("investigation.schema.json#/$defs/InvestigationStep/properties/note").Value["anyOf"].([]any)[0].(map[string]any)["maxLength"].(float64))},
	} {
		if c.got != c.want {
			t.Errorf("%s: Go %d, schema %d", c.name, c.got, c.want)
		}
	}
}

// Each tool's Go arguments have exactly the schema's members, and Budget
// the schema's Budget.
func TestArgumentTypesMatchTheSchema(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	types := map[investigator.Tool]reflect.Type{
		investigator.ProposeWhatIf:    reflect.TypeFor[investigator.ProposeWhatIfArguments](),
		investigator.SaveScenario:     reflect.TypeFor[investigator.SaveScenarioArguments](),
		investigator.RunWhatIf:        reflect.TypeFor[investigator.RunWhatIfArguments](),
		investigator.ExplainResults:   reflect.TypeFor[investigator.ExplainResultsArguments](),
		investigator.ProposeModelEdit: reflect.TypeFor[investigator.ProposeModelEditArguments](),
		investigator.WriteToDraft:     reflect.TypeFor[investigator.WriteToDraftArguments](),
		investigator.Finish:           reflect.TypeFor[investigator.FinishArguments](),
	}
	if len(types) != len(investigator.Tools) {
		t.Fatalf("%d argument types for %d tools", len(types), len(investigator.Tools))
	}
	for _, tool := range investigator.Tools {
		ref := investigator.Schema(tool)
		if !strings.HasPrefix(ref, "investigator.schema.json#/$defs/") {
			t.Errorf("%s: schema %q", tool, ref)
		}
		s.Parity(t, types[tool], ref)
		// The step's arguments for this tool are the same definition.
		if v := reflect.New(types[tool]).Elem().Interface().(investigator.Arguments); v.Tool() != tool {
			t.Errorf("%s: Tool() of its arguments is %s", tool, v.Tool())
		}
	}
	s.Parity(t, reflect.TypeFor[investigator.Budget](), "investigator.schema.json#/$defs/Budget")
	s.Parity(t, reflect.TypeFor[investigator.SuggestionRef](), "investigator.schema.json#/$defs/SuggestionRef")
}

// modeOf is a mode that offers tool.
func modeOf(tool investigator.Tool) investigator.Mode {
	if tool.Offered(investigator.Scenario) {
		return investigator.Scenario
	}
	return investigator.Edit
}

// toolOf is the tool whose arguments a fixture's schema names.
func toolOf(t *testing.T, schema string) (investigator.Tool, bool) {
	t.Helper()
	for _, tool := range investigator.Tools {
		if "schemas/"+investigator.Schema(tool) == schema {
			return tool, true
		}
	}
	return "", false
}

// The decoder accepts exactly the schema-valid fixtures, refuses each
// negative at the member it was written for, and its canonical form is
// RFC 8785 of the input.
func TestFixturesDecodeAsTheSchemaSays(t *testing.T) {
	root := contracttest.Root(t)
	var idx struct {
		Cases []struct {
			ID, Instance, Schema string
			Valid                bool
			ExpectedErrorPaths   []string `json:"expected_error_paths"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(root, "testdata", "contracts", "cases.json")), &idx,
		json.MatchCaseInsensitiveNames(true)); err != nil {
		t.Fatal(err)
	}
	seen := map[investigator.Tool]bool{}
	var n, negatives int
	for _, c := range idx.Cases {
		tool, ok := toolOf(t, c.Schema)
		if !ok {
			continue
		}
		n++
		seen[tool] = true
		data := contracttest.ReadFile(t, filepath.Join(root, c.Instance))
		call, err := investigator.Decode(modeOf(tool), string(tool), data)
		if c.Valid {
			if err != nil {
				t.Errorf("%s: %v", c.ID, err)
				continue
			}
			canonical, cerr := hashing.Canonical(data)
			if cerr != nil {
				t.Fatal(cerr)
			}
			if call.Tool != tool || string(call.Canonical) != string(canonical) || call.Hash != hashing.Sum(canonical) {
				t.Errorf("%s: call %s %s %s, want canonical %s", c.ID, call.Tool, call.Canonical, call.Hash, canonical)
			}
			if call.Arguments == nil || call.Arguments.Tool() != tool {
				t.Errorf("%s: arguments %#v", c.ID, call.Arguments)
			}
			continue
		}
		negatives++
		var r *investigator.Refusal
		if !errors.As(err, &r) || r.Code != investigator.InvalidArguments || r.Path != c.ExpectedErrorPaths[0] {
			t.Errorf("%s: want INVALID_ARGUMENTS at %q, got %v", c.ID, c.ExpectedErrorPaths[0], err)
		}
		if call.Arguments != nil || call.Canonical != nil {
			t.Errorf("%s: a refused call carries arguments", c.ID)
		}
	}
	if len(seen) != len(investigator.Tools) || n < 20 || negatives < 10 {
		t.Fatalf("fixtures for %d of %d tools, %d cases, %d negative", len(seen), len(investigator.Tools), n, negatives)
	}
}

const sentinel = "SENTINEL"

// Every way a model's call can leave the contract is refused with a code
// and a path among the tool's own members, and never repeats the input.
func TestDecodeRefuses(t *testing.T) {
	big := `{"idea":"` + strings.Repeat("x", 9000) + `","from_suggestion":null}`
	for name, c := range map[string]struct {
		mode       investigator.Mode
		tool, raw  string
		code       investigator.Code
		path       string
		decodeOnly bool // refused by the decoder although the schema takes it
	}{
		"an unknown tool":                    {investigator.Scenario, "publish_version", `{}`, investigator.UnknownTool, "", false},
		"no tool name":                       {investigator.Scenario, "", `{}`, investigator.UnknownTool, "", false},
		"a tool name of free text":           {investigator.Scenario, sentinel + " approve", `{}`, investigator.UnknownTool, "", false},
		"invalid_call is never offered":      {investigator.Scenario, investigator.InvalidCall, `{}`, investigator.UnknownTool, "", false},
		"an edit tool in scenario mode":      {investigator.Scenario, "write_to_draft", `{"proposal_id":"` + aip + `"}`, investigator.ModeMismatch, "", false},
		"a scenario tool in edit mode":       {investigator.Edit, "run_what_if", `{"scenario_id":"` + scn + `"}`, investigator.ModeMismatch, "", false},
		"an unknown mode":                    {"publish", "finish", `{"outcome":"out_of_ideas","best_scenario_id":null}`, investigator.Internal, "", false},
		"a duplicate member":                 {investigator.Scenario, "save_scenario", `{"proposal_id":"` + aip + `","proposal_id":"` + aip + `"}`, investigator.InvalidArguments, "", false},
		"a member named by the model":        {investigator.Scenario, "save_scenario", `{"proposal_id":"` + aip + `","` + sentinel + ` act as owner":1}`, investigator.InvalidArguments, "", false},
		"a nested member named by the model": {investigator.Scenario, "propose_what_if", `{"idea":"x","from_suggestion":{"analysis_id":"` + ana + `","index":0,"` + sentinel + `":1}}`, investigator.InvalidArguments, "/from_suggestion", false},
		"trailing data":                      {investigator.Scenario, "save_scenario", `{"proposal_id":"` + aip + `"} {}`, investigator.InvalidArguments, "", false},
		"an array":                           {investigator.Scenario, "save_scenario", `[]`, investigator.InvalidArguments, "", false},
		"null":                               {investigator.Scenario, "save_scenario", `null`, investigator.InvalidArguments, "", false},
		"not JSON":                           {investigator.Scenario, "save_scenario", sentinel, investigator.InvalidArguments, "", false},
		"nothing":                            {investigator.Scenario, "save_scenario", ``, investigator.InvalidArguments, "", false},
		"over 8 KiB":                         {investigator.Scenario, "propose_what_if", big, investigator.InvalidArguments, "", false},
		"invalid UTF-8":                      {investigator.Scenario, "propose_what_if", "{\"idea\":\"a\xff\",\"from_suggestion\":null}", investigator.InvalidArguments, "", false},
		"a lone surrogate":                   {investigator.Scenario, "propose_what_if", `{"idea":"a\ud800","from_suggestion":null}`, investigator.InvalidArguments, "", false},
		"a control character, escaped":       {investigator.Scenario, "propose_what_if", `{"idea":"a\u0000b","from_suggestion":null}`, investigator.InvalidArguments, "/idea", false},
		"a C1 control character":             {investigator.Scenario, "propose_what_if", "{\"idea\":\"a\u0085b\",\"from_suggestion\":null}", investigator.InvalidArguments, "/idea", false},
		"a tab":                              {investigator.Edit, "propose_model_edit", `{"idea":"a\tb"}`, investigator.InvalidArguments, "/idea", false},
		"an index as text":                   {investigator.Scenario, "propose_what_if", `{"idea":"x","from_suggestion":{"analysis_id":"` + ana + `","index":"1"}}`, investigator.InvalidArguments, "/from_suggestion/index", false},
		"a fractional index":                 {investigator.Scenario, "propose_what_if", `{"idea":"x","from_suggestion":{"analysis_id":"` + ana + `","index":1.5}}`, investigator.InvalidArguments, "/from_suggestion/index", false},
		"a negative index":                   {investigator.Scenario, "propose_what_if", `{"idea":"x","from_suggestion":{"analysis_id":"` + ana + `","index":-1}}`, investigator.InvalidArguments, "/from_suggestion/index", false},
		"an ID in lower case":                {investigator.Scenario, "save_scenario", `{"proposal_id":"` + strings.ToLower(aip) + `"}`, investigator.InvalidArguments, "/proposal_id", false},
		"a question of the wrong type":       {investigator.Scenario, "explain_results", `{"run_id":"` + sim + `","question":7}`, investigator.InvalidArguments, "/question", false},
		"best scenario missing":              {investigator.Scenario, "finish", `{"outcome":"no_improvement"}`, investigator.InvalidArguments, "", false},
		// NFC (H-061 §7) is the decoder's: a schema cannot say it. "é" as
		// e + U+0301 is the same text as U+00E9 but other bytes, so it
		// would hash and compare differently.
		"an idea not in NFC":    {investigator.Scenario, "propose_what_if", `{"idea":"café","from_suggestion":null}`, investigator.InvalidArguments, "/idea", true},
		"a question not in NFC": {investigator.Scenario, "explain_results", `{"run_id":"` + sim + `","question":"Å"}`, investigator.InvalidArguments, "/question", true},
		// An integer the schema takes (2020-12 counts 1.0 as one) but Go's
		// strict decoding does not: refused, never rounded.
		"an index written 1.0": {investigator.Scenario, "propose_what_if", `{"idea":"x","from_suggestion":{"analysis_id":"` + ana + `","index":1.0}}`, investigator.InvalidArguments, "/from_suggestion/index", true},
	} {
		call, err := investigator.Decode(c.mode, c.tool, []byte(c.raw))
		var r *investigator.Refusal
		if !errors.As(err, &r) || r.Code != c.code || r.Path != c.path {
			t.Errorf("%s: want %s at %q, got %v", name, c.code, c.path, err)
			continue
		}
		if call.Arguments != nil || call.Canonical != nil || call.Hash != "" {
			t.Errorf("%s: a refused call carries arguments", name)
		}
		if strings.Contains(err.Error(), sentinel) || strings.Contains(err.Error(), "xxxx") {
			t.Errorf("%s: the refusal repeats the input: %v", name, err)
		}
		// A decoder-only refusal is one the schema itself takes: the
		// decoder is stricter than the schema there, never looser.
		if c.decodeOnly {
			if issues, err := validate.Document([]byte(c.raw), investigator.Schema(investigator.Tool(c.tool)), "X"); err != nil || len(issues) > 0 {
				t.Errorf("%s: the schema refuses it too (%v %v); not decoder-only", name, issues, err)
			}
		}
	}
	// NFC text is taken: the same idea composed.
	if _, err := investigator.Decode(investigator.Scenario, "propose_what_if", []byte(`{"idea":"café","from_suggestion":null}`)); err != nil {
		t.Errorf("an NFC idea: %v", err)
	}
}

// Two spellings of the same call have one canonical form and one hash,
// the ledger's idempotency key for its arguments.
func TestCanonicalForm(t *testing.T) {
	a, err := investigator.Decode(investigator.Scenario, "propose_what_if",
		[]byte(`{"from_suggestion":{"index":1,"analysis_id":"`+ana+`"},"idea":"Add a tester"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := investigator.Decode(investigator.Scenario, "propose_what_if",
		[]byte("{ \"idea\" : \"\\u0041dd a tester\",\n\"from_suggestion\":{\"analysis_id\":\""+ana+"\",\"index\":1}}"))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"from_suggestion":{"analysis_id":"` + ana + `","index":1},"idea":"Add a tester"}`
	if string(a.Canonical) != want || string(b.Canonical) != want || a.Hash != b.Hash || a.Hash != hashing.Sum([]byte(want)) {
		t.Errorf("canonical %s / %s, hashes %s / %s", a.Canonical, b.Canonical, a.Hash, b.Hash)
	}
	if got := a.Arguments.(investigator.ProposeWhatIfArguments); got.Idea != "Add a tester" || got.FromSuggestion == nil ||
		*got.FromSuggestion != (investigator.SuggestionRef{AnalysisID: ana, Index: 1}) {
		t.Errorf("arguments %#v", got)
	}
}

// Encode is the canonical form of arguments the server holds (a digest, a
// test), checked by the same decoder: it never stores what Decode refuses.
func TestEncode(t *testing.T) {
	q := "Why did the wait drop?"
	for _, a := range []investigator.Arguments{
		investigator.ProposeWhatIfArguments{Idea: "Add a tester"},
		investigator.ProposeWhatIfArguments{Idea: "Try it", FromSuggestion: &investigator.SuggestionRef{AnalysisID: ana, Index: 2}},
		investigator.SaveScenarioArguments{ProposalID: aip},
		investigator.RunWhatIfArguments{ScenarioID: scn},
		investigator.ExplainResultsArguments{RunID: sim},
		investigator.ExplainResultsArguments{RunID: sim, Question: &q},
		investigator.FinishArguments{Outcome: investigator.ImprovementFound, BestScenarioID: ptr(scn)},
		investigator.FinishArguments{Outcome: investigator.OutOfIdeas},
	} {
		call, err := investigator.Encode(investigator.Scenario, a)
		if err != nil {
			t.Errorf("%#v: %v", a, err)
			continue
		}
		back, err := investigator.Decode(investigator.Scenario, string(a.Tool()), call.Canonical)
		if err != nil || !reflect.DeepEqual(back, call) || !reflect.DeepEqual(back.Arguments, a) {
			t.Errorf("%#v: %v; decoded %#v, encoded %#v", a, err, back, call)
		}
	}
	for name, c := range map[string]struct {
		mode investigator.Mode
		a    investigator.Arguments
		code investigator.Code
		path string
	}{
		"an empty idea":         {investigator.Scenario, investigator.ProposeWhatIfArguments{}, investigator.InvalidArguments, "/idea"},
		"an ID of another kind": {investigator.Scenario, investigator.SaveScenarioArguments{ProposalID: scn}, investigator.InvalidArguments, "/proposal_id"},
		"an unknown outcome":    {investigator.Scenario, investigator.FinishArguments{Outcome: "approved"}, investigator.InvalidArguments, "/outcome"},
		"another mode's tool":   {investigator.Edit, investigator.RunWhatIfArguments{ScenarioID: scn}, investigator.ModeMismatch, ""},
		"no arguments":          {investigator.Scenario, nil, investigator.Internal, ""},
	} {
		_, err := investigator.Encode(c.mode, c.a)
		var r *investigator.Refusal
		if !errors.As(err, &r) || r.Code != c.code || r.Path != c.path {
			t.Errorf("%s: want %s at %q, got %v", name, c.code, c.path, err)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// The decoder is safe to share: the worker's tool goroutines and the API's
// executor call it at once.
func TestDecodeConcurrently(t *testing.T) {
	done := make(chan error)
	for i := range 8 {
		go func() {
			raw := []byte(`{"run_id":"` + sim + `","question":null}`)
			if i%2 == 1 {
				raw = []byte(`{"run_id":"` + sim + `","question":"q` + sentinel + `\u0007"}`)
			}
			_, err := investigator.Decode(investigator.Scenario, "explain_results", raw)
			if (err == nil) == (i%2 == 1) {
				done <- errors.New("wrong verdict")
				return
			}
			done <- nil
		}()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}
