package worker

import (
	"context"
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kcl1104/geminus/internal/audit"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/jobs"
)

// tx records the statements sent to it, as the loop's transaction would
// carry them; the rest of pgx.Tx is never called.
type tx struct {
	pgx.Tx
	execs [][]any
	fail  error
}

func (t *tx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.execs = append(t.execs, append([]any{sql}, args...))
	return pgconn.NewCommandTag("INSERT 0 1"), t.fail
}

func job(typ jobs.Type, r jobs.Resource, payload string) jobs.Job {
	return jobs.Job{ID: ids.New("job"), OrganizationID: ids.New("org"), Type: typ, Status: jobs.Running, Resource: r,
		Payload: []byte(payload), Attempt: 2, MaxAttempts: 3}
}

// T-035 (D-091 (b)): the loop's event for a failed attempt is
// job.attempt_failed on the job's resource, by the worker, under the
// request that queued the job, written through the transaction it is given
// (the one that records the failure), and an error writing it is returned
// so that transaction rolls back.
func TestTheAttemptsEvent(t *testing.T) {
	req := ids.New("req")
	run, prop, imp, ana := ids.New("sim"), ids.New("aip"), ids.New("imp"), ids.New("ana")
	w := &Worker{cfg: Config{ID: "worker-7"}}
	for _, c := range []struct {
		j              jobs.Job
		target, id     string
		requeued       bool
		correlated     bool
		key, wantedReq string
	}{
		{job(jobs.Simulation, jobs.Resource{SimulationRunID: run}, `{"run_id":"`+run+`","request_id":"`+req+`"}`), "simulation", run, false, true, "run_id", req},
		{job(jobs.ModelProposal, jobs.Resource{AIProposalID: prop}, `{"proposal_id":"`+prop+`","request_id":"`+req+`"}`), "ai_proposal", prop, true, true, "proposal_id", req},
		{job(jobs.ImportValidate, jobs.Resource{ImportID: imp}, `{"import_id":"`+imp+`","mapping_revision":2,"request_id":"`+req+`"}`), "import", imp, true, true, "import_id", req},
		{job(jobs.Analysis, jobs.Resource{AIAnalysisID: ana}, `{"analysis_id":"`+ana+`"}`), "ai_analysis", ana, false, false, "analysis_id", ""},
		// A request ID that is not one: never used.
		{job(jobs.ImportProfile, jobs.Resource{ImportID: imp}, `{"import_id":"`+imp+`","request_id":"req_SENTINEL"}`), "import", imp, false, false, "import_id", ""},
		{job(jobs.ImportProfile, jobs.Resource{ImportID: imp}, `not json`), "import", imp, false, false, "import_id", ""},
	} {
		var got tx
		f := jobs.Failure{Code: "LEASE_EXPIRED", Message: "the job's own words", Retryable: true}
		if err := w.audit(context.Background(), &got, c.j, f, c.requeued); err != nil {
			t.Fatalf("%s: %v", c.j.Type, err)
		}
		if len(got.execs) != 1 || !strings.Contains(got.execs[0][0].(string), "INSERT INTO twin.audit_events") {
			t.Fatalf("%s: %v", c.j.Type, got.execs)
		}
		a := got.execs[0][1:]
		if a[1] != c.j.OrganizationID || a[2] != "worker" || a[3] != "worker-7" || a[4] != "job.attempt_failed" || a[5] != c.target || a[6] != c.id {
			t.Errorf("%s: %v", c.j.Type, a)
		}
		if reqID := a[7].(string); (c.correlated && reqID != c.wantedReq) || (!c.correlated && (reqID == "" || !strings.HasPrefix(reqID, "req_") || strings.Contains(reqID, "SENTINEL"))) {
			t.Errorf("%s: request %q", c.j.Type, reqID)
		}
		var meta map[string]any
		if err := json.Unmarshal([]byte(a[8].(string)), &meta); err != nil {
			t.Fatal(err)
		}
		outcome := "failed"
		if c.requeued {
			outcome = "requeued"
		}
		if meta["job_id"] != c.j.ID || meta["job_type"] != string(c.j.Type) || meta["attempt"] != float64(2) || meta["max_attempts"] != float64(3) ||
			meta["code"] != "LEASE_EXPIRED" || meta["retryable"] != true || meta["outcome"] != outcome || (meta["uncorrelated"] == true) == c.correlated {
			t.Errorf("%s: %v", c.j.Type, meta)
		}
		// The failure's message is the job's own record, not the audit's.
		if strings.Contains(a[8].(string), "own words") {
			t.Errorf("%s: the message is audited: %s", c.j.Type, a[8])
		}
		// Every line about the job names it and its resource.
		kv := correlation(c.j)
		keys := []string{}
		for i := 0; i < len(kv); i += 2 {
			keys = append(keys, kv[i].(string))
		}
		want := []string{"job_id", "job_type", "attempt", c.key}
		if c.correlated {
			want = append(want, "request_id")
		}
		if !slices.Equal(keys, want) {
			t.Errorf("%s: correlation %v", c.j.Type, kv)
		}
	}
	// The database's error is the loop's, so the failure's transaction
	// rolls back with it.
	boom := errors.New("refused")
	failing := tx{fail: boom}
	if err := w.audit(context.Background(), &failing, job(jobs.Simulation, jobs.Resource{SimulationRunID: run}, `{}`), jobs.ReapFailure, true); !errors.Is(err, boom) {
		t.Errorf("%v", err)
	}
	// A worker name the catalogue refuses sends nothing.
	bad := &Worker{cfg: Config{ID: "1 bad"}}
	var none tx
	if err := bad.audit(context.Background(), &none, job(jobs.Simulation, jobs.Resource{SimulationRunID: run}, `{}`), jobs.ReapFailure, true); !errors.Is(err, audit.ErrInvalid) || len(none.execs) != 0 {
		t.Errorf("%v %v", err, none.execs)
	}
}
