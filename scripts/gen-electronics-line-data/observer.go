package main

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation/engine"
)

// record is one row of the log: an operation (a board's step at a
// station) or a downtime (a machine's breakdown), with times in ticks
// (1 ms) since the anchor.
type record struct {
	serial, recordType, station, equipment string
	start, end                             int64 // end -1 while it lasts
	result, shift, product, operator       string
	seq                                    int // the order the engine began it in
	node                                   int // the process of an operation; -1 for a downtime
}

// outcome is what a process step's result means (ELECTRONICS_LINE §6):
// a test passes or fails; a repair (debug) passes when the board is
// repaired and fails when it is scrapped; other steps have no result.
type outcome uint8

const (
	noResult outcome = iota
	testResult
	repairResult
)

type nodeInfo struct {
	id      string
	process bool
	station string // the log's station of a process step
	result  outcome
	reqs    []engine.Requirement
	product string // a source's entity type
	code    string // a source's family letter in serials (A for board_a)
}

type unitState struct{ busy, down bool }

// pendingDown is a breakdown the engine started on the unit an entity is
// releasing, before the observation that says which entity it is
// (finish-first, D-074 (a)).
type pendingDown struct {
	clock int
	tick  int64
}

type resState struct {
	id, station    string
	human          bool
	units          []unitState
	inUse, down    int // the engine's levels, from its last observations
	releasing      int // the engine's releases not yet matched to an entity or batch
	deferred       []pendingDown
	clockUnit      map[int]int     // failure clock -> unit down
	openDown       map[int]*record // unit -> its downtime
	operatorOffset int             // human: this resource's first operator number in a shift
}

type entState struct {
	serial, product string
	held            map[int]int // resource -> unit (not a batch member)
	op              *record     // the step in progress or just ended
	setup           int64       // the changeover ticks before the next service
}

type batchKey struct {
	node   int
	number uint64
}

type batchState struct {
	units             map[int]int // resource -> unit
	released          map[int]bool
	blocked           int
	members, finished int // members started, and ended
}

// observer turns the engine's observations of one replication into the
// station log. Units of a resource are fungible in the engine (but for a
// resource that changes over, D-055); the log names them: a step takes the
// unit of least index that is neither busy nor down, a breakdown the one
// it takes in the engine (an idle unit, or the one being released). At
// every step's start the units it books agree with the engine's level of
// units in use and down, or the observer fails: the log is never a guess
// the engine contradicts.
type observer struct {
	p        *engine.Program
	nodes    []nodeInfo
	res      []resState
	ents     map[uint64]*entState
	batches  map[batchKey]*batchState
	records  []*record
	shifts   *shiftTable
	operator map[string]int // shift label -> its first operator number
	err      error
}

// stationAttrs are the pack's domain attributes of a node (ELECTRONICS_LINE §3.3).
type stationAttrs struct {
	Station  string `json:"station"`
	Pass     string `json:"pass"`
	ResultOf string `json:"result_of"`
}

func attrs(raw jsontext.Value) (stationAttrs, error) {
	var a stationAttrs
	if len(raw) == 0 {
		return a, nil
	}
	err := json.Unmarshal(raw, &a)
	return a, err
}

