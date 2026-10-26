package provider

import (
	"context"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/spend"
)

// T-055: a model whose provider prices no inference geo (the Meta Model
// API: its Messages endpoint refuses inference_geo) is reserved for at its
// list price and settled at it, whatever geo an answer names: never the
// 1.1x Anthropic's US-only inference costs, and no inference_geo_unknown
// warning on every call. The geo rules of T-053 (review round 1, M2) are
// unchanged for Anthropic's model (TestTheReservationBoundsTheCall,
// TestSettlement).
func TestAListPricedModelIsReservedAndSettledAtItsListPrice(t *testing.T) {
	const model = "muse-spark-1.3"
	p, ok := spend.PriceOf(model)
	if !ok || spend.GeoPriced(model) {
		t.Fatalf("%s: priced %v, geo priced %v", model, ok, spend.GeoPriced(model))
	}
	usage := Usage{InputTokens: 120, OutputTokens: 30, CacheReadInputTokens: 7}
	for _, geo := range []string{"", spend.GeoUS, "eu", spend.GeoGlobal} {
		b := &billable{quote: Quote{Model: model, MaxInputTokens: 5000}, respond: func(context.Context, Request) (Response, error) {
			return Response{Text: []byte(validNeedsInput), Model: model, Credential: "meta", Usage: usage, InferenceGeo: geo}, nil
		}}
		m := &meter{}
		c := metered(t, b, m)
		res, err := ask(t, c, context.Background())
		if err != nil {
			t.Fatalf("geo %q: %v", geo, err)
		}
		budget := DefaultBudget()
		wantReserved := int64(1+budget.MaxRetries) * p.AttemptBound(5000, budget.MaxOutputTokens)
		if len(m.charges) != 1 || m.charges[0].NanoUSD != wantReserved || m.charges[0].Model != model {
			t.Errorf("geo %q: charges %+v, want one of %d (no geo multiplier)", geo, m.charges, wantReserved)
		}
		cost := p.Cost(spend.Tokens{Input: 120, Output: 30, CacheRead: 7})
		if len(m.settled) != 1 || m.settled[0].NanoUSD != cost || m.settled[0].Outcome != SpendUsage || m.settled[0].InferenceGeo != "" {
			t.Errorf("geo %q: settled %+v, want %d at the list price and no geo", geo, m.settled, cost)
		}
		if res.Metadata.CostNanoUSD != cost || res.Metadata.InferenceGeo != "" || res.Metadata.SpendOverReservation {
			t.Errorf("geo %q: metadata %+v", geo, res.Metadata)
		}
	}
	// An attempt without an answer counts at the attempt's list-price bound.
	b := &billable{quote: Quote{Model: model, MaxInputTokens: 5000}, respond: func(context.Context, Request) (Response, error) {
		return Response{Model: model, Credential: "meta", UnknownAttempts: 2}, NewError(CodeUnavailable, 0, "meta")
	}}
	m := &meter{}
	if _, err := ask(t, metered(t, b, m), context.Background()); CodeOf(err) != CodeUnavailable {
		t.Fatal(err)
	}
	if per := p.AttemptBound(5000, DefaultBudget().MaxOutputTokens); len(m.settled) != 1 || m.settled[0].NanoUSD != 2*per || m.settled[0].Outcome != SpendUnknown {
		t.Errorf("unknown attempts: %+v, want 2 x %d", m.settled, per)
	}
}
