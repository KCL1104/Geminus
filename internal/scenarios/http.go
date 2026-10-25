package scenarios

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Route is one implemented operation of openapi.yaml.
type Route struct {
	Method, Path, OperationID string
	Roles                     []tenant.Role // x-required-roles
}

// Routes are the scenario operations this package serves. A contract test
// compares them with planning/api-operations.json; their roles are the
// ones the service requires.
var Routes = []Route{
	{"POST", "/v1/model-versions/{id}/scenarios", "createManualScenario", writeRoles},
	{"GET", "/v1/model-versions/{id}/scenarios", "listScenarios", readRoles},
	{"GET", "/v1/scenarios/{id}", "getScenario", readRoles},
}

// Register adds the scenario routes to mux.
func Register(mux *http.ServeMux, log *slog.Logger, v auth.Verifier, users httpapi.Resolver, s *Service) {
	h := handlers{s: s, log: log}
	for _, r := range Routes {
		var f func(http.ResponseWriter, *http.Request, tenant.Scope) error
		switch r.OperationID {
		case "createManualScenario":
			f = h.create
		case "listScenarios":
			f = h.list
		case "getScenario":
			f = h.get
		}
		mux.Handle(r.Method+" "+r.Path, httpapi.Handler(log, httpapi.Scoped(v, users, f)))
	}
}

type handlers struct {
	s   *Service
	log *slog.Logger
}

var versionIDs = regexp.MustCompile(`^mv_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// pathID is the {id} of the request if it is an ID of kind, else 400.
func pathID(r *http.Request, ids *regexp.Regexp, kind string) (string, error) {
	id := r.PathValue("id")
	if !ids.MatchString(id) {
		return "", &httpapi.Error{Code: httpapi.InvalidRequest, Message: "id must be a " + kind + " ID",
			Details: []httpapi.Detail{{Path: "", Reason: "id"}}}
	}
	return id, nil
}

// maxRequestBytes bounds a create: the patch may be as large as the IR it
// edits (D-029), plus the name; createSimulation's bound.
const maxRequestBytes = validate.MaxIRBytes + 64<<10

func details(issues []model.ValidationIssue) []httpapi.Detail {
	out := make([]httpapi.Detail, len(issues))
	for i, is := range issues {
		out[i] = httpapi.Detail{Path: is.Path, Reason: is.Message, Code: is.Code, Severity: string(is.Severity)}
	}
	return out
}

// Refused maps the service's errors to what the client sees (openapi error
// codes, with JSON Pointers into the request); tenant and transport errors
// pass to httpapi, and anything else (ErrCorrupt among them) is
// INTERNAL_ERROR there.
func Refused(err error) error {
	var re *RequestError
	var ie *InvalidError
	var storage *DatasetStorageError
	switch {
	case errors.As(err, &re):
		return &httpapi.Error{Code: httpapi.RequestValidationFailed, Message: "the scenario request is not acceptable", Details: details(re.Issues)}
	case errors.As(err, &ie):
		return &httpapi.Error{Code: httpapi.ModelValidationFailed,
			Message: "the scenario's patch does not apply to its base or the result is not valid; nothing was saved", Details: details(ie.Issues)}
	case errors.Is(err, ErrNotFound):
		return httpapi.Errorf(httpapi.NotFound, "not found")
	case errors.Is(err, ErrStorageUnavailable):
		return httpapi.Errorf(httpapi.ServiceUnavailable, "the version binds datasets and dataset storage is not available")
	case errors.As(err, &storage):
		// The cause is logged by the handler, never sent.
		return &httpapi.Error{Code: httpapi.ServiceUnavailable, Message: "the dataset snapshots could not be read; nothing was saved (retry later)",
			Details: []httpapi.Detail{{Path: "", Reason: "the dataset storage failed", Code: "DATASET_STORAGE_FAILED"}}}
	case errors.Is(err, ErrInvalidCursor):
		return httpapi.InvalidCursor()
	case errors.Is(err, ErrInvalidLimit):
		return &httpapi.Error{Code: httpapi.InvalidRequest, Message: "limit must be one integer from 1 to 100",
			Details: []httpapi.Detail{{Path: "", Reason: "limit"}}}
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		return httpapi.Errorf(httpapi.IdempotencyKeyConflict, "this Idempotency-Key was used for a different request")
	}
	return err
}

func (h handlers) create(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	versionID, err := pathID(r, versionIDs, "model version")
	if err != nil {
		return err
	}
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var req CreateRequest
	body, err := httpapi.DecodeJSON(r, maxRequestBytes, "http.schema.json#/$defs/CreateScenario", &req)
	if err != nil {
		return err
	}
	status, out, err := h.s.Create(r.Context(), sc, httpapi.RequestID(r.Context()), key, versionID, body, req)
	if err != nil {
		var storage *DatasetStorageError
		if errors.As(err, &storage) {
			h.log.WarnContext(r.Context(), "reading a scenario's dataset snapshots failed", "request_id", httpapi.RequestID(r.Context()),
				"organization_id", sc.OrganizationID, "model_version_id", versionID, "error", storage.Err.Error())
		}
		return Refused(err)
	}
	httpapi.WriteBody(w, status, out)
	return nil
}

func (h handlers) list(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	versionID, err := pathID(r, versionIDs, "model version")
	if err != nil {
		return err
	}
	cursor, limit, err := httpapi.PageQuery(r)
	if err != nil {
		return err
	}
	page, err := h.s.List(r.Context(), sc, versionID, cursor, limit)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteJSON(w, 200, page)
	return nil
}

func (h handlers) get(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, scenarioIDs, "scenario")
	if err != nil {
		return err
	}
	s, err := h.s.Get(r.Context(), sc, id)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteJSON(w, 200, s)
	return nil
}
