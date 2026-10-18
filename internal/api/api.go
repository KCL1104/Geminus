// Package api assembles the API's routes into one http.Handler (the same
// handler cmd/api serves and the integration tests exercise). It wires
// packages together and holds no business rules.
package api

import (
	"log/slog"
	"net/http"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/project"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Handler serves every implemented /v1 operation. Any other path or method
// is 404 NOT_FOUND in the error envelope.
func Handler(log *slog.Logger, v auth.Verifier, db *tenant.DB) http.Handler {
	mux := http.NewServeMux()
	project.Register(mux, log, v, db, &project.Service{DB: db})
	mux.Handle("/", httpapi.Handler(log, func(http.ResponseWriter, *http.Request) error {
		return httpapi.Errorf(httpapi.NotFound, "no such operation")
	}))
	return mux
}
