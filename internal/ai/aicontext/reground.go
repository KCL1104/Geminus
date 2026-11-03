package aicontext

import (
	"encoding/json/v2"
	"fmt"
	"regexp"
	"strings"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/schemas"
)

// The context of the one re-ask of an explanation the server's grounding
// check refused (H-086 (R), the owner's choice of 2026-11-03; the check is
// internal/ai/analyst's, unchanged): the first call's sealed prompt again
// (its system text, blocks, role and locale; an explanation is never
// researched), the model's prior answer as the assistant turn after it
// (provider.Revision, as a proposal's repair sends it, H-057), and the
// follow-up, a user turn of the reground template (prompts/reground) and one
// server_data block:
//
//   - SERVER_CHECK: {"issues": [{"code", "path", "rule"}...],
//     "issues_not_listed": n}, the check's issues, each cleaned again here
//     whatever it said: a code the check emits (any other is
//     ANALYSIS_OUTPUT_INVALID), a path of the analysis output schema's own
//     member names and array indexes (cut before the first other token),
//     and the server's fixed rule for the code. Nothing the answer, the
//     question or a model label said is echoed into the follow-up.
//
// The prompt's template version names the re-ask after the first's
// ("analyst-0.3.3+reground-0.1.0"); its InputHash covers the prior answer by
// its digest. The context keeps the first's organization and attribution:
// the second call is charged to the same analysis (and so to its
// investigation's ceiling, H-061 §12), reserved and settled on its own.

// regroundTemplate is the follow-up's template (prompts/reground).
const regroundTemplate = "reground"

// CheckIssue is one issue of the server's grounding check (analyst.Issue):
// its code and a JSON Pointer into the refused answer.
type CheckIssue struct {
	Code string
	Path string
}

// checkOutputInvalid is the check's generic code (analyst.OutputInvalid):
// what an issue of any other code is listed as.
const checkOutputInvalid = "ANALYSIS_OUTPUT_INVALID"

// checkRules are the rule each code of the grounding check stands for, in
// the server's own words, as the follow-up lists it. internal/ai/analyst's
// tests keep its codes and these equal (TestEveryCheckCodeHasItsRule).
var checkRules = map[string]string{
	"ANALYSIS_NO_BASELINE":    "No baseline run was supplied: baseline must be null and delta empty, and no evidence may cite the baseline run.",
	"ANALYSIS_DELTA_UNKNOWN":  "The delta's metric_path is not one of the metric paths COMPARISON lists: a delta may name only those.",
	"ANALYSIS_DELTA_REPEATED": "An earlier delta already has this metric_path: each metric path is at most one delta.",
	"ANALYSIS_DELTA_MISMATCH": "The value is not COMPARISON's value for that metric path, rounded to the decimals written (a relative_delta at least to 2 decimals): " +
		"copy COMPARISON's value, or null where COMPARISON has null.",
	"ANALYSIS_EVIDENCE_UNKNOWN": "The evidence names a run that was not supplied, or a metric path ALLOWED_METRIC_REFERENCES does not list for that run " +
		"(or a statistic under one that has no value): cite only listed metrics with a value.",
	"ANALYSIS_NUMBER_UNVERIFIED": "The text holds a number that is none of the values the data supplied, at the decimals written (a percentage as the value " +
		"times 100), or a number that cannot be checked (spelled in words or in other digits): write only supplied numbers, in Arabic digits; " +
		"never one you computed or chose; otherwise say it without the number.",
	"ANALYSIS_CERTAINTY":           "The claim promises an outcome (guaranteed, certainly, definitely, will always, 保證, 一定會): a simulation result is not a certain future.",
	"ANALYSIS_SERVER_ID":           "The text names a server ID: name runs by their role (baseline, scenario) and entities by the names MODEL_SUMMARY shows.",
	"ANALYSIS_UNCERTAINTY_MISSING": "A run has fewer than 2 replications, so the interval of its mean is unavailable: uncertainty must say so.",
	"ANALYSIS_OUTPUT_INVALID": "The value is not one the output schema or the server's rules accept: a bottleneck's kind is observation or inference " +
		"and it has at least one evidence; every suggested experiment has requires_confirmation true.",
}

// CheckRule is the rule the follow-up gives for code, and whether code is
// one of the grounding check's.
func CheckRule(code string) (string, bool) {
	r, ok := checkRules[code]
	return r, ok
}

