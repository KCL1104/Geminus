package generator

// DB-backed tests of H-057's one repair through the job loop as the plain
// worker login (without GEMINUS_TEST_DATABASE_URL they skip: NOT RUN).

import (
	"context"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
)

// slowFake answers as the fake does, after d (or when the attempt ends).
type slowFake struct {
	*fake.Backend
	d time.Duration
}

func (s slowFake) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	select {
	case <-time.After(s.d):
	case <-ctx.Done():
	}
	return s.Backend.Complete(ctx, req)
}

// The repair runs inside the job's one attempt, which the worker's
// heartbeat keeps leased for as long as it runs: two calls that together
// outlast the lease (3 s, renewed every second) complete the job in its
// first attempt, ready from the second answer; the row says it was revised
// (generated_by.revised_after_check, provider_metadata.repair) and the
// proposal.generate event names the repair; no attempt failed or was
// requeued.
func TestARepairOutlastsTheLease(t *testing.T) {
	e := setup(t)
	mdl, mv := e.model(t)
	f := fake.New()
	f.Script(provider.RoleModeler, repairIntent, answer(t, reference(brokenRouting)))
	f.ScriptRevision(provider.RoleModeler, repairIntent, answer(t, reference(fixedRouting)))
	id := e.create(t, mdl, mv, "edit", repairIntent)
	start := time.Now()
	p := e.until(t, e.loop(t, slowFake{Backend: f, d: 2 * fast.Lease / 3}), id)
	if took := time.Since(start); took < fast.Lease {
		t.Fatalf("the two calls took %v, not longer than the lease %v", took, fast.Lease)
	}
	if p.Status != "ready" || p.GeneratedBy == nil || !p.GeneratedBy.RevisedAfterCheck {
		t.Fatalf("%s %+v %s", p.Status, p.GeneratedBy, p.Validation)
	}
	if reqs := f.Requests(); len(reqs) != 2 || reqs[1].Prompt.Revision == nil {
		t.Fatalf("%d provider requests", len(reqs))
	}
	if n := e.count(t, `SELECT count(*) FROM twin.jobs WHERE ai_proposal_id=$1 AND status='completed' AND attempt=1`, id); n != 1 {
		t.Error("the job did not complete in its first attempt")
	}
	if n := e.count(t, `SELECT count(*) FROM twin.ai_proposals WHERE id=$1 AND provider_metadata->'repair'->>'outcome'='ready'
		AND provider_metadata->'repair'->'call'->>'prompt_template_version' LIKE '%+repair-0.1.0'`, id); n != 1 {
		t.Error("the row does not record the repair")
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='proposal.generate' AND metadata->>'repair'='ready'
		AND (metadata->>'repair_issues')::int > 0 AND metadata->>'status'='ready'`, id); n != 1 {
		t.Error("the proposal.generate event does not name the repair")
	}
	for _, action := range []string{"proposal.attempt_failed", "job.attempt_failed"} {
		if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action=$2`, id, action); n != 0 {
			t.Errorf("%d %s events", n, action)
		}
	}
}
