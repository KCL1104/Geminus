package scenarios

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// checked is a scenario patch that applies to its base: the patch in RFC
// 8785 form (the bytes a scenario stores), its D-019 hash, and the patched
// model, for the dataset binding stage.
type checked struct {
	patch []byte
	hash  string
	ir    *model.ModelIR
}

// maxIssues is Error.details' maxItems.
const maxIssues = 1000

// check checks scenario patch doc against base, a version's IR as stored
// (the canonical text its hash covers, D-040), as the AI scenario mode's
// review checks a provider's changes (D-021: one allowlist for a person
// and the AI): the P0 patch profile and the scenario allowlist, with a test
// before an array element changes (D-014, D-020, D-057), applied on a copy
// as a whole or not at all; the full validation of the patched model; the
// domain pack's rules. An error anywhere is an *InvalidError: patch issues
// point into the request (/patch/...), the others into the patched model;
// warnings do not refuse. What only an AI answer needs (the ID pool, the
// expected effects, the risk, and that the answer changes something) does
// not apply to a person's request, so [] and a patch that changes nothing
// are accepted: a saved baseline (D-078 (b)). It is pure: what the server
// read goes in, nothing is read or written. Any other error is the
// server's: a base that is not a valid model, a domain no pack serves.
func check(base, doc []byte, packs PackLookup) (checked, error) {
	// Issues of the patched model are refused like the patch's own, as
	// createSimulation refuses them: an older version that a later rule no
	// longer accepts (D-067 allows a scenario on any version) gets the
	// rule's issue, not a server error. A base that is not JSON is the
	// server's fault (patch.Apply's error).
	res, err := patch.Apply(base, doc, patch.Scenario)
	if err != nil {
		return checked{}, fmt.Errorf("scenarios: applying the patch: %w", err)
	}
	if !res.Applied() {
		return checked{}, &InvalidError{Issues: capped(prefixed("/patch", res.PatchIssues))}
	}
	if !res.Valid() {
		return checked{}, &InvalidError{Issues: capped(validate.Sort(res.Validation.Issues))}
	}
	ir := res.Validation.IR
	pack, err := packs.Lookup(ir.Domain)
	if err != nil {
		return checked{}, fmt.Errorf("scenarios: the domain of the base: %w", err)
	}
	if issues := pack.Validate(ir); slices.ContainsFunc(issues, isError) {
		return checked{}, &InvalidError{Issues: capped(validate.Sort(issues))}
	}
	canonical, err := hashing.Canonical(doc)
	if err != nil {
		return checked{}, err // patch.Apply parsed it
	}
	hash, err := hashing.Patch(canonical)
	if err != nil {
		return checked{}, err
	}
	return checked{patch: canonical, hash: hash, ir: ir}, nil
}

// nameIssues is the name rule (D-078 (d)): the person's text, kept as
// given (1–200 characters and no U+0000: the schema and httpapi), but a
// name of only white space says nothing, as an intent of only white space
// says nothing to createProposal.
func nameIssues(name string) []model.ValidationIssue {
	if strings.TrimFunc(name, unicode.IsSpace) == "" {
		return []model.ValidationIssue{model.NewIssue(validate.ConstraintFailed, "/name", "the name says nothing: it is only white space", model.SeverityError)}
	}
	return nil
}

func isError(i model.ValidationIssue) bool { return i.Severity == model.SeverityError }

// prefixed points issues of a member of the request body into it.
func prefixed(root string, issues []model.ValidationIssue) []model.ValidationIssue {
	out := make([]model.ValidationIssue, len(issues))
	for i, is := range issues {
		out[i] = model.NewIssue(is.Code, root+is.Path, is.Message, is.Severity)
	}
	return validate.Sort(out)
}

// withoutIR rewrites the dataset binding stage's /ir/... paths as paths
// into the patched model, as its other issues are; manifest paths stay.
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

// capped keeps an issue list within Error.details' 1000: the first ones,
// after a LIMIT_EXCEEDED issue that says how many are left out.
func capped(issues []model.ValidationIssue) []model.ValidationIssue {
	if len(issues) <= maxIssues {
		return issues
	}
	kept, rest := issues[:maxIssues-1], issues[maxIssues-1:]
	severity := model.SeverityWarning
	if slices.ContainsFunc(rest, isError) {
		severity = model.SeverityError
	}
	return append([]model.ValidationIssue{model.NewIssue(validate.LimitExceeded, "", fmt.Sprintf("%d more issues are not listed", len(rest)), severity)}, kept...)
}
