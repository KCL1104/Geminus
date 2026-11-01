package investigations

import (
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/results/compare"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	store "github.com/kcl1104/geminus/internal/storage/investigations"
	"github.com/kcl1104/geminus/prompts"
)

// The version stored with each new investigation is the planner's
// template's (prompts/investigator, its tool descriptions included), so a
// template change cannot leave it behind (H-061 A9: investigator-0.4.0).
func TestPromptTemplateVersionIsTheTemplates(t *testing.T) {
	_, v, err := prompts.Template(string(investigator.Role))
	if err != nil || v != PromptTemplateVersion || PromptTemplateVersion != "investigator-0.4.0" {
		t.Errorf("stored %q, the template's %q (%v)", PromptTemplateVersion, v, err)
	}
}

// The investigation routes are the contract's: method, path and roles
// (planning/api-operations.json, from openapi.yaml).
func TestRoutesMatchContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "planning", "api-operations.json"))
	if err != nil {
		t.Fatal(err)
	}
	type operation struct {
		Method      string   `json:"method"`
		Path        string   `json:"path"`
		OperationID string   `json:"operation_id"`
		Roles       []string `json:"roles"`
	}
	var doc struct {
		Operations []operation `json:"operations"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range Routes {
		got = append(got, r.OperationID)
		i := slices.IndexFunc(doc.Operations, func(o operation) bool { return o.OperationID == r.OperationID })
		if i < 0 {
			t.Errorf("%s is not a contract operation", r.OperationID)
			continue
		}
		o := doc.Operations[i]
		var roles []string
		for _, x := range r.Roles {
			roles = append(roles, string(x))
		}
		if o.Method != r.Method || o.Path != r.Path || !slices.Equal(slices.Sorted(slices.Values(o.Roles)), slices.Sorted(slices.Values(roles))) {
			t.Errorf("%s: route %s %s %v, contract %s %s %v", r.OperationID, r.Method, r.Path, roles, o.Method, o.Path, o.Roles)
		}
	}
	if want := []string{"createInvestigation", "listInvestigations", "getInvestigation", "stopInvestigation"}; !slices.Equal(got, want) {
		t.Errorf("routes %v, want %v", got, want)
	}
}

// Each service error has the contract's code, and the operation-specific
// ones their detail code and place (openapi.yaml createInvestigation and
// stopInvestigation).
func TestRefused(t *testing.T) {
	for _, c := range []struct {
		err    error
		code   httpapi.Code
		detail string
		path   string
	}{
		{ErrOff, httpapi.ServiceUnavailable, CodeOff, ""},
		{ErrNotFound, httpapi.NotFound, "", ""},
		{&StaleBaseError{Path: "/base_version_id"}, httpapi.ModelVersionConflict, "", "/base_version_id"},
		{&StaleBaseError{Path: "/from_analysis_id"}, httpapi.ModelVersionConflict, "", "/from_analysis_id"},
		{ErrRunning, httpapi.LifecycleConflict, CodeRunning, ""},
		{ErrEnded, httpapi.LifecycleConflict, "", ""},
		{ErrNotTheRequester, httpapi.Forbidden, "", ""},
		{ErrInvalidCursor, httpapi.InvalidRequest, "", ""},
		{ErrInvalidLimit, httpapi.InvalidRequest, "", ""},
		{&DailyLimitError{RetryAfter: 60}, httpapi.RateLimited, CodeDailyLimit, ""},
		{&NotApprovedError{}, httpapi.AIPolicyNotApproved, CodeRoleNotApproved, ""},
		{&NotApprovedError{Planner: true}, httpapi.AIPolicyNotApproved, CodePlannerModelNotApprov, "/planner_model"},
		{&RequestError{Issues: []model.ValidationIssue{model.NewIssue(CodePlannerNotOffered, "/planner_model", "x", model.SeverityError)}},
			httpapi.RequestValidationFailed, CodePlannerNotOffered, "/planner_model"},
		{&RequestError{Issues: []model.ValidationIssue{model.NewIssue(CodeResearchOverCeiling, "/research", "x", model.SeverityError)}},
			httpapi.RequestValidationFailed, CodeResearchOverCeiling, "/research"},
		{&proposals.UnavailableError{Availability: proposals.NotConfigured}, httpapi.AIProviderNotConfigured, "", ""},
		{&proposals.UnavailableError{Availability: proposals.PolicyNotApproved}, httpapi.AIPolicyNotApproved, "", ""},
		{&proposals.UnavailableError{Availability: proposals.PolicyNotApproved, Model: true}, httpapi.AIPolicyNotApproved, proposals.CodeModelNotApproved, "/model"},
		{idempotency.ErrConflict, httpapi.IdempotencyKeyConflict, "", ""},
	} {
		var he *httpapi.Error
		if !errors.As(Refused(c.err), &he) || he.Code != c.code {
			t.Errorf("%v: %v, want %s", c.err, Refused(c.err), c.code)
			continue
		}
		if c.detail != "" && (len(he.Details) != 1 || he.Details[0].Code != c.detail || he.Details[0].Path != c.path) {
			t.Errorf("%v: details %+v, want %s at %q", c.err, he.Details, c.detail, c.path)
		}
		if c.detail == "" && c.path != "" && (len(he.Details) != 1 || he.Details[0].Path != c.path) {
			t.Errorf("%v: details %+v, want the path %q", c.err, he.Details, c.path)
		}
	}
	other := errors.New("a database fault")
	if Refused(other) != other {
		t.Error("an unexpected error is mapped")
	}
}

// H-061 §10: research that cannot fit the investigation's USD 2.50 is
// refused at create. Muse Spark 1.3's research reserves USD 1.60672 (its
// context window at its input price, 32,000 output tokens, 64 searches);
// Muse Wonder 1.0's more than USD 2.50 on its input bound alone.
func TestResearchReservation(t *testing.T) {
	spark, ok := ResearchReservation("muse-spark-1.3")
	if !ok || spark != 1_606_720_000 || spark > Ceiling {
		t.Errorf("Muse Spark 1.3: %d %v", spark, ok)
	}
	wonder, ok := ResearchReservation("muse-wonder-1.0")
	// Since H-065 its output bound is 128,000 tokens: USD 6.914304.
	if !ok || wonder != 6_914_304_000 || wonder <= Ceiling {
		t.Errorf("Muse Wonder 1.0: %d %v (must exceed the ceiling)", wonder, ok)
	}
	if _, ok := ResearchReservation("fake"); ok {
		t.Error("a model without a price is priced")
	}
}

func f(v float64) *float64 { return &v }

func metric(path string, b, s float64) *compare.Metric {
	abs, rel := s-b, (s-b)/b
	return &compare.Metric{MetricPath: path, Baseline: f(b), Scenario: f(s), AbsoluteDelta: &abs, RelativeDelta: &rel, Status: compare.Available}
}

func candidate(i int, m *compare.Metric, comparable bool, ops int) Candidate {
	c := Candidate{Index: i, operations: ops}
	if m != nil || !comparable {
		c.Comparison = &investigator.RunComparison{Comparable: comparable, Objective: m}
	}
	return c
}

// The server's ranking (H-056 §9): the comparable what-ifs that improved
// the objective in its fixed direction, by relative change, then fewer
// operations, then index; none named when none improved. The closing line
// counts the comparable pairs and carries the best's figures only.
func TestRankingAndClosingLine(t *testing.T) {
	const p50 = "/metrics/lead_time_minutes/p50"
	lower := &Objective{MetricPath: p50, Better: "lower"}
	cs := []Candidate{
		candidate(1, metric(p50, 40, 38), true, 1), // 5% lower
		candidate(2, metric(p50, 40, 36), true, 3), // 10% lower
		candidate(3, metric(p50, 40, 36), true, 2), // 10% lower, fewer operations than 2
	}
	b := best(cs, lower)
	if b == nil || b.Index != 3 {
		t.Fatalf("best %+v, want what-if 3", b)
	}
	line := closingLine(store.Scenario, p50, cs, b)
	if line.Key != Improved || line.CandidateIndex == nil || *line.CandidateIndex != 3 || line.Compared != 3 || line.MetricPath == nil ||
		*line.MetricPath != p50 || line.AbsoluteDelta == nil || *line.AbsoluteDelta != -4 || line.DraftRevision != nil {
		t.Errorf("closing line %+v", line)
	}
	// A pair that is not comparable is never ranked, and one not run yet
	// is not counted.
	mixed := []Candidate{candidate(1, metric(p50, 40, 30), false, 1), candidate(2, metric(p50, 40, 38), true, 5), {Index: 3}}
	if b := best(mixed, lower); b == nil || b.Index != 2 {
		t.Errorf("best of the mixed %+v", b)
	}
	if line := closingLine(store.Scenario, p50, mixed, best(mixed, lower)); line.Compared != 1 || line.Key != Improved {
		t.Errorf("mixed closing line %+v", line)
	}
	// Higher is better for throughput: a lower throughput never wins.
	higher := &Objective{MetricPath: "/metrics/throughput/mean", Better: "higher"}
	worse := []Candidate{candidate(1, metric("/metrics/throughput/mean", 100, 90), true, 1)}
	if b := best(worse, higher); b != nil {
		t.Errorf("a worse throughput is best: %+v", b)
	}
	if line := closingLine(store.Scenario, "/metrics/throughput/mean", worse, nil); line.Key != NoImprovement || line.Compared != 1 ||
		line.CandidateIndex != nil || line.AbsoluteDelta != nil {
		t.Errorf("no improvement: %+v", line)
	}
	// Nothing comparable: not compared.
	if line := closingLine(store.Scenario, p50, []Candidate{candidate(1, nil, false, 1)}, nil); line.Key != NotCompared || line.Compared != 0 {
		t.Errorf("not compared: %+v", line)
	}
	// Edit mode: whether the AI's edit was written into the draft.
	rev := int64(4)
	if line := closingLine(store.Edit, "", []Candidate{{Index: 1, AppliedDraftRevision: &rev}}, nil); line.Key != DraftWritten ||
		line.DraftRevision == nil || *line.DraftRevision != 4 {
		t.Errorf("draft written: %+v", line)
	}
	if line := closingLine(store.Edit, "", []Candidate{{Index: 1}}, nil); line.Key != DraftNotWritten || line.DraftRevision != nil {
		t.Errorf("draft not written: %+v", line)
	}
	// Every closing line is the contract's.
	for _, l := range []ClosingLine{closingLine(store.Scenario, p50, cs, b), closingLine(store.Scenario, p50, nil, nil),
		closingLine(store.Edit, "", []Candidate{{Index: 1, AppliedDraftRevision: &rev}}, nil)} {
		data, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		if issues, err := validate.Document(data, "investigation.schema.json#/$defs/InvestigationClosingLine", "RESPONSE"); err != nil || len(issues) > 0 {
			t.Errorf("%s: %v %v", data, issues, err)
		}
	}
}

// The list's cursor is bound to the organization, the model and the list.
func TestCursor(t *testing.T) {
	pos := store.Position{CreatedAt: time.Date(2026, 11, 1, 6, 0, 0, 123, time.UTC), ID: "inv_01M4YDQK00B8S5WQXJ3TQ7ZK2A"}
	c := encodeCursor("org_01M4YDQK00Y5H2929M9WWZWTVH", "mdl_01M4YDQK004AGVNEGJQJ1NMM0N", pos)
	got, err := decodeCursor(c, "org_01M4YDQK00Y5H2929M9WWZWTVH", "mdl_01M4YDQK004AGVNEGJQJ1NMM0N")
	if err != nil || !got.CreatedAt.Equal(pos.CreatedAt) || got.ID != pos.ID {
		t.Errorf("%+v %v", got, err)
	}
	for _, bad := range []struct{ c, org, model string }{
		{c, "org_01M4YDQK00Y5H2929M9WWZWTVJ", "mdl_01M4YDQK004AGVNEGJQJ1NMM0N"},
		{c, "org_01M4YDQK00Y5H2929M9WWZWTVH", "mdl_01M4YDQK004AGVNEGJQJ1NMM0P"},
		{"not-base64!", "org_01M4YDQK00Y5H2929M9WWZWTVH", "mdl_01M4YDQK004AGVNEGJQJ1NMM0N"},
	} {
		if _, err := decodeCursor(bad.c, bad.org, bad.model); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

// The seconds to the next UTC day, for Retry-After.
func TestUntilTomorrow(t *testing.T) {
	if got := untilTomorrow(time.Date(2026, 11, 1, 23, 59, 30, 0, time.UTC)); got != 30 {
		t.Errorf("%d", got)
	}
	if got := untilTomorrow(time.Date(2026, 11, 1, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))); got != 8*3600 {
		t.Errorf("%d", got)
	}
}
