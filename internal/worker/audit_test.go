package worker_test

import (
	"context"
	"encoding/json/v2"
	"maps"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// enqueueWith is enqueue with the payload's request_id (as every real
// enqueue writes one).
func (e *env) enqueueWith(t *testing.T, o dbtest.Org, attempts int, requestID string) jobs.Job {
	t.Helper()
	imp := dbtest.NewImport(t, e.admin, o)
	var j jobs.Job
	if err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		j, err = jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: o.ID, Type: jobs.ImportProfile,
			Resource: jobs.Resource{ImportID: imp}, Payload: map[string]any{"import_id": imp, "request_id": requestID}, MaxAttempts: attempts}, policy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return j
}

// event is one audit row as these tests read it.
type event struct {
	ActorType, ActorID, TargetType, TargetID, RequestID string
	Metadata                                            map[string]any
}

// events are the job.attempt_failed events of j's import, oldest first.
func (e *env) events(t *testing.T, j jobs.Job) []event {
	t.Helper()
	rows, err := e.admin.Query(context.Background(), `SELECT actor_type, actor_id, target_type, target_id, request_id, metadata::text
		FROM twin.audit_events WHERE organization_id=$1 AND action='job.attempt_failed' AND target_id=$2 ORDER BY created_at, id`,
		j.OrganizationID, j.Resource.ImportID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (event, error) {
		var ev event
		var meta string
		if err := r.Scan(&ev.ActorType, &ev.ActorID, &ev.TargetType, &ev.TargetID, &ev.RequestID, &meta); err != nil {
			return ev, err
		}
		return ev, json.Unmarshal([]byte(meta), &ev.Metadata)
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (e *env) auditCount(t *testing.T, org string) int {
	t.Helper()
	var n int
	if err := e.admin.QueryRow(context.Background(), `SELECT count(*) FROM twin.audit_events WHERE organization_id=$1`, org).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// T-035 (D-091 (b)), TEST-027: an attempt that fails is audited in the
// transaction that records it, on the job's resource, by the worker, under
// the request that queued the job: what failed, whether it is tried again.
func TestFailedAttemptsAreAudited(t *testing.T) {
	e := setup(t, 1)
	o := e.orgs[0]
	req := ids.New("req")
	final := e.enqueueWith(t, o, 1, req)
	retried := e.enqueueWith(t, o, 0, req)
	untracked := e.enqueue(t, o, jobs.ImportProfile, 1) // a payload without a request ID
	h := handler.Func(func(ctx context.Context, run handler.Run) error {
		if run.Job().ID == retried.ID {
			return &jobs.Failure{Code: "STORAGE_UNAVAILABLE", Message: "the object store did not answer", Retryable: true}
		}
		return &jobs.Failure{Code: "INVALID_INPUT", Message: "the upload is not CSV"}
	})
	w := e.worker(t, config(h, e.log))
	for range 3 {
		if !w.Pass(context.Background()) {
			t.Fatal("a pass ran no job")
		}
	}
	for _, c := range []struct {
		j             jobs.Job
		status        jobs.Status
		code, outcome string
		retryable     bool
		max           float64
		correlated    bool
	}{
		{final, jobs.Failed, "INVALID_INPUT", "failed", false, 1, true},
		{retried, jobs.Queued, "STORAGE_UNAVAILABLE", "requeued", true, 3, true},
		{untracked, jobs.Failed, "INVALID_INPUT", "failed", false, 1, false},
	} {
		if got := e.job(t, c.j); got.Status != c.status || stored(t, got).Code != c.code {
			t.Errorf("%s: %s %s", c.j.ID, got.Status, got.Error)
		}
		evs := e.events(t, c.j)
		if len(evs) != 1 {
			t.Fatalf("%s: %d events", c.j.ID, len(evs))
		}
		ev := evs[0]
		want := map[string]any{"job_id": c.j.ID, "job_type": "import_profile", "attempt": float64(1), "max_attempts": c.max,
			"code": c.code, "retryable": c.retryable, "outcome": c.outcome}
		if !c.correlated {
			want["uncorrelated"] = true
		}
		if ev.ActorType != "worker" || ev.ActorID != "worker-1" || ev.TargetType != "import" || ev.TargetID != c.j.Resource.ImportID ||
			!maps.Equal(ev.Metadata, want) {
			t.Errorf("%s: %+v", c.j.ID, ev)
		}
		// (The metadata is exactly want: the failure's message, the job's
		// record, is not in it.)
		if c.correlated && ev.RequestID != req || !c.correlated && (ev.RequestID == req || !strings.HasPrefix(ev.RequestID, "req_")) {
			t.Errorf("%s: request %s", c.j.ID, ev.RequestID)
		}
	}
	// A job that completes writes no failure event.
	done := e.enqueueWith(t, o, 0, req)
	ok := handler.Func(func(ctx context.Context, run handler.Run) error {
		return run.Finish(ctx, jobs.Completed, profile(run.Job()))
	})
	if !e.worker(t, config(ok, e.log)).Pass(context.Background()) {
		t.Fatal("the pass ran no job")
	}
	if n := len(e.events(t, done)); n != 0 || e.job(t, done).Status != jobs.Completed {
		t.Errorf("a completed job: %d failure events", n)
	}
}

// T-035 (D-091 (b)): each job the reaper takes back is audited in the
// reap's transaction, by the reaping worker: LEASE_EXPIRED, queued again
// or failed.
func TestTheReaperAudits(t *testing.T) {
	e := setup(t, 1)
	o := e.orgs[0]
	req := ids.New("req")
	again := e.enqueueWith(t, o, 0, req)
	last := e.enqueueWith(t, o, 1, req)
	for range 2 {
		if err := e.scope.Run(context.Background(), o.ID, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
			_, _, ok, err := jobs.Claim(ctx, tx, o.ID, "crashed-worker", []jobs.Type{jobs.ImportProfile}, policy)
			if !ok && err == nil {
				t.Fatal("nothing to claim")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	e.exec(t, `UPDATE twin.jobs SET lease_expires_at=now()-interval '1 second' WHERE id = ANY($1)`, []string{again.ID, last.ID})
	if e.worker(t, config(noop, e.log)).Pass(context.Background()) {
		t.Error("a reaped job ran before its backoff")
	}
	for _, c := range []struct {
		j       jobs.Job
		status  jobs.Status
		outcome string
	}{{again, jobs.Queued, "requeued"}, {last, jobs.Failed, "failed"}} {
		if got := e.job(t, c.j); got.Status != c.status || stored(t, got).Code != jobs.LeaseExpired {
			t.Errorf("%s: %s %s", c.j.ID, got.Status, got.Error)
		}
		evs := e.events(t, c.j)
		if len(evs) != 1 || evs[0].ActorID != "worker-1" || evs[0].RequestID != req || evs[0].Metadata["code"] != jobs.LeaseExpired ||
			evs[0].Metadata["outcome"] != c.outcome || evs[0].Metadata["retryable"] != true || evs[0].Metadata["job_id"] != c.j.ID {
			t.Errorf("%s: %+v", c.j.ID, evs)
		}
	}
}

// T-035 (D-091 (c)), the other half: when the audit event of a business
// write cannot be written, the write is not committed either. The database
// refuses this organization's events: a handler's audited result, the
// loop's failure record and the reaper's take-back all leave the job and
// its import as they were, until the refusal ends.
func TestAnAuditFailureCommitsNothing(t *testing.T) {
	e := setup(t, 1)
	o := e.orgs[0]
	req := ids.New("req")
	j := e.enqueueWith(t, o, 1, req)
	stop := dbtest.RefuseAudit(t, e.admin, o.ID)
	before := e.auditCount(t, o.ID)
	var finishErr error
	h := handler.Func(func(ctx context.Context, run handler.Run) error {
		job := run.Job()
		// A result whose audit event is refused: nothing of it commits.
		finishErr = run.Finish(ctx, jobs.Completed, func(ctx context.Context, tx tenant.Tx) error {
			if err := profile(job)(ctx, tx); err != nil {
				return err
			}
			return auditlog.AppendWorker(ctx, tx, job.OrganizationID, "worker-1", auditlog.Event{Action: "import.profile", TargetType: "import",
				TargetID: job.Resource.ImportID, RequestID: req, Metadata: map[string]any{"attempt": job.Attempt, "status": "AWAITING_MAPPING"}})
		})
		if finishErr == nil {
			t.Error("the refused result committed")
		}
		return &jobs.Failure{Code: "INVALID_INPUT", Message: "the upload is not CSV"}
	})
	w := e.worker(t, config(h, e.log))
	if !w.Pass(context.Background()) {
		t.Fatal("the pass ran no job")
	}
	// Neither the result nor the failure's record committed: the job is
	// still this attempt's, running, and the import untouched.
	got := e.job(t, j)
	if got.Status != jobs.Running || got.Error != nil || got.LockedBy != "worker-1" || got.Attempt != 1 {
		t.Errorf("after the refused failure record: %+v", got)
	}
	if s, rows, _ := e.importRow(t, j); s != "UPLOADED" || rows != 0 {
		t.Errorf("the import is %s with %d rows", s, rows)
	}
	if !strings.Contains(e.log.String(), "recording the failure failed") {
		t.Errorf("the refused record is not logged: %s", e.log.String())
	}
	// The lease ends; the reaper's take-back is refused as well.
	e.exec(t, `UPDATE twin.jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, j.ID)
	w.Pass(context.Background())
	if got := e.job(t, j); got.Status != jobs.Running || got.Error != nil {
		t.Errorf("the refused reap committed: %+v", got)
	}
	if !strings.Contains(e.log.String(), "reaping failed") {
		t.Errorf("the refused reap is not logged: %s", e.log.String())
	}
	if n := e.auditCount(t, o.ID); n != before {
		t.Errorf("%d audit events while they were refused, %d before", n, before)
	}
	// Once events can be written again, the next pass takes the job back,
	// with its event.
	stop()
	w.Pass(context.Background())
	if got := e.job(t, j); got.Status != jobs.Failed || stored(t, got).Code != jobs.LeaseExpired {
		t.Errorf("after the refusal: %s %s", got.Status, got.Error)
	}
	if s := e.importStatus(t, j); s != "FAILED" {
		t.Errorf("the import is %s", s)
	}
	if evs := e.events(t, j); len(evs) != 1 || evs[0].Metadata["code"] != jobs.LeaseExpired || evs[0].Metadata["outcome"] != "failed" {
		t.Errorf("%+v", evs)
	}
}

// T-035 (D-093 (b)): a handler's lines, logged with the context the loop
// gives it, carry the job's organization, ID, type, attempt, import and
// the request that queued it, through the privacy filter.
func TestTheHandlersLinesCarryTheJob(t *testing.T) {
	e := setup(t, 1)
	o := e.orgs[0]
	req := ids.New("req")
	j := e.enqueueWith(t, o, 0, req)
	log := observability.NewLogger(e.log, "worker")
	h := handler.Func(func(ctx context.Context, run handler.Run) error {
		log.WarnContext(ctx, "inside the handler", "error", `cell "CASE-SENTINEL" is not a number`)
		return run.Finish(ctx, jobs.Completed, profile(run.Job()))
	})
	cfg := config(h, e.log)
	cfg.Log = log
	if !e.worker(t, cfg).Pass(context.Background()) {
		t.Fatal("the pass ran no job")
	}
	var inside, ended map[string]any
	for _, l := range strings.Split(strings.TrimSpace(e.log.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) != nil {
			continue
		}
		switch m["msg"] {
		case "inside the handler":
			inside = m
		case "job ended":
			ended = m
		}
	}
	for name, m := range map[string]map[string]any{"handler": inside, "loop": ended} {
		if m == nil || m["organization_id"] != o.ID || m["job_id"] != j.ID || m["job_type"] != "import_profile" || m["attempt"] != float64(1) ||
			m["import_id"] != j.Resource.ImportID || m["request_id"] != req || m["component"] != "worker" {
			t.Errorf("%s line: %v", name, m)
		}
	}
	if strings.Contains(e.log.String(), "SENTINEL") {
		t.Errorf("a quoted value reached the log: %s", e.log.String())
	}
}
