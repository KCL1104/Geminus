package aispend_test

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/observability/telemetry"
)

// metrics installs a telemetry whose metrics t can read, for the test.
func metrics(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	tel, err := telemetry.New(tracenoop.NewTracerProvider(), sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	if err != nil {
		t.Fatal(err)
	}
	prev := telemetry.Install(tel)
	t.Cleanup(func() { telemetry.Install(prev) })
	return reader
}

// warnings is geminus.ai.spend.warnings by warning.
func warnings(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "geminus.ai.spend.warnings" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				w, _ := dp.Attributes.Value("geminus.ai.warning")
				out[w.AsString()] += dp.Value
			}
		}
	}
	return out
}

// T-053 review round 1, L1 (the reviewer's probe P2): a settlement above
// its reservation means the bound's assumption broke (the quote was too
// low, or more attempts may have been billed than were reserved for). What
// was billed is recorded all the same, even past a limit (the ledger never
// under-records), and the case is flagged: the settle event says
// over_reservation, and geminus.ai.spend.warnings counts it. The next
// reservation is refused by the limit it passed. M2: the settle event
// names the geo the answer was priced at, and an unknown one is counted.
func TestASettlementAboveItsReservationIsRecordedAndFlagged(t *testing.T) {
	e := setup(t)
	reader := metrics(t)
	ctx := context.Background()
	at := time.Date(2208, 8, 8, 8, 0, 0, 0, time.UTC)
	base := e.totals(t, e.a.ID, at)
	l := spend.Limits{Month: base.Month + 1_000_000, Day: base.Day + 1_000_000, OrganizationDay: 1000}
	m := e.meter(t, l, at)
	r, err := m.Reserve(ctx, charge(e.a.ID, e.impA, 600))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Settle(ctx, r, provider.Settlement{NanoUSD: 5000, Outcome: provider.SpendUsage, Usage: provider.Usage{InputTokens: 1},
		InferenceGeo: spend.GeoGlobal}); err != nil {
		t.Fatalf("a settlement above its reservation was refused: %v", err)
	}
	if got := e.totals(t, e.a.ID, at); got.OrganizationDay != 5000 || got.Day != base.Day+5000 || got.Month != base.Month+5000 {
		t.Errorf("totals %+v: not what was billed", got)
	}
	var flagged, plain int
	if err := e.admin.QueryRow(ctx, `SELECT count(*) FILTER (WHERE metadata->'over_reservation' = 'true' AND metadata->>'inference_geo' = 'global'),
		count(*) FILTER (WHERE NOT metadata ? 'over_reservation') FROM twin.audit_events
		WHERE organization_id = $1 AND action = 'ai_spend.settle' AND metadata->>'reservation_id' = $2`, e.a.ID, r.ID).Scan(&flagged, &plain); err != nil {
		t.Fatal(err)
	}
	if flagged != 1 || plain != 0 {
		t.Errorf("settle events: %d flagged over_reservation at global, %d not", flagged, plain)
	}
	if w := warnings(t, reader); w[telemetry.SpendOverReservation] != 1 || w[telemetry.SpendGeoUnknown] != 0 {
		t.Errorf("warnings %v, want one over the reservation", w)
	}
	if _, err := m.Reserve(ctx, charge(e.a.ID, e.impA, 1)); provider.CodeOf(err) != provider.CodeQuotaExceeded {
		t.Errorf("the next reservation: %v", err)
	}
	// A settlement within its reservation is not flagged; one at an
	// unknown geo is counted as such.
	r2, err := e.meter(t, roomy, at.Add(48*time.Hour)).Reserve(ctx, charge(e.a.ID, e.impA, 600))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Settle(ctx, r2, provider.Settlement{NanoUSD: 600, Outcome: provider.SpendUsage, Usage: provider.Usage{InputTokens: 1},
		InferenceGeo: spend.GeoUnknown}); err != nil {
		t.Fatal(err)
	}
	if err := e.admin.QueryRow(ctx, `SELECT count(*) FROM twin.audit_events WHERE organization_id = $1 AND action = 'ai_spend.settle'
		AND metadata->>'reservation_id' = $2 AND NOT metadata ? 'over_reservation' AND metadata->>'inference_geo' = 'unknown'`, e.a.ID, r2.ID).Scan(&plain); err != nil || plain != 1 {
		t.Errorf("a settlement at its reservation, unknown geo: %d plain events, %v", plain, err)
	}
	if w := warnings(t, reader); w[telemetry.SpendOverReservation] != 1 || w[telemetry.SpendGeoUnknown] != 1 {
		t.Errorf("warnings %v, want one of each", w)
	}
	// A geo this build does not price is never written as it is.
	r3, err := m.Reserve(ctx, charge(e.b.ID, e.impB, 10))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Settle(ctx, r3, provider.Settlement{NanoUSD: 1, Outcome: provider.SpendUsage, Usage: provider.Usage{InputTokens: 1},
		InferenceGeo: "eu-west"}); err == nil {
		t.Error("a settlement named a geo this build does not price")
	}
}
