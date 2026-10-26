package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/packs"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/des"
	"github.com/kcl1104/geminus/internal/simulation/engine"
)

// Version names this generator's output rules; a change to what a row
// says, or how rows are chosen and written, is a new version.
const Version = "electronics-line-synthetic-1"

// DefaultSeed is the committed data set's seed. It is the engine's run
// seed of replication 0 (SIMULATION_SPEC §6); runs in tests and demos use
// other seeds, so a run's replications are not the log's.
const DefaultSeed int64 = 52

// The data set (relative to the repository root) and its files.
const (
	DataDir         = "testdata/datasets/electronics-line"
	CSVFile         = "station-log-synthetic.csv"
	ModelFile       = "ground-truth-model.json"
	GroundTruthFile = "ground-truth.json"
	// NegativeRows is how many rows of the log each derived fixture keeps.
	NegativeRows = 40
)

// The ground-truth model: the electronics line pack's template (T-051),
// built by the pack with the template file's own model ID and in English.
const (
	TemplateName = "electronics_assembly_test_line"
	domain       = "electronics_line"
	modelID      = "mdl_01M4YDQK00E1ECTR0N1CS00000"
	modelName    = "Synthetic electronics assembly and test line (ground truth)"
)

// Columns are the CSV's header (README.md says what each holds).
var Columns = []string{"serial", "record_type", "station", "equipment", "start", "end", "result", "shift", "product", "operator", "synthetic"}

// Anchor is model time 0 (Monday 00:00 model local time, D-052) as a
// moment: Monday 2026-09-28 00:00 at +08:00, an offset without daylight
// saving time (ELECTRONICS_LINE §6: a window across a DST change would be
// a data-quality issue).
func Anchor() time.Time { return time.Date(2026, 9, 28, 0, 0, 0, 0, time.FixedZone("+08:00", 8*3600)) }

const (
	dayTicks  = int64(24 * 60 * 60 * 1000)
	weekTicks = 7 * dayTicks
)

// Config is one generation: the engine seed, and how many weeks the log
// covers after one week of warm-up (the line starts empty at time 0).
type Config struct {
	Seed  int64
	Weeks int
}

// DefaultConfig is the committed data set's.
func DefaultConfig() Config { return Config{Seed: DefaultSeed, Weeks: 1} }

// WindowTicks is the observed window [W, H) in ticks since the anchor.
func (c Config) WindowTicks() (w, h int64) { return weekTicks, weekTicks * int64(1+c.Weeks) }

// runInput is the frozen input of one replication of ir with c's seed and
// a horizon of days (warm-up one week).
func (c Config) runInput(ir *model.ModelIR, days int) (simulation.RunInput, error) {
	data, err := model.Encode(ir)
	if err != nil {
		return simulation.RunInput{}, err
	}
	h, err := hashing.Model(data)
	if err != nil {
		return simulation.RunInput{}, err
	}
	return simulation.RunInput{Model: *ir, ModelHash: h, EffectiveModelHash: h,
		Config: simulation.RunConfig{Horizon: simulation.Duration{Value: float64(days), Unit: model.Day},
			Warmup: simulation.Duration{Value: 7, Unit: model.Day}, Replications: 1, Seed: c.Seed, TraceLevel: simulation.TraceNone},
		Limits: simulation.ExecutionLimits{WallTimeoutSeconds: 300, MaxEvents: 10_000_000, MaxActiveEntities: 100_000, MaxEntityVisits: 10_000}}, nil
}

// Output is one generation's files.
type Output struct {
	CSV, Model, GroundTruth []byte
	// Negative are the derived fixtures, by path in the data set.
	Negative map[string][]byte
}

// Files maps each file's path in the data set to its bytes.
func (o *Output) Files() map[string][]byte {
	f := map[string][]byte{CSVFile: o.CSV, ModelFile: o.Model, GroundTruthFile: o.GroundTruth}
	for k, v := range o.Negative {
		f[k] = v
	}
	return f
}

// groundModel is the ground-truth model: the pack's template.
func groundModel() (*model.ModelIR, error) {
	reg, err := packs.Registry()
	if err != nil {
		return nil, err
	}
	p, err := reg.Lookup(domain)
	if err != nil {
		return nil, err
	}
	tp, ok := p.(domainpack.TemplateProvider)
	if !ok {
		return nil, errors.New("the electronics line pack offers no template")
	}
	return tp.Template(TemplateName, modelID, modelName, domainpack.English)
}

