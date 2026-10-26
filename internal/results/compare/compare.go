// Package compare is the baseline/scenario comparison of two completed
// runs (T-029; SIMULATION_SPEC §7, D-010 APPROVED; the details are D-088
// and D-090, APPROVED, H-024) and the metric paths an analysis may cite (AI_SPEC
// §9; the analyst service is T-030's). It is pure: it reads what the caller
// read in its tenant scope (each run's frozen input and its Result) and
// returns http.schema.json#/$defs/Comparison; it does no I/O and keeps no
// state, so the API (internal/comparisons) and a later analysis job give
// the same answer for the same runs.
//
// Two runs are comparable when they share the model version, the engine
// version, the dataset manifest, the horizon and the warmup (in ticks), the
// replication count and the seed, and the baseline's frozen patch is []
// (§7; only the scenario patch is the experiment's variable). Otherwise the
// comparison says why, keeps both runs' values side by side and gives no
// delta. Each statistic of either Result's metrics (a summary's mean, p50
// and p95, a resource's utilization, a queue's waits and length) is one
// comparison metric named by its RFC 6901 JSON Pointer from the Result's
// root: absolute_delta = scenario − baseline and relative_delta =
// absolute_delta / baseline, computed once here so that neither the UI nor
// the AI invents them; a baseline of 0 has no relative delta, and an
// unavailable or missing value has no delta at all. Confidence intervals
// do not enter the delta (P0 defines no paired CI, §7).
//
// The order of the metrics is fixed (groups, then map keys in byte order,
// then statistics), never Go's map order, so the same runs always give the
// same bytes.
package compare

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
)

// Run is what the comparison knows of one completed run: its frozen input
// (the run row: version, engine, manifest, patch, configuration) and the
// Result stored for it.
type Run struct {
	ID                  string
	ModelVersionID      string
	EngineVersion       string
	DatasetManifestHash string
	ModelHash           string // the pinned version's
	EffectiveModelHash  string // the patched model's
	ConfigHash          string
	ScenarioPatch       []byte // the frozen JSON Patch array; [] for a baseline
	Config              simulation.RunConfig
	Result              *simulation.Result
}

// Status is ComparisonMetric.status.
type Status string

const (
	Available    Status = "available"
	Unavailable  Status = "unavailable"
	Incomparable Status = "incomparable"
)

// Reasons of a comparison metric (http.schema.json#/$defs/ComparisonReason).
const (
	// ReasonBaselineZero: the baseline is 0, so there is no relative delta
	// (§7); the absolute delta stays.
	ReasonBaselineZero = "BASELINE_ZERO"
	// ReasonDeltaOutOfRange, ReasonRelativeDeltaOutOfRange: the delta is
	// outside the contract's ±MaxValue (or not finite); it is null, never
	// clipped.
	ReasonDeltaOutOfRange         = "DELTA_OUT_OF_RANGE"
	ReasonRelativeDeltaOutOfRange = "RELATIVE_DELTA_OUT_OF_RANGE"
	// The statistic is unavailable in that run (its summary says why).
	ReasonBaselineUnavailable = "BASELINE_UNAVAILABLE"
	ReasonScenarioUnavailable = "SCENARIO_UNAVAILABLE"
	ReasonBothUnavailable     = "BOTH_UNAVAILABLE"
	// The metric is not in that run's Result at all.
	ReasonMissingInBaseline = "MISSING_IN_BASELINE"
	ReasonMissingInScenario = "MISSING_IN_SCENARIO"
	// The runs are not comparable (Comparison.IncomparableReasons).
	ReasonRunsNotComparable = "RUNS_NOT_COMPARABLE"
)

// Why two runs are not comparable (http.schema.json
// #/$defs/IncomparableReason), in the order they are listed.
const (
	ModelVersionMismatch    = "MODEL_VERSION_MISMATCH"
	EngineVersionMismatch   = "ENGINE_VERSION_MISMATCH"
	DatasetManifestMismatch = "DATASET_MANIFEST_MISMATCH"
	HorizonMismatch         = "HORIZON_MISMATCH"
	WarmupMismatch          = "WARMUP_MISMATCH"
	ReplicationsMismatch    = "REPLICATIONS_MISMATCH"
	SeedMismatch            = "SEED_MISMATCH"
	BaselinePatchNotEmpty   = "BASELINE_PATCH_NOT_EMPTY"
)

