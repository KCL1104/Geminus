package investigations

import (
	"cmp"
	"slices"

	"github.com/kcl1104/geminus/internal/results/compare"
	store "github.com/kcl1104/geminus/internal/storage/investigations"
)

// Best is InvestigationBest: the server's best comparable what-if.
type Best struct {
	Index     int            `json:"index"`
	Objective compare.Metric `json:"objective"`
}

// ClosingLine is InvestigationClosingLine: a catalog key and server figures
// only, which the page words in the output locale.
type ClosingLine struct {
	Key            string   `json:"key"`
	CandidateIndex *int     `json:"candidate_index"`
	MetricPath     *string  `json:"metric_path"`
	AbsoluteDelta  *float64 `json:"absolute_delta"`
	RelativeDelta  *float64 `json:"relative_delta"`
	Compared       int      `json:"compared"`
	DraftRevision  *int64   `json:"draft_revision"`
}

// The closing line's keys.
const (
	Improved        = "improved"
	NoImprovement   = "no_improvement"
	NotCompared     = "not_compared"
	DraftWritten    = "draft_written"
	DraftNotWritten = "draft_not_written"
)

// gain is how far m moved the objective the right way (positive: better),
// by its fixed direction; ok is false when m has no delta to rank.
func gain(m *compare.Metric, better string) (abs float64, rel *float64, ok bool) {
	if m == nil || m.Status != compare.Available || m.AbsoluteDelta == nil {
		return 0, nil, false
	}
	sign := 1.0
	if better == "lower" {
		sign = -1
	}
	if m.RelativeDelta != nil {
		r := sign * *m.RelativeDelta
		rel = &r
	}
	return sign * *m.AbsoluteDelta, rel, true
}

// best is the server's ranking (H-056 §9, kept by H-061): of the comparable
// candidates whose objective improved, the largest relative change in the
// objective's fixed direction (a change without a relative figure, as from
// a baseline of 0, after those with one, by its absolute change), then the
// fewer reviewed operations, then the lower index. nil when none improved:
// the server names no best what-if then (PROPOSED, A7).
func best(cs []Candidate, objective *Objective) *Best {
	if objective == nil {
		return nil
	}
	type ranked struct {
		c   Candidate
		abs float64
		rel *float64
	}
	var improved []ranked
	for _, c := range cs {
		if c.Comparison == nil || !c.Comparison.Comparable {
			continue
		}
		if abs, rel, ok := gain(c.Comparison.Objective, objective.Better); ok && abs > 0 {
			improved = append(improved, ranked{c, abs, rel})
		}
	}
	if len(improved) == 0 {
		return nil
	}
	slices.SortStableFunc(improved, func(a, b ranked) int {
		switch {
		case a.rel != nil && b.rel == nil:
			return -1
		case a.rel == nil && b.rel != nil:
			return 1
		case a.rel != nil && *a.rel != *b.rel:
			return cmp.Compare(*b.rel, *a.rel)
		case a.rel == nil && a.abs != b.abs:
			return cmp.Compare(b.abs, a.abs)
		case a.c.operations != b.c.operations:
			return cmp.Compare(a.c.operations, b.c.operations)
		}
		return cmp.Compare(a.c.Index, b.c.Index)
	})
	top := improved[0].c
	return &Best{Index: top.Index, Objective: *top.Comparison.Objective}
}

// closingLine is the server's closing line of an ended investigation:
// in edit mode whether the AI's edit was written into the draft; in
// scenario mode how many what-ifs had a comparable pair and which, if any,
// improved the objective (the best's figures).
func closingLine(mode store.Mode, metric string, cs []Candidate, b *Best) ClosingLine {
	if mode == store.Edit {
		for _, c := range cs {
			if c.AppliedDraftRevision != nil {
				rev := *c.AppliedDraftRevision
				return ClosingLine{Key: DraftWritten, DraftRevision: &rev}
			}
		}
		return ClosingLine{Key: DraftNotWritten}
	}
	compared := 0
	for _, c := range cs {
		if c.Comparison != nil && c.Comparison.Comparable {
			compared++
		}
	}
	m := metric
	switch {
	case compared == 0:
		return ClosingLine{Key: NotCompared, MetricPath: &m}
	case b == nil:
		return ClosingLine{Key: NoImprovement, MetricPath: &m, Compared: compared}
	}
	i := b.Index
	return ClosingLine{Key: Improved, CandidateIndex: &i, MetricPath: &m, AbsoluteDelta: b.Objective.AbsoluteDelta,
		RelativeDelta: b.Objective.RelativeDelta, Compared: compared}
}
