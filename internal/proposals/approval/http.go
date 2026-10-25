package approval

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"

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

// Routes are the decision operations this package serves. A contract test
// compares them with planning/api-operations.json; their roles are the
// ones the service requires.
var Routes = []Route{
	{"POST", "/v1/ai-proposals/{id}/approve", "approveProposal", writeRoles},
	{"POST", "/v1/ai-proposals/{id}/reject", "rejectProposal", writeRoles},
}

// Register adds the decision routes to mux.
func Register(mux *http.ServeMux, log *slog.Logger, v auth.Verifier, users httpapi.Resolver, s *Service) {
	h := handlers{s: s, log: log}
	for _, r := range Routes {
		var f func(http.ResponseWriter, *http.Request, tenant.Scope) error
		switch r.OperationID {
		case "approveProposal":
			f = h.approve
		case "rejectProposal":
			f = h.reject
		}
		mux.Handle(r.Method+" "+r.Path, httpapi.Handler(log, httpapi.Scoped(v, users, f)))
	}
}

type handlers struct {
	s   *Service
	log *slog.Logger
}

var proposalIDs = regexp.MustCompile(`^aip_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// pathID is the proposal ID of the path, else 400.
func pathID(r *http.Request) (string, error) {
	id := r.PathValue("id")
	if !proposalIDs.MatchString(id) {
		return "", &httpapi.Error{Code: httpapi.InvalidRequest, Message: "id must be a proposal ID",
			Details: []httpapi.Detail{{Path: "", Reason: "id"}}}
	}
	return id, nil
}

// Refused maps the service's errors to what the client sees (openapi error
// codes, with JSON Pointers into the request); tenant and transport errors
// pass to httpapi, and anything else is INTERNAL_ERROR there.
func Refused(err error) error {
	var decided *DecidedError
	var state *StateError
	var risk *RiskChangedError
	var invalid *InvalidError
	_, storage := storageCause(err)
	switch {
	case errors.Is(err, ErrNotFound):
		return httpapi.Errorf(httpapi.NotFound, "not found")
	case errors.As(err, &decided):
		return &httpapi.Error{Code: httpapi.ProposalAlreadyDecided, Message: "the proposal is already " + string(decided.Status) + "; nothing was changed",
			Details: []httpapi.Detail{{Path: "", Reason: "the proposal is " + string(decided.Status)}}}
	case errors.As(err, &state):
		message := "only a ready proposal can be approved; nothing was changed"
		if state.Decision == "reject" {
			message = "only a ready or needs_input proposal can be rejected; nothing was changed"
		}
		return &httpapi.Error{Code: httpapi.LifecycleConflict, Message: message,
			Details: []httpapi.Detail{{Path: "", Reason: "the proposal is " + string(state.Status)}}}
	case errors.Is(err, ErrOtherBase):
		return &httpapi.Error{Code: httpapi.ModelVersionConflict, Message: "expected_base_version_id is not this proposal's base; nothing was changed",
			Details: []httpapi.Detail{{Path: "/expected_base_version_id", Reason: "not this proposal's base"}}}
	case errors.As(err, &risk):
		return &httpapi.Error{Code: httpapi.LifecycleConflict,
			Message: "the server's risk of this proposal is not the one it was reviewed with; nothing was changed (ask for a new proposal)",
			Details: []httpapi.Detail{{Path: risk.Path, Reason: fmt.Sprintf("the server now computes %s, not %s", risk.Now, risk.Reviewed)}}}
	case errors.As(err, &invalid):
		e := &httpapi.Error{Code: httpapi.ModelValidationFailed, Message: "the proposal does not pass the server's review again; nothing was changed"}
		for _, is := range invalid.Issues {
			e.Details = append(e.Details, httpapi.Detail{Path: is.Path, Reason: is.Message, Code: is.Code, Severity: string(is.Severity)})
		}
		return e
	case errors.Is(err, ErrInvalidComment):
		return &httpapi.Error{Code: httpapi.RequestValidationFailed, Message: "the request body does not match the schema",
			Details: []httpapi.Detail{{Path: "/comment", Reason: "at most 2000 characters, without U+0000", Severity: "error"}}}
	case errors.Is(err, ErrDatasetsUnavailable):
		return httpapi.Errorf(httpapi.ServiceUnavailable, "the proposal's base binds datasets and dataset storage is not available")
	case storage:
		// The revalidation's or the draft sync's; the cause is logged by
		// the handler, never sent.
		return &httpapi.Error{Code: httpapi.ServiceUnavailable, Message: "the dataset snapshots could not be read; nothing was changed (retry later)",
			Details: []httpapi.Detail{{Path: "", Reason: "the dataset storage failed", Code: "DATASET_STORAGE_FAILED"}}}
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		return httpapi.Errorf(httpapi.IdempotencyKeyConflict, "this Idempotency-Key was used for a different request")
	}
	return err
}

func (h handlers) approve(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var req ApproveRequest
	body, err := httpapi.DecodeJSON(r, httpapi.MaxBodyBytes, "http.schema.json#/$defs/Approve", &req)
	if err != nil {
		return err
	}
	status, out, err := h.s.Approve(r.Context(), sc, httpapi.RequestID(r.Context()), key, id, body, req)
	if err != nil {
		if cause, ok := storageCause(err); ok {
			h.log.WarnContext(r.Context(), "reading an approval's dataset snapshots failed", "request_id", httpapi.RequestID(r.Context()),
				"organization_id", sc.OrganizationID, "proposal_id", id, "error", cause.Error())
		}
		return Refused(err)
	}
	httpapi.WriteBody(w, status, out) // 201, or the 409 of a conflict this key already answered
	return nil
}

func (h handlers) reject(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var req RejectRequest
	body, err := httpapi.DecodeJSON(r, httpapi.MaxBodyBytes, "http.schema.json#/$defs/Reject", &req)
	if err != nil {
		return err
	}
	status, out, err := h.s.Reject(r.Context(), sc, httpapi.RequestID(r.Context()), key, id, body, req)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteBody(w, status, out)
	return nil
}
