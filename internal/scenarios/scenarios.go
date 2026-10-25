// Package scenarios is the scenario service (T-028; REQ-010, AI_SPEC §7,
// API_DATABASE §4, D-004, D-021, D-067; D-078–D-080 PROPOSED): a person
// saves a what-if on an immutable version, and any member reads and lists
// the scenarios of the organization, those people saved and those approved
// scenario proposals created (internal/proposals/approval, T-026) alike.
//
// A scenario is an immutable base version and an ordered JSON Patch under
// the scenario allowlist, the one the AI scenario mode takes too (D-021):
// existing capacities, distribution parameters, routing probabilities and
// D-057's line parameters, never the topology or a new dataset binding.
// Creating one checks the patch as the AI scenario's review does (check),
// runs the dataset binding stage against the base's whole manifest, and
// commits exactly one scenario, its audit event and the idempotency
// response in one tenant.DB.Run transaction after the caller's membership
// and role (D-008, D-011). It creates no version, changes no model or
// draft and queues nothing: a run is the person's own createSimulation
// (AI_SPEC §7). Scenarios are never changed (0001). SQL lives in
// internal/storage; the AI cannot reach this package (architecture rules
// ai-suggests-only and scenarios-only-in-the-api).
package scenarios

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/datasets"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	models "github.com/kcl1104/geminus/internal/storage/models"
	"github.com/kcl1104/geminus/internal/storage/objects"
	store "github.com/kcl1104/geminus/internal/storage/scenarios"
	"github.com/kcl1104/geminus/internal/storage/snapshots"
	"github.com/kcl1104/geminus/internal/tenant"
)

var (
	// ErrNotFound: no such version or scenario in the organization (or no
	// access); the same for both (TEST-009).
	ErrNotFound = errors.New("scenarios: not found")
	// ErrInvalidCursor: the cursor is not one this service issued for this
	// list.
	ErrInvalidCursor = errors.New("scenarios: invalid cursor")
	// ErrInvalidLimit: a page size outside 1–100.
	ErrInvalidLimit = errors.New("scenarios: invalid page size")
	// ErrStorageUnavailable: the base binds datasets and no object storage
	// is configured to verify them (503; D-044); nothing was stored.
	ErrStorageUnavailable = errors.New("scenarios: the base binds datasets and dataset storage is not configured")
)

// RequestError is a request that is not acceptable as it stands (422
// REQUEST_VALIDATION_FAILED); Issues point into the request body.
type RequestError struct{ Issues []model.ValidationIssue }

func (e *RequestError) Error() string {
	return fmt.Sprintf("scenarios: invalid request (%d issue(s))", len(e.Issues))
}

// InvalidError is a patch that does not apply to the base under the
// scenario allowlist, or whose patched model is not valid (422
// MODEL_VALIDATION_FAILED): patch issues point into the request body
// (/patch/...), model issues into the patched model, dataset issues into
// the base's manifest (/dataset_bindings/...). Nothing was stored.
type InvalidError struct{ Issues []model.ValidationIssue }

func (e *InvalidError) Error() string {
	return fmt.Sprintf("scenarios: the patch is not acceptable (%d issue(s))", len(e.Issues))
}

// DatasetStorageError: the dataset binding stage could not read or verify
// the base's snapshots (an infrastructure failure, 503
// DATASET_STORAGE_FAILED, as an approval's, D-069 (b)); nothing was stored.
type DatasetStorageError struct{ Err error }

func (e *DatasetStorageError) Error() string {
	return "scenarios: the dataset snapshots could not be read: " + e.Err.Error()
}

func (e *DatasetStorageError) Unwrap() error { return e.Err }

// PackLookup finds a model's domain pack; *domainpack.Registry is one.
type PackLookup interface {
	Lookup(domain string) (domainpack.DomainPack, error)
}

// Service runs scenario operations for a verified scope.
type Service struct {
	DB    *tenant.DB
	Packs PackLookup
	// Objects holds the dataset snapshots a base version's manifest binds
	// (D-044); nil when object storage is disabled, and then a scenario on
	// a base that binds datasets is refused with 503, as the binding stage
	// cannot verify them.
	Objects objects.Store
}

// The roles each operation requires (x-required-roles; SECURITY §2).
var (
	writeRoles = tenant.Writers
	readRoles  = tenant.Members
)

const (
	createOperation = "createManualScenario"
	maxLimit        = 100
)

// CreateRequest is http.schema.json#/$defs/CreateScenario; callers have
// validated it against the schema.
type CreateRequest struct {
	Name  string         `json:"name"`
	Patch jsontext.Value `json:"patch"`
}

// fingerprint is a create's idempotency hash (D-011, D-079 (b)): the
// version of the URL and the RFC 8785 form of the body. The key's scope
// does not hold the version, so this is what tells the same key used on
// another version apart; bodies that differ only in member order or
// spacing are one request.
func fingerprint(versionID string, body []byte) (string, error) {
	canonical, err := hashing.Canonical(body)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(map[string]any{"model_version_id": versionID, "body": jsontext.Value(canonical)}, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	return hashing.Of(data)
}

// bindingFailure is what a create answers when the dataset binding stage
// fails with err rather than with issues: a storage failure, unless the
// request itself ended.
func bindingFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &DatasetStorageError{Err: err}
}

