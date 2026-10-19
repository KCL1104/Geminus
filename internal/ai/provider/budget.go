package provider

import (
	"fmt"
	"time"
)

// Budget bounds one provider call (AI_SPEC §10, SECURITY §8). The values
// are engineering candidates like D-007's limits: the owner's token quota
// per tenant and period (H-001, H-017) is still open, and no quota over
// time is enforced here (a period quota needs storage; T-025/T-036).
type Budget struct {
	// MaxPromptBytes bounds the system text plus the user message. Every
	// token of the provider's tokenizer covers at least one byte, so this
	// is also an upper bound on input tokens.
	MaxPromptBytes int
	// MaxOutputTokens is the provider's max_tokens.
	MaxOutputTokens int64
	// MaxOutputBytes bounds the answer the server reads and validates.
	MaxOutputBytes int
	// Timeout bounds the whole call: every attempt, retry and failover.
	Timeout time.Duration
	// AttemptTimeout bounds one HTTP attempt.
	AttemptTimeout time.Duration
	// MaxRetries is the number of retries on the same credential for 408,
	// 409, 429, 5xx, 529 and connection errors (D-018).
	MaxRetries int
	// MaxConcurrentPerOrganization bounds the calls one organization has in
	// flight in this process.
	MaxConcurrentPerOrganization int
}

// DefaultBudget is used when the deployment sets none.
func DefaultBudget() Budget {
	return Budget{
		MaxPromptBytes:               128 << 10,
		MaxOutputTokens:              8192,
		MaxOutputBytes:               256 << 10,
		Timeout:                      120 * time.Second,
		AttemptTimeout:               90 * time.Second,
		MaxRetries:                   2,
		MaxConcurrentPerOrganization: 2,
	}
}

// BudgetCaps are the largest values a deployment may configure.
var BudgetCaps = Budget{
	MaxPromptBytes:               512 << 10,
	MaxOutputTokens:              32000,
	MaxOutputBytes:               1 << 20,
	Timeout:                      300 * time.Second, // the job timeout of D-007
	AttemptTimeout:               300 * time.Second,
	MaxRetries:                   4,
	MaxConcurrentPerOrganization: 16,
}

// Validate checks that every limit is set, positive and within BudgetCaps.
func (b Budget) Validate() error {
	check := func(name string, v, max int64, allowZero bool) error {
		if v < 0 || (v == 0 && !allowZero) || v > max {
			return fmt.Errorf("ai budget: %s must be between %d and %d", name, map[bool]int64{true: 0, false: 1}[allowZero], max)
		}
		return nil
	}
	for _, c := range []error{
		check("max prompt bytes", int64(b.MaxPromptBytes), int64(BudgetCaps.MaxPromptBytes), false),
		check("max output tokens", b.MaxOutputTokens, BudgetCaps.MaxOutputTokens, false),
		check("max output bytes", int64(b.MaxOutputBytes), int64(BudgetCaps.MaxOutputBytes), false),
		check("timeout", int64(b.Timeout), int64(BudgetCaps.Timeout), false),
		check("attempt timeout", int64(b.AttemptTimeout), int64(b.Timeout), false),
		check("max retries", int64(b.MaxRetries), int64(BudgetCaps.MaxRetries), true),
		check("max concurrent calls per organization", int64(b.MaxConcurrentPerOrganization), int64(BudgetCaps.MaxConcurrentPerOrganization), false),
	} {
		if c != nil {
			return c
		}
	}
	return nil
}
