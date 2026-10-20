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
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"regexp"
	"slices"
	"strings"
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
	initial  string             // prompt context and catalogs before the pack saw any IR
	result   *simulation.Result // an engine result of the first model, for the concurrency check
}

// renamedDomains: a copy of a model gets one of these to show that the
// generic validator and engine never read the domain. They share no prefix,
// suffix or length with a realistic pack ID, so no test on the domain can
// treat both alike (T-042 review B1, B3).
var renamedDomains = []string{"zz.conformance:renamed", "qq.conformance:second"}

func (c *checker) renamed() string {
	if c.s.Pack.ID() == renamedDomains[0] {
		return renamedDomains[1]
	}
	return renamedDomains[0]
}

// sentinel is user text a pack must never carry into static pack data.
const sentinel = "CONFORMANCE-SENTINEL-c0ffee: customer roadmap"

// sentinelID prefixes every ID of the sentinel model: IDs are customer
// text too (T-042 third review L3).
const sentinelID = "sentinelc0ffee_"

// sentinelModel is a copy of m in which every name and ID carries the
// sentinel. References are renamed with their targets, so it is the same
// model under another tenant's names.
func sentinelModel(m *model.ModelIR) model.ModelIR {
	ir := clone(m)
	id := func(s string) string {
		if s == "" {
			return s
		}
		return (sentinelID + s)[:min(len(sentinelID)+len(s), 128)]
	}
	ir.Name = sentinel
	for i := range ir.EntityTypes {
		ir.EntityTypes[i].ID, ir.EntityTypes[i].Name = id(ir.EntityTypes[i].ID), sentinel
	}
	for i := range ir.Resources {
		ir.Resources[i].ID, ir.Resources[i].Name = id(ir.Resources[i].ID), sentinel
	}
	for i := range ir.Nodes {
		n := &ir.Nodes[i]
		n.ID, n.Name, n.EntityType = id(n.ID), sentinel, id(n.EntityType)
		for j := range n.Requires {
			n.Requires[j].ResourceID = id(n.Requires[j].ResourceID)
		}
	}
	for i := range ir.Flows {
		f := &ir.Flows[i]
		f.ID, f.From, f.To = id(f.ID), id(f.From), id(f.To)
	}
	for i := range ir.Constraints {
		ir.Constraints[i].NodeID = id(ir.Constraints[i].NodeID)
	}
	for i := range ir.Metrics {
		mt := &ir.Metrics[i]
		mt.ID, mt.SinkID, mt.ResourceID, mt.NodeID = id(mt.ID), id(mt.SinkID), id(mt.ResourceID), id(mt.NodeID)
	}
	if pos := ir.Views.TwoD.Positions; pos != nil {
		ir.Views.TwoD.Positions = make(map[string]model.Position, len(pos))
		for k, v := range pos {
			ir.Views.TwoD.Positions[id(k)] = v
		}
	}
	return ir
}

// events is a small non-empty canonical event dataset.
var events = domainpack.CanonicalEventDataset{DatasetVersionID: "dsv_01M4YDQK00CNF0RMANCE00000E", Events: []domainpack.CanonicalEvent{
	{CaseID: "case-1", EventType: "started", Timestamp: "2026-10-15T09:00:00Z"},
	{CaseID: "case-1", EventType: "finished", Timestamp: "2026-10-15T10:30:00Z"},
	{CaseID: "case-2", EventType: "started", Timestamp: "2026-10-15T09:20:00Z"},
}}

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
		// Static pack data is read before the pack sees any model, and
		// compared at the end (prompt/static).
		{"prompt/context", func() { c.initial = c.staticData() }},
		{"identity", c.identity}, {"catalog", c.catalogs}, {"validate", c.validate}, {"views", c.views}, {"templates", c.templates},
		{"metrics", func() { c.metrics(ctx) }}, {"inference", func() { c.inference(ctx) }},
		{"prompt", c.prompt}, {"concurrent", func() { c.concurrent(ctx) }}, {"prompt/static", func() { c.static(ctx) }},
	} {
		// Each step guards its own pack calls; this catches the rest.
		c.call(step.name, step.run)
	}
	return c.findings, nil
}

