// Package effect holds the canonical effects of a ready proposal that a
// person's approval and an AI investigation share (H-056 §6.2, H-061 §6.4,
// slice A4; PROPOSED, behind GEMINUS_INVESTIGATIONS):
//
//   - Recheck and Check: the server reviews the stored content again as it
//     reviewed the provider's answer, trusting none of the stored results,
//     then runs the dataset binding stage against the base's whole manifest
//     with the snapshots read again (D-069 (b)). internal/proposals/approval
//     runs exactly these before it approves.
//   - NewScenario: the one scenario a scenario proposal's effect is (D-067:
//     named after the reviewed summary).
//   - Service.SaveScenario and Service.WriteDraft: the AI's action on its own
//     ready proposal, inside its investigation's acting-for transaction for
//     the person who started it (tenant.Membership.ActingFor): save the
//     scenario, or write the edit into a draft that holds none of the
//     person's changes. The proposal becomes applied, never approved: no
//     decider, no version (0019 refuses either). Each returns a *Refusal (an
//     InvestigationToolCode) for what the AI reacts to, for the executor's
//     savepoint; any other error is the transaction's.
//
// It never publishes, approves or rejects: no version is written here
// (TestNoPublishOrDecision), and the architecture rule
// proposal-effects-only-in-the-api keeps the worker, its job handlers and
// internal/ai from linking it. SQL lives in internal/storage.
package effect

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
	modelsvc "github.com/kcl1104/geminus/internal/models"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/datasets"
	"github.com/kcl1104/geminus/internal/storage/investigations"
	models "github.com/kcl1104/geminus/internal/storage/models"
	"github.com/kcl1104/geminus/internal/storage/objects"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/storage/scenarios"
	"github.com/kcl1104/geminus/internal/storage/snapshots"
	"github.com/kcl1104/geminus/internal/tenant"
)

// InvalidError: the proposal does not pass the server's review again; its
// issues are cleaned (fixed sentences, schema paths: D-061 (f)). Nothing
// changed.
type InvalidError struct{ Issues []model.ValidationIssue }

func (e *InvalidError) Error() string {
	return fmt.Sprintf("effect: the proposal does not pass the review again (%d issue(s))", len(e.Issues))
}

// RiskChangedError: the server's risk of the proposal, computed again, is
// not the one stored with it (D-069 (b)). Nothing changed.
type RiskChangedError struct{ Path, Reviewed, Now string }

func (e *RiskChangedError) Error() string {
	return fmt.Sprintf("effect: the risk at %s is now %s, not the reviewed %s", e.Path, e.Now, e.Reviewed)
}

// ErrDatasetsUnavailable: the base binds datasets and no object storage is
// configured to verify them again; nothing changed.
var ErrDatasetsUnavailable = errors.New("effect: the base binds datasets and dataset storage is not configured")

// Rechecked is a ready proposal reviewed again against its base.
type Rechecked struct {
	Outcome review.Outcome
	// Candidate is the base with the reviewed changes applied, in RFC 8785
	// form (the bytes a version or a draft stores, D-040), Hash its hash, IR
	// it decoded.
	Candidate []byte
	Hash      string
	IR        *model.ModelIR
}

