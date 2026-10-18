package conformance

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// Each broken pack differs from queuePack in one method and must produce a
// finding of the named check: the suite proves the properties it claims.

type badID struct{ queuePack }

func (badID) ID() string { return "" }

type otherID struct{ queuePack }

func (otherID) ID() string { return "generic_queue_v2" }

type dupKind struct{ queuePack }

func (p dupKind) NodeCatalog() []domainpack.NodeDefinition {
	n := p.queuePack.NodeCatalog()
	return append(n, n[0])
}

type nonGenericType struct{ queuePack }

func (p nonGenericType) NodeCatalog() []domainpack.NodeDefinition {
	n := p.queuePack.NodeCatalog()
	n[2].Type = "teller"
	return n
}

type badRequirement struct{ queuePack }

func (p badRequirement) NodeCatalog() []domainpack.NodeDefinition {
	n := p.queuePack.NodeCatalog()
	n[2].ResourceTypes = []string{"robot"}
	return n
}

var sharedNodes = queuePack{}.NodeCatalog()

type sharedCatalog struct{ queuePack }

func (sharedCatalog) NodeCatalog() []domainpack.NodeDefinition { return sharedNodes }
func (p sharedCatalog) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Nodes = p.NodeCatalog()
	return c
}

type badResourceType struct{ queuePack }

func (badResourceType) ResourceCatalog() []domainpack.ResourceDefinition {
	return []domainpack.ResourceDefinition{{Kind: "clerk", Type: "robot", Label: "Clerks"}}
}
func (p badResourceType) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Resources = p.ResourceCatalog()
	return c
}

type acceptsAnything struct{ queuePack }

func (acceptsAnything) Validate(*model.ModelIR) []model.ValidationIssue { return nil }

type rejectsValid struct{ queuePack }

func (rejectsValid) Validate(*model.ModelIR) []model.ValidationIssue {
	return []model.ValidationIssue{model.NewIssue("NOPE", "/nodes", "no", model.SeverityError)}
}

type mutatingValidate struct{ queuePack }

func (p mutatingValidate) Validate(ir *model.ModelIR) []model.ValidationIssue {
	if ir != nil {
		ir.Name += "!"
	}
	return p.queuePack.Validate(ir)
}

type flakyValidate struct {
	queuePack
	n *atomic.Int64
}

func (p flakyValidate) Validate(ir *model.ModelIR) []model.ValidationIssue {
	out := p.queuePack.Validate(ir)
	if p.n.Add(1)%2 == 0 {
		out = append(out, model.NewIssue("HINT", "/name", "a hint", model.SeverityWarning))
	}
	return out
}

type panicsOnNil struct{ queuePack }

func (p panicsOnNil) Validate(ir *model.ModelIR) []model.ValidationIssue {
	_ = ir.Domain
	return p.queuePack.Validate(ir)
}

type missingNode struct{ queuePack }

func (p missingNode) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	v := p.queuePack.DefaultViews(ir)
	delete(v.Positions, ir.Nodes[0].ID)
	return v
}

type ghostNode struct{ queuePack }

func (p ghostNode) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	v := p.queuePack.DefaultViews(ir)
	v.Positions["ghost"] = model.Position{X: 1e6, Y: 1e6}
	return v
}

type movesSaved struct{ queuePack }

func (p movesSaved) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	c := *ir
	c.Views.TwoD.Positions = nil
	return p.queuePack.DefaultViews(&c)
}

type overlapping struct{ queuePack }

func (overlapping) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	v := domainpack.ViewDefinitions{Positions: map[string]model.Position{}}
	for _, n := range ir.Nodes {
		v.Positions[n.ID] = model.Position{}
	}
	for id, p := range ir.Views.TwoD.Positions {
		v.Positions[id] = p
	}
	return v
}

type mutatingViews struct{ queuePack }

func (p mutatingViews) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	v := p.queuePack.DefaultViews(ir)
	ir.Views.TwoD.Positions = v.Positions
	return v
}

type mutatingMetrics struct{ queuePack }

func (p mutatingMetrics) ComputeMetrics(r *simulation.Result) map[string]simulation.MetricSummary {
	r.Metrics.FailureCount.SampleCount++
	return p.queuePack.ComputeMetrics(r)
}