// allIssues is everything the generic validator reports on ir, warnings included.
func allIssues(ir *model.ModelIR) []model.ValidationIssue {
	data, err := model.Encode(ir)
	if err != nil {
		return []model.ValidationIssue{model.NewIssue("ENCODE", "", err.Error(), model.SeverityError)}
	}
	res, err := validate.IR(data)
	if err != nil {
		return []model.ValidationIssue{model.NewIssue("VALIDATE", "", err.Error(), model.SeverityError)}
	}
	return res.Issues
}

// variantCodes is the error each invalid variant must produce: one per
// validator stage after the schema (primitive support, limits, ids, refs,
// routing, distributions, constraints) and the zero-time cycle of TEST-002.
var variantCodes = map[string]string{
	// D-063 (PROPOSED): a declared system_wip metric, gated until T-051.
	"line-flow primitive not implemented": validate.PrimitiveNotSupported,
	"unknown flow target":                 validate.UnknownReference,
	"probabilities not summing to 1":      validate.RoutingInvalid,
	"duplicate node ID":                   validate.DuplicateID,
	"no sink":                             validate.NoReachableSink,
	"bad triangular distribution":         validate.DistributionInvalid,
	"metadata nested too deep":            validate.LimitExceeded,
	"constraint not met":                  validate.ConstraintFailed,
	"zero-time cycle":                     validate.ZeroTimeCycle,
}

// invalidVariants are copies of ir that break one rule each (variantCodes,
// TEST-002), so the domain-invariance check also compares the validator's
// error rules, not only an empty issue list.
func invalidVariants(ir *model.ModelIR) map[string]model.ModelIR {
	out := map[string]model.ModelIR{}
	gated := clone(ir)
	gated.Metrics = append(gated.Metrics, model.Metric{ID: "zz_conformance_wip", Type: model.MetricSystemWIP})
	out["line-flow primitive not implemented"] = gated
	deep := clone(ir)
	deep.Metadata = jsontext.Value(strings.Repeat(`{"a":`, validate.MaxContainerDepth+1) + "1" + strings.Repeat("}", validate.MaxContainerDepth+1))
	out["metadata nested too deep"] = deep
	for _, n := range ir.Nodes {
		if n.Type == model.NodeSink {
			// The schema fixes equals at 1; a sink has no outgoing flow.
			v := clone(ir)
			v.Constraints = append(v.Constraints, model.Constraint{Type: model.ConstraintOutgoingProbabilitySum, NodeID: n.ID, Equals: 1})
			out["constraint not met"] = v
			break
		}
	}
	if len(ir.Flows) > 0 {
		// A decision node looping to itself, spliced into the first flow.
		v := clone(ir)
		const loop = "zz_conformance_loop"
		to := v.Flows[0].To
		v.Flows[0].To = loop
		v.Nodes = append(v.Nodes, model.Node{ID: loop, Type: model.NodeDecision})
		v.Flows = append(v.Flows,
			model.Flow{ID: loop + "_again", From: loop, To: loop, Routing: model.Routing{Type: model.RoutingProbability, Probability: 0.5}},
			model.Flow{ID: loop + "_on", From: loop, To: to, Routing: model.Routing{Type: model.RoutingProbability, Probability: 0.5}})
		out["zero-time cycle"] = v
	}
	if len(ir.Flows) > 0 {
		v := clone(ir)
		v.Flows[0].To = "zz_conformance_missing_node"
		out["unknown flow target"] = v
		v = clone(ir)
		v.Flows[0].Routing.Probability /= 2
		out["probabilities not summing to 1"] = v
	}
	if len(ir.Nodes) > 0 {
		v := clone(ir)
		v.Nodes = append(v.Nodes, v.Nodes[0])
		out["duplicate node ID"] = v
	}
	v := clone(ir)
	sinks := map[string]bool{}
	v.Nodes = slices.DeleteFunc(v.Nodes, func(n model.Node) bool { sinks[n.ID] = n.Type == model.NodeSink; return sinks[n.ID] })
	v.Flows = slices.DeleteFunc(v.Flows, func(f model.Flow) bool { return sinks[f.From] || sinks[f.To] })
	out["no sink"] = v
	for i, n := range ir.Nodes {
		if n.ServiceTime != nil {
			v := clone(ir)
			lo, mode, hi := 5.0, 1.0, 3.0
			v.Nodes[i].ServiceTime.Distribution = model.Distribution{Type: model.DistTriangular, Min: &lo, Mode: &mode, Max: &hi}
			out["bad triangular distribution"] = v
			break
		}
	}
	return out
}

