package provider

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/spend"
)

// T-053: a billable backend is called only through a metered client: the
// client reserves the most a call can cost before the call, refuses the
// call when the meter refuses, and settles what it cost after it.

const pricedModel = "claude-opus-5-5"

var attribution = Attribution{TargetType: "ai_proposal", TargetID: "aip_01M4YDQK00TA00000000000001", RequestID: "req_01M4YDQK00TA00000000000001"}

// billable is a test billable backend.
type billable struct {
	quote    Quote
	quoteErr error
	calls    atomic.Int32
	reqs     []Request
	mu       sync.Mutex
	respond  func(ctx context.Context, req Request) (Response, error)
}

func (b *billable) Name() string { return "test-billable" }
func (b *billable) Quote(Request) (Quote, error) {
	return b.quote, b.quoteErr
}
func (b *billable) Complete(ctx context.Context, req Request) (Response, error) {
	b.calls.Add(1)
	b.mu.Lock()
	b.reqs = append(b.reqs, req)
	b.mu.Unlock()
	return b.respond(ctx, req)
}

func billed(text string, usage Usage) func(context.Context, Request) (Response, error) {
	return func(context.Context, Request) (Response, error) {
		return Response{Text: []byte(text), Model: pricedModel, Credential: "org-a", Usage: usage, InferenceGeo: spend.GeoGlobal}, nil
	}
}

// meter records what the client asks of it.
type meter struct {
	mu       sync.Mutex
	reserve  func(Charge) (Reservation, error)
	charges  []Charge
	settled  []Settlement
	settleTo []Reservation
	ctxErr   []error
	settle   error
}

func (m *meter) Reserve(_ context.Context, c Charge) (Reservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.charges = append(m.charges, c)
	if m.reserve != nil {
		return m.reserve(c)
	}
	return Reservation{ID: "r1", Charge: c}, nil
}

func (m *meter) Settle(ctx context.Context, r Reservation, s Settlement) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settleTo, m.settled, m.ctxErr = append(m.settleTo, r), append(m.settled, s), append(m.ctxErr, ctx.Err())
	return m.settle
}

