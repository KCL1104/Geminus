// Package aiapproval holds the owner's recorded approvals of live AI and
// the token that proves one (T-024 review L5; H-001, H-017, H-018, D-018;
// T-053).
//
// A Record is the owner's decision as written down: the provider and the
// exact model, the data policy, the monthly budget, the credentials it
// covers, who approved it, when and where it is recorded, and the owner's
// confirmations live AI waits for (the Console spend limits of both
// provider organizations; that both organizations keep the same data
// retention arrangement). Record.Gap names what a record still lacks.
//
// An Approval can only be made by Recorded, from a record this build
// holds that lacks nothing; its zero value is no approval, and Valid
// checks every time that exactly that record is still held and complete.
// internal/config reads AI_POLICY_APPROVAL through Recorded, and the live
// adapters (internal/ai/provider/anthropic, and since T-055
// internal/ai/provider/meta) accept nothing but a valid Approval of their
// own provider for its own model, so no code path reaches a live provider
// with an approval the build does not hold in full, whatever string it
// holds. The records: H-018 (Anthropic, T-053) and H-029 amended by H-033
// (the Meta Model API, T-055); each provider has its own rules (Gap).
//
// The package is a leaf: it imports nothing of this module, so the adapter
// may depend on it without depending on the rest of internal/config
// (architecture rule ai-suggests-only).
package aiapproval

import (
	"reflect"
	"slices"
	"time"
)

// Reason says what a record lacks before it can turn live AI on.
type Reason string

const (
	// ReasonIncomplete: the record does not name a provider this build has
	// an adapter for (anthropic, meta), the model, the provider's
	// credentials (anthropic: org-a and org-b; meta: meta), the approver,
	// the date or the source.
	ReasonIncomplete Reason = "record_incomplete"
	// ReasonPolicyUnsupported: the data policy is not one this build
	// enforces (synthetic data and user-typed text only, for the proposal
	// roles only, no raw prompt or response in logs).
	ReasonPolicyUnsupported Reason = "policy_unsupported"
	// ReasonBudgetMissing: no positive monthly budget.
	ReasonBudgetMissing Reason = "budget_missing"
	// ReasonSpendLimitsUnconfirmed: the owner has not confirmed the
	// Console spend limits of both provider organizations.
	ReasonSpendLimitsUnconfirmed Reason = "spend_limits_unconfirmed"
	// ReasonRetentionUnconfirmed: the owner has not confirmed which data
	// retention arrangement both provider organizations have, and that it
	// is the same for both (H-018, D-018; PROPOSED D-113).
	ReasonRetentionUnconfirmed Reason = "retention_unconfirmed"
	// ReasonModelNotStandard: a Meta record names a model, or a
	// segmentation model, that is not one of the Standard Models this build
	// knows (metaStandardModels): a Contributor model's content may be used
	// to train Meta's models, and H-029 chose Standard mode (T-055).
	ReasonModelNotStandard Reason = "model_not_standard"
)

// InputsSyntheticAndUserText is H-018's data policy for the demonstration
// period: the provider receives synthetic data and text the user typed,
// nothing else.
const InputsSyntheticAndUserText = "synthetic_and_user_typed_text"

// InputsSyntheticDemoImagesAndUserText is H-029's and H-033's data policy
// (T-055): the provider receives synthetic data, demo images and text the
// user typed, nothing else. Only a Meta record names it: an approval with
// it is one whose provider may be sent a prompt's images.
const InputsSyntheticDemoImagesAndUserText = "synthetic_demo_images_and_user_typed_text"

// RetentionMetaStandard is the Meta Model API's Standard Services, the
// arrangement a Meta record confirms (T-055): "Meta does not use your
// Content from Standard Services to train Meta Models" (Meta Model API
// Data Commitments, https://dev.meta.ai/legal/commitments, retrieved
// 2026-10-26); Content retained "as needed" under the Terms of Service §4.4
// (https://dev.meta.ai/legal/terms-of-service, last updated 2026-10-09),
// including when flagged for a potential policy violation. Which mode a
// call uses is its model ID (metaStandardModels).
const RetentionMetaStandard = "meta_standard_services"

