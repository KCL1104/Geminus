package aiapproval_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider/anthropic"
	"github.com/kcl1104/geminus/internal/config/aiapproval"
	"github.com/kcl1104/geminus/internal/secret"
)

// T-055: the owner's approval of the Meta Model API is recorded as decided:
// H-029 (1) "加上 Meta Model API": a Meta provider for Muse Spark in
// Standard mode, never the Contributor variant whose content is used for
// training; T-053's spend caps and switches (USD 100 a month); only
// synthetic data and demo images; the key in an environment variable. H-033
// (2026-10-26) resumed it after H-031's pause and added SAM 3.1 for the
// photo path, with only synthetic data, demo images and text the user typed
// sent. One key, labelled meta. H-035 (1) (2026-10-26) is the owner's
// acceptance of Meta's Terms of Service, the retention terms included.
func TestTheH029Record(t *testing.T) {
	r, ok := aiapproval.Lookup("H-029")
	if !ok {
		t.Fatal("H-029 is not recorded")
	}
	want := aiapproval.Record{
		ID:                "H-029",
		Provider:          "meta",
		Model:             "muse-spark-1.3",
		SegmentationModel: "sam-3.1",
		DataPolicy: aiapproval.DataPolicy{
			Inputs:        aiapproval.InputsSyntheticDemoImagesAndUserText,
			Roles:         []string{"modeler", "scenario", "analyst", "investigator"}, // H-038; H-060, H-063
			ResearchRoles: []string{"modeler", "scenario"},                            // H-046 (2)
			ToolRoles:     []string{"investigator"},                                   // H-060, H-063
		},
		ProposalModels:   []string{"muse-spark-1.3", "muse-wonder-1.0"}, // H-049
		PlannerModels:    []string{"muse-spark-1.3", "muse-wonder-1.0"}, // H-060, H-061 §9, H-063
		MonthlyBudgetUSD: 100,
		Credentials:      []string{"meta"},
		ApprovedBy:       "KCL1104 (repository owner)",
		ApprovedAt:       "2026-10-26",
		Source: "H-029 (1), amended by H-033 (planning/decisions.json human_decisions; planning/HACKATHON_PLAN.md); " +
			"the investigator role, its tool calls and planner models per H-060, confirmed by the owner in H-063",
		// The owner accepted Meta's retention terms in H-035 (1); H-029 (1)
		// is the Standard-mode choice, not that acceptance (D-122 (b); T-055
		// review round 1, L4).
		Retention: aiapproval.Retention{Arrangement: aiapproval.RetentionMetaStandard, Confirmation: aiapproval.Confirmation{
			By: "KCL1104 (repository owner)", At: "2026-10-26",
			Source: "H-035 (1): the owner accepted the Meta Model API Terms of Service (2026-10-09 version, checked by the coordinator), " +
				"its retention for safety, abuse and policy review (§4.1, §4.4) included; Standard Services are not trained on (§5.1); " +
				"Standard mode per H-029 (1)"}},
	}
	if !reflect.DeepEqual(r, want) {
		t.Fatalf("the H-029 record is\n%+v\nwant\n%+v", r, want)
	}
	if got := r.Gap(); got != "" {
		t.Fatalf("gap %q", got)
	}
	a, ok := aiapproval.Recorded("H-029")
	if !ok || !a.Valid() || a.ID() != "H-029" || a.Provider() != "meta" || a.Model() != "muse-spark-1.3" || a.SegmentationModel() != "sam-3.1" ||
		!a.AllowsImages() || a.MonthlyBudgetUSD() != 100 || !slices.Equal(a.Credentials(), []string{"meta"}) {
		t.Fatalf("H-029 yields %+v %v", a, ok)
	}
	// H-038: the analyst too; the importer never.
	if !a.AllowsRole("modeler") || !a.AllowsRole("scenario") || !a.AllowsRole("analyst") || a.AllowsRole("importer") {
		t.Error("roles")
	}
	// H-046 (2): the proposal roles may use Meta's web search; the analyst
	// and the importer never, nor a role the record does not name.
	if !a.AllowsResearch("modeler") || !a.AllowsResearch("scenario") || a.AllowsResearch("analyst") || a.AllowsResearch("importer") ||
		a.AllowsResearch("") || a.AllowsResearch("photo") {
		t.Error("research roles")
	}
	// H-018 covers no image and no segmentation model.
	h018, ok := aiapproval.Recorded("H-018")
	if !ok || h018.Provider() != "anthropic" || h018.AllowsImages() || h018.SegmentationModel() != "" ||
		h018.AllowsResearch("modeler") || h018.AllowsResearch("scenario") {
		t.Errorf("H-018: %+v %v", h018, ok)
	}
	var zero aiapproval.Approval
	if zero.Provider() != "" || zero.SegmentationModel() != "" || zero.AllowsImages() || zero.AllowsResearch("modeler") {
		t.Error("the zero Approval names something")
	}
	for _, id := range []string{"H-033", "H-031", "h-029", "H-029 ", "meta"} {
		if _, ok := aiapproval.Lookup(id); ok {
			t.Errorf("%q is recorded", id)
		}
	}
}