// Create saves the scenario req on version versionID for the scoped user,
// or replays the answer of the earlier request with the same
// Idempotency-Key (same version and body: the same answer; anything else:
// idempotency.ErrConflict). It returns the HTTP status (201) and body.
func (s *Service) Create(ctx context.Context, sc tenant.Scope, requestID, key, versionID string, body []byte, req CreateRequest) (int, []byte, error) {
	// The request itself first, as the schema's checks are.
	if issues := nameIssues(req.Name); len(issues) > 0 {
		return 0, nil, &RequestError{Issues: issues}
	}
	hash, err := fingerprint(versionID, body)
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
		org := m.OrganizationID
		// Any immutable version of the organization, not only the latest
		// (D-067): nothing of the model changes, so nothing is locked.
		v, err := models.GetVersion(ctx, tx, org, versionID)
		if errors.Is(err, models.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		c, err := check(v.IR, req.Patch, s.Packs)
		if err != nil {
			return err
		}
		// The dataset binding stage (D-006, D-033), against the base's
		// whole manifest with the snapshots read again: the patched
		// model's empirical distributions are bound and every snapshot
		// verifies, as an AI scenario's approval checks.
		if len(v.DatasetBindings) > 0 && s.Objects == nil {
			return ErrStorageUnavailable
		}
		keys, err := snapshots.Keys(ctx, tx, org, v.DatasetBindings)
		if err != nil {
			return err
		}
		resolution, err := datasets.Resolve(ctx, snapshots.Store{Keys: keys, Objects: s.Objects}, org, c.ir, v.DatasetBindings)
		if err != nil {
			return bindingFailure(ctx, err)
		}
		if !resolution.Valid() {
			return &InvalidError{Issues: capped(withoutIR(resolution.Issues))}
		}
		row, err := store.Insert(ctx, tx, store.Scenario{ID: ids.New("scn"), OrganizationID: org, ModelID: v.ModelID, BaseVersionID: v.ID,
			Name: req.Name, Patch: c.patch, PatchHash: c.hash, CreatedBy: m.UserID})
		if err != nil {
			return err
		}
		var ops []jsontext.Value
		if err := json.Unmarshal(c.patch, &ops); err != nil {
			return err
		}
		// IDs, the hash and a count: never the name (the person's text,
		// D-041) or the patch (SECURITY §9).
		if err := auditlog.AppendUser(ctx, tx, org, m.UserID, auditlog.Event{
			Action: "scenario.create", TargetType: "scenario", TargetID: row.ID, RequestID: requestID,
			Metadata: map[string]any{"model_id": row.ModelID, "base_version_id": row.BaseVersionID, "patch_hash": row.PatchHash,
				"operations": len(ops)}}); err != nil {
			return err
		}
		pub, err := Public(row)
		if err != nil {
			return err
		}
		data, err := json.Marshal(pub)
		if err != nil {
			return err
		}
		if out, err = hashing.Canonical(data); err != nil {
			return err
		}
		status = 201
		return idempotency.Complete(ctx, tx, k, idempotency.Response{Status: status, Body: out})
	})
	return status, out, err
}

// Get returns scenario id.
func (s *Service) Get(ctx context.Context, sc tenant.Scope, id string) (Scenario, error) {
	var out Scenario
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		r, err := store.Get(ctx, tx, m.OrganizationID, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out, err = Public(r)
		return err
	})
	return out, err
}

// List returns one page of the scenarios on version versionID, oldest
// first: a person's and those approved proposals created alike.
func (s *Service) List(ctx context.Context, sc tenant.Scope, versionID, cursor string, limit int) (Page, error) {
	if limit < 1 || limit > maxLimit {
		return Page{}, ErrInvalidLimit
	}
	var after *store.Position
	if cursor != "" {
		pos, err := decodeCursor(cursor, sc.OrganizationID, versionID)
		if err != nil {
			return Page{}, err
		}
		after = &pos
	}
	page := Page{Items: []Scenario{}}
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		if _, err := models.GetVersion(ctx, tx, m.OrganizationID, versionID); errors.Is(err, models.ErrNotFound) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		rows, err := store.List(ctx, tx, m.OrganizationID, versionID, after, limit+1)
		if err != nil {
			return err
		}
		if len(rows) > limit {
			last := rows[limit-1]
			c := encodeCursor(m.OrganizationID, versionID, store.Position{CreatedAt: last.CreatedAt, ID: last.ID})
			page.NextCursor = &c
			rows = rows[:limit]
		}
		for _, r := range rows {
			p, err := Public(r)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, p)
		}
		return nil
	})
	return page, err
}

// cursor is the position after which a page starts, bound to the
// organization, the version and the list (API_DATABASE §3).
type cursor struct {
	Version int    `json:"v"`
	Org     string `json:"o"`
	List    string `json:"l"`
	Base    string `json:"b"`
	After   string `json:"t"` // RFC 3339 with nanoseconds
	ID      string `json:"i"`
}

const cursorList = "scenarios"

func encodeCursor(org, versionID string, pos store.Position) string {
	data, _ := json.Marshal(cursor{Version: 1, Org: org, List: cursorList, Base: versionID, After: timestamp(pos.CreatedAt), ID: pos.ID})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(s, org, versionID string) (store.Position, error) {
	data, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return store.Position{}, ErrInvalidCursor
	}
	var c cursor
	if err := json.Unmarshal(data, &c, json.RejectUnknownMembers(true)); err != nil {
		return store.Position{}, ErrInvalidCursor
	}
	t, err := time.Parse(time.RFC3339Nano, c.After)
	if err != nil || c.Version != 1 || c.Org != org || c.List != cursorList || c.Base != versionID || !scenarioIDs.MatchString(c.ID) {
		return store.Position{}, ErrInvalidCursor
	}
	return store.Position{CreatedAt: t, ID: c.ID}, nil
}
