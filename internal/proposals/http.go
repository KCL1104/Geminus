package proposals

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Route is one implemented operation of openapi.yaml.
type Route struct {
	Method, Path, OperationID string
	Roles                     []tenant.Role // x-required-roles
}

// Routes are the proposal operations this package serves. A contract test
// compares them with planning/api-operations.json; their roles are the ones
// the service requires. approveProposal and rejectProposal are
// internal/proposals/approval's (T-026).
var Routes = []Route{
	{"POST", "/v1/models/{id}/ai-proposals", "createProposal", writeRoles},
	{"GET", "/v1/models/{id}/ai-proposals", "listProposals", readRoles},
	{"GET", "/v1/ai-proposals/{id}", "getProposal", readRoles},
	// T-056: an edit proposal asked for with a photo (image_http.go).
	{"POST", "/v1/models/{id}/ai-proposals/image", "createImageProposal", writeRoles},
	// H-052: the photo itself, for the proposal page (image_read.go).
	{"GET", "/v1/ai-proposals/{id}/image", "getProposalImage", readRoles},
}

// Register adds the proposal routes to mux.
func Register(mux *http.ServeMux, log *slog.Logger, v auth.Verifier, users httpapi.Resolver, s *Service) {
	h := handlers{s: s}
	for _, r := range Routes {
		var f func(http.ResponseWriter, *http.Request, tenant.Scope) error
		switch r.OperationID {
		case "createProposal":
			f = h.create
		case "listProposals":
			f = h.list
		case "getProposal":
			f = h.get
		case "createImageProposal":
			f = h.createImage
		case "getProposalImage":
			f = h.image
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

// maxRequestBytes bounds a proposal request: an intent of 20,000
// characters (up to 4 bytes each) and 500 selected IDs of up to 128.
const maxRequestBytes = 256 << 10

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
	if he := refusedImage(err); he != nil {
		return he
	}
	var re *RequestError
	var ue *UnavailableError
	switch {
	case errors.As(err, &re):
		return &httpapi.Error{Code: httpapi.RequestValidationFailed, Message: "the proposal request is not acceptable", Details: details(re.Issues)}
	case errors.As(err, &ue) && ue.Model:
		// A model this deployment's approval does not list (H-049): the
		// code the worker would fail the call with, never another model.
		return &httpapi.Error{Code: httpapi.AIPolicyNotApproved, Message: "this model is not approved for proposals in this deployment; ask with its default model",
			Details: []httpapi.Detail{{Path: "/model", Reason: "this deployment's approval does not list the model for proposals", Code: CodeModelNotApproved}}}
	case errors.As(err, &ue) && ue.Research:
		// Web research is off for this deployment's provider (H-046 (2)):
		// the proposals' code for its availability, and the code the worker
		// would fail the call with.
		code, message := httpapi.AIProviderNotConfigured, "web research is not available with this deployment's AI provider; ask without research"
		if ue.Availability == PolicyNotApproved {
			code, message = httpapi.AIPolicyNotApproved, "web research is off until the owner approves it for this deployment's AI provider; ask without research"
		}
		return &httpapi.Error{Code: code, Message: message, Details: []httpapi.Detail{{Path: "/research",
			Reason: "this deployment's provider may not search the web for the request", Code: CodeResearchUnsupported}}}
	case errors.As(err, &ue):
		if ue.Availability == PolicyNotApproved {
			return httpapi.Errorf(httpapi.AIPolicyNotApproved, "AI assistance is off until the owner approves its model, quota and data policy")
		}
		return httpapi.Errorf(httpapi.AIProviderNotConfigured, "AI assistance is not configured in this deployment")
	case errors.Is(err, ErrNotFound):
		return httpapi.Errorf(httpapi.NotFound, "not found")
	case errors.Is(err, ErrStaleBase):
		return &httpapi.Error{Code: httpapi.ModelVersionConflict, Message: "base_version_id is not the model's latest version",
			Details: []httpapi.Detail{{Path: "/base_version_id", Reason: "not the latest version"}}}
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
	modelID, err := pathID(r, modelIDs, "model")
	if err != nil {
		return err
	}
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var req CreateRequest
	body, err := httpapi.DecodeJSON(r, maxRequestBytes, "http.schema.json#/$defs/ProposalRequest", &req)
	if err != nil {
		return err
	}
	status, out, err := h.s.Create(r.Context(), sc, httpapi.RequestID(r.Context()), key, modelID, body, req)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteBody(w, status, out)
	return nil
}

func (h handlers) list(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	modelID, err := pathID(r, modelIDs, "model")
	if err != nil {
		return err
	}
	cursor, limit, err := httpapi.PageQuery(r)
	if err != nil {
		return err
	}
	page, err := h.s.List(r.Context(), sc, httpapi.RequestID(r.Context()), modelID, cursor, limit)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteJSON(w, 200, page)
	return nil
}

func (h handlers) get(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, proposalIDs, "proposal")
	if err != nil {
		return err
	}
	p, err := h.s.Get(r.Context(), sc, httpapi.RequestID(r.Context()), id)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteJSON(w, 200, p)
	return nil
}