// Recheck reviews ready proposal p again against its base version's IR, as
// the server reviewed the provider's answer (AI_SPEC §6, SECURITY §5;
// D-069 (b) APPROVED): what the proposal holds is the answer as the review
// kept it (its summary, assumptions, canonical changes, expected effects,
// confidence and the provider's risk claim), so the same review runs every
// server check again (patch profile and allowlist of the proposal's mode,
// full candidate validation, the proposal's ID pool, the domain pack's
// rules, the numbers of the expected effects, the server's own risk) and
// trusts none of the stored results. It is pure: no database, no provider.
//
// An answer the review refuses now is an *InvalidError (cleaned issues). A
// server risk other than the stored one is a *RiskChangedError. Changes
// that are not the stored patch's canonical form, or another patch hash,
// are a server fault (an error), never applied.
func Recheck(p store.Proposal, base []byte, pack domainpack.DomainPack) (Rechecked, error) {
	if p.Summary == nil || p.Patch == nil || p.PatchHash == nil || p.ServerRisk == nil || p.ProviderRisk == nil ||
		p.Risk == nil || p.Confidence == nil {
		return Rechecked{}, fmt.Errorf("effect: ready proposal %s lacks its reviewed content", p.ID)
	}
	mode := provider.Mode(p.Mode)
	var pm patch.Mode
	switch mode {
	case provider.ModeEdit:
		pm = patch.AIEdit
	case provider.ModeScenario:
		pm = patch.Scenario
	default:
		return Rechecked{}, fmt.Errorf("effect: proposal %s has mode %q", p.ID, p.Mode)
	}
	var pool idpool.Pool
	if err := json.Unmarshal(p.IDPool, &pool); err != nil {
		return Rechecked{}, fmt.Errorf("effect: proposal %s: the stored ID pool: %w", p.ID, err)
	}
	var changes model.Patch
	if err := json.Unmarshal(p.Patch, &changes); err != nil {
		return Rechecked{}, fmt.Errorf("effect: proposal %s: the stored patch: %w", p.ID, err)
	}
	confidence := *p.Confidence
	o, err := review.Review(review.Input{Mode: mode, Base: base, Pool: pool, Pack: pack, Output: provider.ProposalOutput{
		Status: "proposed", Summary: *p.Summary, Assumptions: p.Assumptions, RiskLevel: *p.ProviderRisk, Confidence: &confidence,
		Changes: changes, ExpectedEffects: p.ExpectedEffects, RequiresConfirmation: true}})
	if err != nil {
		return Rechecked{}, fmt.Errorf("effect: reviewing proposal %s again: %w", p.ID, err)
	}
	if o.Status != review.Ready {
		return Rechecked{}, &InvalidError{Issues: o.Validation.Issues}
	}
	stored, err := hashing.Canonical(p.Patch)
	if err != nil {
		return Rechecked{}, fmt.Errorf("effect: proposal %s: the stored patch: %w", p.ID, err)
	}
	if !bytes.Equal(stored, o.Changes) || o.PatchHash != *p.PatchHash {
		return Rechecked{}, fmt.Errorf("effect: proposal %s: the stored patch is not the one its stored hash covers", p.ID)
	}
	switch {
	case string(o.Risk) != *p.Risk:
		return Rechecked{}, &RiskChangedError{Path: "/risk_level", Reviewed: *p.Risk, Now: string(o.Risk)}
	case string(o.ServerRisk) != *p.ServerRisk:
		return Rechecked{}, &RiskChangedError{Path: "/server_risk_level", Reviewed: *p.ServerRisk, Now: string(o.ServerRisk)}
	}
	res, err := patch.Apply(base, o.Changes, pm)
	if err != nil {
		return Rechecked{}, err
	}
	if !res.Valid() { // the review applied the same patch to the same base
		return Rechecked{}, fmt.Errorf("effect: proposal %s: the reviewed patch does not apply again", p.ID)
	}
	candidate, err := hashing.Canonical(res.Candidate)
	if err != nil {
		return Rechecked{}, err
	}
	return Rechecked{Outcome: o, Candidate: candidate, Hash: hashing.Sum(candidate), IR: res.Validation.IR}, nil
}

// Checked is a rechecked proposal whose candidate passed the dataset
// binding stage against its base's whole manifest.
type Checked struct {
	Rechecked
	// Manifest is the base's whole manifest in D-034 order ([] for none):
	// the proposal's dataset_bindings (D-062 (g)).
	Manifest []model.DatasetBinding
	// Snapshots opens that manifest's snapshots, read again for this check;
	// a draft write or sync that follows uses the same.
	Snapshots snapshots.Store
}

