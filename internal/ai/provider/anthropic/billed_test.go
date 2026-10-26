package anthropic_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/anthropic"
	"github.com/kcl1104/geminus/internal/ai/spend"
)

// T-053 review round 1 (H-003): what a call can be billed for. Local test
// server only.

// M3: a call is billed for at most 1 + MaxRetries attempts, the
// reservation's bound, only if it never fails over after an attempt that
// may have been billed. Once an attempt on a credential ended without an
// answer (a lost connection, a cut body, a timeout), a 401, 402 or 403 that
// follows marks the credential as before, but the call does not move to
// the next one: it fails (unavailable, retryable), and the next call, with
// a reservation of its own, uses the next credential. A credential refused
// on every attempt with a definite status (429, 5xx, then 402) was billed
// for nothing, so the call still fails over.
func TestNoFailoverAfterAnAttemptThatMayHaveBeenBilled(t *testing.T) {
	lost := reply{hangUp: true}
	cut := reply{status: 200, body: ok(answerJ).body, cutBody: true}
	for _, c := range []struct {
		name string
		a, b []reply
		p4   bool
	}{
		// The reviewer's probe P4: A loses two connections, then answers
		// 402; B would lose three more.
		{"P4: 402 after two lost connections", []reply{lost, lost, apiError(402, "billing_error")}, []reply{lost}, true},
		{"401 after a lost connection", []reply{lost, apiError(401, "authentication_error")}, []reply{ok(answerJ)}, false},
		{"403 after a cut body", []reply{cut, apiError(403, "permission_error")}, []reply{ok(answerJ)}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newServer(t, map[string][]reply{keyA: c.a, keyB: c.b})
			cl, b, m := meteredClient(t, s, config(keyA, keyB), 10*spendUSD, func(bu *provider.Budget) { bu.MaxRetries = 2 })
			res, err := ask(t, cl)
			per := perAttempt(t, b, provider.DefaultBudget().MaxOutputTokens)
			unanswered := int64(len(c.a) - 1)
			var pe *provider.Error
			if !errors.As(err, &pe) || pe.Code != provider.CodeUnavailable || !pe.Retryable() || pe.Credential != "org-a" {
				t.Errorf("error %v, want a retryable unavailable on org-a", err)
			}
			if s.count(keyA) != len(c.a) || s.count(keyB) != 0 || s.count(keyA)+s.count(keyB) > 1+2 {
				t.Errorf("attempts A=%d B=%d: failed over after an attempt that may have been billed", s.count(keyA), s.count(keyB))
			}
			if want := unanswered * per; m.Spent() != want || m.Committed() != want || res.Metadata.CostNanoUSD != want {
				t.Errorf("spent %d committed %d metadata %d, want %d unanswered attempts at %d", m.Spent(), m.Committed(),
					res.Metadata.CostNanoUSD, unanswered, per)
			}
			// The credential is still marked: the next call goes to B.
			res, err = ask(t, cl)
			if c.p4 {
				// B loses all three attempts: no answer, each counted.
				if provider.CodeOf(err) != provider.CodeUnavailable || s.count(keyB) != 3 || s.count(keyA) != len(c.a) {
					t.Errorf("next call: %v A=%d B=%d", err, s.count(keyA), s.count(keyB))
				}
				return
			}
			if err != nil || res.Metadata.Credential != "org-b" || s.count(keyB) != 1 || s.count(keyA) != len(c.a) {
				t.Errorf("next call: %v %+v A=%d B=%d", err, res.Metadata, s.count(keyA), s.count(keyB))
			}
		})
	}
	// Definite refusals on every attempt of A: nothing billed, failover.
	s := newServer(t, map[string][]reply{keyA: {apiError(429, "rate_limit_error"), apiError(529, "overloaded_error"), apiError(402, "billing_error")},
		keyB: {ok(answerJ)}})
	cl, _, m := meteredClient(t, s, config(keyA, keyB), 10*spendUSD, func(bu *provider.Budget) { bu.MaxRetries = 2 })
	res, err := ask(t, cl)
	if err != nil || res.Metadata.Credential != "org-b" || s.count(keyA) != 3 || s.count(keyB) != 1 {
		t.Fatalf("definite refusals: %v %+v A=%d B=%d", err, res.Metadata, s.count(keyA), s.count(keyB))
	}
	if cost := price(t).Cost(spend.Tokens{Input: 120, Output: 30}); m.Spent() != cost {
		t.Errorf("spent %d, want B's answer %d", m.Spent(), cost)
	}
}

// I1: a 200 answer read to its end that cannot be decoded may have been
// billed, and its usage is unknown: it settles at the whole reservation
// (the reviewer's probe P5), never at nothing, and it does not fail over.
func TestAnUndecodableAnswerSettlesAtTheReservation(t *testing.T) {
	garbled := reply{status: 200, header: map[string]string{"request-id": "req_test_1"}, body: `{"id":"msg_1","type":"message","usage":`}
	for _, retries := range []int{0, 2} {
		s := newServer(t, map[string][]reply{keyA: {garbled}, keyB: {ok(answerJ)}})
		cl, b, m := meteredClient(t, s, config(keyA, keyB), 10*spendUSD, func(bu *provider.Budget) { bu.MaxRetries = retries })
		res, err := ask(t, cl)
		reserved := int64(1+retries) * perAttempt(t, b, provider.DefaultBudget().MaxOutputTokens)
		if provider.CodeOf(err) != provider.CodeUnavailable || s.count(keyA) != 1 || s.count(keyB) != 0 {
			t.Errorf("retries %d: %v A=%d B=%d", retries, err, s.count(keyA), s.count(keyB))
		}
		if m.Spent() != reserved || res.Metadata.CostNanoUSD != reserved {
			t.Errorf("retries %d: spent %d metadata %d, want the reservation %d", retries, m.Spent(), res.Metadata.CostNanoUSD, reserved)
		}
	}
}