// Generate runs replication 0 of the ground-truth model with cfg's seed
// through the engine and writes what it did in the observed window as a
// station log (ELECTRONICS_LINE §6), with the ground truth and the derived
// negative fixtures.
func Generate(ctx context.Context, cfg Config) (*Output, error) {
	if cfg.Weeks < 1 || cfg.Weeks > 8 {
		return nil, errors.New("weeks must be 1 to 8")
	}
	ir, err := groundModel()
	if err != nil {
		return nil, err
	}
	shifts, err := newShiftTable(ir)
	if err != nil {
		return nil, err
	}
	// One day past the window: every step begun in it has ended.
	days := 7*(1+cfg.Weeks) + 1
	in, err := cfg.runInput(ir, days)
	if err != nil {
		return nil, err
	}
	prog, err := engine.Compile(in)
	if err != nil {
		return nil, err
	}
	obs, err := newObserver(prog, ir, shifts)
	if err != nil {
		return nil, err
	}
	if _, err := engine.RunReplication(ctx, prog, 0, engine.NewBudget(in.Limits.MaxEvents), obs); err != nil {
		return nil, err
	}
	if obs.err != nil {
		return nil, obs.err
	}
	w, h := cfg.WindowTicks()
	var recs []*record
	for _, r := range obs.records {
		if r.start < w || r.start >= h {
			continue
		}
		if r.end < 0 {
			return nil, fmt.Errorf("a %s at %s begun in the window had not ended at the horizon", r.recordType, r.station)
		}
		recs = append(recs, r)
	}
	slices.SortStableFunc(recs, func(a, b *record) int {
		if a.start != b.start {
			return int(a.start - b.start) // ticks within weeks: no overflow
		}
		return a.seq - b.seq
	})
	csv, err := render(recs)
	if err != nil {
		return nil, err
	}
	negative, err := derive(csv)
	if err != nil {
		return nil, err
	}
	modelJSON, err := json.Marshal(ir, json.Deterministic(true), jsontext.EscapeForHTML(false), jsontext.WithIndent("  "))
	if err != nil {
		return nil, err
	}
	modelJSON = append(modelJSON, '\n')
	enc, err := model.Encode(ir)
	if err != nil {
		return nil, err
	}
	hash, err := hashing.Model(enc)
	if err != nil {
		return nil, err
	}
	gt, err := truth(cfg, hash, days, recs, csv, negative)
	if err != nil {
		return nil, err
	}
	return &Output{CSV: csv, Model: modelJSON, GroundTruth: gt, Negative: negative}, nil
}

func stamp(tick int64) string {
	return Anchor().Add(time.Duration(tick) * time.Millisecond).Truncate(time.Second).Format(time.RFC3339)
}

// render writes the log: the header, then one row per record, LF, every
// field plain (no quoting is ever needed: the generator writes no comma,
// quote or line break).
func render(recs []*record) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(strings.Join(Columns, ",") + "\n")
	for _, r := range recs {
		fields := []string{r.serial, r.recordType, r.station, r.equipment, stamp(r.start), stamp(r.end), r.result, r.shift, r.product, r.operator, "true"}
		for _, f := range fields {
			if strings.ContainsAny(f, ",\"\r\n") {
				return nil, fmt.Errorf("a field needs quoting: %q", f)
			}
		}
		b.WriteString(strings.Join(fields, ",") + "\n")
	}
	return b.Bytes(), nil
}

// fixture is one derived negative fixture: the column it changes, which
// row (the first of the head that matches) and the new value.
type fixture struct {
	name   string
	column string
	match  func(f []string) bool
	value  func(f []string) (string, error)
}

func col(name string) int { return slices.Index(Columns, name) }

func operation(f []string) bool { return f[col("record_type")] == "operation" }

func testStep(f []string) bool {
	return operation(f) && slices.Contains([]string{"aoi", "ict", "fct", "burn_in"}, f[col("station")])
}

func constant(v string) func([]string) (string, error) {
	return func([]string) (string, error) { return v, nil }
}

