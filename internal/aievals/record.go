package aievals

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"os"
	"regexp"
	"slices"
	"time"
)

// The spend record (T-053 review round 1, H1; PROPOSED D-114 (b)). The
// live eval and smoke spend through the same two provider organizations as
// the worker, but outside the database ledger (a run needs no database),
// so what they spend is kept in an append-only file committed with their
// reports, checks/ai-evals/live-spend.jsonl, one JSON object per line:
//
//   - before a run's first call, an "open" line: the run's ID, kind (eval,
//     smoke or, T-056 review round 1, smoke-photo), UTC start, UTC day and
//     month, model, approval, cap in nano-USD and report file name;
//   - after the run, whatever its end (complete, a gate failed, its cap or
//     a cancellation stopped it, an error after paid calls, the report
//     could not be written), a "close" line: what it cost (settled) and
//     what its meter held (committed: settled plus any reservation left
//     open, the amount counted).
//
// What earlier runs spent in a UTC day or month is the committed amount of
// each closed run on the day or month it opened or closed in, and the whole
// cap of each run that opened without a close (it may have stopped after
// its calls without writing one: fail safe). A run whose cap is more than
// what the day or the month has left is refused before any call, and so
// is every run while the record is missing, cannot be read or does not
// parse (fail closed). The record is per checkout: runs in another checkout
// see it only once it is committed and merged; worker spend is in the
// database ledger, and the Console spend limit bounds both together (see
// D-114 (b)).

// RecordName is the spend record's file name, next to the reports.
const RecordName = "live-spend.jsonl"

// lockSuffix names the lock file a run holds while it reads the record and
// appends its open line (never committed: .gitignore).
const lockSuffix = ".lock"

// How a run ended, in its close line.
const (
	// ResultComplete: every case ran and no gate failed (exit 0).
	ResultComplete = "complete"
	// ResultGateFailed: every case ran and a gate failed (exit 1).
	ResultGateFailed = "gate_failed"
	// ResultStopped: the run's cap or a cancellation stopped it (exit 1).
	ResultStopped = "stopped"
	// ResultError: the run stopped on an error (exit 3).
	ResultError = "error"
	// ResultReportUnwritten: the report could not be written (exit 3).
	ResultReportUnwritten = "report_unwritten"
)

var results = []string{ResultComplete, ResultGateFailed, ResultStopped, ResultError, ResultReportUnwritten}

// openLine is a run's open line.
type openLine struct {
	Event      string `json:"event"`
	RunID      string `json:"run_id"`
	Kind       string `json:"kind"`
	StartedAt  string `json:"started_at"`
	Day        string `json:"day"`
	Month      string `json:"month"`
	Model      string `json:"model"`
	Approval   string `json:"approval"`
	CapNanoUSD int64  `json:"cap_nano_usd"`
	Report     string `json:"report"`
}

// closeLine is a run's close line.
type closeLine struct {
	Event            string `json:"event"`
	RunID            string `json:"run_id"`
	ClosedAt         string `json:"closed_at"`
	Day              string `json:"day"`
	Month            string `json:"month"`
	SettledNanoUSD   int64  `json:"settled_nano_usd"`
	CommittedNanoUSD int64  `json:"committed_nano_usd"`
	Result           string `json:"result"`
}

// The members of each line, exactly.
var (
	openMembers  = []string{"approval", "cap_nano_usd", "day", "event", "kind", "model", "month", "report", "run_id", "started_at"}
	closeMembers = []string{"closed_at", "committed_nano_usd", "day", "event", "month", "result", "run_id", "settled_nano_usd"}
)

const stamp = "2006-01-02T15:04:05Z"