type invalidMetric struct{ queuePack }

func (invalidMetric) ComputeMetrics(*simulation.Result) map[string]simulation.MetricSummary {
	return map[string]simulation.MetricSummary{"broken": {Status: simulation.Available, Population: simulation.PopulationReplication, Unit: "x", SampleCount: 1}}
}

type emptyKey struct{ queuePack }

func (p emptyKey) ComputeMetrics(r *simulation.Result) map[string]simulation.MetricSummary {
	out := p.queuePack.ComputeMetrics(r)
	out[""] = out["abandon_rate"]
	return out
}

type flakyMetrics struct {
	queuePack
	n *atomic.Int64
}

func (p flakyMetrics) ComputeMetrics(r *simulation.Result) map[string]simulation.MetricSummary {
	v := float64(p.n.Add(1))
	return map[string]simulation.MetricSummary{"calls": {Status: simulation.Available, Mean: &v, P50: &v, P95: &v, SampleCount: 1,
		Population: simulation.PopulationReplication, Unit: "call"}}
}

type nilNil struct{ queuePack }

func (nilNil) InferFromEvents(context.Context, domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	return nil, nil
}

type ignoresCancel struct {
	queuePack
	m model.ModelIR
}

func (p ignoresCancel) InferFromEvents(context.Context, domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	m := clone(&p.m)
	return &domainpack.InferenceResult{Model: &m}, nil
}

type proposesForeign struct {
	queuePack
	m model.ModelIR
}

func (p proposesForeign) InferFromEvents(ctx context.Context, _ domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m := clone(&p.m)
	m.Domain = "software_delivery"
	return &domainpack.InferenceResult{Model: &m}, nil
}

type proposesInvalid struct {
	queuePack
	m model.ModelIR
}

func (p proposesInvalid) InferFromEvents(ctx context.Context, _ domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m := clone(&p.m)
	m.Flows[0].To = "nowhere"
	return &domainpack.InferenceResult{Model: &m}, nil
}

type unavailableWithResult struct{ queuePack }

func (unavailableWithResult) InferFromEvents(context.Context, domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	return &domainpack.InferenceResult{}, domainpack.ErrUnavailable
}

type promptOtherDomain struct{ queuePack }

func (p promptOtherDomain) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Domain = "software_delivery"
	return c
}

type promptDropsNodes struct{ queuePack }

func (p promptDropsNodes) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Nodes = c.Nodes[1:]
	return c
}

type promptChanges struct {
	queuePack
	n *atomic.Int64
}

func (p promptChanges) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	if p.n.Add(1)%2 == 0 {
		c.Rules = append(c.Rules, "and another")
	}
	return c
}

type flakyID struct{ n *atomic.Int64 }

func (p flakyID) ID() string {
	if p.n.Add(1) > 1 {
		return queueDomain + "_changed"
	}
	return queueDomain
}

type flakyIDPack struct {
	queuePack
	flakyID
}

func (p flakyIDPack) ID() string { return p.flakyID.ID() }

type emptyCatalog struct{ queuePack }

func (emptyCatalog) NodeCatalog() []domainpack.NodeDefinition { return nil }
func (emptyCatalog) PromptContext() domainpack.PromptContext {
	c := queuePack{}.PromptContext()
	c.Nodes = nil
	return c
}

type noLabel struct{ queuePack }

func (p noLabel) NodeCatalog() []domainpack.NodeDefinition {
	n := p.queuePack.NodeCatalog()
	n[0].Label = ""
	return n
}

type dupResource struct{ queuePack }

func (p dupResource) ResourceCatalog() []domainpack.ResourceDefinition {
	r := p.queuePack.ResourceCatalog()
	return append(r, r[0])
}

type flakyViews struct {
	queuePack
	n *atomic.Int64
}

func (p flakyViews) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	v := p.queuePack.DefaultViews(ir)
	if p.n.Add(1)%2 == 0 {
		for id, pos := range v.Positions {
			if _, saved := ir.Views.TwoD.Positions[id]; !saved {
				v.Positions[id] = model.Position{X: pos.X + 0.5, Y: pos.Y}
			}
		}
	}
	return v
}

