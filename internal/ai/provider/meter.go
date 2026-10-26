package provider

import (
	"context"
	"errors"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/kcl1104/geminus/internal/ai/spend"
)

// Spend metering (T-053, H-018; PROPOSED D-110, D-111). A backend that
// costs money is Billable: the Client takes one only with a Meter
// (NewMetered), and for every call it
//
//  1. checks the call as for any backend (prompt sealed and in budget, a
//     free slot for the organization) and that it is attributed;
//  2. prices the most the call can cost, from the backend's Quote and the
//     model's list price (internal/ai/spend): every attempt (1 + the
//     budget's retries) at its input bound and max_tokens, at the dearest
//     inference geo (the request names none, so the workspace's default
//     decides: 1.1x, review round 1 M2) when the model's provider prices
//     geos (spend.GeoPriced: Anthropic), at the list price otherwise (the
//     Meta Model API names no geo, T-055). A model this build cannot price
//     is refused. The quote runs within the call's deadline (the budget's
//     Timeout, which starts before it) and the caller's cancellation: it
//     may ask the provider (the Meta adapter counts a request with images,
//     H-037). A quote that fails with a *Error fails the call with it (its
//     code kept); any other failure is invalid_request; neither reserves;
//  3. reserves that amount; a refused reservation is a refused call
//     (quota_exceeded, or spend_unavailable when the meter cannot answer),
//     and the backend is never asked;
//  4. calls the backend with the reservation;
//  5. settles the reservation with what the call cost, even when the
//     caller's context has ended: the usage of the answer that came back
//     (a refused, truncated or invalid answer was billed too) at the geo it
//     says it ran in (the dearest when it names none this build knows; the
//     list price for a provider without geo prices),
//     plus every attempt that ended without an answer after it may have
//     reached the provider, at its bound (the provider may have billed it);
//     an answer that came back but cannot be read, at the whole
//     reservation at least; nothing when no attempt can have been billed.
//     The settlement is what was billed even when it is more than the
//     reservation (the bound's assumptions broke): it is never clamped,
//     and Metadata.SpendOverReservation says so (review round 1, M3, L1).
//     A settlement that fails leaves the reservation counted at its bound.
//
// Only 401, 402 and 403 make the adapter fail over (D-018), and only while
// every attempt on the credential was answered with an error status, which
// is not billed: after an attempt that may have been billed it fails the
// call instead (review round 1, M3). So the attempts that may have been
// billed are all on one credential, at most 1 + MaxRetries of them, which
// is what the reservation holds.

// Spend codes (T-053).
const (
	// CodeQuotaExceeded: the call would pass a spend limit (Error.Limit
	// says which); no provider call was made. Not retryable: the limit
	// holds until the day or month ends.
	CodeQuotaExceeded Code = "quota_exceeded"
	// CodeSpendUnavailable: the spend ledger could not reserve for the
	// call; no provider call was made. Retryable.
	CodeSpendUnavailable Code = "spend_unavailable"
)

// The limits a quota_exceeded call names (Error.Limit).
const (
	LimitOrganizationDay = "organization_day"
	LimitDay             = "day"
	LimitMonth           = "month"
	// LimitRun bounds one live eval or smoke run (MemoryMeter).
	LimitRun = "run"
)

// Attribution names what a call is for, for the spend ledger and its
// audit: the resource a job works on and the request that queued it.
type Attribution struct {
	TargetType string // "ai_proposal", "ai_analysis", "import"
	TargetID   string
	RequestID  string
}

func (a Attribution) complete() bool {
	return a.TargetType != "" && a.TargetID != "" && a.RequestID != ""
}

// Quote is what a billable backend says of a request before sending it:
// the model it will call and the most input tokens one attempt can be
// billed for.
type Quote struct {
	Model          string
	MaxInputTokens int64
	// Counted is the provider's own count of the request's input tokens
	// when the backend asked for one (H-037: a request with images, the
	// Meta adapter's count_tokens), 0 when the quote is the backend's own
	// bound. Only what the run records reads it (the live photo smoke's
	// report: the count against the tokens billed); the reservation is
	// MaxInputTokens.
	Counted int64
}

