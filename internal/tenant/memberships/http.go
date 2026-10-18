package memberships

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

// Routes are the membership operations this package serves. A contract
// test compares them with planning/api-operations.json; their roles are
// the ones the service requires.
var Routes = []Route{
	{"GET", "/v1/organizations/{organization_id}/memberships", "listMemberships", manageRoles},
	{"POST", "/v1/organizations/{organization_id}/memberships", "createMembership", manageRoles},
	{"PATCH", "/v1/organizations/{organization_id}/memberships/{user_id}", "updateMembership", manageRoles},
	{"DELETE", "/v1/organizations/{organization_id}/memberships/{user_id}", "deleteMembership", manageRoles},
}

// Register adds the membership routes to mux.
func Register(mux *http.ServeMux, log *slog.Logger, v auth.Verifier, users httpapi.Resolver, s *Service) {
	h := handlers{s: s}
	for _, r := range Routes {
		var f func(http.ResponseWriter, *http.Request, tenant.Scope) error
		switch r.OperationID {
		case "listMemberships":
			f = h.list
		case "createMembership":
			f = h.create
		case "updateMembership":
			f = h.update
		case "deleteMembership":
			f = h.delete
		}
		mux.Handle(r.Method+" "+r.Path, httpapi.Handler(log, httpapi.Scoped(v, users, inScope(f))))
	}
}

// inScope requires the path's organization to be the one the
// X-Organization-ID header selected (API_DATABASE §1); another one is not
// found, as an organization the caller is not a member of would be.
func inScope(next func(http.ResponseWriter, *http.Request, tenant.Scope) error) func(http.ResponseWriter, *http.Request, tenant.Scope) error {
	return func(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
		if r.PathValue("organization_id") != sc.OrganizationID {
			return httpapi.Errorf(httpapi.NotFound, "not found")
		}
		return next(w, r, sc)
	}
}

type handlers struct{ s *Service }

func (h handlers) list(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	cursor, limit, err := httpapi.PageQuery(r)
	if err != nil {
		return err
	}
	page, err := h.s.List(r.Context(), sc, cursor, limit)
	if err != nil {
		return problem(err)
	}
	httpapi.WriteJSON(w, 200, page)
	return nil
}

func (h handlers) create(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var req CreateRequest
	body, err := httpapi.DecodeJSON(r, httpapi.MaxBodyBytes, "http.schema.json#/$defs/MembershipWrite", &req)
	if err != nil {
		return err
	}
	status, out, err := h.s.Create(r.Context(), sc, httpapi.RequestID(r.Context()), key, body, req)
	if err != nil {
		return problem(err)
	}
	httpapi.WriteBody(w, status, out)
	return nil
}

func (h handlers) update(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	user, err := pathUser(r)
	if err != nil {
		return err
	}
	var req UpdateRequest
	if _, err := httpapi.DecodeJSON(r, httpapi.MaxBodyBytes, "http.schema.json#/$defs/MembershipUpdate", &req); err != nil {
		return err
	}
	m, err := h.s.Update(r.Context(), sc, httpapi.RequestID(r.Context()), user, req)
	if err != nil {
		return problem(err)
	}
	httpapi.WriteJSON(w, 200, m)
	return nil
}

func (h handlers) delete(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	user, err := pathUser(r)
	if err != nil {
		return err
	}
	if err := h.s.Delete(r.Context(), sc, httpapi.RequestID(r.Context()), user); err != nil {
		return problem(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func pathUser(r *http.Request) (string, error) {
	user := r.PathValue("user_id")
	if !ValidUserID(user) {
		return "", &httpapi.Error{Code: httpapi.InvalidRequest, Message: "user_id must be a user ID",
			Details: []httpapi.Detail{{Path: "", Reason: "user_id"}}}
	}
	return user, nil
}

// problem maps the service's errors; others (tenant, storage) are mapped by
// httpapi.
func problem(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpapi.Errorf(httpapi.NotFound, "not found")
	case errors.Is(err, ErrExists):
		return httpapi.Errorf(httpapi.LifecycleConflict, "the user is already a member")
	case errors.Is(err, ErrLastOwner):
		return httpapi.Errorf(httpapi.LifecycleConflict, "the organization must keep at least one owner")
	case errors.Is(err, ErrUnknownUser):
		return &httpapi.Error{Code: httpapi.RequestValidationFailed, Message: "no user has this ID",
			Details: []httpapi.Detail{{Path: "/user_id", Reason: "no user has this ID"}}}
	case errors.Is(err, ErrInvalidCursor):
		return httpapi.InvalidCursor()
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		return httpapi.Errorf(httpapi.IdempotencyKeyConflict, "this Idempotency-Key was used for a different request")
	}
	return err
}
