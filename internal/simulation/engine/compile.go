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
	"github.com/kcl1104/geminus/internal/model/calendar"
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
	finite       bool // some process has a finite buffer (D-049)
	breakdowns   bool // some resource breaks down (D-051)
	// calendars (D-052, T-049): every calendar some resource refers to, in
	// ID order, and whether there is one.
	calendars  []ccalendar
	calendared bool
}

// ccalendar is a calendar some resource refers to: its resources, in ID
// order (a CAPACITY_CHANGE updates them in that order, D-052).
type ccalendar struct {
	id        string
	resources []int
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
	capacity    int  // process: its buffer's capacity from its explicit queue (D-049); 0 unlimited
	canBlock    bool // source, process or delay whose route can reach a finite buffer
	// ttrMax is a process's TTRmax (D-050 B2): the longest repair of the
	// resources of its bundle that break down, 0 without one, -1 when one
	// has no bound (exponential, lognormal, Weibull: no deadline).
	ttrMax int64
	// release is a source's release schedule (D-053), instead of arrival.
	release *releaser
	// need is the bundle of a process with a resource on a calendar, for
	// B2's next_fit, and xEnd the end of the last exception of their
	// calendars (D-050's X_end); nil and 0 without one.
	need []calendar.Need
	xEnd int64
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
	// Breakdowns (D-051): each unit's time to failure and time to repair,
	// nil when the resource does not break down; ttrMax is the repair
	// sampler's bound (-1 for none).
	ttf, ttr *sampler
	ttrMax   int64
	// pattern is the resource's S(t) on its calendar (D-052), nil without
	// one (S = capacity); cal its calendar's index in Program.calendars.
	pattern *calendar.Pattern
	cal     int
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
	res, err := validateIR(data)
	if err != nil {
		return nil, fmt.Errorf("engine: validating the model: %w", err)
	}
	if !res.Valid() {
		return nil, &InputError{Issues: prefix("/model", res.Issues)}
	}
	ir := res.IR // a fresh copy: nothing the caller holds aliases the program
	if issues := unsupported(ir); len(issues) > 0 {
		return nil, &InputError{Issues: prefix("/model", issues)}
	}

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

// validateIR is the validator Compile runs: always validate.IR, except in a
// test of this package that stands in a validator checking every primitive
// to show that the kernel list below refuses on its own (T-046 review M2).
var validateIR = validate.IR

// kernelExecutes holds the line-flow primitives (ADR-026, D-058;
// validate.Primitives) this kernel executes: finite buffers since
// des-go-0.2.0 (T-047, D-049); breakdowns, lognormal and Weibull since
// des-go-0.3.0 (T-048, D-051, D-056); calendars and release schedules
// since des-go-0.4.0 (T-049, D-052, D-053). Each of T-050–T-051 adds its
// own together with the kernel change and a new engine_version. It is kept
// apart from the validator's list so that a primitive whose rules are
// checked but whose kernel is not there yet is still refused. Only read,
// never iterated.
var kernelExecutes = map[validate.Primitive]bool{validate.FiniteBuffer: true,
	validate.Breakdowns: true, validate.Lognormal: true, validate.Weibull: true,
	validate.Calendars: true, validate.ReleaseSchedule: true}

// unsupported refuses every use of a primitive the kernel does not execute,
// so no model is ever run as if such a member were absent (D-063).
func unsupported(ir *model.ModelIR) []model.ValidationIssue {
	var out []model.ValidationIssue
	for _, u := range validate.Uses(ir) {
		if !kernelExecutes[u.Primitive] {
			out = append(out, validate.NotSupported(u))
		}
	}
	return out
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
	for _, d := range distributions(ir) {
		if d.Distribution.Type == model.DistEmpirical && !bound[d.Distribution.SamplesRef] {
			issues = append(issues, invalid(d.path+"/samples_ref", d.Distribution.SamplesRef+" has no binding in the manifest"))
		}
	}
	return issues, nil
}

// located is a distribution of the IR and its pointer under /model.
type located struct {
	*model.Distribution
	path string
}

// distributions lists every distribution of ir, the nodes' then the
// resources' (breakdowns, T-048), in IR order.
func distributions(ir *model.ModelIR) []located {
	var out []located
	for i := range ir.Nodes {
		for _, d := range ir.Nodes[i].Distributions() {
			out = append(out, located{d.Distribution, validate.Pointer("model", "nodes", strconv.Itoa(i), d.Member, "distribution")})
		}
	}
	for i := range ir.Resources {
		for _, d := range ir.Resources[i].Distributions() {
			out = append(out, located{d.Distribution, validate.Pointer("model", "resources", strconv.Itoa(i)) + "/" + d.Path})
		}
	}
	return out
}

// resolveDatasets quantizes every empirical distribution's samples and
// reports which datasets have a 0-tick sample (D-033).
func resolveDatasets(ir *model.ModelIR, datasets map[string]simulation.ResolvedDataset) (map[string][]int64, map[string]bool, []model.ValidationIssue) {
	out, zero, seen := map[string][]int64{}, map[string]bool{}, map[string]bool{}
	var issues []model.ValidationIssue
	for _, d := range distributions(ir) {
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
	// The calendars resources refer to, in ID order (D-048: their first
	// changes are scheduled in that order at t=0).
	calendars := slices.Clone(ir.Calendars)
	slices.SortFunc(calendars, byID(func(c model.Calendar) string { return c.ID }))
	calIndex := map[string]int{}
	for _, c := range calendars {
		for _, r := range resources {
			if r.CalendarID == c.ID {
				calIndex[c.ID] = len(p.calendars)
				p.calendars = append(p.calendars, ccalendar{id: c.ID})
				break
			}
		}
	}
	resIndex := make(map[string]int, len(resources))
	for i, r := range resources {
		resIndex[r.ID] = i
		c := cresource{id: r.ID, capacity: r.Capacity, cal: -1}
		if r.CalendarID != "" {
			ci, ok := calIndex[r.CalendarID]
			if !ok {
				return &InputError{Issues: []model.ValidationIssue{model.NewIssue(validate.UnknownReference, "/model/resources", r.ID+": no calendar "+r.CalendarID, model.SeverityError)}}
			}
			cal := calendars[slices.IndexFunc(calendars, func(x model.Calendar) bool { return x.ID == r.CalendarID })]
			pat, err := calendar.FromModel(cal, model.TimeUnit(ir.TimeUnit), r.Capacity, r.ShiftCapacity)
			if err != nil {
				return &InputError{Issues: []model.ValidationIssue{model.NewIssue(validate.CalendarInvalid, "/model/resources", r.ID+": "+err.Error(), model.SeverityError)}}
			}
			c.pattern, c.cal = pat, ci
			p.calendars[ci].resources = append(p.calendars[ci].resources, i)
			p.calendared = true
		}
		if b := r.Breakdowns; b != nil {
			var err error
			ttf, ttr := b.TimeToFailure.Distribution, b.TimeToRepair.Distribution
			if c.ttf, err = newSampler(ttf, model.TimeUnit(ir.TimeUnit), empirical[ttf.SamplesRef]); err == nil {
				c.ttr, err = newSampler(ttr, model.TimeUnit(ir.TimeUnit), empirical[ttr.SamplesRef])
			}
			if err != nil {
				return &InputError{Issues: []model.ValidationIssue{model.NewIssue(validate.DistributionInvalid, "/model/resources", r.ID+": "+err.Error(), model.SeverityError)}}
			}
			c.ttrMax = c.ttr.bound()
			p.breakdowns = true
		}
		p.resources = append(p.resources, c)
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
			if s := n.Arrival.Schedule; s != nil {
				// D-053 (T-049): a release schedule instead of an
				// interarrival distribution (the schema allows one of them).
				if c.release, err = newReleaser(*s, model.TimeUnit(ir.TimeUnit)); err != nil {
					return &InputError{Issues: []model.ValidationIssue{model.NewIssue(validate.ReleaseScheduleInvalid, "/model/nodes", n.ID+": "+err.Error(), model.SeverityError)}}
				}
			} else {
				c.arrival = sample(&n.Arrival.Distribution)
			}
			c.maxEntities = n.Arrival.MaxEntities
			p.sources = append(p.sources, i)
		case model.NodeProcess:
			c.service = sample(&n.ServiceTime.Distribution)
			c.buffer = i
			for _, r := range n.Requires {
				c.requires = append(c.requires, requirement{resource: resIndex[r.ResourceID], units: r.Units})
			}
			slices.SortFunc(c.requires, func(a, b requirement) int { return cmp.Compare(a.resource, b.resource) })
			// D-050 B2: the bundle's TTRmax is its resources' longest
			// repair, none (-1) when one has no bound.
			for _, q := range c.requires {
				if r := &p.resources[q.resource]; r.ttr != nil {
					if r.ttrMax < 0 || c.ttrMax < 0 {
						c.ttrMax = -1
					} else {
						c.ttrMax = max(c.ttrMax, r.ttrMax)
					}
				}
			}
			// D-050 B2 with calendars (T-049): next_fit's bundle and X_end.
			if slices.ContainsFunc(c.requires, func(q requirement) bool { return p.resources[q.resource].pattern != nil }) {
				for _, q := range c.requires {
					pat := p.resources[q.resource].pattern
					if pat == nil {
						pat = calendar.Always(p.resources[q.resource].capacity)
					}
					c.need = append(c.need, calendar.Need{Pattern: pat, Units: q.units})
					c.xEnd = max(c.xEnd, pat.ExceptionsEnd())
				}
			}
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
			// So does its capacity (D-049): it bounds the process's buffer,
			// whether an entity comes through the queue or straight in.
			if c := nodes[i].Capacity; c > 0 {
				p.nodes[p.nodes[i].buffer].capacity = c
				p.finite = true
			}
		}
	}
	if p.finite {
		for i := range p.nodes {
			switch p.nodes[i].typ {
			case model.NodeSource, model.NodeProcess, model.NodeDelay:
				p.nodes[i].canBlock = p.reachesFinite(i)
			}
		}
	}
	return nil
}

// reachesFinite reports whether a route from node i, through decisions,
// failure and rework nodes, can end at a finite buffer: an entity leaving i
// can be blocked there (D-049).
func (p *Program) reachesFinite(i int) bool {
	seen := map[int]bool{}
	stack := []int{}
	for _, rt := range p.nodes[i].routes {
		stack = append(stack, rt.to)
	}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		switch c := &p.nodes[n]; c.typ {
		case model.NodeQueue, model.NodeProcess:
			if c.buffer >= 0 && p.nodes[c.buffer].capacity > 0 {
				return true
			}
		case model.NodeDecision, model.NodeFailure, model.NodeRework:
			for _, rt := range c.routes {
				stack = append(stack, rt.to)
			}
		}
	}
	return false
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

// HasFiniteBuffers reports whether some process has a finite buffer
// (D-049): only then does a run record blocking and its Result members.
func (p *Program) HasFiniteBuffers() bool { return p.finite }

// HasBreakdowns reports whether some resource breaks down (D-051): only
// then do failure clocks run and a Result have downtime members.
func (p *Program) HasBreakdowns() bool { return p.breakdowns }

// ResourceHasBreakdowns reports whether resource i breaks down.
func (p *Program) ResourceHasBreakdowns(i int) bool { return p.resources[i].ttf != nil }

// HasCalendars reports whether some resource is on a calendar (D-052):
// only then do capacities change and a Result have scheduled members.
func (p *Program) HasCalendars() bool { return p.calendared }

// ResourceHasCalendar reports whether resource i is on a calendar.
func (p *Program) ResourceHasCalendar(i int) bool { return p.resources[i].pattern != nil }

// InitialCapacity is resource i's units on shift at t=0, S(0) (the
// capacity without a calendar): the Program's, never an observation
// (D-047).
func (p *Program) InitialCapacity(i int) int {
	if pat := p.resources[i].pattern; pat != nil {
		return pat.At(0)
	}
	return p.resources[i].capacity
}

// BufferCapacity is process i's buffer capacity, 0 when unlimited.
func (p *Program) BufferCapacity(i int) int { return p.nodes[i].capacity }

// CanBlock reports whether an entity leaving node i (a source, process or
// delay) can be blocked by a full buffer (D-049).
func (p *Program) CanBlock(i int) bool { return p.nodes[i].canBlock }

// Requirement is one unit type of a bundle: a resource index and units.
type Requirement struct{ Resource, Units int }

// Requires is process i's bundle in resource order (a copy).
func (p *Program) Requires(i int) []Requirement {
	out := make([]Requirement, len(p.nodes[i].requires))
	for k, q := range p.nodes[i].requires {
		out[k] = Requirement{q.resource, q.units}
	}
	return out
}

// ResourceCount, ResourceID and ResourceCapacity describe resources in ID order.
func (p *Program) ResourceCount() int         { return len(p.resources) }
func (p *Program) ResourceID(i int) string    { return p.resources[i].id }
func (p *Program) ResourceCapacity(i int) int { return p.resources[i].capacity }

// Seed is the run's base seed; TraceLevel and Limits are the frozen config.
func (p *Program) Seed() uint64                       { return p.seed }
func (p *Program) TraceLevel() simulation.TraceLevel  { return p.traceLevel }
func (p *Program) Limits() simulation.ExecutionLimits { return p.limits }
