package aiapproval_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/aievals"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/contracttest"
)

// T-053 review round 1 (H-003), H1 and L6: the live eval and smoke spend
// through the same two provider organizations as the worker, outside the
// DB ledger (D-114 (b)). So a run's cap must fit the deployment's daily and
// monthly limits (H-018), and what earlier runs spent is kept in an
// append-only record committed with the reports,
// checks/ai-evals/live-spend.jsonl: an "open" line before any call, a
// "close" line with what the run cost after it, whatever the run's end.
// A run is refused (exit 2, no backend built) when its cap does not fit
// what the day or the month has left; an open line without its close
// counts at its whole cap; a record that cannot be read or does not parse
// refuses every run. No network: the backend is this package's in-process
// fake, the keys are fake strings.

const recordName = "live-spend.jsonl"

// mainRun runs aievals.Main for the live configuration env in dir (the
// report directory, where the record is too), with -root root.
type mainRun struct {
	code           int
	stdout, stderr string
	built          int
	backend        *inProcess
}

func runMain(t *testing.T, env map[string]string, root, dir string, args ...string) mainRun {
	t.Helper()
	var out, errb bytes.Buffer
	r := mainRun{backend: &inProcess{Backend: fake.New()}}
	factory := func(ai config.AI) (provider.Billable, error) {
		r.built++
		return r.backend, nil
	}
	r.code = aievals.Main(context.Background(), append([]string{"-root", root, "-dir", dir}, args...), func(k string) string { return env[k] },
		&out, &errb, factory)
	r.stdout, r.stderr = out.String(), errb.String()
	return r
}

// emptyRecord is a report directory with an empty spend record.
func emptyRecord(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, recordName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// recordLines is the record's lines, decoded.
func recordLines(t *testing.T, dir string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, recordName))
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("a record line does not parse: %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

// H1: a run's cap fits the deployment's daily limit and the monthly limit
// (the reviewer's probe P3: 100, 50 and 10.01 against the USD 10 day),
// or the run is refused before any backend is built.
func TestTheRunCapFitsTheDayAndTheMonth(t *testing.T) {
	root := contracttest.Root(t)
	for _, c := range []struct {
		name, cap, day, month string
		smoke                 bool
	}{
		{"the whole monthly budget in one run", "100", "", "", true},
		{"five daily limits in one run", "50", "", "", true},
		{"just above the daily limit", "10.01", "", "", true},
		{"the default eval cap above a lower daily limit", "", "5", "", false},
		{"the default smoke cap above a lower daily limit", "", "1.5", "", true},
		{"above a lower monthly limit", "10", "8", "8", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := liveEnv()
			for k, v := range map[string]string{"AI_EVAL_LIVE_MAX_USD": c.cap, "AI_DAILY_LIMIT_USD": c.day, "AI_MONTHLY_LIMIT_USD": c.month} {
				if v != "" {
					env[k] = v
				}
			}
			if c.day != "" && c.month == "" {
				env["AI_ORGANIZATION_DAILY_LIMIT_USD"] = "1"
			}
			args := []string{"-cases", "MOD-002"}
			if c.smoke {
				args = []string{"-smoke"}
			}
			dir := emptyRecord(t)
			r := runMain(t, env, root, dir, args...)
			if r.code != aievals.ExitRefused || r.built != 0 || !strings.Contains(r.stderr, "REFUSED (no provider call was made)") {
				t.Errorf("exit %d, backend built %d: %s %s", r.code, r.built, r.stdout, r.stderr)
			}
			if lines := recordLines(t, dir); len(lines) != 0 {
				t.Errorf("a refused run wrote the record: %v", lines)
			}
		})
	}
	// Within both, the run is made.
	env := liveEnv()
	env["AI_EVAL_LIVE_MAX_USD"] = "10"
	if r := runMain(t, env, root, emptyRecord(t), "-smoke"); r.code != aievals.ExitOK || r.built != 1 {
		t.Errorf("a cap at the daily limit: exit %d built %d: %s", r.code, r.built, r.stderr)
	}
}