// metaStandardModels are the Meta models of the Standard Services by
// their model IDs: Meta Model API Model Designation and Geographic Use
// Policy §1 (https://dev.meta.ai/legal/geographic-use-policy, last updated
// 2026-10-12, retrieved 2026-10-26: "Standard Models ... Muse Spark 1.1,
// Muse Spark 1.2, Muse Spark 1.3 ... SAM 3.1"; "Contributor Models ...
// Muse Spark 1.2 Contributor, Muse Spark 1.3 Contributor") with the IDs of
// https://dev.meta.ai/docs/models. Only the language models a proposal can
// use and the segmentation model are listed; a designation that changes
// is a reviewed change here.
var (
	metaStandardModels     = []string{"muse-spark-1.1", "muse-spark-1.2", "muse-spark-1.3"}
	metaStandardSegmenters = []string{"sam-3.1"}
)

// The data retention arrangements a provider organization can have
// (https://platform.claude.com/docs/en/manage-claude/api-and-data-retention,
// retrieved 2026-10-26; docs/SECURITY.md §7).
const (
	// RetentionStandard: the commercial default, inputs and outputs
	// deleted within 30 days (up to 2 years when flagged by trust and
	// safety, or when the law requires).
	RetentionStandard = "standard"
	// RetentionZero: a zero data retention arrangement the account team
	// enabled for the organization.
	RetentionZero = "zero_data_retention"
)

// liveRoles are the prompt roles a data policy may let live AI serve
// (PROPOSED D-112): the proposal roles, whose context is the model and the
// user's intent. The importer sends sample rows of uploaded files and the
// analyst results computed from bound datasets, which H-018's policy does
// not cover; their jobs are not built yet (T-027, T-030).
var liveRoles = []string{"modeler", "scenario"}

// DataPolicy is what the provider may receive and what may be kept of it.
type DataPolicy struct {
	// Inputs is what the provider may receive: InputsSyntheticAndUserText.
	Inputs string
	// Roles are the prompt roles live AI serves, among liveRoles.
	Roles []string
	// LogsKeepRawText: logs may keep raw prompts or responses. H-018: no.
	LogsKeepRawText bool
}

// Confirmation is an owner's statement as recorded: who, when (a calendar
// date, YYYY-MM-DD), and the decision that records it.
type Confirmation struct {
	By, At, Source string
}

// Given reports whether the confirmation names who, a calendar date in
// the form YYYY-MM-DD, and where it is recorded.
func (c Confirmation) Given() bool {
	at, err := time.Parse(time.DateOnly, c.At)
	return c.By != "" && err == nil && at.Format(time.DateOnly) == c.At && c.Source != ""
}

// Retention is the owner's confirmation of the data retention arrangement
// both provider organizations have.
type Retention struct {
	// Arrangement is RetentionStandard or RetentionZero, the same for both
	// organizations.
	Arrangement string
	Confirmation
}

// Record is an owner's approval of live AI, as recorded.
type Record struct {
	ID       string // the decision that records it
	Provider string // "anthropic" (H-001) or "meta" (H-029, T-055)
	Model    string // the exact model ID
	// SegmentationModel is the exact model ID of the segmentation model the
	// approval covers (a Meta record: SAM 3.1 for the photo path, H-033);
	// empty for none.
	SegmentationModel string
	// DataPolicy is what may be sent and kept.
	DataPolicy DataPolicy
	// MonthlyBudgetUSD is the monthly budget in whole US dollars, both
	// provider organizations combined.
	MonthlyBudgetUSD int64
	// Credentials are the credential labels the approval covers; each must
	// be configured for live AI.
	Credentials []string
	ApprovedBy  string
	ApprovedAt  string
	Source      string
	// SpendLimits: the owner set the Console spend limits of both
	// provider organizations.
	SpendLimits Confirmation
	// Retention: both provider organizations have the same, named, data
	// retention arrangement.
	Retention Retention
}

