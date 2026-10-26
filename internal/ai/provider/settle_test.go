package provider

import (
	"context"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/spend"
)

// T-053 review round 1 (H-003): the reservation and the settlement of a
// metered call.

// ceilTenth is n·1.1 rounded up: the price of US-only inference, the
// dearest inference geo (M2).
func ceilTenth(n int64) int64 { return (n*11 + 9) / 10 }

// M2: the request names no inference geo (the workspace's default decides,
// D-116, approved option A, H-031), so the reservation holds every attempt at the dearest
// geo the default can apply: 1.1x the list price.
func TestTheReservationAssumesTheDearestGeo(t *testing.T) {
	b := &billable{quote: Quote{Model: pricedModel, MaxInputTokens: 50_000}, respond: billed(validProposal, Usage{InputTokens: 1, OutputTokens: 1})}
	m := &meter{}
	if _, err := ask(t, metered(t, b, m, func(bu *Budget) { bu.MaxRetries = 2; bu.MaxOutputTokens = 8192 }), context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := 3 * ceilTenth(price(t).AttemptBound(50_000, 8192)); len(m.charges) != 1 || m.charges[0].NanoUSD != want {
		t.Errorf("charges %+v, want %d", m.charges, want)
	}
}

// M3, L1: what was billed is recorded, even when it is more than the
// reservation (the reservation's bound did not hold: an unexpected
// number of unanswered attempts, or an answer above the quote). The
// settlement is never clamped to the reservation.
func TestASettlementAboveTheReservationIsRecorded(t *testing.T) {
	per := ceilTenth(price(t).AttemptBound(1000, 100))
	big := Usage{InputTokens: 900_000, OutputTokens: 100}
	for name, c := range map[string]struct {
		resp Response
		want int64
		over bool
	}{
		"more lost attempts than reserved for": {Response{Credential: "org-a", UnknownAttempts: 9}, 9 * per, true},
		"an answer above the quote": {Response{Text: []byte(validProposal), Credential: "org-a", InferenceGeo: spend.GeoGlobal, Usage: big},
			price(t).Cost(spend.Tokens{Input: big.InputTokens, Output: big.OutputTokens}), true},
		"exactly the reservation": {Response{Credential: "org-a", UnknownAttempts: 3}, 3 * per, false},
		"within it":               {Response{Credential: "org-a", UnknownAttempts: 1}, per, false},
	} {
		t.Run(name, func(t *testing.T) {
			b := &billable{quote: Quote{Model: pricedModel, MaxInputTokens: 1000}, respond: func(context.Context, Request) (Response, error) {
				return c.resp, nil
			}}
			m := &meter{}
			res, _ := ask(t, metered(t, b, m, func(bu *Budget) { bu.MaxOutputTokens = 100; bu.MaxRetries = 2 }), context.Background())
			if len(m.settled) != 1 || m.settled[0].NanoUSD != c.want || res.Metadata.CostNanoUSD != c.want {
				t.Fatalf("settled %+v metadata %d, want %d (reserved %d)", m.settled, res.Metadata.CostNanoUSD, c.want, 3*per)
			}
			if res.Metadata.SpendOverReservation != c.over {
				t.Errorf("over the reservation: %v, want %v", res.Metadata.SpendOverReservation, c.over)
			}
		})
	}
}

// M2: the metadata names the geo an answer was priced at, as this build
// prices it (global, us, unknown): never the provider's own text.
func TestTheMetadataNamesThePricedGeo(t *testing.T) {
	for reported, want := range map[string]string{"global": spend.GeoGlobal, "us": spend.GeoUS, "": spend.GeoUnknown, "<script>": spend.GeoUnknown} {
		b := &billable{quote: Quote{Model: pricedModel, MaxInputTokens: 10}, respond: func(context.Context, Request) (Response, error) {
			return Response{Text: []byte(validProposal), Credential: "org-a", Usage: Usage{InputTokens: 1}, InferenceGeo: reported}, nil
		}}
		res, err := ask(t, metered(t, b, &meter{}), context.Background())
		if err != nil || res.Metadata.InferenceGeo != want {
			t.Errorf("reported %q: metadata %q (%v), want %q", reported, res.Metadata.InferenceGeo, err, want)
		}
	}
	// No answer, no geo.
	b := &billable{quote: Quote{Model: pricedModel, MaxInputTokens: 10}, respond: func(context.Context, Request) (Response, error) {
		return Response{Credential: "org-a", UnknownAttempts: 1, InferenceGeo: "us"}, NewError(CodeUnavailable, 0, "org-a")
	}}
	if res, _ := ask(t, metered(t, b, &meter{}), context.Background()); res.Metadata.InferenceGeo != "" {
		t.Errorf("no answer: metadata geo %q", res.Metadata.InferenceGeo)
	}
}
