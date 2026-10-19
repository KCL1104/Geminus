package anthropic

import (
	"net/http"
	"time"
)

// NewForTest points the backend at a local test server. Test-only: no
// production code can change the provider URL.
func NewForTest(cfg Config, baseURL string, client *http.Client) (*Backend, error) {
	return newBackend(cfg, baseURL, client)
}

// SetClock replaces the backend's clock (the 402 cooldown).
func SetClock(b *Backend, now func() time.Time) { b.now = now }

// BillingCooldown is the 402 cooldown.
const BillingCooldown = billingCooldown

var ProductionURL = productionURL