// Gap is what r lacks before it can turn live AI on; "" when nothing. A
// record of a provider this build has no adapter for lacks everything.
func (r Record) Gap() Reason {
	switch r.Provider {
	case "anthropic":
		return r.anthropicGap()
	case "meta":
		return r.metaGap()
	}
	return ReasonIncomplete
}

// rolesOK reports whether the data policy names live roles only, and one
// at least.
func (p DataPolicy) rolesOK() bool {
	return len(p.Roles) > 0 && !slices.ContainsFunc(p.Roles, func(role string) bool { return !slices.Contains(liveRoles, role) })
}

// anthropicGap: H-018's rules (T-053). Both provider organizations'
// credentials, synthetic data and user-typed text only (no image), the
// Console spend limits and the retention arrangement confirmed.
func (r Record) anthropicGap() Reason {
	switch {
	case r.ID == "" || r.Model == "" || r.SegmentationModel != "" || r.ApprovedBy == "" || r.ApprovedAt == "" || r.Source == "" ||
		!slices.Equal(r.Credentials, []string{"org-a", "org-b"}):
		return ReasonIncomplete
	case r.DataPolicy.Inputs != InputsSyntheticAndUserText || r.DataPolicy.LogsKeepRawText || !r.DataPolicy.rolesOK():
		return ReasonPolicyUnsupported
	case r.MonthlyBudgetUSD <= 0:
		return ReasonBudgetMissing
	case !r.SpendLimits.Given():
		return ReasonSpendLimitsUnconfirmed
	case !r.Retention.Given() || (r.Retention.Arrangement != RetentionStandard && r.Retention.Arrangement != RetentionZero):
		return ReasonRetentionUnconfirmed
	}
	return ""
}

// metaGap: H-029's and H-033's rules (T-055). One key (label meta);
// Standard Models only; synthetic data, demo images (a segmentation model
// takes nothing else) and user-typed text; the Standard Services
// confirmed. H-029 keeps T-053's in-code spend caps and asks for no
// dashboard spend limit, so the record needs no SpendLimits (PROPOSED
// D-122).
func (r Record) metaGap() Reason {
	p := r.DataPolicy
	switch {
	case r.ID == "" || r.Model == "" || r.ApprovedBy == "" || r.ApprovedAt == "" || r.Source == "" || !slices.Equal(r.Credentials, []string{"meta"}):
		return ReasonIncomplete
	case !slices.Contains(metaStandardModels, r.Model) || (r.SegmentationModel != "" && !slices.Contains(metaStandardSegmenters, r.SegmentationModel)):
		return ReasonModelNotStandard
	case (p.Inputs != InputsSyntheticAndUserText && p.Inputs != InputsSyntheticDemoImagesAndUserText) || p.LogsKeepRawText || !p.rolesOK() ||
		(r.SegmentationModel != "" && p.Inputs != InputsSyntheticDemoImagesAndUserText):
		return ReasonPolicyUnsupported
	case r.MonthlyBudgetUSD <= 0:
		return ReasonBudgetMissing
	case !r.Retention.Given() || r.Retention.Arrangement != RetentionMetaStandard:
		return ReasonRetentionUnconfirmed
	}
	return ""
}

func (r Record) clone() Record {
	r.DataPolicy.Roles = slices.Clone(r.DataPolicy.Roles)
	r.Credentials = slices.Clone(r.Credentials)
	return r
}

