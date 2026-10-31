package investigations

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Route is one implemented operation of openapi.yaml.
type Route struct {
	Method, Path, OperationID string
	Roles                     []tenant.Role // x-required-roles
}

// Routes are the investigation operations this package serves. A contract
// test compares them with planning/api-operations.json; their roles are the
// ones the service requires (the stop's requester-or-owner rule is the
// service's).
var Routes = []Route{
	{"POST", "/v1/models/{id}/investigations", "createInvestigation", writeRoles},
	{"GET", "/v1/models/{id}/investigations", "listInvestigations", readRoles},
	{"GET", "/v1/investigations/{id}", "getInvestigation", readRoles},
	{"POST", "/v1/investigations/{id}/stop", "stopInvestigation", writeRoles},
}

// Register adds the investigation routes to mux.
func Register(mux *http.ServeMux, log *slog.Logger, v auth.Verifier, users httpapi.Resolver, s *Service) {
	h := handlers{s: s}
	for _, r := range Routes {
		var f func(http.ResponseWriter, *http.Request, tenant.Scope) error
		switch r.OperationID {
		case "createInvestigation":
			f = h.create
		case "listInvestigations":
			f = h.list
		case "getInvestigation":
			f = h.get
		case "stopInvestigation":
			f = h.stop
		}
		mux.Handle(r.Method+" "+r.Path, httpapi.Handler(log, httpapi.Scoped(v, users, f)))
	}
}

type handlers struct{ s *Service }

