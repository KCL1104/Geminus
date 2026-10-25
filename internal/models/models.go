// Package models is the model, draft and version service (T-012, ADR-022):
// create a model from a domain pack's template, edit its draft under
// optimistic concurrency, publish immutable versions and reset a draft.
// Each operation is one tenant.DB.Run transaction that checks the caller's
// membership and role first (D-008); every write locks the model row
// before it reads the draft, so writes of one model run one after another.
// SQL lives in internal/storage/models.
package models

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/datasets"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	store "github.com/kcl1104/geminus/internal/storage/models"
	"github.com/kcl1104/geminus/internal/storage/projects"
	"github.com/kcl1104/geminus/internal/tenant"
)

var (
	// ErrNotFound: no such model, version or project in the organization
	// (or no access); the same for both (TEST-009).
	ErrNotFound = errors.New("models: not found")
	// ErrConflict: the draft or the model moved since the caller read it:
	// stale If-Match revision or base (409 MODEL_VERSION_CONFLICT, D-003).
	ErrConflict = errors.New("models: the draft or model changed")
	// ErrNoChanges: publishing a clean draft (409 LIFECYCLE_CONFLICT).
	ErrNoChanges = errors.New("models: the draft has no changes to publish")
	// ErrInvalidCursor: the cursor is not one this service issued for
	// this list.
	ErrInvalidCursor = errors.New("models: invalid cursor")
	// ErrDatasetsUnavailable: the version binds datasets and no dataset
	// store is configured to verify them (503; T-016 provides one).
	ErrDatasetsUnavailable = errors.New("models: dataset storage is not configured")
	// ErrInvalidLimit: a page size outside 1–100 (openapi Limit).
	ErrInvalidLimit = errors.New("models: invalid page size")
	// ErrNotConfirmed: a reset without confirm_discard. The HTTP contract
	// requires it (ResetDraft), so a request never gets here without it.
	ErrNotConfirmed = errors.New("models: a reset must be confirmed")
	// ErrInvalidComment: a publish comment over 2000 characters, with
	// U+0000 or not UTF-8 (422 at /comment). The HTTP layer refuses these
	// first (Publish schema, httpapi.DecodeJSON).
	ErrInvalidComment = errors.New("models: invalid publish comment")
)

// InvalidError is a patch or model that is not acceptable (422
// MODEL_VALIDATION_FAILED); Issues point into the patch document or the IR.
type InvalidError struct{ Issues []model.ValidationIssue }

func (e *InvalidError) Error() string {
	return fmt.Sprintf("models: invalid (%d issue(s))", len(e.Issues))
}

// DatasetStorageError: the dataset binding stage could not read or verify
// a snapshot the manifest binds (an infrastructure failure, not a problem
// of the data, which is an issue). Nothing was stored. publishVersion
// answers it with 503 DATASET_STORAGE_FAILED, as approveProposal and
// createManualScenario do (D-069 (b), H-022; T-054).
type DatasetStorageError struct{ Err error }

func (e *DatasetStorageError) Error() string {
	return "models: the dataset snapshots could not be read: " + e.Err.Error()
}

func (e *DatasetStorageError) Unwrap() error { return e.Err }

// bindingFailure is what the dataset binding stage answers when it fails
// with err rather than with issues: a storage failure, unless the request
// itself ended (then the context's error, which httpapi answers as it
// answers any ended request). The approval's and the scenarios' rule.
func bindingFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &DatasetStorageError{Err: err}
}

// PackLookup finds the domain pack of a model; *domainpack.Registry is one.
type PackLookup interface {
	Lookup(domain string) (domainpack.DomainPack, error)
}

// Service runs model operations for a verified scope.
type Service struct {
	DB    *tenant.DB
	Packs PackLookup
	// Datasets opens frozen dataset snapshots; nil until object storage
	// exists (T-016). A manifest that is not empty then fails closed.
	Datasets datasets.Store
}

// The roles each operation requires (x-required-roles, planning/api-operations.json).
var (
	writeRoles = tenant.Writers
	readRoles  = tenant.Members
)

// Reads combine the model, its draft and its latest version, which a
// publish changes together: one snapshot, so a read never sees half of it.
var readSnapshot = tenant.ReadSnapshot

// maxLimit is the largest page (openapi Limit); maxIssues the most issues a
// draft or an error holds (migration 0003, Error.details).
const (
	maxLimit  = 100
	maxIssues = 1000
)

const (
	createOperation  = "createModel"
	publishOperation = "publishVersion"
)

