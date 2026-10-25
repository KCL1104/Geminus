// Package approval is the human decision on an AI proposal (T-026; AI_SPEC
// §6, API_DATABASE §4, SECURITY §5; D-003, D-004, D-011, D-027, D-030,
// D-046, D-062; D-067, D-068, D-069 APPROVED). A person, an editor or owner
// of the proposal's organization, approves a ready proposal, which then has
// exactly one effect, or rejects it, which has none. The AI never decides:
// this package calls no provider, the worker cannot link it (architecture
// rule decisions-only-in-the-api) and cannot write a version, a scenario or
// a ready proposal (0002), and nothing a provider said grants anything: the
// server reviews the stored content again before it applies it.
//
// The HTTP adapter checks the request itself first (path, media type,
// body, the Idempotency-Key header). Each decision is then one
// tenant.DB.Run transaction: the caller's membership and role first
// (D-008), then the Idempotency-Key (D-011: the same key and body replay
// the stored answer, a replay still needs the role; the fingerprint holds
// the proposal, since the key's scope does not), then the
// model row, its draft and the proposal, locked in that order (D-003, the
// lock order of a publish, ADR-022), then the checks, the one effect, the
// proposal's decision, the audit event and the idempotency response,
// committed together. An approval whose base is no longer the model's
// latest version makes the proposal conflicted (AI_SPEC §6: no rebase) and
// answers 409; that is the one refusal that writes. The database checks
// the effect again when the transaction commits (0001, 0013). SQL lives in
// internal/storage.
package approval

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	modelsvc "github.com/kcl1104/geminus/internal/models"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/datasets"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	models "github.com/kcl1104/geminus/internal/storage/models"
	"github.com/kcl1104/geminus/internal/storage/objects"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/storage/scenarios"
	"github.com/kcl1104/geminus/internal/storage/snapshots"
	"github.com/kcl1104/geminus/internal/tenant"
)

var (
	// ErrNotFound: no such proposal in the organization (or no access);
	// the same for both (TEST-009).
	ErrNotFound = errors.New("approval: not found")
	// ErrOtherBase: expected_base_version_id is not the proposal's base
	// (409 MODEL_VERSION_CONFLICT at /expected_base_version_id); nothing
	// changed.
	ErrOtherBase = errors.New("approval: expected_base_version_id is not the proposal's base")
	// ErrInvalidComment: a comment over 2000 characters, with U+0000 or
	// not UTF-8 (422 at /comment). The HTTP layer refuses these first
	// (the Approve and Reject schemas, httpapi.DecodeJSON).
	ErrInvalidComment = errors.New("approval: invalid comment")
	// ErrDatasetsUnavailable: the base binds datasets and no object
	// storage is configured to verify them again (503); nothing changed.
	ErrDatasetsUnavailable = errors.New("approval: the base binds datasets and dataset storage is not configured")
)

// DecidedError: the proposal was decided before (409
// PROPOSAL_ALREADY_DECIDED, D-030): approved, rejected, or conflicted by
// an earlier approval. Nothing changed, whatever the key.
type DecidedError struct{ Status store.Status }

func (e *DecidedError) Error() string { return "approval: the proposal is already " + string(e.Status) }

// StateError: the proposal is in no state the decision may leave (409
// LIFECYCLE_CONFLICT): approving needs ready, rejecting ready or
// needs_input. Nothing changed.
type StateError struct {
	Status   store.Status
	Decision string // "approve" or "reject"
}

func (e *StateError) Error() string {
	return fmt.Sprintf("approval: a %s proposal cannot be %sd", e.Status, strings.TrimSuffix(e.Decision, "e"))
}

// RiskChangedError: the server's risk of the proposal, computed again, is
// not the one the person reviewed (409 LIFECYCLE_CONFLICT at Path,
// D-069 (b) APPROVED): a new proposal is needed. Nothing changed.
type RiskChangedError struct{ Path, Reviewed, Now string }

func (e *RiskChangedError) Error() string {
	return fmt.Sprintf("approval: the risk at %s is now %s, not the reviewed %s", e.Path, e.Now, e.Reviewed)
}

// InvalidError: the proposal does not pass the server's review again (422
// MODEL_VALIDATION_FAILED); its issues are cleaned (fixed sentences, schema
// paths: D-061 (f)). The proposal stays ready; nothing changed.
type InvalidError struct{ Issues []model.ValidationIssue }

