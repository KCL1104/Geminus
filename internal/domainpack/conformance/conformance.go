// Package conformance is the DomainPack conformance suite (T-042): the
// behaviour every pack needs so that the generic kernel serves its domain
// from the one Generic Twin IR, with no second model, renderer topology or
// engine. A pack's tests call Run with valid models of its domain; the suite
// names no concrete pack (scripts/check-architecture enforces it).
//
// Test-only: imported from _test.go files, never from production code.
package conformance

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"regexp"
	"slices"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/des"
	"github.com/kcl1104/geminus/internal/simulation/engine"
)

// Suite is one pack and valid models of its domain.
type Suite struct {
	Pack domainpack.DomainPack
	// Models are IRs of the pack's domain that pass the generic validator.
	Models []model.ModelIR
	// Horizon and Replications of the engine runs; zero means one day and two.
	Horizon      simulation.Duration
	Replications int
	// Datasets resolves the models' empirical distributions (logical_ref ->
	// frozen samples), as the worker would before a run.
	Datasets map[string]simulation.ResolvedDataset
}

// Finding is one property a pack does not have.
type Finding struct {
	Check  string // stable name, e.g. "views/covers-nodes"
	Detail string
}

func (f Finding) String() string { return f.Check + ": " + f.Detail }

// Run reports every finding of Check as a test error.
func Run(t *testing.T, s Suite) {
	t.Helper()
	findings, err := Check(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		t.Errorf("%s does not conform: %s", s.Pack.ID(), f)
	}
}

// DomainMismatch is the issue a pack returns for an IR of another domain
// (docs/TWIN_IR_SPEC.md issue codes; REQ-003 fail closed).
const DomainMismatch = "DOMAIN_MISMATCH"

