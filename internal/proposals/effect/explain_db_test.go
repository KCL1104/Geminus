package effect_test

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/analyses"
	"github.com/kcl1104/geminus/internal/analyses/analyzer"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/runs"
	"github.com/kcl1104/geminus/internal/storage/investigations"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/worker"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// H-061 §12 and §13 with H-086 (R), through the real logins: the re-ask of
// a refused explanation is a second paid call, and stopping an
// investigation does not end its running analysis job's context. So an
// explanation the investigation asked for (explain_results through
// analyses.Queue in its acting-for scope) whose first answer the server's
// check refuses, and whose investigation the requester stops while that
// first call is in flight, is not asked again: the analyzer reads the
// investigation again under the job's lease before the re-ask. One analyst
// request; the analysis fails with the first refusal, ANALYSIS_UNGROUNDED,
// and its analysis.attempt_failed event records no re-ask. While the
// investigation still runs, the same refusal is asked again once and the
// analysis is the second answer.
func TestAnExplanationIsNotAskedAgainOnceItsInvestigationStops(t *testing.T) {
	for _, stop := range []bool{true, false} {
		e := setup(t)
		m := e.model(t, e.scope(tenant.Editor), e.project)
		inv := e.start(t, investigations.Scenario, m.ID, m.LatestVersion.ID)
		base, run := e.completedPair(t, inv, e.propose(t, inv, "Add a CI runner", raiseCI))

		const question = "Why is the lead time shorter?"
		f := fake.New()
		f.Script(provider.RoleAnalyst, question, explained(t, "Lead time fell by 4242.5 minutes."))
		f.ScriptRevision(provider.RoleAnalyst, question, explained(t, "Lead time fell with the extra CI runner."))
		b := &stopsTheFirstCall{Backend: f}
		if stop {
			b.stop = func() error {
				return e.db.Run(context.Background(), e.scope(tenant.Editor), tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
					return investigations.Stop(ctx, tx, e.org.ID, inv.ID, e.editor())
				})
			}
		}
		client, err := provider.New(b, provider.DefaultBudget())
		if err != nil {
			t.Fatal(err)
		}
		h, err := analyzer.New(analyzer.Config{Provider: client, Packs: e.reg, Actor: "test-worker", Log: discard()})
		if err != nil {
			t.Fatal(err)
		}
		w, err := worker.New(e.wscope, worker.Config{ID: "test-worker", Policy: fast, Heartbeat: time.Second, Idle: 20 * time.Millisecond,
			ReapLimit: 10, Handlers: map[jobs.Type]handler.Handler{jobs.Analysis: h}, Log: discard()})
		if err != nil {
			t.Fatal(err)
		}
		var explainedID string
		if err := e.acting(inv, func(ctx context.Context, tx tenant.Tx, mb tenant.Membership) error {
			a, err := e.ana.Queue(ctx, tx, mb, ids.New("req"), run, analyses.CreateRequest{BaselineRunID: base, Question: question, OutputLocale: "en"})
			explainedID = a.ID
			return err
		}); err != nil {
			t.Fatalf("explain_results in the scope: %v", err)
		}
		var status, code string
		deadline := time.Now().Add(time.Minute)
		for status != "completed" && status != "failed" {
			if time.Now().After(deadline) {
				t.Fatalf("stop %v: the analysis is still %s", stop, status)
			}
			w.Pass(context.Background())
			e.owner(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT status, coalesce(error->>'code', '') FROM twin.ai_analyses WHERE id=$1`, explainedID).Scan(&status, &code)
			})
		}
		if err := b.stopErr(); err != nil {
			t.Fatalf("stopping the investigation: %v", err)
		}
		var state string
		e.owner(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT status FROM twin.investigations WHERE id=$1`, inv.ID).Scan(&state)
		})
		analyst := 0
		for _, r := range f.Requests() {
			if r.Prompt.Role == provider.RoleAnalyst {
				analyst++
			}
		}
		if stop {
			if state != "stopped" || status != "failed" || code != analyzer.Ungrounded || analyst != 1 {
				t.Errorf("stopped: the investigation %s, the analysis %s %s, %d analyst requests: want the first refusal and one call", state, status, code, analyst)
			}
			ev := e.only(t, explainedID, "analysis.attempt_failed")
			if _, ok := ev.meta["repair"]; ok || ev.meta["code"] != analyzer.Ungrounded || ev.meta["issues"] != float64(1) {
				t.Errorf("analysis.attempt_failed %v: want the first refusal and no re-ask", ev.meta)
			}
			continue
		}
		if state != "running" || status != "completed" || analyst != 2 {
			t.Errorf("running: the investigation %s, the analysis %s %s, %d analyst requests: want the re-ask's analysis", state, status, code, analyst)
		}
		if ev := e.only(t, explainedID, "analysis.generate"); ev.meta["repair"] != "grounded" {
			t.Errorf("analysis.generate %v", ev.meta)
		}
	}
}

