// Package proposals is the AI proposal service (T-025; AI_SPEC §4,
// API_DATABASE §4, D-002, D-011, D-016, D-027, D-046): it queues a proposal
// on a model's latest version and serves proposals as their job left them.
// Each operation is one tenant.DB.Run transaction that checks the caller's
// membership and role first (D-008); SQL lives in internal/storage. The job
// that asks the provider and reviews its answer is
// internal/proposals/generator's; deciding a proposal is T-026's.
//
// The server binds every identity, validation and approval field: the
// client names the base, the mode, the intent, the selected entities and
// the output language; the server adds the organization, the model, the
// creator, the ID allocation pool (D-046), the status and, later, the
// validation and the risk. Nothing a provider says becomes any of them.
// The proposal, its one job, its audit event and the idempotency response
// commit together, and only then is 202 answered (D-011).
package proposals

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	models "github.com/kcl1104/geminus/internal/storage/models"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/tenant"
)

var (
	// ErrNotFound: no such proposal, model or version in the organization
	// (or no access); the same for all (TEST-009).
	ErrNotFound = errors.New("proposals: not found")
	// ErrStaleBase: the base is not the model's latest version (409
	// MODEL_VERSION_CONFLICT): a proposal is approved only on the latest
	// base (AI_SPEC §6), so one on an older version could never be.
	ErrStaleBase = errors.New("proposals: the base is not the model's latest version")
	// ErrInvalidCursor: the cursor is not one this service issued for this
	// list.
	ErrInvalidCursor = errors.New("proposals: invalid cursor")
	// ErrInvalidLimit: a page size outside 1–100.
	ErrInvalidLimit = errors.New("proposals: invalid page size")
)

// RequestError is a request that is not acceptable as it stands (422
// REQUEST_VALIDATION_FAILED); Issues point into the request body.
type RequestError struct{ Issues []model.ValidationIssue }

func (e *RequestError) Error() string {
	return fmt.Sprintf("proposals: invalid request (%d issue(s))", len(e.Issues))
}

// Availability is whether this deployment may ask an AI provider at all.
// The zero value is NotConfigured: a service set up without saying refuses
// (AI_SPEC §2).
type Availability int

const (
	// NotConfigured: live AI is disabled (AI_PROVIDER_NOT_CONFIGURED).
	NotConfigured Availability = iota
	// PolicyNotApproved: a live provider is selected but the owner has not
	// recorded the approval of model, quota and data policy
	// (AI_POLICY_NOT_APPROVED; H-001, H-017).
	PolicyNotApproved
	// Available: the deterministic fake (development, tests) or an approved
	// live provider.
	Available
)

// UnavailableError is a request refused because AI is off (503 with Code);
// nothing was stored.
type UnavailableError struct{ Availability Availability }

func (e *UnavailableError) Error() string {
	if e.Availability == PolicyNotApproved {
		return "proposals: live AI is not approved (AI_POLICY_NOT_APPROVED)"
	}
	return "proposals: AI is not configured (AI_PROVIDER_NOT_CONFIGURED)"
}

// Service runs proposal operations for a verified scope.
type Service struct {
	DB *tenant.DB
	// AI is this deployment's provider availability (cmd/api derives it
	// from the configuration); the zero value refuses every new proposal.
	AI Availability
	// Policy is the queue's (jobs.DefaultPolicy when zero).
	Policy jobs.Policy
}

// The roles each operation requires (x-required-roles, planning/api-operations.json).
var (
	writeRoles = tenant.Writers
	readRoles  = tenant.Members
)

const (
	createOperation = "createProposal"
	maxLimit        = 100
)

func (s *Service) policy() jobs.Policy {
	if s.Policy == (jobs.Policy{}) {
		return jobs.DefaultPolicy
	}
	return s.Policy
}

// CreateRequest is http.schema.json#/$defs/ProposalRequest.
type CreateRequest struct {
	BaseVersionID string          `json:"base_version_id"`
	Mode          string          `json:"mode"`
	Intent        string          `json:"intent"`
	Context       *RequestContext `json:"context,omitzero"`
	OutputLocale  string          `json:"output_locale,omitzero"`
}

