package engine

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
)

// ErrInvalidInput: the run input is not one this engine will execute. An
// *InputError carries the validation issues.
var ErrInvalidInput = errors.New("engine: invalid run input")

// InputError lists why Compile refused a run input; paths point into the IR
// (/model/...), the config (/config/...), the limits (/limits/...), the
// manifest (/manifest/<i>/...) or the datasets (/datasets/<logical_ref>).
// Issues are in (path, code) order (D-023).
type InputError struct{ Issues []model.ValidationIssue }

func (e *InputError) Error() string {
	return fmt.Sprintf("%v (%d issue(s))", ErrInvalidInput, len(e.Issues))
}
func (e *InputError) Unwrap() error { return ErrInvalidInput }

// Program is a compiled run input: index-based, immutable after Compile and
// safe to share between concurrently running replications. Nodes and
// resources are ordered by the bytes of their IDs, so every index, and every
// iteration over them, is the same on every run.
type Program struct {
	unit         model.TimeUnit
	nodes        []cnode
	index        map[string]int // read-only after Compile
	resources    []cresource
	sources      []int // node indices of the sources, in ID order
	horizon      int64 // ticks; events at tick <= horizon are processed
	warmup       int64
	replications int
	seed         uint64
	traceLevel   simulation.TraceLevel
	limits       simulation.ExecutionLimits
}

type cnode struct {
	id          string
	typ         model.NodeType
	arrival     *sampler // source
	maxEntities int      // source; 0 = until the horizon
	service     *sampler // process
	delay       *sampler // delay
	requires    []requirement
	routes      []route // decision: p > 0 flows by flow ID with cumulative probabilities; others: the single p = 1 flow
	buffer      int     // queue: the process whose request buffer it names; process: itself; otherwise -1
	label       string  // process: the queue-metric key (its explicit queue's ID, else its own)
	outcome     model.SinkOutcome
}

type requirement struct {
	resource int
	units    int
}

type route struct {
	flow       string
	to         int
	cumulative float64
}

type cresource struct {
	id       string
	capacity int
}

// Compile checks a run input and builds its Program. The IR is validated
// again in full (schema, semantics and, with the actual dataset samples, the
// D-033 binding rules): the engine never relies on a caller having done it.
// Compile does no I/O.
func Compile(in simulation.RunInput) (*Program, error) {
	data, err := model.Encode(&in.Model)
	if err != nil {
		return nil, fmt.Errorf("engine: encoding the model: %w", err)
	}
	res, err := validate.IR(data)
	if err != nil {
		return nil, fmt.Errorf("engine: validating the model: %w", err)
	}
	if !res.Valid() {
		return nil, &InputError{Issues: prefix("/model", res.Issues)}
	}
	ir := res.IR // a fresh copy: nothing the caller holds aliases the program

	if issues, err := checkManifest(ir, in.Manifest, in.Datasets); err != nil || len(issues) > 0 {
		if err != nil {
			return nil, err
		}
		return nil, &InputError{Issues: validate.Sort(issues)}
	}
	empirical, zeroRefs, issues := resolveDatasets(ir, in.Datasets)
	if len(issues) > 0 {
		return nil, &InputError{Issues: validate.Sort(issues)}
	}
	if issues := validate.Bindings(ir, zeroRefs); hasError(issues) {
		return nil, &InputError{Issues: prefix("/model", issues)}
	}
	p := &Program{unit: model.TimeUnit(ir.TimeUnit), traceLevel: in.Config.TraceLevel, limits: in.Limits, replications: in.Config.Replications}
	if issues := p.setConfig(in.Config, in.Limits); len(issues) > 0 {
		return nil, &InputError{Issues: issues}
	}
	if err := p.build(ir, empirical); err != nil {
		return nil, err
	}
	return p, nil
}

func prefix(root string, issues []model.ValidationIssue) []model.ValidationIssue {
	out := make([]model.ValidationIssue, len(issues))
	for i, is := range issues {
		out[i] = model.NewIssue(is.Code, root+is.Path, is.Message, is.Severity)
	}
	return out
}

func hasError(issues []model.ValidationIssue) bool {
	return slices.ContainsFunc(issues, func(i model.ValidationIssue) bool { return i.Severity == model.SeverityError })
}

