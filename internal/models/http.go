package models

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Route is one implemented operation of openapi.yaml.
type Route struct {
	Method, Path, OperationID string
	Roles                     []tenant.Role // x-required-roles
}

// Routes are the model operations this package serves. A contract test
// compares them with planning/api-operations.json; their roles are the
// ones the service requires (writeRoles, readRoles).
var Routes = []Route{
	{"POST", "/v1/projects/{id}/models", "createModel", writeRoles},
	{"GET", "/v1/projects/{id}/models", "listModels", readRoles},
	{"GET", "/v1/models/{id}", "getModel", readRoles},
	{"GET", "/v1/models/{id}/draft", "getDraft", readRoles},
	{"PATCH", "/v1/models/{id}/draft", "patchDraft", writeRoles},
	{"POST", "/v1/models/{id}/draft/reset", "resetDraft", writeRoles},
	{"POST", "/v1/models/{id}/versions", "publishVersion", writeRoles},
	{"GET", "/v1/models/{id}/versions", "listVersions", readRoles},
	{"GET", "/v1/model-versions/{id}", "getModelVersion", readRoles},
}

// Register adds the model routes to mux.
func Register(mux *http.ServeMux, log *slog.Logger, v auth.Verifier, users httpapi.Resolver, s *Service) {
	h := handlers{s: s, log: log}
	for _, r := range Routes {
		var f func(http.ResponseWriter, *http.Request, tenant.Scope) error
		switch r.OperationID {
		case "createModel":
			f = h.create
		case "listModels":
			f = h.list
		case "getModel":
			f = h.get
		case "getDraft":
			f = h.getDraft
		case "patchDraft":
			f = h.patchDraft
		case "resetDraft":
			f = h.resetDraft
		case "publishVersion":
			f = h.publish
		case "listVersions":
			f = h.listVersions
		case "getModelVersion":
			f = h.getVersion
		}
		mux.Handle(r.Method+" "+r.Path, httpapi.Handler(log, httpapi.Scoped(v, users, f)))
	}
}

type handlers struct {
	s   *Service
	log *slog.Logger
}

