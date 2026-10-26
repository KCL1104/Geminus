package vision

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
)

// T-056: a segmenter that costs money is called only through a meter, as
// the provider client's billable backends are (T-053): the most the call
// can cost is reserved before it, at the model's price per image (every
// attempt, 1 + the budget's retries), and what it cost is settled after
// it, even when the caller's context has ended.

const testModel = "test-sam"

// billableSeg is a test billable segmenter.
type billableSeg struct {
	model   string
	respond func(ctx context.Context, req Request) (Response, error)
	calls   atomic.Int32
	reqs    []Request
}

func (b *billableSeg) Name() string                       { return "test-billable-segmenter" }
func (b *billableSeg) QuoteModel(Request) (string, error) { return b.model, nil }
func (b *billableSeg) Segment(ctx context.Context, req Request) (Response, error) {
	b.calls.Add(1)
	b.reqs = append(b.reqs, req)
	return b.respond(ctx, req)
}

// meter records what the client asks of it.
type meter struct {
	mu      sync.Mutex
	reserve func(provider.Charge) (provider.Reservation, error)
	charges []provider.Charge
	settled []provider.Settlement
	ctxErr  []error
}

func (m *meter) Reserve(_ context.Context, c provider.Charge) (provider.Reservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.charges = append(m.charges, c)
	if m.reserve != nil {
		return m.reserve(c)
	}
	return provider.Reservation{ID: "r1", Charge: c}, nil
}

func (m *meter) Settle(ctx context.Context, r provider.Reservation, s provider.Settlement) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settled, m.ctxErr = append(m.settled, s), append(m.ctxErr, ctx.Err())
	return nil
}

func metered(t *testing.T, b Billable, m provider.Meter) *Client {
	t.Helper()
	c, err := NewMetered(b, DefaultBudget(), m)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAMeteredSegmentation(t *testing.T) {
	const per = 2_500_000 // nano-USD per image (a test price)
	restore := priceImagesAt(map[string]int64{testModel: per})
	defer restore()
	b := &billableSeg{model: testModel, respond: func(context.Context, Request) (Response, error) {
		return Response{Boxes: []Box{box(0, 0, 0, 10, 10)}, Model: testModel, Credential: "meta-a", Images: 1}, nil
	}}
	m := &meter{}
	res, err := metered(t, b, m).Segment(context.Background(), input())
	if err != nil {
		t.Fatal(err)
	}
	retries := int64(DefaultBudget().MaxRetries)
	want := provider.Charge{OrganizationID: orgA, Role: provider.RoleModeler, Model: testModel, Attribution: attribution, NanoUSD: per * (1 + retries)}
	if len(m.charges) != 1 || m.charges[0] != want {
		t.Fatalf("charges %+v, want %+v", m.charges, want)
	}
	if b.reqs[0].Reservation != "r1" {
		t.Errorf("the request does not carry its reservation: %+v", b.reqs[0])
	}
	if len(m.settled) != 1 || m.settled[0] != (provider.Settlement{NanoUSD: per, Outcome: provider.SpendUsage, Credential: "meta-a"}) {
		t.Errorf("settled %+v", m.settled)
	}
	if res.Metadata.CostNanoUSD != per || res.Metadata.SpendReservation != "r1" || res.Metadata.SpendUnsettled {
		t.Errorf("metadata %+v", res.Metadata)
	}
}

// What a call cost: an answer's images; an attempt that ended without an
// answer after it may have reached the provider, at its price; nothing
// when no attempt can have been billed. Settled even when the caller has
// gone.
func TestSegmentationSettlement(t *testing.T) {
	const per = 1000
	restore := priceImagesAt(map[string]int64{testModel: per})
	defer restore()
	for name, c := range map[string]struct {
		resp Response
		err  error
		want provider.Settlement
	}{
		"refused":       {Response{}, provider.NewError(provider.CodeRejected, 400, "meta-a"), provider.Settlement{Outcome: provider.SpendNone}},
		"lost":          {Response{UnknownAttempts: 2, Credential: "meta-a"}, provider.NewError(provider.CodeUnavailable, 0, "meta-a"), provider.Settlement{NanoUSD: 2 * per, Outcome: provider.SpendUnknown, Credential: "meta-a", UnknownAttempts: 2}},
		"answered late": {Response{Images: 1, UnknownAttempts: 1, Credential: "meta-a", Model: testModel}, nil, provider.Settlement{NanoUSD: 2 * per, Outcome: provider.SpendUnknown, Credential: "meta-a", UnknownAttempts: 1}},
	} {
		b := &billableSeg{model: testModel, respond: func(context.Context, Request) (Response, error) { return c.resp, c.err }}
		m := &meter{}
		_, _ = metered(t, b, m).Segment(context.Background(), input())
		if len(m.settled) != 1 || m.settled[0] != c.want {
			t.Errorf("%s: settled %+v, want %+v", name, m.settled, c.want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &billableSeg{model: testModel, respond: func(context.Context, Request) (Response, error) {
		cancel()
		return Response{Images: 1, Credential: "meta-a"}, nil
	}}
	m := &meter{}
	if _, err := metered(t, b, m).Segment(ctx, input()); provider.CodeOf(err) != provider.CodeCancelled {
		t.Errorf("cancelled: %v", err)
	}
	if len(m.settled) != 1 || m.settled[0].NanoUSD != per || m.ctxErr[0] != nil {
		t.Errorf("a cancelled call was not settled on its own context: %+v %v", m.settled, m.ctxErr)
	}
}

// A model this build cannot price, a refused reservation and a ledger that
// cannot answer each refuse the call: the segmenter is never asked.
func TestARefusedReservationRefusesTheSegmentation(t *testing.T) {
	b := &billableSeg{model: "sam-3.1", respond: func(context.Context, Request) (Response, error) { return Response{}, nil }}
	m := &meter{}
	if _, err := metered(t, b, m).Segment(context.Background(), input()); provider.CodeOf(err) != provider.CodeInvalidRequest ||
		len(m.charges) != 0 || b.calls.Load() != 0 {
		t.Errorf("unpriced: %v, %d charges, %d calls", err, len(m.charges), b.calls.Load())
	}
	restore := priceImagesAt(map[string]int64{testModel: 1000})
	defer restore()
	b.model = testModel
	for name, c := range map[string]struct {
		err  error
		want provider.Code
	}{
		"a limit":           {&provider.Error{Code: provider.CodeQuotaExceeded, Limit: provider.LimitOrganizationDay}, provider.CodeQuotaExceeded},
		"no ledger":         {errors.New("connection refused"), provider.CodeSpendUnavailable},
		"an empty reserve ": {nil, provider.CodeSpendUnavailable},
	} {
		m := &meter{reserve: func(provider.Charge) (provider.Reservation, error) { return provider.Reservation{}, c.err }}
		_, err := metered(t, b, m).Segment(context.Background(), input())
		if provider.CodeOf(err) != c.want || b.calls.Load() != 0 {
			t.Errorf("%s: %v after %d calls", name, err, b.calls.Load())
		}
		var pe *provider.Error
		if c.want == provider.CodeQuotaExceeded && (!errors.As(err, &pe) || pe.Limit != provider.LimitOrganizationDay) {
			t.Errorf("%s: the limit is not named: %v", name, err)
		}
	}
}