// H1: a run opens its line before the first call and closes it after the
// run with what it cost; its report's name holds the run's ID and UTC
// time, so a second run never overwrites the first's report.
func TestARunIsRecordedOpenThenClosed(t *testing.T) {
	root := contracttest.Root(t)
	dir := emptyRecord(t)
	env := liveEnv()
	var before []map[string]any
	first := runMainWith(t, env, root, dir, func(context.Context, provider.Request) { before = recordLines(t, dir) }, "-smoke")
	if first.code != aievals.ExitOK || first.built != 1 {
		t.Fatalf("exit %d built %d: %s %s", first.code, first.built, first.stdout, first.stderr)
	}
	if len(before) != 1 || before[0]["event"] != "open" || before[0]["kind"] != "smoke" ||
		before[0]["cap_nano_usd"] != float64(aievals.DefaultSmokeCap) || before[0]["model"] != "claude-opus-5-5" || before[0]["approval"] != "H-018" {
		t.Fatalf("the record before the first call: %v", before)
	}
	lines := recordLines(t, dir)
	if len(lines) != 2 || lines[1]["event"] != "close" || lines[1]["run_id"] != lines[0]["run_id"] || lines[1]["result"] != "complete" {
		t.Fatalf("the record after the run: %v", lines)
	}
	price, _ := spend.PriceOf("claude-opus-5-5")
	cost := price.Cost(spend.Tokens{Input: 1500, Output: 300})
	if lines[1]["settled_nano_usd"] != float64(cost) || lines[1]["committed_nano_usd"] != float64(cost) {
		t.Errorf("the close line %v, want the run's cost %d", lines[1], cost)
	}
	now := time.Now().UTC()
	if day := lines[0]["day"]; day != now.Format(time.DateOnly) && day != now.Add(-time.Minute).Format(time.DateOnly) {
		t.Errorf("the open line's day %v", day)
	}
	name, _ := lines[0]["report"].(string)
	runID, _ := lines[0]["run_id"].(string)
	if !regexp.MustCompile(`^live-smoke-[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{6}Z-[0-7][0-9A-HJKMNP-TV-Z]{25}-claude-opus-5-5\.json$`).MatchString(name) ||
		!strings.Contains(name, runID) || !strings.Contains(first.stdout, name) {
		t.Errorf("the report %q (run %q): %s", name, runID, first.stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Errorf("the report: %v", err)
	}
	// A second run: its own report, two more lines.
	second := runMain(t, env, root, dir, "-smoke")
	if second.code != aievals.ExitOK {
		t.Fatalf("second run: exit %d: %s", second.code, second.stderr)
	}
	reports, _ := filepath.Glob(filepath.Join(dir, "live-smoke-*.json"))
	if lines := recordLines(t, dir); len(reports) != 2 || len(lines) != 4 || lines[2]["run_id"] == runID {
		t.Errorf("after two runs: %d reports, record %v", len(reports), lines)
	}
}

// runMainWith is runMain with a hook the backend calls before each call.
func runMainWith(t *testing.T, env map[string]string, root, dir string, hook func(context.Context, provider.Request), args ...string) mainRun {
	t.Helper()
	var out, errb bytes.Buffer
	r := mainRun{}
	factory := func(ai config.AI) (provider.Billable, error) {
		r.built++
		return &hooked{inProcess: &inProcess{Backend: fake.New()}, hook: hook}, nil
	}
	r.code = aievals.Main(context.Background(), append([]string{"-root", root, "-dir", dir}, args...), func(k string) string { return env[k] },
		&out, &errb, factory)
	r.stdout, r.stderr = out.String(), errb.String()
	return r
}

// hooked is the in-process backend with a hook before each call.
type hooked struct {
	*inProcess
	hook func(context.Context, provider.Request)
}

func (h *hooked) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	h.hook(ctx, req)
	return h.inProcess.Complete(ctx, req)
}

// openLine and closeLine are record lines of a run started at at.
func openLine(id, kind string, at time.Time, capNano int64) string {
	return `{"event":"open","run_id":"` + id + `","kind":"` + kind + `","started_at":"` + at.UTC().Format("2006-01-02T15:04:05Z") +
		`","day":"` + at.UTC().Format(time.DateOnly) + `","month":"` + at.UTC().Format("2006-01") + `","model":"claude-opus-5-5","approval":"H-018","cap_nano_usd":` +
		itoa(capNano) + `,"report":"live-` + prefix(kind) + at.UTC().Format("2006-01-02T150405Z") + "-" + id + `-claude-opus-5-5.json"}` + "\n"
}

