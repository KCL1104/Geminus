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
			Inputs: aiapproval.InputsSyntheticDemoImagesAndUserText,
			Roles:  []string{"modeler", "scenario", "analyst"}, // H-038
		},
		MonthlyBudgetUSD: 100,
		Credentials:      []string{"meta"},
		ApprovedBy:       "KCL1104 (repository owner)",
		ApprovedAt:       "2026-10-26",
		Source:           "H-029 (1), amended by H-033 (planning/decisions.json human_decisions; planning/HACKATHON_PLAN.md)",
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
	// H-018 covers no image and no segmentation model.
	h018, ok := aiapproval.Recorded("H-018")
	if !ok || h018.Provider() != "anthropic" || h018.AllowsImages() || h018.SegmentationModel() != "" {
		t.Errorf("H-018: %+v %v", h018, ok)
	}
	var zero aiapproval.Approval
	if zero.Provider() != "" || zero.SegmentationModel() != "" || zero.AllowsImages() {
		t.Error("the zero Approval names something")
	}
	for _, id := range []string{"H-033", "H-031", "h-029", "H-029 ", "meta"} {
		if _, ok := aiapproval.Lookup(id); ok {
			t.Errorf("%q is recorded", id)
		}
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
		"muse spark 1.2":           {func(r *aiapproval.Record) { r.Model = "muse-spark-1.2" }, ""},
		"no model":                 {func(r *aiapproval.Record) { r.Model = "" }, aiapproval.ReasonIncomplete},
		"no approver":              {func(r *aiapproval.Record) { r.ApprovedBy = "" }, aiapproval.ReasonIncomplete},
		"no source":                {func(r *aiapproval.Record) { r.Source = "" }, aiapproval.ReasonIncomplete},
		"the Anthropic labels":     {func(r *aiapproval.Record) { r.Credentials = []string{"org-a", "org-b"} }, aiapproval.ReasonIncomplete},
		"two keys":                 {func(r *aiapproval.Record) { r.Credentials = []string{"meta", "meta-b"} }, aiapproval.ReasonIncomplete},
		"no key":                   {func(r *aiapproval.Record) { r.Credentials = nil }, aiapproval.ReasonIncomplete},
		"contributor":              {func(r *aiapproval.Record) { r.Model = "muse-spark-1.3-contributor" }, aiapproval.ReasonModelNotStandard},
		"contributor 1.2":          {func(r *aiapproval.Record) { r.Model = "muse-spark-1.2-contributor" }, aiapproval.ReasonModelNotStandard},
		"another spelling":         {func(r *aiapproval.Record) { r.Model = "Muse-Spark-1.3" }, aiapproval.ReasonModelNotStandard},
		"a Claude model":           {func(r *aiapproval.Record) { r.Model = "claude-opus-5-5" }, aiapproval.ReasonModelNotStandard},
		"another segmenter":        {func(r *aiapproval.Record) { r.SegmentationModel = "sam-3" }, aiapproval.ReasonModelNotStandard},
		"segmentation, text only":  {func(r *aiapproval.Record) { r.DataPolicy.Inputs = aiapproval.InputsSyntheticAndUserText }, aiapproval.ReasonPolicyUnsupported},
		"customer data":            {func(r *aiapproval.Record) { r.DataPolicy.Inputs = "customer_photos" }, aiapproval.ReasonPolicyUnsupported},
		"raw text in logs":         {func(r *aiapproval.Record) { r.DataPolicy.LogsKeepRawText = true }, aiapproval.ReasonPolicyUnsupported},
		"importer role":            {func(r *aiapproval.Record) { r.DataPolicy.Roles = []string{"modeler", "importer"} }, aiapproval.ReasonPolicyUnsupported},
		"no role":                  {func(r *aiapproval.Record) { r.DataPolicy.Roles = nil }, aiapproval.ReasonPolicyUnsupported},
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