// Check is the revalidation before a ready proposal's one effect (D-069
// (b) APPROVED), in the caller's transaction: Recheck against base, then
// the dataset binding stage with the snapshots read now: the candidate's
// empirical distributions are bound in base's manifest and every snapshot
// verifies. A proposal that holds another manifest than its base's is the
// server's fault (an error); a manifest without object storage is
// ErrDatasetsUnavailable; a snapshot that cannot be read is
// datasets.Failure's (a datasets.ReadError while the request lives); a
// binding issue is an *InvalidError.
func Check(ctx context.Context, tx tenant.Tx, org string, objs objects.Store, pack domainpack.DomainPack, p store.Proposal, base models.Version) (Checked, error) {
	r, err := Recheck(p, base.IR, pack)
	if err != nil {
		return Checked{}, err
	}
	manifest := sorted(base.DatasetBindings)
	if manifest == nil || !slices.Equal(manifest, sorted(p.DatasetBindings)) {
		return Checked{}, fmt.Errorf("effect: proposal %s does not bind its base's dataset manifest", p.ID)
	}
	if len(manifest) > 0 && objs == nil {
		return Checked{}, ErrDatasetsUnavailable
	}
	keys, err := snapshots.Keys(ctx, tx, org, manifest)
	if err != nil {
		return Checked{}, err
	}
	snaps := snapshots.Store{Keys: keys, Objects: objs}
	resolution, err := datasets.Resolve(ctx, snaps, org, r.IR, manifest)
	if err != nil {
		// A snapshot that cannot be read is a datasets.ReadError (503
		// DATASET_STORAGE_FAILED on a person's route, D-061 (c)); the ended
		// request's context error, or a server fault, otherwise.
		return Checked{}, datasets.Failure(ctx, err)
	}
	if o := r.Outcome.Refuse(WithoutIR(resolution.Issues)); o.Status != r.Outcome.Status {
		return Checked{}, &InvalidError{Issues: o.Validation.Issues}
	}
	return Checked{Rechecked: r, Manifest: manifest, Snapshots: snaps}, nil
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

// WithoutIR rewrites the dataset binding stage's /ir/... paths as paths
// into the candidate, as the other model issues are; manifest paths stay.
func WithoutIR(issues []model.ValidationIssue) []model.ValidationIssue {
	out := make([]model.ValidationIssue, len(issues))
	for i, is := range issues {
		if rest, ok := strings.CutPrefix(is.Path, "/ir"); ok && (rest == "" || rest[0] == '/') {
			is.Path = rest
		}
		out[i] = is
	}
	return validate.Sort(out)
}

// maxName is a scenario's longest name (scenario.schema.json).
const maxName = 200

// ScenarioName is the name of the scenario a scenario proposal's effect
// creates (D-067 APPROVED): the proposal's summary, which the person
// reviewed, each run of white space made one space, cut at maxName
// characters with "…"; the proposal's ID when the summary is only white
// space.
func ScenarioName(summary, id string) string {
	name := strings.Join(strings.Fields(summary), " ")
	if name == "" {
		return id
	}
	if r := []rune(name); len(r) > maxName {
		return strings.TrimRightFunc(string(r[:maxName-1]), unicode.IsSpace) + "…"
	}
	return name
}

// NewScenario is the one scenario checked scenario proposal p's effect is,
// created by createdBy: an immutable what-if on its base with the reviewed
// changes and their hash (D-004, AI_SPEC §7), naming p as its source. In an
// AI investigation's acting-for transaction the database labels it (0019).
func NewScenario(org string, p store.Proposal, c Checked, createdBy string) scenarios.Scenario {
	return scenarios.Scenario{ID: ids.New("scn"), OrganizationID: org, ModelID: p.ModelID, BaseVersionID: p.BaseVersionID,
		Name: ScenarioName(*p.Summary, p.ID), Patch: c.Outcome.Changes, PatchHash: c.Outcome.PatchHash, SourceProposalID: &p.ID,
		CreatedBy: createdBy}
}

// Refusal is an AI effect's typed refusal (H-061 §6.2 step 6, §7): the
// InvestigationToolCode and the JSON Pointer into the call's arguments the
// executor records in the ledger, after rolling back to its savepoint.
// Nothing the call asked for took effect.
type Refusal struct {
	Code investigator.Code
	Path string
	// Issues are the codes of the issues a RECHECK_FAILED refusal found
	// (never their messages); nil otherwise.
	Issues []string
}

func (r *Refusal) Error() string { return fmt.Sprintf("effect: refused: %s at %q", r.Code, r.Path) }

// Unwrap makes every *Refusal investigator.ErrRefused (errors.Is).
func (r *Refusal) Unwrap() error { return investigator.ErrRefused }

var (
	// ErrNotActing: an AI effect was asked for outside an AI
	// investigation's acting-for transaction; nothing was read.
	ErrNotActing = errors.New("effect: the transaction does not act for an AI investigation")
	// ErrEnded: the investigation is not running (stopped or finished
	// first); nothing was written. The database refuses any write after
	// the end as well (0019).
	ErrEnded = errors.New("effect: the investigation is not running")
)

// PackLookup finds a model's domain pack; *domainpack.Registry is one.
type PackLookup interface {
	Lookup(domain string) (domainpack.DomainPack, error)
}

// Service runs the AI's effects in the caller's transaction.
type Service struct {
	Packs PackLookup
	// Objects holds the dataset snapshots a base's manifest binds (D-044);
	// nil when object storage is disabled, and then a proposal whose base
	// binds datasets is ErrDatasetsUnavailable.
	Objects objects.Store
}

// Saved is what SaveScenario made: investigator.schema.json's
// ScenarioObservation, but for the budget.
type Saved struct {
	ScenarioID, Name, ProposalID string
}

// Written is what WriteDraft made: DraftObservation, but for the budget.
type Written struct {
	DraftRevision  int64
	OperationCount int
}

// target is a ready proposal of the investigation, under the executor's
// lock order (H-061 §6.3): the model row, its draft, the investigation FOR
// SHARE, then the proposal.
type target struct {
	proposal store.Proposal
	model    models.Model
	draft    models.Draft
	base     models.Version
}

// proposalPath is where save_scenario's and write_to_draft's refusals point:
// their one argument.
const proposalPath = "/proposal_id"

// locate reads the investigation's proposal id in mode and takes the
// executor's locks: a proposal that is not this investigation's (or no
// proposal) is NOT_IN_INVESTIGATION; one a person decided first is
// PROPOSAL_DECIDED; one that is not ready (still generating, needs input,
// invalid, failed, or applied already) PROPOSAL_NOT_READY; one whose base
// is no longer the model's latest BASE_MOVED (never rebased, never made
// conflicted: the AI never decides).
func locate(ctx context.Context, tx tenant.Tx, m tenant.Membership, id, mode string) (target, error) {
	if m.ActingFor == "" {
		return target{}, ErrNotActing
	}
	if err := m.Require(tenant.Writers...); err != nil {
		return target{}, err
	}
	org := m.OrganizationID
	if !modelsvc.ValidID("aip", id) {
		return target{}, &Refusal{Code: investigator.NotInInvestigation, Path: proposalPath}
	}
	p, err := store.Get(ctx, tx, org, id)
	if errors.Is(err, store.ErrNotFound) || err == nil && (p.InvestigationID == nil || *p.InvestigationID != m.ActingFor) {
		return target{}, &Refusal{Code: investigator.NotInInvestigation, Path: proposalPath}
	}
	if err != nil {
		return target{}, err
	}
	if p.Mode != mode {
		return target{}, &Refusal{Code: investigator.ModeMismatch, Path: ""}
	}
	mdl, err := models.LockModel(ctx, tx, org, p.ModelID)
	if err != nil {
		return target{}, fmt.Errorf("effect: the model of proposal %s: %w", id, err)
	}
	d, err := models.LockDraft(ctx, tx, org, p.ModelID)
	if err != nil {
		return target{}, fmt.Errorf("effect: the draft of proposal %s: %w", id, err)
	}
	inv, err := investigations.Share(ctx, tx, org, m.ActingFor)
	if errors.Is(err, investigations.ErrNotFound) {
		return target{}, ErrEnded
	}
	if err != nil {
		return target{}, err
	}
	if inv.Status != investigations.Running {
		return target{}, ErrEnded
	}
	if inv.CreatedBy != m.UserID || inv.ModelID != p.ModelID {
		return target{}, fmt.Errorf("effect: investigation %s is not %s's on model %s", inv.ID, m.UserID, p.ModelID)
	}
	if p, err = store.Lock(ctx, tx, org, id); err != nil {
		return target{}, err
	}
	switch p.Status {
	case store.Ready:
	case store.Approved, store.Rejected, store.Conflicted:
		return target{}, &Refusal{Code: investigator.ProposalDecided, Path: proposalPath}
	default:
		return target{}, &Refusal{Code: investigator.ProposalNotReady, Path: proposalPath}
	}
	if p.BaseVersionID != mdl.LatestVersionID {
		return target{}, &Refusal{Code: investigator.BaseMoved, Path: proposalPath}
	}
	base, err := models.GetVersion(ctx, tx, org, p.BaseVersionID)
	if err != nil {
		return target{}, fmt.Errorf("effect: the base of proposal %s: %w", id, err)
	}
	return target{proposal: p, model: mdl, draft: d, base: base}, nil
}

// check is Check for the AI: a proposal the review refuses now is
// RECHECK_FAILED with its issue codes, one whose risk changed since its
// review RECHECK_FAILED with none.
func (s *Service) check(ctx context.Context, tx tenant.Tx, org string, t target) (Checked, error) {
	pack, err := s.Packs.Lookup(t.model.Domain)
	if err != nil {
		return Checked{}, err
	}
	c, err := Check(ctx, tx, org, s.Objects, pack, t.proposal, t.base)
	var invalid *InvalidError
	var risk *RiskChangedError
	switch {
	case errors.As(err, &invalid):
		codes := []string{}
		for _, is := range invalid.Issues {
			if !slices.Contains(codes, is.Code) {
				codes = append(codes, is.Code)
			}
		}
		return Checked{}, &Refusal{Code: investigator.RecheckFailed, Path: proposalPath, Issues: codes}
	case errors.As(err, &risk):
		// The server's risk changed since the review: no issue to name.
		return Checked{}, &Refusal{Code: investigator.RecheckFailed, Path: proposalPath}
	}
	return c, err
}

// applied is the proposal.apply event of t's proposal, with its effect,
// marked as the AI's: IDs, the hash, the risk and a count, never anything
// the provider wrote.
func applied(ctx context.Context, tx tenant.Tx, m tenant.Membership, requestID string, t target, c Checked, effect map[string]any) error {
	p := t.proposal
	meta := map[string]any{"mode": p.Mode, "model_id": p.ModelID, "base_version_id": p.BaseVersionID, "patch_hash": c.Outcome.PatchHash,
		"server_risk_level": string(c.Outcome.ServerRisk), "risk_level": string(c.Outcome.Risk)}
	for k, v := range effect {
		meta[k] = v
	}
	return auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{Action: "proposal.apply", TargetType: "ai_proposal",
		TargetID: p.ID, RequestID: requestID, Metadata: auditlog.MarkAI(meta, m.ActingFor)})
}

