// Package api assembles the API's routes into one http.Handler (the same
// handler cmd/api serves and the integration tests exercise). It wires
// packages together and holds no business rules.
package api

import (
	"log/slog"
	"net/http"
	"path"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/project"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/memberships"
)

// Handler serves every implemented /v1 operation. Any other path or method
// is 404 NOT_FOUND in the error envelope, including what ServeMux would
// answer differently (T-009 review L2): a path that is not in clean form
// (a redirect) and HEAD (GET's handler). No operation declares either.
// "OPTIONS *" never gets here: net/http answers it with an empty 200.
func Handler(log *slog.Logger, v auth.Verifier, db *tenant.DB) http.Handler {
	mux := http.NewServeMux()
	project.Register(mux, log, v, db, &project.Service{DB: db})
	memberships.Register(mux, log, v, db, &memberships.Service{DB: db})
	notFound := httpapi.Handler(log, func(http.ResponseWriter, *http.Request) error {
		return httpapi.Errorf(httpapi.NotFound, "no such operation")
	})
	mux.Handle("/", notFound)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.URL.Path != path.Clean(r.URL.Path) {
			notFound.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
