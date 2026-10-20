// Package review is the server's review of a provider's proposal (T-025;
// AI_SPEC §4–§5, D-002, D-014, D-016, D-020–D-022, D-027, D-046). A
// provider answer is untrusted data: it becomes a proposal only through
// this review, which binds nothing the provider says about identity,
// validation or approval and computes the risk itself.
//
// For a proposed change the review runs, in order: the provider-output
// rules the schema already states (checked again here, so nothing relies
// on the caller having validated), the patch profile (op allowlist,
// decoded-pointer allowlist of the mode, test before modify, atomic
// application: internal/model/patch), the full validation of the candidate
// IR, the ID allocation pool (a new node, flow or resource takes an ID of
// its own kind from the proposal's pool), the domain pack's rules, and the
// numbers in the expected effects (qualitative only: a number that is not
// a value of the base or the candidate is a fabricated prediction, even
// one the person's intent states). Any error makes the proposal invalid, and an invalid outcome
// keeps only its issues: nothing the provider wrote, not even inside an
// issue, since every issue the review returns is cleaned (Clean: a fixed
// message of its code, a path of known tokens). Otherwise it is ready,
// with the canonical patch, its hash (D-019), the server's risk, the
// provider's claim and the effective risk, max of the two (D-027).
//
// The dataset binding stage needs object storage, so the adapter runs it
// on the ready outcome's candidate and passes its issues to Refuse.
//
// The package reads no database and calls no provider; it is pure
// computation over what the server read (the architecture rules
// ai-suggests-only and ai-is-pure-but-the-adapter).
package review

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// Status is the state a review leaves a generating proposal in.
type Status string

const (
	Ready      Status = "ready"
	NeedsInput Status = "needs_input"
	Invalid    Status = "invalid"
)

// Input is what a review reads: the server's facts about the proposal and
// the provider's answer.
type Input struct {
	// Mode is the proposal's, fixed when it was created (D-004).
	Mode provider.Mode
	// Base is the base version's IR as stored: the canonical text its hash
	// covers (D-040).
	Base []byte
	// Pool is the proposal's ID allocation pool (D-046).
	Pool idpool.Pool
	// Pack is the model's domain pack.
	Pack domainpack.DomainPack
	// Output is the provider's schema-valid answer, untrusted.
	Output provider.ProposalOutput
}

// Validation is common.schema.json#/$defs/Validation.
type Validation struct {
	Valid  bool                    `json:"valid"`
	Issues []model.ValidationIssue `json:"issues"`
}

// Outcome is what the proposal becomes. Fields other than Status and
// Validation are set only as the status allows: Summary, Assumptions and
// Questions for needs_input; everything but Questions for ready; only
// Validation for invalid.
type Outcome struct {
	Status          Status
	Summary         string
	Assumptions     []string
	ExpectedEffects []string
	Questions       []string
	Confidence      *float64
	// Changes is the patch in RFC 8785 form; PatchHash is D-019's hash of it.
	Changes   []byte
	PatchHash string
	// Candidate is the base with Changes applied, for the adapter's dataset
	// binding stage; never stored or encoded.
	Candidate *model.ModelIR `json:"-"`
	// ServerRisk is the server's (D-016), ProviderRisk the provider's claim,
	// Risk their maximum (D-027).
	ServerRisk, ProviderRisk, Risk Risk
	Validation                     *Validation
}

func errorIssue(code, path, message string) model.ValidationIssue {
	return model.NewIssue(code, path, message, model.SeverityError)
}

func isError(i model.ValidationIssue) bool { return i.Severity == model.SeverityError }

// maxIssues is Validation.issues' maxItems.
const maxIssues = 1000

// capped keeps an issue list within maxIssues: the first ones, after a
// LIMIT_EXCEEDED issue that says how many are left out.
func capped(issues []model.ValidationIssue) []model.ValidationIssue {
	issues = validate.Sort(issues)
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

// invalid is the outcome of issues: nothing else is kept, and the issues
// are cleaned (Clean), so that none quotes the answer.
func invalid(issues []model.ValidationIssue) Outcome {
	issues = cleaned(issues)
	if !slices.ContainsFunc(issues, isError) {
		issues = append(issues, errorIssue(OutputInvalid, "", sentNotAProposal))
	}
	return Outcome{Status: Invalid, Validation: &Validation{Valid: false, Issues: capped(issues)}}
}

// InvalidOutput is the outcome of an answer the provider client refused
// (T-024's invalid_output): its issues hold JSON Pointers into the answer,
// cleaned like every other (Clean), and nothing of the answer.
func InvalidOutput(issues []model.ValidationIssue) Outcome { return invalid(issues) }

// Refuse adds the issues of a later stage (the adapter's dataset binding
// stage), cleaned (Clean), to a ready outcome: errors make it invalid,
// warnings stay with it.
func (o Outcome) Refuse(issues []model.ValidationIssue) Outcome {
	if o.Status != Ready || len(issues) == 0 {
		return o
	}
	if slices.ContainsFunc(issues, isError) {
		return invalid(issues)
	}
	v := *o.Validation
	v.Issues = capped(append(slices.Clone(v.Issues), cleaned(issues)...))
	o.Validation = &v
	return o
}

// ErrInput: the server's own input is unusable (a bug or corrupt row, never
// the provider's doing).
var ErrInput = errors.New("review: invalid input")

func inputError(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInput, fmt.Sprintf(format, a...))
}

