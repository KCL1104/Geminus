// Package analyst is the server's check of an analyst answer (T-030;
// AI_SPEC §9, D-010, D-026, D-090). A provider answer is untrusted data:
// schema-valid is not grounded. An analysis is stored as completed only
// when every claim it makes can be checked against what the server
// supplied:
//
//   - without a baseline run there is no baseline summary, no delta and no
//     baseline evidence (baseline=null, delta=[]);
//   - each delta names a metric path of the server's comparison (once), and
//     its values are the comparison's as a person rounds them: the decimals
//     the answer wrote, the sign included; a null where the comparison has
//     none. What is stored is the comparison's own values;
//   - each piece of evidence names a run that was supplied and a metric
//     the context listed for that run (ALLOWED_METRIC_REFERENCES, summary
//     level), or a statistic under one that the result holds a value for
//     (compare.Resolve);
//   - every number in the prose is one of the supplied values
//     (locale.CheckProse: Arabic numbers rounded as written, percentages;
//     number words and other digits are unverifiable);
//   - each conclusion has evidence and is an observation or an inference;
//     each experiment needs a person to choose it;
//   - no claim promises an outcome (AI_SPEC §9: a simulation result is not
//     a certain future), no prose names a server ID (D-026: runs are named
//     by role; the context holds none), and an analysis of fewer than two
//     replications says what is uncertain (the interval is unavailable).
//
// Any issue refuses the whole answer: nothing it says is stored (the job
// fails the analysis, internal/analyses). The package reads no database and
// calls no provider (the architecture rule ai-is-pure-but-the-adapter).
package analyst

import (
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/results/compare"
	"github.com/kcl1104/geminus/internal/simulation"
)

// Issue codes: why an answer is not grounded, at a JSON Pointer into it.
const (
	// NoBaseline: a baseline summary, a delta or baseline evidence without
	// a baseline run.
	NoBaseline = "ANALYSIS_NO_BASELINE"
	// DeltaUnknown: a delta's metric path is not one of the comparison's.
	DeltaUnknown = "ANALYSIS_DELTA_UNKNOWN"
	// DeltaRepeated: a second delta of the same metric path.
	DeltaRepeated = "ANALYSIS_DELTA_REPEATED"
	// DeltaMismatch: a delta's value is not the comparison's.
	DeltaMismatch = "ANALYSIS_DELTA_MISMATCH"
	// EvidenceUnknown: evidence of a run that was not supplied, or of a
	// metric that was not listed for it or has no value.
	EvidenceUnknown = "ANALYSIS_EVIDENCE_UNKNOWN"
	// NumberUnverified: a number in the prose that is none of the supplied
	// values, or a number written in a way that cannot be checked.
	NumberUnverified = "ANALYSIS_NUMBER_UNVERIFIED"
	// Certainty: a claim promises an outcome ("guaranteed", 「保證」).
	Certainty = "ANALYSIS_CERTAINTY"
	// ServerID: prose names a server ID (a run, a model, an organization).
	ServerID = "ANALYSIS_SERVER_ID"
	// UncertaintyMissing: fewer than two replications and no uncertainty.
	UncertaintyMissing = "ANALYSIS_UNCERTAINTY_MISSING"
	// OutputInvalid: what the output schema already states (kind,
	// evidence, confirmation), checked again so nothing relies on the
	// caller.
	OutputInvalid = "ANALYSIS_OUTPUT_INVALID"
)

// Issue is one reason the answer is not grounded.
type Issue struct {
	Code string
	Path string
}

// Input is an answer and what the server supplied for it.
type Input struct {
	Output   provider.AnalysisOutput
	Allowed  []aicontext.MetricRef // the context's ALLOWED_METRIC_REFERENCES
	Supplied []float64             // every number the context supplied
	Scenario *simulation.Result
	// Baseline and Comparison are both nil without a baseline run.
	Baseline   *simulation.Result
	Comparison *compare.Comparison
}

