package analyzer

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// H-061 §9 (slice A6, re-applied on the analysis graph when A6 met E2-E6):
// an analysis whose row carries an investigation's label (started's
// investigation, read under the lease by storage/investigations.LabelOf) is
// asked with the investigation-rules-1 line once; a person's analysis is
// asked as before, without it, and both come back checked.
func TestAnInvestigationsAnalysisIsAskedWithItsRule(t *testing.T) {
	const rule = "\n## Investigation rules (investigation-rules-1)\n\n"
	var hashes []string
	for _, labelled := range []bool{false, true} {
		f := fake.New()
		c, err := provider.New(f, provider.DefaultBudget())
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		h := setup(t, c, nil, provider.DefaultBudget(), &buf)
		st := fixture(t)
		st.investigation = labelled
		a, err := h.analyze(context.Background(), tOrg, st, tAttr)
		meta, issues := a.meta, a.issues
		if err != nil || meta == nil || issues != 0 {
			t.Fatalf("labelled %v: %v, %d issues", labelled, err, issues)
		}
		reqs := f.Requests()
		if len(reqs) != 1 || reqs[0].Prompt.Role != provider.RoleAnalyst {
			t.Fatalf("labelled %v: %d requests", labelled, len(reqs))
		}
		want := 0
		if labelled {
			want = 1
		}
		if n := strings.Count(reqs[0].Prompt.System, rule); n != want {
			t.Errorf("labelled %v: the call carries the rule %d times", labelled, n)
		}
		hashes = append(hashes, meta.InputHash)
	}
	if hashes[0] == hashes[1] {
		t.Error("the labelled prompt hashes as the person's")
	}
}

// H-061 §12 and §13 with H-086 (R): the re-ask of a refused explanation is a
// second paid call, and a stop does not end the analysis job's context, so
// an investigation's analysis asks again only while its investigation still
// runs, read again (started.stillRuns, under the lease) after the first
// answer was refused and before the follow-up is built. Once it has ended,
// the first refusal stands: ANALYSIS_UNGROUNDED, not retried, the first
// call's metadata, no second request or reservation, no re-ask record, and
// a log line saying why. A read that fails is the same refusal, logged as
// an error. A labelled analysis without the read asks nothing more; a
// person's analysis is never asked about it.
func TestAnInvestigationsAnalysisIsAskedAgainOnlyWhileItRuns(t *testing.T) {
	errRead := errors.New("the database is gone")
	for name, c := range map[string]struct {
		labelled bool
		read     func(context.Context) (bool, error) // nil for none
		asked    int
		again    bool   // the re-ask was made
		line     string // the extra log line's msg, "" for none
		level    string
	}{
		"it still runs":       {labelled: true, read: func(context.Context) (bool, error) { return true, nil }, asked: 1, again: true},
		"it has ended":        {labelled: true, read: func(context.Context) (bool, error) { return false, nil }, asked: 1, line: "the analysis's investigation has ended; its refused answer is not asked again", level: "INFO"},
		"the read fails":      {labelled: true, read: func(context.Context) (bool, error) { return true, errRead }, asked: 1, line: "reading an analysis's investigation before its re-ask failed; the first refusal stands", level: "ERROR"},
		"no read":             {labelled: true},
		"a person's analysis": {read: func(context.Context) (bool, error) { return false, nil }, again: true},
	} {
		t.Run(name, func(t *testing.T) {
			f := fake.New()
			f.Script(provider.RoleAnalyst, tQuestion, invented(t))
			f.ScriptRevision(provider.RoleAnalyst, tQuestion, fixedAnswer(t))
			l := &ledger{m: provider.NewMemoryMeter(1_000_000_000_000)}
			client, err := provider.NewMetered(billed{f}, provider.DefaultBudget(), l)
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			h := setup(t, client, nil, provider.DefaultBudget(), &buf)
			st := fixture(t)
			st.investigation = c.labelled
			asked := 0
			if c.read != nil {
				st.stillRuns = func(ctx context.Context) (bool, error) {
					asked++
					if len(f.Requests()) != 1 {
						t.Errorf("asked with %d requests made, want after the first call", len(f.Requests()))
					}
					return c.read(ctx)
				}
			}
			a, err := h.analyze(context.Background(), tOrg, st, tAttr)
			if asked != c.asked {
				t.Errorf("asked %d times, want %d", asked, c.asked)
			}
			if c.again {
				if err != nil || a.recheck == nil || a.recheck.Outcome != "grounded" || len(f.Requests()) != 2 || len(l.charges) != 2 {
					t.Errorf("%v %+v, %d requests, %d charges: want the re-ask's analysis", err, a.recheck, len(f.Requests()), len(l.charges))
				}
				return
			}
			var fl *jobs.Failure
			if !errors.As(err, &fl) || fl.Code != Ungrounded || fl.Retryable || fl.Message != "the AI answer is not grounded in the results (ANALYSIS_NUMBER_UNVERIFIED at /scenario/summary)" {
				t.Fatalf("%v, want the first refusal, not retried", err)
			}
			if a.recheck != nil || a.meta == nil || a.meta.TemplateVersion != "analyst-0.3.3" || a.meta.SpendReservation == "" || a.issues != 1 {
				t.Errorf("%+v: want the first call's metadata and issues, no re-ask", a)
			}
			if len(f.Requests()) != 1 || len(l.charges) != 1 || len(l.settled) != 1 {
				t.Errorf("%d requests, %d charges, %d settled: want the first call only", len(f.Requests()), len(l.charges), len(l.settled))
			}
			var extra []map[string]any
			for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
				var m map[string]any
				if err := json.Unmarshal([]byte(line), &m); err != nil {
					t.Fatalf("a log line that is not JSON: %q", line)
				}
				if m["msg"] != "ai provider call ended" {
					extra = append(extra, m)
				}
			}
			switch {
			case c.line == "" && len(extra) != 0:
				t.Errorf("logged %v, want only the call's line", extra)
			case c.line != "" && (len(extra) != 1 || extra[0]["msg"] != c.line || extra[0]["level"] != c.level || extra[0]["analysis_id"] != tAnalysis):
				t.Errorf("logged %v, want %s %q", extra, c.level, c.line)
			}
		})
	}
}