func invalid(path, message string) model.ValidationIssue {
	return model.NewIssue(validate.DatasetBindingInvalid, path, message, model.SeverityError)
}

// checkManifest checks the frozen manifest instead of trusting the caller:
// every binding schema-valid, in logical_ref byte order without repeats
// (ADR-012, D-034), every empirical distribution bound, and Datasets holding
// exactly the manifest's bindings. A binding the model does not use is
// allowed, as in datasets.Resolve.
func checkManifest(ir *model.ModelIR, manifest []model.DatasetBinding, datasets map[string]simulation.ResolvedDataset) ([]model.ValidationIssue, error) {
	var issues []model.ValidationIssue
	bound := make(map[string]bool, len(manifest))
	for i, b := range manifest {
		at := "/manifest/" + strconv.Itoa(i)
		// Invalid UTF-8 becomes U+FFFD, which the ASCII patterns reject.
		data, err := json.Marshal(b, jsontext.AllowInvalidUTF8(true))
		if err != nil {
			return nil, fmt.Errorf("engine: encoding manifest binding %d: %w", i, err)
		}
		found, err := validate.Document(data, "common.schema.json#/$defs/DatasetBinding", validate.DatasetBindingInvalid)
		if err != nil {
			return nil, fmt.Errorf("engine: validating manifest binding %d: %w", i, err)
		}
		issues = append(issues, prefix(at, found)...)
		if i > 0 && b.LogicalRef <= manifest[i-1].LogicalRef {
			issues = append(issues, invalid(at+"/logical_ref", "the manifest must be in logical_ref order and bind each reference once"))
		}
		bound[b.LogicalRef] = true
		switch ds, ok := datasets[b.LogicalRef]; {
		case !ok:
			issues = append(issues, invalid("/datasets/"+b.LogicalRef, "no resolved dataset for "+b.LogicalRef))
		case ds.Binding != b:
			issues = append(issues, invalid("/datasets/"+b.LogicalRef, "the resolved dataset's binding differs from the manifest"))
		}
	}
	for ref := range datasets {
		if !bound[ref] {
			issues = append(issues, invalid("/datasets/"+ref, "a resolved dataset that is not in the manifest"))
		}
	}
	for i := range ir.Nodes {
		for _, d := range ir.Nodes[i].Distributions() {
			if d.Distribution.Type == model.DistEmpirical && !bound[d.Distribution.SamplesRef] {
				issues = append(issues, invalid(validate.Pointer("model", "nodes", strconv.Itoa(i), d.Member, "distribution", "samples_ref"),
					d.Distribution.SamplesRef+" has no binding in the manifest"))
			}
		}
	}
	return issues, nil
}

// resolveDatasets quantizes every empirical distribution's samples and
// reports which datasets have a 0-tick sample (D-033).
func resolveDatasets(ir *model.ModelIR, datasets map[string]simulation.ResolvedDataset) (map[string][]int64, map[string]bool, []model.ValidationIssue) {
	out, zero, seen := map[string][]int64{}, map[string]bool{}, map[string]bool{}
	var issues []model.ValidationIssue
	for i := range ir.Nodes {
		for _, d := range ir.Nodes[i].Distributions() {
			ref := d.Distribution.SamplesRef
			if d.Distribution.Type != model.DistEmpirical || seen[ref] {
				continue
			}
			seen[ref] = true
			path := "/datasets/" + ref
			ds, ok := datasets[ref]
			switch {
			case !ok:
				issues = append(issues, invalid(path, "no resolved dataset for "+ref))
				continue
			case ds.Binding.LogicalRef != ref:
				issues = append(issues, invalid(path, "the resolved dataset is bound to another logical_ref"))
				continue
			case len(ds.Samples) == 0 || len(ds.Samples) != ds.Binding.SampleCount:
				issues = append(issues, invalid(path, fmt.Sprintf("%d samples, the binding says %d", len(ds.Samples), ds.Binding.SampleCount)))
				continue
			}
			ticks := make([]int64, len(ds.Samples))
			ok = true
			for k, s := range ds.Samples {
				t, err := model.Ticks(s, ds.Binding.Unit)
				if err != nil {
					issues = append(issues, invalid(path, fmt.Sprintf("sample %d: %v", k, err)))
					ok = false
					break
				}
				ticks[k] = t
				zero[ref] = zero[ref] || t == 0
			}
			if ok {
				out[ref] = ticks
			}
		}
	}
	return out, zero, issues
}