// Warnings (http.schema.json#/$defs/ComparisonWarning), in the order they
// are listed.
const (
	// WarningScenarioPatchEmpty: the scenario run's patch is [] too, so
	// nothing was varied.
	WarningScenarioPatchEmpty = "SCENARIO_PATCH_EMPTY"
	// WarningSingleReplication: a run has one replication, so neither has a
	// confidence interval and a difference cannot be told from noise.
	WarningSingleReplication = "SINGLE_REPLICATION"
	// WarningCensoredEntities: a replication of a run ended with active
	// entities; lead times and waits describe the completed cohort only.
	WarningCensoredEntities = "CENSORED_ENTITIES"
	// WarningMetricsTruncated: more statistics than MaxMetrics; the first
	// MaxMetrics in the comparison's order are listed.
	WarningMetricsTruncated = "METRICS_TRUNCATED"
)

// MaxMetrics is Comparison.metrics' maxItems.
const MaxMetrics = 10_000

// MaxValue bounds every number of a Result's metrics and of a comparison
// (the schemas' ±1e15).
const MaxValue = 1e15

// Metric is http.schema.json#/$defs/ComparisonMetric. Null (nil) means "no
// value", never 0.
type Metric struct {
	MetricPath    string   `json:"metric_path"`
	Baseline      *float64 `json:"baseline"`
	Scenario      *float64 `json:"scenario"`
	AbsoluteDelta *float64 `json:"absolute_delta"`
	RelativeDelta *float64 `json:"relative_delta"`
	Status        Status   `json:"status"`
	Reason        *string  `json:"reason"`
}

// Comparison is http.schema.json#/$defs/Comparison.
type Comparison struct {
	BaselineRunID       string   `json:"baseline_run_id"`
	ScenarioRunID       string   `json:"scenario_run_id"`
	Comparable          bool     `json:"comparable"`
	IncomparableReasons []string `json:"incomparable_reasons"`
	Metrics             []Metric `json:"metrics"`
	Warnings            []string `json:"warnings"`
}

var (
	// ErrInconsistent: a run's Result does not match the run's own frozen
	// input, or breaks the Result contract (an available summary without a
	// value, a value out of range); it is never compared (a server fault).
	ErrInconsistent = errors.New("compare: a run's result does not match its frozen input")
	// ErrSameRun: the baseline and the scenario are one run.
	ErrSameRun = errors.New("compare: the baseline and the scenario are the same run")
	// ErrUnknownPath: the path is not the metric path of a statistic of the
	// result (malformed, not under /metrics, not a statistic, or a key the
	// result does not have).
	ErrUnknownPath = errors.New("compare: not a metric path of the result")
)

// group is one member of Result.metrics: the statistics it has and whether
// it is a map keyed by resource, queue or metric ID.
type group struct {
	name  string
	keyed bool
	stats []string
}

var (
	summaryStats = []string{"mean", "p50", "p95"}
	queueStats   = []string{"wait_mean_minutes", "wait_p50_minutes", "wait_p95_minutes", "queue_length_mean"}
)

// groups in the comparison's order: the run-level metrics first, then the
// keyed maps, the largest (resources, queues) last, so a truncation
// (MaxMetrics) keeps the run-level ones.
var groups = []group{
	{"throughput", false, summaryStats},
	{"lead_time_minutes", false, summaryStats},
	{"failure_count", false, summaryStats},
	{"rework_count", false, summaryStats},
	{"additional_metrics", true, summaryStats},
	{"dora", true, summaryStats},
	{"domain", true, summaryStats},
	{"resources", true, []string{"utilization_mean"}},
	{"queues", true, queueStats},
}

// Path is the RFC 6901 JSON Pointer of tokens ("~" as "~0", "/" as "~1").
func Path(tokens ...string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(strings.NewReplacer("~", "~0", "/", "~1").Replace(t))
	}
	return b.String()
}

