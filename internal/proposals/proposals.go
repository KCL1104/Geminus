// Package proposals is the AI proposal service (T-025; AI_SPEC §4,
// API_DATABASE §4, D-002, D-011, D-016, D-027, D-046): it queues a proposal
// on a model's latest version and serves proposals as their job left them.
// Each operation is one tenant.DB.Run transaction that checks the caller's
// membership and role first (D-008); SQL lives in internal/storage. The job
// that asks the provider and reviews its answer is
// internal/proposals/generator's; a person's decision (approve, reject) is
// internal/proposals/approval's (T-026).
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
	"slices"
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
	"github.com/kcl1104/geminus/internal/storage/objects"
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
type UnavailableError struct {
	Availability Availability
	// Photo: the request is a photo, refused because the photo path is off
	// (Service.Photo; the T-056 follow-up), details[0].code
	// CodeImageUnsupported.
	Photo bool
	// Research: the request asks for web research, refused because this
	// deployment's provider may not search for it (Service.Research, H-046
	// (2)), details[0].code CodeResearchUnsupported.
	Research bool
	// Model: the request names a model this deployment's approval does not
	// list for proposals (Service.Models, Service.PhotoModels; H-049),
	// details[0].code CodeModelNotApproved at /model.
	Model bool
}

// CodeResearchUnsupported: the request asks for web research and this
// deployment's provider may not search for it (Service.Research), so the
// worker would refuse the call; the code the job would fail it with
// (provider.CodeResearchUnsupported, as internal/proposals/generator writes
// it).
const CodeResearchUnsupported = "AI_RESEARCH_UNSUPPORTED"

// CodeModelNotApproved: the request names a model this deployment's
// approval does not list for proposals (H-049), so the worker would refuse
// the call; the code the job would fail it with
// (provider.CodeModelNotApproved, as internal/proposals/generator writes
// it).
const CodeModelNotApproved = "AI_MODEL_NOT_APPROVED"

// ProposalModels are the models a request may name (common.schema.json
// ProposalModel, H-049): Muse Spark 1.3 and Muse Wonder 1.0, by their exact
// model IDs. Whether this deployment takes one is Service.Models'.
var ProposalModels = []string{"muse-spark-1.3", "muse-wonder-1.0"}

