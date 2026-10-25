package runner

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/simulation/engine"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	store "github.com/kcl1104/geminus/internal/storage/runs"
)

// Without a database: each engine failure is the job failure the run
// keeps. D-050 B1 (T-047): ErrDeadlock is SIMULATION_DEADLOCK, never
// retried (the frozen input deadlocks again), with the engine's reason,
// here the hand-derived G-D2 one (testdata/simulation/blocking-goldens.json).
func TestEngineFailures(t *testing.T) {
	var doc struct {
		Cases []struct {
			ID       string `json:"id"`
			Expected struct {
				Deadlock *struct {
					Tick   int64  `json:"tick"`
					Entity string `json:"entity"`
					Node   string `json:"node"`
					Since  int64  `json:"since"`
					Reason string `json:"reason"`
				} `json:"deadlock"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/blocking-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	var de *engine.DeadlockError
	reason := ""
	for _, c := range doc.Cases {
		if d := c.Expected.Deadlock; d != nil && c.ID == "G-D2" {
			de, reason = &engine.DeadlockError{Tick: d.Tick, Entity: d.Entity, Node: d.Node, Since: d.Since}, d.Reason
		}
	}
	if de == nil {
		t.Fatal("no deadlock golden")
	}
	h := &Handler{}
	for _, c := range []struct {
		err       error
		code, msg string
	}{
		{fmt.Errorf("replication 3: %w", de), store.SimulationDeadlock, reason},
		{engine.ErrEventBudget, store.EventBudgetExceeded, "the run exceeded its event budget (max_events)"},
		{fmt.Errorf("%w: r0/s/1 entering n at tick 5", engine.ErrEntityVisits), store.EntityVisitsExceeded, "an entity entered more nodes than max_entity_visits"},
	} {
		var f *jobs.Failure
		if err := h.failed(c.err); !errors.As(err, &f) || f.Code != c.code || f.Message != c.msg || f.Retryable {
			t.Errorf("%v: %+v, want %s %q", c.err, err, c.code, c.msg)
		}
	}
	// An ErrDeadlock without its details is still a deadlock, with a fixed message.
	var f *jobs.Failure
	if err := h.failed(fmt.Errorf("%w: somewhere", engine.ErrDeadlock)); !errors.As(err, &f) || f.Code != store.SimulationDeadlock || f.Retryable {
		t.Errorf("bare ErrDeadlock: %+v", err)
	}
}

// D-050 B2 (T-048): ErrStalled is SIMULATION_STALLED, never retried (the
// frozen input stalls again), with the engine's reason, here the
// hand-derived G-S1 one (testdata/simulation/breakdown-goldens.json).
func TestStalledFailure(t *testing.T) {
	var doc struct {
		Cases []struct {
			ID       string `json:"id"`
			Expected struct {
				Stalled *struct {
					Tick     int64  `json:"tick"`
					Entity   string `json:"entity"`
					Node     string `json:"node"`
					Since    int64  `json:"since"`
					Deadline int64  `json:"deadline"`
					Reason   string `json:"reason"`
				} `json:"stalled"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/breakdown-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	var se *engine.StalledError
	reason := ""
	for _, c := range doc.Cases {
		if s := c.Expected.Stalled; s != nil && c.ID == "G-S1" {
			se, reason = &engine.StalledError{Tick: s.Tick, Entity: s.Entity, Node: s.Node, Since: s.Since, Deadline: s.Deadline}, s.Reason
		}
	}
	if se == nil {
		t.Fatal("no stall golden")
	}
	h := &Handler{}
	var f *jobs.Failure
	if err := h.failed(fmt.Errorf("replication 2: %w", se)); !errors.As(err, &f) || f.Code != store.SimulationStalled || f.Message != reason || f.Retryable {
		t.Errorf("stall: %+v, want %s %q", err, store.SimulationStalled, reason)
	}
	if err := h.failed(fmt.Errorf("%w: somewhere", engine.ErrStalled)); !errors.As(err, &f) || f.Code != store.SimulationStalled || f.Retryable || f.Message == "" {
		t.Errorf("bare ErrStalled: %+v", err)
	}
}