// tokens splits a JSON Pointer; malformed escapes are an error, so a
// pointer that parses is the only spelling of its tokens.
func tokens(path string) ([]string, bool) {
	if !strings.HasPrefix(path, "/") {
		return nil, false
	}
	parts := strings.Split(path[1:], "/")
	for i, p := range parts {
		for j := 0; j < len(p); j++ {
			if p[j] == '~' && (j+1 == len(p) || (p[j+1] != '0' && p[j+1] != '1')) {
				return nil, false
			}
		}
		parts[i] = strings.NewReplacer("~1", "/", "~0", "~").Replace(p)
	}
	return parts, true
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) <= MaxValue }

// value is a copy of v with -0 written as 0, or nil.
func value(v *float64) *float64 {
	if v == nil {
		return nil
	}
	x := *v
	if x == 0 {
		x = 0
	}
	return &x
}

func inconsistent(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInconsistent, fmt.Sprintf(format, args...))
}

// summaryStat is one statistic of a MetricSummary: the value when the
// summary is available, nil when it is unavailable.
func summaryStat(s simulation.MetricSummary, stat, at string) (*float64, error) {
	var v *float64
	switch stat {
	case "mean":
		v = s.Mean
	case "p50":
		v = s.P50
	case "p95":
		v = s.P95
	}
	switch s.Status {
	case simulation.Available:
		if v == nil || !finite(*v) {
			return nil, inconsistent("%s: an available summary without a finite %s", at, stat)
		}
		return value(v), nil
	case simulation.Unavailable:
		if v != nil {
			return nil, inconsistent("%s: an unavailable summary with a %s", at, stat)
		}
		return nil, nil
	}
	return nil, inconsistent("%s: summary status %q", at, s.Status)
}

func queueStat(q simulation.QueueStats, stat, at string) (*float64, error) {
	if stat == "queue_length_mean" {
		// A time integral: known whether or not a visit completed.
		if !finite(q.QueueLengthMean) {
			return nil, inconsistent("%s: queue length %v", at, q.QueueLengthMean)
		}
		return value(&q.QueueLengthMean), nil
	}
	var v *float64
	switch stat {
	case "wait_mean_minutes":
		v = q.WaitMeanMinutes
	case "wait_p50_minutes":
		v = q.WaitP50Minutes
	case "wait_p95_minutes":
		v = q.WaitP95Minutes
	}
	switch q.Status {
	case simulation.Available:
		if v == nil || !finite(*v) {
			return nil, inconsistent("%s: an available queue without a finite %s", at, stat)
		}
		return value(v), nil
	case simulation.Unavailable:
		if v != nil {
			return nil, inconsistent("%s: an unavailable queue with a %s", at, stat)
		}
		return nil, nil
	}
	return nil, inconsistent("%s: queue status %q", at, q.Status)
}

// keys returns the keys of group g's map in m, in byte order.
func keys(m simulation.Metrics, g group) []string {
	var out []string
	switch g.name {
	case "additional_metrics":
		for k := range m.AdditionalMetrics {
			out = append(out, k)
		}
	case "dora":
		for k := range m.DORA {
			out = append(out, k)
		}
	case "domain":
		for k := range m.Domain {
			out = append(out, k)
		}
	case "resources":
		for k := range m.Resources {
			out = append(out, k)
		}
	case "queues":
		for k := range m.Queues {
			out = append(out, k)
		}
	}
	slices.Sort(out) // byte order, whatever the map's
	return out
}

// lookup returns statistic stat of key in group g of m: present reports
// whether m has the key (always, for a group without keys); the value is
// nil when it is unavailable.
func lookup(m simulation.Metrics, g group, key, stat string) (v *float64, present bool, err error) {
	at := Path("metrics", g.name, key, stat)
	if !g.keyed {
		at = Path("metrics", g.name, stat)
	}
	var s simulation.MetricSummary
	switch g.name {
	case "throughput":
		s, present = m.Throughput, true
	case "lead_time_minutes":
		s, present = m.LeadTimeMinutes, true
	case "failure_count":
		s, present = m.FailureCount, true
	case "rework_count":
		s, present = m.ReworkCount, true
	case "additional_metrics":
		s, present = m.AdditionalMetrics[key]
	case "dora":
		s, present = m.DORA[key]
	case "domain":
		s, present = m.Domain[key]
	case "resources":
		r, ok := m.Resources[key]
		if !ok {
			return nil, false, nil
		}
		if r.UtilizationMean != nil && !finite(*r.UtilizationMean) {
			return nil, true, inconsistent("%s: utilization %v", at, *r.UtilizationMean)
		}
		return value(r.UtilizationMean), true, nil
	case "queues":
		q, ok := m.Queues[key]
		if !ok {
			return nil, false, nil
		}
		v, err := queueStat(q, stat, at)
		return v, true, err
	}
	if !present {
		return nil, false, nil
	}
	v, err = summaryStat(s, stat, at)
	return v, true, err
}

