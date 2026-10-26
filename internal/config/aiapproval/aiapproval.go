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
// adapter (internal/ai/provider/anthropic) accepts nothing but a valid
// Approval for its own model, so no code path reaches the live provider
// with an approval the build does not hold in full, whatever string it
// holds.
//
// The package is a leaf: it imports nothing of this module, so the adapter
// may depend on it without depending on the rest of internal/config
// (architecture rule ai-suggests-only).
package aiapproval

import (
	"reflect"
	"slices"
)

// Reason says what a record lacks before it can turn live AI on.
type Reason string

const (
	// ReasonIncomplete: the record does not name the provider
	// (anthropic), the model, both credentials (org-a, org-b), the
	// approver, the date or the source.
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
)

// InputsSyntheticAndUserText is H-018's data policy for the demonstration
// period: the provider receives synthetic data and text the user typed,
// nothing else.
const InputsSyntheticAndUserText = "synthetic_and_user_typed_text"

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

// Confirmation is an owner's statement as recorded: who, when, and the
// decision that records it.
type Confirmation struct {
	By, At, Source string
}

// Given reports whether the confirmation names who, when and where.
func (c Confirmation) Given() bool { return c.By != "" && c.At != "" && c.Source != "" }

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
	Provider string // "anthropic" (H-001)
	Model    string // the exact model ID
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

// Gap is what r lacks before it can turn live AI on; "" when nothing.
func (r Record) Gap() Reason {
	switch {
	case r.ID == "" || r.Provider != "anthropic" || r.Model == "" || r.ApprovedBy == "" || r.ApprovedAt == "" || r.Source == "" ||
		!slices.Equal(r.Credentials, []string{"org-a", "org-b"}):
		return ReasonIncomplete
	case r.DataPolicy.Inputs != InputsSyntheticAndUserText || r.DataPolicy.LogsKeepRawText || len(r.DataPolicy.Roles) == 0 ||
		slices.ContainsFunc(r.DataPolicy.Roles, func(role string) bool { return !slices.Contains(liveRoles, role) }):
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
// Retention is not confirmed: H-018 says Anthropic's retention rules are
// checked in the official documentation before going live and that both
// organizations are set alike, and which arrangement each organization
// has is an account setting only the owner sees (docs/SECURITY.md §7).
// Until the owner confirms it and a reviewed change records it here
// (Arrangement, By, At, Source), this record cannot turn live AI on. An
// agent never fills it in on the owner's behalf.
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
}

// recorded lists the owner's recorded approvals of live AI, by the ID of
// the decision that records them. Adding or changing one is the owner's
// decision, recorded in docs/DECISIONS_AND_TBD.md and made in a reviewed
// change; tests of this package alone replace one, for the duration of a
// test (export_test.go).
var recorded = map[string]Record{h018.ID: h018}

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

// Credentials are the credential labels the approval covers.
func (a Approval) Credentials() []string {
	if !a.Valid() {
		return nil
	}
	return slices.Clone(a.rec.Credentials)
}