// H-049: a person chooses the model of a proposal, Muse Spark 1.3 (the
// record's model, the default) or Muse Wonder 1.0; an explanation of results
// (the analyst) stays on the record's model. A model the record does not list,
// a Contributor model among them, is never allowed, nor any model for a role
// the record does not serve. H-018 lists no choice: its one model only.
func TestH049ModelChoice(t *testing.T) {
	a, ok := aiapproval.Recorded("H-029")
	if !ok {
		t.Fatal("no H-029 approval")
	}
	if got := a.ProposalModels(); !slices.Equal(got, []string{"muse-spark-1.3", "muse-wonder-1.0"}) {
		t.Fatalf("proposal models %q", got)
	}
	got := a.ProposalModels()
	got[0] = "changed"
	if a.ProposalModels()[0] != "muse-spark-1.3" {
		t.Fatal("ProposalModels hands out the record's own list")
	}
	for _, c := range []struct {
		role, model string
		want        bool
	}{
		{"modeler", "muse-spark-1.3", true}, {"scenario", "muse-spark-1.3", true}, {"analyst", "muse-spark-1.3", true},
		{"modeler", "muse-wonder-1.0", true}, {"scenario", "muse-wonder-1.0", true},
		{"analyst", "muse-wonder-1.0", false}, // explanations stay on Muse Spark 1.3
		{"importer", "muse-spark-1.3", false}, {"importer", "muse-wonder-1.0", false},
		{"modeler", "muse-spark-1.3-contributor", false}, {"modeler", "Muse-Wonder-1.0", false}, {"modeler", "claude-opus-5-5", false},
		{"modeler", "sam-3.1", false}, {"modeler", "", false}, {"", "muse-spark-1.3", false},
	} {
		if got := a.AllowsModel(c.role, c.model); got != c.want {
			t.Errorf("H-029 allows %q for %q: %v, want %v", c.model, c.role, got, c.want)
		}
	}
	h018, ok := aiapproval.Recorded("H-018")
	if !ok {
		t.Fatal("no H-018 approval")
	}
	if h018.ProposalModels() != nil || !h018.AllowsModel("modeler", "claude-opus-5-5") || h018.AllowsModel("modeler", "muse-wonder-1.0") ||
		h018.AllowsModel("modeler", "muse-spark-1.3") || h018.AllowsModel("analyst", "claude-opus-5-5") {
		t.Errorf("H-018: %q", h018.ProposalModels())
	}
	var zero aiapproval.Approval
	if zero.ProposalModels() != nil || zero.AllowsModel("modeler", "muse-spark-1.3") {
		t.Error("the zero Approval allows a model")
	}
}