var modelIDs = regexp.MustCompile(`^mdl_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// pathID is the {id} of the request if it is an ID of kind, else 400.
func pathID(r *http.Request, ids *regexp.Regexp, kind string) (string, error) {
	id := r.PathValue("id")
	if !ids.MatchString(id) {
		return "", &httpapi.Error{Code: httpapi.InvalidRequest, Message: "id must be a " + kind + " ID",
			Details: []httpapi.Detail{{Path: "", Reason: "id"}}}
	}
	return id, nil
}

// maxRequestBytes bounds an investigation request: an intent of 20,000
// characters (up to 4 bytes each), 500 selected IDs, the run settings.
const maxRequestBytes = 256 << 10

// maxStopBytes bounds a stop's body, the empty object.
const maxStopBytes = 1 << 10

func details(issues []model.ValidationIssue) []httpapi.Detail {
	out := make([]httpapi.Detail, len(issues))
	for i, is := range issues {
		out[i] = httpapi.Detail{Path: is.Path, Reason: is.Message, Code: is.Code, Severity: string(is.Severity)}
	}
	return out
}

// Refused maps the service's errors to what the client sees (openapi error
// codes); tenant and transport errors pass to httpapi.
func Refused(err error) error {
	var re *RequestError
	var na *NotApprovedError
	var ue *proposals.UnavailableError
	var dl *DailyLimitError
	var sb *StaleBaseError
	switch {
	case errors.Is(err, ErrOff):
		return &httpapi.Error{Code: httpapi.ServiceUnavailable, Message: "AI investigations are not available here",
			Details: []httpapi.Detail{{Path: "", Reason: "investigations are off in this deployment", Code: CodeOff}}}
	case errors.As(err, &re):
		message := "the investigation request is not acceptable"
		for _, is := range re.Issues {
			switch is.Code {
			case CodePlannerNotOffered:
				message = "this planner is not offered in this deployment"
			case CodeResearchOverCeiling:
				message = "research with this model cannot fit the investigation's spending limit"
			}
		}
		return &httpapi.Error{Code: httpapi.RequestValidationFailed, Message: message, Details: details(re.Issues)}
	case errors.As(err, &na) && na.Planner:
		return &httpapi.Error{Code: httpapi.AIPolicyNotApproved, Message: "this planner model is not approved in this deployment",
			Details: []httpapi.Detail{{Path: "/planner_model", Reason: "this deployment's approval does not list the model for the investigator role",
				Code: CodePlannerModelNotApprov}}}
	case errors.As(err, &na):
		return &httpapi.Error{Code: httpapi.AIPolicyNotApproved, Message: "AI investigations are not approved in this deployment",
			Details: []httpapi.Detail{{Path: "", Reason: "this deployment's approval does not list the investigator role", Code: CodeRoleNotApproved}}}
	case errors.As(err, &ue):
		return proposals.Refused(err)
	case errors.As(err, &dl):
		return &httpapi.Error{Code: httpapi.RateLimited, Message: "this organization has started 10 AI investigations today; try again tomorrow (UTC)",
			Details: []httpapi.Detail{{Path: "", Reason: "at most 10 per organization per UTC day", Code: CodeDailyLimit}}}
	case errors.Is(err, ErrNotFound):
		return httpapi.Errorf(httpapi.NotFound, "not found")
	case errors.As(err, &sb):
		return &httpapi.Error{Code: httpapi.ModelVersionConflict, Message: "the model has a newer version; start from the latest",
			Details: []httpapi.Detail{{Path: sb.Path, Reason: "not the model's latest version"}}}
	case errors.Is(err, ErrRunning):
		return &httpapi.Error{Code: httpapi.LifecycleConflict, Message: "you already have an AI investigation running; stop it or wait for it to end",
			Details: []httpapi.Detail{{Path: "", Reason: "one running investigation per person per organization", Code: CodeRunning}}}
	case errors.Is(err, ErrEnded):
		return httpapi.Errorf(httpapi.LifecycleConflict, "this investigation has already ended; nothing was changed")
	case errors.Is(err, ErrNotTheRequester):
		return httpapi.Errorf(httpapi.Forbidden, "only the person who started it, or an owner, can stop it")
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

// off answers ErrOff before anything of the request is read: an operation
// of a deployment or an organization without investigations changes
// nothing and reads nothing.
func (h handlers) off(sc tenant.Scope) error {
	if !h.s.serves(sc.OrganizationID) {
		return Refused(ErrOff)
	}
	return nil
}

func (h handlers) create(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	if err := h.off(sc); err != nil {
		return err
	}
	modelID, err := pathID(r, modelIDs, "model")
	if err != nil {
		return err
	}
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var req CreateRequest
	body, err := httpapi.DecodeJSON(r, maxRequestBytes, "investigation.schema.json#/$defs/InvestigationRequest", &req)
	if err != nil {
		return err
	}
	status, out, err := h.s.Create(r.Context(), sc, httpapi.RequestID(r.Context()), key, modelID, body, req)
	var dl *DailyLimitError
	if errors.As(err, &dl) {
		w.Header().Set("Retry-After", strconv.Itoa(dl.RetryAfter))
	}
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteBody(w, status, out)
	return nil
}

func (h handlers) list(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	if err := h.off(sc); err != nil {
		return err
	}
	modelID, err := pathID(r, modelIDs, "model")
	if err != nil {
		return err
	}
	cursor, limit, err := httpapi.PageQuery(r)
	if err != nil {
		return err
	}
	page, err := h.s.List(r.Context(), sc, modelID, cursor, limit)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteJSON(w, 200, page)
	return nil
}

func (h handlers) get(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	if err := h.off(sc); err != nil {
		return err
	}
	id, err := pathID(r, investigationIDs, "investigation")
	if err != nil {
		return err
	}
	v, err := h.s.Get(r.Context(), sc, id)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteJSON(w, 200, v)
	return nil
}

func (h handlers) stop(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	if err := h.off(sc); err != nil {
		return err
	}
	id, err := pathID(r, investigationIDs, "investigation")
	if err != nil {
		return err
	}
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var req struct{}
	body, err := httpapi.DecodeJSON(r, maxStopBytes, "investigation.schema.json#/$defs/StopInvestigation", &req)
	if err != nil {
		return err
	}
	status, out, err := h.s.Stop(r.Context(), sc, httpapi.RequestID(r.Context()), key, id, body)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteBody(w, status, out)
	return nil
}
