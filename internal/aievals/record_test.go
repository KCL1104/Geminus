package aievals_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/aievals"
)

// T-053 review round 1, H1: the spend record of the live eval and smoke
// (checks/ai-evals/live-spend.jsonl), at fixed times. The command's use of
// it (refusals, the open line before any call, the close line at every end)
// is tested in internal/config/aiapproval, the only package that can turn
// live AI on in a test.

// record writes content to a new record and opens it (lock held).
func record(t *testing.T, content string) (*aievals.Record, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), aievals.RecordName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := aievals.OpenRecord(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(r.Release)
	return r, path
}

// A run is appended open, then closed; what the record says it spent in a
// UTC day and month is each closed run's committed amount in the days and
// months it opened and closed in, each open run's whole cap, nothing of
// other days and months.
func TestTheRecordSumsTheDayAndTheMonth(t *testing.T) {
	usd := spend.NanoPerUSD
	r, path := record(t, "")
	day1 := time.Date(2026, 10, 30, 23, 50, 0, 0, time.UTC)
	ids := []string{"01JB0000000000000000000001", "01JB0000000000000000000002", "01JB0000000000000000000003", "01JB0000000000000000000004"}
	// Run 1: opened and closed on 30 October, USD 0.40 of a USD 2 cap.
	if err := r.Open(ids[0], "smoke", day1, "claude-opus-5-5", "H-018", 2*usd); err != nil {
		t.Fatal(err)
	}
	if err := aievals.CloseRun(path, ids[0], day1.Add(time.Minute), 400_000_000, 400_000_000, aievals.ResultComplete); err != nil {
		t.Fatal(err)
	}
	// Run 2: opened on 30 October, closed on 1 November (another day and
	// month), USD 3 committed of USD 2.5 settled.
	if err := r.Open(ids[1], "eval", day1.Add(5*time.Minute), "claude-opus-5-5", "H-018", 8*usd); err != nil {
		t.Fatal(err)
	}
	if err := aievals.CloseRun(path, ids[1], day1.Add(26*time.Hour), 2_500_000_000, 3*usd, aievals.ResultStopped); err != nil {
		t.Fatal(err)
	}
	// Run 3: opened on 1 November, never closed (a USD 5 cap).
	if err := r.Open(ids[2], "eval", day1.Add(25*time.Hour), "claude-opus-5-5", "H-018", 5*usd); err != nil {
		t.Fatal(err)
	}
	// Run 4: September, closed.
	if err := r.Open(ids[3], "eval", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), "claude-opus-5-5", "H-018", 9*usd); err != nil {
		t.Fatal(err)
	}
	if err := aievals.CloseRun(path, ids[3], time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC), 9*usd, 9*usd, aievals.ResultComplete); err != nil {
		t.Fatal(err)
	}
	r.Release()
	again, err := aievals.OpenRecord(path)
	if err != nil {
		t.Fatalf("the record written does not read: %v", err)
	}
	defer again.Release()
	for _, c := range []struct {
		now        time.Time
		day, month int64
	}{
		{day1, 400_000_000 + 3*usd, 400_000_000 + 3*usd},                                        // 30 Oct: runs 1 and 2
		{day1.Add(25 * time.Hour), 3*usd + 5*usd, 3*usd + 5*usd},                                // 1 Nov: run 2's close, run 3 open at its cap
		{day1.Add(-24 * time.Hour), 0, 400_000_000 + 3*usd},                                     // 29 Oct: nothing that day; October's runs
		{time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC), 0, 9 * usd},                              // September
		{time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), 0, 0},                                    // December
		{day1.In(time.FixedZone("UTC-10", -10*3600)), 400_000_000 + 3*usd, 400_000_000 + 3*usd}, // the UTC day, whatever the zone
	} {
		if day, month := again.Spent(c.now); day != c.day || month != c.month {
			t.Errorf("%s: day %d month %d, want %d %d", c.now, day, month, c.day, c.month)
		}
	}
	data, _ := os.ReadFile(path)
	if lines := strings.Count(string(data), "\n"); lines != 7 {
		t.Errorf("%d lines, want 7 (4 opens, 3 closes)", lines)
	}
}

// One run at a time reads the record and appends its open line: the lock
// is a file next to the record, taken exclusively, given back on Release;
// while it is held, or when a run stopped while holding it, every other
// run is refused.
func TestTheRecordIsLockedWhileARunOpens(t *testing.T) {
	r, path := record(t, "")
	if _, err := aievals.OpenRecord(path); !errors.Is(err, aievals.ErrRecordLocked) {
		t.Errorf("a second open while the first holds the lock: %v", err)
	}
	r.Release()
	r.Release() // twice is fine
	again, err := aievals.OpenRecord(path)
	if err != nil {
		t.Fatalf("after the release: %v", err)
	}
	again.Release()
	if err := again.Open("01JB0000000000000000000001", "eval", time.Now(), "claude-opus-5-5", "H-018", 1); err == nil {
		t.Error("an open line without the lock")
	}
	// A record that does not parse is refused, and the lock is not kept.
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := aievals.OpenRecord(path); err == nil || errors.Is(err, aievals.ErrRecordLocked) {
		t.Errorf("a malformed record: %v", err)
	}
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the lock was kept after a refusal: %v", err)
	}
	// A missing record is refused (it is committed: never started again
	// empty).
	if _, err := aievals.OpenRecord(filepath.Join(t.TempDir(), aievals.RecordName)); err == nil {
		t.Error("a missing record was opened")
	}
}

// An open line the record refuses is never written: the open line is
// checked as a read line is.
func TestAnOpenLineIsChecked(t *testing.T) {
	r, path := record(t, "")
	for name, c := range map[string]struct {
		id, kind, model, approval string
		capNano                   int64
	}{
		"not a run ID":    {"run-1", "eval", "claude-opus-5-5", "H-018", 1},
		"an unknown kind": {"01JB0000000000000000000001", "both", "claude-opus-5-5", "H-018", 1},
		"no cap":          {"01JB0000000000000000000001", "eval", "claude-opus-5-5", "H-018", 0},
		"not a model":     {"01JB0000000000000000000001", "eval", "Claude Opus", "H-018", 1},
		"not an approval": {"01JB0000000000000000000001", "eval", "claude-opus-5-5", "h 18", 1},
	} {
		if err := r.Open(c.id, c.kind, time.Now(), c.model, c.approval, c.capNano); err == nil {
			t.Errorf("%s: written", name)
		}
	}
	if data, _ := os.ReadFile(path); len(data) != 0 {
		t.Errorf("the record: %q", data)
	}
}
