package validate_test

import (
	"testing"

	"github.com/kcl1104/geminus/internal/model/validate"
)

// T-050: the rules of batch processes (D-054, BATCH_INVALID) on the
// minimal model (start -> work(worker) -> end, minutes; worker has
// capacity 1; entity type "change"). Paths are TWIN_IR_SPEC §4's; the
// details the approved text leaves open are D-096 (i), PROPOSED.

func batch(minSize, maxSize int, maxWait ...float64) func(ir map[string]any) {
	return func(ir map[string]any) {
		b := map[string]any{"min_size": minSize, "max_size": maxSize}
		if len(maxWait) > 0 {
			b["max_wait"] = maxWait[0]
		}
		obj(nodes(ir)[1])["batch"] = b
	}
}

// buffered puts a queue of capacity c (0: unlimited) in front of work.
func buffered(c int) func(ir map[string]any) {
	return func(ir map[string]any) {
		q := map[string]any{"id": "q_work", "type": "queue"}
		if c > 0 {
			q["capacity"] = c
		}
		ir["nodes"] = append(nodes(ir), q)
		obj(flows(ir)[0])["to"] = "q_work"
		ir["flows"] = append(flows(ir), flow("q_work__work", "q_work", "work", 1))
	}
}

func both(edits ...func(ir map[string]any)) func(ir map[string]any) {
	return func(ir map[string]any) {
		for _, e := range edits {
			e(ir)
		}
	}
}

func TestBatchRules(t *testing.T) {
	checkCases(t, []struct {
		name string
		edit func(ir map[string]any)
		want [][3]string
	}{
		{"a full batch only", batch(4, 4), nil},
		{"a full batch with a wait limit that cannot matter", batch(4, 4, 30), nil},
		{"min below max with a wait limit", batch(2, 4, 25), nil},
		{"a wait limit of 0", batch(1, 4, 0), nil},
		{"one at a time", batch(1, 1), nil},
		{"the largest batch", batch(1, 10000, 60), nil},
		{"a batch process without resources", both(batch(2, 3, 5), func(ir map[string]any) { obj(nodes(ir)[1])["requires"] = []any{} }), nil},
		{"a finite buffer as large as the batch", both(buffered(4), batch(2, 4, 10)), nil},
		{"a finite buffer larger than the batch", both(buffered(9), batch(4, 4)), nil},
		{"an unlimited buffer", both(buffered(0), batch(4, 4)), nil},
		// D-054: 1 <= min_size <= max_size (the schema has the bounds).
		{"min above max", batch(8, 4, 30), [][3]string{{validate.BatchInvalid, "/nodes/1/batch", "error"}}},
		// D-054: min_size < max_size needs max_wait.
		{"min below max without a wait limit", batch(2, 4), [][3]string{{validate.BatchInvalid, "/nodes/1/batch", "error"}}},
		// D-054: a finite buffer holds at least max_size (path TWIN_IR_SPEC §4).
		{"a finite buffer smaller than the batch", both(buffered(3), batch(2, 4, 10)),
			[][3]string{{validate.BatchInvalid, "/nodes/1/batch/max_size", "error"}}},
		{"a buffer of one under a batch of two", both(buffered(1), batch(2, 2)),
			[][3]string{{validate.BatchInvalid, "/nodes/1/batch/max_size", "error"}}},
		// D-096 (i): max_wait is a duration in the model's unit, at most 365
		// days once quantized (inclusive, like D-050's L <= 365 days).
		{"a wait limit of exactly 365 days", batch(1, 2, 365*day), nil},
		{"a wait limit of 365 days and one tick", batch(1, 2, 365*day+1.0/60000),
			[][3]string{{validate.BatchInvalid, "/nodes/1/batch/max_wait", "error"}}},
		{"a wait limit beyond 365 days with min = max", batch(3, 3, 400*day),
			[][3]string{{validate.BatchInvalid, "/nodes/1/batch/max_wait", "error"}}},
		{"two problems at once", both(buffered(1), batch(3, 2, 10)), [][3]string{
			{validate.BatchInvalid, "/nodes/1/batch", "error"}, {validate.BatchInvalid, "/nodes/1/batch/max_size", "error"}}},
	})
}

// BATCH_INVALID belongs to the distribution and domain semantics stage
// (TWIN_IR_SPEC §4): a reference error hides it, and it hides the bundle
// feasibility stage after it (a breakdown resource needed twice, R3).
func TestBatchRulesStage(t *testing.T) {
	r := run(t, edit(t, both(batch(5, 4, 1), func(ir map[string]any) { obj(flows(ir)[1])["to"] = "nowhere" })))
	if got := issuesOf(r); len(got) != 1 || got[0][0] != validate.UnknownReference {
		t.Errorf("with an unknown reference: %v", r.Issues)
	}
	r3 := withMachine(2, 2, breakdowns(constantDist(60), constantDist(5)))
	r = run(t, edit(t, both(batch(5, 4, 1), r3)))
	if got := issuesOf(r); len(got) != 1 || got[0] != [3]string{validate.BatchInvalid, "/nodes/1/batch", "error"} {
		t.Errorf("with R3 broken too: %v", r.Issues)
	}
	r = run(t, edit(t, both(batch(4, 4), r3)))
	if got := issuesOf(r); len(got) != 1 || got[0] != [3]string{validate.ResourceRequirementInvalid, "/nodes/1/requires/1/units", "error"} {
		t.Errorf("a valid batch, R3 broken: %v", r.Issues)
	}
}