func (e *InvalidError) Error() string {
	return fmt.Sprintf("approval: the proposal does not pass the review again (%d issue(s))", len(e.Issues))
}

// PackLookup finds a model's domain pack; *domainpack.Registry is one.
type PackLookup interface {
	Lookup(domain string) (domainpack.DomainPack, error)
}

// Service runs decisions for a verified scope.
type Service struct {
	DB    *tenant.DB
	Packs PackLookup
	// Objects holds the dataset snapshots a base version's manifest binds
	// (D-044); nil when object storage is disabled, and then approving a
	// proposal whose base binds datasets is refused with 503, since the
	// binding stage cannot verify them again.
	Objects objects.Store
}

// The roles a decision requires (x-required-roles; SECURITY §2).
var writeRoles = tenant.Writers

const (
	approveOperation = "approveProposal"
	rejectOperation  = "rejectProposal"
	// maxName is a scenario's longest name (scenario.schema.json).
	maxName = 200
)

// ApproveRequest is http.schema.json#/$defs/Approve; RejectRequest is
// …/Reject. Callers have validated them against the schema.
type (
	ApproveRequest struct {
		ExpectedBaseVersionID string `json:"expected_base_version_id"`
		Comment               string `json:"comment,omitzero"`
	}
	RejectRequest struct {
		Comment string `json:"comment,omitzero"`
	}
)

// EditApproval and ScenarioApproval are the two shapes of
// http.schema.json#/$defs/Approval, told apart by mode.
type (
	EditApproval struct {
		ProposalID     string `json:"proposal_id"`
		Status         string `json:"status"`
		Mode           string `json:"mode"`
		NewVersionID   string `json:"new_version_id"`
		Version        int64  `json:"version"`
		PatchHash      string `json:"patch_hash"`
		DraftOutOfDate bool   `json:"draft_out_of_date"`
	}
	ScenarioApproval struct {
		ProposalID string `json:"proposal_id"`
		Status     string `json:"status"`
		Mode       string `json:"mode"`
		ScenarioID string `json:"scenario_id"`
		PatchHash  string `json:"patch_hash"`
	}
)

// decisionComment is the comment a decision keeps (D-068 APPROVED): nil
// for none or an empty one; at most 2000 characters (code points, as JSON
// Schema and PostgreSQL count them), without U+0000, which PostgreSQL text
// cannot store.
func decisionComment(c string) (*string, error) {
	if c == "" {
		return nil, nil
	}
	if !utf8.ValidString(c) || utf8.RuneCountInString(c) > 2000 || strings.ContainsRune(c, 0) {
		return nil, ErrInvalidComment
	}
	return &c, nil
}

// decisionBody is the body of either decision (Approve, Reject) as the
// fingerprint reads it: an empty comment is no comment (D-068), so it is
// left out.
type decisionBody struct {
	ExpectedBaseVersionID string `json:"expected_base_version_id,omitzero"`
	Comment               string `json:"comment,omitzero"`
}