// I1: a body cut on the last attempt is one unanswered attempt, counted
// once (the transport counted it), not also an answer that cannot be read:
// two cut attempts settle at two attempt bounds, the whole reservation.
func TestACutBodyOnTheLastAttemptCountsOnce(t *testing.T) {
	cut := reply{status: 200, body: ok(answerJ).body, cutBody: true}
	s := newServer(t, map[string][]reply{keyA: {cut, cut}, keyB: {ok(answerJ)}})
	cl, b, m := meteredClient(t, s, config(keyA, keyB), 10*spendUSD, func(bu *provider.Budget) { bu.MaxRetries = 1 })
	res, err := ask(t, cl)
	per := perAttempt(t, b, provider.DefaultBudget().MaxOutputTokens)
	if provider.CodeOf(err) != provider.CodeUnavailable || s.count(keyA) != 2 || s.count(keyB) != 0 {
		t.Errorf("%v A=%d B=%d", err, s.count(keyA), s.count(keyB))
	}
	if m.Spent() != 2*per || res.Metadata.CostNanoUSD != 2*per || res.Metadata.SpendOverReservation {
		t.Errorf("spent %d metadata %+v, want two attempt bounds %d", m.Spent(), res.Metadata, 2*per)
	}
}

// geoAnswer is ok(answerJ) with usage.inference_geo set to geo, or without
// the member when geo is "-".
func geoAnswer(geo string) reply {
	r := ok(answerJ)
	member := `"inference_geo":"` + geo + `",`
	if geo == "-" {
		member = ""
	}
	r.body = strings.Replace(r.body, `"inference_geo":"global",`, member, 1)
	return r
}

// ceilTenth is n·1.1 rounded up: the price of US-only inference, the
// dearest inference geo (data residency page, Claude 4.6 and later).
func ceilTenth(n int64) int64 { return (n*11 + 9) / 10 }

// M2: the request names no inference geo. Where inference runs (global or
// US only) is a data residency choice the owner makes for the provider
// organizations' workspaces (default_inference_geo, allowed_inference_geos;
// D-116, approved option A, H-031), not something the adapter decides: pinning "global"
// would fail in a workspace that allows "us" only, pinning "us" would cost
// 1.1x everywhere. So the reservation assumes the dearest geo the
// workspace's default can apply, and each answer is priced at the geo its
// usage.inference_geo reports.
func TestTheRequestNamesNoInferenceGeo(t *testing.T) {
	s := newServer(t, map[string][]reply{keyA: {geoAnswer("global")}})
	c, _ := client(t, s, config(keyA))
	if _, err := ask(t, c); err != nil {
		t.Fatal(err)
	}
	if geo, present := s.bodies[0]["inference_geo"]; present {
		t.Errorf("the request names inference_geo %v; the workspace's default decides (the owner's choice, D-116)", geo)
	}
}

// M2: an answer is priced at the geo it says it ran in: global at the list
// price, us at 1.1x; one that names no geo, or one this build does not
// know, at the dearest (1.1x). The reservation holds every attempt at 1.1x.
func TestTheAnswerIsPricedAtItsInferenceGeo(t *testing.T) {
	cost := price(t).Cost(spend.Tokens{Input: 120, Output: 30})
	for geo, want := range map[string]int64{"global": cost, "us": ceilTenth(cost), "-": ceilTenth(cost), "": ceilTenth(cost),
		"eu": ceilTenth(cost), "GLOBAL": ceilTenth(cost)} {
		t.Run(geo, func(t *testing.T) {
			s := newServer(t, map[string][]reply{keyA: {geoAnswer(geo)}})
			c, _, m := meteredClient(t, s, config(keyA), 10*spendUSD)
			res, err := ask(t, c)
			if err != nil {
				t.Fatal(err)
			}
			if m.Spent() != want || res.Metadata.CostNanoUSD != want {
				t.Errorf("geo %q: spent %d metadata %d, want %d", geo, m.Spent(), res.Metadata.CostNanoUSD, want)
			}
		})
	}
	// The reservation: every attempt at the dearest geo.
	s := newServer(t, map[string][]reply{keyA: {apiError(400, "invalid_request_error")}})
	b, err := anthropic.NewForTest(config(keyA), s.srv.URL+"/", s.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	q, err := b.Quote(context.Background(), request(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	budget := provider.DefaultBudget()
	charges := &chargeLog{}
	c, err := provider.NewMetered(b, budget, charges)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ask(t, c)
	want := int64(1+budget.MaxRetries) * ceilTenth(price(t).AttemptBound(q.MaxInputTokens, budget.MaxOutputTokens))
	if len(charges.charges) != 1 || charges.charges[0] != want {
		t.Errorf("reserved %v, want %d (every attempt at 1.1x)", charges.charges, want)
	}
}

// chargeLog is a meter that grants every charge and records its amount.
type chargeLog struct{ charges []int64 }

func (l *chargeLog) Reserve(_ context.Context, c provider.Charge) (provider.Reservation, error) {
	l.charges = append(l.charges, c.NanoUSD)
	return provider.Reservation{ID: "r1", Charge: c}, nil
}

func (l *chargeLog) Settle(context.Context, provider.Reservation, provider.Settlement) error {
	return nil
}