func metered(t *testing.T, b Billable, m Meter, mutate ...func(*Budget)) *Client {
	t.Helper()
	budget := DefaultBudget()
	for _, f := range mutate {
		f(&budget)
	}
	c, err := NewMetered(b, budget, m)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ask(t *testing.T, c *Client, ctx context.Context) (ProposalResult, error) {
	return c.GenerateModelProposal(ctx, ProposalContext{OrganizationID: orgA, Mode: ModeEdit, Prompt: prompt(t, RoleModeler), Attribution: attribution})
}

func price(t *testing.T) spend.Price {
	p, ok := spend.PriceOf(pricedModel)
	if !ok {
		t.Fatal("no price")
	}
	return p
}

// A billable backend needs a meter: New refuses it, NewMetered needs one.
func TestBillableNeedsAMeter(t *testing.T) {
	b := &billable{quote: Quote{Model: pricedModel, MaxInputTokens: 10}}
	if _, err := New(b, DefaultBudget()); err == nil {
		t.Error("New took a billable backend")
	}
	if _, err := NewMetered(b, DefaultBudget(), nil); err == nil {
		t.Error("NewMetered took no meter")
	}
	if _, err := NewMetered(nil, DefaultBudget(), &meter{}); err == nil {
		t.Error("NewMetered took no backend")
	}
}

// The reservation is the most the call can cost: every attempt (1 + the
// budget's retries) at the quote's input tokens and max_tokens, at the
// dearest inference geo (1.1x; review round 1, M2); the charge names the
// organization, role, model and what it is for.
func TestTheReservationBoundsTheCall(t *testing.T) {
	b := &billable{quote: Quote{Model: pricedModel, MaxInputTokens: 50_000}, respond: billed(validProposal, Usage{InputTokens: 1200, OutputTokens: 300})}
	m := &meter{}
	c := metered(t, b, m, func(bu *Budget) { bu.MaxRetries = 2; bu.MaxOutputTokens = 8192 })
	res, err := ask(t, c, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := price(t)
	want := Charge{OrganizationID: orgA, Role: RoleModeler, Model: pricedModel, Attribution: attribution, NanoUSD: 3 * ceilTenth(p.AttemptBound(50_000, 8192))}
	if len(m.charges) != 1 || m.charges[0] != want {
		t.Fatalf("charges %+v, want %+v", m.charges, want)
	}
	if len(b.reqs) != 1 || b.reqs[0].Reservation != "r1" {
		t.Errorf("the request does not carry its reservation: %+v", b.reqs)
	}
	cost := p.Cost(spend.Tokens{Input: 1200, Output: 300})
	if len(m.settled) != 1 || m.settled[0] != (Settlement{NanoUSD: cost, Usage: Usage{InputTokens: 1200, OutputTokens: 300}, Outcome: SpendUsage,
		Credential: "org-a", InferenceGeo: spend.GeoGlobal}) {
		t.Errorf("settled %+v, want cost %d", m.settled, cost)
	}
	if res.Metadata.CostNanoUSD != cost || res.Metadata.SpendReservation != "r1" || res.Metadata.SpendUnsettled || res.Metadata.InferenceGeo != spend.GeoGlobal ||
		res.Metadata.SpendOverReservation {
		t.Errorf("metadata %+v", res.Metadata)
	}
}

// A refused reservation is a refused call: the backend is never asked.
func TestARefusedReservationRefusesTheCall(t *testing.T) {
	for name, c := range map[string]struct {
		err       error
		code      Code
		retryable bool
		limit     string
	}{
		"quota":           {&Error{Code: CodeQuotaExceeded, Limit: "organization_day"}, CodeQuotaExceeded, false, "organization_day"},
		"monthly quota":   {&Error{Code: CodeQuotaExceeded, Limit: "month"}, CodeQuotaExceeded, false, "month"},
		"meter failed":    {errors.New("database unavailable"), CodeSpendUnavailable, true, ""},
		"meter cancelled": {context.Canceled, CodeSpendUnavailable, true, ""},
		// A meter that grants nothing it can settle (no reservation ID) is
		// a meter that failed (mutation run 1: U6 survived without this).
		"no reservation ID": {nil, CodeSpendUnavailable, true, ""},
	} {
		t.Run(name, func(t *testing.T) {
			b := &billable{quote: Quote{Model: pricedModel, MaxInputTokens: 10}, respond: billed(validProposal, Usage{})}
			m := &meter{reserve: func(Charge) (Reservation, error) { return Reservation{}, c.err }}
			_, err := ask(t, metered(t, b, m), context.Background())
			var pe *Error
			if !errors.As(err, &pe) || pe.Code != c.code || pe.Retryable() != c.retryable || pe.Limit != c.limit {
				t.Fatalf("%v (%+v)", err, pe)
			}
			if b.calls.Load() != 0 || len(m.settled) != 0 {
				t.Errorf("calls %d settled %v", b.calls.Load(), m.settled)
			}
		})
	}
}

// What cannot be priced or attributed is not reserved for, and not called;
// nor is what the client refuses anyway.
func TestNothingIsReservedForARefusedCall(t *testing.T) {
	ok := billed(validProposal, Usage{InputTokens: 1})
	for name, c := range map[string]struct {
		b    *billable
		ctx  ProposalContext
		want Code
	}{
		"unpriced model": {&billable{quote: Quote{Model: "claude-opus-5", MaxInputTokens: 10}, respond: ok},
			ProposalContext{OrganizationID: orgA, Mode: ModeEdit, Attribution: attribution}, CodeInvalidRequest},
		"quote failed": {&billable{quoteErr: errors.New("no projection"), respond: ok},
			ProposalContext{OrganizationID: orgA, Mode: ModeEdit, Attribution: attribution}, CodeInvalidRequest},
		"no attribution": {&billable{quote: Quote{Model: pricedModel, MaxInputTokens: 10}, respond: ok},
			ProposalContext{OrganizationID: orgA, Mode: ModeEdit}, CodeInvalidRequest},
		"half an attribution": {&billable{quote: Quote{Model: pricedModel, MaxInputTokens: 10}, respond: ok},
			ProposalContext{OrganizationID: orgA, Mode: ModeEdit, Attribution: Attribution{TargetType: "ai_proposal"}}, CodeInvalidRequest},
		"other organization's prompt": {&billable{quote: Quote{Model: pricedModel, MaxInputTokens: 10}, respond: ok},
			ProposalContext{OrganizationID: orgB, Mode: ModeEdit, Attribution: attribution}, CodeInvalidRequest},
	} {
		t.Run(name, func(t *testing.T) {
			m := &meter{}
			cl := metered(t, c.b, m)
			in := c.ctx
			in.Prompt = prompt(t, RoleModeler)
			_, err := cl.GenerateModelProposal(context.Background(), in)
			if CodeOf(err) != c.want {
				t.Fatalf("%v, want %s", err, c.want)
			}
			if len(m.charges) != 0 || c.b.calls.Load() != 0 {
				t.Errorf("charges %v calls %d", m.charges, c.b.calls.Load())
			}
		})
	}
	// The concurrency limit refuses before the meter is asked.
	release := make(chan struct{})
	b := &billable{quote: Quote{Model: pricedModel, MaxInputTokens: 10}, respond: func(ctx context.Context, _ Request) (Response, error) {
		<-release
		return Response{Text: []byte(validProposal), Model: pricedModel}, nil
	}}
	m := &meter{}
	cl := metered(t, b, m, func(bu *Budget) { bu.MaxConcurrentPerOrganization = 1 })
	done := make(chan error)
	go func() { _, err := ask(t, cl, context.Background()); done <- err }()
	for b.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	if _, err := ask(t, cl, context.Background()); CodeOf(err) != CodeConcurrencyLimited {
		t.Errorf("second call: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(m.charges) != 1 {
		t.Errorf("charges %d, want 1", len(m.charges))
	}
}

// What a call costs: its usage when an answer came back (whatever the
// answer was), at the geo it reports; nothing when no attempt can have
// been billed; every attempt that ended without an answer at its bound (at
// the dearest geo), since the provider may have billed it (a timeout, a
// lost connection). Never clamped to the reservation: more lost attempts
// than reserved for are recorded at what they may have cost (review round
// 1, M3: the reservation's bound broke, and the ledger never under-records).
func TestSettlement(t *testing.T) {
	p := price(t)
	per := ceilTenth(p.AttemptBound(1000, 100))
	usage := Usage{InputTokens: 900, OutputTokens: 50}
	cost := p.Cost(spend.Tokens{Input: 900, Output: 50})
	global := spend.GeoGlobal
	for name, c := range map[string]struct {
		resp    Response
		err     error
		want    Settlement
		code    Code
		cancel  bool
		timeout bool
	}{
		"answer": {resp: Response{Text: []byte(validProposal), Usage: usage, Credential: "org-b", InferenceGeo: global},
			want: Settlement{NanoUSD: cost, Usage: usage, Outcome: SpendUsage, Credential: "org-b", InferenceGeo: global}},
		"invalid answer is billed": {resp: Response{Text: []byte(`{"status":"approved"}`), Usage: usage, Credential: "org-a", InferenceGeo: global},
			want: Settlement{NanoUSD: cost, Usage: usage, Outcome: SpendUsage, Credential: "org-a", InferenceGeo: global}, code: CodeInvalidOutput},
		"truncated is billed": {resp: Response{Usage: usage, Credential: "org-a", InferenceGeo: global}, err: NewError(CodeTruncated, 0, "org-a"),
			want: Settlement{NanoUSD: cost, Usage: usage, Outcome: SpendUsage, Credential: "org-a", InferenceGeo: global}, code: CodeTruncated},
		"rate limited is not billed": {resp: Response{Credential: "org-a"}, err: NewError(CodeRateLimited, 429, "org-a"),
			want: Settlement{Outcome: SpendNone, Credential: "org-a"}, code: CodeRateLimited},
		"exhausted credentials are not billed": {err: NewError(CodeCredentialsExhausted, 0, ""),
			want: Settlement{Outcome: SpendNone}, code: CodeCredentialsExhausted},
		"a lost connection may be billed": {resp: Response{Credential: "org-a", UnknownAttempts: 1}, err: NewError(CodeUnavailable, 0, "org-a"),
			want: Settlement{NanoUSD: per, Outcome: SpendUnknown, Credential: "org-a", UnknownAttempts: 1}, code: CodeUnavailable},
		"an answer after lost attempts": {resp: Response{Text: []byte(validProposal), Usage: usage, Credential: "org-a", UnknownAttempts: 2, InferenceGeo: global},
			want: Settlement{NanoUSD: cost + 2*per, Usage: usage, Outcome: SpendUnknown, Credential: "org-a", UnknownAttempts: 2, InferenceGeo: global}},
		"more lost attempts than reserved for": {resp: Response{Credential: "org-a", UnknownAttempts: 9}, err: NewError(CodeUnavailable, 0, "org-a"),
			want: Settlement{NanoUSD: 9 * per, Outcome: SpendUnknown, Credential: "org-a", UnknownAttempts: 9}, code: CodeUnavailable},
		"cancelled while in flight": {resp: Response{Credential: "org-a", UnknownAttempts: 1}, cancel: true,
			want: Settlement{NanoUSD: per, Outcome: SpendUnknown, Credential: "org-a", UnknownAttempts: 1}, code: CodeCancelled},
		// Review round 1, I1: an answer that cannot be read settles at the
		// whole reservation, whatever else it says.
		"an undecodable answer": {resp: Response{Credential: "org-a", UnknownAttempts: 1, Undecoded: true}, err: NewError(CodeUnavailable, 0, "org-a"),
			want: Settlement{NanoUSD: 3 * per, Outcome: SpendUnknown, Credential: "org-a", UnknownAttempts: 1}, code: CodeUnavailable},
		// Review round 1, M2: the answer's geo prices it; one it does not
		// name, or this build does not know, at the dearest.
		"an answer from US-only inference": {resp: Response{Text: []byte(validProposal), Usage: usage, Credential: "org-a", InferenceGeo: "us"},
			want: Settlement{NanoUSD: ceilTenth(cost), Usage: usage, Outcome: SpendUsage, Credential: "org-a", InferenceGeo: spend.GeoUS}},
		"an answer naming no geo": {resp: Response{Text: []byte(validProposal), Usage: usage, Credential: "org-a"},
			want: Settlement{NanoUSD: ceilTenth(cost), Usage: usage, Outcome: SpendUsage, Credential: "org-a", InferenceGeo: spend.GeoUnknown}},
		"an answer naming an unknown geo": {resp: Response{Text: []byte(validProposal), Usage: usage, Credential: "org-a", InferenceGeo: "eu-west PROMPT"},
			want: Settlement{NanoUSD: ceilTenth(cost), Usage: usage, Outcome: SpendUsage, Credential: "org-a", InferenceGeo: spend.GeoUnknown}},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b := &billable{quote: Quote{Model: pricedModel, MaxInputTokens: 1000}, respond: func(context.Context, Request) (Response, error) {
				if c.cancel {
					cancel()
					return c.resp, NewError(CodeCancelled, 0, "org-a").WithCause(context.Canceled)
				}
				return c.resp, c.err
			}}
			m := &meter{}
			_, err := ask(t, metered(t, b, m, func(bu *Budget) { bu.MaxOutputTokens = 100; bu.MaxRetries = 2 }), ctx)
			if CodeOf(err) != c.code {
				t.Fatalf("code %q, want %q (%v)", CodeOf(err), c.code, err)
			}
			if len(m.settled) != 1 || m.settled[0] != c.want {
				t.Fatalf("settled %+v, want %+v", m.settled, c.want)
			}
			// The settlement survives the caller's cancellation.
			if m.ctxErr[0] != nil {
				t.Errorf("settled with a done context: %v", m.ctxErr[0])
			}
			if m.settleTo[0].ID != "r1" {
				t.Errorf("settled %+v", m.settleTo[0])
			}
		})
	}
}

// A settlement that cannot be written leaves the reservation in place
// (counted at its bound) and says so; the answer is still the caller's.
func TestAFailedSettlementKeepsTheReservation(t *testing.T) {
	b := &billable{quote: Quote{Model: pricedModel, MaxInputTokens: 10}, respond: billed(validProposal, Usage{InputTokens: 5})}
	m := &meter{settle: errors.New("database unavailable")}
	res, err := ask(t, metered(t, b, m), context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Metadata.SpendUnsettled || res.Metadata.CostNanoUSD != price(t).Cost(spend.Tokens{Input: 5}) {
		t.Errorf("metadata %+v", res.Metadata)
	}
}

// The in-memory meter (live eval and smoke) never lets concurrent
// reservations pass its limit, releases what a settlement does not use,
// and settles a reservation once.
func TestMemoryMeter(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryMeter(1000)
	var wg sync.WaitGroup
	var granted atomic.Int64
	var refused atomic.Int32
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Reserve(ctx, Charge{NanoUSD: 100}); err == nil {
				granted.Add(100)
			} else if CodeOf(err) == CodeQuotaExceeded {
				refused.Add(1)
			} else {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if granted.Load() != 1000 || refused.Load() != 40 || m.Committed() != 1000 {
		t.Fatalf("granted %d refused %d committed %d", granted.Load(), refused.Load(), m.Committed())
	}
	m2 := NewMemoryMeter(1000)
	r, err := m2.Reserve(ctx, Charge{NanoUSD: 600})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Reserve(ctx, Charge{NanoUSD: 600}); CodeOf(err) != CodeQuotaExceeded {
		t.Errorf("over the limit: %v", err)
	}
	if err := m2.Settle(ctx, r, Settlement{NanoUSD: 150, Outcome: SpendUsage}); err != nil {
		t.Fatal(err)
	}
	if m2.Committed() != 150 || m2.Spent() != 150 {
		t.Errorf("committed %d spent %d", m2.Committed(), m2.Spent())
	}
	if err := m2.Settle(ctx, r, Settlement{NanoUSD: 150}); err == nil {
		t.Error("settled twice")
	}
	if _, err := m2.Reserve(ctx, Charge{NanoUSD: 850}); err != nil {
		t.Errorf("released room: %v", err)
	}
	if _, err := m2.Reserve(ctx, Charge{NanoUSD: 0}); err == nil {
		t.Error("a zero charge")
	}
}