// Statistic is one statistic of a Result: its metric path and its value
// (nil when unavailable, never 0).
type Statistic struct {
	Path  string
	Value *float64
}

// Statistics lists every statistic of r's metrics in the comparison's
// order: the metric paths an analysis may cite as evidence of r. Metrics
// that break the contract Compare checks them against (metricsRef) are
// ErrInconsistent: nothing is listed from them (review round 2 R2-L1).
func Statistics(r *simulation.Result) ([]Statistic, error) {
	if r == nil {
		return nil, inconsistent("no result")
	}
	if err := validMetrics(r.RunID, r.Metrics); err != nil {
		return nil, err
	}
	var out []Statistic
	for _, g := range groups {
		ks := []string{""}
		if g.keyed {
			ks = keys(r.Metrics, g)
		}
		for _, k := range ks {
			for _, stat := range g.stats {
				v, _, err := lookup(r.Metrics, g, k, stat)
				if err != nil {
					return nil, err
				}
				out = append(out, Statistic{Path: statPath(g, k, stat), Value: v})
			}
		}
	}
	return out, nil
}

func statPath(g group, key, stat string) string {
	if !g.keyed {
		return Path("metrics", g.name, stat)
	}
	return Path("metrics", g.name, key, stat)
}

// Resolve returns the statistic of r that path names: its value, or nil
// when it is unavailable. A path that is not the metric path of one of r's
// statistics (Statistics) is ErrUnknownPath: a summary rather than one of
// its statistics, a member that is not a statistic (sample_count, the CI,
// unit, reason, capacity), a key r lacks, a malformed or differently
// spelled pointer, anything outside /metrics. Metrics that break the
// contract Compare checks them against (metricsRef) are ErrInconsistent,
// whatever the path (review round 2 R2-L1).
func Resolve(r *simulation.Result, path string) (*float64, error) {
	if r == nil {
		return nil, inconsistent("no result")
	}
	if err := validMetrics(r.RunID, r.Metrics); err != nil {
		return nil, err
	}
	t, ok := tokens(path)
	if !ok || len(t) < 3 || t[0] != "metrics" {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPath, path)
	}
	i := slices.IndexFunc(groups, func(g group) bool { return g.name == t[1] })
	if i < 0 {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPath, path)
	}
	g := groups[i]
	key, stat := "", t[2]
	if g.keyed {
		if len(t) != 4 {
			return nil, fmt.Errorf("%w: %q", ErrUnknownPath, path)
		}
		key, stat = t[2], t[3]
	} else if len(t) != 3 {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPath, path)
	}
	if !slices.Contains(g.stats, stat) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPath, path)
	}
	v, present, err := lookup(r.Metrics, g, key, stat)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPath, path)
	}
	return v, nil
}

// emptyPatch reports whether a frozen patch is the baseline's [].
// A JSON null decodes to an empty slice too, so the value's kind is
// checked first: only an array is a patch (review round 1 L5; 0001's CHECK
// keeps a stored run's patch an array, but Compare is shared).
func emptyPatch(id string, patch []byte) (bool, error) {
	var ops []jsontext.Value
	if t := bytes.TrimLeft(patch, " \t\r\n"); len(t) == 0 || t[0] != '[' {
		return false, inconsistent("run %s: the frozen patch is not a JSON array", id)
	}
	if err := json.Unmarshal(patch, &ops); err != nil {
		return false, inconsistent("run %s: the frozen patch is not a JSON array", id)
	}
	return len(ops) == 0, nil
}

// metricsRef is the part of the Result contract the comparison reads every
// value of.
const metricsRef = "simulation.schema.json#/$defs/Result/properties/metrics"

