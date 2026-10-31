package investigator

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	reg "github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/comparisons"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/results/compare"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/storage/analyses"
	"github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/storage/runs"
	"github.com/kcl1104/geminus/internal/storage/scenarios"
)

// The observation (H-061 §5.6): what the planner is shown of a carried-out
// call, written once by the worker from rows it can read (0002: SELECT on
// ai_proposals, scenarios, simulation_runs, simulation_results and
// ai_analyses), in RFC 8785 form, at most 6 KiB, schema-checked
// (investigator.schema.json ToolObservation) before it is stored. Every
// figure is the server's (compare.Compare over the stored results, as
// internal/analyses/analyzer and the API's compareSimulations compute it;
// the analysis's findings as analyst.Check passed them); AI and person text
// in it (a summary, questions, findings, suggestions, a scenario's name) is
// quoted data, bounded, never obeyed.

const observationSchema = "investigator.schema.json#/$defs/ToolObservation"

// Children is what a resolved call made, as the worker reads it for its
// observation.
type Children struct {
	Proposal *proposals.Proposal
	Scenario *scenarios.Scenario
	// Baseline and Run are a run_what_if's pair, with their stored Result
	// JSON once completed.
	Baseline, Run                 *runs.Run
	BaselineResult, RunResult     []byte
	Analysis                      *analyses.Analysis
	DraftRevision, OperationCount int64
}

// settled reports whether a done call's children have left their
// in-progress states, so its observation can be written (H-061 §5.6): a
// proposal past generating, both runs final, the analysis past generating;
// a saved scenario and a written draft at once.
func settled(tool reg.Tool, c Children) bool {
	switch tool {
	case reg.ProposeWhatIf, reg.ProposeModelEdit:
		return c.Proposal != nil && c.Proposal.Status != proposals.Generating
	case reg.SaveScenario:
		return c.Scenario != nil
	case reg.RunWhatIf:
		return c.Baseline != nil && c.Run != nil && c.Baseline.Status.Final() && c.Run.Status.Final()
	case reg.ExplainResults:
		return c.Analysis != nil && c.Analysis.Status != analyses.Generating
	case reg.WriteToDraft:
		return c.Proposal != nil && c.DraftRevision > 0
	}
	return false
}

// observe is the done call's observation: tool's result over its children,
// with index (a proposal's place in the investigation), the objective's
// metric path (scenario mode) and left, the budget after the step.
func observe(tool reg.Tool, c Children, index int, objective string, left reg.Budget) ([]byte, error) {
	var v map[string]any
	switch tool {
	case reg.ProposeWhatIf, reg.ProposeModelEdit:
		v = proposalResult(*c.Proposal, index)
	case reg.SaveScenario:
		src := ""
		if c.Scenario.SourceProposalID != nil {
			src = *c.Scenario.SourceProposalID
		}
		v = map[string]any{"scenario_id": c.Scenario.ID, "name": cut(c.Scenario.Name, 200), "proposal_id": src}
	case reg.RunWhatIf:
		var err error
		if v, err = runResult(c, objective); err != nil {
			return nil, err
		}
	case reg.ExplainResults:
		var err error
		if v, err = explanationResult(*c.Analysis); err != nil {
			return nil, err
		}
	case reg.WriteToDraft:
		v = map[string]any{"draft_revision": c.DraftRevision, "operation_count": max(c.OperationCount, 1)}
	default:
		return nil, fmt.Errorf("investigator: %s has no observation", tool)
	}
	v["budget"] = left
	return fit(v)
}

// fit is the observation in canonical form within 6 KiB: its texts cut
// further (500, then 100 characters, then the lists shortened) until it
// fits, and checked against the schema.
func fit(v map[string]any) ([]byte, error) {
	for _, bound := range []int{0, 500, 100, 40} {
		w := v
		if bound > 0 {
			w = shorten(v, bound).(map[string]any)
		}
		raw, err := json.Marshal(w, json.Deterministic(true))
		if err != nil {
			return nil, err
		}
		c, err := hashing.Canonical(raw)
		if err != nil {
			return nil, err
		}
		if len(c) > reg.MaxObservationBytes {
			continue
		}
		if issues, err := validate.Document(c, observationSchema, "INVALID_OBSERVATION"); err != nil || len(issues) > 0 {
			return nil, errors.New("investigator: an observation the schema refuses")
		}
		return c, nil
	}
	return nil, errors.New("investigator: an observation larger than its bound")
}