// Billable is a backend that costs money. Quote is called before the
// reservation, with the call's context (its deadline and the caller's
// cancellation): a backend may ask its provider how large req is (H-037).
// A failure it can classify is a *Error, the call's error; any other error
// is a request it cannot quote (invalid_request).
type Billable interface {
	Backend
	Quote(ctx context.Context, req Request) (Quote, error)
}

// Charge is what a reservation is asked for.
type Charge struct {
	OrganizationID string
	Role           Role
	Model          string
	Attribution    Attribution
	// NanoUSD is the most the call can cost.
	NanoUSD int64
}

// Reservation is a granted Charge.
type Reservation struct {
	ID     string
	Charge Charge
}

// SpendOutcome says how a settlement's amount was found.
type SpendOutcome string

const (
	// SpendUsage: an answer came back; its usage is what was billed.
	SpendUsage SpendOutcome = "usage"
	// SpendNone: no attempt can have been billed (refused before sending,
	// or every attempt answered with an error status).
	SpendNone SpendOutcome = "none"
	// SpendUnknown: an attempt ended without an answer after it may have
	// reached the provider; it is counted at its bound.
	SpendUnknown SpendOutcome = "unknown"
)

// Settlement is what a call cost.
type Settlement struct {
	NanoUSD         int64
	Usage           Usage
	Outcome         SpendOutcome
	Credential      string // label of the credential that answered, if any
	UnknownAttempts int
	// InferenceGeo is the geo the answer's usage was priced at
	// (spend.GeoGlobal, spend.GeoUS, or spend.GeoUnknown when the answer
	// named none this build knows); empty when no usage was priced.
	InferenceGeo string
}

// Meter is the spend ledger (internal/storage/aispend; MemoryMeter for a
// live eval). Reserve returns an *Error with CodeQuotaExceeded and the
// Limit when the charge would pass a limit, any other error when it cannot
// answer. Settle is called once per granted reservation.
type Meter interface {
	Reserve(ctx context.Context, c Charge) (Reservation, error)
	Settle(ctx context.Context, r Reservation, s Settlement) error
}

// settleTimeout bounds a settlement, which runs after the caller's context
// may have ended.
const settleTimeout = 10 * time.Second

// NewMetered returns a Client for the billable backend b under budget,
// metered by m.
func NewMetered(b Billable, budget Budget, m Meter) (*Client, error) {
	if b == nil || m == nil {
		return nil, errors.New("ai provider: a billable backend and a meter are required")
	}
	if err := budget.Validate(); err != nil {
		return nil, err
	}
	return &Client{backend: b, billable: b, meter: m, budget: budget, active: map[string]int{}}, nil
}

