package contracttest_test

// H-061 A2 (PROPOSED): the investigation contract, checked from Go. The
// Python checker (scripts/check_contracts.py) validates the fixtures with
// the reference jsonschema library; the API's executor and every response
// test validate with internal/model/validate (santhosh-tekuri/jsonschema,
// Go's RE2 for patterns). These tests hold the two to the same verdicts on
// the H-061 fixtures, and pin the places where the investigation contract
// repeats another contract's list, so the copies cannot drift.

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/httpapi"
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
	// Edit mode's closed member list is entry A's but the scenario members,
	// so an edit with run settings is refused like an unknown member (review
	// round 1) and a member added to entry A is not refused in edit mode.
	entryA := s.At("investigation.schema.json#/$defs/InvestigationFromRequest").Value
	var editMembers []string
	for _, r := range entryA["allOf"].([]any) {
		rule := r.(map[string]any)
		if rule["if"].(map[string]any)["properties"].(map[string]any)["mode"].(map[string]any)["const"] != "edit" {
			continue
		}
		then := rule["then"].(map[string]any)
		if then["additionalProperties"] != false {
			t.Error("edit mode's members are not closed")
		}
		for k, v := range then["properties"].(map[string]any) {
			if v != true {
				t.Errorf("edit mode narrows %s; entry A's own schema should", k)
			}
			editMembers = append(editMembers, k)
		}
	}
	var scenarioless []string
	for k := range entryA["properties"].(map[string]any) {
		if !slices.Contains([]string{"run_config", "baseline_name", "objective"}, k) {
			scenarioless = append(scenarioless, k)
		}
	}
	if got, want := slices.Sorted(slices.Values(editMembers)), slices.Sorted(slices.Values(scenarioless)); !slices.Equal(got, want) {
		t.Errorf("edit mode's members %v, entry A's without the scenario members %v", got, want)
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

// requestIssues is every issue the Go validator reports for each request
// negative, in its sorted order: what httpapi.DecodeJSON sends as the 422's
// details, one per issue. A body is checked as one entry only (a body with
// from_analysis_id is entry B, any other entry A), so no issue comes from the
// entry the person did not use (H-061 A2 review round 1: a oneOf without a
// discriminator reported both entries' failures, at the root). A missing or
// unknown member is at its object, named in the reason.
var requestIssues = map[string][][2]string{
	"invalid-investigation-request-replications":                {{"/run_config/replications", "maximum: got 11, want 10"}},
	"invalid-investigation-request-horizon":                     {{"/run_config/horizon/value", "maximum: got 29, want 28"}},
	"invalid-investigation-request-horizon-minutes":             {{"/run_config/horizon/value", "maximum: got 40,321, want 40,320"}},
	"invalid-investigation-request-trace-full":                  {{"/run_config/trace_level", "value must be one of 'none', 'sampled'"}},
	"invalid-investigation-request-planner":                     {{"/planner_model", "value must be one of 'muse-spark-1.3', 'muse-wonder-1.0'"}},
	"invalid-investigation-request-scenario-without-run-config": {{"", "missing required members: baseline_name, run_config"}},
	"invalid-investigation-request-edit-with-run-config":        {{"", "members not allowed: baseline_name, run_config"}},
	"invalid-investigation-request-limits":                      {{"", "members not allowed: caps"}},
	"invalid-investigation-request-advance-step":                {{"", "members not allowed: expected_step"}},
	"invalid-investigation-request-both-entries": {
		{"", "members not allowed: base_version_id, baseline_name, intent, mode, research, run_config"}},
	"invalid-investigation-request-from-analysis-locale":  {{"", "missing required members: output_locale"}},
	"invalid-investigation-request-from-analysis-planner": {{"/planner_model", "value must be one of 'muse-spark-1.3', 'muse-wonder-1.0'"}},
	"invalid-investigation-request-empty":                 {{"", "missing required members: base_version_id, intent, mode"}},
	"invalid-investigation-stop-request-reason":           {{"", "members not allowed: reason"}},
}

func isRequestBody(c fixtureCase) bool {
	return c.Schema == "schemas/investigation.schema.json#/$defs/InvestigationRequest" ||
		c.Schema == "schemas/investigation.schema.json#/$defs/StopInvestigation"
}

// Each request negative gets exactly its issues, and cases.json names
// exactly their paths, so a negative shows the member it fails at.
func TestRequestNegativesNameTheirMember(t *testing.T) {
	root := contracttest.Root(t)
	seen := map[string]bool{}
	for _, c := range fixtureCases(t) {
		if c.Layer != "schema" || !isRequestBody(c) || c.Valid {
			continue
		}
		want, ok := requestIssues[c.ID]
		if !ok {
			t.Errorf("%s: a request negative without its exact issues here", c.ID)
			continue
		}
		seen[c.ID] = true
		issues, err := validate.Document(contracttest.ReadFile(t, filepath.Join(root, c.Instance)), strings.TrimPrefix(c.Schema, "schemas/"), "SCHEMA")
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		var got [][2]string
		var paths []string
		for _, i := range issues {
			got = append(got, [2]string{i.Path, i.Message})
			paths = append(paths, i.Path)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: issues %q, want %q", c.ID, got, want)
		}
		if exp := slices.Compact(slices.Sorted(slices.Values(c.ExpectedErrorPaths))); !slices.Equal(exp, slices.Compact(paths)) {
			t.Errorf("%s: cases.json expects paths %q, the issues are at %q", c.ID, c.ExpectedErrorPaths, paths)
		}
	}
	for id := range requestIssues {
		if !seen[id] {
			t.Errorf("%s: not a request negative in cases.json", id)
		}
	}
}

// openAPIExample returns the lines of the named example under an operation,
// from openapi.yaml's text (the Go module has no YAML parser; the Python
// checker validates each example against its schema).
func openAPIExample(t *testing.T, operationID, name string) string {
	t.Helper()
	spec := string(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "openapi", "openapi.yaml")))
	_, op, ok := strings.Cut(spec, "operationId: "+operationID+"\n")
	if !ok {
		t.Fatalf("openapi.yaml has no %s", operationID)
	}
	if end := strings.Index(op, "operationId: "); end >= 0 {
		op = op[:end]
	}
	lines := strings.Split(op, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != name+":" {
			continue
		}
		indent := len(l) - len(strings.TrimLeft(l, " "))
		var out []string
		for _, m := range lines[i+1:] {
			if strings.TrimSpace(m) != "" && len(m)-len(strings.TrimLeft(m, " ")) <= indent {
				break
			}
			out = append(out, strings.TrimSpace(m))
		}
		return strings.Join(out, "\n")
	}
	t.Fatalf("%s has no example %s", operationID, name)
	return ""
}

