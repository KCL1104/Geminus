package worker

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/jobs"
)

// Review L5: how an attempt ended whose failure the loop tried to record,
// for its span and metric: requeued or failed when recorded; lease_lost
// when the job was no longer the attempt's; not_recorded when the record
// failed otherwise.
func TestFailureOutcome(t *testing.T) {
	for _, c := range []struct {
		err     error
		retried bool
		want    string
	}{
		{nil, false, "failed"},
		{nil, true, "requeued"},
		{jobs.ErrLeaseLost, false, "lease_lost"},
		{fmt.Errorf("commit: %w", jobs.ErrLeaseLost), true, "lease_lost"},
		{errors.New("the database is down"), false, "not_recorded"},
		{errors.New("the database is down"), true, "not_recorded"},
	} {
		if got := failureOutcome(c.err, c.retried); got != c.want {
			t.Errorf("failureOutcome(%v, %v) = %s, want %s", c.err, c.retried, got, c.want)
		}
	}
}

// The loop's event carries the failure's own retryable flag, and every job
// type's event fits the catalogue (a refusal would leave the failure
// unrecorded, D-091 (c)).
func TestTheAttemptsEventOfEveryType(t *testing.T) {
	w := &Worker{cfg: Config{ID: "worker-7"}}
	for _, typ := range []jobs.Type{jobs.Simulation, jobs.ModelProposal, jobs.ImportProfile, jobs.ImportValidate, jobs.ImportMapping, jobs.Analysis} {
		r := jobs.Resource{ImportID: ids.New("imp")}
		switch typ {
		case jobs.Simulation:
			r = jobs.Resource{SimulationRunID: ids.New("sim")}
		case jobs.ModelProposal:
			r = jobs.Resource{AIProposalID: ids.New("aip")}
		case jobs.Analysis:
			r = jobs.Resource{AIAnalysisID: ids.New("ana")}
		}
		var got tx
		if err := w.audit(context.Background(), &got, job(typ, r, `{}`), jobs.Failure{Code: "ENGINE_FAILED", Message: "x"}, false); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		var meta map[string]any
		if err := json.Unmarshal([]byte(got.execs[0][9].(string)), &meta); err != nil || meta["retryable"] != false || meta["job_type"] != string(typ) {
			t.Errorf("%s: %v %v", typ, meta, err)
		}
	}
}

type fakeRuntime string

func (f fakeRuntime) Error() string { return string(f) }
func (fakeRuntime) RuntimeError()   {}

// Review L3: a recovered panic's message is logged only when the Go runtime
// raised it; a value of any other type, even one with a RuntimeError
// method, is named by its type alone.
func TestPanickedTrustsOnlyTheRuntime(t *testing.T) {
	if got := (panicked{value: fakeRuntime("cell CASE-SENTINEL")}).Error(); got != "handler panicked with a worker.fakeRuntime" {
		t.Errorf("%q", got)
	}
	var got string
	func() {
		defer func() { got = (panicked{value: recover()}).Error() }()
		var m map[string]int
		m["x"] = 1
	}()
	if got != "handler panicked: assignment to entry in nil map" || strings.Contains(got, "SENTINEL") {
		t.Errorf("%q", got)
	}
}