// CheckCodes are the codes CheckRule knows.
func CheckCodes() []string {
	out := make([]string, 0, len(checkRules))
	for c := range checkRules {
		out = append(out, c)
	}
	return out
}

// analysisMembers are the member names analysis-output.schema.json declares,
// the only names a cleaned path may hold.
var analysisMembers = func() map[string]bool {
	data, err := schemas.FS.ReadFile("analysis-output.schema.json")
	if err != nil {
		panic(fmt.Sprintf("aicontext: the embedded analysis output schema: %v", err)) // a build without the contract
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		panic(fmt.Sprintf("aicontext: the embedded analysis output schema: %v", err))
	}
	out := map[string]bool{}
	var collect func(v any)
	collect = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if props, ok := v["properties"].(map[string]any); ok {
				for name := range props {
					out[name] = true
				}
			}
			for _, x := range v {
				collect(x)
			}
		case []any:
			for _, x := range v {
				collect(x)
			}
		}
	}
	collect(doc)
	return out
}()

var checkIndex = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})$`)

// checkPath is path cut before its first token that is neither an array
// index nor a member of the analysis output; "" (the answer as a whole)
// when none is left.
func checkPath(path string) string {
	if !strings.HasPrefix(path, "/") {
		return ""
	}
	tokens := strings.Split(path[1:], "/")
	n := 0
	for _, tok := range tokens {
		if !checkIndex.MatchString(tok) && !analysisMembers[tok] {
			break
		}
		n++
	}
	if n == 0 {
		return ""
	}
	return "/" + strings.Join(tokens[:n], "/")
}

// checkIssue is one issue of SERVER_CHECK.
type checkIssue struct {
	Code string `json:"code"`
	Path string `json:"path"`
	Rule string `json:"rule"`
}

// ReviseAnalysis is the context of the re-ask of first, whose schema-valid
// answer prior the server's grounding check refused with issues (at most
// MaxRepairIssues; notListed more were left out).
func ReviseAnalysis(first provider.AnalysisContext, prior provider.PriorAnswer, issues []CheckIssue, notListed int) (provider.AnalysisContext, error) {
	switch {
	case first.Prompt.Role != provider.RoleAnalyst:
		return provider.AnalysisContext{}, fail(InvalidInput, "/prompt/role")
	case first.Prompt.Revision != nil:
		// An explanation is asked again at most once.
		return provider.AnalysisContext{}, fail(InvalidInput, "/prompt/revision")
	case !prior.Present():
		return provider.AnalysisContext{}, fail(InvalidInput, "/prior")
	case len(issues) == 0 || len(issues) > MaxRepairIssues:
		return provider.AnalysisContext{}, fail(InvalidInput, "/issues")
	case notListed < 0:
		return provider.AnalysisContext{}, fail(InvalidInput, "/issues_not_listed")
	}
	if err := first.Prompt.Verify(); err != nil {
		return provider.AnalysisContext{}, &Error{Code: InvalidInput, Path: "/prompt", cause: err}
	}
	text, version, err := template(regroundTemplate)
	if err != nil {
		return provider.AnalysisContext{}, err
	}
	list := make([]checkIssue, len(issues))
	for i, is := range issues {
		code := is.Code
		rule, ok := checkRules[code]
		if !ok {
			// Never a code the check emits: the check's generic one.
			code, rule = checkOutputInvalid, checkRules[checkOutputInvalid]
		}
		list[i] = checkIssue{Code: code, Path: checkPath(is.Path), Rule: rule}
	}
	b, err := block(provider.ServerData, "SERVER_CHECK", struct {
		Issues    []checkIssue `json:"issues"`
		NotListed int          `json:"issues_not_listed"`
	}{list, notListed})
	if err != nil {
		return provider.AnalysisContext{}, err
	}
	p := first.Prompt
	p.Revision = &provider.Revision{Prior: prior, Rules: text, Blocks: []provider.Block{b}}
	p.TemplateVersion += "+" + version
	sealed, err := provider.Seal(p)
	if err != nil {
		return provider.AnalysisContext{}, &Error{Code: InvalidInput, Path: "/prompt", cause: err}
	}
	return provider.AnalysisContext{OrganizationID: first.OrganizationID, Prompt: sealed, Attribution: first.Attribution}, nil
}