// yamlScalar is how openapi.yaml spells a plain string: single-quoted when
// it holds ": " or starts with a quote.
func yamlScalar(s string) string {
	if strings.Contains(s, ": ") || strings.HasPrefix(s, "'") {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return s
}

// createInvestigation's too_many_replications example is the 422 the API
// sends for that body: httpapi.DecodeJSON's message and its one detail.
func TestTooManyReplicationsExampleIsTheServers(t *testing.T) {
	body := contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "contracts", "invalid-investigation-request-replications.json"))
	r := httptest.NewRequest("POST", "/v1/models/mdl_01M4YDQK004AGVNEGJQJ1NMM0N/investigations", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	var dst map[string]any
	_, err := httpapi.DecodeJSON(r, httpapi.MaxBodyBytes, "investigation.schema.json#/$defs/InvestigationRequest", &dst)
	var he *httpapi.Error
	if !errors.As(err, &he) || he.Code != httpapi.RequestValidationFailed {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if len(he.Details) != 1 || he.Details[0].Path != "/run_config/replications" {
		t.Fatalf("details %+v, want one at /run_config/replications", he.Details)
	}
	d := he.Details[0]
	example := openAPIExample(t, "createInvestigation", "too_many_replications")
	want := []string{"code: " + string(he.Code), "message: " + yamlScalar(he.Message), "- path: " + d.Path,
		"reason: " + yamlScalar(d.Reason), "severity: " + d.Severity}
	if d.Code != "" {
		want = append(want, "code: "+d.Code)
	}
	for _, line := range want {
		if !slices.Contains(strings.Split(example, "\n"), line) {
			t.Errorf("the too_many_replications example has no %q:\n%s", line, example)
		}
	}
	if n := strings.Count(example, "- path: "); n != 1 {
		t.Errorf("the too_many_replications example has %d details, the server sends 1", n)
	}
}

type ledgerStep struct {
	Seq         int     `json:"seq"`
	Status      string  `json:"status"`
	RequestedAt string  `json:"requested_at"`
	ResolvedAt  *string `json:"resolved_at"`
	ObservedAt  *string `json:"observed_at"`
}

type ledgerView struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Activity struct {
		State string `json:"state"`
	} `json:"activity"`
	Used struct {
		Steps int `json:"steps"`
	} `json:"used"`
	Steps []ledgerStep `json:"steps"`
}