type unencodableViews struct{ queuePack }

func (p unencodableViews) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	v := p.queuePack.DefaultViews(ir)
	for id := range v.Positions {
		if _, saved := ir.Views.TwoD.Positions[id]; !saved {
			v.Positions[id] = model.Position{X: math.Inf(1), Y: float64(len(id))}
		}
	}
	return v
}

type emptySummary struct{ queuePack }

func (p emptySummary) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Summary = ""
	return c
}

type emptyRule struct{ queuePack }

func (p emptyRule) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Rules = append(c.Rules, "")
	return c
}

// T-042 review: packs the first suite let through.

type nanMetric struct{ queuePack }

func (nanMetric) ComputeMetrics(*simulation.Result) map[string]simulation.MetricSummary {
	v := math.NaN()
	return map[string]simulation.MetricSummary{"nan": {Status: simulation.Available, Mean: &v, P50: &v, P95: &v, SampleCount: 1, Population: simulation.PopulationReplication, Unit: "x"}}
}

// onEnginePath is true while the engine calls ComputeMetrics: the digest is
// not yet set. A pack that behaves differently there fools a check that
// only calls ComputeMetrics directly.
func onEnginePath(r *simulation.Result) bool { return r.Reproducibility.ResultDigest == "" }

type panicsOnEnginePath struct{ queuePack }

func (p panicsOnEnginePath) ComputeMetrics(r *simulation.Result) map[string]simulation.MetricSummary {
	if onEnginePath(r) {
		panic("engine path")
	}
	return p.queuePack.ComputeMetrics(r)
}

type rewritesSeed struct{ queuePack }

func (p rewritesSeed) ComputeMetrics(r *simulation.Result) map[string]simulation.MetricSummary {
	if onEnginePath(r) {
		r.Reproducibility.Seed = 7
		r.Reproducibility.ModelHash = "sha256:" + strings.Repeat("0", 64)
	}
	return p.queuePack.ComputeMetrics(r)
}

type rewritesQueues struct{ queuePack }

func (p rewritesQueues) ComputeMetrics(r *simulation.Result) map[string]simulation.MetricSummary {
	if onEnginePath(r) {
		for k, q := range r.Metrics.Queues {
			q.SampleCount++
			r.Metrics.Queues[k] = q
		}
	}
	return p.queuePack.ComputeMetrics(r)
}

type rewritesAdditional struct{ queuePack }

func (p rewritesAdditional) ComputeMetrics(r *simulation.Result) map[string]simulation.MetricSummary {
	if onEnginePath(r) {
		reason := "X"
		r.Metrics.AdditionalMetrics["zz_injected"] = simulation.MetricSummary{Status: simulation.Unavailable, Population: simulation.PopulationReplication, Unit: "x", Reason: &reason}
	}
	return p.queuePack.ComputeMetrics(r)
}

var (
	lastSeenMu sync.Mutex
	lastSeen   string
)

// leaksIntoPrompt remembers the last model it validated and puts its name
// into the prompt context.
type leaksIntoPrompt struct{ queuePack }

func (p leaksIntoPrompt) Validate(ir *model.ModelIR) []model.ValidationIssue {
	if ir != nil {
		lastSeenMu.Lock()
		lastSeen = ir.Name
		lastSeenMu.Unlock()
	}
	return p.queuePack.Validate(ir)
}

func (p leaksIntoPrompt) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	lastSeenMu.Lock()
	defer lastSeenMu.Unlock()
	if lastSeen != "" && lastSeen != "Test-only service counter" {
		c.Rules = append(c.Rules, "Customer model: "+lastSeen)
	}
	return c
}

// answers is a correct pack that infers: an error without events or with a
// cancelled context, a valid model of its domain otherwise.
type answers struct {
	queuePack
	m model.ModelIR
}

func (p answers) InferFromEvents(ctx context.Context, d domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(d.Events) == 0 {
		return nil, fmt.Errorf("%w: no events", domainpack.ErrUnavailable)
	}
	m := clone(&p.m)
	return &domainpack.InferenceResult{Model: &m, Assumptions: []string{"test-only"}}, nil
}