// fixtures are the negative cases of D-072 (b) (an invalid row) and of
// the pack's data-quality rules of D-059 (warnings).
var fixtures = []fixture{
	{"negative/end-before-start.csv", "end", operation, func(f []string) (string, error) {
		start, err := time.Parse(time.RFC3339, f[col("start")])
		if err != nil {
			return "", err
		}
		return start.Add(-time.Minute).Format(time.RFC3339), nil
	}},
	{"negative/unknown-station.csv", "station", operation, constant("xray")},
	{"negative/unknown-product.csv", "product", operation, constant("board_c")},
	{"negative/missing-product.csv", "product", operation, constant("")},
	{"negative/unknown-record-type.csv", "record_type", operation, constant("maintenance")},
	{"negative/missing-test-result.csv", "result", testStep, constant("")},
	{"negative/unknown-test-result.csv", "result", testStep, constant("PASS")},
}

// derive makes each fixture from the head of the log (header and the first
// NegativeRows rows), changing one cell of one row.
func derive(csv []byte) (map[string][]byte, error) {
	lines := strings.SplitN(string(csv), "\n", NegativeRows+2)
	if len(lines) < NegativeRows+2 {
		return nil, errors.New("the log is shorter than a fixture")
	}
	head := lines[:NegativeRows+1]
	out := map[string][]byte{}
	for _, fx := range fixtures {
		rows := slices.Clone(head)
		done := false
		for i := 1; i < len(rows) && !done; i++ {
			f := strings.Split(rows[i], ",")
			if !fx.match(f) {
				continue
			}
			v, err := fx.value(f)
			if err != nil {
				return nil, err
			}
			f[col(fx.column)] = v
			rows[i] = strings.Join(f, ",")
			done = true
		}
		if !done {
			return nil, fmt.Errorf("%s: no row of the head to change", fx.name)
		}
		out[fx.name] = []byte(strings.Join(rows, "\n") + "\n")
	}
	return out, nil
}

type fileInfo struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Rows   int    `json:"rows"`
	Note   string `json:"note,omitzero"`
}

type groundTruth struct {
	Synthetic bool   `json:"synthetic"`
	Note      string `json:"note"`
	Generator struct {
		Version       string `json:"version"`
		Command       string `json:"command"`
		Seed          int64  `json:"seed"`
		Replication   int    `json:"replication"`
		EngineVersion string `json:"engine_version"`
		HorizonDays   int    `json:"engine_horizon_days"`
	} `json:"generator"`
	Model struct {
		File      string `json:"file"`
		Domain    string `json:"domain"`
		Template  string `json:"template"`
		ModelHash string `json:"model_hash"`
		Note      string `json:"note"`
	} `json:"model"`
	Anchor string `json:"anchor"`
	Window struct {
		Start string `json:"start"`
		End   string `json:"end"`
		Weeks int    `json:"weeks"`
		Note  string `json:"note"`
	} `json:"window"`
	Rules   []string            `json:"rules"`
	Files   map[string]fileInfo `json:"files"`
	Summary summary             `json:"summary"`
}

// summary counts what the log holds (facts of the file, not of a plant).
type summary struct {
	Operations         int            `json:"operations"`
	Downtimes          int            `json:"downtimes"`
	Boards             int            `json:"boards"`
	BoardsStartedAtSMT map[string]int `json:"boards_started_at_smt"`
	BoardsShipped      map[string]int `json:"boards_shipped"`
	BoardsScrapped     int            `json:"boards_scrapped"`
	OperationsBy       map[string]int `json:"operations_by_station"`
	FailuresBy         map[string]int `json:"test_failures_by_station"`
	DowntimesBy        map[string]int `json:"downtimes_by_equipment"`
	DowntimeMinutesBy  map[string]int `json:"downtime_minutes_by_equipment"`
	SMTFamilyChanges   int            `json:"smt_family_changes"`
}

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