// Model is http.schema.json#/$defs/Model.
type Model struct {
	ID             string        `json:"id"`
	OrganizationID string        `json:"organization_id"`
	ProjectID      string        `json:"project_id"`
	Domain         string        `json:"domain"`
	Name           string        `json:"name"`
	Draft          DraftSummary  `json:"draft"`
	LatestVersion  LatestVersion `json:"latest_version"`
	CreatedAt      string        `json:"created_at"`
}

// DraftSummary is Model.draft.
type DraftSummary struct {
	Revision      int64  `json:"revision"`
	BaseVersionID string `json:"base_version_id"`
	OutOfDate     bool   `json:"out_of_date"`
}

// LatestVersion is Model.latest_version.
type LatestVersion struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

// Draft is http.schema.json#/$defs/Draft.
type Draft struct {
	ModelID       string                  `json:"model_id"`
	BaseVersionID string                  `json:"base_version_id"`
	Revision      int64                   `json:"revision"`
	IR            jsontext.Value          `json:"ir"`
	Hash          string                  `json:"hash"`
	OutOfDate     bool                    `json:"out_of_date"`
	Issues        []model.ValidationIssue `json:"issues"`
}

// Version is http.schema.json#/$defs/ModelVersion.
type Version struct {
	ID              string                 `json:"id"`
	OrganizationID  string                 `json:"organization_id"`
	ModelID         string                 `json:"model_id"`
	Version         int64                  `json:"version"`
	SchemaVersion   string                 `json:"schema_version"`
	ParentVersionID *string                `json:"parent_version_id"`
	IR              jsontext.Value         `json:"ir"`
	Hash            string                 `json:"hash"`
	DatasetBindings []model.DatasetBinding `json:"dataset_bindings"`
	Comment         *string                `json:"comment"` // D-041; null when there was none
	CreatedBy       string                 `json:"created_by"`
	CreatedAt       string                 `json:"created_at"`
}

