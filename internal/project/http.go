package project

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Route is one implemented operation of openapi.yaml.
type Route struct {
	Method, Path, OperationID string
	Roles                     []tenant.Role // x-required-roles
}

// Routes are the project operations this package serves. A contract test
// compares them with planning/api-operations.json; their roles are the
// ones the service requires (createRoles, readRoles).
var Routes = []Route{
	{"POST", "/v1/projects", "createProject", createRoles},
	{"GET", "/v1/projects", "listProjects", readRoles},
	{"GET", "/v1/projects/{id}", "getProject", readRoles},
}

// Register adds the project routes to mux.
func Register(mux *http.ServeMux, log *slog.Logger, v auth.Verifier, users httpapi.Resolver, s *Service) {
	h := handlers{s: s}
	for _, r := range Routes {
		var f func(http.ResponseWriter, *http.Request, tenant.Scope) error
		switch r.OperationID {
		case "createProject":
			f = h.create
		case "listProjects":
			f = h.list
		case "getProject":
			f = h.get
		}
		mux.Handle(r.Method+" "+r.Path, httpapi.Handler(log, httpapi.Scoped(v, users, f)))
	}
}

type handlers struct{ s *Service }

func (h handlers) create(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var req CreateRequest
	body, err := httpapi.DecodeJSON(r, httpapi.MaxBodyBytes, "http.schema.json#/$defs/CreateProject", &req)
	if err != nil {
		return err
	}
	status, out, err := h.s.Create(r.Context(), sc, httpapi.RequestID(r.Context()), key, body, req)
	if errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {
		return httpapi.Errorf(httpapi.IdempotencyKeyConflict, "this Idempotency-Key was used for a different request")
	}
	if err != nil {
		return err
	}
	httpapi.WriteBody(w, status, out)
	return nil
}

func (h handlers) list(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	cursor, limit, err := httpapi.PageQuery(r)
	if err != nil {
		return err
	}
	page, err := h.s.List(r.Context(), sc, cursor, limit)
	if errors.Is(err, ErrInvalidCursor) {
		return httpapi.InvalidCursor()
	}
	if err != nil {
		return err
	}
	httpapi.WriteJSON(w, 200, page)
	return nil
}

func (h handlers) get(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id := r.PathValue("id")
	if !ValidID(id) {
		return &httpapi.Error{Code: httpapi.InvalidRequest, Message: "id must be a project ID",
			Details: []httpapi.Detail{{Path: "", Reason: "id"}}}
	}
	p, err := h.s.Get(r.Context(), sc, id)
	if errors.Is(err, ErrNotFound) {
		return httpapi.Errorf(httpapi.NotFound, "not found")
	}
	if err != nil {
		return err
	}
	httpapi.WriteJSON(w, 200, p)
	return nil
}