// H-060, H-061 §9 (confirmed by the owner in H-063): the investigator role,
// for synthetic data only, answers with tool calls (the only role the record
// names them for), plans on Muse Spark 1.3 (the record's model) or Muse
// Wonder 1.0 (listed so its evaluation can call it; offering it is gated
// separately, H-061 §14), and never researches. H-018 serves no investigator:
// a Claude deployment answers createInvestigation with 503.
func TestH061Planner(t *testing.T) {
	a, ok := aiapproval.Recorded("H-029")
	if !ok {
		t.Fatal("no H-029 approval")
	}
	if !a.AllowsRole("investigator") || !a.AllowsTools("investigator") || a.AllowsResearch("investigator") {
		t.Error("the investigator role")
	}
	for _, role := range []string{"modeler", "scenario", "analyst", "importer", "", "Investigator"} {
		if a.AllowsTools(role) {
			t.Errorf("tool calls for %q", role)
		}
	}
	if got := a.PlannerModels(); !slices.Equal(got, []string{"muse-spark-1.3", "muse-wonder-1.0"}) {
		t.Fatalf("planner models %q", got)
	}
	got := a.PlannerModels()
	got[0] = "changed"
	if a.PlannerModels()[0] != "muse-spark-1.3" {
		t.Fatal("PlannerModels hands out the record's own list")
	}
	for _, c := range []struct {
		model string
		want  bool
	}{
		{"muse-spark-1.3", true}, {"muse-wonder-1.0", true}, {"muse-spark-1.3-contributor", false}, {"muse-spark-1.2", false},
		{"Muse-Wonder-1.0", false}, {"claude-opus-5-5", false}, {"sam-3.1", false}, {"", false},
	} {
		if got := a.AllowsModel("investigator", c.model); got != c.want {
			t.Errorf("the planner %q: %v, want %v", c.model, got, c.want)
		}
	}
	// The planner list widens no other role: the analyst stays on Muse Spark.
	if a.AllowsModel("analyst", "muse-wonder-1.0") {
		t.Error("the planner list widened the analyst")
	}
	h018, ok := aiapproval.Recorded("H-018")
	if !ok {
		t.Fatal("no H-018 approval")
	}
	if h018.AllowsRole("investigator") || h018.AllowsTools("investigator") || h018.PlannerModels() != nil || h018.AllowsModel("investigator", "claude-opus-5-5") {
		t.Error("H-018 serves the investigator")
	}
	var zero aiapproval.Approval
	if zero.AllowsTools("investigator") || zero.PlannerModels() != nil || zero.AllowsModel("investigator", "muse-spark-1.3") {
		t.Error("the zero Approval serves the investigator")
	}
}

func h029(t *testing.T) aiapproval.Record {
	t.Helper()
	r, ok := aiapproval.Lookup("H-029")
	if !ok {
		t.Fatal("H-029 is not recorded")
	}
	return r
}