// Models is http.schema.json#/$defs/Models; Versions is …/Versions.
type (
	Models struct {
		Items      []Model `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	Versions struct {
		Items      []Version `json:"items"`
		NextCursor *string   `json:"next_cursor"`
	}
)

// CreateRequest is http.schema.json#/$defs/CreateModel; PublishRequest is
// …/Publish; ResetRequest is …/ResetDraft. Callers have validated them
// against the schema (httpapi.DecodeJSON).
type (
	CreateRequest struct {
		Name     string            `json:"name"`
		Domain   string            `json:"domain"`
		Template string            `json:"template"`
		Locale   domainpack.Locale `json:"locale,omitzero"`
	}
	PublishRequest struct {
		ExpectedBaseVersionID string `json:"expected_base_version_id"`
		Comment               string `json:"comment,omitzero"`
	}
	ResetRequest struct {
		ExpectedLatestVersionID string `json:"expected_latest_version_id"`
		ConfirmDiscard          bool   `json:"confirm_discard"`
	}
)

// Create creates a model in project from req's template, with version 1
// and a draft at revision 1 (API_DATABASE §4), or replays the response of
// the earlier request with the same Idempotency-Key. It returns the HTTP
// status and body to send; body is the request body as received.
func (s *Service) Create(ctx context.Context, sc tenant.Scope, requestID, key, projectID string, body []byte, req CreateRequest) (int, []byte, error) {
	hash, err := requestHash(map[string]any{"project_id": projectID}, body)
	if err != nil {
		return 0, nil, err
	}
	k := idempotency.Key{Org: sc.OrganizationID, Actor: sc.UserID, Operation: createOperation, Key: key}
	var status int
	var out []byte
	err = s.DB.Run(ctx, sc, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(writeRoles...); err != nil {
			return err
		}
		replay, err := idempotency.Claim(ctx, tx, k, hash)
		if err != nil {
			return err
		}
		if replay != nil {
			status = replay.Status
			out, err = hashing.Canonical(replay.Body)
			return err
		}
		if _, err := projects.Get(ctx, tx, m.OrganizationID, projectID); errors.Is(err, projects.ErrNotFound) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		locale := req.Locale
		if locale == "" {
			locale = domainpack.English
		}
		modelID, versionID := ids.New("mdl"), ids.New("mv")
		ir, err := s.template(req.Domain, req.Template, modelID, req.Name, locale)
		if err != nil {
			return err
		}
		data, hash, err := encode(ir)
		if err != nil {
			return err
		}
		res, err := validate.IR(data)
		if err != nil {
			return err
		}
		if res.IR == nil {
			return fmt.Errorf("models: template %s of %s is not schema-valid: %v", req.Template, req.Domain, res.Issues)
		}
		issues, err := s.issues(ctx, m.OrganizationID, res.IR, res.Issues, nil)
		if err != nil {
			return err
		}
		// The pack built it: errors are the pack's, so nothing is created
		// (API_DATABASE §4); warnings are saved with the draft.
		if slices.ContainsFunc(issues, isError) {
			return fmt.Errorf("models: template %s of %s is not valid: %v", req.Template, req.Domain, issues)
		}
		mdl, err := store.InsertModel(ctx, tx, store.Model{ID: modelID, OrganizationID: m.OrganizationID, ProjectID: projectID,
			Name: ir.Name, Domain: ir.Domain, LatestVersionID: versionID, CreatedBy: m.UserID})
		if err != nil {
			return err
		}
		if _, err := store.InsertVersion(ctx, tx, store.Version{ID: versionID, OrganizationID: m.OrganizationID, ModelID: modelID, Version: 1,
			SchemaVersion: ir.SchemaVersion, IR: data, Hash: hash, CreatedBy: m.UserID}); err != nil {
			return err
		}
		d, err := store.InsertDraft(ctx, tx, store.Draft{OrganizationID: m.OrganizationID, ModelID: modelID, BaseVersionID: versionID,
			Revision: 1, IR: data, Hash: hash, Issues: issues, UpdatedBy: m.UserID})
		if err != nil {
			return err
		}
		if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
			Action: "model.create", TargetType: "model", TargetID: modelID, RequestID: requestID,
			Metadata: map[string]any{"template": req.Template, "locale": string(locale), "version_id": versionID}}); err != nil {
			return err
		}
		resp, err := json.Marshal(publicModel(mdl, d, 1))
		if err != nil {
			return err
		}
		if out, err = hashing.Canonical(resp); err != nil {
			return err
		}
		status = 201
		return idempotency.Complete(ctx, tx, k, idempotency.Response{Status: status, Body: out})
	})
	return status, out, err
}

// GetModel returns the model id with its draft and latest version summaries.
func (s *Service) GetModel(ctx context.Context, sc tenant.Scope, id string) (Model, error) {
	var out Model
	err := s.DB.Run(ctx, sc, readSnapshot, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		mdl, err := store.GetModel(ctx, tx, m.OrganizationID, id)
		if err != nil {
			return notFound(err)
		}
		out, err = s.summary(ctx, tx, mdl)
		return err
	})
	return out, err
}

func (s *Service) summary(ctx context.Context, tx tenant.Tx, mdl store.Model) (Model, error) {
	d, err := store.GetDraft(ctx, tx, mdl.OrganizationID, mdl.ID)
	if err != nil {
		return Model{}, err
	}
	latest, err := store.GetVersion(ctx, tx, mdl.OrganizationID, mdl.LatestVersionID)
	if err != nil {
		return Model{}, err
	}
	return publicModel(mdl, d, latest.Version), nil
}

// ListModels returns one page of the project's models, oldest first.
func (s *Service) ListModels(ctx context.Context, sc tenant.Scope, projectID, cursor string, limit int) (Models, error) {
	if limit < 1 || limit > maxLimit {
		return Models{}, ErrInvalidLimit
	}
	var after *store.Position
	if cursor != "" {
		c, err := decodeCursor(cursor, sc.OrganizationID, "models", projectID)
		if err != nil {
			return Models{}, err
		}
		at, err := time.Parse(time.RFC3339Nano, c.After)
		if err != nil || !ValidID("mdl", c.ID) {
			return Models{}, ErrInvalidCursor
		}
		after = &store.Position{CreatedAt: at, ID: c.ID}
	}
	page := Models{Items: []Model{}}
	err := s.DB.Run(ctx, sc, readSnapshot, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		if _, err := projects.Get(ctx, tx, m.OrganizationID, projectID); errors.Is(err, projects.ErrNotFound) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		rows, err := store.ListModels(ctx, tx, m.OrganizationID, projectID, after, limit+1)
		if err != nil {
			return err
		}
		if len(rows) > limit {
			last := rows[limit-1]
			c := encodeCursor(cursor_{Version: 1, Org: m.OrganizationID, List: "models", Parent: projectID,
				After: last.CreatedAt.UTC().Format(time.RFC3339Nano), ID: last.ID})
			page.NextCursor = &c
			rows = rows[:limit]
		}
		for _, r := range rows {
			item, err := s.summary(ctx, tx, r)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return nil
	})
	return page, err
}

// GetDraft returns the model's draft.
func (s *Service) GetDraft(ctx context.Context, sc tenant.Scope, modelID string) (Draft, error) {
	var out Draft
	err := s.DB.Run(ctx, sc, readSnapshot, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		mdl, err := store.GetModel(ctx, tx, m.OrganizationID, modelID)
		if err != nil {
			return notFound(err)
		}
		d, err := store.GetDraft(ctx, tx, m.OrganizationID, modelID)
		if err != nil {
			return err
		}
		out, err = publicDraft(d, mdl.LatestVersionID)
		return err
	})
	return out, err
}

// PatchDraft applies a JSON Patch (application/json-patch+json) to the
// draft if it is still at revision expected (If-Match). The result only has
// to be schema-valid; its semantic issues are saved with it (D-028).
func (s *Service) PatchDraft(ctx context.Context, sc tenant.Scope, requestID, modelID string, expected int64, patchDoc []byte) (Draft, error) {
	var out Draft
	err := s.DB.Run(ctx, sc, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(writeRoles...); err != nil {
			return err
		}
		mdl, err := store.LockModel(ctx, tx, m.OrganizationID, modelID)
		if err != nil {
			return notFound(err)
		}
		d, err := store.GetDraft(ctx, tx, m.OrganizationID, modelID)
		if err != nil {
			return err
		}
		if d.Revision != expected {
			return ErrConflict
		}
		current, err := storedIR(d.IR, d.Hash)
		if err != nil {
			return err
		}
		res, err := patch.Apply(current, patchDoc, patch.ManualDraft)
		if err != nil {
			return err
		}
		if !res.Applied() {
			return &InvalidError{Issues: capped(validate.Sort(res.PatchIssues))}
		}
		if res.Validation.IR == nil { // not schema-valid: nothing is saved (D-028)
			return &InvalidError{Issues: capped(validate.Sort(res.Validation.Issues))}
		}
		if at, ok := nulAt(res.Candidate); ok {
			return &InvalidError{Issues: []model.ValidationIssue{model.NewIssue(validate.IRSchemaInvalid, at, "U+0000 cannot be stored", model.SeverityError)}}
		}
		base, err := store.GetVersion(ctx, tx, m.OrganizationID, d.BaseVersionID)
		if err != nil {
			return err
		}
		issues, err := s.issues(ctx, m.OrganizationID, res.Validation.IR, res.Validation.Issues, base.DatasetBindings)
		if err != nil {
			return err
		}
		hash, err := hashing.Model(res.Candidate)
		if err != nil {
			return err
		}
		next, err := store.UpdateDraft(ctx, tx, store.Draft{OrganizationID: m.OrganizationID, ModelID: modelID, BaseVersionID: d.BaseVersionID,
			Revision: d.Revision + 1, IR: res.Candidate, Hash: hash, Issues: issues, UpdatedBy: m.UserID}, d.Revision)
		if errors.Is(err, store.ErrStale) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
			Action: "model.draft.update", TargetType: "model", TargetID: modelID, RequestID: requestID,
			Metadata: map[string]any{"revision": next.Revision}}); err != nil {
			return err
		}
		out, err = publicDraft(next, mdl.LatestVersionID)
		return err
	})
	return out, err
}

// ResetDraft discards the draft's changes: it becomes the latest version
// again, if the draft is still at revision expected and req names the
// latest version (D-003: explicit, never automatic).
func (s *Service) ResetDraft(ctx context.Context, sc tenant.Scope, requestID, modelID string, expected int64, req ResetRequest) (Draft, error) {
	if !req.ConfirmDiscard {
		return Draft{}, ErrNotConfirmed
	}
	var out Draft
	err := s.DB.Run(ctx, sc, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(writeRoles...); err != nil {
			return err
		}
		mdl, err := store.LockModel(ctx, tx, m.OrganizationID, modelID)
		if err != nil {
			return notFound(err)
		}
		d, err := store.GetDraft(ctx, tx, m.OrganizationID, modelID)
		if err != nil {
			return err
		}
		if d.Revision != expected || req.ExpectedLatestVersionID != mdl.LatestVersionID {
			return ErrConflict
		}
		latest, err := store.GetVersion(ctx, tx, m.OrganizationID, mdl.LatestVersionID)
		if err != nil {
			return err
		}
		next, err := s.syncDraft(ctx, tx, m, d, latest)
		if err != nil {
			return err
		}
		if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
			Action: "model.draft.reset", TargetType: "model", TargetID: modelID, RequestID: requestID,
			Metadata: map[string]any{"revision": next.Revision, "version_id": latest.ID}}); err != nil {
			return err
		}
		out, err = publicDraft(next, mdl.LatestVersionID)
		return err
	})
	return out, err
}

// Publish makes the draft the model's next immutable version (API_DATABASE
// §4): the draft must be at revision expected (If-Match), based on the
// latest version, which req names, and have changes; the whole candidate
// is validated, datasets included. Snapshots that cannot be read or
// verified, for the candidate or for the draft that follows the new
// version, are a DatasetStorageError, and the transaction stores nothing
// (no version, audit event or idempotency response; H-022). Like Create
// it is idempotent per key and returns the HTTP status and body.
func (s *Service) Publish(ctx context.Context, sc tenant.Scope, requestID, key, modelID string, expected int64, body []byte, req PublishRequest) (int, []byte, error) {
	comment, err := publishComment(req.Comment)
	if err != nil {
		return 0, nil, err
	}
	hash, err := requestHash(map[string]any{"model_id": modelID, "if_match_revision": expected}, body)
	if err != nil {
		return 0, nil, err
	}
	k := idempotency.Key{Org: sc.OrganizationID, Actor: sc.UserID, Operation: publishOperation, Key: key}
	var status int
	var out []byte
	err = s.DB.Run(ctx, sc, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(writeRoles...); err != nil {
			return err
		}
		replay, err := idempotency.Claim(ctx, tx, k, hash)
		if err != nil {
			return err
		}
		if replay != nil {
			status = replay.Status
			out, err = hashing.Canonical(replay.Body)
			return err
		}
		mdl, err := store.LockModel(ctx, tx, m.OrganizationID, modelID)
		if err != nil {
			return notFound(err)
		}
		d, err := store.GetDraft(ctx, tx, m.OrganizationID, modelID)
		if err != nil {
			return err
		}
		if d.Revision != expected || req.ExpectedBaseVersionID != d.BaseVersionID || d.BaseVersionID != mdl.LatestVersionID {
			return ErrConflict
		}
		base, err := store.GetVersion(ctx, tx, m.OrganizationID, d.BaseVersionID)
		if err != nil {
			return err
		}
		if d.Hash == base.Hash {
			return ErrNoChanges
		}
		candidate, err := storedIR(d.IR, d.Hash)
		if err != nil {
			return err
		}
		res, err := validate.IR(candidate)
		if err != nil {
			return err
		}
		if res.IR == nil { // a stored draft is always schema-valid
			return fmt.Errorf("models: the stored draft of %s is not schema-valid: %v", modelID, res.Issues)
		}
		resolution, issues, err := s.check(ctx, m.OrganizationID, res.IR, res.Issues, base.DatasetBindings)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(issues, isError) {
			return &InvalidError{Issues: issues} // capped by check
		}
		v, err := store.InsertVersion(ctx, tx, store.Version{ID: ids.New("mv"), OrganizationID: m.OrganizationID, ModelID: modelID,
			Version: base.Version + 1, SchemaVersion: res.IR.SchemaVersion, ParentVersionID: &base.ID, IR: candidate, Hash: d.Hash,
			DatasetBindings: resolution.Manifest, Comment: comment, CreatedBy: m.UserID})
		if err != nil {
			return err
		}
		if err := s.advance(ctx, tx, m, mdl, d, base, v, res.IR.Name); err != nil {
			return err
		}
		// The comment is the user's text: it is kept with the version
		// (D-041); audit metadata holds IDs and counts only.
		if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
			Action: "model.publish", TargetType: "model_version", TargetID: v.ID, RequestID: requestID,
			Metadata: map[string]any{"version": v.Version, "model_id": modelID}}); err != nil {
			return err
		}
		pv, err := publicVersion(v)
		if err != nil {
			return err
		}
		resp, err := json.Marshal(pv)
		if err != nil {
			return err
		}
		if out, err = hashing.Canonical(resp); err != nil {
			return err
		}
		status = 201
		return idempotency.Complete(ctx, tx, k, idempotency.Response{Status: status, Body: out})
	})
	return status, out, err
}

// advance moves the model to version v, created from base, and applies the
// draft rule of ADR-022 (D-003): a clean draft follows the new version; a
// dirty one is kept as it is and is then out of date. Publishing from the
// draft creates v from the draft's own content, so the draft follows it.
func (s *Service) advance(ctx context.Context, tx tenant.Tx, m tenant.Membership, mdl store.Model, d store.Draft, base, v store.Version, name string) error {
	_, err := s.Advance(ctx, tx, m, mdl, d, base, v, name)
	return err
}

// Advance is the version step of ADR-022 for a version another service
// created in tx from base, the model's latest (T-026's approval of an edit
// proposal): the model's latest becomes v and its name name; a clean draft
// (its content is its own base's, or v's) follows v one revision later, a
// dirty one is kept byte for byte and is out of date from then on (D-003:
// no rebase). It returns the draft as it is afterwards. The caller holds
// the model row (store.LockModel) and read d after taking it; s.Datasets
// must open v's manifest when it has one (the synced draft's issues).
func (s *Service) Advance(ctx context.Context, tx tenant.Tx, m tenant.Membership, mdl store.Model, d store.Draft, base, v store.Version, name string) (store.Draft, error) {
	if err := store.SetLatest(ctx, tx, m.OrganizationID, mdl.ID, v.ID, name); err != nil {
		return store.Draft{}, err
	}
	// Clean is against the draft's own base, which is not base when the
	// draft is already behind (T-012 review L3).
	own := base
	if d.BaseVersionID != base.ID {
		var err error
		if own, err = store.GetVersion(ctx, tx, m.OrganizationID, d.BaseVersionID); err != nil {
			return store.Draft{}, err
		}
	}
	if d.Hash != own.Hash && d.Hash != v.Hash {
		return d, nil // dirty: kept, and out of date from now on
	}
	return s.syncDraft(ctx, tx, m, d, v)
}

// syncDraft makes the draft version v's content, one revision later.
func (s *Service) syncDraft(ctx context.Context, tx tenant.Tx, m tenant.Membership, d store.Draft, v store.Version) (store.Draft, error) {
	ir, err := storedIR(v.IR, v.Hash)
	if err != nil {
		return store.Draft{}, err
	}
	res, err := validate.IR(ir)
	if err != nil {
		return store.Draft{}, err
	}
	if res.IR == nil {
		return store.Draft{}, fmt.Errorf("models: stored version %s is not schema-valid: %v", v.ID, res.Issues)
	}
	issues, err := s.issues(ctx, m.OrganizationID, res.IR, res.Issues, v.DatasetBindings)
	if err != nil {
		return store.Draft{}, err
	}
	next, err := store.UpdateDraft(ctx, tx, store.Draft{OrganizationID: m.OrganizationID, ModelID: d.ModelID, BaseVersionID: v.ID,
		Revision: d.Revision + 1, IR: ir, Hash: v.Hash, Issues: issues, UpdatedBy: m.UserID}, d.Revision)
	if errors.Is(err, store.ErrStale) {
		return store.Draft{}, ErrConflict
	}
	return next, err
}

// GetVersion returns the version id.
func (s *Service) GetVersion(ctx context.Context, sc tenant.Scope, id string) (Version, error) {
	var out Version
	err := s.DB.Run(ctx, sc, readSnapshot, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		v, err := store.GetVersion(ctx, tx, m.OrganizationID, id)
		if err != nil {
			return notFound(err)
		}
		out, err = publicVersion(v)
		return err
	})
	return out, err
}

// ListVersions returns one page of the model's versions, newest first.
func (s *Service) ListVersions(ctx context.Context, sc tenant.Scope, modelID, cursor string, limit int) (Versions, error) {
	if limit < 1 || limit > maxLimit {
		return Versions{}, ErrInvalidLimit
	}
	var before *int64
	if cursor != "" {
		c, err := decodeCursor(cursor, sc.OrganizationID, "versions", modelID)
		if err != nil {
			return Versions{}, err
		}
		n, err := strconv.ParseInt(c.After, 10, 64)
		if err != nil || n < 2 || strconv.FormatInt(n, 10) != c.After || c.ID != "" {
			return Versions{}, ErrInvalidCursor
		}
		before = &n
	}
	page := Versions{Items: []Version{}}
	err := s.DB.Run(ctx, sc, readSnapshot, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		if _, err := store.GetModel(ctx, tx, m.OrganizationID, modelID); err != nil {
			return notFound(err)
		}
		rows, err := store.ListVersions(ctx, tx, m.OrganizationID, modelID, before, limit+1)
		if err != nil {
			return err
		}
		if len(rows) > limit {
			c := encodeCursor(cursor_{Version: 1, Org: m.OrganizationID, List: "versions", Parent: modelID,
				After: strconv.FormatInt(rows[limit-1].Version, 10)})
			page.NextCursor = &c
			rows = rows[:limit]
		}
		for _, v := range rows {
			pv, err := publicVersion(v)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, pv)
		}
		return nil
	})
	return page, err
}

// template builds the model from its domain pack's template.
func (s *Service) template(domain, name, modelID, modelName string, locale domainpack.Locale) (*model.ModelIR, error) {
	pack, err := s.Packs.Lookup(domain)
	if err != nil {
		return nil, err
	}
	tp, ok := pack.(domainpack.TemplateProvider)
	if !ok {
		return nil, fmt.Errorf("models: the %s pack offers no templates", domain)
	}
	// The request was schema-valid, so a template the pack cannot build is
	// the pack's fault, not the caller's.
	ir, err := tp.Template(name, modelID, modelName, locale)
	if err != nil {
		return nil, fmt.Errorf("models: template %s of %s: %w", name, domain, err)
	}
	return ir, nil
}

// issues are a draft's saved issues (D-028): the generic semantic ones the
// caller has, the domain pack's, and the dataset bindings against manifest.
func (s *Service) issues(ctx context.Context, org string, ir *model.ModelIR, semantic []model.ValidationIssue, manifest []model.DatasetBinding) ([]model.ValidationIssue, error) {
	_, issues, err := s.check(ctx, org, ir, semantic, manifest)
	return issues, err
}

// check runs the pack's rules and the dataset binding stage on a
// schema-valid IR and returns every issue, IR paths relative to the IR and
// manifest paths into manifest (TWIN_IR_SPEC §5.2). Each stage runs only if
// the ones before it found no error (D-023). Every binding of manifest is
// checked and frozen, used or not (§5.2: a run keeps the version's
// manifest; T-012 review M3), so a manifest needs dataset storage, and a
// snapshot that cannot be read is a DatasetStorageError (bindingFailure).
func (s *Service) check(ctx context.Context, org string, ir *model.ModelIR, semantic []model.ValidationIssue, manifest []model.DatasetBinding) (datasets.Resolution, []model.ValidationIssue, error) {
	pack, err := s.Packs.Lookup(ir.Domain)
	if err != nil {
		return datasets.Resolution{}, nil, err
	}
	if len(manifest) > 0 && s.Datasets == nil {
		return datasets.Resolution{}, nil, ErrDatasetsUnavailable
	}
	issues := slices.Clone(semantic)
	if !slices.ContainsFunc(issues, isError) {
		issues = append(issues, pack.Validate(ir)...)
	}
	var res datasets.Resolution
	if !slices.ContainsFunc(issues, isError) { // binding checks need a semantically valid IR
		res, err = datasets.Resolve(ctx, s.Datasets, org, ir, manifest)
		if err != nil {
			return datasets.Resolution{}, nil, bindingFailure(ctx, err)
		}
		for _, i := range res.Issues {
			if rest, ok := strings.CutPrefix(i.Path, "/ir"); ok && (rest == "" || rest[0] == '/') {
				i.Path = rest
			}
			issues = append(issues, i)
		}
	}
	return res, capped(validate.Sort(issues)), nil
}

// capped keeps a sorted issue list within maxIssues: the first ones, after
// a LIMIT_EXCEEDED issue at the root that says how many are left out. It is
// an error if any left out is, so a capped list blocks a publish exactly
// when the whole one would.
func capped(issues []model.ValidationIssue) []model.ValidationIssue {
	if len(issues) <= maxIssues {
		return issues
	}
	kept, rest := issues[:maxIssues-1], issues[maxIssues-1:]
	severity := model.SeverityWarning
	if slices.ContainsFunc(rest, isError) {
		severity = model.SeverityError
	}
	marker := model.NewIssue(validate.LimitExceeded, "", fmt.Sprintf("%d more issues are not listed", len(rest)), severity)
	return append([]model.ValidationIssue{marker}, kept...)
}

// nulAt is the JSON Pointer of the first string in doc that holds U+0000,
// which PostgreSQL text and jsonb cannot store: the value's, or for a
// member name the object's (as httpapi.DecodeJSON reports one).
func nulAt(doc []byte) (string, bool) {
	if !bytes.Contains(doc, []byte(`\u0000`)) {
		return "", false
	}
	dec := jsontext.NewDecoder(bytes.NewReader(doc))
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			return "", false
		}
		if tok.Kind() == '"' && strings.ContainsRune(tok.String(), 0) {
			p := dec.StackPointer()
			if kind, n := dec.StackIndex(dec.StackDepth()); kind == '{' && n%2 == 1 {
				p = p.Parent()
			}
			return string(p), true
		}
	}
}

func isError(i model.ValidationIssue) bool { return i.Severity == model.SeverityError }

func encode(ir *model.ModelIR) ([]byte, string, error) {
	data, err := model.Encode(ir)
	if err != nil {
		return nil, "", err
	}
	canonical, err := hashing.Canonical(data)
	if err != nil {
		return nil, "", err
	}
	return canonical, hashing.Sum(canonical), nil
}

// requestHash is the idempotency hash of a request: its canonical body and
// the path and header values that are part of what it asks (D-011).
func requestHash(params map[string]any, body []byte) (string, error) {
	canonical, err := hashing.Canonical(body)
	if err != nil {
		return "", err
	}
	params["body"] = jsontext.Value(canonical)
	data, err := json.Marshal(params, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	return hashing.Of(data)
}

func notFound(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

func timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func publicModel(mdl store.Model, d store.Draft, latestVersion int64) Model {
	return Model{ID: mdl.ID, OrganizationID: mdl.OrganizationID, ProjectID: mdl.ProjectID, Domain: mdl.Domain, Name: mdl.Name,
		Draft:         DraftSummary{Revision: d.Revision, BaseVersionID: d.BaseVersionID, OutOfDate: d.BaseVersionID != mdl.LatestVersionID},
		LatestVersion: LatestVersion{ID: mdl.LatestVersionID, Version: latestVersion}, CreatedAt: timestamp(mdl.CreatedAt)}
}

// storedIR is a stored IR as it was checked and hashed: its canonical text
// (RFC 8785), read back byte for byte (D-040). The database checks the hash
// on every write; it is checked again here, so bytes the hash does not
// cover are an error, never served.
func storedIR(ir []byte, hash string) (jsontext.Value, error) {
	if got := hashing.Sum(ir); got != hash {
		return nil, fmt.Errorf("the stored IR hashes to %s, not its stored hash %s", got, hash)
	}
	return jsontext.Value(ir), nil
}

func publicDraft(d store.Draft, latestVersionID string) (Draft, error) {
	ir, err := storedIR(d.IR, d.Hash)
	if err != nil {
		return Draft{}, fmt.Errorf("models: draft of %s: %w", d.ModelID, err)
	}
	issues := d.Issues
	if issues == nil {
		issues = []model.ValidationIssue{}
	}
	return Draft{ModelID: d.ModelID, BaseVersionID: d.BaseVersionID, Revision: d.Revision, IR: ir, Hash: d.Hash,
		OutOfDate: d.BaseVersionID != latestVersionID, Issues: issues}, nil
}

func publicVersion(v store.Version) (Version, error) {
	ir, err := storedIR(v.IR, v.Hash)
	if err != nil {
		return Version{}, fmt.Errorf("models: version %s: %w", v.ID, err)
	}
	bindings := v.DatasetBindings
	if bindings == nil {
		bindings = []model.DatasetBinding{}
	}
	return Version{ID: v.ID, OrganizationID: v.OrganizationID, ModelID: v.ModelID, Version: v.Version, SchemaVersion: v.SchemaVersion,
		ParentVersionID: v.ParentVersionID, IR: ir, Hash: v.Hash, DatasetBindings: bindings, Comment: v.Comment, CreatedBy: v.CreatedBy, CreatedAt: timestamp(v.CreatedAt)}, nil
}

// publishComment is the comment a version keeps (D-041): nil for none or
// an empty one; at most 2000 characters (code points, as JSON Schema and
// PostgreSQL count them), without U+0000, which PostgreSQL text cannot
// store.
func publishComment(c string) (*string, error) {
	if c == "" {
		return nil, nil
	}
	if !utf8.ValidString(c) || utf8.RuneCountInString(c) > 2000 || strings.ContainsRune(c, 0) {
		return nil, ErrInvalidComment
	}
	return &c, nil
}

// cursor_ is the position a page starts after, bound to the organization,
// the list and its parent (project or model; API_DATABASE §3). It is not
// signed: it only moves within the caller's own organization, which RLS
// and the organization filter confine.
type cursor_ struct {
	Version int    `json:"v"`
	Org     string `json:"o"`
	List    string `json:"l"`
	Parent  string `json:"p"`
	After   string `json:"t"`
	ID      string `json:"i,omitzero"`
}

func encodeCursor(c cursor_) string {
	data, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(s, org, list, parent string) (cursor_, error) {
	if len(s) > 2048 {
		return cursor_{}, ErrInvalidCursor
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return cursor_{}, ErrInvalidCursor
	}
	var c cursor_
	if err := json.Unmarshal(data, &c, json.RejectUnknownMembers(true)); err != nil {
		return cursor_{}, ErrInvalidCursor
	}
	if c.Version != 1 || c.Org != org || c.List != list || c.Parent != parent || encodeCursor(c) != s {
		return cursor_{}, ErrInvalidCursor
	}
	return c, nil
}

var ulid = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// ValidID reports whether id is a D-001 ID with the prefix.
func ValidID(prefix, id string) bool {
	rest, ok := strings.CutPrefix(id, prefix+"_")
	return ok && ulid.MatchString(rest)
}
