package analyses

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Route is one implemented operation of openapi.yaml.
type Route struct {
	Method, Path, OperationID string
	Roles                     []tenant.Role // x-required-roles
}

// Routes are the analysis operations this package serves. A contract test
// compares them with planning/api-operations.json; their roles are the ones
// the service requires.
var Routes = []Route{
	{"POST", "/v1/simulations/{id}/analyses", "createAnalysis", writeRoles},
	{"GET", "/v1/analyses/{id}", "getAnalysis", readRoles},
}

// Register adds the analysis routes to mux.
func Register(mux *http.ServeMux, log *slog.Logger, v auth.Verifier, users httpapi.Resolver, s *Service) {
	h := handlers{s: s}
	for _, r := range Routes {
		f := h.get
		if r.OperationID == "createAnalysis" {
			f = h.create
		}
		mux.Handle(r.Method+" "+r.Path, httpapi.Handler(log, httpapi.Scoped(v, users, f)))
	}
}

type handlers struct{ s *Service }

var (
	runIDs      = regexp.MustCompile(`^sim_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	analysisIDs = regexp.MustCompile(`^ana_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
)

// pathID is the {id} of the request if it is an ID of kind, else 400.
func pathID(r *http.Request, ids *regexp.Regexp, kind string) (string, error) {
	id := r.PathValue("id")
	if !ids.MatchString(id) {
		return "", &httpapi.Error{Code: httpapi.InvalidRequest, Message: "id must be a " + kind + " ID",
			Details: []httpapi.Detail{{Path: "", Reason: "id"}}}
	}
	return id, nil
}

// maxRequestBytes bounds an analysis request: a question of 10,000
// characters (up to 4 bytes each), a run ID and the language.
const maxRequestBytes = 64 << 10

// Refused maps the service's errors to what the client sees (openapi error
// codes); tenant and transport errors pass to httpapi.
func Refused(err error) error {
	var re *RequestError
	var run *RunError
	var ue *proposals.UnavailableError
	switch {
	case errors.As(err, &re):
		details := make([]httpapi.Detail, len(re.Issues))
		for i, is := range re.Issues {
			details[i] = httpapi.Detail{Path: is.Path, Reason: is.Message, Code: is.Code, Severity: string(is.Severity)}
		}
		return &httpapi.Error{Code: httpapi.RequestValidationFailed, Message: "the analysis request is not acceptable", Details: details}
	case errors.As(err, &ue):
		return proposals.Refused(err)
	case errors.As(err, &run) && errors.Is(run.Err, ErrRunNotFound):
		return &httpapi.Error{Code: httpapi.NotFound, Message: "not found", Details: []httpapi.Detail{{Path: "", Reason: run.Param}}}
	case errors.As(err, &run) && errors.Is(run.Err, ErrNotReady):
		return &httpapi.Error{Code: httpapi.ResultNotReady, Message: "the run has no result yet; analyze completed runs only",
			Details: []httpapi.Detail{{Path: "", Reason: run.Param}}}
	case errors.Is(err, ErrNotFound):
		return httpapi.Errorf(httpapi.NotFound, "not found")
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		return httpapi.Errorf(httpapi.IdempotencyKeyConflict, "this Idempotency-Key was used for a different request")
	}
	return err
}

func (h handlers) create(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	runID, err := pathID(r, runIDs, "simulation run")
	if err != nil {
		return err
	}
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var req CreateRequest
	body, err := httpapi.DecodeJSON(r, maxRequestBytes, "http.schema.json#/$defs/AnalysisRequest", &req)
	if err != nil {
		return err
	}
	status, out, err := h.s.Create(r.Context(), sc, httpapi.RequestID(r.Context()), key, runID, body, req)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteBody(w, status, out)
	return nil
}

func (h handlers) get(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, analysisIDs, "analysis")
	if err != nil {
		return err
	}
	a, err := h.s.Get(r.Context(), sc, httpapi.RequestID(r.Context()), id)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteJSON(w, 200, a)
	return nil
}