func (e *UnavailableError) Error() string {
	switch {
	case e.Model:
		return "proposals: the model is not approved for proposals in this deployment (AI_POLICY_NOT_APPROVED, AI_MODEL_NOT_APPROVED)"
	case e.Research && e.Availability == PolicyNotApproved:
		return "proposals: web research is not approved for this deployment's provider (AI_POLICY_NOT_APPROVED, AI_RESEARCH_UNSUPPORTED)"
	case e.Research:
		return "proposals: this deployment's provider does not research (AI_PROVIDER_NOT_CONFIGURED, AI_RESEARCH_UNSUPPORTED)"
	case e.Photo && e.Availability == PolicyNotApproved:
		return "proposals: the photo path is not approved (AI_POLICY_NOT_APPROVED, AI_IMAGE_UNSUPPORTED)"
	case e.Photo:
		return "proposals: the photo path is not configured (AI_PROVIDER_NOT_CONFIGURED, AI_IMAGE_UNSUPPORTED)"
	case e.Availability == PolicyNotApproved:
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
	// Photo is this deployment's photo path's availability (cmd/api derives
	// it from the photo path's settings as the worker decides them, the
	// T-056 follow-up, D-124); the zero value refuses every photo, whatever
	// AI says of proposals in words.
	Photo Availability
	// Policy is the queue's (jobs.DefaultPolicy when zero).
	Policy jobs.Policy
	// Objects stores the photos proposals are asked for with (T-056,
	// D-044); nil when object storage is disabled, so a photo is refused
	// (503) and a proposal in words is not affected.
	Objects objects.Store
	// Research is whether this deployment's provider may search the web for
	// both proposal roles (H-046 (2); cmd/api derives it from the settings
	// the worker's live gate reads and the approval's data policy, never
	// from a key); the zero value refuses every request that asks for
	// research, whatever AI says of proposals without it.
	Research Availability
	// Models are the models a person may choose for a proposal in words
	// (H-049; cmd/api takes them from the approval the worker's live gate
	// accepts, aiapproval.Approval.ProposalModels, never from a key; both of
	// ProposalModels with the fake); a request that names another is
	// refused, and nil refuses every request that names one. A request that
	// names none asks the deployment's model.
	Models []string
	// PhotoModels are Models for a photo: the photo path's approval's.
	PhotoModels []string
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

// checkModel refuses a model this deployment's approval does not list for
// proposals (H-049): Models, or PhotoModels for a photo. "" is the
// deployment's model.
func (s *Service) checkModel(model string, photo bool) error {
	allowed := s.Models
	if photo {
		allowed = s.PhotoModels
	}
	if model == "" || slices.Contains(allowed, model) {
		return nil
	}
	return &UnavailableError{Availability: PolicyNotApproved, Model: true}
}

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
	// Research: the person asks for web research first (H-046 (2)).
	Research bool `json:"research,omitzero"`
	// Model: the model the person chose (H-049); "" for the deployment's.
	Model string `json:"model,omitzero"`
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
		if req.Research && s.Research != Available {
			return &UnavailableError{Availability: s.Research, Research: true}
		}
		if err := s.checkModel(req.Model, false); err != nil {
			return err
		}
		p, err := s.prepare(ctx, tx, m, modelID, req)
		if err != nil {
			return err
		}
		if p, err = store.Insert(ctx, tx, p); err != nil {
			return err
		}
		if _, err := jobs.Enqueue(ctx, tx, proposalJob(m.OrganizationID, p.ID, requestID, req.Research, req.Model), s.policy()); err != nil {
			return err
		}
		// IDs, the mode, the language and a count: never the intent
		// (customer prose) or the selected IDs (SECURITY §9); whether the
		// person asked for research (H-046 (2)); the model they chose
		// (H-049).
		meta := map[string]any{"model_id": p.ModelID, "base_version_id": p.BaseVersionID, "mode": p.Mode,
			"output_locale": p.OutputLocale, "selected_entities": len(p.SelectedEntityIDs), "source": "intent"}
		if req.Research {
			meta["research"] = true
		}
		if req.Model != "" {
			meta["requested_model"] = req.Model
		}
		if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
			Action: "proposal.create", TargetType: "ai_proposal", TargetID: p.ID, RequestID: requestID, Metadata: meta}); err != nil {
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

// ResearchJobAttempts is the attempts of a research proposal's job (H-046
// (2), review round 1 M1): one. A research attempt may be billed for a
// whole server-side search loop whose cost nothing bounds but the attempt's
// timeout (provider.ResearchMaxRetries), so a failed one is never run
// again on its own: the proposal fails, and the person asks again.
const ResearchJobAttempts = 1

// proposalJob is the model_proposal job of proposal id: its frozen input
// (the proposal and the request; the person's choice of research and of
// model, H-049, each absent when not made) and, for research, a single
// attempt.
func proposalJob(org, id, requestID string, research bool, model string) jobs.New {
	j := jobs.New{ID: ids.New("job"), OrganizationID: org, Type: jobs.ModelProposal, Resource: jobs.Resource{AIProposalID: id},
		Payload: map[string]any{"proposal_id": id, "request_id": requestID}}
	if research {
		j.Payload["research"] = true
		j.MaxAttempts = ResearchJobAttempts
	}
	if model != "" {
		j.Payload["model"] = model
	}
	return j
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
	mdl, v, err := checkBase(ctx, tx, m.OrganizationID, modelID, req.BaseVersionID, true)
	if err != nil {
		return store.Proposal{}, err
	}
	var selected []string
	if req.Context != nil {
		selected = req.Context.SelectedEntityIDs
	}
	return s.bind(m, mdl, v, store.Proposal{ID: ids.New("aip"), Mode: req.Mode, Intent: req.Intent, OutputLocale: string(loc),
		SelectedEntityIDs: selected})
}

// bind binds what the server decides into p, a proposal on version v of
// mdl, both checked in the caller's transaction: its organization, model,
// base, creator and ID pool (D-046).
func (s *Service) bind(m tenant.Membership, mdl models.Model, v models.Version, p store.Proposal) (store.Proposal, error) {
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
	p.OrganizationID, p.ModelID, p.BaseVersionID, p.IDPool, p.CreatedBy = m.OrganizationID, mdl.ID, v.ID, poolJSON, m.UserID
	return p, nil
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