// validMetrics checks the metrics of run id's Result against metricsRef:
// the one check Compare (check), Statistics and Resolve each make before
// reading a value (review round 1 I2, round 2 R2-L1; D-090 (a)).
func validMetrics(id string, m simulation.Metrics) error {
	// The Go types hold exactly the schema's members (contract tests), and
	// a stored Result is decoded strictly, so its metrics re-encode to what
	// was stored, as far as the schema can tell.
	data, err := json.Marshal(m)
	if err != nil {
		return inconsistent("run %q: the result's metrics do not encode (a number that is not finite)", id)
	}
	issues, err := validate.Document(data, metricsRef, "RESULT_INCONSISTENT")
	if err != nil {
		return err
	}
	if len(issues) > 0 {
		// The path only: the message may quote a value.
		return inconsistent("run %q: the result's metrics break the contract at %q (%d issue(s))", id, "/metrics"+issues[0].Path, len(issues))
	}
	return nil
}

// check returns a run's measurement window in ticks after checking that
// its Result is the run's own and keeps its contract where the comparison
// reads it (D-089 (b)): its ID, status and engine; its seed, replication
// count (and summaries), dataset manifest, model, effective model and
// config hashes; its window's unit, boundary and ticks against the run's
// frozen configuration; and its metrics against simulation.schema.json
// (review round 1 I1, I2). The other members (each replication's values
// but its censored count, the trace) are not read, so not checked.
func check(r Run) (warmup, horizon int64, err error) {
	res := r.Result
	if res == nil {
		return 0, 0, inconsistent("run %s has no result", r.ID)
	}
	if res.RunID != r.ID || res.Status != "completed" || res.EngineVersion != r.EngineVersion {
		return 0, 0, inconsistent("run %s: the result is of run %q, %q, engine %q", r.ID, res.RunID, res.Status, res.EngineVersion)
	}
	rep := res.Reproducibility
	if rep.Seed != r.Config.Seed || rep.Replications != r.Config.Replications || len(res.ReplicationSummaries) != r.Config.Replications ||
		rep.DatasetManifestHash != r.DatasetManifestHash {
		return 0, 0, inconsistent("run %s: the result's seed, replications or manifest are not the run's", r.ID)
	}
	if rep.ModelHash != r.ModelHash || rep.EffectiveModelHash != r.EffectiveModelHash || rep.ConfigHash != r.ConfigHash {
		return 0, 0, inconsistent("run %s: the result's model, effective model or config hash is not the run's", r.ID)
	}
	mw := res.MeasurementWindow
	h, herr := model.Ticks(r.Config.Horizon.Value, r.Config.Horizon.Unit)
	w, werr := model.Ticks(r.Config.Warmup.Value, r.Config.Warmup.Unit)
	if herr != nil || werr != nil || mw.TickUnit != "millisecond" || mw.TimeBoundary != "warmup <= t <= horizon" || mw.HorizonTicks != h ||
		mw.WarmupTicks != w {
		return 0, 0, inconsistent("run %s: the result's window is not the run's configuration", r.ID)
	}
	if err := validMetrics(r.ID, res.Metrics); err != nil {
		return 0, 0, err
	}
	return w, h, nil
}

func censored(r *simulation.Result) bool {
	return slices.ContainsFunc(r.ReplicationSummaries, func(s simulation.ReplicationSummary) bool { return s.CensoredEntities > 0 })
}

