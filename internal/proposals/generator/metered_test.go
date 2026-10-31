package generator

// DB-backed tests of a metered proposal job (T-053): the model_proposal
// job through the job loop as the plain worker login, with a billable test
// backend (the fake's answers, a billed usage) behind a metered client and
// the spend ledger (internal/storage/aispend) as its meter. No live
// provider: the backend is in this process. Without
// GEMINUS_TEST_DATABASE_URL they skip (NOT RUN).

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/storage/aispend"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/worker"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// billedFake answers as the fake does and reports a billed usage, as the
// live adapter would; it counts the calls that reached it.
type billedFake struct {
	*fake.Backend
	usage provider.Usage
	calls atomic.Int32
}

func (b *billedFake) Quote(_ context.Context, req provider.Request) (provider.Quote, error) {
	n := int64(len(req.Prompt.System) + len(req.Prompt.User()))
	if r := req.Prompt.Revision; r != nil {
		// H-057: a repair's two turns, as the live adapters quote them.
		n += int64(r.Prior.Len() + len(r.User()))
	}
	return provider.Quote{Model: "claude-opus-5-5", MaxInputTokens: n}, nil
}

func (b *billedFake) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	b.calls.Add(1)
	resp, err := b.Backend.Complete(ctx, req)
	resp.Usage, resp.Model, resp.Credential, resp.InferenceGeo = b.usage, "claude-opus-5-5", "org-a", "global"
	return resp, err
}

// meteredLoop is the job loop with the generator over b, metered by the
// ledger under limits; the ledger books the database's UTC day (T-053
// review round 1, L3: the meter has no clock of its own).
func (e *env) meteredLoop(t *testing.T, b provider.Billable, limits spend.Limits) *worker.Worker {
	t.Helper()
	m, err := aispend.New(aispend.Config{Actor: "test-worker", Limits: limits,
		Run: func(ctx context.Context, org string, fn func(ctx context.Context, tx tenant.Tx) error) error {
			return e.scope.Run(ctx, org, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error { return fn(ctx, tx) })
		}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := provider.NewMetered(b, provider.DefaultBudget(), m)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Config{Provider: c, Packs: packs(t), Actor: "test-worker", Log: discard()})
	if err != nil {
		t.Fatal(err)
	}
	w, err := worker.New(e.scope, worker.Config{ID: "test-worker", Policy: fast, Heartbeat: time.Second, Idle: 20 * time.Millisecond,
		ReapLimit: 10, Handlers: map[jobs.Type]handler.Handler{jobs.ModelProposal: h}, Log: discard()})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// A metered proposal is reserved for before its call and settled with
// what it cost after, on the proposal, under the request that queued it;
// the proposal's own event names the cost and the reservation.
func TestAMeteredProposal(t *testing.T) {
	e := setup(t)
	mdl, mv := e.model(t)
	f := &billedFake{Backend: fake.New(), usage: provider.Usage{InputTokens: 3000, OutputTokens: 400}}
	f.Script(provider.RoleModeler, "Raise the CI runners to 6.", answer(t, proposedAnswer(raiseRunners)))
	id := e.create(t, mdl, mv, "edit", "Raise the CI runners to 6.")
	var before time.Time // the database's UTC day (review round 1, L3)
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT (now() AT TIME ZONE 'UTC')::date`).Scan(&before)
	})
	p := e.until(t, e.meteredLoop(t, f, spend.Limits{Month: 1 << 50, Day: 1 << 49, OrganizationDay: 1 << 48}), id)
	if p.Status != "ready" || f.calls.Load() != 1 {
		t.Fatalf("%s after %d calls", p.Status, f.calls.Load())
	}
	price, _ := spend.PriceOf("claude-opus-5-5")
	cost := price.Cost(spend.Tokens{Input: 3000, Output: 400})
	if n := e.count(t, `SELECT count(*) FROM twin.ai_spend_reservations WHERE ai_proposal_id=$1 AND state='settled' AND outcome='usage'
		AND settled_nano_usd=$2 AND input_tokens=3000 AND output_tokens=400 AND credential_label='org-a'
		AND day >= $3 AND day <= (now() AT TIME ZONE 'UTC')::date`, id, cost, before); n != 1 {
		t.Errorf("%d settled reservations of the proposal on the database's day", n)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.ai_spend_days d JOIN twin.ai_spend_reservations r USING (organization_id, day)
		WHERE r.ai_proposal_id=$1 AND d.organization_id=$2 AND d.committed_nano_usd=$3`, id, e.org.ID, cost); n != 1 {
		t.Errorf("the organization's day does not hold the cost")
	}
	for _, action := range []string{"ai_spend.reserve", "ai_spend.settle"} {
		if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action=$2 AND actor_type='worker'`, id, action); n != 1 {
			t.Errorf("%d %s events", n, action)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='proposal.generate'
		AND (metadata->>'cost_nano_usd')::bigint=$2 AND metadata ? 'spend_reservation_id'`, id, cost); n != 1 {
		t.Errorf("%d proposal.generate events with the cost", n)
	}
}

// A spend limit refuses the call before it is made: the backend is never
// asked, the proposal fails for good with AI_QUOTA_EXCEEDED (shown as
// RATE_LIMITED), nothing is reserved, and the refusal is audited.
func TestASpendLimitRefusesTheProposal(t *testing.T) {
	e := setup(t)
	mdl, mv := e.model(t)
	f := &billedFake{Backend: fake.New(), usage: provider.Usage{InputTokens: 1, OutputTokens: 1}}
	id := e.create(t, mdl, mv, "edit", "Raise the CI runners to 6.")
	p := e.until(t, e.meteredLoop(t, f, spend.Limits{Month: 10, Day: 10, OrganizationDay: 10}), id)
	if p.Status != "failed" || p.Error == nil || p.Error.Error.Code != "RATE_LIMITED" || p.Error.Error.Details[0].Code != "AI_QUOTA_EXCEEDED" {
		t.Fatalf("%+v", p)
	}
	if f.calls.Load() != 0 {
		t.Errorf("the backend was asked %d times", f.calls.Load())
	}
	if n := e.count(t, `SELECT count(*) FROM twin.ai_spend_reservations WHERE ai_proposal_id=$1`, id); n != 0 {
		t.Errorf("%d reservations", n)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.jobs WHERE ai_proposal_id=$1 AND status='failed' AND attempt=1`, id); n != 1 {
		t.Error("the job was retried or did not fail")
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='ai_spend.refuse'
		AND metadata->>'limit'='organization_day'`, id); n != 1 {
		t.Errorf("%d refusal events", n)
	}
}
