package investigator

import (
	"slices"

	"github.com/kcl1104/geminus/internal/results/compare"
)

// RunComparison is investigator.schema.json#/$defs/RunComparison: what the
// server's comparison of a baseline and a what-if run (compare.Compare over
// the stored results, as compareSimulations) shows of an investigation's
// candidate: whether the pair is comparable and why not, the objective's
// metric, at most six headline metrics and the warnings. Only server
// figures.
type RunComparison struct {
	Comparable          bool             `json:"comparable"`
	IncomparableReasons []string         `json:"incomparable_reasons"`
	Objective           *compare.Metric  `json:"objective"`
	Metrics             []compare.Metric `json:"metrics"`
	Warnings            []string         `json:"warnings"`
}

// Headline are the metrics a RunComparison shows, in this order, when the
// comparison has them (PROPOSED, H-061 A7: the contract says "at most six";
// these are the run-level lead time, throughput, failure and rework
// figures every pack's result has).
var Headline = []string{
	"/metrics/lead_time_minutes/p50", "/metrics/lead_time_minutes/mean", "/metrics/lead_time_minutes/p95",
	"/metrics/throughput/mean", "/metrics/failure_count/mean", "/metrics/rework_count/mean",
}

// ComparisonOf is what c shows for an investigation whose objective is the
// metric at objective (an InvestigationObjectiveMetric): the one projection
// the API's candidates and the worker's observation share, so the person
// and the AI see the same figures. A metric c lacks is left out; an
// objective it lacks is null.
func ComparisonOf(c compare.Comparison, objective string) RunComparison {
	out := RunComparison{Comparable: c.Comparable, IncomparableReasons: slices.Clone(c.IncomparableReasons), Metrics: []compare.Metric{},
		Warnings: slices.Clone(c.Warnings)}
	if out.IncomparableReasons == nil {
		out.IncomparableReasons = []string{}
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	if m, ok := c.Metric(objective); ok {
		out.Objective = &m
	}
	for _, path := range Headline {
		if m, ok := c.Metric(path); ok {
			out.Metrics = append(out.Metrics, m)
		}
	}
	return out
}