// Check returns the analysis to store, with the comparison's own delta
// values, or the issues that make the answer ungrounded (and a zero
// analysis).
func Check(in Input) (provider.AnalysisOutput, []Issue) {
	c := checker{in: in}
	o := in.Output
	if in.Baseline == nil && o.Baseline != nil {
		c.add(NoBaseline, "/baseline")
	}
	if o.Baseline != nil {
		c.claim("/baseline/summary", o.Baseline.Summary)
	}
	c.claim("/scenario/summary", o.Scenario.Summary)
	if len(o.Uncertainty) == 0 && (few(in.Scenario) || (in.Baseline != nil && few(in.Baseline))) {
		c.add(UncertaintyMissing, "/uncertainty")
	}

	deltas := make([]provider.AnalysisDelta, len(o.Delta))
	seen := map[string]bool{}
	for i, d := range o.Delta {
		at := "/delta/" + strconv.Itoa(i)
		deltas[i] = provider.AnalysisDelta{MetricPath: d.MetricPath, Reason: clone(d.Reason)}
		if d.Reason != nil {
			c.claim(at+"/reason", *d.Reason)
		}
		if in.Comparison == nil {
			c.add(NoBaseline, at)
			continue
		}
		m, ok := in.Comparison.Metric(d.MetricPath)
		switch {
		case !ok:
			c.add(DeltaUnknown, at+"/metric_path")
			continue
		case seen[d.MetricPath]:
			c.add(DeltaRepeated, at+"/metric_path")
			continue
		}
		seen[d.MetricPath] = true
		if !rounded(d.AbsoluteDelta, m.AbsoluteDelta, 0) {
			c.add(DeltaMismatch, at+"/absolute_delta")
		}
		// A relative delta is a fraction: at least two decimals (a
		// percentage point), so a written 0 or 1 cannot stand for 40%.
		if !rounded(d.RelativeDelta, m.RelativeDelta, 2) {
			c.add(DeltaMismatch, at+"/relative_delta")
		}
		deltas[i].AbsoluteDelta, deltas[i].RelativeDelta = clone(m.AbsoluteDelta), clone(m.RelativeDelta)
	}

	bottleneck := make([]provider.Bottleneck, len(o.Bottleneck))
	for i, b := range o.Bottleneck {
		at := "/bottleneck/" + strconv.Itoa(i)
		bottleneck[i] = provider.Bottleneck{Statement: b.Statement, Kind: b.Kind, Evidence: slices.Clone(b.Evidence)}
		c.claim(at+"/statement", b.Statement)
		if b.Kind != "observation" && b.Kind != "inference" {
			c.add(OutputInvalid, at+"/kind")
		}
		if len(b.Evidence) == 0 {
			c.add(OutputInvalid, at+"/evidence")
		}
		for j, e := range b.Evidence {
			c.evidence(at+"/evidence/"+strconv.Itoa(j), e)
		}
	}
	for i, s := range o.Assumptions {
		c.prose("/assumptions/"+strconv.Itoa(i), s)
	}
	for i, s := range o.Uncertainty {
		c.prose("/uncertainty/"+strconv.Itoa(i), s)
	}
	for i, x := range o.SuggestedNextExperiments {
		at := "/suggested_next_experiments/" + strconv.Itoa(i)
		c.prose(at+"/name", x.Name)
		c.claim(at+"/hypothesis", x.Hypothesis)
		if !x.RequiresConfirmation {
			c.add(OutputInvalid, at+"/requires_confirmation")
		}
	}
	if len(c.issues) > 0 {
		return provider.AnalysisOutput{}, c.issues
	}
	var baseline *provider.AnalysisSummary
	if o.Baseline != nil {
		baseline = &provider.AnalysisSummary{Summary: o.Baseline.Summary}
	}
	return provider.AnalysisOutput{Baseline: baseline, Scenario: o.Scenario, Delta: deltas, Bottleneck: bottleneck,
		Assumptions: slices.Clone(o.Assumptions), Uncertainty: slices.Clone(o.Uncertainty),
		SuggestedNextExperiments: slices.Clone(o.SuggestedNextExperiments)}, nil
}

type checker struct {
	in     Input
	issues []Issue
}

func (c *checker) add(code, path string) { c.issues = append(c.issues, Issue{Code: code, Path: path}) }

// prose checks text a person reads: its numbers, and that it names no server ID.
func (c *checker) prose(path, text string) {
	if len(locale.CheckProse(text, c.in.Supplied)) > 0 {
		c.add(NumberUnverified, path)
	}
	if serverID.MatchString(text) {
		c.add(ServerID, path)
	}
}

// claim checks a statement about the results: prose that promises nothing.
func (c *checker) claim(path, text string) {
	c.prose(path, text)
	if certain.MatchString(text) {
		c.add(Certainty, path)
	}
}

var (
	// D-001's server IDs.
	serverID = regexp.MustCompile(`\b(?:org|usr|prj|mdl|mv|ds|dsv|imp|aip|scn|sim|ana|job|aud|req|art|asr)_[0-7][0-9A-HJKMNP-TV-Z]{25}\b`)
	// ponytail: a word list for a promised outcome, so "not guaranteed" in a claim is refused too; uncertainty and assumptions are not checked, where such words belong.
	certain = regexp.MustCompile(`(?i)\b(?:guarantee[sd]?|certainly|definitely|will always|is certain to|are certain to)\b|保證|保证|必然|一定會|一定会|肯定會|肯定会|絕對會|绝对会`)
)

// few: the run has fewer than two replications (no interval of its mean).
func few(r *simulation.Result) bool { return r != nil && len(r.ReplicationSummaries) < 2 }

func (c *checker) evidence(path string, e provider.Evidence) {
	var r *simulation.Result
	switch e.Run {
	case "scenario":
		r = c.in.Scenario
	case "baseline":
		if c.in.Baseline == nil {
			c.add(NoBaseline, path)
			return
		}
		r = c.in.Baseline
	}
	if r == nil || !c.cited(e, r) {
		c.add(EvidenceUnknown, path)
	}
}

// cited: e names a metric listed for its run, or a statistic under one
// that r holds a value for.
func (c *checker) cited(e provider.Evidence, r *simulation.Result) bool {
	for _, a := range c.in.Allowed {
		if a.Run != e.Run {
			continue
		}
		if e.MetricPath == a.MetricPath {
			return true
		}
		if strings.HasPrefix(e.MetricPath, a.MetricPath+"/") {
			v, err := compare.Resolve(r, e.MetricPath)
			return err == nil && v != nil
		}
	}
	return false
}

// rounded: stated is actual rounded to the decimals stated shows, at
// least least (the sign included), or both are null.
func rounded(stated, actual *float64, least int) bool {
	if stated == nil || actual == nil {
		return stated == nil && actual == nil
	}
	s := strconv.FormatFloat(*stated, 'f', -1, 64)
	decimals := 0
	if i := strings.IndexByte(s, '.'); i >= 0 {
		decimals = len(s) - i - 1
	}
	decimals = max(decimals, least)
	tol := 0.5*math.Pow(10, -float64(decimals)) + 1e-9*math.Max(1, math.Abs(*actual))
	return math.Abs(*stated-*actual) <= tol
}

func clone[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