func newObserver(p *engine.Program, ir *model.ModelIR, shifts *shiftTable) (*observer, error) {
	o := &observer{p: p, ents: map[uint64]*entState{}, batches: map[batchKey]*batchState{}, shifts: shifts}
	byID := map[string]*model.Node{}
	for i := range ir.Nodes {
		byID[ir.Nodes[i].ID] = &ir.Nodes[i]
	}
	out := map[string][]string{} // node -> the nodes its flows go to
	for _, f := range ir.Flows {
		out[f.From] = append(out[f.From], f.To)
	}
	stationOfResource := map[string]string{}
	o.nodes = make([]nodeInfo, p.NodeCount())
	for i := range o.nodes {
		id := p.NodeID(i)
		n := byID[id]
		a, err := attrs(n.DomainAttributes)
		if err != nil {
			return nil, fmt.Errorf("node %s: %w", id, err)
		}
		info := nodeInfo{id: id, process: n.Type == model.NodeProcess}
		switch n.Type {
		case model.NodeSource:
			info.product = n.EntityType
			cut := n.EntityType[strings.LastIndexByte(n.EntityType, '_')+1:]
			if len(cut) != 1 || cut[0] < 'a' || cut[0] > 'z' {
				return nil, fmt.Errorf("entity type %s does not end in a family letter", n.EntityType)
			}
			info.code = strings.ToUpper(cut)
		case model.NodeProcess:
			info.station = id
			if a.Station != "" {
				info.station = a.Station
			}
			info.reqs = p.Requires(i)
			for _, rq := range info.reqs {
				if rq.Units != 1 {
					return nil, fmt.Errorf("process %s takes %d units of a resource; the generator names one", id, rq.Units)
				}
				if rid := p.ResourceID(rq.Resource); a.Station != "" && a.Pass == "first" && stationOfResource[rid] == "" {
					stationOfResource[rid] = a.Station
				}
			}
			if to := out[id]; len(to) == 1 && byID[to[0]].Type == model.NodeDecision {
				d, err := attrs(byID[to[0]].DomainAttributes)
				if err != nil {
					return nil, fmt.Errorf("node %s: %w", to[0], err)
				}
				info.result = repairResult
				if d.ResultOf == id {
					info.result = testResult
				}
			}
		}
		o.nodes[i] = info
	}
	types := map[string]model.ResourceType{}
	setups := map[string]bool{}
	for _, r := range ir.Resources {
		types[r.ID] = r.Type
		setups[r.ID] = r.Setups != nil
	}
	o.res = make([]resState, p.ResourceCount())
	humans := 0
	for i := range o.res {
		id := p.ResourceID(i)
		c := p.ResourceCapacity(i)
		if setups[id] && c != 1 {
			return nil, fmt.Errorf("resource %s changes over with %d units; the generator cannot tell which unit the engine takes", id, c)
		}
		st := stationOfResource[id]
		if st == "" {
			st = id
		}
		r := resState{id: id, station: st, human: types[id] == model.ResourceHuman, units: make([]unitState, c),
			clockUnit: map[int]int{}, openDown: map[int]*record{}}
		if r.human {
			r.operatorOffset = humans
			humans += c
		}
		o.res[i] = r
	}
	o.operator = map[string]int{}
	for i, label := range shifts.labels {
		o.operator[label] = 1 + i*humans
	}
	return o, nil
}

func (o *observer) fail(format string, args ...any) {
	if o.err == nil {
		o.err = fmt.Errorf("observer: "+format, args...)
	}
}

func equipmentID(r *resState, u int) string { return r.id + "_" + strconv.Itoa(u+1) }

// take books the unit of least index of r that is neither busy nor down.
func (r *resState) take() int {
	for u := range r.units {
		if !r.units[u].busy && !r.units[u].down {
			r.units[u].busy = true
			return u
		}
	}
	return -1
}

func (r *resState) count() (busy, down int) {
	for _, s := range r.units {
		if s.busy {
			busy++
		}
		if s.down {
			down++
		}
	}
	return
}

// goDown takes unit u of r down for failure clock c at tick, opening its downtime.
func (o *observer) goDown(r *resState, u, c int, tick int64) {
	r.units[u].down = true
	r.clockUnit[c] = u
	id := equipmentID(r, u)
	d := &record{serial: id, recordType: "downtime", station: r.station, equipment: id, start: tick, end: -1, result: "breakdown",
		shift: o.shifts.at(tick), seq: len(o.records), node: -1}
	r.openDown[u] = d
	o.records = append(o.records, d)
}

// free releases unit u of r: it is idle, or it goes down at once for the
// first breakdown that waits for it.
func (o *observer) free(r *resState, u int) {
	if u < 0 || u >= len(r.units) || !r.units[u].busy {
		o.fail("%s: unit %d released but not busy", r.id, u)
		return
	}
	if r.releasing--; r.releasing < 0 {
		o.fail("%s: a release the engine did not report", r.id)
		return
	}
	r.units[u].busy = false
	if len(r.deferred) > 0 {
		d := r.deferred[0]
		r.deferred = r.deferred[1:]
		o.goDown(r, u, d.clock, d.tick)
	}
}

// check compares the units r books with the engine's levels.
func (o *observer) check(r *resState) {
	busy, down := r.count()
	if r.releasing != 0 || len(r.deferred) != 0 || busy != r.inUse || down != r.down {
		o.fail("%s: %d units busy and %d down booked, the engine has %d and %d (%d releases, %d breakdowns pending)",
			r.id, busy, down, r.inUse, r.down, r.releasing, len(r.deferred))
	}
}

