package provider

import "github.com/kcl1104/geminus/internal/model"

// ProposalOutput is proposal-output.schema.json: what a provider may say
// about a proposal. It has no proposal ID, base, mode, validation, approval
// or server risk (D-002): the server binds those, and RiskLevel is kept only
// as the provider's claim, which can raise the server's risk but never lower
// it (D-027). Changes is untrusted until the server validates it (T-025).
type ProposalOutput struct {
	Status               string      `json:"status"` // "proposed" or "needs_input"
	Summary              string      `json:"summary"`
	Assumptions          []string    `json:"assumptions"`
	RiskLevel            string      `json:"risk_level,omitzero"`
	Confidence           *float64    `json:"confidence,omitzero"`
	Changes              model.Patch `json:"changes,omitzero"`
	ExpectedEffects      []string    `json:"expected_effects,omitzero"`
	Questions            []string    `json:"questions,omitzero"`
	RequiresConfirmation bool        `json:"requires_confirmation"`
}

// MappingOutput is ai-mapping.schema.json: column mapping suggestions,
// separate from a person's confirmed mapping (D-009, D-043).
type MappingOutput struct {
	Mappings []Mapping `json:"mappings"`
}

// Mapping is one suggested column mapping.
type Mapping struct {
	SourceColumn         string  `json:"source_column"`
	TargetField          *string `json:"target_field"`
	Confidence           float64 `json:"confidence"`
	Evidence             string  `json:"evidence"`
	Transform            string  `json:"transform"`
	RequiresConfirmation bool    `json:"requires_confirmation"`
}

// AnalysisOutput is analysis-output.schema.json: runs are named by role
// (baseline or scenario), never by ID (D-026). Schema-valid is not
// grounded: evidence paths, values, deltas and prose numbers are checked by
// the analyst service (T-030) against the supplied results.
type AnalysisOutput struct {
	Baseline                 *AnalysisSummary `json:"baseline"`
	Scenario                 AnalysisSummary  `json:"scenario"`
	Delta                    []AnalysisDelta  `json:"delta"`
	Bottleneck               []Bottleneck     `json:"bottleneck"`
	Assumptions              []string         `json:"assumptions"`
	Uncertainty              []string         `json:"uncertainty"`
	SuggestedNextExperiments []Experiment     `json:"suggested_next_experiments"`
}

type AnalysisSummary struct {
	Summary string `json:"summary"`
}

type AnalysisDelta struct {
	MetricPath    string   `json:"metric_path"`
	AbsoluteDelta *float64 `json:"absolute_delta"`
	RelativeDelta *float64 `json:"relative_delta"`
	Reason        *string  `json:"reason"`
}

type Bottleneck struct {
	Statement string     `json:"statement"`
	Kind      string     `json:"kind"` // observation | inference
	Evidence  []Evidence `json:"evidence"`
}

type Evidence struct {
	Run        string `json:"run"` // baseline | scenario
	MetricPath string `json:"metric_path"`
}

// Experiment is a hypothesis for a person to choose; it never runs.
type Experiment struct {
	Name                 string `json:"name"`
	Hypothesis           string `json:"hypothesis"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
}