// D-001 IDs of the path parameters.
var (
	projectIDs = regexp.MustCompile(`^prj_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	modelIDs   = regexp.MustCompile(`^mdl_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	versionIDs = regexp.MustCompile(`^mv_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
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

// draftETags is openapi IfMatch and DraftETag: one strong ETag.
var draftETags = regexp.MustCompile(`^"draft-revision-([1-9][0-9]*)"$`)

// ifMatch is the draft revision the If-Match header names. A write must
// say which revision it read (D-003): without the header the request is
// 428; anything but one ETag of the draft's form (a list, "*", a weak
// ETag) is 400. A well-formed revision too large for any draft is -1, so
// the service still checks the caller first and then answers 409 (T-013
// review L1).
func ifMatch(r *http.Request) (int64, error) {
	values := r.Header.Values("If-Match")
	if len(values) == 0 {
		return 0, httpapi.Errorf(httpapi.PreconditionRequired, `an If-Match header with the draft's ETag ("draft-revision-N") is required`)
	}
	m := draftETags.FindStringSubmatch(values[0])
	if len(values) > 1 || len(values[0]) > 100 || m == nil {
		return 0, &httpapi.Error{Code: httpapi.InvalidRequest, Message: `If-Match must be one ETag of the form "draft-revision-N"`,
			Details: []httpapi.Detail{{Path: "", Reason: "If-Match"}}}
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil { // well formed, but no draft has reached that revision
		return -1, nil
	}
	return n, nil
}

func setETag(w http.ResponseWriter, d Draft) {
	w.Header().Set("ETag", fmt.Sprintf(`"draft-revision-%d"`, d.Revision))
}

// refused maps the service's errors to what the client sees (openapi
// error codes); tenant and transport errors pass to httpapi.
func refused(err error) error {
	var invalid *InvalidError
	switch {
	case errors.Is(err, ErrNotFound):
		return httpapi.Errorf(httpapi.NotFound, "not found")
	case errors.Is(err, ErrConflict):
		return httpapi.Errorf(httpapi.ModelVersionConflict, "the draft or the model changed since it was read; read it again (no change was made)")
	case errors.Is(err, ErrNoChanges):
		return httpapi.Errorf(httpapi.LifecycleConflict, "the draft has no changes to publish")
	case errors.Is(err, ErrInvalidCursor):
		return httpapi.InvalidCursor()
	case errors.Is(err, ErrDatasetsUnavailable):
		return httpapi.Errorf(httpapi.ServiceUnavailable, "the version binds datasets and dataset storage is not available")
	case errors.Is(err, ErrInvalidLimit): // PageQuery bounds it first
		return &httpapi.Error{Code: httpapi.InvalidRequest, Message: "limit must be one integer from 1 to 100",
			Details: []httpapi.Detail{{Path: "", Reason: "limit"}}}
	case errors.Is(err, ErrNotConfirmed): // the ResetDraft schema requires it first
		return &httpapi.Error{Code: httpapi.RequestValidationFailed, Message: "the request body does not match the schema",
			Details: []httpapi.Detail{{Path: "/confirm_discard", Reason: "a reset must be confirmed", Severity: "error"}}}
	case errors.Is(err, ErrInvalidComment):
		return &httpapi.Error{Code: httpapi.RequestValidationFailed, Message: "the request body does not match the schema",
			Details: []httpapi.Detail{{Path: "/comment", Reason: "at most 2000 characters, without U+0000", Severity: "error"}}}
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		return httpapi.Errorf(httpapi.IdempotencyKeyConflict, "this Idempotency-Key was used for a different request")
	case errors.As(err, &invalid):
		e := &httpapi.Error{Code: httpapi.ModelValidationFailed, Message: "the model is not valid"}
		for _, is := range invalid.Issues {
			e.Details = append(e.Details, httpapi.Detail{Path: is.Path, Reason: is.Message, Code: is.Code, Severity: string(is.Severity)})
		}
		return e
	}
	return err
}

// publishRefused is refused for publishVersion, which also answers a
// dataset binding stage that cannot read or verify the snapshots the base
// binds (in the candidate's check or in that of the draft that follows the
// new version): 503 DATASET_STORAGE_FAILED, as approveProposal and
// createManualScenario do (D-069 (b), H-022; T-054). The cause is logged
// by the handler, never sent. H-022 decided publishVersion only: patchDraft
// and resetDraft run the same stage and keep refused's answer.
func publishRefused(err error) error {
	var storage *DatasetStorageError
	if errors.As(err, &storage) {
		return &httpapi.Error{Code: httpapi.ServiceUnavailable, Message: "the dataset snapshots could not be read; nothing was published (retry later)",
			Details: []httpapi.Detail{{Path: "", Reason: "the dataset storage failed", Code: "DATASET_STORAGE_FAILED"}}}
	}
	return refused(err)
}

func (h handlers) create(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	projectID, err := pathID(r, projectIDs, "project")
	if err != nil {
		return err
	}
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var req CreateRequest
	body, err := httpapi.DecodeJSON(r, httpapi.MaxBodyBytes, "http.schema.json#/$defs/CreateModel", &req)
	if err != nil {
		return err
	}
	status, out, err := h.s.Create(r.Context(), sc, httpapi.RequestID(r.Context()), key, projectID, body, req)
	if err != nil {
		return refused(err)
	}
	httpapi.WriteBody(w, status, out)
	return nil
}

func (h handlers) list(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	projectID, err := pathID(r, projectIDs, "project")
	if err != nil {
		return err
	}
	cursor, limit, err := httpapi.PageQuery(r)
	if err != nil {
		return err
	}
	page, err := h.s.ListModels(r.Context(), sc, projectID, cursor, limit)
	if err != nil {
		return refused(err)
	}
	httpapi.WriteJSON(w, 200, page)
	return nil
}

func (h handlers) get(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, modelIDs, "model")
	if err != nil {
		return err
	}
	m, err := h.s.GetModel(r.Context(), sc, id)
	if err != nil {
		return refused(err)
	}
	httpapi.WriteJSON(w, 200, m)
	return nil
}

func (h handlers) getDraft(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, modelIDs, "model")
	if err != nil {
		return err
	}
	d, err := h.s.GetDraft(r.Context(), sc, id)
	if err != nil {
		return refused(err)
	}
	setETag(w, d)
	httpapi.WriteJSON(w, 200, d)
	return nil
}