func (o *observer) operatorID(r *resState, u int, shift string) string {
	first, ok := o.operator[shift]
	if !ok {
		o.fail("%s: a step starts outside every shift", r.id)
		return ""
	}
	return fmt.Sprintf("op-%02d", first+r.operatorOffset+u)
}

// Observe implements engine.Observer.
func (o *observer) Observe(ob engine.Observation) {
	if o.err != nil {
		return
	}
	switch ob.Kind {
	case engine.ObsArrival:
		src := &o.nodes[ob.Node]
		o.ents[ob.Entity] = &entState{serial: fmt.Sprintf("SYN-%s-%06d", src.code, ob.Value), product: src.product, held: map[int]int{}}
	case engine.ObsResource:
		r := &o.res[ob.Resource]
		if v := int(ob.Value); v < r.inUse {
			r.releasing += r.inUse - v
			r.inUse = v
		} else {
			r.inUse = v
		}
	case engine.ObsSetup:
		if e := o.ents[ob.Entity]; e != nil {
			e.setup = ob.Value
		}
	case engine.ObsServiceStart:
		o.start(ob)
	case engine.ObsServiceEnd:
		o.end(ob)
	case engine.ObsUnblocked:
		n := &o.nodes[ob.Node]
		if !n.process {
			return
		}
		if ob.Batch != 0 {
			b := o.batches[batchKey{ob.Node, ob.Batch}]
			if b == nil {
				o.fail("%s: an unknown batch unblocked", n.id)
				return
			}
			if b.blocked--; b.blocked == 0 {
				o.releaseBatch(n, b, ob)
			}
			return
		}
		e := o.ents[ob.Entity]
		for _, rq := range n.reqs {
			u, ok := e.held[rq.Resource]
			if !ok {
				o.fail("%s: an entity unblocked without its unit", n.id)
				return
			}
			delete(e.held, rq.Resource)
			o.free(&o.res[rq.Resource], u)
		}
	case engine.ObsBlocked:
		if ob.Batch != 0 && o.nodes[ob.Node].process {
			if b := o.batches[batchKey{ob.Node, ob.Batch}]; b != nil {
				b.blocked++
			}
		}
	case engine.ObsFailure:
		if e := o.ents[ob.Entity]; e != nil && e.op != nil && e.op.end == ob.Tick && o.nodes[e.op.node].result == testResult {
			e.op.result = "fail"
		}
	case engine.ObsComplete:
		if e := o.ents[ob.Entity]; e != nil && ob.Value == 0 && e.op != nil && e.op.end == ob.Tick && o.nodes[e.op.node].result == repairResult {
			e.op.result = "fail" // scrapped at debug
		}
		delete(o.ents, ob.Entity)
	case engine.ObsResourceDown:
		r := &o.res[ob.Resource]
		r.down = int(ob.Value)
		if r.releasing > 0 {
			r.deferred = append(r.deferred, pendingDown{ob.At, ob.Tick})
			return
		}
		u := -1
		for i, s := range r.units {
			if !s.busy && !s.down {
				u = i
				break
			}
		}
		if u < 0 {
			o.fail("%s: a breakdown with no idle unit", r.id)
			return
		}
		o.goDown(r, u, ob.At, ob.Tick)
	case engine.ObsResourceUp:
		r := &o.res[ob.Resource]
		r.down = int(ob.Value)
		u, ok := r.clockUnit[ob.At]
		if !ok || !r.units[u].down {
			o.fail("%s: a repair of no unit down", r.id)
			return
		}
		delete(r.clockUnit, ob.At)
		r.units[u].down = false
		r.openDown[u].end = ob.Tick
		delete(r.openDown, u)
	}
}