var (
	runIDShape    = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	modelShape    = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,127}$`)
	approvalShape = regexp.MustCompile(`^[A-Z][A-Z0-9-]{0,31}$`)
)

// recordedRun is one run of the record.
type recordedRun struct {
	open   openLine
	closed *closeLine
}

// Record is the spend record a run is about to open in, read and locked.
type Record struct {
	path string
	lock string
	runs map[string]*recordedRun
}

// ErrRecordLocked: another run holds the record's lock.
var ErrRecordLocked = errors.New("aievals: the spend record is locked: another live run is starting, or one stopped while starting (remove the .lock file next to the record once no run is going)")

// OpenRecord takes the record's lock and reads it. The record must exist
// (an empty file is a record of no runs) and parse; anything else is an
// error, and the lock is not held.
func OpenRecord(path string) (*Record, error) {
	lock := path + lockSuffix
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil, ErrRecordLocked
	}
	if err != nil {
		return nil, fmt.Errorf("aievals: the spend record's lock: %w", err)
	}
	_ = f.Close()
	r := &Record{path: path, lock: lock, runs: map[string]*recordedRun{}}
	if err := r.read(); err != nil {
		r.Release()
		return nil, err
	}
	return r, nil
}

// Release gives the lock back; it may be called more than once.
func (r *Record) Release() {
	if r.lock != "" {
		_ = os.Remove(r.lock)
		r.lock = ""
	}
}

// read parses the whole record: every line one object of the open or close
// shape exactly, each ending with a newline, every close after its run's
// open, one open and at most one close per run. Errors say the line and
// why, never what the line holds.
func (r *Record) read() error {
	info, err := os.Stat(r.path)
	if err != nil {
		return fmt.Errorf("aievals: the spend record: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("aievals: the spend record is not a file")
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		return fmt.Errorf("aievals: the spend record: %w", err)
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		return errors.New("aievals: the spend record's last line has no newline (a write was cut)")
	}
	for i, line := range bytes.SplitAfter(data, []byte("\n")) {
		if len(line) == 0 {
			continue // after the final newline
		}
		if err := r.parse(bytes.TrimSuffix(line, []byte("\n"))); err != nil {
			return fmt.Errorf("aievals: the spend record, line %d: %w", i+1, err)
		}
	}
	return nil
}

func (r *Record) parse(line []byte) error {
	var members map[string]jsontext.Value
	if err := json.Unmarshal(line, &members); err != nil {
		return errors.New("not one JSON object")
	}
	keys := slices.Sorted(maps.Keys(members))
	var event string
	if err := json.Unmarshal(members["event"], &event); err != nil {
		return errors.New("no event")
	}
	switch event {
	case "open":
		if !slices.Equal(keys, openMembers) {
			return errors.New("an open line has other members than its own")
		}
		var o openLine
		if err := json.Unmarshal(line, &o, json.RejectUnknownMembers(true)); err != nil {
			return errors.New("an open line does not decode")
		}
		if err := o.check(); err != nil {
			return err
		}
		if _, dup := r.runs[o.RunID]; dup {
			return errors.New("a run opened twice")
		}
		r.runs[o.RunID] = &recordedRun{open: o}
	case "close":
		if !slices.Equal(keys, closeMembers) {
			return errors.New("a close line has other members than its own")
		}
		var c closeLine
		if err := json.Unmarshal(line, &c, json.RejectUnknownMembers(true)); err != nil {
			return errors.New("a close line does not decode")
		}
		run, ok := r.runs[c.RunID]
		switch {
		case !ok:
			return errors.New("a close line before its run's open line")
		case run.closed != nil:
			return errors.New("a run closed twice")
		}
		if err := c.check(run.open); err != nil {
			return err
		}
		run.closed = &c
	default:
		return errors.New("an unknown event")
	}
	return nil
}

// at is s as a UTC time of the record's form, or an error.
func at(s string) (time.Time, error) {
	t, err := time.Parse(stamp, s)
	if err != nil || t.Format(stamp) != s {
		return time.Time{}, errors.New("a time not of the form 2006-01-02T15:04:05Z")
	}
	return t, nil
}

func (o openLine) check() error {
	start, err := at(o.StartedAt)
	switch {
	case !runIDShape.MatchString(o.RunID):
		return errors.New("a run ID is not a ULID")
	case o.Kind != "eval" && o.Kind != "smoke" && o.Kind != PhotoSmokeKind:
		return errors.New("an unknown kind")
	case err != nil:
		return err
	case o.Day != start.Format(time.DateOnly) || o.Month != start.Format("2006-01"):
		return errors.New("a day or month that is not the run's start's")
	case !modelShape.MatchString(o.Model):
		return errors.New("not a model name")
	case !approvalShape.MatchString(o.Approval):
		return errors.New("not an approval ID")
	case o.CapNanoUSD <= 0:
		return errors.New("a cap that is not positive")
	case o.Report != ReportName(o.Kind, start, o.RunID, o.Model):
		return errors.New("a report that is not the run's")
	}
	return nil
}

func (c closeLine) check(o openLine) error {
	end, err := at(c.ClosedAt)
	start, _ := at(o.StartedAt)
	switch {
	case err != nil:
		return err
	case end.Before(start):
		return errors.New("a run closed before it started")
	case c.Day != end.Format(time.DateOnly) || c.Month != end.Format("2006-01"):
		return errors.New("a day or month that is not the run's close's")
	case c.SettledNanoUSD < 0 || c.CommittedNanoUSD < c.SettledNanoUSD:
		return errors.New("an amount that is negative, or committed below settled")
	case !slices.Contains(results, c.Result):
		return errors.New("an unknown result")
	}
	return nil
}

// Spent is what the record's runs spent in now's UTC day and month: the
// committed amount of a closed run, the whole cap of a run without a
// close, each counted in the day and month it opened in and those it
// closed in.
func (r *Record) Spent(now time.Time) (day, month int64) {
	d, m := now.UTC().Format(time.DateOnly), now.UTC().Format("2006-01")
	for _, run := range r.runs {
		amount, days, months := run.open.CapNanoUSD, []string{run.open.Day}, []string{run.open.Month}
		if c := run.closed; c != nil {
			amount, days, months = c.CommittedNanoUSD, append(days, c.Day), append(months, c.Month)
		}
		if slices.Contains(days, d) {
			day = addSat(day, amount)
		}
		if slices.Contains(months, m) {
			month = addSat(month, amount)
		}
	}
	return day, month
}

func addSat(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// appendLine appends one line to the record at path, which must exist,
// and syncs it.
func appendLine(path string, v any) error {
	data, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(data, '\n'))
	serr := f.Sync()
	cerr := f.Close()
	return errors.Join(werr, serr, cerr)
}

// Open appends the run's open line; it is called while the lock is held,
// before the run's first call.
func (r *Record) Open(runID, kind string, start time.Time, model, approval string, capNano int64) error {
	if r.lock == "" {
		return errors.New("aievals: the spend record's lock is not held")
	}
	start = start.UTC()
	o := openLine{Event: "open", RunID: runID, Kind: kind, StartedAt: start.Format(stamp), Day: start.Format(time.DateOnly),
		Month: start.Format("2006-01"), Model: model, Approval: approval, CapNanoUSD: capNano, Report: ReportName(kind, start, runID, model)}
	if err := o.check(); err != nil {
		return fmt.Errorf("aievals: the open line: %w", err)
	}
	return appendLine(r.path, o)
}

// CloseRun appends the close line of run runID to the record at path:
// what the run cost (settled) and what its meter held (committed).
func CloseRun(path, runID string, end time.Time, settled, committed int64, result string) error {
	end = end.UTC()
	return appendLine(path, closeLine{Event: "close", RunID: runID, ClosedAt: end.Format(stamp), Day: end.Format(time.DateOnly),
		Month: end.Format("2006-01"), SettledNanoUSD: max(settled, 0), CommittedNanoUSD: max(committed, settled, 0), Result: result})
}