// reachable reports how inv breaks the ledger's own rules, which the schema
// cannot state: rows in seq order, a row inserted only when every earlier
// row is resolved and observed (H-061 §11, the A3 trigger), times in order,
// used.steps counted from the rows, and a running investigation's activity
// read from its last row (§5.5, §5.6).
func reachable(inv ledgerView) []string {
	var out []string
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			out = append(out, "time "+s)
		}
		return v
	}
	for i, s := range inv.Steps {
		if s.Seq != i+1 {
			out = append(out, "row "+s.RequestedAt+" is not seq i+1")
		}
		if (s.Status == "pending") != (s.ResolvedAt == nil) {
			out = append(out, "a row is pending exactly while it is unresolved")
		}
		if s.ObservedAt != nil && (s.ResolvedAt == nil || at(*s.ObservedAt).Before(at(*s.ResolvedAt))) {
			out = append(out, "an observation before its row is resolved")
		}
		if s.ResolvedAt != nil && at(*s.ResolvedAt).Before(at(s.RequestedAt)) {
			out = append(out, "a row resolved before it was asked")
		}
		if i == len(inv.Steps)-1 {
			break
		}
		if s.ResolvedAt == nil || s.ObservedAt == nil {
			out = append(out, "a row follows one not yet resolved and observed")
		} else if at(inv.Steps[i+1].RequestedAt).Before(at(*s.ObservedAt)) {
			out = append(out, "a row asked before the one before it was observed")
		}
	}
	if inv.Used.Steps != len(inv.Steps) {
		out = append(out, "used.steps is not the number of rows")
	}
	if inv.Status == "running" {
		want := []string{"queued", "planning"}
		if n := len(inv.Steps); n > 0 {
			switch last := inv.Steps[n-1]; {
			case last.Status == "pending":
				want = []string{"carrying_out"}
			case last.ObservedAt == nil:
				want = []string{"waiting"}
			default:
				want = []string{"planning"}
			}
		}
		if !slices.Contains(want, inv.Activity.State) {
			out = append(out, "activity "+inv.Activity.State+", the rows say "+strings.Join(want, " or "))
		}
	}
	return out
}

