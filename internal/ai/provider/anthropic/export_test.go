package anthropic

import (
	"net/http"
)

// NewForTest points the backend at a local test server. Test-only: no
// production code can change the provider URL.
func NewForTest(cfg Config, baseURL string, client *http.Client) (*Backend, error) {
	return newBackend(cfg, baseURL, client)
}

var ProductionURL = productionURL