func closeLine(id string, at time.Time, settled int64, result string) string {
	return `{"event":"close","run_id":"` + id + `","closed_at":"` + at.UTC().Format("2006-01-02T15:04:05Z") + `","day":"` + at.UTC().Format(time.DateOnly) +
		`","month":"` + at.UTC().Format("2006-01") + `","settled_nano_usd":` + itoa(settled) + `,"committed_nano_usd":` + itoa(settled) +
		`,"result":"` + result + `"}` + "\n"
}

func prefix(kind string) string {
	if kind == "smoke" {
		return "smoke-"
	}
	return ""
}

func itoa(n int64) string {
	data, _ := json.Marshal(n)
	return string(data)
}

const (
	runA = "01JB0000000000000000000001"
	runB = "01JB0000000000000000000002"
	runC = "01JB0000000000000000000003"
)

// H1: what earlier runs spent is what the record says: close lines of the
// UTC day and month at what they cost, an open line without its close at
// its whole cap, nothing of other days and months; a run whose cap does
// not fit what is left is refused before any backend is built. The record
// holds lines for today and tomorrow (and this month and the next) where
// the test means "now", so the test does not depend on the time of day.
func TestTheRecordBoundsTheNextRun(t *testing.T) {
	root := contracttest.Root(t)
	now := time.Now().UTC()
	tomorrow := now.Add(24 * time.Hour)
	nextMonth := now.AddDate(0, 1, 0)
	usd := func(x float64) int64 { return int64(x * float64(spend.NanoPerUSD)) }
	for _, c := range []struct {
		name    string
		record  string
		cap     string
		refused bool
	}{
		{"an open run holds its whole cap of the day", openLine(runA, "eval", now, usd(9.5)) + openLine(runB, "eval", tomorrow, usd(9.5)), "1", true},
		{"a closed run holds what it cost", openLine(runA, "eval", now, usd(9.5)) + closeLine(runA, now, usd(0.25), "complete") +
			openLine(runB, "eval", tomorrow, usd(9.5)) + closeLine(runB, tomorrow, usd(0.25), "complete"), "9", false},
		{"closed runs fill the day", openLine(runA, "eval", now, usd(9)) + closeLine(runA, now, usd(8.5), "gate_failed") +
			openLine(runB, "smoke", tomorrow, usd(9)) + closeLine(runB, tomorrow, usd(8.5), "stopped"), "2", true},
		{"other days do not count against the day", openLine(runA, "eval", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), usd(9.9)) +
			closeLine(runA, time.Date(2020, 1, 1, 1, 0, 0, 0, time.UTC), usd(9.9), "complete"), "10", false},
		{"the month: its other days count", monthOf(now, usd(9.95)) + monthOf(nextMonth, usd(9.95)), "1", true},
		{"a run closed on the next day counts on both days", openLine(runA, "eval", now.Add(-24*time.Hour), usd(9.5)) +
			closeLine(runA, now, usd(9.5), "complete") + openLine(runB, "eval", now, usd(9.5)) + closeLine(runB, tomorrow, usd(9.5), "complete"), "1", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, recordName), []byte(c.record), 0o644); err != nil {
				t.Fatal(err)
			}
			env := liveEnv()
			env["AI_EVAL_LIVE_MAX_USD"] = c.cap
			r := runMain(t, env, root, dir, "-smoke")
			if c.refused && (r.code != aievals.ExitRefused || r.built != 0 || !strings.Contains(r.stderr, "REFUSED (no provider call was made)")) {
				t.Errorf("want refused: exit %d built %d: %s", r.code, r.built, r.stderr)
			}
			if !c.refused && (r.code != aievals.ExitOK || r.built != 1) {
				t.Errorf("want run: exit %d built %d: %s", r.code, r.built, r.stderr)
			}
			data, _ := os.ReadFile(filepath.Join(dir, recordName))
			if c.refused && string(data) != c.record {
				t.Error("a refused run changed the record")
			}
			if !c.refused && !strings.HasPrefix(string(data), c.record) {
				t.Error("the run rewrote the record instead of appending to it")
			}
		})
	}
}

