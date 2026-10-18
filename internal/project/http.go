package project

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

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
	key, err := idempotencyKey(r)
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

// idempotencyKey reads the Idempotency-Key header (openapi IdempotencyKey:
// 8–128 characters; visible ASCII here, so it stores and logs unambiguously).
func idempotencyKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) == 0 {
		return "", httpapi.Errorf(httpapi.IdempotencyKeyRequired, "an Idempotency-Key header is required")
	}
	k := values[0]
	if len(values) > 1 || len(k) < 8 || len(k) > 128 || strings.ContainsFunc(k, func(c rune) bool { return c < 0x21 || c > 0x7e }) {
		return "", &httpapi.Error{Code: httpapi.InvalidRequest, Message: "Idempotency-Key must be one value of 8–128 visible ASCII characters",
			Details: []httpapi.Detail{{Path: "", Reason: "Idempotency-Key"}}}
	}
	return k, nil
}

func (h handlers) list(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	q, err := httpapi.Query(r)
	if err != nil {
		return err
	}
	limit := 50
	if vs, ok := q["limit"]; ok {
		n, err := strconv.Atoi(vs[0])
		if len(vs) != 1 || err != nil || n < 1 || n > 100 || strconv.Itoa(n) != vs[0] {
			return &httpapi.Error{Code: httpapi.InvalidRequest, Message: "limit must be one integer from 1 to 100",
				Details: []httpapi.Detail{{Path: "", Reason: "limit"}}}
		}
		limit = n
	}
	cursor := ""
	if vs, ok := q["cursor"]; ok {
		if len(vs) != 1 || vs[0] == "" || len(vs[0]) > 2048 {
			return invalidCursor()
		}
		cursor = vs[0]
	}
	page, err := h.s.List(r.Context(), sc, cursor, limit)
	if errors.Is(err, ErrInvalidCursor) {
		return invalidCursor()
	}
	if err != nil {
		return err
	}
	httpapi.WriteJSON(w, 200, page)
	return nil
}

func invalidCursor() error {
	return &httpapi.Error{Code: httpapi.InvalidRequest, Message: "the cursor is not valid for this list",
		Details: []httpapi.Detail{{Path: "", Reason: "cursor"}}}
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