// h018 is the owner's approval of live AI for the demonstration (H-018,
// 2026-10-20: 「全都照建議」 on the decision page, item (3)): Claude Opus 5.5,
// whose exact model ID is claude-opus-5-5 (Anthropic's model list as the
// claude-api reference gives it, 2026-10-06); only synthetic data and
// text the user typed, logs never keep raw prompts or responses; USD 100
// a month for both provider organizations combined (the Console spend
// limit), with a daily limit in code (D-110, PROPOSED). The owner
// confirmed the Console spend limits of both organizations on 2026-10-26
// (H-025, recorded on the main branch by the coordinator).
//
// Retention: H-018 says Anthropic's retention rules are checked in the
// official documentation before going live and that both organizations
// are set alike; which arrangement each organization has is an account
// setting only the owner sees (docs/SECURITY.md §7, PROPOSED D-113). The
// owner answered on 2026-10-26 (H-026, recorded by the coordinator): both
// provider organizations keep Anthropic's default standard retention, no
// zero data retention, the same for both. With it the record lacks
// nothing; live AI still needs AI_MODEL, both keys and GEMINUS_AI_LIVE=on
// (internal/config). An agent never fills in a confirmation the owner has
// not given.
var h018 = Record{
	ID:       "H-018",
	Provider: "anthropic",
	Model:    "claude-opus-5-5",
	DataPolicy: DataPolicy{
		Inputs: InputsSyntheticAndUserText,
		Roles:  []string{"modeler", "scenario"},
	},
	MonthlyBudgetUSD: 100,
	Credentials:      []string{"org-a", "org-b"},
	ApprovedBy:       "KCL1104 (repository owner)",
	ApprovedAt:       "2026-10-20",
	Source:           "H-018 (planning/decisions.json human_decisions; decision page https://claude.ai/artifact/T5R9YeWWw4q2q8kKkGvU2P)",
	SpendLimits: Confirmation{By: "KCL1104 (repository owner)", At: "2026-10-26",
		Source: "H-025 (Console spend limits set for both provider organizations; relayed by the coordinator)"},
	Retention: Retention{Arrangement: RetentionStandard, Confirmation: Confirmation{By: "KCL1104 (repository owner)", At: "2026-10-26",
		Source: "H-026 (both provider organizations keep the default standard retention, no zero data retention; the owner's answer in the session, recorded by the coordinator)"}},
}

// h029 is the owner's approval of the Meta Model API for the hackathon
// demo (T-055): H-029 (1), 2026-10-26, 「加上 Meta Model API
// (Recommended)」: a Meta provider for Muse Spark in Standard mode, never
// the Contributor variant that trains on the content; the Claude adapter
// kept; T-053's spend caps and switches; only synthetic data and demo
// images; the key in an environment variable, its value never read by an
// agent. H-033, 2026-10-26, 「兩者都用 Meta (Recommended)」, resumed it after
// H-031's pause: natural-language proposals on Muse Spark, the photo path
// (T-056) on SAM 3.1 and Muse Spark, only synthetic data, demo images and
// text the user typed sent, live calls only on independently reviewed
// T-055 code. The model is muse-spark-1.3 ("Recommended for new work",
// https://dev.meta.ai/docs/models; planning/HACKATHON_PLAN.md), the
// segmentation model sam-3.1 (H-033). The monthly budget is T-053's in-code
// cap, USD 100 (H-029: 沿用 T-053 的花費上限; the deployment's daily and
// per-organization limits are D-110's, PROPOSED). One key, labelled meta
// (GEMINUS_MUSE_API_KEY, the name the owner gave it). Standard mode is
// confirmed by H-029 itself: the owner chose it; which mode a call uses is
// its model ID, and the adapter refuses an answer from any other model.
// The retention terms are confirmed by H-035 (1), 2026-10-26, 「接受
// (Recommended)」: the owner accepted the Meta Model API Terms of Service
// (the 2026-10-09 version, checked by the coordinator), its retention for
// safety, abuse and policy review (§4.1, §4.4) included. H-029 (1) is the
// Standard-mode choice, not that acceptance (D-122 (b); T-055 review round
// 1, L4).
//
// The owner has confirmed no dashboard spend limit for the Meta team (H-033
// mentions the hackathon's USD 150 credit for 10 days); none is recorded,
// and the Meta rules do not ask for one (PROPOSED D-122). Roles: the
// proposal roles; T-056 adds the photo path's role, if it is another one,
// in a reviewed change of this record.
var h029 = Record{
	ID:                "H-029",
	Provider:          "meta",
	Model:             "muse-spark-1.3",
	SegmentationModel: "sam-3.1",
	DataPolicy: DataPolicy{
		Inputs: InputsSyntheticDemoImagesAndUserText,
		Roles:  []string{"modeler", "scenario"},
	},
	MonthlyBudgetUSD: 100,
	Credentials:      []string{"meta"},
	ApprovedBy:       "KCL1104 (repository owner)",
	ApprovedAt:       "2026-10-26",
	Source:           "H-029 (1), amended by H-033 (planning/decisions.json human_decisions; planning/HACKATHON_PLAN.md)",
	Retention: Retention{Arrangement: RetentionMetaStandard, Confirmation: Confirmation{By: "KCL1104 (repository owner)", At: "2026-10-26",
		Source: "H-035 (1): the owner accepted the Meta Model API Terms of Service (2026-10-09 version, checked by the coordinator), " +
			"its retention for safety, abuse and policy review (§4.1, §4.4) included; Standard Services are not trained on (§5.1); " +
			"Standard mode per H-029 (1)"}},
}