// monthOf is ten closed runs of n each on ten days of t's month, none of
// them t's day or the next: the month's total, not the day's.
func monthOf(t time.Time, n int64) string {
	var b strings.Builder
	first := time.Date(t.Year(), t.Month(), 1, 12, 0, 0, 0, time.UTC)
	for i, day := 0, first; i < 10; day = day.AddDate(0, 0, 1) {
		if d := day.Day(); d == t.Day() || d == t.Add(24*time.Hour).Day() {
			continue
		}
		id := fmt.Sprintf("01JC%020d%02d", int(t.Month()), i)
		b.WriteString(openLine(id, "eval", day, n) + closeLine(id, day.Add(time.Minute), n, "complete"))
		i++
	}
	return b.String()
}

// H1: a record that is missing, cannot be read or does not parse refuses
// every run (fail closed): the spend of the earlier runs is not known.
func TestAnUnreadableRecordRefuses(t *testing.T) {
	root := contracttest.Root(t)
	now := time.Now().UTC()
	open := openLine(runA, "eval", now, spend.NanoPerUSD)
	for name, record := range map[string]string{
		"not JSON":                   "garbage\n",
		"a line without its newline": strings.TrimSuffix(open, "\n"),
		"an empty line":              open + "\n",
		"an unknown member":          strings.Replace(open, `"event":"open"`, `"event":"open","note":"x"`, 1),
		"an unknown event":           strings.Replace(open, `"event":"open"`, `"event":"reopen"`, 1),
		"a close without its open":   closeLine(runA, now, 1, "complete"),
		"two closes of one run":      open + closeLine(runA, now, 1, "complete") + closeLine(runA, now, 1, "complete"),
		"two opens of one run":       open + open,
		"a negative cost":            open + strings.Replace(closeLine(runA, now, 1, "complete"), `"settled_nano_usd":1`, `"settled_nano_usd":-1`, 1),
		"committed below settled":    open + strings.Replace(closeLine(runA, now, 5, "complete"), `"committed_nano_usd":5`, `"committed_nano_usd":4`, 1),
		"a zero cap":                 openLine(runB, "eval", now, 0),
		"a day not the start's":      strings.Replace(open, `"day":"`+now.Format(time.DateOnly), `"day":"2020-01-01`, 1),
		"a month not the start's":    strings.Replace(open, `"month":"`+now.Format("2006-01"), `"month":"2020-01`, 1),
		"an unknown kind":            openLine(runC, "both", now, 1),
		"an unknown result":          open + closeLine(runA, now, 1, "fine"),
		"not a run ID":               openLine("run-1", "eval", now, 1),
		"a report of another run":    strings.Replace(open, "-"+runA+"-", "-"+runB+"-", 1),
		"a string amount":            strings.Replace(open, `"cap_nano_usd":1000000000`, `"cap_nano_usd":"1000000000"`, 1),
		"a closing before the start": open + closeLine(runA, now.Add(-time.Hour), 1, "complete"),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, recordName), []byte(record), 0o644); err != nil {
				t.Fatal(err)
			}
			r := runMain(t, liveEnv(), root, dir, "-smoke")
			if r.code != aievals.ExitRefused || r.built != 0 || !strings.Contains(r.stderr, "spend record") {
				t.Errorf("exit %d built %d: %s", r.code, r.built, r.stderr)
			}
		})
	}
	// Missing, and not a file.
	for name, dir := range map[string]string{"missing": t.TempDir(), "a directory": func() string {
		d := t.TempDir()
		_ = os.Mkdir(filepath.Join(d, recordName), 0o755)
		return d
	}()} {
		if r := runMain(t, liveEnv(), root, dir, "-smoke"); r.code != aievals.ExitRefused || r.built != 0 || !strings.Contains(r.stderr, "spend record") {
			t.Errorf("%s: exit %d built %d: %s", name, r.code, r.built, r.stderr)
		}
	}
}