// prefixed points patch issues into the proposal's changes.
func prefixed(issues []model.ValidationIssue) []model.ValidationIssue {
	out := make([]model.ValidationIssue, len(issues))
	for i, is := range issues {
		out[i] = model.NewIssue(is.Code, "/changes"+is.Path, is.Message, is.Severity)
	}
	return out
}

// poolReasons are the fixed messages of a pool violation: the path locates
// the ID, which the message never quotes.
var poolReasons = map[idpool.Reason]string{
	idpool.NotInPool:  "a new entity's ID is not one of this proposal's allocated IDs (D-046)",
	idpool.WrongKind:  "a new entity's ID is allocated for another kind of entity (D-046)",
	idpool.UsedInBase: "a new entity's ID belongs to another entity of the base model (D-046)",
	idpool.Missing:    "the pool check has no model to compare",
}

// Review reviews in.Output. The error is for the server's own input only;
// everything wrong with the answer is an invalid outcome.
func Review(in Input) (Outcome, error) {
	var pm patch.Mode
	switch in.Mode {
	case provider.ModeEdit:
		pm = patch.AIEdit
	case provider.ModeScenario:
		pm = patch.Scenario
	default:
		return Outcome{}, inputError("unknown mode %q", in.Mode)
	}
	if in.Pack == nil {
		return Outcome{}, inputError("no domain pack")
	}
	if err := in.Pool.Validate(); err != nil {
		return Outcome{}, inputError("the ID pool: %v", err)
	}
	baseResult, err := validate.IR(in.Base)
	if err != nil {
		return Outcome{}, err
	}
	if !baseResult.Valid() {
		return Outcome{}, inputError("the base version is not a valid model")
	}
	baseIR := baseResult.IR
	if in.Pack.ID() != baseIR.Domain {
		return Outcome{}, inputError("the pack is not the model's domain")
	}
	used := usedIDs(baseIR)
	for _, k := range idpool.Kinds {
		for _, id := range in.Pool.IDs(k) {
			if used[id] {
				return Outcome{}, inputError("the ID pool overlaps the base model")
			}
		}
	}

	out := in.Output
	if issues := outputIssues(out); len(issues) > 0 {
		return invalid(issues), nil
	}
	if out.Status == "needs_input" {
		return Outcome{Status: NeedsInput, Summary: out.Summary, Assumptions: nonNil(out.Assumptions), Questions: slices.Clone(out.Questions)}, nil
	}

	encoded, err := json.Marshal(out.Changes)
	if err != nil {
		return Outcome{}, err
	}
	changes, err := hashing.Canonical(encoded)
	if err != nil {
		return Outcome{}, err
	}
	res, err := patch.Apply(in.Base, changes, pm)
	if err != nil {
		return Outcome{}, err
	}
	if !res.Applied() {
		return invalid(prefixed(res.PatchIssues)), nil
	}
	if !res.Valid() {
		return invalid(res.Validation.Issues), nil
	}
	candidate := res.Validation.IR
	if string(res.Candidate) == string(in.Base) {
		return invalid([]model.ValidationIssue{errorIssue(OutputInvalid, "/changes", sentNoOp)}), nil
	}
	issues := slices.Clone(res.Validation.Issues) // warnings only
	var refused []model.ValidationIssue
	for _, v := range in.Pool.Check(baseIR, candidate) {
		refused = append(refused, errorIssue(IDPoolViolation, v.Path, poolReasons[v.Reason]))
	}
	if len(refused) > 0 {
		return invalid(refused), nil
	}
	packIssues := in.Pack.Validate(candidate)
	if slices.ContainsFunc(packIssues, isError) {
		return invalid(packIssues), nil
	}
	issues = append(issues, packIssues...)
	// The person's numbers are not among them: a demanded outcome restated
	// as an effect is still a prediction (eval MOD-050).
	supplied := append(numbers(baseIR), numbers(candidate)...)
	for i, effect := range out.ExpectedEffects {
		if len(locale.CheckProse(effect, supplied)) > 0 {
			refused = append(refused, errorIssue(OutputInvalid, "/expected_effects/"+strconv.Itoa(i), sentEffectNumber))
		}
	}
	if len(refused) > 0 {
		return invalid(refused), nil
	}
	hash, err := hashing.Patch(changes)
	if err != nil {
		return Outcome{}, err
	}
	server := ServerRisk(baseIR, candidate)
	claim := Risk(out.RiskLevel)
	confidence := *out.Confidence
	return Outcome{Status: Ready, Summary: out.Summary, Assumptions: nonNil(out.Assumptions), ExpectedEffects: nonNil(out.ExpectedEffects),
		Confidence: &confidence, Changes: changes, PatchHash: hash, Candidate: candidate,
		ServerRisk: server, ProviderRisk: claim, Risk: Max(server, claim),
		Validation: &Validation{Valid: true, Issues: nonNilIssues(capped(cleaned(issues)))}}, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return slices.Clone(s)
}

