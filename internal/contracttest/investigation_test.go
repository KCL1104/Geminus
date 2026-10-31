package contracttest_test

// H-061 A2 (PROPOSED): the investigation contract, checked from Go. The
// Python checker (scripts/check_contracts.py) validates the fixtures with
// the reference jsonschema library; the API's executor and every response
// test validate with internal/model/validate (santhosh-tekuri/jsonschema,
// Go's RE2 for patterns). These tests hold the two to the same verdicts on
// the H-061 fixtures, and pin the places where the investigation contract
// repeats another contract's list, so the copies cannot drift.

import (
	"encoding/json/v2"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
)

type fixtureCase struct {
	ID                 string   `json:"id"`
	Instance           string   `json:"instance"`
	Schema             string   `json:"schema"`
	Valid              bool     `json:"valid"`
	Layer              string   `json:"layer"`
	ExpectedErrorPaths []string `json:"expected_error_paths"`
}

func fixtureCases(t *testing.T) []fixtureCase {
	t.Helper()
	var idx struct {
		Cases []fixtureCase `json:"cases"`
	}
	data := contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "contracts", "cases.json"))
	if err := json.Unmarshal(data, &idx, json.RejectUnknownMembers(false)); err != nil {
		t.Fatal(err)
	}
	return idx.Cases
}

// h061Labels are the fixtures of the AI's labels on existing resources.
var h061Labels = []string{
	"valid-proposal-applied-scenario", "valid-proposal-applied-edit", "valid-proposal-ready-by-ai",
	"invalid-proposal-applied-without-investigation", "invalid-proposal-applied-with-decider",
	"invalid-proposal-applied-edit-without-revision", "invalid-proposal-applied-edit-with-version",
	"invalid-proposal-applied-scenario-without-scenario", "invalid-proposal-investigation-index",
	"invalid-proposal-draft-revision-when-ready",
	"valid-scenario-made-by-ai", "invalid-scenario-made-by-ai-without-proposal", "invalid-scenario-made-by-ai-free-text",
	"valid-run-made-by-ai", "invalid-run-made-by-ai-without-investigation",
	"valid-analysis-record-by-ai", "invalid-analysis-record-investigation-id",
	"valid-draft-ai-edit", "invalid-draft-ai-edit-without-proposal",
	"valid-model-version-ai-edit", "invalid-model-version-ai-edit-member",
}

func isH061(c fixtureCase) bool {
	return strings.HasPrefix(c.Schema, "schemas/investigation.schema.json") ||
		strings.HasPrefix(c.Schema, "schemas/investigator.schema.json") || slices.Contains(h061Labels, c.ID)
}

// Every H-061 fixture gets the Go validator's verdict that cases.json
// states, and each negative is refused at the member it was written for.
func TestInvestigationFixturesAgreeInGo(t *testing.T) {
	root := contracttest.Root(t)
	var n, labels, negatives int
	for _, c := range fixtureCases(t) {
		if c.Layer != "schema" || !isH061(c) {
			continue
		}
		n++
		if slices.Contains(h061Labels, c.ID) {
			labels++
		}
		ref := strings.TrimPrefix(c.Schema, "schemas/")
		issues, err := validate.Document(contracttest.ReadFile(t, filepath.Join(root, c.Instance)), ref, "SCHEMA")
		if err != nil {
			t.Errorf("%s: %v", c.ID, err)
			continue
		}
		if c.Valid {
			if len(issues) > 0 {
				t.Errorf("%s: want valid, got %v", c.ID, issues)
			}
			continue
		}
		negatives++
		if len(issues) == 0 {
			t.Errorf("%s: schema negative accepted by the Go validator", c.ID)
			continue
		}
		var at []string
		for _, i := range issues {
			at = append(at, i.Path)
		}
		for _, p := range c.ExpectedErrorPaths {
			if !slices.ContainsFunc(at, func(got string) bool { return got == p || strings.HasPrefix(got, p+"/") || p == "" }) {
				t.Errorf("%s: no issue at %q; got %v", c.ID, p, issues)
			}
		}
	}
	if labels != len(h061Labels) {
		t.Errorf("%d of the %d label fixtures are in cases.json", labels, len(h061Labels))
	}
	if n < 90 || negatives < 50 {
		t.Fatalf("only %d H-061 fixtures (%d negative) exercised", n, negatives)
	}
}

// Every label fixture is an existing fixture plus the AI's members only,
// so a person's rows are byte for byte as before (H-061 §10: labels are
// present only on AI rows).
func TestLabelsAreOptionalMembersOnly(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	for _, c := range []struct{ ref, member string }{
		{"proposal.schema.json#", "investigation"},
		{"proposal.schema.json#", "applied_draft_revision"},
		{"scenario.schema.json#", "made_by_ai"},
		{"simulation.schema.json#/$defs/Run", "made_by_ai"},
		{"http.schema.json#/$defs/AnalysisRecord", "investigation_id"},
		{"http.schema.json#/$defs/Draft", "ai_edit"},
		{"http.schema.json#/$defs/ModelVersion", "ai_edit"},
	} {
		n := s.At(c.ref)
		props, _ := n.Value["properties"].(map[string]any)
		if props[c.member] == nil {
			t.Errorf("%s has no %s", c.ref, c.member)
		}
		for _, r := range n.Value["required"].([]any) {
			if r == c.member {
				t.Errorf("%s requires %s: an existing row would no longer match", c.ref, c.member)
			}
		}
	}
}