func hasError(issues []model.ValidationIssue) bool {
	return slices.ContainsFunc(issues, func(i model.ValidationIssue) bool { return i.Severity == model.SeverityError })
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

// overwrite changes every value reachable from v that a caller could set:
// strings, numbers and bytes through pointers, fields and slice elements,
// and each string-keyed map gets a new key (map values in the IR are plain
// structs, so only a shared map can be seen, empty or not). A template that
// shares any of it with another caller's model shows the change there
// (templates/copies; T-044 review 2 N1). It covers the kinds the IR is made
// of (no interfaces, booleans or pointer cycles); unexported fields cannot
// be set and are skipped.
func overwrite(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			overwrite(v.Elem())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if f := v.Field(i); f.CanSet() {
				overwrite(f)
			}
		}
	case reflect.Slice:
		for i := range v.Len() {
			overwrite(v.Index(i))
		}
	case reflect.Map:
		if !v.IsNil() && v.Type().Key().Kind() == reflect.String {
			v.SetMapIndex(reflect.ValueOf("zz_conformance_added").Convert(v.Type().Key()), reflect.Zero(v.Type().Elem()))
		}
	case reflect.String:
		v.SetString(v.String() + " (changed by the caller)")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 1)
	case reflect.Uint8:
		v.SetUint(v.Uint() + 1) // the bytes of raw JSON (metadata)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(v.Float() + 1)
	}
}

func encoded(v any) string {
	data, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return "unencodable: " + err.Error()
	}
	return string(data)
}

// templateModelID is the model ID templates are asked for; otherModelID the
// second caller's, for templates/copies.
const (
	templateModelID = "mdl_01M4YDQK00TEMP1ATE00000000"
	otherModelID    = "mdl_01M4YDQK00TEMP1ATE00000001"
)

// templates: a pack that offers create-model templates (TemplateProvider)
// lists at least one; each, in every Locale, is a valid model of the pack's
// domain with the ID and name asked for; every locale gives the same model
// apart from its default names (REQ-022); the same call gives the same
// model; an unknown template or locale is an error.
func (c *checker) templates() {
	tp, ok := c.s.Pack.(domainpack.TemplateProvider)
	if !ok {
		return
	}
	var names []string
	if !c.call("templates/list", func() { names = tp.Templates() }) {
		return
	}
	if len(names) == 0 || slices.Contains(names, "") || len(slices.Compact(slices.Sorted(slices.Values(names)))) != len(names) {
		c.fail("templates/list", "Templates() must list at least one template, each once and named: %q", names)
	}
	const modelName = "Conformance template"
	get := func(check, name string, locale domainpack.Locale) (ir *model.ModelIR, err error, ok bool) {
		ok = c.call(check, func() { ir, err = tp.Template(name, templateModelID, modelName, locale) })
		return ir, err, ok
	}
	for _, name := range names {
		var first string
		for _, locale := range domainpack.Locales {
			ir, err, ok := get("templates/valid", name, locale)
			if !ok {
				continue
			}
			if err != nil || ir == nil {
				c.fail("templates/valid", "template %q in %s: no model (%v)", name, locale, err)
				continue
			}
			if ir.Domain != c.s.Pack.ID() || ir.ModelID != templateModelID || ir.Name != modelName {
				c.fail("templates/valid", "template %q in %s: domain %q, model ID or name not the ones asked for", name, locale, ir.Domain)
			}
			if issues := genericIssues(ir); len(issues) > 0 {
				c.fail("templates/valid", "template %q in %s is not a valid model: %v", name, locale, issues)
			} else {
				var domain []model.ValidationIssue
				cp := clone(ir)
				if c.call("templates/valid", func() { domain = c.s.Pack.Validate(&cp) }) && slices.ContainsFunc(domain, isError) {
					c.fail("templates/valid", "template %q in %s fails the pack's rules: %v", name, locale, domain)
				}
			}
			if again, err, ok := get("templates/deterministic", name, locale); ok && (err != nil || again == nil || encoded(again) != encoded(ir)) {
				c.fail("templates/deterministic", "template %q in %s differs between two calls", name, locale)
			}
			// Each caller gets its own model: building one for another model
			// ID and name, and changing it all the way down, leaves the first
			// as it was.
			before := encoded(ir)
			var other *model.ModelIR
			if c.call("templates/copies", func() { other, _ = tp.Template(name, otherModelID, "Other template", locale) }) && other != nil {
				overwrite(reflect.ValueOf(other))
				if encoded(ir) != before {
					c.fail("templates/copies", "template %q in %s: a second model shares data with the first one", name, locale)
				}
			}
			if s := withoutNames(ir); first == "" {
				first = s
			} else if s != first {
				c.fail("templates/locales", "template %q in %s differs from %s in more than its names", name, locale, domainpack.Locales[0])
			}
		}
		for _, bad := range []domainpack.Locale{"", "fr", "EN", "zh-hant", "zh-Hans", "zh", "zh-TW", "zh-Hant-TW", "en-US", "en-GB"} {
			if ir, err, ok := get("templates/unknown-locale", name, bad); ok && (err == nil || ir != nil) {
				c.fail("templates/unknown-locale", "template %q accepts the locale %q", name, bad)
			}
		}
	}
	unknown := []string{"zz_no_such_template"}
	for _, name := range names {
		// A name in another case, or padded, is another (unknown) name.
		for _, v := range []string{strings.ToUpper(name), strings.ToLower(name), " " + name, name + " "} {
			if !slices.Contains(names, v) && !slices.Contains(unknown, v) {
				unknown = append(unknown, v)
			}
		}
	}
	for _, name := range unknown {
		if ir, err, ok := get("templates/unknown-template", name, domainpack.Locales[0]); ok && (err == nil || ir != nil) {
			c.fail("templates/unknown-template", "the unknown template %q is answered with a model", name)
		}
	}
}