// completedPair saves proposal p of inv as a scenario and runs the baseline
// (slot 0) and that scenario (slot 1) in inv's scope, as run_what_if does,
// until both completed; it returns their IDs.
func (e *env) completedPair(t *testing.T, inv investigations.Investigation, p proposals.Proposal) (base, run string) {
	t.Helper()
	saved, err := e.save(inv, p.ProposalID)
	if err != nil {
		t.Fatalf("save_scenario: %v", err)
	}
	var b, r runs.Accepted
	if err := e.acting(inv, func(ctx context.Context, tx tenant.Tx, mb tenant.Membership) error {
		var err error
		if b, err = e.runs.Queue(ctx, tx, mb, ids.New("req"), runs.CreateRequest{ModelVersionID: inv.BaseVersionID, Name: "Baseline",
			ScenarioPatch: jsontext.Value(`[]`), Config: runConfig}); err != nil {
			return err
		}
		r, err = e.runs.Queue(ctx, tx, mb, ids.New("req"), runs.CreateRequest{ModelVersionID: inv.BaseVersionID, Name: saved.Name,
			ScenarioID: saved.ScenarioID, Config: runConfig})
		return err
	}); err != nil {
		t.Fatalf("run_what_if in the scope: %v", err)
	}
	deadline := time.Now().Add(time.Minute)
	for e.count(t, `SELECT count(*) FROM twin.simulation_runs WHERE investigation_id=$1 AND status='completed'`, inv.ID) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the AI's runs did not complete")
		}
		e.w.Pass(context.Background())
		time.Sleep(10 * time.Millisecond)
	}
	return b.ID, r.ID
}

// explained is an explanation with summary as the scenario's.
func explained(t *testing.T, summary string) []byte {
	t.Helper()
	b, err := json.Marshal(provider.AnalysisOutput{Baseline: &provider.AnalysisSummary{Summary: "The baseline ran with its CI runners."},
		Scenario: provider.AnalysisSummary{Summary: summary}, Delta: []provider.AnalysisDelta{}, Bottleneck: []provider.Bottleneck{},
		Assumptions: []string{}, Uncertainty: []string{"Only one replication was run."}, SuggestedNextExperiments: []provider.Experiment{}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// stopsTheFirstCall runs stop, when set, while the first analyst request is
// being answered: a person stopping the investigation during the call.
type stopsTheFirstCall struct {
	*fake.Backend
	stop func() error
	mu   sync.Mutex
	done bool
	err  error
}

func (s *stopsTheFirstCall) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	s.mu.Lock()
	if s.stop != nil && !s.done && req.Prompt.Role == provider.RoleAnalyst {
		s.done, s.err = true, s.stop()
	}
	s.mu.Unlock()
	return s.Backend.Complete(ctx, req)
}

func (s *stopsTheFirstCall) stopErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