func truth(cfg Config, hash string, days int, recs []*record, csv []byte, negative map[string][]byte) ([]byte, error) {
	var gt groundTruth
	gt.Synthetic = true
	gt.Note = "Synthetic data for demonstration and tests (T-052, H-016, H-029): generated by the engine from the ground-truth model below; " +
		"NOT customer data, plant measurements or typical values. Every row is marked synthetic."
	gt.Generator.Version = Version
	gt.Generator.Command = fmt.Sprintf("go run ./scripts/gen-electronics-line-data -seed %d -weeks %d", cfg.Seed, cfg.Weeks)
	gt.Generator.Seed = cfg.Seed
	gt.Generator.EngineVersion = des.Version
	gt.Generator.HorizonDays = days
	gt.Model.File = ModelFile
	gt.Model.Domain = domain
	gt.Model.Template = TemplateName
	gt.Model.ModelHash = hash
	gt.Model.Note = "The electronics line pack's template (ELECTRONICS_LINE §4.1, §4.2, §4.4; T-051), all parameters synthetic. " +
		"A model created from the template has another model ID and name, and so another hash; what the engine runs is the same."
	gt.Anchor = Anchor().Format(time.RFC3339)
	w, h := cfg.WindowTicks()
	gt.Window.Start, gt.Window.End, gt.Window.Weeks = stamp(w), stamp(h), cfg.Weeks
	gt.Window.Note = "Rows are the steps and breakdowns that start in [start, end); the line starts empty at the anchor, and the week before the window is warm-up."
	gt.Rules = []string{
		"The log is what the engine's replication 0 of the ground-truth model did with the seed above (an engine Observer, never a second simulation).",
		"Times: model time 0 is the anchor; a tick is 1 ms; timestamps are RFC 3339 at +08:00, cut to whole seconds.",
		"operation: one board's step at a station: start is the service start (after the SMT line's changeover, so a changeover is the gap before the first board of the other family), end the service end.",
		"downtime: one machine's breakdown: serial and equipment are the machine, start the moment it went down, end its repair; result is the reason code breakdown.",
		"station: the step's domain attribute station (smt, aoi, ict, fct, burn_in, pack; a retest is logged at its station), or the step's ID for touch-up and debug (aoi_touchup, ict_debug, fct_debug).",
		"equipment: <machine resource ID>_<unit>, unit 1 being the idle one of least index the step took; empty for steps of people (touch-up, debug, packing).",
		"operator: op-NN for steps of people (by shift, resource and unit); synthetic, and never imported (ELECTRONICS_LINE §6: operator IDs stay unmapped).",
		"result: pass or fail at a test (AOI, ICT, FCT, burn-in, and their retests); at debug pass is repaired and fail scrapped; empty elsewhere.",
		"shift: the line calendar's label (early 06:00-14:00, late 14:00-22:00, Monday to Friday) at the row's start; empty outside the shifts (burn-in at night, a breakdown off shift).",
		"product: the board's family, the source's entity type (board_a, board_b); serial is SYN-<family letter>-<arrival number at its source>.",
		"Each negative fixture is the header and the first rows of the log with one cell changed (its name says which).",
	}
	gt.Files = map[string]fileInfo{CSVFile: {SHA256: digest(csv), Bytes: int64(len(csv)), Rows: len(recs)}}
	for name, data := range negative {
		gt.Files[name] = fileInfo{SHA256: digest(data), Bytes: int64(len(data)), Rows: NegativeRows, Note: "derived; see rules"}
	}
	s := summary{BoardsStartedAtSMT: map[string]int{}, BoardsShipped: map[string]int{}, OperationsBy: map[string]int{},
		FailuresBy: map[string]int{}, DowntimesBy: map[string]int{}, DowntimeMinutesBy: map[string]int{}}
	boards := map[string]bool{}
	lastSMT := ""
	var downMS = map[string]int64{}
	for _, r := range recs {
		if r.recordType == "downtime" {
			s.Downtimes++
			s.DowntimesBy[r.equipment]++
			downMS[r.equipment] += r.end - r.start
			continue
		}
		s.Operations++
		boards[r.serial] = true
		s.OperationsBy[r.station]++
		switch {
		case r.station == "smt":
			s.BoardsStartedAtSMT[r.product]++
			if lastSMT != "" && lastSMT != r.product {
				s.SMTFamilyChanges++
			}
			lastSMT = r.product
		case r.station == "pack":
			s.BoardsShipped[r.product]++
		}
		if r.result == "fail" {
			if obsTest(r.station) {
				s.FailuresBy[r.station]++
			} else {
				s.BoardsScrapped++
			}
		}
	}
	for k, v := range downMS {
		s.DowntimeMinutesBy[k] = int(v / 60000)
	}
	s.Boards = len(boards)
	gt.Summary = s
	data, err := json.Marshal(gt, json.Deterministic(true), jsontext.EscapeForHTML(false), jsontext.WithIndent("  "))
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func obsTest(station string) bool {
	return slices.Contains([]string{"aoi", "ict", "fct", "burn_in"}, station)
}