// infersFromNothing invents a model from an empty dataset.
type infersFromNothing struct{ answers }

func (p infersFromNothing) InferFromEvents(ctx context.Context, _ domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	return p.answers.InferFromEvents(ctx, events)
}

var sharedTypes = []string{"generic", "human"}

type sharedResourceTypes struct{ queuePack }

func (p sharedResourceTypes) NodeCatalog() []domainpack.NodeDefinition {
	n := p.queuePack.NodeCatalog()
	n[2].ResourceTypes = sharedTypes
	return n
}
func (p sharedResourceTypes) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Nodes = p.NodeCatalog()
	return c
}

var sharedRes = queuePack{}.ResourceCatalog()

type sharedResources struct{ queuePack }

func (sharedResources) ResourceCatalog() []domainpack.ResourceDefinition { return sharedRes }
func (p sharedResources) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Resources = p.ResourceCatalog()
	return c
}

type mismatchWithoutPath struct{ queuePack }

func (p mismatchWithoutPath) Validate(ir *model.ModelIR) []model.ValidationIssue {
	if ir != nil && ir.Domain != queueDomain {
		return []model.ValidationIssue{model.NewIssue(DomainMismatch, "", "other domain", model.SeverityError)}
	}
	return p.queuePack.Validate(ir)
}

type mismatchAsWarning struct{ queuePack }

func (p mismatchAsWarning) Validate(ir *model.ModelIR) []model.ValidationIssue {
	if ir != nil && ir.Domain != queueDomain {
		return []model.ValidationIssue{model.NewIssue(DomainMismatch, "/domain", "other domain", model.SeverityWarning)}
	}
	return p.queuePack.Validate(ir)
}

// layoutOnlyWithSavedPositions lays nothing out for an IR with no saved
// position.
type layoutOnlyWithSavedPositions struct{ queuePack }

func (p layoutOnlyWithSavedPositions) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	if len(ir.Views.TwoD.Positions) == 0 {
		return domainpack.ViewDefinitions{Positions: map[string]model.Position{}}
	}
	return p.queuePack.DefaultViews(ir)
}

// ignoresSavedWhenPlacing lays the other nodes out as if nothing were saved.
type ignoresSavedWhenPlacing struct{ queuePack }

func (ignoresSavedWhenPlacing) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	c := *ir
	c.Views.TwoD.Positions = nil
	v := domainpack.LayeredLayout(&c)
	for id, pos := range ir.Views.TwoD.Positions {
		v.Positions[id] = pos
	}
	return v
}

var validated atomic.Int64

// countsIntoPrompt reports in its prompt context how many models it has
// validated: stable between two calls, changed after seeing models.
type countsIntoPrompt struct{ queuePack }

func (p countsIntoPrompt) Validate(ir *model.ModelIR) []model.ValidationIssue {
	validated.Add(1)
	return p.queuePack.Validate(ir)
}

func (p countsIntoPrompt) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	if n := validated.Load(); n > 0 {
		c.Rules = append(c.Rules, fmt.Sprintf("seen %d models", n))
	}
	return c
}

// leaksViewsIntoPrompt remembers node names it laid out (the suite's models
// have none) and puts them into the prompt context.
type leaksViewsIntoPrompt struct{ queuePack }

var seenNames sync.Map

func (p leaksViewsIntoPrompt) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	for _, n := range ir.Nodes {
		if n.Name != "" {
			seenNames.Store(n.Name, true)
		}
	}
	return p.queuePack.DefaultViews(ir)
}

func (p leaksViewsIntoPrompt) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	seenNames.Range(func(k, _ any) bool { c.Rules = append(c.Rules, "Customer step: "+k.(string)); return true })
	return c
}

// leaksEventsIntoPrompt remembers case IDs of the events it was given,
// other than the suite's own "case-" ones, and puts them into the prompt.
type leaksEventsIntoPrompt struct{ queuePack }

var seenCases sync.Map

func (p leaksEventsIntoPrompt) InferFromEvents(ctx context.Context, d domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	for _, e := range d.Events {
		if !strings.HasPrefix(e.CaseID, "case-") {
			seenCases.Store(e.CaseID, true)
		}
	}
	return p.queuePack.InferFromEvents(ctx, d)
}

