package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// H-037 (T-055 review round 1, M1): a billable backend's quote may ask the
// provider before the call is reserved (the Meta adapter counts a request
// with images with count_tokens). So the client hands the quote the call's
// context: the quote and the call share one deadline, the budget's Timeout
// from before the quote, and the caller's cancellation. A typed error of
// the quote is the call's error, its code kept; nothing is reserved and the
// backend is not called.

// quoting is a billable backend whose quote is quoteFn.
type quoting struct {
	*billable
	quoteFn func(ctx context.Context, req Request) (Quote, error)
}

func (q *quoting) Quote(ctx context.Context, req Request) (Quote, error) { return q.quoteFn(ctx, req) }

func TestTheQuoteRunsWithinTheCall(t *testing.T) {
	var quoteDeadline, callDeadline time.Time
	var quoted, called bool
	b := &quoting{billable: &billable{respond: func(ctx context.Context, req Request) (Response, error) {
		callDeadline, called = ctx.Deadline()
		return billed(validProposal, Usage{InputTokens: 1})(ctx, req)
	}}, quoteFn: func(ctx context.Context, _ Request) (Quote, error) {
		quoteDeadline, quoted = ctx.Deadline()
		return Quote{Model: pricedModel, MaxInputTokens: 10}, nil
	}}
	start := time.Now()
	c := metered(t, b, &meter{}, func(bu *Budget) { bu.Timeout, bu.AttemptTimeout = 7*time.Second, time.Second })
	if _, err := ask(t, c, context.Background()); err != nil {
		t.Fatal(err)
	}
	end := time.Now()
	if !quoted || quoteDeadline.Before(start.Add(7*time.Second)) || quoteDeadline.After(end.Add(7*time.Second)) {
		t.Errorf("the quote's deadline %v (set: %v), want the call's: 7 s from its start", quoteDeadline, quoted)
	}
	if !called || !callDeadline.Equal(quoteDeadline) {
		t.Errorf("the call's deadline %v, the quote's %v: one deadline for both", callDeadline, quoteDeadline)
	}

	// The caller's cancellation reaches the quote.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.quoteFn = func(qctx context.Context, _ Request) (Quote, error) {
		cancel()
		select {
		case <-qctx.Done():
			return Quote{}, NewError(CodeCancelled, 0, "").WithCause(qctx.Err())
		case <-time.After(5 * time.Second):
			return Quote{}, errors.New("the quote was not cancelled")
		}
	}
	m := &meter{}
	if _, err := ask(t, metered(t, b, m), ctx); CodeOf(err) != CodeCancelled || !errors.Is(err, context.Canceled) || len(m.charges) != 0 {
		t.Errorf("cancelled: %v, charges %v", err, m.charges)
	}
}

func TestATypedQuoteErrorIsTheCallsError(t *testing.T) {
	for name, qerr := range map[string]error{
		"rate limited": NewError(CodeRateLimited, 429, "meta"),
		"unavailable":  NewError(CodeUnavailable, 0, "meta"),
		"key refused":  NewError(CodeCredentialsExhausted, 401, "meta"),
		"rejected":     NewError(CodeRejected, 400, "meta"),
		"policy":       NewError(CodePolicyNotApproved, 0, ""),
		"wrapped":      fmt.Errorf("count: %w", NewError(CodeTimeout, 0, "meta")),
	} {
		t.Run(name, func(t *testing.T) {
			b := &quoting{billable: &billable{respond: billed(validProposal, Usage{InputTokens: 1})},
				quoteFn: func(context.Context, Request) (Quote, error) { return Quote{}, qerr }}
			m := &meter{}
			_, err := ask(t, metered(t, b, m), context.Background())
			var want, pe *Error
			if !errors.As(qerr, &want) {
				t.Fatal("not a provider error")
			}
			if !errors.As(err, &pe) || pe.Code != want.Code || pe.Status != want.Status || pe.Credential != want.Credential || pe.Retryable() != want.Retryable() {
				t.Errorf("%v (%+v), want %+v", err, pe, want)
			}
			if len(m.charges) != 0 || len(m.settled) != 0 || b.calls.Load() != 0 {
				t.Errorf("charges %v settled %v calls %d", m.charges, m.settled, b.calls.Load())
			}
		})
	}
}
