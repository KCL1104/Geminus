package aispend_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/storage/aispend"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
)

// superRun runs fn in a transaction of the provisioning superuser, whom
// RLS does not restrict, scoped as the worker scope's transactions are
// (app.organization_id is set: the audit append and the deployment
// totals' policy read it). Only the ledger's own organization filter can
// keep one organization's rows out of another's statements there.
func (e *env) superRun(ctx context.Context, org string, fn func(ctx context.Context, tx tenant.Tx) error) error {
	return postgres.OpenPool(e.admin).Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id', $1, true)`, org); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}

// T-053 review round 1, M1: the ledger's organization filter holds by
// itself (AGENTS.md §2: an application filter next to RLS), as the other
// storage packages test theirs (TestOrganizationFilter in projects,
// proposals, models...): the meter run as the superuser, whom RLS does not
// restrict (the reviewer's probe P1). Settle finds a reservation in its
// organization only (mutant D9: the SELECT … FOR UPDATE without
// organization_id); Reserve moves the organization's own day only (F1:
// the organization day's UPDATE without it); TotalsAt reads the
// organization's own day (F2). F3 (the reservation's UPDATE without
// organization_id) is equivalent: the filtered SELECT … FOR UPDATE of the
// same transaction comes first.
func TestOrganizationFilter(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	at := time.Date(2207, 7, 7, 7, 0, 0, 0, time.UTC)
	worker := e.meter(t, roomy, at)
	super, err := aispend.New(aispend.Config{Run: e.superRun, Actor: "worker-t053", Limits: roomy})
	if err != nil {
		t.Fatal(err)
	}
	aispend.PinDay(super, at)
	// Both organizations hold a reservation, and a day row, on the day.
	// The amounts are this run's own: earlier runs' organizations hold rows
	// on the same day, and a statement without the filter must not find one
	// that happens to hold the same amount (mutation run 1: F4 survived on
	// fixed amounts).
	nA, nB := 1000+rand.Int64N(1_000_000_000), 100+rand.Int64N(1_000_000)
	rA, err := worker.Reserve(ctx, charge(e.a.ID, e.impA, nA))
	if err != nil {
		t.Fatal(err)
	}
	rB, err := worker.Reserve(ctx, charge(e.b.ID, e.impB, nB))
	if err != nil {
		t.Fatal(err)
	}
	beforeA, beforeB := e.totals(t, e.a.ID, at), e.totals(t, e.b.ID, at)
	state := func(id string) string {
		t.Helper()
		var s string
		if err := e.admin.QueryRow(ctx, `SELECT state FROM twin.ai_spend_reservations WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	// Settle: A's reservation presented as B's is not found; nothing moves.
	stolen := rA
	stolen.Charge.OrganizationID = e.b.ID
	if err := super.Settle(ctx, stolen, provider.Settlement{Outcome: provider.SpendNone}); !errors.Is(err, aispend.ErrNotFound) {
		t.Errorf("another organization's settlement of A's reservation: %v, want ErrNotFound", err)
	}
	if a, b := e.totals(t, e.a.ID, at), e.totals(t, e.b.ID, at); a != beforeA || b != beforeB || state(rA.ID) != "reserved" {
		t.Errorf("a settlement in B moved A: A %+v (was %+v) B %+v (was %+v), A's reservation %s", a, beforeA, b, beforeB, state(rA.ID))
	}

	// Reserve: a charge for A moves A's day by the charge, B's not at all,
	// and its event names A's day (not another organization's).
	r7, err := super.Reserve(ctx, charge(e.a.ID, e.impA, 7))
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if a, b := e.totals(t, e.a.ID, at), e.totals(t, e.b.ID, at); a.OrganizationDay != beforeA.OrganizationDay+7 || b.OrganizationDay != beforeB.OrganizationDay {
		t.Errorf("reserve for A: A %+v (was %+v) B %+v (was %+v)", a, beforeA, b, beforeB)
	}
	var held int64
	if err := e.admin.QueryRow(ctx, `SELECT (metadata->>'organization_day_committed_nano_usd')::bigint FROM twin.audit_events
		WHERE organization_id = $1 AND action = 'ai_spend.reserve' AND metadata->>'reservation_id' = $2`, e.a.ID, r7.ID).Scan(&held); err != nil ||
		held != beforeA.OrganizationDay+7 {
		t.Errorf("the reserve event names an organization day of %d (%v), want A's %d", held, err, beforeA.OrganizationDay+7)
	}

	// TotalsAt: each organization's own day.
	for org, want := range map[string]int64{e.a.ID: beforeA.OrganizationDay + 7, e.b.ID: beforeB.OrganizationDay} {
		var got aispend.Totals
		if err := e.superRun(ctx, org, func(ctx context.Context, tx tenant.Tx) error {
			var err error
			got, err = aispend.TotalsAt(ctx, tx, org, at)
			return err
		}); err != nil || got.OrganizationDay != want {
			t.Errorf("TotalsAt of %s: %+v %v, want its day %d", org, got, err, want)
		}
	}

	// Settle in its own organization moves that organization only.
	if err := super.Settle(ctx, rB, provider.Settlement{NanoUSD: 4, Outcome: provider.SpendUsage, Usage: provider.Usage{InputTokens: 1}}); err != nil {
		t.Fatalf("B's own settlement: %v", err)
	}
	if a, b := e.totals(t, e.a.ID, at), e.totals(t, e.b.ID, at); a.OrganizationDay != beforeA.OrganizationDay+7 ||
		b.OrganizationDay != beforeB.OrganizationDay-(nB-4) || state(rB.ID) != "settled" || state(rA.ID) != "reserved" {
		t.Errorf("B's settlement: A %+v B %+v (was %+v), states %s %s", a, b, beforeB, state(rA.ID), state(rB.ID))
	}
}
