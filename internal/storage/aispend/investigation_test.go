package aispend_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/storage/aispend"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/investigations"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/tenant"
)

// investigationEnv is setup's ledger with an investigation of a's owner in
// scenario mode, one proposal of it (what-if 1) and one person's proposal.
type investigationEnv struct {
	*env
	inv, aiProposal, personsProposal string
}

func withInvestigation(t *testing.T) investigationEnv {
	t.Helper()
	e := setup(t)
	ctx := context.Background()
	_, mdl, mv := dbtest.NewModel(t, e.admin, e.a)
	owner := e.a.Members[tenant.Owner].UserID
	api := tenant.New(dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 2, true))
	var stored string
	if err := e.admin.QueryRow(ctx, `SELECT ir FROM twin.model_versions WHERE id=$1`, mv).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var ir model.ModelIR
	if err := model.Decode([]byte(stored), &ir); err != nil {
		t.Fatal(err)
	}
	p, err := idpool.New(&ir)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	ie := investigationEnv{env: e, inv: ids.New("inv"), aiProposal: ids.New("aip"), personsProposal: ids.New("aip")}
	hash := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	intent := "Cut the lead time at ICT"
	if err := api.Run(ctx, tenant.Scope{OrganizationID: e.a.ID, UserID: owner}, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
		if _, err := investigations.Insert(ctx, tx, investigations.Investigation{ID: ie.inv, OrganizationID: e.a.ID, ModelID: mdl, BaseVersionID: mv,
			Mode: investigations.Scenario, Source: investigations.FromRequest, Intent: &intent, OutputLocale: "en", ProposalModel: "muse-spark-1.3",
			PlannerModel: "muse-spark-1.3", PromptTemplateVersion: "investigator-0.1.0",
			Objective:     []byte(`{"metric_path":"/metrics/lead_time_minutes/p50","better":"lower"}`),
			RunConfig:     []byte(`{"horizon":{"value":7,"unit":"day"},"warmup":{"value":0,"unit":"day"},"replications":5,"seed":1,"trace_level":"sampled"}`),
			RunConfigHash: &hash, BaselineName: &intent, Caps: investigations.CapsFor(investigations.Scenario, false), CreatedBy: owner}); err != nil {
			return err
		}
		if _, err := jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: e.a.ID, Type: jobs.Investigation,
			Resource: jobs.Resource{InvestigationID: ie.inv}, Payload: map[string]any{"investigation_id": ie.inv}, MaxAttempts: 5},
			jobs.DefaultPolicy); err != nil {
			return err
		}
		_, err := proposals.Insert(ctx, tx, proposals.Proposal{ID: ie.personsProposal, OrganizationID: e.a.ID, ModelID: mdl, BaseVersionID: mv,
			Mode: "scenario", Intent: intent, OutputLocale: "en", IDPool: pool, CreatedBy: owner})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := api.Run(ctx, tenant.Scope{OrganizationID: e.a.ID, UserID: owner, ActingFor: ie.inv}, tenant.ReadWrite,
		func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
			_, err := proposals.Insert(ctx, tx, proposals.Proposal{ID: ie.aiProposal, OrganizationID: e.a.ID, ModelID: mdl, BaseVersionID: mv,
				Mode: "scenario", Intent: intent, OutputLocale: "en", IDPool: pool, CreatedBy: owner})
			return err
		}); err != nil {
		t.Fatal(err)
	}
	return ie
}

func (ie investigationEnv) turn(n int64) provider.Charge {
	return provider.Charge{OrganizationID: ie.a.ID, Role: provider.Role("investigator"), Model: "muse-spark-1.3", NanoUSD: n,
		Attribution: provider.Attribution{TargetType: "investigation", TargetID: ie.inv, RequestID: ids.New("req")}}
}

func (ie investigationEnv) proposalCall(aip string, n int64) provider.Charge {
	return provider.Charge{OrganizationID: ie.a.ID, Role: provider.RoleScenario, Model: "muse-spark-1.3", NanoUSD: n,
		Attribution: provider.Attribution{TargetType: "ai_proposal", TargetID: aip, RequestID: ids.New("req")}}
}

func refusedFor(err error, limit string) bool {
	var pe *provider.Error
	return errors.As(err, &pe) && pe.Code == provider.CodeQuotaExceeded && pe.Limit == limit
}