// fingerprint is a decision's idempotency hash (D-011, D-069 (a)): the
// proposal of the URL and the normalized parsed body (API_DATABASE §7).
// The key's scope does not hold the proposal, so this is what tells the
// same key used on another proposal apart; bodies that differ only in
// member order, spacing or an empty comment are one request. A member the
// schemas do not have is an error, never left out.
func fingerprint(proposalID string, body []byte) (string, error) {
	var parsed decisionBody
	if err := json.Unmarshal(body, &parsed, json.RejectUnknownMembers(true)); err != nil {
		return "", err
	}
	normalized, err := json.Marshal(parsed, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	canonical, err := hashing.Canonical(normalized)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(map[string]any{"proposal_id": proposalID, "body": jsontext.Value(canonical)}, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	return hashing.Of(data)
}

// replayed is the stored answer r as request requestID, with the same key
// and body, gets it again: byte for byte, except that an error answer (the
// 409 of a conflict, the only one a decision stores) names this request,
// as every error answer does: error.request_id is the response's
// X-Request-ID (D-069 (a), review L1). The first request is in the audit
// event of the conflict.
func replayed(r idempotency.Response, requestID string) ([]byte, error) {
	if r.Status < 400 {
		return hashing.Canonical(r.Body)
	}
	var e proposals.ErrorEnvelope
	if err := json.Unmarshal(r.Body, &e, json.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("approval: a stored %d is not an error envelope: %w", r.Status, err)
	}
	e.Error.RequestID = requestID
	data, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return hashing.Canonical(data)
}

// locked is what a decision holds: the model row, its draft and the
// proposal, locked in that order.
type locked struct {
	proposal store.Proposal
	model    models.Model
	draft    models.Draft
}

// lock reads proposal id, then locks its model row, the model's draft and
// the proposal (D-003's order: every write of a model takes its row first,
// so a decision, a publish and a draft edit of one model run one after
// another), and returns the proposal as it is under the lock.
func lock(ctx context.Context, tx tenant.Tx, org, id string) (locked, error) {
	p, err := store.Get(ctx, tx, org, id)
	if errors.Is(err, store.ErrNotFound) {
		return locked{}, ErrNotFound
	}
	if err != nil {
		return locked{}, err
	}
	mdl, err := models.LockModel(ctx, tx, org, p.ModelID)
	if err != nil {
		return locked{}, fmt.Errorf("approval: the model of proposal %s: %w", id, err)
	}
	d, err := models.LockDraft(ctx, tx, org, p.ModelID)
	if err != nil {
		return locked{}, fmt.Errorf("approval: the draft of proposal %s: %w", id, err)
	}
	if p, err = store.Lock(ctx, tx, org, id); err != nil {
		return locked{}, err
	}
	return locked{proposal: p, model: mdl, draft: d}, nil
}

func decidedStatus(s store.Status) bool {
	return s == store.Approved || s == store.Rejected || s == store.Conflicted
}

// Approve approves proposal id for the scoped user, or replays the answer
// of the earlier request with the same Idempotency-Key. It returns the HTTP
// status and body: 201 and the Approval, or 409 and the error envelope when
// the approval found the base moved and made the proposal conflicted (the
// key's stored answer). Every other refusal is an error and changes
// nothing.
func (s *Service) Approve(ctx context.Context, sc tenant.Scope, requestID, key, id string, body []byte, req ApproveRequest) (int, []byte, error) {
	comment, err := decisionComment(req.Comment)
	if err != nil {
		return 0, nil, err
	}
	hash, err := fingerprint(id, body)
	if err != nil {
		return 0, nil, err
	}
	k := idempotency.Key{Org: sc.OrganizationID, Actor: sc.UserID, Operation: approveOperation, Key: key}
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
			out, err = replayed(*replay, requestID)
			return err
		}
		l, err := lock(ctx, tx, m.OrganizationID, id)
		if err != nil {
			return err
		}
		p := l.proposal
		switch {
		case decidedStatus(p.Status):
			return &DecidedError{Status: p.Status}
		case p.Status != store.Ready:
			return &StateError{Status: p.Status, Decision: "approve"}
		}
		if p.BaseVersionID != l.model.LatestVersionID {
			if status, out, err = s.conflict(ctx, tx, m, requestID, l); err != nil {
				return err
			}
			return idempotency.Complete(ctx, tx, k, idempotency.Response{Status: status, Body: out})
		}
		if req.ExpectedBaseVersionID != p.BaseVersionID {
			return ErrOtherBase
		}
		resp, err := s.apply(ctx, tx, m, requestID, l, comment)
		if err != nil {
			return err
		}
		data, err := json.Marshal(resp)
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

// conflict makes the ready proposal of l conflicted for the caller: its
// base is no longer the model's latest version, and it is never rebased
// (AI_SPEC §6). It returns the 409 the approval answers, which is stored
// as the key's answer like a success.
func (s *Service) conflict(ctx context.Context, tx tenant.Tx, m tenant.Membership, requestID string, l locked) (int, []byte, error) {
	p := l.proposal
	if err := store.Conflict(ctx, tx, m.OrganizationID, p.ID, m.UserID); err != nil {
		return 0, nil, err
	}
	if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
		Action: "proposal.conflict", TargetType: "ai_proposal", TargetID: p.ID, RequestID: requestID,
		Metadata: map[string]any{"mode": p.Mode, "model_id": p.ModelID, "base_version_id": p.BaseVersionID,
			"latest_version_id": l.model.LatestVersionID}}); err != nil {
		return 0, nil, err
	}
	e := proposals.ErrorEnvelope{Error: proposals.ErrorBody{Code: "MODEL_VERSION_CONFLICT", RequestID: requestID,
		Message: "the model's latest version is no longer this proposal's base; the proposal is now conflicted and nothing was applied (ask for a new proposal on the latest version)",
		Details: []proposals.ErrorDetail{{Path: "/expected_base_version_id", Reason: "the proposal's base is not the model's latest version"}}}}
	data, err := json.Marshal(e)
	if err != nil {
		return 0, nil, err
	}
	out, err := hashing.Canonical(data)
	return 409, out, err
}

