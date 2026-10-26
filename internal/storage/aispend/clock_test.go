package aispend_test

import (
	"context"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/storage/aispend"
	"github.com/kcl1104/geminus/internal/tenant"
)

// T-053 review round 1, L3: the database's clock picks a reservation's
// UTC day and month, inside the reservation's transaction, never the
// worker's: a worker whose clock is a day or a month off would otherwise
// reserve into a fresh bucket, and the deployment could spend up to twice
// a limit. The meter has no clock of its own to be handed (aispend.Config;
// the red run of this test, with the clock the meter had then set to
// 2299-12-31, is checks/evidence/2026-10-26-T-053-r1/red-l1-l3-ledger.log).
// Also under session time zones far from UTC either way, so that the day
// is the UTC day, not the session's: at any hour one of UTC+14 and UTC-12
// is on another date.
func TestTheDatabaseClockPicksTheDay(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for _, zone := range []string{"UTC", "Pacific/Kiritimati", "Etc/GMT+12"} {
		t.Run(zone, func(t *testing.T) {
			run := func(ctx context.Context, org string, fn func(ctx context.Context, tx tenant.Tx) error) error {
				return e.run(ctx, org, func(ctx context.Context, tx tenant.Tx) error {
					if _, err := tx.Exec(ctx, `SELECT set_config('TimeZone', $1, true)`, zone); err != nil {
						return err
					}
					return fn(ctx, tx)
				})
			}
			m, err := aispend.New(aispend.Config{Run: run, Actor: "worker-t053", Limits: roomy})
			if err != nil {
				t.Fatal(err)
			}
			before := e.databaseDay(t)
			r, err := m.Reserve(ctx, charge(e.a.ID, e.impA, 100))
			if err != nil {
				t.Fatal(err)
			}
			after := e.databaseDay(t)
			var day time.Time
			if err := e.admin.QueryRow(ctx, `SELECT day FROM twin.ai_spend_reservations WHERE id = $1`, r.ID).Scan(&day); err != nil {
				t.Fatal(err)
			}
			if !day.Equal(before) && !day.Equal(after) {
				t.Errorf("the reservation is in %s, the database's UTC day is %s", day.Format(time.DateOnly), before.Format(time.DateOnly))
			}
			// The settlement moves the reservation's own day and month.
			if err := m.Settle(ctx, r, provider.Settlement{NanoUSD: 40, Outcome: provider.SpendUsage, Usage: provider.Usage{InputTokens: 1}}); err != nil {
				t.Fatal(err)
			}
			var held int64
			if err := e.admin.QueryRow(ctx, `SELECT committed_nano_usd FROM twin.ai_spend_days WHERE organization_id = $1 AND day = $2`,
				e.a.ID, day).Scan(&held); err != nil || held == 0 {
				t.Errorf("the reservation's day holds %d (%v)", held, err)
			}
			// Nothing of the organization (new to this test) is on another day.
			var elsewhere int
			if err := e.admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM twin.ai_spend_reservations WHERE organization_id = $1 AND day NOT IN ($2, $3))
				+ (SELECT count(*) FROM twin.ai_spend_days WHERE organization_id = $1 AND day NOT IN ($2, $3))`, e.a.ID, before, after).Scan(&elsewhere); err != nil || elsewhere != 0 {
				t.Errorf("%d rows of the organization on another day (%v)", elsewhere, err)
			}
		})
	}
}

// databaseDay is the database's UTC day now.
func (e *env) databaseDay(t *testing.T) time.Time {
	t.Helper()
	var d time.Time
	if err := e.admin.QueryRow(context.Background(), `SELECT (now() AT TIME ZONE 'UTC')::date`).Scan(&d); err != nil {
		t.Fatal(err)
	}
	return d
}
