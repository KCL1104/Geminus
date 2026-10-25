package approval

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
)

// rechecked is a ready proposal reviewed again at its approval.
type rechecked struct {
	outcome review.Outcome
	// candidate is the base with the reviewed changes applied, in RFC 8785
	// form (the bytes a version stores, D-040), hash its hash, ir it decoded.
	candidate []byte
	hash      string
	ir        *model.ModelIR
}

// recheck reviews ready proposal p again against its base version's IR,
// as the server reviewed the provider's answer (AI_SPEC §6, SECURITY §5;
// D-069 (b) APPROVED): what the proposal holds is the answer as the review
// kept it (its summary, assumptions, canonical changes, expected effects,
// confidence and the provider's risk claim), so the same review runs every
// server check again (patch profile and allowlist of the proposal's mode,
// full candidate validation, the proposal's ID pool, the domain pack's
// rules, the numbers of the expected effects, the server's own risk) and
// trusts none of the stored results. It is pure: no database, no provider.
//
// An answer the review refuses now is an *InvalidError (cleaned issues; the
// proposal stays ready, as the state machine has no ready → invalid). A
// server risk other than the stored one is a *RiskChangedError: the person
// reviewed another risk. Changes that are not the stored patch's canonical
// form, or another patch hash, are a server fault (an error), never
// approved.
func recheck(p store.Proposal, base []byte, pack domainpack.DomainPack) (rechecked, error) {
	if p.Summary == nil || p.Patch == nil || p.PatchHash == nil || p.ServerRisk == nil || p.ProviderRisk == nil ||
		p.Risk == nil || p.Confidence == nil {
		return rechecked{}, fmt.Errorf("approval: ready proposal %s lacks its reviewed content", p.ID)
	}
	mode := provider.Mode(p.Mode)
	var pm patch.Mode
	switch mode {
	case provider.ModeEdit:
		pm = patch.AIEdit
	case provider.ModeScenario:
		pm = patch.Scenario
	default:
		return rechecked{}, fmt.Errorf("approval: proposal %s has mode %q", p.ID, p.Mode)
	}
	var pool idpool.Pool
	if err := json.Unmarshal(p.IDPool, &pool); err != nil {
		return rechecked{}, fmt.Errorf("approval: proposal %s: the stored ID pool: %w", p.ID, err)
	}
	var changes model.Patch
	if err := json.Unmarshal(p.Patch, &changes); err != nil {
		return rechecked{}, fmt.Errorf("approval: proposal %s: the stored patch: %w", p.ID, err)
	}
	confidence := *p.Confidence
	o, err := review.Review(review.Input{Mode: mode, Base: base, Pool: pool, Pack: pack, Output: provider.ProposalOutput{
		Status: "proposed", Summary: *p.Summary, Assumptions: p.Assumptions, RiskLevel: *p.ProviderRisk, Confidence: &confidence,
		Changes: changes, ExpectedEffects: p.ExpectedEffects, RequiresConfirmation: true}})
	if err != nil {
		return rechecked{}, fmt.Errorf("approval: reviewing proposal %s again: %w", p.ID, err)
	}
	if o.Status != review.Ready {
		return rechecked{}, &InvalidError{Issues: o.Validation.Issues}
	}
	stored, err := hashing.Canonical(p.Patch)
	if err != nil {
		return rechecked{}, fmt.Errorf("approval: proposal %s: the stored patch: %w", p.ID, err)
	}
	if !bytes.Equal(stored, o.Changes) || o.PatchHash != *p.PatchHash {
		return rechecked{}, fmt.Errorf("approval: proposal %s: the stored patch is not the one its stored hash covers", p.ID)
	}
	switch {
	case string(o.Risk) != *p.Risk:
		return rechecked{}, &RiskChangedError{Path: "/risk_level", Reviewed: *p.Risk, Now: string(o.Risk)}
	case string(o.ServerRisk) != *p.ServerRisk:
		return rechecked{}, &RiskChangedError{Path: "/server_risk_level", Reviewed: *p.ServerRisk, Now: string(o.ServerRisk)}
	}
	res, err := patch.Apply(base, o.Changes, pm)
	if err != nil {
		return rechecked{}, err
	}
	if !res.Valid() { // the review applied the same patch to the same base
		return rechecked{}, fmt.Errorf("approval: proposal %s: the reviewed patch does not apply again", p.ID)
	}
	candidate, err := hashing.Canonical(res.Candidate)
	if err != nil {
		return rechecked{}, err
	}
	return rechecked{outcome: o, candidate: candidate, hash: hashing.Sum(candidate), ir: res.Validation.IR}, nil
}

// withoutIR rewrites the dataset binding stage's /ir/... paths as paths
// into the candidate, as the other model issues are; manifest paths stay.
func withoutIR(issues []model.ValidationIssue) []model.ValidationIssue {
	out := make([]model.ValidationIssue, len(issues))
	for i, is := range issues {
		if rest, ok := strings.CutPrefix(is.Path, "/ir"); ok && (rest == "" || rest[0] == '/') {
			is.Path = rest
		}
		out[i] = is
	}
	return validate.Sort(out)
}
