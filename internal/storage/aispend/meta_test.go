package aispend_test

import (
	"context"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/ids"
)

// T-055: the Meta adapter's charges go to the same ledger as Claude's: the
// Muse Spark model, the SAM 3.1 model (priced per image) and the key's label
// meta pass migration 0016's checks, are recorded on the reservation and in
// the audit, and move the same totals. No provider is called.
func TestMetaChargesAreLedgered(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	at := time.Date(2201, 5, 1, 12, 0, 0, 0, time.UTC)
	m := e.meter(t, roomy, at)
	before := e.totals(t, e.a.ID, at)
	image, _ := spend.ImagePriceOf("sam-3.1")
	var total int64
	for _, c := range []struct {
		model    string
		reserved int64
		settled  provider.Settlement
	}{
		{"muse-spark-1.3", 500_000, provider.Settlement{NanoUSD: 280_000, Usage: provider.Usage{InputTokens: 120, OutputTokens: 30}, Outcome: provider.SpendUsage, Credential: "meta"}},
		{"sam-3.1", 3 * image, provider.Settlement{NanoUSD: image, Outcome: provider.SpendUsage, Credential: "meta"}},
	} {
		r, err := m.Reserve(ctx, provider.Charge{OrganizationID: e.a.ID, Role: provider.RoleModeler, Model: c.model, NanoUSD: c.reserved,
			Attribution: provider.Attribution{TargetType: "import", TargetID: e.impA, RequestID: ids.New("req")}})
		if err != nil {
			t.Fatalf("%s: %v", c.model, err)
		}
		if err := m.Settle(ctx, r, c.settled); err != nil {
			t.Fatalf("%s: %v", c.model, err)
		}
		total += c.settled.NanoUSD
		var model, label string
		var settled int64
		if err := e.admin.QueryRow(ctx, `SELECT model, credential_label, settled_nano_usd FROM twin.ai_spend_reservations WHERE id = $1`, r.ID).
			Scan(&model, &label, &settled); err != nil {
			t.Fatal(err)
		}
		if model != c.model || label != "meta" || settled != c.settled.NanoUSD {
			t.Errorf("%s: reservation %s %s %d", c.model, model, label, settled)
		}
	}
	if got := e.totals(t, e.a.ID, at); got.OrganizationDay != before.OrganizationDay+total || got.Month != before.Month+total {
		t.Errorf("totals %+v, before %+v, settled %d", got, before, total)
	}
	if e.events(t, e.a.ID, "ai_spend.reserve", e.impA) != 2 || e.events(t, e.a.ID, "ai_spend.settle", e.impA) != 2 {
		t.Error("the writes are not audited")
	}
}
