package me

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Route is one implemented operation of openapi.yaml.
type Route struct {
	Method, Path, OperationID string
	Scope                     string        // x-tenant-scope
	Roles                     []tenant.Role // x-required-roles
}

// Routes are the operations this package serves. A contract test compares
// them with planning/api-operations.json: GET /v1/me selects no
// organization (scope "user"), so it requires no role.
var Routes = []Route{
	{"GET", "/v1/me", "getMe", "user", nil},
}

// Reader is what the handler needs of Service.
type Reader interface {
	Get(ctx context.Context, user string) (Me, error)
}

// Register adds GET /v1/me to mux. The request is authenticated by
// httpapi.Self: a token is required, X-Organization-ID is refused and an
// identity without a user is 403.
func Register(mux *http.ServeMux, log *slog.Logger, v auth.Verifier, users httpapi.Resolver, s Reader) {
	r := Routes[0]
	mux.Handle(r.Method+" "+r.Path, httpapi.Handler(log, httpapi.Self(v, users, func(w http.ResponseWriter, req *http.Request, user string) error {
		out, err := s.Get(req.Context(), user)
		if err != nil {
			return err
		}
		// WriteJSON sends Cache-Control: no-store with every response: this
		// one is personal data.
		httpapi.WriteJSON(w, 200, out)
		return nil
	})))
}