// SaveScenario is save_scenario's effect (H-061 §6.4): the investigation's
// ready scenario proposal proposalID, on the model's latest version and
// passing the server's checks again (Check), becomes its one scenario,
// labelled as the AI's, and the proposal becomes applied with it; audit
// proposal.apply. It runs only in the investigation's acting-for
// transaction for its requester (ErrNotActing otherwise).
func (s *Service) SaveScenario(ctx context.Context, tx tenant.Tx, m tenant.Membership, requestID, proposalID string) (Saved, error) {
	t, err := locate(ctx, tx, m, proposalID, "scenario")
	if err != nil {
		return Saved{}, err
	}
	c, err := s.check(ctx, tx, m.OrganizationID, t)
	if err != nil {
		return Saved{}, err
	}
	sc, err := scenarios.Insert(ctx, tx, NewScenario(m.OrganizationID, t.proposal, c, m.UserID))
	if err != nil {
		return Saved{}, err
	}
	if err := store.Apply(ctx, tx, m.OrganizationID, t.proposal.ID, &sc.ID, nil); err != nil {
		return Saved{}, err
	}
	if err := applied(ctx, tx, m, requestID, t, c, map[string]any{"scenario_id": sc.ID}); err != nil {
		return Saved{}, err
	}
	return Saved{ScenarioID: sc.ID, Name: sc.Name, ProposalID: t.proposal.ID}, nil
}