func isError(i model.ValidationIssue) bool { return i.Severity == model.SeverityError }

// withoutNames is ir encoded with its user-visible names removed: what must
// not differ between locales.
func withoutNames(ir *model.ModelIR) string {
	c := clone(ir)
	c.Name = ""
	for i := range c.EntityTypes {
		c.EntityTypes[i].Name = ""
	}
	for i := range c.Resources {
		c.Resources[i].Name = ""
	}
	for i := range c.Nodes {
		c.Nodes[i].Name = ""
	}
	return encoded(c)
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
		other.Domain = c.renamed()
		var foreign []model.ValidationIssue
		if c.call("validate/fail-closed", func() { foreign = c.s.Pack.Validate(&other) }) &&
			!slices.ContainsFunc(foreign, func(is model.ValidationIssue) bool {
				return is.Code == DomainMismatch && is.Path == "/domain" && is.Severity == model.SeverityError
			}) {
			c.fail("validate/fail-closed", "model %d with domain %q: no %s error at /domain (got %v)", i, other.Domain, DomainMismatch, foreign)
		}
		// The generic validator never reads the domain (REQ-003, TEST-002):
		// the same IR under an unrelated domain has the same issues, for the
		// valid model and for variants that break one rule each.
		variants := invalidVariants(&c.s.Models[i])
		variants["as given"] = clone(&c.s.Models[i])
		for _, name := range slices.Sorted(mapKeys(variants)) {
			v := variants[name]
			a := allIssues(&v)
			v.Domain = c.renamed()
			if b := allIssues(&v); encoded(a) != encoded(b) {
				c.fail("validate/generic-ignores-domain", "model %d %s: %s vs %s", i, name, encoded(a), encoded(b))
			}
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
		// A saved position on a point of the default layout makes the
		// pack place the other nodes around it (T-042 review B8).
		stripped := clone(&c.s.Models[i])
		stripped.Views.TwoD.Positions = nil
		var grid domainpack.ViewDefinitions
		c.call("views/default", func() { grid = c.s.Pack.DefaultViews(&stripped) })
		for _, variant := range []string{"as given", "without positions", "with one saved position", "with a saved position on the default grid"} {
			ir := clone(&c.s.Models[i])
			switch variant {
			case "without positions":
				ir.Views.TwoD.Positions = nil
			case "with one saved position":
				ir.Views.TwoD.Positions = map[string]model.Position{ir.Nodes[len(ir.Nodes)-1].ID: {X: -7777, Y: 3333}}
			case "with a saved position on the default grid":
				p, ok := grid.Positions[ir.Nodes[0].ID]
				if len(ir.Nodes) < 2 || !ok {
					continue
				}
				ir.Views.TwoD.Positions = map[string]model.Position{ir.Nodes[1].ID: p}
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
		if c.result == nil {
			c.result = plain
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
		if g1, g2 := engineOwned(res), engineOwned(plain); g1 != g2 {
			c.fail("metrics/generic-unchanged", "model %d: the pack changed the engine's generic metrics", i)
		}
		again, err := (&des.Engine{DomainMetrics: c.s.Pack.ComputeMetrics}).Run(ctx, in, nil)
		if err != nil || again.Reproducibility.ResultDigest != res.Reproducibility.ResultDigest {
			c.fail("metrics/deterministic", "model %d: a second run has another digest (%v)", i, err)
		}
		// The engine never reads the domain: the same IR under another
		// domain has the same generic numbers.
		other := clone(&c.s.Models[i])
		other.Domain = c.renamed()
		oin, err := c.freeze(other)
		if err == nil {
			var ores *simulation.Result
			if ores, err = (&des.Engine{}).Run(ctx, oin, nil); err == nil && domainFree(ores) != domainFree(plain) {
				c.fail("metrics/engine-ignores-domain", "model %d: renaming the domain changed the engine's numbers", i)
			}
		}
		if err != nil {
			c.fail("metrics/renamed-run", "model %d: the same IR under another domain does not run: %v", i, err)
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

// engineOwned is what the engine alone decides in a result: everything but
// the domain metrics and the digest over them. Seed, hashes and all generic
// metrics stay in, so a pack cannot rewrite them (T-042 review B6).
func engineOwned(r *simulation.Result) string {
	cp := *r
	cp.Metrics.DORA = nil
	cp.Reproducibility.ResultDigest = ""
	return encoded(cp)
}

// domainFree is engineOwned without the model hashes, which differ when
// the domain is renamed.
func domainFree(r *simulation.Result) string {
	cp := *r
	cp.Metrics.DORA = nil
	cp.Reproducibility.ResultDigest, cp.Reproducibility.ModelHash, cp.Reproducibility.EffectiveModelHash = "", "", ""
	return encoded(cp)
}

// inference: with no events a pack answers with an error (needs input or
// unavailable, never an invented model: AGENTS.md §2); with events it
// either declines with ErrUnavailable or proposes a model of its own domain
// that the generic validator accepts; it never answers (nil, nil) and stops
// when its context is cancelled.
func (c *checker) inference(ctx context.Context) {
	empty := domainpack.CanonicalEventDataset{DatasetVersionID: events.DatasetVersionID}
	var res *domainpack.InferenceResult
	var err error
	if c.call("inference/needs-input", func() { res, err = c.s.Pack.InferFromEvents(ctx, empty) }) && (err == nil || res != nil) {
		c.fail("inference/needs-input", "answered a dataset with no events with a result (error %v)", err)
	}
	var again *domainpack.InferenceResult
	var againErr error
	if !c.call("inference/answer", func() {
		res, err = c.s.Pack.InferFromEvents(ctx, events)
		again, againErr = c.s.Pack.InferFromEvents(ctx, events)
	}) {
		return
	}
	if encoded(res) != encoded(again) || fmt.Sprint(err) != fmt.Sprint(againErr) {
		c.fail("inference/deterministic", "two calls with the same dataset disagree: inference is a function of the dataset, and IDs come from the server (D-016)")
	}
	switch {
	case err != nil && res != nil:
		c.fail("inference/answer", "an error (%v) with a result", err)
	case err != nil:
		// ErrUnavailable or a typed refusal is acceptable.
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
	if c.call("inference/cancel", func() { res, err = c.s.Pack.InferFromEvents(cancelled, events) }) && (err == nil || res != nil) {
		c.fail("inference/cancel", "answered a cancelled context with a result (error %v)", err)
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

// staticData is the prompt context and catalogs, encoded.
func (c *checker) staticData() string {
	return encoded([]any{c.s.Pack.PromptContext(), c.s.Pack.NodeCatalog(), c.s.Pack.ResourceCatalog()})
}

// static: the prompt context and catalogs are static pack data. After the
// pack has seen a model and events carrying a sentinel, they are what they
// were before it saw any IR, and contain no customer text (SECURITY §7;
// T-042 review B4). Its other answers are what they were before it saw the
// sentinel model (stateless, T-042 review N7).
func (c *checker) static(ctx context.Context) {
	// What the pack answers for every model before and after it sees the
	// sentinel model: a pack that remembers an earlier call (another
	// tenant's model) answers differently (T-042 review N7).
	answers := func() string {
		var out []any
		for i := range c.s.Models {
			m := clone(&c.s.Models[i])
			out = append(out, c.s.Pack.Validate(&m), c.s.Pack.DefaultViews(&m))
		}
		if c.result != nil {
			out = append(out, c.s.Pack.ComputeMetrics(clonedResult(c.result)))
		}
		return encoded(out)
	}
	var before string
	if !c.call("stateless", func() { before = answers() }) {
		return
	}
	ir := sentinelModel(&c.s.Models[0])
	seen := events
	seen.Events = append(slices.Clone(events.Events), domainpack.CanonicalEvent{CaseID: sentinel, EventType: sentinel, Timestamp: "2026-10-15T11:00:00Z"})
	// ComputeMetrics is not exposed here: an engine result carries no
	// customer text, and the metrics step already gave it one, which the
	// comparison with c.initial covers.
	c.call("prompt/static", func() {
		c.s.Pack.Validate(&ir)
		c.s.Pack.DefaultViews(&ir)
		_, _ = c.s.Pack.InferFromEvents(ctx, seen)
	})
	var final string
	if !c.call("prompt/static", func() { final = c.staticData() }) {
		return
	}
	if strings.Contains(final, sentinel) || strings.Contains(final, sentinelID) {
		c.fail("prompt/static", "the prompt context or a catalog carries text from a model or dataset the pack was given")
	} else if c.initial != "" && final != c.initial {
		c.fail("prompt/static", "the prompt context or a catalog changed after the pack was given models and events")
	}
	var after string
	if !c.call("stateless", func() { after = answers() }) {
		return
	}
	if strings.Contains(after, sentinel) || strings.Contains(after, sentinelID) {
		c.fail("stateless", "Validate, DefaultViews or ComputeMetrics returns text from a model or dataset of an earlier call")
	} else if after != before {
		c.fail("stateless", "Validate, DefaultViews or ComputeMetrics answers differently after the pack saw another model")
	}
}

// concurrent: packs must be safe for concurrent use (the API and worker
// share one registry): every method, ComputeMetrics and InferFromEvents
// included, gives each of 8 goroutines what a sequential call gives.
// Disagreement is reported here; data races are found by running the suite
// under -race (make backend-race).
func (c *checker) concurrent(ctx context.Context) {
	ir := clone(&c.s.Models[0])
	methods := []string{"NodeCatalog", "ResourceCatalog", "Validate", "DefaultViews", "PromptContext", "ComputeMetrics", "InferFromEvents", "Template"}
	// A template provider's first template, in the last locale (T-044 review M1).
	template := func() string { return "" }
	if tp, ok := c.s.Pack.(domainpack.TemplateProvider); ok {
		template = func() string {
			names := tp.Templates()
			if len(names) == 0 {
				return ""
			}
			ir, err := tp.Template(names[0], templateModelID, "Conformance template", domainpack.Locales[len(domainpack.Locales)-1])
			return encoded(ir) + fmt.Sprint(err)
		}
	}
	calls := func(ir *model.ModelIR) []string {
		var metrics map[string]simulation.MetricSummary
		if c.result != nil {
			metrics = c.s.Pack.ComputeMetrics(clonedResult(c.result))
		}
		inferred, err := c.s.Pack.InferFromEvents(ctx, events)
		return []string{encoded(c.s.Pack.NodeCatalog()), encoded(c.s.Pack.ResourceCatalog()), encoded(c.s.Pack.Validate(ir)), encoded(c.s.Pack.DefaultViews(ir)),
			encoded(c.s.Pack.PromptContext()), encoded(metrics), encoded(inferred) + fmt.Sprint(err), template()}
	}
	var want []string
	if !c.call("concurrent", func() { want = calls(&ir) }) {
		return
	}
	const n = 8
	got := make([][]string, n)
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
		for k := range s {
			if s[k] != want[k] {
				c.fail("concurrent", "goroutine %d: %s disagrees with a sequential call", g, methods[k])
			}
		}
	}
}