func (ie investigationEnv) reservations(t *testing.T) (n int, held int64) {
	t.Helper()
	if err := ie.run(context.Background(), ie.a.ID, func(ctx context.Context, tx tenant.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*), coalesce(sum(CASE WHEN state='reserved' THEN reserved_nano_usd ELSE settled_nano_usd END), 0)
			FROM twin.ai_spend_reservations WHERE organization_id=$1 AND (investigation_id=$2 OR ai_proposal_id=$3)`,
			ie.a.ID, ie.inv, ie.aiProposal).Scan(&n, &held)
	}); err != nil {
		t.Fatal(err)
	}
	return n, held
}

// H-060, H-061 §12: an investigation's planner turns are reserved on the
// investigation, at most 20 of them; the 21st is refused before any
// provider call (quota_exceeded, limit investigation), writes no
// reservation and is recorded as refused.
func TestPlannerTurnsAreCapped(t *testing.T) {
	ie := withInvestigation(t)
	ctx := context.Background()
	m := ie.meter(t, roomy, time.Date(2203, 1, 5, 12, 0, 0, 0, time.UTC))
	var first provider.Reservation
	for i := range 20 {
		r, err := m.Reserve(ctx, ie.turn(1000))
		if err != nil {
			t.Fatalf("turn %d: %v", i+1, err)
		}
		if i == 0 {
			first = r
		}
	}
	if _, err := m.Reserve(ctx, ie.turn(1)); !refusedFor(err, provider.LimitInvestigation) {
		t.Fatalf("the 21st turn: %v", err)
	}
	if n, _ := ie.reservations(t); n != 20 {
		t.Errorf("%d reservations after the refused turn, want 20", n)
	}
	if got := ie.events(t, ie.a.ID, "ai_spend.refuse", ie.inv); got != 1 {
		t.Errorf("%d ai_spend.refuse events on the investigation, want 1", got)
	}
	var limit string
	if err := ie.admin.QueryRow(ctx, `SELECT metadata->>'limit' FROM twin.audit_events WHERE organization_id=$1 AND action='ai_spend.refuse' AND target_id=$2`,
		ie.a.ID, ie.inv).Scan(&limit); err != nil || limit != "investigation" {
		t.Errorf("the refusal's limit %q (%v)", limit, err)
	}
	// A turn's settlement names the investigation, and is never refused.
	if err := m.Settle(ctx, first, provider.Settlement{NanoUSD: 5000, Outcome: provider.SpendUsage}); err != nil {
		t.Fatalf("settling a turn above its reservation: %v", err)
	}
	if got := ie.events(t, ie.a.ID, "ai_spend.settle", ie.inv); got != 1 {
		t.Errorf("%d ai_spend.settle events on the investigation, want 1", got)
	}
}

// USD 2.50 per investigation: settled plus open, its planner turns' and its
// proposals' and analyses' calls together; a person's proposal is not held
// to it.
func TestTheInvestigationCeiling(t *testing.T) {
	ie := withInvestigation(t)
	ctx := context.Background()
	m := ie.meter(t, roomy, time.Date(2203, 1, 6, 12, 0, 0, 0, time.UTC))
	if _, err := m.Reserve(ctx, ie.turn(aispend.InvestigationCeiling+1)); !refusedFor(err, provider.LimitInvestigation) {
		t.Fatalf("one turn over the ceiling: %v", err)
	}
	turn, err := m.Reserve(ctx, ie.turn(2_000_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Reserve(ctx, ie.proposalCall(ie.aiProposal, 500_000_001)); !refusedFor(err, provider.LimitInvestigation) {
		t.Fatalf("the investigation's proposal past the ceiling: %v", err)
	}
	if _, err := m.Reserve(ctx, ie.proposalCall(ie.aiProposal, 500_000_000)); err != nil {
		t.Fatalf("the investigation's proposal up to the ceiling: %v", err)
	}
	if n, held := ie.reservations(t); n != 2 || held != aispend.InvestigationCeiling {
		t.Errorf("%d reservations holding %d, want 2 holding the ceiling", n, held)
	}
	if _, err := m.Reserve(ctx, ie.proposalCall(ie.personsProposal, 3_000_000_000)); err != nil {
		t.Errorf("a person's proposal: %v", err)
	}
	// Settling below the reservation frees room for the next call.
	if err := m.Settle(ctx, turn, provider.Settlement{NanoUSD: 1_000_000_000, Outcome: provider.SpendUsage}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Reserve(ctx, ie.turn(1_000_000_000)); err != nil {
		t.Errorf("a turn after the settlement freed room: %v", err)
	}
}

// The database refuses what the meter's own check would: a planner turn
// written past the cap by hand (the worker login) is refused by its
// trigger, so no other writer can pass the ceiling.
func TestTheCeilingIsTheDatabases(t *testing.T) {
	ie := withInvestigation(t)
	ctx := context.Background()
	day := time.Date(2203, 1, 7, 0, 0, 0, 0, time.UTC)
	err := ie.run(ctx, ie.a.ID, func(ctx context.Context, tx tenant.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO twin.ai_spend_days(organization_id,day) VALUES ($1,$2) ON CONFLICT DO NOTHING`, ie.a.ID, day); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO twin.ai_spend_reservations(id,organization_id,day,role,model,investigation_id,request_id,reserved_nano_usd)
			VALUES ($1,$2,$3,'investigator','muse-spark-1.3',$4,$5,$6)`, ids.New("asr"), ie.a.ID, day, ie.inv, ids.New("req"),
			aispend.InvestigationCeiling+1)
		return err
	})
	if err == nil {
		t.Fatal("the database stored a reservation past the investigation's ceiling")
	}
	var refused bool
	_ = ie.worker.Run(ctx, ie.a.ID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM twin.ai_spend_reservations WHERE investigation_id=$1)`, ie.inv).Scan(&refused)
	})
	if !refused {
		t.Error("a refused reservation was kept")
	}
}