// H1, L6: whatever the run's end after it was opened, the record is closed
// with what the run cost: a run stopped by its cap (exit 1), a run that
// broke a gate (exit 1), and a run that stopped on an error after a paid
// call (exit 3), which also prints its summary and cost.
func TestEveryEndClosesTheRecord(t *testing.T) {
	root := contracttest.Root(t)
	price, _ := spend.PriceOf("claude-opus-5-5")
	cost := price.Cost(spend.Tokens{Input: 1500, Output: 300})

	// The cap holds no call: nothing is spent; the run is closed at 0.
	dir := emptyRecord(t)
	env := liveEnv()
	env["AI_EVAL_LIVE_MAX_USD"] = "0.01"
	if r := runMain(t, env, root, dir, "-cases", "MOD-001,MOD-002"); r.code != aievals.ExitGates {
		t.Fatalf("a small cap: exit %d: %s", r.code, r.stderr)
	}
	if lines := recordLines(t, dir); len(lines) != 2 || lines[1]["result"] != "stopped" || lines[1]["settled_nano_usd"] != float64(0) {
		t.Errorf("a small cap: %v", lines)
	}

	// A safe-failure case reaches ready: a gate fails.
	dir = emptyRecord(t)
	var out, errb bytes.Buffer
	b := &inProcess{Backend: fake.New()}
	cases, err := aievals.ReadCases(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.ID == "MOD-030" {
			b.Script(provider.RoleModeler, c.Intent, proposedRaise())
		}
	}
	code := aievals.Main(context.Background(), []string{"-root", root, "-dir", dir, "-cases", "MOD-030"}, func(k string) string { return liveEnv()[k] },
		&out, &errb, func(config.AI) (provider.Billable, error) { return b, nil })
	if code != aievals.ExitGates {
		t.Fatalf("a gate: exit %d: %s %s", code, out.String(), errb.String())
	}
	if lines := recordLines(t, dir); len(lines) != 2 || lines[1]["result"] != "gate_failed" || lines[1]["settled_nano_usd"] != float64(cost) {
		t.Errorf("a gate: %v", lines)
	}

	// The second case's fixture is missing: the run stops after the first
	// case's paid call.
	tmp := t.TempDir()
	corpus := filepath.Join(tmp, "testdata", "ai-evals")
	models := filepath.Join(tmp, "testdata", "golden-models")
	for _, d := range []string{corpus, models} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fixture, err := os.ReadFile(filepath.Join(root, "testdata", "golden-models", "default-delivery.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(models, "default-delivery.json"), fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, c := range cases {
		if c.ID == "MOD-002" {
			data, _ := json.Marshal(c)
			lines = append(lines, string(data))
			c.ID, c.ModelFixture = "MOD-902", "testdata/golden-models/missing.json"
			data, _ = json.Marshal(c)
			lines = append(lines, string(data))
		}
	}
	if err := os.WriteFile(filepath.Join(corpus, "modeler.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir = emptyRecord(t)
	r := runMain(t, liveEnv(), tmp, dir, "-cases", "MOD-002,MOD-902")
	if r.code != aievals.ExitReport || r.built != 1 {
		t.Fatalf("an error after a paid call: exit %d built %d: %s %s", r.code, r.built, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "cost USD "+spend.FormatUSD(cost)) {
		t.Errorf("no summary with the cost on stdout: %q (stderr %q)", r.stdout, r.stderr)
	}
	if got := recordLines(t, dir); len(got) != 2 || got[1]["result"] != "error" || got[1]["settled_nano_usd"] != float64(cost) {
		t.Errorf("an error after a paid call: %v", got)
	}
}

// proposedRaise is a proposal that raises the CI runners to 6.
func proposedRaise() []byte {
	var changes any
	_ = json.Unmarshal([]byte(`[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`), &changes)
	out, _ := json.Marshal(map[string]any{"status": "proposed", "summary": "raise the runners", "assumptions": []string{},
		"risk_level": "low", "confidence": 0.9, "changes": changes, "expected_effects": []string{"Waiting may fall; a simulation will tell."},
		"requires_confirmation": true})
	return out
}