// apply reviews the ready proposal of l again (recheck), runs the dataset
// binding stage against its base's manifest, creates its one effect and
// records the approval and its audit event.
func (s *Service) apply(ctx context.Context, tx tenant.Tx, m tenant.Membership, requestID string, l locked, comment *string) (any, error) {
	org, p := m.OrganizationID, l.proposal
	base, err := models.GetVersion(ctx, tx, org, p.BaseVersionID)
	if err != nil {
		return nil, fmt.Errorf("approval: the base of proposal %s: %w", p.ID, err)
	}
	pack, err := s.Packs.Lookup(l.model.Domain)
	if err != nil {
		return nil, err
	}
	r, err := recheck(p, base.IR, pack)
	if err != nil {
		return nil, err
	}
	// The manifest a new version freezes is the proposal's dataset_bindings,
	// the base's whole manifest (D-062 (g), TWIN_IR_SPEC §5.2); a proposal
	// that holds another one is the server's fault, never approved.
	manifest := sorted(base.DatasetBindings)
	if manifest == nil || !slices.Equal(manifest, sorted(p.DatasetBindings)) {
		return nil, fmt.Errorf("approval: proposal %s does not bind its base's dataset manifest", p.ID)
	}
	// The dataset binding stage (D-006, D-033), with the snapshots read
	// again now: the candidate's empirical distributions are bound in that
	// manifest and every snapshot verifies.
	if len(manifest) > 0 && s.Objects == nil {
		return nil, ErrDatasetsUnavailable
	}
	keys, err := snapshots.Keys(ctx, tx, org, manifest)
	if err != nil {
		return nil, err
	}
	snaps := snapshots.Store{Keys: keys, Objects: s.Objects}
	resolution, err := datasets.Resolve(ctx, snaps, org, r.ir, manifest)
	if err != nil {
		// A snapshot that cannot be read is a datasets.ReadError (503
		// DATASET_STORAGE_FAILED, as the generator's, D-061 (c); review
		// I2); the ended request's context error, or a server fault (500),
		// otherwise (T-054 review L1, L2).
		return nil, datasets.Failure(ctx, err)
	}
	if o := r.outcome.Refuse(withoutIR(resolution.Issues)); o.Status != r.outcome.Status {
		return nil, &InvalidError{Issues: o.Validation.Issues}
	}
	common := map[string]any{"mode": p.Mode, "model_id": p.ModelID, "base_version_id": p.BaseVersionID, "patch_hash": r.outcome.PatchHash,
		"server_risk_level": string(r.outcome.ServerRisk), "risk_level": string(r.outcome.Risk)}
	audit := func(meta map[string]any) error {
		// IDs, hashes and risk: never the comment, the person's text (D-041,
		// D-068), nor anything the provider wrote (SECURITY §9).
		return auditlog.AppendUser(ctx, tx, org, m.UserID, auditlog.Event{Action: "proposal.approve", TargetType: "ai_proposal",
			TargetID: p.ID, RequestID: requestID, Metadata: meta})
	}
	switch p.Mode {
	case "edit":
		v, err := models.InsertVersion(ctx, tx, models.Version{ID: ids.New("mv"), OrganizationID: org, ModelID: p.ModelID,
			Version: base.Version + 1, SchemaVersion: r.ir.SchemaVersion, ParentVersionID: &base.ID, SourceProposalID: &p.ID,
			IR: r.candidate, Hash: r.hash, DatasetBindings: manifest, Comment: comment, CreatedBy: m.UserID})
		if err != nil {
			return nil, err
		}
		// The draft rule of ADR-022 (D-003): a clean draft moves to the new
		// version, a dirty one is kept as it is and is out of date. The move
		// checks the draft's bindings with the snapshots read again; one that
		// cannot be read is a datasets.ReadError there too (models applies
		// datasets.Failure), the same 503 as the revalidation's, and nothing
		// is committed.
		svc := modelsvc.Service{DB: s.DB, Packs: s.Packs, Datasets: snaps}
		d, err := svc.Advance(ctx, tx, m, l.model, l.draft, base, v, r.ir.Name)
		if err != nil {
			return nil, err
		}
		if err := store.Approve(ctx, tx, org, p.ID, m.UserID, &v.ID, nil, comment); err != nil {
			return nil, err
		}
		outOfDate := d.BaseVersionID != v.ID
		common["new_version_id"], common["version"], common["draft_out_of_date"] = v.ID, v.Version, outOfDate
		if err := audit(common); err != nil {
			return nil, err
		}
		return EditApproval{ProposalID: p.ID, Status: string(store.Approved), Mode: p.Mode, NewVersionID: v.ID, Version: v.Version,
			PatchHash: r.outcome.PatchHash, DraftOutOfDate: outOfDate}, nil
	case "scenario":
		// An immutable what-if on the base: no version, no draft change,
		// no run (D-004, AI_SPEC §7).
		sc, err := scenarios.Insert(ctx, tx, scenarios.Scenario{ID: ids.New("scn"), OrganizationID: org, ModelID: p.ModelID,
			BaseVersionID: base.ID, Name: scenarioName(*p.Summary, p.ID), Patch: r.outcome.Changes, PatchHash: r.outcome.PatchHash,
			SourceProposalID: &p.ID, CreatedBy: m.UserID})
		if err != nil {
			return nil, err
		}
		if err := store.Approve(ctx, tx, org, p.ID, m.UserID, nil, &sc.ID, comment); err != nil {
			return nil, err
		}
		common["scenario_id"] = sc.ID
		if err := audit(common); err != nil {
			return nil, err
		}
		return ScenarioApproval{ProposalID: p.ID, Status: string(store.Approved), Mode: p.Mode, ScenarioID: sc.ID,
			PatchHash: r.outcome.PatchHash}, nil
	}
	return nil, fmt.Errorf("approval: proposal %s has mode %q", p.ID, p.Mode)
}