// Every valid investigation fixture is a state the ledger can reach, so
// A7's Get and A8's page are built against states that can happen (H-061 A2
// review round 1). The OpenAPI examples are copies of these fixtures
// (scripts/check_contracts.py holds them equal).
func TestInvestigationFixturesAreReachable(t *testing.T) {
	root := contracttest.Root(t)
	var n int
	for _, c := range fixtureCases(t) {
		if c.Layer != "schema" || !c.Valid {
			continue
		}
		var invs []ledgerView
		data := contracttest.ReadFile(t, filepath.Join(root, c.Instance))
		switch c.Schema {
		case "schemas/investigation.schema.json#/$defs/Investigation":
			var v ledgerView
			if err := json.Unmarshal(data, &v, json.RejectUnknownMembers(false)); err != nil {
				t.Fatalf("%s: %v", c.ID, err)
			}
			invs = append(invs, v)
		case "schemas/investigation.schema.json#/$defs/Investigations":
			var page struct {
				Items []ledgerView `json:"items"`
			}
			if err := json.Unmarshal(data, &page, json.RejectUnknownMembers(false)); err != nil {
				t.Fatalf("%s: %v", c.ID, err)
			}
			invs = append(invs, page.Items...)
		default:
			continue
		}
		for _, inv := range invs {
			n++
			for _, p := range reachable(inv) {
				t.Errorf("%s (%s): %s", c.ID, inv.ID, p)
			}
		}
	}
	if n < 5 {
		t.Fatalf("only %d investigations checked", n)
	}
}

// The rules above refuse what they are written to refuse.
func TestReachableRefuses(t *testing.T) {
	ts := func(s string) *string { return &s }
	at := func(seq int, sec int) string { return fmt.Sprintf("2026-11-01T06:%02d:%02dZ", seq, sec) }
	done := func(seq int, observed bool) ledgerStep {
		s := ledgerStep{Seq: seq, Status: "done", RequestedAt: at(seq, 5), ResolvedAt: ts(at(seq, 6))}
		if observed {
			s.ObservedAt = ts(at(seq, 30))
		}
		return s
	}
	pending := func(seq int) ledgerStep { return ledgerStep{Seq: seq, Status: "pending", RequestedAt: at(seq, 5)} }
	view := func(status, activity string, steps ...ledgerStep) ledgerView {
		v := ledgerView{Status: status, Steps: steps}
		v.Activity.State, v.Used.Steps = activity, len(steps)
		return v
	}
	early := done(2, false)
	early.RequestedAt = at(1, 20) // before row 1's observation at 06:01:30
	for _, c := range []struct {
		name string
		inv  ledgerView
		ok   bool
	}{
		{"waiting on the last row", view("running", "waiting", done(1, true), done(2, false)), true},
		{"carrying out the last row", view("running", "carrying_out", done(1, true), pending(2)), true},
		{"planning after an observed row", view("running", "planning", done(1, true)), true},
		{"queued with no row", view("running", "queued"), true},
		{"stopped while waiting", view("stopped", "ended", done(1, true), done(2, false)), true},
		{"a row after an unobserved row (the review's case)", view("running", "carrying_out", done(1, false), pending(2)), false},
		{"a row after a pending row", view("stopped", "ended", pending(1), pending(2)), false},
		{"a row asked before the one before it was observed", view("running", "waiting", done(1, true), early), false},
		{"waiting while a row is pending", view("running", "waiting", done(1, true), pending(2)), false},
		{"planning while a row waits", view("running", "planning", done(1, true), done(2, false)), false},
		{"a seq gap", view("running", "planning", done(1, true), done(3, true)), false},
		{"used.steps off by one", func() ledgerView { v := view("running", "planning", done(1, true)); v.Used.Steps = 2; return v }(), false},
		{"a pending row resolved", view("running", "carrying_out", ledgerStep{Seq: 1, Status: "pending", RequestedAt: at(1, 5), ResolvedAt: ts(at(1, 6))}), false},
		{"observed before resolved", view("running", "planning", ledgerStep{Seq: 1, Status: "done", RequestedAt: at(1, 5), ResolvedAt: ts(at(1, 6)), ObservedAt: ts(at(1, 5))}), false},
		{"resolved before asked", view("running", "planning", ledgerStep{Seq: 1, Status: "done", RequestedAt: at(1, 5), ResolvedAt: ts(at(1, 4)), ObservedAt: ts(at(1, 30))}), false},
	} {
		if got := reachable(c.inv); (len(got) == 0) != c.ok {
			t.Errorf("%s: %q", c.name, got)
		}
	}
}