// A Meta record's gaps. Standard mode is the model ID (Meta's Model
// Designation Policy): a Contributor model, or any model this build does
// not know as a Standard one, is model_not_standard. The Console spend
// limit of H-018 has no counterpart: H-029 keeps T-053's in-code caps
// (PROPOSED D-122), so a Meta record needs no spend-limit confirmation.
func TestMetaGapReasons(t *testing.T) {
	for name, c := range map[string]struct {
		mutate func(*aiapproval.Record)
		want   aiapproval.Reason
	}{
		"no spend-limit confirmation": {func(r *aiapproval.Record) { r.SpendLimits = aiapproval.Confirmation{} }, ""},
		"no segmentation model":       {func(r *aiapproval.Record) { r.SegmentationModel = "" }, ""},
		"text only, no segmentation": {func(r *aiapproval.Record) {
			r.SegmentationModel, r.DataPolicy.Inputs = "", aiapproval.InputsSyntheticAndUserText
		}, ""},
		"muse spark 1.2":                         {func(r *aiapproval.Record) { r.Model, r.ProposalModels = "muse-spark-1.2", nil }, ""},
		"muse spark 1.2, the choice not changed": {func(r *aiapproval.Record) { r.Model = "muse-spark-1.2" }, aiapproval.ReasonIncomplete},
		"no model":                               {func(r *aiapproval.Record) { r.Model = "" }, aiapproval.ReasonIncomplete},
		"no approver":                            {func(r *aiapproval.Record) { r.ApprovedBy = "" }, aiapproval.ReasonIncomplete},
		"no source":                              {func(r *aiapproval.Record) { r.Source = "" }, aiapproval.ReasonIncomplete},
		"the Anthropic labels":                   {func(r *aiapproval.Record) { r.Credentials = []string{"org-a", "org-b"} }, aiapproval.ReasonIncomplete},
		"two keys":                               {func(r *aiapproval.Record) { r.Credentials = []string{"meta", "meta-b"} }, aiapproval.ReasonIncomplete},
		"no key":                                 {func(r *aiapproval.Record) { r.Credentials = nil }, aiapproval.ReasonIncomplete},
		"contributor":                            {func(r *aiapproval.Record) { r.Model = "muse-spark-1.3-contributor" }, aiapproval.ReasonModelNotStandard},
		"contributor 1.2":                        {func(r *aiapproval.Record) { r.Model = "muse-spark-1.2-contributor" }, aiapproval.ReasonModelNotStandard},
		"another spelling":                       {func(r *aiapproval.Record) { r.Model = "Muse-Spark-1.3" }, aiapproval.ReasonModelNotStandard},
		"a Claude model":                         {func(r *aiapproval.Record) { r.Model = "claude-opus-5-5" }, aiapproval.ReasonModelNotStandard},
		"another segmenter":                      {func(r *aiapproval.Record) { r.SegmentationModel = "sam-3" }, aiapproval.ReasonModelNotStandard},
		"segmentation, text only":                {func(r *aiapproval.Record) { r.DataPolicy.Inputs = aiapproval.InputsSyntheticAndUserText }, aiapproval.ReasonPolicyUnsupported},
		"customer data":                          {func(r *aiapproval.Record) { r.DataPolicy.Inputs = "customer_photos" }, aiapproval.ReasonPolicyUnsupported},
		"raw text in logs":                       {func(r *aiapproval.Record) { r.DataPolicy.LogsKeepRawText = true }, aiapproval.ReasonPolicyUnsupported},
		"importer role":                          {func(r *aiapproval.Record) { r.DataPolicy.Roles = []string{"modeler", "importer"} }, aiapproval.ReasonPolicyUnsupported},
		// H-046 (2): research for the proposal roles only, each a role the
		// record serves, each once; none is a complete record too.
		"no research":           {func(r *aiapproval.Record) { r.DataPolicy.ResearchRoles = nil }, ""},
		"modeler research only": {func(r *aiapproval.Record) { r.DataPolicy.ResearchRoles = []string{"modeler"} }, ""},
		"analyst research": {func(r *aiapproval.Record) { r.DataPolicy.ResearchRoles = []string{"modeler", "analyst"} },
			aiapproval.ReasonPolicyUnsupported},
		"importer research": {func(r *aiapproval.Record) { r.DataPolicy.ResearchRoles = []string{"importer"} }, aiapproval.ReasonPolicyUnsupported},
		"research for a role not served": {func(r *aiapproval.Record) {
			r.DataPolicy.Roles, r.DataPolicy.ResearchRoles = []string{"modeler"}, []string{"modeler", "scenario"}
		}, aiapproval.ReasonPolicyUnsupported},
		"research named twice": {func(r *aiapproval.Record) { r.DataPolicy.ResearchRoles = []string{"modeler", "modeler"} },
			aiapproval.ReasonPolicyUnsupported},
		"research of another role": {func(r *aiapproval.Record) { r.DataPolicy.ResearchRoles = []string{"Modeler"} }, aiapproval.ReasonPolicyUnsupported},
		// H-049: the models a person may choose, the record's model among
		// them, each a Standard one, each once; none is a complete record too
		// (every proposal on the record's model).
		"no model choice":       {func(r *aiapproval.Record) { r.ProposalModels = nil }, ""},
		"the default only":      {func(r *aiapproval.Record) { r.ProposalModels = []string{"muse-spark-1.3"} }, ""},
		"without the default":   {func(r *aiapproval.Record) { r.ProposalModels = []string{"muse-wonder-1.0"} }, aiapproval.ReasonIncomplete},
		"a model named twice":   {func(r *aiapproval.Record) { r.ProposalModels = []string{"muse-spark-1.3", "muse-spark-1.3"} }, aiapproval.ReasonIncomplete},
		"an empty model choice": {func(r *aiapproval.Record) { r.ProposalModels = []string{"muse-spark-1.3", ""} }, aiapproval.ReasonModelNotStandard},
		"a Contributor model to choose": {func(r *aiapproval.Record) {
			r.ProposalModels = []string{"muse-spark-1.3", "muse-spark-1.3-contributor"}
		}, aiapproval.ReasonModelNotStandard},
		"a Claude model to choose": {func(r *aiapproval.Record) { r.ProposalModels = []string{"muse-spark-1.3", "claude-opus-5-5"} },
			aiapproval.ReasonModelNotStandard},
		"Muse Wonder as the default": {func(r *aiapproval.Record) {
			r.Model, r.ProposalModels = "muse-wonder-1.0", []string{"muse-wonder-1.0", "muse-spark-1.3"}
		}, ""},
		"no role": {func(r *aiapproval.Record) { r.DataPolicy.Roles = nil }, aiapproval.ReasonPolicyUnsupported},
		// H-060, H-061 §9: the investigator answers with tool calls only, so
		// the role and its tool calls come together; tool calls for no other
		// role; the planner models each a Standard one, each once, and only
		// with the role. Research never.
		"no investigator": {func(r *aiapproval.Record) {
			r.DataPolicy.Roles, r.DataPolicy.ToolRoles, r.PlannerModels = []string{"modeler", "scenario", "analyst"}, nil, nil
		}, ""},
		"the investigator without tool calls": {func(r *aiapproval.Record) { r.DataPolicy.ToolRoles = nil }, aiapproval.ReasonPolicyUnsupported},
		"tool calls without the role": {func(r *aiapproval.Record) {
			r.DataPolicy.Roles = []string{"modeler", "scenario", "analyst"}
		}, aiapproval.ReasonPolicyUnsupported},
		"tool calls for a proposal role": {func(r *aiapproval.Record) { r.DataPolicy.ToolRoles = []string{"investigator", "scenario"} },
			aiapproval.ReasonPolicyUnsupported},
		"tool calls named twice": {func(r *aiapproval.Record) { r.DataPolicy.ToolRoles = []string{"investigator", "investigator"} },
			aiapproval.ReasonPolicyUnsupported},
		"investigator research": {func(r *aiapproval.Record) { r.DataPolicy.ResearchRoles = []string{"modeler", "investigator"} },
			aiapproval.ReasonPolicyUnsupported},
		"no planner choice":   {func(r *aiapproval.Record) { r.PlannerModels = nil }, ""},
		"Muse Spark planning": {func(r *aiapproval.Record) { r.PlannerModels = []string{"muse-spark-1.3"} }, ""},
		"a planner named twice": {func(r *aiapproval.Record) { r.PlannerModels = []string{"muse-spark-1.3", "muse-spark-1.3"} },
			aiapproval.ReasonIncomplete},
		"a Contributor planner": {func(r *aiapproval.Record) { r.PlannerModels = []string{"muse-spark-1.3", "muse-spark-1.3-contributor"} },
			aiapproval.ReasonModelNotStandard},
		"an empty planner": {func(r *aiapproval.Record) { r.PlannerModels = []string{""} }, aiapproval.ReasonModelNotStandard},
		"planners without the investigator": {func(r *aiapproval.Record) {
			r.DataPolicy.Roles, r.DataPolicy.ToolRoles = []string{"modeler", "scenario", "analyst"}, nil
		}, aiapproval.ReasonPolicyUnsupported},
		"no budget":                {func(r *aiapproval.Record) { r.MonthlyBudgetUSD = 0 }, aiapproval.ReasonBudgetMissing},
		"retention not confirmed":  {func(r *aiapproval.Record) { r.Retention = aiapproval.Retention{} }, aiapproval.ReasonRetentionUnconfirmed},
		"Anthropic's arrangement":  {func(r *aiapproval.Record) { r.Retention.Arrangement = aiapproval.RetentionStandard }, aiapproval.ReasonRetentionUnconfirmed},
		"retention without a date": {func(r *aiapproval.Record) { r.Retention.At = "" }, aiapproval.ReasonRetentionUnconfirmed},
	} {
		r := h029(t)
		c.mutate(&r)
		if got := r.Gap(); got != c.want {
			t.Errorf("%s: gap %q, want %q", name, got, c.want)
		}
	}
	// An Anthropic record covers no image and no segmentation model, and
	// keeps its own arrangements.
	for name, c := range map[string]struct {
		mutate func(*aiapproval.Record)
		want   aiapproval.Reason
	}{
		"images":                 {func(r *aiapproval.Record) { r.DataPolicy.Inputs = aiapproval.InputsSyntheticDemoImagesAndUserText }, aiapproval.ReasonPolicyUnsupported},
		"a segmentation model":   {func(r *aiapproval.Record) { r.SegmentationModel = "sam-3.1" }, aiapproval.ReasonIncomplete},
		"Meta's arrangement":     {func(r *aiapproval.Record) { r.Retention.Arrangement = aiapproval.RetentionMetaStandard }, aiapproval.ReasonRetentionUnconfirmed},
		"the Meta label":         {func(r *aiapproval.Record) { r.Credentials = []string{"meta"} }, aiapproval.ReasonIncomplete},
		"no spend-limit confirm": {func(r *aiapproval.Record) { r.SpendLimits = aiapproval.Confirmation{} }, aiapproval.ReasonSpendLimitsUnconfirmed},
		// H-046 (2): the Anthropic adapter has no web search, so no
		// Anthropic record may name research.
		"research": {func(r *aiapproval.Record) { r.DataPolicy.ResearchRoles = []string{"modeler"} }, aiapproval.ReasonPolicyUnsupported},
		// H-049: the Anthropic adapter asks its one model; no Anthropic
		// record lists a choice.
		"a model choice": {func(r *aiapproval.Record) { r.ProposalModels = []string{"claude-opus-5-5"} }, aiapproval.ReasonIncomplete},
		// H-061 §9: the Claude adapter sends no tools, so no Anthropic record
		// serves the investigator (a Claude deployment has no investigation).
		"the investigator": {func(r *aiapproval.Record) {
			r.DataPolicy.Roles, r.DataPolicy.ToolRoles = []string{"modeler", "scenario", "investigator"}, []string{"investigator"}
		}, aiapproval.ReasonPolicyUnsupported},
		"tool calls":       {func(r *aiapproval.Record) { r.DataPolicy.ToolRoles = []string{"investigator"} }, aiapproval.ReasonPolicyUnsupported},
		"a planner choice": {func(r *aiapproval.Record) { r.PlannerModels = []string{"claude-opus-5-5"} }, aiapproval.ReasonIncomplete},
	} {
		r := complete(t)
		c.mutate(&r)
		if got := r.Gap(); got != c.want {
			t.Errorf("H-018 with %s: gap %q, want %q", name, got, c.want)
		}
	}
	// A record of a provider this build has no adapter for is incomplete.
	r := h029(t)
	r.Provider = "openai"
	if got := r.Gap(); got != aiapproval.ReasonIncomplete {
		t.Errorf("another provider: %q", got)
	}
}

// The Anthropic adapter takes no Meta approval, whatever else its
// configuration says: the approval is checked first, for its provider.
func TestTheAnthropicAdapterTakesNoMetaApproval(t *testing.T) {
	a, ok := aiapproval.Recorded("H-029")
	if !ok {
		t.Fatal("no H-029 approval")
	}
	cfg := anthropic.Config{Approval: a, Model: "muse-spark-1.3", Credentials: []anthropic.Credential{{Label: "meta", Key: secret.Secret("LLM_000000_test-not-a-key")}}}
	if _, err := anthropic.New(cfg); !errors.Is(err, anthropic.ErrNotApproved) {
		t.Errorf("a Meta approval: %v", err)
	}
}