func nonNilIssues(s []model.ValidationIssue) []model.ValidationIssue {
	if s == nil {
		return []model.ValidationIssue{}
	}
	return s
}

// outputIssues re-checks what proposal-output.schema.json states about the
// answer's shape and its fixed values, so that no trusted state can come
// from it whatever the caller checked: the status is proposed or
// needs_input, requires_confirmation is true, a proposal has changes, a
// confidence and a risk claim and no questions, a request for input has
// questions and no changes, and no text is empty. It also refuses U+0000
// anywhere in the text and the changes, which PostgreSQL text (22021) and
// jsonb (22P05) cannot store: such an answer is invalid here rather than an
// outcome whose write fails (H-003 review M2).
func outputIssues(o provider.ProposalOutput) []model.ValidationIssue {
	var out []model.ValidationIssue
	bad := func(path, message string) { out = append(out, errorIssue(OutputInvalid, path, message)) }
	text := func(path, s string) {
		switch {
		case s == "":
			bad(path, sentEmpty)
		case strings.ContainsRune(s, 0):
			bad(path, sentNUL)
		}
	}
	text("/summary", o.Summary)
	for _, l := range []struct {
		member string
		list   []string
	}{{"assumptions", o.Assumptions}, {"expected_effects", o.ExpectedEffects}, {"questions", o.Questions}} {
		for i, s := range l.list {
			text("/"+l.member+"/"+strconv.Itoa(i), s)
		}
	}
	// A patch that does not encode is the server's error (Review's own
	// encoding returns it).
	if encoded, err := json.Marshal(o.Changes); err == nil {
		if at, ok := nulAt(encoded); ok {
			bad("/changes"+at, sentNUL)
		}
	}
	if !o.RequiresConfirmation {
		bad("/requires_confirmation", sentConfirmation)
	}
	switch o.Status {
	case "proposed":
		if len(o.Changes) == 0 {
			bad("/changes", sentChanges)
		}
		if o.Confidence == nil || *o.Confidence < 0 || *o.Confidence > 1 {
			bad("/confidence", sentConfidence)
		}
		if !Risk(o.RiskLevel).valid() {
			bad("/risk_level", sentRiskClaim)
		}
		if o.Questions != nil {
			bad("/questions", sentNoQuestions)
		}
	case "needs_input":
		if len(o.Questions) == 0 {
			bad("/questions", sentQuestions)
		}
		if o.Changes != nil {
			bad("/changes", sentInputNoChanges)
		}
	default:
		bad("/status", sentStatus)
	}
	return out
}

// usedIDs is every entity ID of an IR.
func usedIDs(ir *model.ModelIR) map[string]bool {
	out := map[string]bool{}
	for _, e := range ir.EntityTypes {
		out[e.ID] = true
	}
	for _, r := range ir.Resources {
		out[r.ID] = true
	}
	for _, n := range ir.Nodes {
		out[n.ID] = true
	}
	for _, f := range ir.Flows {
		out[f.ID] = true
	}
	for _, m := range ir.Metrics {
		out[m.ID] = true
	}
	return out
}

// nums is the values of the Arabic numbers in text.
func nums(text string) []float64 {
	found, _ := locale.Numbers(text)
	out := make([]float64, len(found))
	for i, n := range found {
		out[i] = n.Value
	}
	return out
}

// numbers is every value a model states: capacities, units, distribution
// parameters, arrival bounds, routing probabilities and constraint values,
// and the numbers in its names (views are presentation, not values).
func numbers(ir *model.ModelIR) []float64 {
	var out []float64
	add := func(p *float64) {
		if p != nil {
			out = append(out, *p)
		}
	}
	out = append(out, nums(ir.Name)...)
	for _, e := range ir.EntityTypes {
		out = append(out, nums(e.Name)...)
	}
	for _, r := range ir.Resources {
		out = append(out, float64(r.Capacity))
		out = append(out, nums(r.Name)...)
	}
	for i := range ir.Nodes {
		n := &ir.Nodes[i]
		out = append(out, nums(n.Name)...)
		if n.Arrival != nil && n.Arrival.MaxEntities > 0 {
			out = append(out, float64(n.Arrival.MaxEntities))
		}
		for _, d := range n.Distributions() {
			add(d.Distribution.Value)
			add(d.Distribution.Mean)
			add(d.Distribution.Min)
			add(d.Distribution.Mode)
			add(d.Distribution.Max)
		}
		for _, r := range n.Requires {
			out = append(out, float64(r.Units))
		}
	}
	for _, f := range ir.Flows {
		out = append(out, f.Routing.Probability)
	}
	for _, c := range ir.Constraints {
		out = append(out, c.Equals)
	}
	return out
}