func mulSat(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

func addSat(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// metering is one call's reservation. geo: the model's provider prices
// inference geos (spend.GeoPriced: Anthropic's), so the attempt's bound is
// at the dearest geo and an answer is priced at the geo it reports;
// otherwise both are the list price (the Meta Model API, T-055).
type metering struct {
	price      spend.Price
	geo        bool
	perAttempt int64
	res        Reservation
}

// reserve prices req, quoting it within callCtx (the call's deadline), and
// reserves for it under ctx (steps 2 and 3).
func (c *Client) reserve(ctx, callCtx context.Context, org string, role Role, attr Attribution, req Request) (*metering, error) {
	q, err := c.billable.Quote(callCtx, req)
	if err != nil {
		var pe *Error
		if errors.As(err, &pe) {
			return nil, pe
		}
		return nil, invalidRequest()
	}
	price, ok := spend.PriceOf(q.Model)
	if !ok {
		return nil, invalidRequest()
	}
	geo := spend.GeoPriced(q.Model)
	per := price.AttemptBound(q.MaxInputTokens, req.MaxOutputTokens)
	if geo {
		per = spend.AtDearestGeo(per)
	}
	most := mulSat(per, int64(1+max(0, req.MaxRetries)))
	res, err := c.meter.Reserve(ctx, Charge{OrganizationID: org, Role: role, Model: q.Model, Attribution: attr, NanoUSD: most})
	if err != nil {
		var pe *Error
		if errors.As(err, &pe) && pe.Code == CodeQuotaExceeded {
			return nil, &Error{Code: CodeQuotaExceeded, Limit: pe.Limit}
		}
		return nil, NewError(CodeSpendUnavailable, 0, "")
	}
	if res.ID == "" {
		return nil, NewError(CodeSpendUnavailable, 0, "")
	}
	return &metering{price: price, geo: geo, perAttempt: per, res: res}, nil
}

// settlement is what the call that answered resp cost (step 5).
func (m *metering) settlement(resp Response) Settlement {
	u := resp.Usage
	s := Settlement{Usage: u, Credential: resp.Credential, UnknownAttempts: resp.UnknownAttempts}
	var cost int64
	if u != (Usage{}) {
		cost = m.price.Cost(spend.Tokens{Input: u.InputTokens, Output: u.OutputTokens, CacheWrite: u.CacheCreationInputTokens,
			CacheRead: u.CacheReadInputTokens})
		if m.geo {
			s.InferenceGeo = spend.Geo(resp.InferenceGeo)
			cost = spend.AtGeo(cost, s.InferenceGeo)
		}
	}
	switch {
	case resp.UnknownAttempts > 0:
		s.Outcome = SpendUnknown
		s.NanoUSD = addSat(cost, mulSat(int64(resp.UnknownAttempts), m.perAttempt))
		if resp.Undecoded {
			// An answer came back that cannot be read: its usage is unknown,
			// up to what the whole call was reserved for (review round 1, I1).
			s.NanoUSD = max(s.NanoUSD, m.res.Charge.NanoUSD)
		}
	case u != (Usage{}):
		s.Outcome, s.NanoUSD = SpendUsage, cost
	default:
		s.Outcome = SpendNone
	}
	return s
}

// settle records s for the reservation, whether or not ctx has ended, and
// notes it in meta: what it cost, at which geo, and whether that is more
// than the reservation held.
func (c *Client) settle(ctx context.Context, m *metering, s Settlement, meta *Metadata) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	err := c.meter.Settle(sctx, m.res, s)
	meta.CostNanoUSD, meta.SpendReservation, meta.SpendUnsettled = s.NanoUSD, m.res.ID, err != nil
	meta.InferenceGeo, meta.SpendOverReservation = s.InferenceGeo, s.NanoUSD > m.res.Charge.NanoUSD
}

// MemoryMeter is an in-process meter with one limit: a live eval or smoke
// run's own cap (cmd/ai-eval-live). It is not the deployment's ledger.
type MemoryMeter struct {
	mu        sync.Mutex
	limit     int64
	committed int64
	spent     int64
	seq       int
	open      map[string]int64
}

// NewMemoryMeter returns a meter that lets at most limit nano-USD be
// reserved or spent.
func NewMemoryMeter(limit int64) *MemoryMeter {
	return &MemoryMeter{limit: limit, open: map[string]int64{}}
}

// Reserve grants c if the committed amount stays within the limit.
func (m *MemoryMeter) Reserve(_ context.Context, c Charge) (Reservation, error) {
	if c.NanoUSD <= 0 {
		return Reservation{}, errors.New("ai provider: a charge must be positive")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.NanoUSD > m.limit-m.committed {
		return Reservation{}, &Error{Code: CodeQuotaExceeded, Limit: LimitRun}
	}
	m.seq++
	id := "memory-" + strconv.Itoa(m.seq)
	m.committed += c.NanoUSD
	m.open[id] = c.NanoUSD
	return Reservation{ID: id, Charge: c}, nil
}

// Settle replaces the reservation by what the call cost.
func (m *MemoryMeter) Settle(_ context.Context, r Reservation, s Settlement) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	reserved, ok := m.open[r.ID]
	if !ok {
		return errors.New("ai provider: no such open reservation")
	}
	delete(m.open, r.ID)
	m.committed += s.NanoUSD - reserved
	m.spent += s.NanoUSD
	return nil
}

// Committed is what open reservations and settled calls hold.
func (m *MemoryMeter) Committed() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.committed
}

// Spent is what settled calls cost.
func (m *MemoryMeter) Spent() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.spent
}
