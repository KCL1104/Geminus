package anthropic

import (
	"net/http"
	"slices"
	"time"
)

// NewForTest points the backend at a local test server. Test-only: no
// production code can change the provider URL. The backend serves every
// role (SetRoles narrows it): its zero Approval names none.
func NewForTest(cfg Config, baseURL string, client *http.Client) (*Backend, error) {
	return newBackend(cfg, baseURL, client, func(string) bool { return true })
}

// SetClock replaces the backend's clock (the 402 cooldown).
func SetClock(b *Backend, now func() time.Time) { b.now = now }

// SetRoles makes the backend serve only roles, as an approval's data
// policy would.
func SetRoles(b *Backend, roles ...string) {
	b.allows = func(role string) bool { return slices.Contains(roles, role) }
}

// BillingCooldown is the 402 cooldown.
const BillingCooldown = billingCooldown

// QuoteOverheadTokens is the quote's allowance for the API's own input.
const QuoteOverheadTokens = quoteOverheadTokens

var ProductionURL = productionURL
