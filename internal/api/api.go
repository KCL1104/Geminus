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
	"github.com/kcl1104/geminus/internal/models"
	"github.com/kcl1104/geminus/internal/packs"
	"github.com/kcl1104/geminus/internal/project"
	"github.com/kcl1104/geminus/internal/runs"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/me"
	"github.com/kcl1104/geminus/internal/tenant/memberships"
)

// Option configures Handler.
type Option func(*options)

type options struct {
	objects objects.Store
	limits  simulation.ExecutionLimits
	stream  runs.Stream
}

// WithObjects gives the API private object storage (D-044): dataset
// snapshots for runs and the runs' trace artifacts. Without it a run that
// needs either fails closed with 503.
func WithObjects(s objects.Store) Option { return func(o *options) { o.objects = s } }

// WithRunLimits sets the execution limits new runs freeze (D-007's when
// unset; tests lower them).
func WithRunLimits(l simulation.ExecutionLimits) Option { return func(o *options) { o.limits = l } }

// WithStream sets how the progress stream polls and ends (tests shorten it).
func WithStream(s runs.Stream) Option { return func(o *options) { o.stream = s } }

// Domains lists the domains Handler serves (internal/packs, the registry
// the worker builds as well).
func Domains() ([]string, error) {
	served, err := packs.Registry()
	if err != nil {
		return nil, err
	}
	return served.Domains(), nil
}

// Handler serves every implemented /v1 operation. Any other path or method
// is 404 NOT_FOUND in the error envelope, including what ServeMux would
// answer differently (T-009 review L2): a path that is not in clean form
// (a redirect) and HEAD (GET's handler). No operation declares either.
// "OPTIONS *" never gets here: net/http answers it with an empty 200.
// The domain packs are internal/packs' (the worker's as well). Publishing
// a version that binds datasets still fails closed with 503 (the model
// service has no dataset store until uploads exist, T-016); runs read
// dataset snapshots and store traces through the object storage given.
func Handler(log *slog.Logger, v auth.Verifier, db *tenant.DB, opts ...Option) (http.Handler, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	served, err := packs.Registry()
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	project.Register(mux, log, v, db, &project.Service{DB: db})
	memberships.Register(mux, log, v, db, &memberships.Service{DB: db})
	me.Register(mux, log, v, db, &me.Service{DB: db})
	models.Register(mux, log, v, db, &models.Service{DB: db, Packs: served})
	runs.Register(mux, log, v, db, &runs.Service{DB: db, Packs: served, Objects: o.objects, Limits: o.limits}, o.stream)
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
	}), nil
}