func (p leaksEventsIntoPrompt) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	seenCases.Range(func(k, _ any) bool { c.Rules = append(c.Rules, "Customer case: "+k.(string)); return true })
	return c
}

// remembersLastModel warns about the model it validated before: one
// tenant's model name in another tenant's answer (T-042 review N7).
type remembersLastModel struct {
	queuePack
	last *atomic.Pointer[string]
}

func (p remembersLastModel) Validate(ir *model.ModelIR) []model.ValidationIssue {
	out := p.queuePack.Validate(ir)
	if ir == nil {
		return out
	}
	name := ir.Name
	if prev := p.last.Swap(&name); prev != nil && *prev != ir.Name {
		out = append(out, model.NewIssue("LAST_MODEL", "", "last model checked: "+*prev, model.SeverityWarning))
	}
	return out
}

// countsIntoIssues adds how often it validated to its issues: its answer
// for a model depends on earlier calls.
type countsIntoIssues struct {
	queuePack
	n *atomic.Int64
}

func (p countsIntoIssues) Validate(ir *model.ModelIR) []model.ValidationIssue {
	return append(p.queuePack.Validate(ir), model.NewIssue("CALLS", "", fmt.Sprint(p.n.Add(1)), model.SeverityWarning))
}

// viewsRememberLastModel shifts its layout by the length of the name of
// the model it laid out before.
type viewsRememberLastModel struct {
	queuePack
	last *atomic.Pointer[string]
}

func (p viewsRememberLastModel) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	v := p.queuePack.DefaultViews(ir)
	name := ir.Name
	if prev := p.last.Swap(&name); prev != nil {
		for id, pos := range v.Positions {
			v.Positions[id] = model.Position{X: pos.X + float64(len(*prev)), Y: pos.Y}
		}
	}
	return v
}

// mintsModelIDs answers every inference with a fresh, valid model ID, as a
// ULID-minting pack would (T-042 review N8).
type mintsModelIDs struct {
	answers
	n *atomic.Int64
}

func (p mintsModelIDs) InferFromEvents(ctx context.Context, d domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	res, err := p.answers.InferFromEvents(ctx, d)
	if res != nil {
		res.Model.ModelID = fmt.Sprintf("mdl_01M4YDQK00GENERCQ%09d", p.n.Add(1))
	}
	return res, err
}

// Each returns a model together with an error in one case (T-042 review N9).
type modelAndErrorWhenEmpty struct{ answers }

func (p modelAndErrorWhenEmpty) InferFromEvents(ctx context.Context, d domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	res, err := p.answers.InferFromEvents(ctx, events)
	if len(d.Events) == 0 {
		return res, fmt.Errorf("%w: no events", domainpack.ErrUnavailable)
	}
	return res, err
}

type modelAndError struct{ answers }

func (p modelAndError) InferFromEvents(ctx context.Context, d domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	res, err := p.answers.InferFromEvents(ctx, d)
	if err == nil {
		err = errors.New("low confidence")
	}
	return res, err
}

type modelAndErrorWhenCancelled struct{ answers }

func (p modelAndErrorWhenCancelled) InferFromEvents(ctx context.Context, d domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	res, err := p.answers.InferFromEvents(context.Background(), d)
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	return res, err
}

type promptDropsResources struct{ queuePack }

func (p promptDropsResources) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Resources = nil
	return c
}

type panicsEverywhere struct{ queuePack }

func (panicsEverywhere) NodeCatalog() []domainpack.NodeDefinition { panic("boom") }

