package aicontext

import (
	"encoding/json/jsontext"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// RunResult is a completed run's result, read in the scope.
type RunResult struct {
	OrganizationID string
	ModelID        string
	RunID          string
	Result         *simulation.Result
}

// AnalysisInput is everything an analysis context is built from: the
// analyzed (scenario) run, an optional baseline run and the server's
// comparison of the two (T-029), all read in Scope.
type AnalysisInput struct {
	Scope         Scope
	Version       Version // the analyzed run's model version
	Domain        domainpack.PromptContext
	Scenario      RunResult
	ScenarioPatch model.Patch // the analyzed run's frozen patch; empty for a baseline run
	Config        simulation.RunConfig
	Baseline      *RunResult
	Comparison    jsontext.Value // server-computed; absent without a baseline
	Question      string
	OutputLocale  string
	Budget        provider.Budget
}

// MetricRef is a metric an analysis may cite as evidence (D-026): a run by
// role and a JSON Pointer into its result, to an available metric.
type MetricRef struct {
	Run        string `json:"run"`
	MetricPath string `json:"metric_path"`
}

// AnalysisPrompt is an analysis context with what the analyst service
// (T-030) checks the answer against: the citable metrics and every number
// the context supplied (for locale.CheckProse).
type AnalysisPrompt struct {
	Context  provider.AnalysisContext
	Allowed  []MetricRef
	Supplied []float64
}

func escapeToken(s string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(s)
}

// availableMetrics lists the result's available metrics as JSON Pointers,
// sorted, and counts the unavailable ones.
func availableMetrics(m simulation.Metrics) (refs []string, unavailable int) {
	summary := func(path string, s simulation.MetricSummary) {
		if s.Status == simulation.Available && s.Mean != nil {
			refs = append(refs, path)
		} else {
			unavailable++
		}
	}
	summary("/metrics/throughput", m.Throughput)
	summary("/metrics/lead_time_minutes", m.LeadTimeMinutes)
	summary("/metrics/failure_count", m.FailureCount)
	summary("/metrics/rework_count", m.ReworkCount)
	for _, group := range []struct {
		name string
		m    map[string]simulation.MetricSummary
	}{{"dora", m.DORA}, {"additional_metrics", m.AdditionalMetrics}} {
		for k, s := range group.m {
			summary("/metrics/"+group.name+"/"+escapeToken(k), s)
		}
	}
	for k, r := range m.Resources {
		if r.UtilizationMean != nil {
			refs = append(refs, "/metrics/resources/"+escapeToken(k))
		} else {
			unavailable++
		}
	}
	for k, q := range m.Queues {
		if q.Status == simulation.Available && q.WaitMeanMinutes != nil {
			refs = append(refs, "/metrics/queues/"+escapeToken(k))
		} else {
			unavailable++
		}
	}
	sort.Strings(refs)
	return refs, unavailable
}

// numbers collects every number in a decoded JSON value.
func numbers(v any, out *[]float64) {
	switch x := v.(type) {
	case float64:
		*out = append(*out, x)
	case []any:
		for _, e := range x {
			numbers(e, out)
		}
	case map[string]any:
		for _, e := range x {
			numbers(e, out)
		}
	}
}

func checkRun(r RunResult, s Scope, at string) error {
	if r.OrganizationID != s.OrganizationID || r.ModelID != s.ModelID {
		return fail(CrossTenant, at)
	}
	if r.Result == nil {
		return fail(InvalidInput, at+"/result")
	}
	if r.Result.RunID != r.RunID {
		return fail(CrossTenant, at+"/result/run_id")
	}
	if r.Result.Status != "completed" {
		return fail(InvalidInput, at+"/result/status")
	}
	return nil
}

// resultView is what the analyst sees of a result: its metrics and
// measurement window, never its run ID, hashes or trace.
func resultView(r *simulation.Result, z *sanitizer, om *omissions, role string) (any, error) {
	v, err := raw(struct {
		EngineVersion     string                       `json:"engine_version"`
		Replications      int                          `json:"replications"`
		Metrics           simulation.Metrics           `json:"metrics"`
		MeasurementWindow simulation.MeasurementWindow `json:"measurement_window"`
	}{r.EngineVersion, len(r.ReplicationSummaries), r.Metrics, r.MeasurementWindow})
	if err != nil {
		return nil, err
	}
	om.add(role+"_results.run_id", 1, ReasonServerBound)
	om.add(role+"_results.replication_summaries", len(r.ReplicationSummaries), ReasonBounded)
	return z.value(v), nil
}

// Analysis builds the context of an analysis of supplied results.
func Analysis(in AnalysisInput) (AnalysisPrompt, error) {
	loc, err := parseLocale(in.OutputLocale)
	if err != nil {
		return AnalysisPrompt{}, err
	}
	if err := in.Budget.Validate(); err != nil {
		return AnalysisPrompt{}, &Error{Code: InvalidInput, Path: "/budget", cause: err}
	}
	if err := checkScope(in.Scope, true); err != nil {
		return AnalysisPrompt{}, err
	}
	if in.Version.OrganizationID != in.Scope.OrganizationID || in.Version.ModelID != in.Scope.ModelID {
		return AnalysisPrompt{}, fail(CrossTenant, "/version")
	}
	if in.Version.IR == nil {
		return AnalysisPrompt{}, fail(InvalidInput, "/version/ir")
	}
	if in.Version.IR.ModelID != in.Scope.ModelID {
		return AnalysisPrompt{}, fail(CrossTenant, "/version/ir/model_id")
	}
	if err := checkRun(in.Scenario, in.Scope, "/scenario"); err != nil {
		return AnalysisPrompt{}, err
	}
	if in.Baseline != nil {
		if err := checkRun(*in.Baseline, in.Scope, "/baseline"); err != nil {
			return AnalysisPrompt{}, err
		}
		if in.Baseline.RunID == in.Scenario.RunID {
			return AnalysisPrompt{}, fail(InvalidInput, "/baseline")
		}
	}
	if len(in.Comparison) > 0 && in.Baseline == nil {
		return AnalysisPrompt{}, fail(InvalidInput, "/comparison")
	}
	if in.Domain.Domain != in.Version.IR.Domain {
		return AnalysisPrompt{}, fail(DomainMismatch, "/domain")
	}
	if utf8.RuneCountInString(in.Question) > maxQuestionRunes {
		return AnalysisPrompt{}, fail(InvalidInput, "/question")
	}

	text, version, err := template(provider.RoleAnalyst)
	if err != nil {
		return AnalysisPrompt{}, err
	}
	system := text + serverRules(provider.RoleAnalyst, "", loc) +
		"- Evidence cites only ALLOWED_METRIC_REFERENCES, by run role (baseline or scenario). A metric that is unavailable, null or not listed is not a known value.\n" +
		domainRules(in.Domain)

	z := newSanitizer(maxStringRunes)
	om := &omissions{}
	var blocks []provider.Block
	add := func(kind provider.BlockKind, name string, v any) error {
		b, err := block(kind, name, v)
		if err == nil {
			blocks = append(blocks, b)
		}
		return err
	}
	var supplied []float64
	collect := func(v any) { numbers(v, &supplied) }

	ir := in.Version.IR
	entities := entityIndex(ir, z)
	summary := map[string]any{"domain": z.text(ir.Domain), "name": z.textBound(ir.Name, maxNameRunes), "time_unit": string(ir.TimeUnit),
		"entities": entities}
	// The model's own quantities count as supplied; array indices do not.
	for _, e := range entities {
		for _, k := range []string{"capacity", "probability"} {
			switch v := e[k].(type) {
			case int:
				supplied = append(supplied, float64(v))
			case float64:
				supplied = append(supplied, v)
			}
		}
	}
	if err := add(provider.UntrustedData, "MODEL_SUMMARY", summary); err != nil {
		return AnalysisPrompt{}, err
	}
	patch, err := raw(in.ScenarioPatch)
	if err != nil {
		return AnalysisPrompt{}, err
	}
	if patch == nil {
		patch = []any{}
	}
	scenario := map[string]any{"patch": z.value(patch)}
	collect(scenario)
	if err := add(provider.UntrustedData, "SCENARIO", scenario); err != nil {
		return AnalysisPrompt{}, err
	}
	config, err := raw(in.Config)
	if err != nil {
		return AnalysisPrompt{}, err
	}
	collect(config)
	if err := add(provider.UntrustedData, "SIMULATION_CONFIG", z.value(config)); err != nil {
		return AnalysisPrompt{}, err
	}
	scen, err := resultView(in.Scenario.Result, z, om, "scenario")
	if err != nil {
		return AnalysisPrompt{}, err
	}
	collect(scen)
	if err := add(provider.UntrustedData, "SCENARIO_RESULTS", scen); err != nil {
		return AnalysisPrompt{}, err
	}
	var allowed []MetricRef
	refs, unavailable := availableMetrics(in.Scenario.Result.Metrics)
	for _, r := range refs {
		allowed = append(allowed, MetricRef{Run: "scenario", MetricPath: r})
	}
	om.add("metric_references.scenario", unavailable, ReasonUnavailable)
	var base any // JSON null without a baseline: baseline=null, delta=[]
	if in.Baseline != nil {
		if base, err = resultView(in.Baseline.Result, z, om, "baseline"); err != nil {
			return AnalysisPrompt{}, err
		}
		collect(base)
		refs, unavailable := availableMetrics(in.Baseline.Result.Metrics)
		for _, r := range refs {
			allowed = append(allowed, MetricRef{Run: "baseline", MetricPath: r})
		}
		om.add("metric_references.baseline", unavailable, ReasonUnavailable)
	}
	if err := add(provider.UntrustedData, "BASELINE_RESULTS", base); err != nil {
		return AnalysisPrompt{}, err
	}
	if len(in.Comparison) > 0 {
		cmp, err := z.json(in.Comparison)
		if err != nil {
			return AnalysisPrompt{}, &Error{Code: InvalidInput, Path: "/comparison", cause: err}
		}
		collect(cmp)
		if err := add(provider.UntrustedData, "COMPARISON", cmp); err != nil {
			return AnalysisPrompt{}, err
		}
	}
	if allowed == nil {
		allowed = []MetricRef{}
	}
	if err := add(provider.ServerData, "ALLOWED_METRIC_REFERENCES", allowed); err != nil {
		return AnalysisPrompt{}, err
	}
	if strings.TrimSpace(in.Question) != "" {
		if err := add(provider.UntrustedData, "QUESTION", z.textBound(in.Question, maxQuestionRunes)); err != nil {
			return AnalysisPrompt{}, err
		}
	}
	p, err := assemble(in.Scope.OrganizationID, provider.RoleAnalyst, loc, system, version, blocks, om, z)
	if err != nil {
		return AnalysisPrompt{}, err
	}
	if !fits(p, in.Budget) {
		return AnalysisPrompt{}, fail(TooLarge, "")
	}
	slices.Sort(supplied)
	supplied = slices.Compact(supplied)
	return AnalysisPrompt{Context: provider.AnalysisContext{OrganizationID: in.Scope.OrganizationID, Prompt: p},
		Allowed: allowed, Supplied: supplied}, nil
}