// shorten cuts every string of v to bound characters (IDs and codes are
// shorter than any bound used).
func shorten(v any, bound int) any {
	switch x := v.(type) {
	case string:
		return cut(x, bound)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = shorten(e, bound)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = shorten(e, bound)
		}
		return out
	case []string:
		out := make([]string, len(x))
		for i, e := range x {
			out[i] = cut(e, bound)
		}
		return out
	}
	return v
}

// cut is s at most n characters, valid UTF-8, never empty when s was not.
func cut(s string, n int) string {
	s = strings.ToValidUTF8(s, "�")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// proposalResult is a propose call's result (ProposalObservation): the
// proposal once it left generating; a person's decision is "decided".
func proposalResult(p proposals.Proposal, index int) map[string]any {
	status := string(p.Status)
	switch p.Status {
	case proposals.Approved, proposals.Rejected, proposals.Conflicted:
		status = "decided"
	case proposals.Ready, proposals.NeedsInput, proposals.Invalid, proposals.Failed:
	default:
		status = "failed"
	}
	var summary any
	if p.Summary != nil && *p.Summary != "" {
		summary = cut(*p.Summary, 2000)
	}
	var risk any
	if p.ServerRisk != nil {
		risk = *p.ServerRisk
	}
	questions := []string{}
	for _, q := range p.Questions {
		if q != "" && len(questions) < 5 {
			questions = append(questions, cut(q, 2000))
		}
	}
	return map[string]any{"proposal_id": p.ID, "index": index, "status": status, "summary": summary,
		"change_count": min(patchOps(p.Patch), 1000), "server_risk_level": risk, "questions": questions, "issue_codes": issueCodes(p.Validation)}
}

// patchOps is how many operations a stored JSON Patch has (0 for none).
func patchOps(patch []byte) int {
	var ops []jsontext.Value
	if len(patch) == 0 || json.Unmarshal(patch, &ops) != nil {
		return 0
	}
	return len(ops)
}

// issueCodes are the distinct codes of a stored validation's issues, at
// most 10, in their order.
func issueCodes(validation []byte) []string {
	out := []string{}
	var v struct {
		Issues []struct {
			Code string `json:"code"`
		} `json:"issues"`
	}
	if len(validation) == 0 || json.Unmarshal(validation, &v) != nil {
		return out
	}
	for _, i := range v.Issues {
		if len(out) >= 10 {
			break
		}
		if i.Code != "" && codeShape(i.Code) && !contains(out, i.Code) {
			out = append(out, i.Code)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// codeShape: ^[A-Z][A-Z0-9_]{0,63}$.
func codeShape(s string) bool {
	if len(s) == 0 || len(s) > 64 || s[0] < 'A' || s[0] > 'Z' {
		return false
	}
	for _, r := range s[1:] {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// failureCode is a stored job failure's code (its JSON's "code"), or
// fallback.
func failureCode(failure []byte, fallback string) string {
	var f struct {
		Code string `json:"code"`
	}
	if len(failure) > 0 && json.Unmarshal(failure, &f) == nil && codeShape(f.Code) {
		return f.Code
	}
	return fallback
}

// runResult is run_what_if's result (RunObservation): the pair's status,
// and once both completed the server's comparison: the objective's metric
// and at most six others, in the comparison's order.
func runResult(c Children, objective string) (map[string]any, error) {
	b, r := *c.Baseline, *c.Run
	out := map[string]any{"baseline_run_id": b.ID, "run_id": r.ID, "comparison": nil, "error_code": nil}
	switch {
	case b.Status == runs.Completed && r.Status == runs.Completed:
		out["status"] = "completed"
	case b.Status == runs.Cancelled || r.Status == runs.Cancelled:
		out["status"], out["error_code"] = "cancelled", "RUN_CANCELLED"
	default:
		code := failureCode(r.Failure, "")
		if r.Status != runs.Failed {
			code = failureCode(b.Failure, "")
		}
		if code == "" {
			code = "RUN_FAILED"
		}
		out["status"], out["error_code"] = "failed", code
	}
	if out["status"] != "completed" {
		return out, nil
	}
	base, err := decodeResult(c.BaselineResult)
	if err != nil {
		return nil, err
	}
	scen, err := decodeResult(c.RunResult)
	if err != nil {
		return nil, err
	}
	cmp, err := compare.Compare(comparisons.Input(b, base), comparisons.Input(r, scen))
	if err != nil {
		return nil, fmt.Errorf("investigator: the pair cannot be compared: %w", err)
	}
	out["comparison"] = comparisonResult(cmp, objective)
	return out, nil
}

// comparisonResult is the comparison as a RunObservation holds it.
func comparisonResult(c compare.Comparison, objective string) map[string]any {
	var obj any
	metrics := []compare.Metric{}
	for _, m := range c.Metrics {
		switch {
		case objective != "" && m.MetricPath == objective:
			obj = m
		case len(metrics) < 6:
			metrics = append(metrics, m)
		}
	}
	reasons := c.IncomparableReasons
	if reasons == nil {
		reasons = []string{}
	}
	if len(reasons) > 8 {
		reasons = reasons[:8]
	}
	warnings := c.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	if len(warnings) > 100 {
		warnings = warnings[:100]
	}
	return map[string]any{"comparable": c.Comparable, "incomparable_reasons": reasons, "objective": obj, "metrics": metrics,
		"warnings": warnings}
}

func decodeResult(data []byte) (*simulation.Result, error) {
	var r simulation.Result
	if len(data) == 0 || model.Decode(data, &r) != nil {
		return nil, errors.New("investigator: a stored result does not decode")
	}
	return &r, nil
}

// explanationResult is explain_results' result (ExplanationObservation):
// the analysis's summary, at most six findings and five suggested
// experiments (each with its place, what from_suggestion names), and how
// many were left out; a failed analysis's code.
func explanationResult(a analyses.Analysis) (map[string]any, error) {
	out := map[string]any{"analysis_id": a.ID, "summary": nil, "findings": []any{}, "suggested_next_experiments": []any{}, "omitted": 0,
		"error_code": nil}
	if a.Status != analyses.Completed {
		out["status"], out["error_code"] = "failed", failureCode(a.Failure, "ANALYSIS_FAILED")
		return out, nil
	}
	var o provider.AnalysisOutput
	if len(a.Analysis) == 0 || json.Unmarshal(a.Analysis, &o) != nil {
		return nil, errors.New("investigator: a stored analysis does not decode")
	}
	out["status"] = "completed"
	summary := cut(o.Scenario.Summary, 1000)
	if summary == "" {
		summary = "-"
	}
	out["summary"] = summary
	omitted := 0
	findings := []any{}
	for _, b := range o.Bottleneck {
		if len(findings) >= 6 || b.Statement == "" || (b.Kind != "observation" && b.Kind != "inference") {
			omitted++
			continue
		}
		findings = append(findings, map[string]any{"statement": cut(b.Statement, 1000), "kind": b.Kind})
	}
	suggestions := []any{}
	for i, e := range o.SuggestedNextExperiments {
		if len(suggestions) >= 5 || i > 9 || e.Name == "" || e.Hypothesis == "" {
			omitted++
			continue
		}
		suggestions = append(suggestions, map[string]any{"index": i, "name": cut(e.Name, 200), "hypothesis": cut(e.Hypothesis, 2000)})
	}
	out["findings"], out["suggested_next_experiments"], out["omitted"] = findings, suggestions, min(omitted, 1000)
	return out, nil
}
