// Package models is the model, draft and version service (T-012, ADR-022):
// create a model from a domain pack's template, edit its draft under
// optimistic concurrency, publish immutable versions and reset a draft.
// Each operation is one tenant.DB.Run transaction that checks the caller's
// membership and role first (D-008); every write locks the model row
// before it reads the draft, so writes of one model run one after another.
// SQL lives in internal/storage/models.
package models

import (
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
)

// InvalidError is a patch or model that is not acceptable (422
// MODEL_VALIDATION_FAILED); Issues point into the patch document or the IR.
type InvalidError struct{ Issues []model.ValidationIssue }

func (e *InvalidError) Error() string {
	return fmt.Sprintf("models: invalid (%d issue(s))", len(e.Issues))
}

// Service runs model operations for a verified scope.
type Service struct {
	DB    *tenant.DB
	Packs *domainpack.Registry
	// Datasets opens frozen dataset snapshots; nil until object storage
	// exists (T-016). A manifest that is not empty then fails closed.
	Datasets datasets.Store
}

// The roles each operation requires (x-required-roles, planning/api-operations.json).
var (
	writeRoles = tenant.Writers
	readRoles  = tenant.Members
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
		issues, err := s.issues(ctx, m.OrganizationID, ir, validate.Semantic(ir), nil)
		if err != nil {
			return err
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
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
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
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
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
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
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
		res, err := patch.Apply(d.IR, patchDoc, patch.ManualDraft)
		if err != nil {
			return err
		}
		if !res.Applied() {
			return &InvalidError{Issues: res.PatchIssues}
		}
		if res.Validation.IR == nil { // not schema-valid: nothing is saved (D-028)
			return &InvalidError{Issues: res.Validation.Issues}
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
		return Draft{}, &InvalidError{Issues: []model.ValidationIssue{model.NewIssue(validate.IRSchemaInvalid, "/confirm_discard",
			"a reset discards the draft's changes and must be confirmed", model.SeverityError)}}
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
// is validated, datasets included. Like Create it is idempotent per key and
// returns the HTTP status and body.
func (s *Service) Publish(ctx context.Context, sc tenant.Scope, requestID, key, modelID string, expected int64, body []byte, req PublishRequest) (int, []byte, error) {
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
		res, err := validate.IR(d.IR)
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
			return &InvalidError{Issues: issues}
		}
		v, err := store.InsertVersion(ctx, tx, store.Version{ID: ids.New("mv"), OrganizationID: m.OrganizationID, ModelID: modelID,
			Version: base.Version + 1, SchemaVersion: res.IR.SchemaVersion, ParentVersionID: &base.ID, IR: d.IR, Hash: d.Hash,
			DatasetBindings: resolution.Manifest, CreatedBy: m.UserID})
		if err != nil {
			return err
		}
		if err := s.advance(ctx, tx, m, mdl, d, base, v, res.IR.Name); err != nil {
			return err
		}
		// The comment is the user's text: audit metadata holds IDs and counts
		// only, and no table has a place for it yet (ADR-022).
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
// Later approval paths (T-026) call this too.
func (s *Service) advance(ctx context.Context, tx tenant.Tx, m tenant.Membership, mdl store.Model, d store.Draft, base, v store.Version, name string) error {
	if err := store.SetLatest(ctx, tx, m.OrganizationID, mdl.ID, v.ID, name); err != nil {
		return err
	}
	if d.Hash != base.Hash && d.Hash != v.Hash {
		return nil // dirty: kept, and out of date from now on
	}
	_, err := s.syncDraft(ctx, tx, m, d, v)
	return err
}

// syncDraft makes the draft version v's content, one revision later.
func (s *Service) syncDraft(ctx context.Context, tx tenant.Tx, m tenant.Membership, d store.Draft, v store.Version) (store.Draft, error) {
	res, err := validate.IR(v.IR)
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
		Revision: d.Revision + 1, IR: v.IR, Hash: v.Hash, Issues: issues, UpdatedBy: m.UserID}, d.Revision)
	if errors.Is(err, store.ErrStale) {
		return store.Draft{}, ErrConflict
	}
	return next, err
}

// GetVersion returns the version id.
func (s *Service) GetVersion(ctx context.Context, sc tenant.Scope, id string) (Version, error) {
	var out Version
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
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
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
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
// schema-valid IR and returns every issue, IR paths relative to the IR.
// Only the bindings of manifest that ir uses are checked, and the
// resolution's manifest is those: a version freezes what it needs.
func (s *Service) check(ctx context.Context, org string, ir *model.ModelIR, semantic []model.ValidationIssue, manifest []model.DatasetBinding) (datasets.Resolution, []model.ValidationIssue, error) {
	manifest = usedBindings(ir, manifest)
	pack, err := s.Packs.Lookup(ir.Domain)
	if err != nil {
		return datasets.Resolution{}, nil, err
	}
	issues := slices.Clone(semantic)
	issues = append(issues, pack.Validate(ir)...)
	if len(manifest) > 0 && s.Datasets == nil {
		return datasets.Resolution{}, nil, ErrDatasetsUnavailable
	}
	var res datasets.Resolution
	if !slices.ContainsFunc(issues, isError) { // binding checks need a semantically valid IR
		res, err = datasets.Resolve(ctx, s.Datasets, org, ir, manifest)
		if err != nil {
			return datasets.Resolution{}, nil, err
		}
		for _, i := range res.Issues {
			if rest, ok := strings.CutPrefix(i.Path, "/ir"); ok && (rest == "" || rest[0] == '/') {
				i.Path = rest
			}
			issues = append(issues, i)
		}
	}
	return res, validate.Sort(issues), nil
}

// usedBindings are the bindings of manifest that ir's empirical
// distributions use.
func usedBindings(ir *model.ModelIR, manifest []model.DatasetBinding) []model.DatasetBinding {
	used := map[string]bool{}
	for i := range ir.Nodes {
		for _, d := range ir.Nodes[i].Distributions() {
			if d.Distribution.Type == model.DistEmpirical {
				used[d.Distribution.SamplesRef] = true
			}
		}
	}
	var out []model.DatasetBinding
	for _, b := range manifest {
		if used[b.LogicalRef] {
			out = append(out, b)
		}
	}
	return out
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

// canonicalIR is a stored IR in the form its hash covers (RFC 8785):
// jsonb keeps the value but not the bytes, so what was stored is not what
// is read back.
func canonicalIR(stored []byte) (jsontext.Value, error) {
	return hashing.Canonical(stored)
}

func publicDraft(d store.Draft, latestVersionID string) (Draft, error) {
	ir, err := canonicalIR(d.IR)
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
	ir, err := canonicalIR(v.IR)
	if err != nil {
		return Version{}, fmt.Errorf("models: version %s: %w", v.ID, err)
	}
	bindings := v.DatasetBindings
	if bindings == nil {
		bindings = []model.DatasetBinding{}
	}
	return Version{ID: v.ID, OrganizationID: v.OrganizationID, ModelID: v.ModelID, Version: v.Version, SchemaVersion: v.SchemaVersion,
		ParentVersionID: v.ParentVersionID, IR: ir, Hash: v.Hash, DatasetBindings: bindings, CreatedBy: v.CreatedBy, CreatedAt: timestamp(v.CreatedAt)}, nil
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