// RequestContext is ProposalRequest.context.
type RequestContext struct {
	SelectedEntityIDs []string `json:"selected_entity_ids,omitzero"`
}

// Accepted is http.schema.json#/$defs/ProposalAccepted.
type Accepted struct {
	ProposalID string `json:"proposal_id"`
	Status     string `json:"status"`
}

// Create queues a proposal on modelID, or replays the response of the
// earlier request with the same Idempotency-Key (same model and body: the
// same answer; anything else: idempotency.ErrConflict). It returns the
// HTTP status (202) and body.
func (s *Service) Create(ctx context.Context, sc tenant.Scope, requestID, key, modelID string, body []byte, req CreateRequest) (int, []byte, error) {
	canonical, err := hashing.Canonical(body)
	if err != nil {
		return 0, nil, err
	}
	fingerprint, err := json.Marshal(map[string]any{"model_id": modelID, "body": jsontext.Value(canonical)}, json.Deterministic(true))
	if err != nil {
		return 0, nil, err
	}
	hash, err := hashing.Of(fingerprint)
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
		if s.AI != Available {
			return &UnavailableError{Availability: s.AI}
		}
		p, err := s.prepare(ctx, tx, m, modelID, req)
		if err != nil {
			return err
		}
		if p, err = store.Insert(ctx, tx, p); err != nil {
			return err
		}
		if _, err := jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: m.OrganizationID, Type: jobs.ModelProposal,
			Resource: jobs.Resource{AIProposalID: p.ID}, Payload: map[string]any{"proposal_id": p.ID, "request_id": requestID}}, s.policy()); err != nil {
			return err
		}
		// IDs, the mode, the language and a count: never the intent
		// (customer prose) or the selected IDs (SECURITY §9).
		if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
			Action: "proposal.create", TargetType: "ai_proposal", TargetID: p.ID, RequestID: requestID,
			Metadata: map[string]any{"model_id": p.ModelID, "base_version_id": p.BaseVersionID, "mode": p.Mode,
				"output_locale": p.OutputLocale, "selected_entities": len(p.SelectedEntityIDs)}}); err != nil {
			return err
		}
		data, err := json.Marshal(Accepted{ProposalID: p.ID, Status: string(store.Generating)})
		if err != nil {
			return err
		}
		if out, err = hashing.Canonical(data); err != nil {
			return err
		}
		status = 202
		return idempotency.Complete(ctx, tx, k, idempotency.Response{Status: status, Body: out})
	})
	return status, out, err
}

// prepare checks req against the organization's model and version and
// binds what the server decides: the proposal's ID, organization, model,
// creator and ID pool.
func (s *Service) prepare(ctx context.Context, tx tenant.Tx, m tenant.Membership, modelID string, req CreateRequest) (store.Proposal, error) {
	var issues []model.ValidationIssue
	if strings.TrimSpace(req.Intent) == "" {
		issues = append(issues, model.NewIssue(validate.ConstraintFailed, "/intent", "the intent says nothing: it is only white space", model.SeverityError))
	}
	loc, err := locale.Parse(req.OutputLocale)
	if err != nil {
		issues = append(issues, model.NewIssue(validate.ConstraintFailed, "/output_locale", "output_locale is en or zh-Hant", model.SeverityError))
	}
	if req.Mode != "edit" && req.Mode != "scenario" {
		issues = append(issues, model.NewIssue(validate.ConstraintFailed, "/mode", "mode is edit or scenario", model.SeverityError))
	}
	if len(issues) > 0 {
		return store.Proposal{}, &RequestError{Issues: issues}
	}
	// The model's row first, as every write about its versions takes it
	// (ADR-022): a publish cannot slip in between the latest check and
	// the commit.
	mdl, err := models.LockModel(ctx, tx, m.OrganizationID, modelID)
	if errors.Is(err, models.ErrNotFound) {
		return store.Proposal{}, ErrNotFound
	}
	if err != nil {
		return store.Proposal{}, err
	}
	v, err := models.GetVersion(ctx, tx, m.OrganizationID, req.BaseVersionID)
	if errors.Is(err, models.ErrNotFound) {
		return store.Proposal{}, ErrNotFound
	}
	if err != nil {
		return store.Proposal{}, err
	}
	if v.ModelID != mdl.ID {
		return store.Proposal{}, &RequestError{Issues: []model.ValidationIssue{model.NewIssue(validate.ConstraintFailed, "/base_version_id",
			"the version is not a version of this model", model.SeverityError)}}
	}
	if mdl.LatestVersionID != v.ID {
		return store.Proposal{}, ErrStaleBase
	}
	var ir model.ModelIR
	if err := model.Decode(v.IR, &ir); err != nil {
		return store.Proposal{}, fmt.Errorf("proposals: the stored version %s does not decode: %w", v.ID, err)
	}
	pool, err := idpool.New(&ir)
	if err != nil {
		return store.Proposal{}, err
	}
	poolJSON, err := json.Marshal(pool)
	if err != nil {
		return store.Proposal{}, err
	}
	var selected []string
	if req.Context != nil {
		selected = req.Context.SelectedEntityIDs
	}
	return store.Proposal{ID: ids.New("aip"), OrganizationID: m.OrganizationID, ModelID: mdl.ID, BaseVersionID: v.ID,
		Mode: req.Mode, Intent: req.Intent, OutputLocale: string(loc), SelectedEntityIDs: selected, IDPool: poolJSON,
		CreatedBy: m.UserID}, nil
}

