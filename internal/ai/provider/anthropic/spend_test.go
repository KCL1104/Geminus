package anthropic_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/anthropic"
	"github.com/kcl1104/geminus/internal/ai/spend"
)

// T-053: the live backend costs money, so it is billable: only a metered
// client calls it, it refuses a request without a reservation and a role
// its approval does not cover, and it reports every attempt the provider
// may have billed without answering. Local test server only.

var _ provider.Billable = (*anthropic.Backend)(nil)

func price(t *testing.T) spend.Price {
	p, ok := spend.PriceOf(model)
	if !ok {
		t.Fatal("no price")
	}
	return p
}

func request(t *testing.T, reservation string) provider.Request {
	return provider.Request{Prompt: prompt(t), OutputSchema: "proposal-output.schema.json", MaxOutputTokens: 1000,
		AttemptTimeout: time.Second, MaxRetries: 0, Reservation: reservation}
}

func TestOnlyAMeteredClientCallsIt(t *testing.T) {
	s := newServer(t, map[string][]reply{keyA: {ok(answerJ)}})
	b, err := anthropic.NewForTest(config(keyA), s.srv.URL+"/", s.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.New(b, provider.DefaultBudget()); err == nil {
		t.Fatal("an unmetered client took the live backend")
	}
	// A request without a reservation never leaves the process.
	if _, err := b.Complete(context.Background(), request(t, "")); provider.CodeOf(err) != provider.CodeInvalidRequest || s.count(keyA) != 0 {
		t.Errorf("unreserved: %v hits %d", err, s.count(keyA))
	}
	if _, err := b.Complete(context.Background(), request(t, "r1")); err != nil || s.count(keyA) != 1 {
		t.Errorf("reserved: %v hits %d", err, s.count(keyA))
	}
}

// The quote is the model and the most input tokens one attempt can be
// billed for: the prompt's bytes and the projected schema's (every token
// covers at least one byte) and a fixed allowance for the API's own
// framing and structured-output instructions.
func TestQuote(t *testing.T) {
	s := newServer(t, nil)
	_, b := client(t, s, config(keyA))
	req := request(t, "")
	q, err := b.Quote(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := anthropic.Project(req.OutputSchema)
	if err != nil {
		t.Fatal(err)
	}
	schema, _ := json.Marshal(proj.Schema)
	want := int64(len(req.Prompt.System)+len(req.Prompt.User())+len(schema)) + anthropic.QuoteOverheadTokens
	if q.Model != model || q.MaxInputTokens != want {
		t.Errorf("quote %+v, want %d", q, want)
	}
	req.OutputSchema = "no-such.schema.json"
	if _, err := b.Quote(context.Background(), req); err == nil {
		t.Error("an unknown schema was quoted")
	}
}

// Live AI serves the roles its approval's data policy names (PROPOSED
// D-112): another role's request never leaves the process, and its
// reservation is released.
func TestOnlyTheApprovedRoles(t *testing.T) {
	s := newServer(t, map[string][]reply{keyA: {ok(answerJ)}})
	c, b, m := meteredClient(t, s, config(keyA), 10*spendUSD)
	anthropic.SetRoles(b, "scenario")
	if _, err := ask(t, c); provider.CodeOf(err) != provider.CodePolicyNotApproved || s.count(keyA) != 0 {
		t.Errorf("modeler: %v hits %d", err, s.count(keyA))
	}
	if m.Committed() != 0 || m.Spent() != 0 {
		t.Errorf("committed %d spent %d", m.Committed(), m.Spent())
	}
	anthropic.SetRoles(b, "modeler", "scenario")
	if _, err := ask(t, c); err != nil || s.count(keyA) != 1 {
		t.Errorf("approved role: %v hits %d", err, s.count(keyA))
	}
}

// What a call cost is its usage at the model's price; a call the limit
// cannot hold is never sent; an answer with an error status costs nothing.
func TestSpend(t *testing.T) {
	s := newServer(t, map[string][]reply{keyA: {ok(answerJ)}})
	c, _, m := meteredClient(t, s, config(keyA), 10*spendUSD)
	res, err := ask(t, c)
	if err != nil {
		t.Fatal(err)
	}
	cost := price(t).Cost(spend.Tokens{Input: 120, Output: 30})
	if m.Spent() != cost || m.Committed() != cost || res.Metadata.CostNanoUSD != cost || res.Metadata.SpendReservation == "" {
		t.Errorf("spent %d committed %d metadata %+v, want %d", m.Spent(), m.Committed(), res.Metadata, cost)
	}

	s = newServer(t, map[string][]reply{keyA: {ok(answerJ)}})
	c, _, m = meteredClient(t, s, config(keyA), 1)
	if _, err := ask(t, c); provider.CodeOf(err) != provider.CodeQuotaExceeded || s.count(keyA) != 0 || m.Committed() != 0 {
		t.Errorf("over the limit: %v hits %d", err, s.count(keyA))
	}

	s = newServer(t, map[string][]reply{keyA: {apiError(429, "rate_limit_error")}, keyB: {apiError(402, "billing_error")}})
	c, _, m = meteredClient(t, s, config(keyA, keyB), 10*spendUSD)
	if _, err := ask(t, c); provider.CodeOf(err) != provider.CodeRateLimited || m.Spent() != 0 || m.Committed() != 0 {
		t.Errorf("429: %v spent %d", err, m.Spent())
	}
	s = newServer(t, map[string][]reply{keyA: {apiError(402, "billing_error")}, keyB: {apiError(401, "authentication_error")}})
	c, _, m = meteredClient(t, s, config(keyA, keyB), 10*spendUSD)
	if _, err := ask(t, c); provider.CodeOf(err) != provider.CodeCredentialsExhausted || m.Spent() != 0 || m.Committed() != 0 {
		t.Errorf("both refused: %v spent %d", err, m.Spent())
	}
	// A refused or truncated answer was billed.
	s = newServer(t, map[string][]reply{keyA: {message("refusal", `[]`)}})
	c, _, m = meteredClient(t, s, config(keyA), 10*spendUSD)
	if _, err := ask(t, c); provider.CodeOf(err) != provider.CodeRefused || m.Spent() != price(t).Cost(spend.Tokens{Input: 1, Output: 1}) {
		t.Errorf("refusal: %v spent %d", err, m.Spent())
	}
}

// perAttempt is the bound of one attempt of the test prompt under budget,
// at the dearest inference geo (the request names none; review round 1,
// M2).
func perAttempt(t *testing.T, b *anthropic.Backend, maxOutput int64) int64 {
	req := request(t, "")
	q, err := b.Quote(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return spend.AtDearestGeo(price(t).AttemptBound(q.MaxInputTokens, maxOutput))
}

// An attempt that ended without an answer may have been billed: a lost
// connection, a cut body, a timeout each count at the attempt's bound, on
// top of the answer's usage, up to the reservation.
func TestAttemptsWithoutAnAnswerCount(t *testing.T) {
	cost := price(t).Cost(spend.Tokens{Input: 120, Output: 30})
	for name, first := range map[string]reply{"lost connection": {hangUp: true}, "cut body": {status: 200, body: ok(answerJ).body, cutBody: true}} {
		t.Run(name, func(t *testing.T) {
			s := newServer(t, map[string][]reply{keyA: {first, ok(answerJ)}})
			c, b, m := meteredClient(t, s, config(keyA), 10*spendUSD, func(bu *provider.Budget) { bu.MaxRetries = 2 })
			res, err := ask(t, c)
			if err != nil {
				t.Fatalf("%v (hits %d)", err, s.count(keyA))
			}
			per := perAttempt(t, b, provider.DefaultBudget().MaxOutputTokens)
			if s.count(keyA) != 2 || m.Spent() != cost+per || res.Metadata.CostNanoUSD != cost+per {
				t.Errorf("hits %d spent %d, want %d + %d", s.count(keyA), m.Spent(), cost, per)
			}
		})
	}
	// The attempt times out: it may have been billed, at its bound.
	slow := reply{status: 200, body: ok(answerJ).body, delay: 2 * time.Second}
	s := newServer(t, map[string][]reply{keyA: {slow}})
	c, b, m := meteredClient(t, s, config(keyA), 10*spendUSD, func(bu *provider.Budget) {
		bu.Timeout, bu.AttemptTimeout, bu.MaxRetries = 300*time.Millisecond, 100*time.Millisecond, 0
	})
	_, err := ask(t, c)
	if code := provider.CodeOf(err); code != provider.CodeTimeout && code != provider.CodeUnavailable {
		t.Fatalf("slow: %v", err)
	}
	per := perAttempt(t, b, provider.DefaultBudget().MaxOutputTokens)
	if got := m.Spent(); got != per || s.count(keyA) != 1 {
		t.Errorf("timed out: spent %d, want the attempt's bound %d (hits %d)", got, per, s.count(keyA))
	}
	// The caller cancels while the attempt is in flight: the same.
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	c, _, m = meteredClient(t, s, config(keyA), 10*spendUSD, func(bu *provider.Budget) { bu.MaxRetries = 0 })
	if _, err := askCtx(t, ctx, c); provider.CodeOf(err) != provider.CodeCancelled {
		t.Fatalf("cancelled: %v", err)
	}
	if m.Spent() != per {
		t.Errorf("cancelled: spent %d, want %d", m.Spent(), per)
	}
	if m.Committed() != m.Spent() {
		t.Errorf("committed %d spent %d", m.Committed(), m.Spent())
	}
}
