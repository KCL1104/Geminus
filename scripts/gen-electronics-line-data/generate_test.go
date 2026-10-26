package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/simulation/des"
)

// The committed data set (testdata/datasets/electronics-line) is the
// generator's output for DefaultConfig: its CSV's SHA-256 is pinned here.
// The pin is a regression lock on this generator and des-go, not an
// oracle: TestTheLogIsTheEngineReplication and the shape tests below are
// the independent checks of what the log says.
const goldenCSVSHA256 = "sha256:7ad4a396aa034c4b1538377dba593aa9e55b8a2e3d34e4be0f06378ce1214cc5"

func generate(t *testing.T, cfg Config) *Output {
	t.Helper()
	out, err := Generate(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

var defaultOutput *Output

// def is DefaultConfig's output, generated once for the package's tests.
func def(t *testing.T) *Output {
	t.Helper()
	if defaultOutput == nil {
		defaultOutput = generate(t, DefaultConfig())
	}
	return defaultOutput
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

// Same seed, same bytes (every file); the CSV's hash is the pinned one; a
// different seed gives another log.
func TestGenerateIsDeterministic(t *testing.T) {
	a := def(t)
	b := generate(t, DefaultConfig())
	for name, data := range a.Files() {
		if !bytes.Equal(data, b.Files()[name]) {
			t.Errorf("%s differs between two generations with the same seed", name)
		}
	}
	if got := sum(a.CSV); got != goldenCSVSHA256 {
		t.Errorf("the CSV's SHA-256 is %s, pinned %s", got, goldenCSVSHA256)
	}
	other := DefaultConfig()
	other.Seed++
	c := generate(t, other)
	if bytes.Equal(a.CSV, c.CSV) || bytes.Equal(a.GroundTruth, c.GroundTruth) {
		t.Error("another seed gives the same log")
	}
	if !bytes.Equal(a.Model, c.Model) {
		t.Error("the ground-truth model depends on the seed")
	}
}

// A longer log of the same seed continues the same trajectory: its first
// week is the committed week, row for row (the engine's events before a
// horizon do not depend on it), so a hold-out split by weeks
// (ELECTRONICS_LINE §7) extends the committed data instead of replacing it.
func TestMoreWeeksExtendTheSameLog(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Weeks = 2
	long := strings.SplitAfter(string(generate(t, cfg).CSV), "\n")
	short := strings.SplitAfter(string(def(t).CSV), "\n")
	_, h := DefaultConfig().WindowTicks()
	end := stamp(h)
	var first []string
	for _, l := range long[1:] {
		if f := strings.Split(l, ","); len(f) > 4 && f[4] < end {
			first = append(first, l)
		}
	}
	if !slices.Equal(first, short[1:len(short)-1]) || len(long) <= len(short) {
		t.Errorf("the first of two weeks has %d rows, the one-week log %d", len(first), len(short)-2)
	}
}

// root is the repository root (this package is scripts/gen-electronics-line-data).
func root(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// Every committed file of the data set is the generator's output, byte
// for byte, and there is no other data file (README.md aside).
func TestCommittedFilesAreTheGeneratorOutput(t *testing.T) {
	dir := filepath.Join(root(t), filepath.FromSlash(DataDir))
	files := def(t).Files()
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Errorf("%s: %v (run `go run ./scripts/gen-electronics-line-data` to regenerate)", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is not the generator's output (run `go run ./scripts/gen-electronics-line-data`)", name)
		}
	}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel = filepath.ToSlash(rel); rel != "README.md" && files[rel] == nil {
			t.Errorf("%s is in the data set but the generator does not write it", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, data := range files {
		total += int64(len(data))
	}
	if total > 3<<20 {
		t.Errorf("the data set is %d bytes; keep it well under 5 MB", total)
	}
}

// The ground-truth model is the pack's template, as the generator ran it,
// labelled synthetic; ground-truth.json names its hash and the generation.
func TestGroundTruth(t *testing.T) {
	out := def(t)
	var ir model.ModelIR
	if err := model.Decode(out.Model, &ir); err != nil {
		t.Fatal(err)
	}
	if ir.Domain != "electronics_line" || len(ir.Nodes) != 41 || len(ir.Flows) != 47 || len(ir.EntityTypes) != 2 {
		t.Errorf("model %s: %d nodes, %d flows, %d entity types", ir.Domain, len(ir.Nodes), len(ir.Flows), len(ir.EntityTypes))
	}
	var meta struct {
		Synthetic bool   `json:"synthetic"`
		Template  string `json:"template"`
	}
	if err := json.Unmarshal(ir.Metadata, &meta); err != nil || !meta.Synthetic || meta.Template != TemplateName {
		t.Errorf("metadata %s (%v)", ir.Metadata, err)
	}
	enc, err := model.Encode(&ir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := hashing.Model(enc)
	if err != nil {
		t.Fatal(err)
	}
	var gt groundTruth
	if err := json.Unmarshal(out.GroundTruth, &gt); err != nil {
		t.Fatal(err)
	}
	if gt.Synthetic != true || gt.Model.ModelHash != h || gt.Model.Template != TemplateName || gt.Model.File != ModelFile {
		t.Errorf("ground truth model %+v, want hash %s", gt.Model, h)
	}
	if gt.Generator.Seed != DefaultSeed || gt.Generator.Version != Version || gt.Generator.EngineVersion != des.Version {
		t.Errorf("generator %+v", gt.Generator)
	}
	if gt.Files[CSVFile].SHA256 != sum(out.CSV) || gt.Files[CSVFile].Bytes != int64(len(out.CSV)) || gt.Files[CSVFile].Rows != len(rows(t, out.CSV)) {
		t.Errorf("csv %+v", gt.Files[CSVFile])
	}
	for name, data := range out.Negative {
		if f := gt.Files[name]; f.SHA256 != sum(data) || f.Bytes != int64(len(data)) {
			t.Errorf("%s: %+v", name, f)
		}
	}
	if gt.Window.Start != "2026-10-05T00:00:00+08:00" || gt.Window.End != "2026-10-12T00:00:00+08:00" || gt.Anchor != "2026-09-28T00:00:00+08:00" {
		t.Errorf("window %+v, anchor %s", gt.Window, gt.Anchor)
	}
}

// logRow is one parsed row of the CSV.
type logRow struct {
	line                                        int
	serial, recordType, station, equipment      string
	start, end                                  time.Time
	result, shift, product, operator, synthetic string
	startTick, endTick                          int64 // ms since the anchor
}

func rows(t *testing.T, data []byte) []logRow {
	t.Helper()
	text := string(data)
	if !strings.HasSuffix(text, "\n") || strings.ContainsAny(text, "\r\"") {
		t.Fatal("the CSV is not LF-terminated plain fields")
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if lines[0] != strings.Join(Columns, ",") {
		t.Fatalf("header %q", lines[0])
	}
	anchor := Anchor()
	var out []logRow
	for i, l := range lines[1:] {
		f := strings.Split(l, ",")
		if len(f) != len(Columns) {
			t.Fatalf("line %d: %d fields", i+2, len(f))
		}
		start, err1 := time.Parse(time.RFC3339, f[4])
		end, err2 := time.Parse(time.RFC3339, f[5])
		if err1 != nil || err2 != nil {
			t.Fatalf("line %d: times %q %q", i+2, f[4], f[5])
		}
		out = append(out, logRow{line: i + 2, serial: f[0], recordType: f[1], station: f[2], equipment: f[3], start: start, end: end,
			result: f[6], shift: f[7], product: f[8], operator: f[9], synthetic: f[10],
			startTick: start.Sub(anchor).Milliseconds(), endTick: end.Sub(anchor).Milliseconds()})
	}
	return out
}

// stationOf is the station each process step is logged at, and whether
// it is a test (a result is pass or fail), a repair (pass: repaired,
// fail: scrapped) or neither.
var (
	tests    = []string{"aoi", "ict", "fct", "burn_in"}
	repairs  = []string{"ict_debug", "fct_debug"}
	stations = []string{"smt", "aoi", "aoi_touchup", "ict", "ict_debug", "fct", "fct_debug", "burn_in", "pack"}
	// equipmentOf: the machine of each station's steps ("" for the
	// stations of people: an operator instead).
	equipmentOf = map[string]string{"smt": "smt_line", "aoi": "aoi_machine", "ict": "ict_tester", "fct": "fct_tester", "burn_in": "burn_in_chamber"}
	units       = map[string]int{"smt_line": 1, "aoi_machine": 1, "ict_tester": 2, "fct_tester": 3, "burn_in_chamber": 6}
	serials     = regexp.MustCompile(`^SYN-([AB])-[0-9]{6}$`)
	equipments  = regexp.MustCompile(`^([a-z_]+)_([1-9])$`)
	operators   = regexp.MustCompile(`^op-[0-9]{2}$`)
)

// shiftAt is the line calendar's shift label at ms since the anchor
// (Monday 00:00): early 06:00-14:00, late 14:00-22:00, Monday to Friday.
func shiftAt(ms int64) string {
	week := int64(7 * 24 * 60 * 60 * 1000)
	m := (ms % week) / 60000
	day, minute := m/1440, m%1440
	switch {
	case day > 4:
		return ""
	case minute >= 360 && minute < 840:
		return "early"
	case minute >= 840 && minute < 1320:
		return "late"
	}
	return ""
}

// Every row has the columns of the README, is marked synthetic, uses the
// pack's vocabulary (D-059), starts inside the observed week and ends
// after it starts; its shift is the calendar's at its start.
func TestLogShape(t *testing.T) {
	out := def(t)
	cfg := DefaultConfig()
	w, h := cfg.WindowTicks()
	rs := rows(t, out.CSV)
	if len(rs) < 20000 {
		t.Fatalf("%d rows", len(rs))
	}
	counts := map[string]int{}
	for _, r := range rs {
		bad := func(format string, args ...any) { t.Errorf("line %d: "+format, append([]any{r.line}, args...)...) }
		if _, off := r.start.Zone(); off != 8*3600 || !strings.HasSuffix(fmt.Sprint(r.start.Format(time.RFC3339)), "+08:00") {
			bad("start %v is not in +08:00", r.start)
		}
		switch {
		case r.synthetic != "true":
			bad("not marked synthetic")
		case r.startTick < w || r.startTick >= h:
			bad("starts outside the observed week")
		case r.end.Before(r.start):
			bad("ends before it starts")
		case r.start.Nanosecond() != 0 || r.end.Nanosecond() != 0:
			bad("not whole seconds")
		case r.shift != shiftAt(r.startTick):
			bad("shift %q, the calendar says %q", r.shift, shiftAt(r.startTick))
		}
		counts[r.recordType]++
		switch r.recordType {
		case "operation":
			m := serials.FindStringSubmatch(r.serial)
			if m == nil || r.product != "board_"+strings.ToLower(m[1]) {
				bad("serial %q, product %q", r.serial, r.product)
			}
			if !slices.Contains(stations, r.station) {
				bad("station %q", r.station)
			}
			if res := equipmentOf[r.station]; res != "" {
				if e := equipments.FindStringSubmatch(r.equipment); e == nil || e[1] != res || r.operator != "" {
					bad("equipment %q, operator %q at %s", r.equipment, r.operator, r.station)
				}
			} else if r.equipment != "" || !operators.MatchString(r.operator) {
				bad("equipment %q, operator %q at %s", r.equipment, r.operator, r.station)
			}
			switch {
			case slices.Contains(tests, r.station) || slices.Contains(repairs, r.station):
				if r.result != "pass" && r.result != "fail" {
					bad("result %q at %s", r.result, r.station)
				}
			case r.result != "":
				bad("result %q at %s", r.result, r.station)
			}
		case "downtime":
			e := equipments.FindStringSubmatch(r.equipment)
			if e == nil || r.serial != r.equipment || r.result != "breakdown" || r.product != "" || r.operator != "" || equipmentOf[r.station] != e[1] {
				bad("downtime %+v", r)
			}
		default:
			bad("record type %q", r.recordType)
		}
		if e := equipments.FindStringSubmatch(r.equipment); e != nil {
			if n := int(e[2][0] - '0'); n > units[e[1]] {
				bad("equipment %q beyond the resource's %d units", r.equipment, units[e[1]])
			}
		}
	}
	if counts["operation"] < 20000 || counts["downtime"] < 20 {
		t.Errorf("record types %v", counts)
	}
}

type interval struct {
	start, end int64
	row        logRow
}

// No equipment, and no operator, does two things at once: operations on
// one machine do not overlap, but for the boards of one burn-in batch
// (one chamber, the same start and end); a machine that is down does no
// work; downtimes of one machine do not overlap.
func TestEquipmentIsNeverDoubleBooked(t *testing.T) {
	by := map[string][]interval{}
	for _, r := range rows(t, def(t).CSV) {
		key := r.equipment
		if key == "" {
			key = r.operator
		}
		by[key] = append(by[key], interval{r.startTick, r.endTick, r})
	}
	keys := slices.Sorted(func(yield func(string) bool) {
		for k := range by {
			if !yield(k) {
				return
			}
		}
	})
	for _, k := range keys {
		iv := by[k]
		slices.SortStableFunc(iv, func(a, b interval) int { return int(a.start - b.start) })
		for i := 1; i < len(iv); i++ {
			a, b := iv[i-1], iv[i]
			sameBatch := a.row.station == "burn_in" && b.row.station == "burn_in" && a.row.recordType == "operation" &&
				b.row.recordType == "operation" && a.start == b.start && a.end == b.end
			if b.start < a.end && !sameBatch {
				t.Errorf("%s: line %d (%s) overlaps line %d (%s)", k, a.row.line, a.row.recordType, b.row.line, b.row.recordType)
			}
		}
	}
}

// next are the stations a board may visit after one, by its result
// (ELECTRONICS_LINE §4.1); a board scrapped at debug, or shipped, has none.
var next = map[string]map[string][]string{
	"smt":         {"": {"aoi"}},
	"aoi":         {"pass": {"ict"}, "fail": {"aoi_touchup"}},
	"aoi_touchup": {"": {"ict"}},
	"ict":         {"pass": {"fct"}, "fail": {"ict_debug"}},
	"ict_debug":   {"pass": {"ict"}, "fail": {}},
	"fct":         {"pass": {"burn_in"}, "fail": {"fct_debug"}},
	"fct_debug":   {"pass": {"fct"}, "fail": {}},
	"burn_in":     {"pass": {"pack"}, "fail": {"fct_debug"}},
	"pack":        {"": {}},
}

// Each board's operations follow the line's routes in time: SMT first
// (for a board released in the week), each step after the last one ends
// and where its result sends the board; retests follow a repair.
func TestRoutesFollowTheLine(t *testing.T) {
	by := map[string][]logRow{}
	var order []string
	for _, r := range rows(t, def(t).CSV) {
		if r.recordType != "operation" {
			continue
		}
		if by[r.serial] == nil {
			order = append(order, r.serial)
		}
		by[r.serial] = append(by[r.serial], r)
	}
	shipped, scrapped, fromSMT := 0, 0, 0
	for _, s := range order {
		ops := by[s]
		if ops[0].station == "smt" {
			fromSMT++
		}
		for i := 1; i < len(ops); i++ {
			prev, cur := ops[i-1], ops[i]
			if cur.station == "smt" {
				t.Errorf("%s: SMT again at line %d", s, cur.line)
			}
			if cur.startTick < prev.endTick {
				t.Errorf("%s: line %d starts before line %d ends", s, cur.line, prev.line)
			}
			if allowed := next[prev.station][prev.result]; !slices.Contains(allowed, cur.station) {
				t.Errorf("%s: %s (%s) then %s at line %d", s, prev.station, prev.result, cur.station, cur.line)
			}
		}
		last := ops[len(ops)-1]
		switch {
		case last.station == "pack":
			shipped++
		case slices.Contains(repairs, last.station) && last.result == "fail":
			scrapped++
		}
	}
	if fromSMT < 3000 || shipped < 3000 || scrapped == 0 {
		t.Errorf("%d boards from SMT, %d shipped, %d scrapped", fromSMT, shipped, scrapped)
	}
}

// The first board after a changeover starts at least the shortest
// changeover (15 min) after the board before it on the SMT line ends: the
// log's start is after the changeover (ELECTRONICS_LINE §6).
func TestChangeoversShowAsGaps(t *testing.T) {
	var smt []logRow
	for _, r := range rows(t, def(t).CSV) {
		if r.recordType == "operation" && r.station == "smt" {
			smt = append(smt, r)
		}
	}
	changes := 0
	for i := 1; i < len(smt); i++ {
		if smt[i].product != smt[i-1].product {
			changes++
			if gap := smt[i].startTick - smt[i-1].endTick; gap < 15*60000-1000 {
				t.Errorf("line %d: a changeover gap of %d ms", smt[i].line, gap)
			}
		}
	}
	if changes < 8 {
		t.Errorf("%d changeovers in a week of two families a day", changes)
	}
}

// The log is the engine's replication 0 of the ground-truth model with the
// generator's seed: what des-go itself counts over the observed week (the
// measurement window [W, H] of a run with that seed, a warm-up of a week
// and a horizon of two) is what the log shows: boards shipped, test
// failures, repairs (rework observations), breakdowns per machine and
// changeovers of the SMT line.
func TestTheLogIsTheEngineReplication(t *testing.T) {
	out := def(t)
	cfg := DefaultConfig()
	var ir model.ModelIR
	if err := model.Decode(out.Model, &ir); err != nil {
		t.Fatal(err)
	}
	in, err := cfg.runInput(&ir, 7*(1+cfg.Weeks))
	if err != nil {
		t.Fatal(err)
	}
	res, err := (&des.Engine{}).Run(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := cfg.WindowTicks(); res.MeasurementWindow.WarmupTicks != w || res.MeasurementWindow.HorizonTicks != h {
		t.Fatalf("window %+v", res.MeasurementWindow)
	}
	rep := res.ReplicationSummaries[0]
	shipped, failures, reworks, smtChanges := 0, 0, 0, 0
	down := map[string]int64{}
	var lastSMT string
	for _, r := range rows(t, out.CSV) {
		switch {
		case r.recordType == "downtime":
			down[equipments.FindStringSubmatch(r.equipment)[1]]++
		case r.station == "pack":
			shipped++
		case slices.Contains(tests, r.station) && r.result == "fail":
			failures++
		case r.station == "aoi_touchup", slices.Contains(repairs, r.station) && r.result == "pass":
			reworks++
		}
		if r.recordType == "operation" && r.station == "smt" {
			if lastSMT != "" && r.product != lastSMT {
				smtChanges++
			}
			lastSMT = r.product
		}
	}
	if int64(shipped) != rep.CompletedCount || int64(failures) != rep.Failures || int64(reworks) != rep.Reworks {
		t.Errorf("log: %d shipped, %d test failures, %d repairs; engine: %d completed, %d failures, %d reworks",
			shipped, failures, reworks, rep.CompletedCount, rep.Failures, rep.Reworks)
	}
	for res := range units {
		st, ok := rep.ResourceStates[res]
		if !ok || st.BreakdownsStarted == nil || *st.BreakdownsStarted != down[res] {
			t.Errorf("%s: the log has %d downtimes, the engine %+v", res, down[res], st)
		}
	}
	// The week's first changeover may be the one from last Friday's family.
	if st := rep.ResourceStates["smt_line"]; st.SetupsStarted == nil || (*st.SetupsStarted != int64(smtChanges) && *st.SetupsStarted != int64(smtChanges+1)) {
		t.Errorf("SMT: %d changes of family in the log, the engine started %v changeovers", smtChanges, st.SetupsStarted)
	}
}

// Each derived fixture is the head of the log with exactly the one change
// its name says (the negative cases of D-059's and D-072's rules).
func TestNegativeFixtures(t *testing.T) {
	out := def(t)
	base := strings.Split(string(out.CSV), "\n")
	want := map[string]struct {
		column int
		check  func(old, new string) bool
	}{
		"negative/end-before-start.csv":    {5, func(old, new string) bool { return new < old }},
		"negative/unknown-station.csv":     {2, func(_, new string) bool { return new == "xray" }},
		"negative/unknown-product.csv":     {8, func(_, new string) bool { return new == "board_c" }},
		"negative/missing-product.csv":     {8, func(_, new string) bool { return new == "" }},
		"negative/unknown-record-type.csv": {1, func(_, new string) bool { return new == "maintenance" }},
		"negative/missing-test-result.csv": {6, func(_, new string) bool { return new == "" }},
		"negative/unknown-test-result.csv": {6, func(_, new string) bool { return new == "PASS" }},
	}
	if len(out.Negative) != len(want) {
		t.Errorf("%d fixtures, want %d", len(out.Negative), len(want))
	}
	for name, w := range want {
		data, ok := out.Negative[name]
		if !ok {
			t.Errorf("%s is missing", name)
			continue
		}
		lines := strings.Split(string(data), "\n")
		if len(lines) != NegativeRows+2 || lines[len(lines)-1] != "" {
			t.Errorf("%s: %d lines", name, len(lines))
			continue
		}
		changed := 0
		for i, l := range lines[:len(lines)-1] {
			if l == base[i] {
				continue
			}
			changed++
			o, n := strings.Split(base[i], ","), strings.Split(l, ",")
			for c := range o {
				if (c == w.column) != (o[c] != n[c]) || (c == w.column && !w.check(o[c], n[c])) {
					t.Errorf("%s line %d column %d: %q -> %q", name, i+1, c, o[c], n[c])
				}
			}
		}
		if changed != 1 {
			t.Errorf("%s: %d rows changed, want 1", name, changed)
		}
	}
}
