package approval

import (
	"errors"

	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/proposals/effect"
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
// D-069 (b) APPROVED): effect.Recheck, which an AI investigation's effects
// run too (H-061 A4), with this package's errors. It is pure: no database,
// no provider.
//
// An answer the review refuses now is an *InvalidError (cleaned issues; the
// proposal stays ready, as the state machine has no ready → invalid). A
// server risk other than the stored one is a *RiskChangedError: the person
// reviewed another risk. Changes that are not the stored patch's canonical
// form, or another patch hash, are a server fault (an error), never
// approved.
func recheck(p store.Proposal, base []byte, pack domainpack.DomainPack) (rechecked, error) {
	r, err := effect.Recheck(p, base, pack)
	if err != nil {
		return rechecked{}, refused(err)
	}
	return rechecked{outcome: r.Outcome, candidate: r.Candidate, hash: r.Hash, ir: r.IR}, nil
}

// refused is an error of the shared revalidation (effect.Recheck,
// effect.Check) as this package's: an *InvalidError, a *RiskChangedError or
// ErrDatasetsUnavailable, which Refused maps for the person; any other
// error is returned as it is.
func refused(err error) error {
	var invalid *effect.InvalidError
	var risk *effect.RiskChangedError
	switch {
	case errors.As(err, &invalid):
		return &InvalidError{Issues: invalid.Issues}
	case errors.As(err, &risk):
		return &RiskChangedError{Path: risk.Path, Reviewed: risk.Reviewed, Now: risk.Now}
	case errors.Is(err, effect.ErrDatasetsUnavailable):
		return ErrDatasetsUnavailable
	}
	return err
}