// Get returns proposal id.
func (s *Service) Get(ctx context.Context, sc tenant.Scope, requestID, id string) (Proposal, error) {
	var out Proposal
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		p, err := store.Get(ctx, tx, m.OrganizationID, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out, err = Public(p, requestID)
		return err
	})
	return out, err
}

// Page is http.schema.json#/$defs/Proposals.
type Page struct {
	Items      []Proposal `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

// List returns one page of the model's proposals, oldest first.
func (s *Service) List(ctx context.Context, sc tenant.Scope, requestID, modelID, cursor string, limit int) (Page, error) {
	if limit < 1 || limit > maxLimit {
		return Page{}, ErrInvalidLimit
	}
	var after *store.Position
	if cursor != "" {
		pos, err := decodeCursor(cursor, sc.OrganizationID, modelID)
		if err != nil {
			return Page{}, err
		}
		after = &pos
	}
	page := Page{Items: []Proposal{}}
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		if _, err := models.GetModel(ctx, tx, m.OrganizationID, modelID); errors.Is(err, models.ErrNotFound) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		rows, err := store.List(ctx, tx, m.OrganizationID, modelID, after, limit+1)
		if err != nil {
			return err
		}
		if len(rows) > limit {
			last := rows[limit-1]
			c := encodeCursor(m.OrganizationID, modelID, store.Position{CreatedAt: last.CreatedAt, ID: last.ID})
			page.NextCursor = &c
			rows = rows[:limit]
		}
		for _, r := range rows {
			p, err := Public(r, requestID)
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
// organization, the model and the list (API_DATABASE §3).
type cursor struct {
	Version int    `json:"v"`
	Org     string `json:"o"`
	List    string `json:"l"`
	Model   string `json:"m"`
	After   string `json:"t"` // RFC 3339 with nanoseconds
	ID      string `json:"i"`
}

const cursorList = "ai-proposals"

var proposalIDs = regexp.MustCompile(`^aip_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

func timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func encodeCursor(org, modelID string, pos store.Position) string {
	data, _ := json.Marshal(cursor{Version: 1, Org: org, List: cursorList, Model: modelID, After: timestamp(pos.CreatedAt), ID: pos.ID})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(s, org, modelID string) (store.Position, error) {
	data, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return store.Position{}, ErrInvalidCursor
	}
	var c cursor
	if err := json.Unmarshal(data, &c, json.RejectUnknownMembers(true)); err != nil {
		return store.Position{}, ErrInvalidCursor
	}
	t, err := time.Parse(time.RFC3339Nano, c.After)
	if err != nil || c.Version != 1 || c.Org != org || c.List != cursorList || c.Model != modelID || !proposalIDs.MatchString(c.ID) {
		return store.Position{}, ErrInvalidCursor
	}
	return store.Position{CreatedAt: t, ID: c.ID}, nil
}