// stillRuns is the read the re-ask waits for: investigations.LabelOf of the
// analysis, in a Checkpoint under the job's lease; the Checkpoint's error
// (a stopped attempt's included) and the read's are its error, never a
// "still runs".
func TestStillRunsReadsTheInvestigationUnderTheLease(t *testing.T) {
	errQuery := errors.New("the query failed")
	for name, c := range map[string]struct {
		labelled, running bool
		query, stop       error
		want              bool
		err               error
	}{
		"it runs":      {labelled: true, running: true, want: true},
		"it has ended": {labelled: true},
		"the read":     {labelled: true, running: true, query: errQuery, err: errQuery},
		"the attempt":  {labelled: true, running: true, stop: handler.ErrStopped, err: handler.ErrStopped},
		"the lease":    {labelled: true, running: true, stop: jobs.ErrLeaseLost, err: jobs.ErrLeaseLost},
	} {
		t.Run(name, func(t *testing.T) {
			tx := &labelTx{labelled: c.labelled, running: c.running, err: c.query}
			hr := &checkpointRun{tx: tx, stop: c.stop}
			got, err := stillRuns(hr, tOrg, tAnalysis)(context.Background())
			if got != c.want || !errors.Is(err, c.err) || (c.err == nil) != (err == nil) {
				t.Errorf("%v %v, want %v %v", got, err, c.want, c.err)
			}
			if c.stop != nil {
				if hr.checkpoints != 1 || len(tx.queries) != 0 {
					t.Errorf("%d checkpoints, queries %v", hr.checkpoints, tx.queries)
				}
				return
			}
			if hr.checkpoints != 1 || len(tx.queries) != 1 || !strings.Contains(tx.queries[0].sql, "FROM twin.ai_analyses r LEFT JOIN twin.investigations i") ||
				!strings.Contains(tx.queries[0].sql, "i.status = 'running' AND now() < i.deadline_at") ||
				!reflect.DeepEqual(tx.queries[0].args, []any{tOrg, tAnalysis}) {
				t.Errorf("%d checkpoints, queries %+v", hr.checkpoints, tx.queries)
			}
		})
	}
}

// checkpointRun is a handler.Run whose Checkpoint runs fn on tx, or fails
// with stop before it, as a stopped attempt's does.
type checkpointRun struct {
	handler.Run
	tx          tenant.Tx
	stop        error
	checkpoints int
}

func (r *checkpointRun) Checkpoint(ctx context.Context, fn func(ctx context.Context, tx tenant.Tx) error) error {
	r.checkpoints++
	if r.stop != nil {
		return r.stop
	}
	return fn(ctx, r.tx)
}

// labelTx answers one row of investigations.LabelOf.
type labelTx struct {
	pgx.Tx
	labelled, running bool
	err               error
	queries           []query
}

type query struct {
	sql  string
	args []any
}

func (tx *labelTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	tx.queries = append(tx.queries, query{sql, args})
	return labelRow{tx}
}

type labelRow struct{ tx *labelTx }

func (r labelRow) Scan(dest ...any) error {
	if r.tx.err != nil {
		return r.tx.err
	}
	*dest[0].(*bool), *dest[1].(*bool) = r.tx.labelled, r.tx.running
	return nil
}