// patchDraft takes an RFC 6902 patch of at most the IR's size (D-029). The
// body must match the patch schema with at least one operation (else 422
// REQUEST_VALIDATION_FAILED); whether the patch applies and leaves a valid
// model is the service's (422 MODEL_VALIDATION_FAILED).
func (h handlers) patchDraft(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, modelIDs, "model")
	if err != nil {
		return err
	}
	expected, err := ifMatch(r)
	if err != nil {
		return refused(err)
	}
	var ops []jsontext.Value
	body, err := httpapi.DecodeMedia(r, "application/json-patch+json", validate.MaxIRBytes, "json-patch.schema.json", &ops)
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		return &httpapi.Error{Code: httpapi.RequestValidationFailed, Message: "the request body does not match the schema",
			Details: []httpapi.Detail{{Path: "", Reason: "a patch has at least one operation", Severity: "error"}}}
	}
	d, err := h.s.PatchDraft(r.Context(), sc, httpapi.RequestID(r.Context()), id, expected, body)
	if err != nil {
		return refused(err)
	}
	setETag(w, d)
	httpapi.WriteJSON(w, 200, d)
	return nil
}

func (h handlers) resetDraft(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, modelIDs, "model")
	if err != nil {
		return err
	}
	expected, err := ifMatch(r)
	if err != nil {
		return refused(err)
	}
	var req ResetRequest
	if _, err := httpapi.DecodeJSON(r, httpapi.MaxBodyBytes, "http.schema.json#/$defs/ResetDraft", &req); err != nil {
		return err
	}
	d, err := h.s.ResetDraft(r.Context(), sc, httpapi.RequestID(r.Context()), id, expected, req)
	if err != nil {
		return refused(err)
	}
	setETag(w, d)
	httpapi.WriteJSON(w, 200, d)
	return nil
}

func (h handlers) publish(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, modelIDs, "model")
	if err != nil {
		return err
	}
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	expected, err := ifMatch(r)
	if err != nil {
		return refused(err)
	}
	var req PublishRequest
	body, err := httpapi.DecodeJSON(r, httpapi.MaxBodyBytes, "http.schema.json#/$defs/Publish", &req)
	if err != nil {
		return err
	}
	status, out, err := h.s.Publish(r.Context(), sc, httpapi.RequestID(r.Context()), key, id, expected, body, req)
	if err != nil {
		var storage *DatasetStorageError
		if errors.As(err, &storage) {
			h.log.WarnContext(r.Context(), "reading a publish's dataset snapshots failed", "request_id", httpapi.RequestID(r.Context()),
				"organization_id", sc.OrganizationID, "model_id", id, "error", storage.Err.Error())
		}
		return publishRefused(err)
	}
	httpapi.WriteBody(w, status, out)
	return nil
}

func (h handlers) listVersions(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, modelIDs, "model")
	if err != nil {
		return err
	}
	cursor, limit, err := httpapi.PageQuery(r)
	if err != nil {
		return err
	}
	page, err := h.s.ListVersions(r.Context(), sc, id, cursor, limit)
	if err != nil {
		return refused(err)
	}
	httpapi.WriteJSON(w, 200, page)
	return nil
}

func (h handlers) getVersion(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, versionIDs, "model version")
	if err != nil {
		return err
	}
	v, err := h.s.GetVersion(r.Context(), sc, id)
	if err != nil {
		return refused(err)
	}
	httpapi.WriteJSON(w, 200, v)
	return nil
}
