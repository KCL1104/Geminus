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
//     budget's retries) at its input bound and max_tokens. A model this
//     build cannot price is refused;
//  3. reserves that amount; a refused reservation is a refused call
//     (quota_exceeded, or spend_unavailable when the meter cannot answer),
//     and the backend is never asked;
//  4. calls the backend with the reservation;
//  5. settles the reservation with what the call cost, even when the
//     caller's context has ended: the usage of the answer that came back
//     (a refused, truncated or invalid answer was billed too), plus every
//     attempt that ended without an answer after it may have reached the
//     provider, at its bound (the provider may have billed it), at most the
//     reservation; nothing when no attempt can have been billed. A
//     settlement that fails leaves the reservation counted at its bound.
//
// Only 401, 402 and 403 make the adapter fail over (D-018); an attempt
// answered with an error status is not billed, so a call is billed for at
// most 1 + MaxRetries attempts.

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
}

// Billable is a backend that costs money.
type Billable interface {
	Backend
	Quote(Request) (Quote, error)
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

// metering is one call's reservation.
type metering struct {
	price      spend.Price
	perAttempt int64
	res        Reservation
}

// reserve prices req and reserves for it (steps 2 and 3).
func (c *Client) reserve(ctx context.Context, org string, role Role, attr Attribution, req Request) (*metering, error) {
	q, err := c.billable.Quote(req)
	if err != nil {
		return nil, invalidRequest()
	}
	price, ok := spend.PriceOf(q.Model)
	if !ok {
		return nil, invalidRequest()
	}
	per := price.AttemptBound(q.MaxInputTokens, req.MaxOutputTokens)
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
	return &metering{price: price, perAttempt: per, res: res}, nil
}

// settlement is what the call that answered resp cost (step 5).
func (m *metering) settlement(resp Response) Settlement {
	u := resp.Usage
	cost := m.price.Cost(spend.Tokens{Input: u.InputTokens, Output: u.OutputTokens, CacheWrite: u.CacheCreationInputTokens, CacheRead: u.CacheReadInputTokens})
	s := Settlement{Usage: u, Credential: resp.Credential, UnknownAttempts: resp.UnknownAttempts}
	switch {
	case resp.UnknownAttempts > 0:
		s.Outcome = SpendUnknown
		s.NanoUSD = addSat(cost, mulSat(int64(resp.UnknownAttempts), m.perAttempt))
		if s.NanoUSD > m.res.Charge.NanoUSD {
			s.NanoUSD = max(m.res.Charge.NanoUSD, cost)
		}
	case u != (Usage{}):
		s.Outcome, s.NanoUSD = SpendUsage, cost
	default:
		s.Outcome = SpendNone
	}
	return s
}

// settle records s for the reservation, whether or not ctx has ended, and
// notes it in meta.
func (c *Client) settle(ctx context.Context, m *metering, s Settlement, meta *Metadata) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	err := c.meter.Settle(sctx, m.res, s)
	meta.CostNanoUSD, meta.SpendReservation, meta.SpendUnsettled = s.NanoUSD, m.res.ID, err != nil
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