// WriteDraft is write_to_draft's effect (H-061 §6.4): the investigation's
// ready edit proposal proposalID, on the model's latest version and passing
// the server's checks again (Check), is written into the model's draft,
// which must still be clean at that base (its content the base's, no AI
// edit in it: DRAFT_CHANGED otherwise). models.WriteDraft computes the IR
// from the base and the reviewed patch under the model's lock, with a CAS
// on the revision read under it, and names the proposal in the draft; the
// proposal then becomes applied at that revision; audit model.draft.update
// and proposal.apply. A person publishes it, or resets it. It runs only in
// the investigation's acting-for transaction for its requester
// (ErrNotActing otherwise).
func (s *Service) WriteDraft(ctx context.Context, tx tenant.Tx, m tenant.Membership, requestID, proposalID string) (Written, error) {
	t, err := locate(ctx, tx, m, proposalID, "edit")
	if err != nil {
		return Written{}, err
	}
	d := t.draft
	if d.BaseVersionID != t.base.ID || d.Hash != t.base.Hash || d.AIProposalID != nil {
		return Written{}, &Refusal{Code: investigator.DraftChanged, Path: ""}
	}
	c, err := s.check(ctx, tx, m.OrganizationID, t)
	if err != nil {
		return Written{}, err
	}
	if hashing.Sum(t.base.IR) != t.base.Hash {
		return Written{}, fmt.Errorf("effect: the stored base %s does not hash to its stored hash", t.base.ID)
	}
	var operations []jsontext.Value
	if err := json.Unmarshal(c.Outcome.Changes, &operations); err != nil || len(operations) == 0 {
		return Written{}, fmt.Errorf("effect: proposal %s: the reviewed changes are not a patch: %v", t.proposal.ID, err)
	}
	draftWriter := modelsvc.Service{Packs: s.Packs, Datasets: c.Snapshots}
	next, err := draftWriter.WriteDraft(ctx, tx, m, requestID, d, modelsvc.DraftChange{From: jsontext.Value(t.base.IR),
		Patch: c.Outcome.Changes, Mode: patch.AIEdit, AIProposal: &t.proposal.ID})
	if errors.Is(err, modelsvc.ErrConflict) {
		return Written{}, &Refusal{Code: investigator.DraftChanged, Path: ""}
	}
	if err != nil {
		return Written{}, err
	}
	if next.Hash != c.Hash {
		return Written{}, fmt.Errorf("effect: proposal %s: the draft written is not the rechecked candidate", t.proposal.ID)
	}
	if err := store.Apply(ctx, tx, m.OrganizationID, t.proposal.ID, nil, &next.Revision); err != nil {
		return Written{}, err
	}
	if err := applied(ctx, tx, m, requestID, t, c, map[string]any{"draft_revision": next.Revision}); err != nil {
		return Written{}, err
	}
	return Written{DraftRevision: next.Revision, OperationCount: len(operations)}, nil
}