func (o *observer) start(ob engine.Observation) {
	n := &o.nodes[ob.Node]
	e := o.ents[ob.Entity]
	if e == nil {
		o.fail("%s: a step of an unknown entity", n.id)
		return
	}
	start := ob.Tick + e.setup // the changeover is the gap before the step (ELECTRONICS_LINE §6)
	e.setup = 0
	shift := o.shifts.at(start)
	rec := &record{serial: e.serial, recordType: "operation", station: n.station, product: e.product, start: start, end: -1,
		shift: shift, seq: len(o.records), node: ob.Node}
	var b *batchState
	if ob.Batch != 0 {
		k := batchKey{ob.Node, ob.Batch}
		if b = o.batches[k]; b == nil {
			b = &batchState{units: map[int]int{}, released: map[int]bool{}}
			o.batches[k] = b
		}
		b.members++
	}
	for _, rq := range n.reqs {
		r := &o.res[rq.Resource]
		var u int
		if b != nil {
			var ok bool
			if u, ok = b.units[rq.Resource]; !ok {
				u = r.take()
				b.units[rq.Resource] = u
			}
		} else {
			u = r.take()
			e.held[rq.Resource] = u
		}
		if u < 0 {
			o.fail("%s: a step starts with no idle unit of %s", n.id, r.id)
			return
		}
		o.check(r)
		if r.human {
			if rec.operator != "" {
				o.fail("%s: two people; the log names one", n.id)
			}
			rec.operator = o.operatorID(r, u, shift)
		} else {
			if rec.equipment != "" {
				o.fail("%s: two machines; the log names one", n.id)
			}
			rec.equipment = equipmentID(r, u)
		}
	}
	e.op = rec
	o.records = append(o.records, rec)
}

func (o *observer) end(ob engine.Observation) {
	n := &o.nodes[ob.Node]
	e := o.ents[ob.Entity]
	if e == nil || e.op == nil || e.op.node != ob.Node || e.op.end >= 0 {
		o.fail("%s: a step ends that did not start", n.id)
		return
	}
	e.op.end = ob.Tick
	if n.result != noResult {
		e.op.result = "pass" // until a failure or a scrap says otherwise
	}
	if ob.Batch != 0 {
		b := o.batches[batchKey{ob.Node, ob.Batch}]
		if b == nil {
			o.fail("%s: an unknown batch ended", n.id)
			return
		}
		// The bundle is released once, before the members' ends, unless a
		// member is blocked (then by the last one unblocked).
		b.finished++
		o.releaseBatch(n, b, ob)
		return
	}
	for _, rq := range n.reqs {
		r := &o.res[rq.Resource]
		if r.releasing == 0 {
			continue // blocked: it keeps the unit (D-049)
		}
		u, ok := e.held[rq.Resource]
		if !ok {
			o.fail("%s: an entity ends without its unit", n.id)
			return
		}
		delete(e.held, rq.Resource)
		o.free(r, u)
	}
}

func (o *observer) releaseBatch(n *nodeInfo, b *batchState, ob engine.Observation) {
	for _, rq := range n.reqs {
		r := &o.res[rq.Resource]
		if b.released[rq.Resource] || r.releasing == 0 {
			continue
		}
		b.released[rq.Resource] = true
		o.free(r, b.units[rq.Resource])
	}
	done := b.finished == b.members && b.blocked == 0
	for _, rq := range n.reqs {
		done = done && b.released[rq.Resource]
	}
	if done {
		delete(o.batches, batchKey{ob.Node, ob.Batch})
	}
}

// shiftTable is the line calendar's labelled shifts within its period, in ticks.
type shiftTable struct {
	period int64
	spans  []shiftSpan
	labels []string // in order of first appearance
}

type shiftSpan struct {
	start, end int64
	label      string
}

var errCalendar = errors.New("the model needs exactly one calendar, labelled shifts and no exceptions")

func newShiftTable(ir *model.ModelIR) (*shiftTable, error) {
	if len(ir.Calendars) != 1 || len(ir.Calendars[0].Exceptions) != 0 {
		return nil, errCalendar
	}
	unit := model.TimeUnit(ir.TimeUnit)
	c := ir.Calendars[0]
	period, err := model.Ticks(c.Period, unit)
	if err != nil {
		return nil, err
	}
	t := &shiftTable{period: period}
	seen := map[string]bool{}
	for _, s := range c.Shifts {
		if s.Label == "" {
			return nil, errCalendar
		}
		a, err1 := model.Ticks(s.Start, unit)
		b, err2 := model.Ticks(s.End, unit)
		if err1 != nil || err2 != nil {
			return nil, errCalendar
		}
		t.spans = append(t.spans, shiftSpan{a, b, s.Label})
		if !seen[s.Label] {
			seen[s.Label] = true
			t.labels = append(t.labels, s.Label)
		}
	}
	return t, nil
}

// at is the label of the shift at tick, "" outside every shift.
func (t *shiftTable) at(tick int64) string {
	m := tick % t.period
	for _, s := range t.spans {
		if m >= s.start && m < s.end {
			return s.label
		}
	}
	return ""
}