func TestBrokenPacksAreCaught(t *testing.T) {
	m := queueModel(t)
	cases := []struct {
		pack   domainpack.DomainPack
		check  string
		detail string // when set, the finding's detail must contain it
	}{
		{badID{}, "identity/id", ""},
		{otherID{}, "identity/models", ""},
		{dupKind{}, "catalog/nodes", ""},
		{nonGenericType{}, "catalog/nodes", ""},
		{badRequirement{}, "catalog/nodes", ""},
		{sharedCatalog{}, "catalog/copies", ""},
		{badResourceType{}, "catalog/resources", ""},
		{acceptsAnything{}, "validate/fail-closed", ""},
		{acceptsAnything{}, "validate/nil", ""},
		{rejectsValid{}, "validate/valid-models", ""},
		{mutatingValidate{}, "validate/read-only", ""},
		{flakyValidate{n: new(atomic.Int64)}, "validate/deterministic", ""},
		{panicsOnNil{}, "validate/nil", ""},
		{missingNode{}, "views/covers-nodes", ""},
		{ghostNode{}, "views/same-ids", ""},
		{movesSaved{}, "views/keeps-saved", ""},
		{overlapping{}, "views/no-overlap", ""},
		{mutatingViews{}, "views/read-only", ""},
		{mutatingMetrics{}, "metrics/read-only", ""},
		{mutatingMetrics{}, "metrics/generic-unchanged", ""},
		{invalidMetric{}, "metrics/schema", ""},
		{emptyKey{}, "metrics/keys", ""},
		{flakyMetrics{n: new(atomic.Int64)}, "metrics/deterministic", ""},
		{nilNil{}, "inference/answer", ""},
		{ignoresCancel{m: m}, "inference/cancel", ""},
		{proposesForeign{m: m}, "inference/answer", ""},
		{unavailableWithResult{}, "inference/answer", ""},
		{proposesInvalid{m: m}, "inference/answer", ""},
		{promptOtherDomain{}, "prompt/context", ""},
		{promptDropsNodes{}, "prompt/catalogs", ""},
		{promptChanges{n: new(atomic.Int64)}, "prompt/static", ""},
		{panicsEverywhere{}, "catalog/nodes", ""},
		{flakyIDPack{flakyID: flakyID{n: new(atomic.Int64)}}, "identity/id", ""},
		{emptyCatalog{}, "catalog/nodes", ""},
		{noLabel{}, "catalog/nodes", ""},
		{dupResource{}, "catalog/resources", ""},
		{flakyViews{n: new(atomic.Int64)}, "views/deterministic", ""},
		{unencodableViews{}, "views/valid-ir", ""},
		{emptySummary{}, "prompt/context", ""},
		{emptyRule{}, "prompt/context", ""},
		{nanMetric{}, "metrics/run", ""},
		{panicsOnEnginePath{}, "metrics/run", ""},
		{rewritesSeed{}, "metrics/generic-unchanged", ""},
		{rewritesQueues{}, "metrics/generic-unchanged", ""},
		{rewritesAdditional{}, "metrics/generic-unchanged", ""},
		{leaksIntoPrompt{}, "prompt/static", ""},
		{infersFromNothing{answers{m: m}}, "inference/needs-input", ""},
		{sharedResourceTypes{}, "catalog/copies", ""},
		{sharedResources{}, "catalog/copies", ""},
		{mismatchWithoutPath{}, "validate/fail-closed", ""},
		{mismatchAsWarning{}, "validate/fail-closed", ""},
		{layoutOnlyWithSavedPositions{}, "views/covers-nodes", ""},
		{ignoresSavedWhenPlacing{}, "views/no-overlap", ""},
		{promptDropsResources{}, "prompt/catalogs", ""},
		{countsIntoPrompt{}, "prompt/static", ""},
		// Review round 2: the sentinel must reach the pack through each
		// exposure, and be recognised as customer text (not just a change).
		{leaksIntoPrompt{}, "prompt/static", "carries text"},
		{leaksViewsIntoPrompt{}, "prompt/static", "carries text"},
		{leaksEventsIntoPrompt{}, "prompt/static", "carries text"},
		{remembersLastModel{last: new(atomic.Pointer[string])}, "stateless", "returns text"},
		{countsIntoIssues{n: new(atomic.Int64)}, "stateless", "answers differently"},
		{viewsRememberLastModel{last: new(atomic.Pointer[string])}, "stateless", "answers differently"},
		{countsMetrics{n: new(atomic.Int64)}, "stateless", "answers differently"},
		{mintsModelIDs{answers{m: m}, new(atomic.Int64)}, "inference/deterministic", ""},
		{countsInference{n: new(atomic.Int64)}, "inference/deterministic", ""},
		{modelAndErrorWhenEmpty{answers{m: m}}, "inference/needs-input", ""},
		{modelAndError{answers{m: m}}, "inference/answer", "with a result"},
		{modelAndErrorWhenCancelled{answers{m: m}}, "inference/cancel", ""},
	}
	for _, c := range cases {
		name := typeName(c.pack)
		t.Run(name+"/"+c.check, func(t *testing.T) {
			findings, err := Check(context.Background(), Suite{Pack: c.pack, Models: []model.ModelIR{m}, Horizon: simulation.Duration{Value: 4, Unit: model.Hour}})
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range findings {
				if f.Check == c.check && strings.Contains(f.Detail, c.detail) {
					return
				}
			}
			t.Errorf("no %s finding (%q); got %v", c.check, c.detail, findings)
		})
	}
	// Correct packs have none: one that declines inference, one that
	// answers it, and one whose ID is 123 characters long (the suite's
	// renamed domain must stay valid, T-042 review B3).
	long := strings.Repeat("d", 123)
	lm := clone(&m)
	lm.Domain = long
	for name, s := range map[string]Suite{
		"declines": {Pack: queuePack{}, Models: []model.ModelIR{m}},
		"answers":  {Pack: answers{m: m}, Models: []model.ModelIR{m}},
		"long ID":  {Pack: longID{id: long}, Models: []model.ModelIR{lm}},
		// A pack whose ID is the suite's first renamed domain gets the other.
		"renamed ID": {Pack: longID{id: renamedDomains[0]}, Models: []model.ModelIR{func() model.ModelIR { r := clone(&m); r.Domain = renamedDomains[0]; return r }()}},
	} {
		if findings, err := Check(context.Background(), s); err != nil || len(findings) > 0 {
			t.Errorf("%s: %v %v", name, err, findings)
		}
	}
}