// recorded lists the owner's recorded approvals of live AI, by the ID of
// the decision that records them. Adding or changing one is the owner's
// decision, recorded in docs/DECISIONS_AND_TBD.md and made in a reviewed
// change; tests of this package alone replace one, for the duration of a
// test (export_test.go).
var recorded = map[string]Record{h018.ID: h018, h029.ID: h029}

// Lookup is the record of id as this build holds it (a copy).
func Lookup(id string) (Record, bool) {
	r, ok := recorded[id]
	if !ok {
		return Record{}, false
	}
	return r.clone(), true
}

// Approval proves that a complete approval of live AI is recorded in this
// build.
type Approval struct {
	rec *Record
}

// Recorded returns the Approval for id if this build records it and the
// record lacks nothing (Gap is "").
func Recorded(id string) (Approval, bool) {
	r, ok := recorded[id]
	if !ok || id == "" || r.Gap() != "" {
		return Approval{}, false
	}
	c := r.clone()
	return Approval{rec: &c}, true
}

// Valid reports whether a is a recorded approval: not the zero value, and
// exactly its record still held and complete.
func (a Approval) Valid() bool {
	if a.rec == nil {
		return false
	}
	r, ok := recorded[a.rec.ID]
	return ok && r.Gap() == "" && reflect.DeepEqual(r, *a.rec)
}

// ID is the decision that records the approval; empty for none.
func (a Approval) ID() string {
	if !a.Valid() {
		return ""
	}
	return a.rec.ID
}

// Model is the exact model the approval names; empty for none.
func (a Approval) Model() string {
	if !a.Valid() {
		return ""
	}
	return a.rec.Model
}

// MonthlyBudgetUSD is the approval's monthly budget; 0 for none.
func (a Approval) MonthlyBudgetUSD() int64 {
	if !a.Valid() {
		return 0
	}
	return a.rec.MonthlyBudgetUSD
}

// AllowsRole reports whether live AI may serve the prompt role.
func (a Approval) AllowsRole(role string) bool {
	return a.Valid() && role != "" && slices.Contains(a.rec.DataPolicy.Roles, role)
}

// Provider is the provider the approval covers ("anthropic", "meta");
// empty for none.
func (a Approval) Provider() string {
	if !a.Valid() {
		return ""
	}
	return a.rec.Provider
}

// SegmentationModel is the exact segmentation model the approval names;
// empty for none.
func (a Approval) SegmentationModel() string {
	if !a.Valid() {
		return ""
	}
	return a.rec.SegmentationModel
}

// AllowsImages reports whether the approval's data policy lets its
// provider be sent images (demo images: H-029, H-033).
func (a Approval) AllowsImages() bool {
	return a.Valid() && a.rec.DataPolicy.Inputs == InputsSyntheticDemoImagesAndUserText
}

// Credentials are the credential labels the approval covers.
func (a Approval) Credentials() []string {
	if !a.Valid() {
		return nil
	}
	return slices.Clone(a.rec.Credentials)
}