// setConfig checks the run config and limits against their schemas and the
// window rule 0 <= warmup < horizon, and quantizes them with model.Ticks
// (the same function config_hash uses).
func (p *Program) setConfig(cfg simulation.RunConfig, limits simulation.ExecutionLimits) []model.ValidationIssue {
	var issues []model.ValidationIssue
	// NaN and infinities cannot be encoded for the schema check; name the member.
	for _, d := range []struct {
		path  string
		value float64
	}{{"/config/horizon/value", cfg.Horizon.Value}, {"/config/warmup/value", cfg.Warmup.Value}} {
		if math.IsNaN(d.value) || math.IsInf(d.value, 0) {
			issues = append(issues, model.NewIssue(validate.IRSchemaInvalid, d.path, "not a finite number", model.SeverityError))
		}
	}
	if len(issues) > 0 {
		return issues
	}
	for _, c := range []struct {
		root, ref string
		v         any
	}{
		{"/config", "simulation.schema.json#/$defs/RunConfig", cfg},
		{"/limits", "simulation.schema.json#/$defs/ExecutionLimits", limits},
	} {
		data, err := json.Marshal(c.v)
		if err != nil {
			return []model.ValidationIssue{model.NewIssue(validate.IRSchemaInvalid, c.root, "not encodable", model.SeverityError)}
		}
		found, err := validate.Document(data, c.ref, validate.IRSchemaInvalid)
		if err != nil {
			return []model.ValidationIssue{model.NewIssue(validate.IRSchemaInvalid, c.root, "cannot be validated", model.SeverityError)}
		}
		issues = append(issues, prefix(c.root, found)...)
	}
	if len(issues) > 0 {
		return issues
	}
	// The schema bounds the seed; the 365 day cap is in ticks (D-005).
	for _, d := range []struct {
		path string
		v    simulation.Duration
	}{{"/config/horizon", cfg.Horizon}, {"/config/warmup", cfg.Warmup}} {
		if _, err := model.Ticks(d.v.Value, d.v.Unit); err != nil {
			issues = append(issues, model.NewIssue(validate.LimitExceeded, d.path, err.Error(), model.SeverityError))
		}
	}
	if len(issues) > 0 {
		return issues
	}
	norm, err := hashing.NormalizeConfig(cfg)
	if err != nil {
		return []model.ValidationIssue{model.NewIssue(validate.LimitExceeded, "/config", "durations or seed out of range", model.SeverityError)}
	}
	if !(norm.WarmupTicks < norm.HorizonTicks) {
		return []model.ValidationIssue{model.NewIssue(validate.LimitExceeded, "/config/warmup", "warmup must be shorter than the horizon", model.SeverityError)}
	}
	p.horizon, p.warmup, p.seed = norm.HorizonTicks, norm.WarmupTicks, uint64(norm.Seed)
	return nil
}

func byID[T any](id func(T) string) func(a, b T) int {
	return func(a, b T) int { return cmp.Compare(id(a), id(b)) } // Go strings compare by bytes
}