var (
	domainPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`) // model-ir.schema.json "domain"
	nodeTypes     = []model.NodeType{model.NodeSource, model.NodeQueue, model.NodeProcess, model.NodeDecision,
		model.NodeDelay, model.NodeSink, model.NodeFailure, model.NodeRework}
	resourceTypes = []string{"human", "machine", "generic"}
)

// ErrSuite: the suite itself is misconfigured (no models, or a model the
// generic validator rejects), so no finding can be trusted.
var ErrSuite = errors.New("conformance: invalid suite")

type checker struct {
	s        Suite
	findings []Finding
}

func (c *checker) fail(check, format string, args ...any) {
	c.findings = append(c.findings, Finding{check, fmt.Sprintf(format, args...)})
}

// call runs one pack method; a panic is a finding, not a crash of the suite.
func (c *checker) call(check string, f func()) (ok bool) {
	defer func() {
		if v := recover(); v != nil {
			c.fail(check, "panicked: %v", v)
			ok = false
		}
	}()
	f()
	return true
}

// Check runs every property against the pack and returns what fails, in a
// fixed order.
func Check(ctx context.Context, s Suite) ([]Finding, error) {
	if s.Pack == nil || len(s.Models) == 0 {
		return nil, fmt.Errorf("%w: a pack and at least one model are required", ErrSuite)
	}
	for i := range s.Models {
		if issues := genericIssues(&s.Models[i]); len(issues) > 0 {
			return nil, fmt.Errorf("%w: model %d is not valid: %v", ErrSuite, i, issues)
		}
	}
	c := &checker{s: s}
	for _, step := range []struct {
		name string
		run  func()
	}{
		{"identity", c.identity}, {"catalog", c.catalogs}, {"validate", c.validate}, {"views", c.views},
		{"metrics", func() { c.metrics(ctx) }}, {"inference", func() { c.inference(ctx) }},
		{"prompt", c.prompt}, {"concurrent", c.concurrent},
	} {
		// Each step guards its own pack calls; this catches the rest.
		c.call(step.name, step.run)
	}
	return c.findings, nil
}

// genericIssues is the generic validator's verdict (schema and semantics) on ir.
func genericIssues(ir *model.ModelIR) []model.ValidationIssue {
	data, err := model.Encode(ir)
	if err != nil {
		return []model.ValidationIssue{model.NewIssue("ENCODE", "", err.Error(), model.SeverityError)}
	}
	res, err := validate.IR(data)
	if err != nil {
		return []model.ValidationIssue{model.NewIssue("VALIDATE", "", err.Error(), model.SeverityError)}
	}
	var out []model.ValidationIssue
	for _, i := range res.Issues {
		if i.Severity == model.SeverityError {
			out = append(out, i)
		}
	}
	return out
}

// clone deep-copies ir through its JSON encoding, so a pack that changes
// its argument is detected by comparing encodings.
func clone(ir *model.ModelIR) model.ModelIR {
	data, err := model.Encode(ir)
	if err != nil {
		panic(err)
	}
	var out model.ModelIR
	if err := model.Decode(data, &out); err != nil {
		panic(err)
	}
	return out
}

func encoded(v any) string {
	data, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return "unencodable: " + err.Error()
	}
	return string(data)
}

func (c *checker) identity() {
	var id, again string
	if !c.call("identity/id", func() { id, again = c.s.Pack.ID(), c.s.Pack.ID() }) {
		return
	}
	if !domainPattern.MatchString(id) {
		c.fail("identity/id", "ID %q is not a valid IR domain value", id)
	}
	if id != again {
		c.fail("identity/id", "ID changes between calls: %q, %q", id, again)
	}
	for i, m := range c.s.Models {
		if m.Domain != id {
			c.fail("identity/models", "model %d has domain %q, not %q", i, m.Domain, id)
		}
	}
}

func (c *checker) catalogs() {
	var nodes, nodes2 []domainpack.NodeDefinition
	var res, res2 []domainpack.ResourceDefinition
	if !c.call("catalog/nodes", func() { nodes = c.s.Pack.NodeCatalog() }) ||
		!c.call("catalog/resources", func() { res = c.s.Pack.ResourceCatalog() }) {
		return
	}
	if len(nodes) == 0 {
		c.fail("catalog/nodes", "empty node catalog")
	}
	kinds := map[string]bool{}
	for _, n := range nodes {
		if n.Kind == "" || kinds[n.Kind] {
			c.fail("catalog/nodes", "kind %q is empty or repeated", n.Kind)
		}
		kinds[n.Kind] = true
		if !slices.Contains(nodeTypes, n.Type) {
			c.fail("catalog/nodes", "kind %q is built on %q, not a generic node type", n.Kind, n.Type)
		}
		if n.Label == "" {
			c.fail("catalog/nodes", "kind %q has no label", n.Kind)
		}
		for _, rt := range n.ResourceTypes {
			if !slices.Contains(resourceTypes, rt) {
				c.fail("catalog/nodes", "kind %q requires resource type %q", n.Kind, rt)
			}
		}
	}
	kinds = map[string]bool{}
	for _, r := range res {
		if r.Kind == "" || kinds[r.Kind] {
			c.fail("catalog/resources", "kind %q is empty or repeated", r.Kind)
		}
		kinds[r.Kind] = true
		if !slices.Contains(resourceTypes, r.Type) {
			c.fail("catalog/resources", "kind %q has type %q", r.Kind, r.Type)
		}
	}
	// Callers may change what they get; the pack must not change with it.
	before := encoded([]any{nodes, res})
	for i := range nodes {
		nodes[i].Kind += "~changed"
		for j := range nodes[i].ResourceTypes {
			nodes[i].ResourceTypes[j] += "~changed"
		}
	}
	for i := range res {
		res[i].Kind += "~changed"
	}
	if !c.call("catalog/copies", func() { nodes2, res2 = c.s.Pack.NodeCatalog(), c.s.Pack.ResourceCatalog() }) {
		return
	}
	if encoded([]any{nodes2, res2}) != before {
		c.fail("catalog/copies", "changing a returned catalog changed the pack")
	}
}

func (c *checker) validate() {
	id := c.s.Pack.ID()
	for i := range c.s.Models {
		ir := clone(&c.s.Models[i])
		before := encoded(&ir)
		var issues, again []model.ValidationIssue
		if !c.call("validate/valid-models", func() { issues, again = c.s.Pack.Validate(&ir), c.s.Pack.Validate(&ir) }) {
			continue
		}
		for _, is := range issues {
			if is.Severity == model.SeverityError {
				c.fail("validate/valid-models", "model %d: %s at %q: %s", i, is.Code, is.Path, is.Message)
			}
		}
		if encoded(&ir) != before {
			c.fail("validate/read-only", "model %d: Validate changed the IR", i)
		}
		if encoded(issues) != encoded(again) {
			c.fail("validate/deterministic", "model %d: two calls disagree", i)
		}
		other := clone(&c.s.Models[i])
		other.Domain = id + "_other"
		var foreign []model.ValidationIssue
		if c.call("validate/fail-closed", func() { foreign = c.s.Pack.Validate(&other) }) &&
			!slices.ContainsFunc(foreign, func(is model.ValidationIssue) bool {
				return is.Code == DomainMismatch && is.Path == "/domain" && is.Severity == model.SeverityError
			}) {
			c.fail("validate/fail-closed", "model %d with domain %q: no %s error at /domain (got %v)", i, other.Domain, DomainMismatch, foreign)
		}
		// The generic validator never reads the domain (REQ-003): the
		// same IR under another domain has the same generic issues.
		if a, b := encoded(validate.Semantic(&ir)), encoded(validate.Semantic(&other)); a != b {
			c.fail("validate/generic-ignores-domain", "model %d: %s vs %s", i, a, b)
		}
	}
	var nilIssues []model.ValidationIssue
	if c.call("validate/nil", func() { nilIssues = c.s.Pack.Validate(nil) }) &&
		!slices.ContainsFunc(nilIssues, func(is model.ValidationIssue) bool { return is.Severity == model.SeverityError }) {
		c.fail("validate/nil", "a nil IR is not an error")
	}
}

// views: DefaultViews completes the 2D positions of the one IR, keyed by its
// node IDs, keeps every saved position, and the IR carrying them is still
// valid; 3D derives from 2D, so no pack supplies another topology.
func (c *checker) views() {
	for i := range c.s.Models {
		for _, variant := range []string{"as given", "without positions", "with one saved position"} {
			ir := clone(&c.s.Models[i])
			switch variant {
			case "without positions":
				ir.Views.TwoD.Positions = nil
			case "with one saved position":
				ir.Views.TwoD.Positions = map[string]model.Position{ir.Nodes[len(ir.Nodes)-1].ID: {X: -7777, Y: 3333}}
			}
			saved := maps(ir.Views.TwoD.Positions)
			before := encoded(&ir)
			var v, again domainpack.ViewDefinitions
			if !c.call("views/default", func() { v, again = c.s.Pack.DefaultViews(&ir), c.s.Pack.DefaultViews(&ir) }) {
				continue
			}
			where := fmt.Sprintf("model %d %s", i, variant)
			if encoded(&ir) != before {
				c.fail("views/read-only", "%s: DefaultViews changed the IR", where)
			}
			if encoded(v) != encoded(again) {
				c.fail("views/deterministic", "%s: two calls disagree", where)
			}
			ids := map[string]bool{}
			for _, n := range ir.Nodes {
				ids[n.ID] = true
				if _, ok := v.Positions[n.ID]; !ok {
					c.fail("views/covers-nodes", "%s: no position for node %q", where, n.ID)
				}
			}
			at := map[model.Position]string{}
			for id, p := range v.Positions {
				if !ids[id] {
					c.fail("views/same-ids", "%s: position for %q, which is not a node of the IR", where, id)
				}
				if other, dup := at[p]; dup && ids[id] {
					c.fail("views/no-overlap", "%s: %q and %q share %v", where, id, other, p)
				}
				at[p] = id
			}
			for id, p := range saved {
				if v.Positions[id] != p {
					c.fail("views/keeps-saved", "%s: %q moved from %v to %v", where, id, p, v.Positions[id])
				}
			}
			ir.Views.TwoD.Positions = v.Positions
			if issues := genericIssues(&ir); len(issues) > 0 {
				c.fail("views/valid-ir", "%s: the IR with its default views is invalid: %v", where, issues)
			}
		}
	}
}

func mapKeys[V any](m map[string]V) iter.Seq[string] {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func maps(m map[string]model.Position) map[string]model.Position {
	out := make(map[string]model.Position, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// freeze builds the run input the worker would for ir.
func (c *checker) freeze(ir model.ModelIR) (simulation.RunInput, error) {
	data, err := model.Encode(&ir)
	if err != nil {
		return simulation.RunInput{}, err
	}
	h, err := hashing.Model(data)
	if err != nil {
		return simulation.RunInput{}, err
	}
	horizon, reps := c.s.Horizon, c.s.Replications
	if horizon.Value == 0 {
		horizon = simulation.Duration{Value: 1, Unit: model.Day}
	}
	if reps == 0 {
		reps = 2
	}
	var manifest []model.DatasetBinding
	for _, ref := range slices.Sorted(mapKeys(c.s.Datasets)) {
		manifest = append(manifest, c.s.Datasets[ref].Binding)
	}
	return simulation.RunInput{Model: ir, ModelHash: h, EffectiveModelHash: h, Datasets: c.s.Datasets, Manifest: manifest,
		Config: simulation.RunConfig{Horizon: horizon, Warmup: simulation.Duration{Value: 0, Unit: horizon.Unit},
			Replications: reps, Seed: 42, TraceLevel: simulation.TraceNone},
		Limits: simulation.ExecutionLimits{WallTimeoutSeconds: 300, MaxEvents: 10_000_000, MaxActiveEntities: 100_000,
			MaxEntityVisits: 10_000, MaxTraceEvents: 0}}, nil
}

// metrics: the pack's metrics ride on the generic engine's Result without
// changing it, are schema-valid summaries, and are reproducible; the
// engine's own numbers do not depend on the domain.
func (c *checker) metrics(ctx context.Context) {
	for i := range c.s.Models {
		in, err := c.freeze(clone(&c.s.Models[i]))
		if err != nil {
			c.fail("metrics/run", "model %d: %v", i, err)
			continue
		}
		plain, err := (&des.Engine{}).Run(ctx, in, nil)
		if err != nil {
			var ie *engine.InputError
			if errors.As(err, &ie) {
				err = fmt.Errorf("%w: %v", err, ie.Issues)
			}
			c.fail("metrics/run", "model %d: the engine rejected a valid model: %v", i, err)
			continue
		}
		cp := clonedResult(plain)
		before := encoded(cp)
		var got map[string]simulation.MetricSummary
		if !c.call("metrics/compute", func() { got = c.s.Pack.ComputeMetrics(cp) }) {
			continue
		}
		if encoded(cp) != before {
			c.fail("metrics/read-only", "model %d: ComputeMetrics changed the engine result", i)
		}
		for k := range got {
			if k == "" {
				c.fail("metrics/keys", "model %d: a metric has an empty key", i)
			}
		}
		var res *simulation.Result
		if !c.call("metrics/compute", func() {
			res, err = (&des.Engine{DomainMetrics: c.s.Pack.ComputeMetrics}).Run(ctx, in, nil)
		}) {
			continue
		}
		if err != nil {
			c.fail("metrics/run", "model %d: %v", i, err)
			continue
		}
		withRun := *res
		withRun.RunID = "sim_01M4YDQK00FGZZWH0PP15XPNFA" // the worker sets run identity
		doc, _ := json.Marshal(withRun)
		if issues, err := validate.Document(doc, "simulation.schema.json#/$defs/Result", validate.IRSchemaInvalid); err != nil || len(issues) > 0 {
			c.fail("metrics/schema", "model %d: the Result with the pack's metrics is not schema-valid: %v %v", i, err, issues)
		}
		if g1, g2 := genericPart(res), genericPart(plain); g1 != g2 {
			c.fail("metrics/generic-unchanged", "model %d: the pack changed the engine's generic metrics", i)
		}
		again, err := (&des.Engine{DomainMetrics: c.s.Pack.ComputeMetrics}).Run(ctx, in, nil)
		if err != nil || again.Reproducibility.ResultDigest != res.Reproducibility.ResultDigest {
			c.fail("metrics/deterministic", "model %d: a second run has another digest (%v)", i, err)
		}
		// The engine never reads the domain: the same IR under another
		// domain has the same generic numbers.
		other := clone(&c.s.Models[i])
		other.Domain = c.s.Pack.ID() + "_other"
		oin, err := c.freeze(other)
		if err == nil {
			var ores *simulation.Result
			if ores, err = (&des.Engine{}).Run(ctx, oin, nil); err == nil && genericPart(ores) != genericPart(plain) {
				c.fail("metrics/engine-ignores-domain", "model %d: renaming the domain changed the engine's numbers", i)
			}
		}
		if err != nil {
			c.fail("metrics/engine-ignores-domain", "model %d: %v", i, err)
		}
	}
}

func clonedResult(r *simulation.Result) *simulation.Result {
	data, err := json.Marshal(r)
	if err != nil {
		panic(err)
	}
	var out simulation.Result
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return &out
}

// genericPart is the engine-owned content of a result: everything but the
// domain metrics, the digest over them and the hashes of the frozen input.
func genericPart(r *simulation.Result) string {
	cp := *r
	cp.Metrics.DORA = nil
	cp.Reproducibility = simulation.Reproducibility{}
	return encoded(cp)
}

// inference: a pack either declines with ErrUnavailable or proposes a model
// of its own domain that the generic validator accepts; it never answers
// (nil, nil) and stops when its context is cancelled.
func (c *checker) inference(ctx context.Context) {
	empty := domainpack.CanonicalEventDataset{DatasetVersionID: "dsv_conformance"}
	var res *domainpack.InferenceResult
	var err error
	if !c.call("inference/answer", func() { res, err = c.s.Pack.InferFromEvents(ctx, empty) }) {
		return
	}
	switch {
	case errors.Is(err, domainpack.ErrUnavailable):
		if res != nil {
			c.fail("inference/answer", "ErrUnavailable with a result")
		}
	case err != nil:
		// A typed refusal of an empty dataset is acceptable.
	case res == nil || res.Model == nil:
		c.fail("inference/answer", "no error and no model")
	default:
		if res.Model.Domain != c.s.Pack.ID() {
			c.fail("inference/answer", "proposed a model of domain %q", res.Model.Domain)
		}
		if issues := genericIssues(res.Model); len(issues) > 0 {
			c.fail("inference/answer", "proposed an invalid model: %v", issues)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if c.call("inference/cancel", func() { res, err = c.s.Pack.InferFromEvents(cancelled, empty) }) && err == nil {
		c.fail("inference/cancel", "answered with a cancelled context")
	}
}

func (c *checker) prompt() {
	var p, again domainpack.PromptContext
	if !c.call("prompt/context", func() { p, again = c.s.Pack.PromptContext(), c.s.Pack.PromptContext() }) {
		return
	}
	if p.Domain != c.s.Pack.ID() {
		c.fail("prompt/context", "domain %q, not %q", p.Domain, c.s.Pack.ID())
	}
	if p.Summary == "" {
		c.fail("prompt/context", "empty summary")
	}
	if !reflect.DeepEqual(p.Nodes, c.s.Pack.NodeCatalog()) || !reflect.DeepEqual(p.Resources, c.s.Pack.ResourceCatalog()) {
		c.fail("prompt/catalogs", "the prompt's catalogs are not the pack's catalogs")
	}
	for _, r := range p.Rules {
		if r == "" {
			c.fail("prompt/context", "an empty rule")
		}
	}
	if encoded(p) != encoded(again) {
		c.fail("prompt/static", "two calls disagree: a prompt context is static pack data")
	}
}

// concurrent: packs must be safe for concurrent use (the API and worker
// share one registry). Disagreement is reported here; data races are found
// by running the suite under -race (make backend-race).
func (c *checker) concurrent() {
	ir := clone(&c.s.Models[0])
	calls := func(ir *model.ModelIR) string {
		return encoded([]any{c.s.Pack.NodeCatalog(), c.s.Pack.ResourceCatalog(), c.s.Pack.Validate(ir), c.s.Pack.DefaultViews(ir), c.s.Pack.PromptContext()})
	}
	var want string
	if !c.call("concurrent", func() { want = calls(&ir) }) {
		return
	}
	const n = 8
	got := make([]string, n)
	var wg sync.WaitGroup
	var panics sync.Map
	for g := range n {
		wg.Go(func() {
			defer func() {
				if v := recover(); v != nil {
					panics.Store(g, v)
				}
			}()
			local := clone(&ir)
			got[g] = calls(&local)
		})
	}
	wg.Wait()
	panics.Range(func(k, v any) bool {
		c.fail("concurrent", "goroutine %v panicked: %v", k, v)
		return true
	})
	for g, s := range got {
		if s != want && s != "" {
			c.fail("concurrent", "goroutine %d disagrees with a sequential call", g)
		}
	}
}