// sorted is a manifest in D-034 order ([] for none), or nil when it binds a
// logical_ref twice.
func sorted(manifest []model.DatasetBinding) []model.DatasetBinding {
	out, err := hashing.SortManifest(manifest)
	if err != nil {
		return nil // never equal to a verified manifest
	}
	return out
}

// scenarioName is the name of the scenario an approval creates (D-067
// APPROVED): the proposal's summary, which the person reviewed, each run
// of white space made one space, cut at maxName characters with "…"; the
// proposal's ID when the summary is only white space.
func scenarioName(summary, id string) string {
	name := strings.Join(strings.Fields(summary), " ")
	if name == "" {
		return id
	}
	if r := []rune(name); len(r) > maxName {
		return strings.TrimRightFunc(string(r[:maxName-1]), unicode.IsSpace) + "…"
	}
	return name
}

// Reject rejects proposal id for the scoped user, or replays the answer of
// the earlier request with the same Idempotency-Key: 200 and the proposal
// as getProposal shows it. It changes no model, draft, version or scenario
// (TEST-006).
func (s *Service) Reject(ctx context.Context, sc tenant.Scope, requestID, key, id string, body []byte, req RejectRequest) (int, []byte, error) {
	comment, err := decisionComment(req.Comment)
	if err != nil {
		return 0, nil, err
	}
	hash, err := fingerprint(id, body)
	if err != nil {
		return 0, nil, err
	}
	k := idempotency.Key{Org: sc.OrganizationID, Actor: sc.UserID, Operation: rejectOperation, Key: key}
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
			out, err = replayed(*replay, requestID)
			return err
		}
		// The approval's lock order, so that an approval and a rejection
		// of one proposal run one after the other.
		l, err := lock(ctx, tx, m.OrganizationID, id)
		if err != nil {
			return err
		}
		p := l.proposal
		switch {
		case decidedStatus(p.Status):
			return &DecidedError{Status: p.Status}
		case p.Status != store.Ready && p.Status != store.NeedsInput:
			return &StateError{Status: p.Status, Decision: "reject"}
		}
		if err := store.Reject(ctx, tx, m.OrganizationID, p.ID, m.UserID, comment); err != nil {
			return err
		}
		if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
			Action: "proposal.reject", TargetType: "ai_proposal", TargetID: p.ID, RequestID: requestID,
			Metadata: map[string]any{"mode": p.Mode, "model_id": p.ModelID, "base_version_id": p.BaseVersionID,
				"from": string(p.Status)}}); err != nil {
			return err
		}
		now, err := store.Get(ctx, tx, m.OrganizationID, p.ID)
		if err != nil {
			return err
		}
		pub, err := proposals.Public(now, requestID)
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
		status = 200
		return idempotency.Complete(ctx, tx, k, idempotency.Response{Status: status, Body: out})
	})
	return status, out, err
}