// build turns a validated IR into index-based nodes, routes and samplers.
func (p *Program) build(ir *model.ModelIR, empirical map[string][]int64) error {
	nodes := slices.Clone(ir.Nodes)
	slices.SortFunc(nodes, byID(func(n model.Node) string { return n.ID }))
	resources := slices.Clone(ir.Resources)
	slices.SortFunc(resources, byID(func(r model.Resource) string { return r.ID }))

	p.index = make(map[string]int, len(nodes))
	for i, n := range nodes {
		p.index[n.ID] = i
	}
	resIndex := make(map[string]int, len(resources))
	for i, r := range resources {
		resIndex[r.ID] = i
		p.resources = append(p.resources, cresource{id: r.ID, capacity: r.Capacity})
	}
	flows := slices.Clone(ir.Flows)
	slices.SortFunc(flows, byID(func(f model.Flow) string { return f.ID }))

	p.nodes = make([]cnode, len(nodes))
	for i, n := range nodes {
		c := cnode{id: n.ID, typ: n.Type, buffer: -1, outcome: n.Outcome}
		var err error
		sample := func(d *model.Distribution) *sampler {
			if err != nil {
				return nil
			}
			var s *sampler
			s, err = newSampler(*d, model.TimeUnit(ir.TimeUnit), empirical[d.SamplesRef])
			return s
		}
		switch n.Type {
		case model.NodeSource:
			c.arrival = sample(&n.Arrival.Distribution)
			c.maxEntities = n.Arrival.MaxEntities
			p.sources = append(p.sources, i)
		case model.NodeProcess:
			c.service = sample(&n.ServiceTime.Distribution)
			c.buffer = i
			for _, r := range n.Requires {
				c.requires = append(c.requires, requirement{resource: resIndex[r.ResourceID], units: r.Units})
			}
			slices.SortFunc(c.requires, func(a, b requirement) int { return cmp.Compare(a.resource, b.resource) })
		case model.NodeDelay:
			c.delay = sample(&n.DelayTime.Distribution)
		}
		if err != nil {
			return &InputError{Issues: []model.ValidationIssue{model.NewIssue(validate.DistributionInvalid, "/model/nodes", n.ID+": "+err.Error(), model.SeverityError)}}
		}
		cumulative := 0.0
		for _, f := range flows {
			if f.From != n.ID || f.Routing.Probability <= 0 {
				continue // a 0% flow is never taken, so it is not a route
			}
			cumulative += f.Routing.Probability
			c.routes = append(c.routes, route{flow: f.ID, to: p.index[f.To], cumulative: cumulative})
		}
		p.nodes[i] = c
	}
	// An explicit queue names the request buffer of the process it feeds
	// (SIMULATION_SPEC §3); the validator guarantees that single process.
	for i := range p.nodes {
		if p.nodes[i].typ == model.NodeProcess {
			p.nodes[i].label = p.nodes[i].id
		}
	}
	for i := range p.nodes {
		if p.nodes[i].typ == model.NodeQueue && len(p.nodes[i].routes) == 1 {
			p.nodes[i].buffer = p.nodes[i].routes[0].to
			// Queue metrics of a process belong to its explicit queue (SIMULATION_SPEC §3).
			p.nodes[p.nodes[i].buffer].label = p.nodes[i].id
		}
	}
	return nil
}

// HorizonTicks and WarmupTicks are the measurement window in ticks.
func (p *Program) HorizonTicks() int64 { return p.horizon }
func (p *Program) WarmupTicks() int64  { return p.warmup }

// Replications is the number of replications to run.
func (p *Program) Replications() int { return p.replications }

// NodeIDs lists the nodes in program (byte) order.
func (p *Program) NodeIDs() []string {
	out := make([]string, len(p.nodes))
	for i, n := range p.nodes {
		out[i] = n.id
	}
	return out
}

// Read-only views for result aggregation (internal/results).

// NodeCount is the number of nodes; indices are 0..NodeCount-1 in ID order.
func (p *Program) NodeCount() int { return len(p.nodes) }

// NodeID and NodeType describe node i.
func (p *Program) NodeID(i int) string           { return p.nodes[i].id }
func (p *Program) NodeType(i int) model.NodeType { return p.nodes[i].typ }

// QueueLabel is the queue-metric key of process i: the ID of the explicit
// queue that names its buffer, or the process's own ID; "" for other nodes.
func (p *Program) QueueLabel(i int) string { return p.nodes[i].label }

// ResourceCount, ResourceID and ResourceCapacity describe resources in ID order.
func (p *Program) ResourceCount() int         { return len(p.resources) }
func (p *Program) ResourceID(i int) string    { return p.resources[i].id }
func (p *Program) ResourceCapacity(i int) int { return p.resources[i].capacity }

// Seed is the run's base seed; TraceLevel and Limits are the frozen config.
func (p *Program) Seed() uint64                       { return p.seed }
func (p *Program) TraceLevel() simulation.TraceLevel  { return p.traceLevel }
func (p *Program) Limits() simulation.ExecutionLimits { return p.limits }