// Compare compares the scenario run with the baseline run.
func Compare(baseline, scenario Run) (Comparison, error) {
	if baseline.ID == scenario.ID {
		return Comparison{}, ErrSameRun
	}
	bw, bh, err := check(baseline)
	if err != nil {
		return Comparison{}, err
	}
	sw, sh, err := check(scenario)
	if err != nil {
		return Comparison{}, err
	}
	baseEmpty, err := emptyPatch(baseline.ID, baseline.ScenarioPatch)
	if err != nil {
		return Comparison{}, err
	}
	scenEmpty, err := emptyPatch(scenario.ID, scenario.ScenarioPatch)
	if err != nil {
		return Comparison{}, err
	}
	reasons := []string{}
	for _, c := range []struct {
		differ bool
		code   string
	}{
		{baseline.ModelVersionID != scenario.ModelVersionID, ModelVersionMismatch},
		{baseline.EngineVersion != scenario.EngineVersion, EngineVersionMismatch},
		{baseline.DatasetManifestHash != scenario.DatasetManifestHash, DatasetManifestMismatch},
		{bh != sh, HorizonMismatch},
		{bw != sw, WarmupMismatch},
		{baseline.Config.Replications != scenario.Config.Replications, ReplicationsMismatch},
		{baseline.Config.Seed != scenario.Config.Seed, SeedMismatch},
		{!baseEmpty, BaselinePatchNotEmpty},
	} {
		if c.differ {
			reasons = append(reasons, c.code)
		}
	}
	comparable := len(reasons) == 0
	b, s := baseline.Result.Metrics, scenario.Result.Metrics
	metrics := []Metric{}
	for _, g := range groups {
		ks := []string{""}
		if g.keyed {
			// The union of both runs' keys, in byte order.
			ks = append(keys(b, g), keys(s, g)...)
			slices.Sort(ks)
			ks = slices.Compact(ks)
		}
		for _, k := range ks {
			for _, stat := range g.stats {
				bv, bok, err := lookup(b, g, k, stat)
				if err != nil {
					return Comparison{}, fmt.Errorf("baseline %s: %w", baseline.ID, err)
				}
				sv, sok, err := lookup(s, g, k, stat)
				if err != nil {
					return Comparison{}, fmt.Errorf("scenario %s: %w", scenario.ID, err)
				}
				metrics = append(metrics, metric(statPath(g, k, stat), bv, sv, bok, sok, comparable))
			}
		}
	}
	warnings := []string{}
	if scenEmpty {
		warnings = append(warnings, WarningScenarioPatchEmpty)
	}
	if baseline.Config.Replications < 2 || scenario.Config.Replications < 2 {
		warnings = append(warnings, WarningSingleReplication)
	}
	if censored(baseline.Result) || censored(scenario.Result) {
		warnings = append(warnings, WarningCensoredEntities)
	}
	if len(metrics) > MaxMetrics {
		metrics = metrics[:MaxMetrics]
		warnings = append(warnings, WarningMetricsTruncated)
	}
	return Comparison{BaselineRunID: baseline.ID, ScenarioRunID: scenario.ID, Comparable: comparable, IncomparableReasons: reasons,
		Metrics: metrics, Warnings: warnings}, nil
}

func reason(code string) *string { return &code }

// metric compares one statistic: b and s are its values (nil:
// unavailable), bok and sok whether each run has the metric at all.
func metric(path string, b, s *float64, bok, sok, comparable bool) Metric {
	m := Metric{MetricPath: path, Baseline: b, Scenario: s}
	switch {
	case !comparable:
		m.Status, m.Reason = Incomparable, reason(ReasonRunsNotComparable)
	case !bok:
		m.Status, m.Reason = Unavailable, reason(ReasonMissingInBaseline)
	case !sok:
		m.Status, m.Reason = Unavailable, reason(ReasonMissingInScenario)
	case b == nil && s == nil:
		m.Status, m.Reason = Unavailable, reason(ReasonBothUnavailable)
	case b == nil:
		m.Status, m.Reason = Unavailable, reason(ReasonBaselineUnavailable)
	case s == nil:
		m.Status, m.Reason = Unavailable, reason(ReasonScenarioUnavailable)
	default:
		m.Status = Available
		abs := *s - *b
		if !finite(abs) {
			m.Reason = reason(ReasonDeltaOutOfRange)
			return m
		}
		m.AbsoluteDelta = value(&abs)
		if *b == 0 {
			m.Reason = reason(ReasonBaselineZero)
			return m
		}
		rel := abs / *b
		if !finite(rel) {
			m.Reason = reason(ReasonRelativeDeltaOutOfRange)
			return m
		}
		m.RelativeDelta = value(&rel)
	}
	return m
}

// Metric returns the comparison metric of path.
func (c Comparison) Metric(path string) (Metric, bool) {
	i := slices.IndexFunc(c.Metrics, func(m Metric) bool { return m.MetricPath == path })
	if i < 0 {
		return Metric{}, false
	}
	return c.Metrics[i], true
}

// JSON is c in RFC 8785 form, the bytes the API answers.
func (c Comparison) JSON() ([]byte, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return hashing.Canonical(data)
}