// A model the generic validator accepts but the engine cannot run (an
// empirical distribution without its dataset) is a finding with the
// engine's issues, not a pass.
func TestUnrunnableModel(t *testing.T) {
	m := queueModel(t)
	m.Nodes[2].ServiceTime.Distribution = model.Distribution{Type: model.DistEmpirical, SamplesRef: "dataset:missing"}
	findings, err := Check(context.Background(), Suite{Pack: queuePack{}, Models: []model.ModelIR{m}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 || findings[0].Check != "metrics/run" || !strings.Contains(findings[0].Detail, "dataset:missing") {
		t.Errorf("findings %v", findings)
	}
}

// Run turns findings into test failures: the helper below runs a broken
// pack through Run in a child test process, which must fail and name the
// finding.
func TestRunReportsFindings(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperRunBrokenPack$", "-test.count=1")
	cmd.Env = append(os.Environ(), "CONFORMANCE_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "does not conform: validate/fail-closed") {
		t.Errorf("Run passed a broken pack (err %v):\n%s", err, out)
	}
}

func TestHelperRunBrokenPack(t *testing.T) {
	if os.Getenv("CONFORMANCE_HELPER") != "1" {
		return // only meaningful in the child process of TestRunReportsFindings
	}
	Run(t, Suite{Pack: acceptsAnything{}, Models: []model.ModelIR{queueModel(t)}})
}

// An invalid suite fails Run instead of passing silently (T-042 review C).
func TestRunFailsInvalidSuites(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperRunInvalidSuite$", "-test.count=1")
	cmd.Env = append(os.Environ(), "CONFORMANCE_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "invalid suite") {
		t.Errorf("Run passed an invalid suite (err %v):\n%s", err, out)
	}
}

func TestHelperRunInvalidSuite(t *testing.T) {
	if os.Getenv("CONFORMANCE_HELPER") != "1" {
		return // only meaningful in the child process of TestRunFailsInvalidSuites
	}
	Run(t, Suite{Pack: queuePack{}})
}

// A suite without models, or with a model the generic validator rejects,
// is an error rather than a pass.
func TestInvalidSuites(t *testing.T) {
	bad := queueModel(t)
	bad.Flows[0].To = "nowhere"
	for _, s := range []Suite{{Pack: queuePack{}}, {Models: []model.ModelIR{queueModel(t)}}, {Pack: queuePack{}, Models: []model.ModelIR{bad}}} {
		if _, err := Check(context.Background(), s); !errors.Is(err, ErrSuite) {
			t.Errorf("%+v: %v", s.Pack, err)
		}
	}
}

// The concurrency check reports goroutines that disagree with a sequential
// call (data races themselves are for -race).
func TestConcurrentDisagreement(t *testing.T) {
	m := queueModel(t)
	c := &checker{s: Suite{Pack: promptChanges{n: new(atomic.Int64)}, Models: []model.ModelIR{m}}}
	c.concurrent(context.Background())
	if len(c.findings) == 0 || c.findings[0].Check != "concurrent" {
		t.Errorf("findings %v", c.findings)
	}
	plain, err := runWithPack(func() simulation.RunInput {
		in, _ := (&checker{s: Suite{Models: []model.ModelIR{m}}}).freeze(m)
		return in
	}(), queuePack{})
	if err != nil {
		t.Fatal(err)
	}
	// The metrics step keeps an engine result, so the concurrency and
	// static checks can call ComputeMetrics.
	kept := &checker{s: Suite{Pack: queuePack{}, Models: []model.ModelIR{m}}}
	kept.metrics(context.Background())
	if kept.result == nil {
		t.Fatal("the metrics step kept no engine result")
	}
	c = &checker{s: Suite{Pack: countsMetrics{n: new(atomic.Int64)}, Models: []model.ModelIR{m}}, result: plain}
	c.concurrent(context.Background())
	if len(c.findings) == 0 || c.findings[0].Check != "concurrent" {
		t.Errorf("ComputeMetrics disagreeing across goroutines: %v", c.findings)
	}
	c = &checker{s: Suite{Pack: countsInference{n: new(atomic.Int64)}, Models: []model.ModelIR{m}}}
	c.concurrent(context.Background())
	if len(c.findings) == 0 || c.findings[0].Check != "concurrent" || !strings.Contains(c.findings[0].Detail, "InferFromEvents disagrees") {
		t.Errorf("InferFromEvents disagreeing across goroutines: %v", c.findings)
	}
	c = &checker{s: Suite{Pack: panicsLater{n: new(atomic.Int64)}, Models: []model.ModelIR{m}}}
	c.concurrent(context.Background())
	if len(c.findings) == 0 || !strings.Contains(c.findings[0].Detail, "panicked") {
		t.Errorf("panicking goroutines: %v", c.findings)
	}
	c = &checker{s: Suite{Pack: queuePack{}, Models: []model.ModelIR{m}}}
	c.concurrent(context.Background())
	if len(c.findings) != 0 {
		t.Errorf("queuePack: %v", c.findings)
	}
}

type longID struct {
	queuePack
	id string
}

func (p longID) ID() string { return p.id }
func (p longID) Validate(ir *model.ModelIR) []model.ValidationIssue {
	if ir == nil || ir.Domain != p.id {
		return []model.ValidationIssue{model.NewIssue(DomainMismatch, "/domain", "other domain", model.SeverityError)}
	}
	return nil
}
func (p longID) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Domain = p.id
	return c
}

// countsMetrics returns how often ComputeMetrics ran: concurrent calls
// disagree with the sequential one.
type countsMetrics struct {
	queuePack
	n *atomic.Int64
}

func (p countsMetrics) ComputeMetrics(*simulation.Result) map[string]simulation.MetricSummary {
	v := float64(p.n.Add(1))
	return map[string]simulation.MetricSummary{"calls": {Status: simulation.Available, Mean: &v, P50: &v, P95: &v, SampleCount: 1, Population: simulation.PopulationReplication, Unit: "call"}}
}

// countsInference answers with how often it was asked.
type countsInference struct {
	queuePack
	n *atomic.Int64
}

func (p countsInference) InferFromEvents(context.Context, domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	return nil, fmt.Errorf("%w: call %d", domainpack.ErrUnavailable, p.n.Add(1))
}

type panicsLater struct {
	queuePack
	n *atomic.Int64
}

func (p panicsLater) PromptContext() domainpack.PromptContext {
	if p.n.Add(1) > 1 {
		panic("second call")
	}
	return p.queuePack.PromptContext()
}

func typeName(v any) string { return strings.TrimPrefix(fmt.Sprintf("%T", v), "conformance.") }