func enum(t *testing.T, s *contracttest.Schemas, ref string) []string {
	t.Helper()
	var out []string
	for _, v := range s.At(ref).Value["enum"].([]any) {
		out = append(out, v.(string))
	}
	return out
}

// The lists the investigation contract repeats from another contract.
func TestInvestigationCopiesMatchTheirSource(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	// A candidate's proposal status is a proposal's status.
	if got, want := enum(t, s, "investigation.schema.json#/$defs/InvestigationCandidate/properties/proposal_status"),
		enum(t, s, "proposal.schema.json#/properties/status"); !slices.Equal(got, want) {
		t.Errorf("candidate proposal_status %v, proposal status %v", got, want)
	}
	if !slices.Contains(enum(t, s, "proposal.schema.json#/properties/status"), "applied") {
		t.Error("proposal status has no applied")
	}
	// Entry A's context is a proposal request's context.
	strip := func(v map[string]any) map[string]any {
		out := map[string]any{}
		for k, x := range v {
			if k != "description" {
				out[k] = x
			}
		}
		return out
	}
	if got, want := strip(s.At("investigation.schema.json#/$defs/InvestigationFromRequest/properties/context").Value),
		strip(s.At("http.schema.json#/$defs/ProposalRequest/properties/context").Value); !reflect.DeepEqual(got, want) {
		t.Errorf("entry A's context %v, ProposalRequest's %v", got, want)
	}
	// The run settings are RunConfig's members (runs.Queue takes a
	// simulation.RunConfig), only narrower.
	s.Parity(t, reflect.TypeFor[simulation.RunConfig](), "investigation.schema.json#/$defs/InvestigationRunConfig")
	if got, want := enum(t, s, "investigation.schema.json#/$defs/InvestigationRunConfig/properties/trace_level"),
		enum(t, s, "simulation.schema.json#/$defs/RunConfig/properties/trace_level"); !slices.Equal(got, want) {
		t.Errorf("trace levels %v, RunConfig's %v", got, want)
	}
	// The comparison the AI is shown, and a candidate's, are compareSimulations'
	// codes and metrics.
	cmp := s.At("http.schema.json#/$defs/Comparison").Value["properties"].(map[string]any)
	mine := s.At("investigator.schema.json#/$defs/RunComparison").Value["properties"].(map[string]any)
	for _, k := range []string{"comparable", "incomparable_reasons", "warnings"} {
		a, b := mine[k].(map[string]any), cmp[k].(map[string]any)
		if a["type"] != b["type"] || a["uniqueItems"] != b["uniqueItems"] || a["maxItems"] != b["maxItems"] {
			t.Errorf("RunComparison.%s %v, Comparison's %v", k, a, b)
		}
		if items, ok := a["items"].(map[string]any); ok {
			if got, want := items["$ref"], "http.schema.json"+b["items"].(map[string]any)["$ref"].(string); got != want {
				t.Errorf("RunComparison.%s items %v, Comparison's %v", k, got, want)
			}
		}
	}
}

// The tools of each mode are the seven tools; finish is in both.
func TestModeToolsPartitionTheTools(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	all := enum(t, s, "investigator.schema.json#/$defs/InvestigatorTool")
	scenario := enum(t, s, "investigator.schema.json#/$defs/ScenarioModeTool")
	edit := enum(t, s, "investigator.schema.json#/$defs/EditModeTool")
	union := slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(scenario), edit...))))
	if !slices.Equal(union, slices.Sorted(slices.Values(all))) {
		t.Errorf("scenario %v + edit %v is not the tools %v", scenario, edit, all)
	}
	for _, x := range scenario {
		if slices.Contains(edit, x) != (x == "finish") {
			t.Errorf("%s: in both modes or finish in one only", x)
		}
	}
}

// Each end reason belongs to exactly one terminal status, and every
// reason has one (H-061 §10).
func TestReasonsPartitionTheEnds(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	reasons := enum(t, s, "common.schema.json#/$defs/InvestigationReason")
	rules := s.At("investigation.schema.json#/$defs/Investigation").Value["allOf"].([]any)
	byStatus := map[string][]string{}
	for _, r := range rules {
		rule := r.(map[string]any)
		cond, _ := rule["if"].(map[string]any)
		status, _ := cond["properties"].(map[string]any)["status"].(map[string]any)
		then, _ := rule["then"].(map[string]any)
		props, _ := then["properties"].(map[string]any)
		reason, _ := props["reason"].(map[string]any)
		if status == nil || reason == nil || reason["enum"] == nil {
			continue
		}
		for _, v := range reason["enum"].([]any) {
			byStatus[status["const"].(string)] = append(byStatus[status["const"].(string)], v.(string))
		}
	}
	got := append(slices.Clone(byStatus["finished"]), byStatus["stopped"]...)
	slices.Sort(got)
	if want := slices.Sorted(slices.Values(reasons)); !slices.Equal(got, want) {
		t.Errorf("finished %v + stopped %v, reasons %v", byStatus["finished"], byStatus["stopped"], reasons)
	}
}
