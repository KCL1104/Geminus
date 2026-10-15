package metrics

// An independent recomputation of every generic metric from the raw
// observation list, ported from the T-022 review: it pairs queue enters with
// service starts and service starts with ends, integrating intervals rather
// than the Collector's level-based step functions, and derives labels and
// requirements from the IR rather than the Program. Build must agree with it
// on every case: multi-replication runs, multi-unit bundles on capacities
// above 1, rework back into an explicit queue next to a direct route into the
// same buffer, failures and reworks at different rates, warmups, and a queue
// with no visit in the window.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/engine"
)

type rawObs struct{ all []engine.Observation }

func (r *rawObs) Observe(o engine.Observation) { r.all = append(r.all, o) }

type tee2 struct{ a, b engine.Observer }

func (t tee2) Observe(o engine.Observation) { t.a.Observe(o); t.b.Observe(o) }

type oracleRep struct {
	completed, failures, reworks, censored int64
	leads                                  []float64
	waits                                  map[string][]float64 // by label
	queueArea                              map[string]int64     // by label
	busy                                   map[string]int64     // by resource id
}

func ovl(a, b, w, h int64) int64 {
	lo, hi := a, b
	if w > lo {
		lo = w
	}
	if h < hi {
		hi = h
	}
	if hi < lo {
		return 0
	}
	return hi - lo
}

func oracle(ir model.ModelIR, p *engine.Program, obs []engine.Observation) oracleRep {
	w, h := p.WarmupTicks(), p.HorizonTicks()
	label := map[string]string{}
	requires := map[string][]model.ResourceRequirement{}
	for _, n := range ir.Nodes {
		if n.Type == model.NodeProcess {
			label[n.ID] = n.ID
			requires[n.ID] = n.Requires
		}
	}
	for _, n := range ir.Nodes {
		if n.Type == model.NodeQueue {
			for _, f := range ir.Flows {
				if f.From == n.ID {
					label[f.To] = n.ID
				}
			}
		}
	}
	r := oracleRep{waits: map[string][]float64{}, queueArea: map[string]int64{}, busy: map[string]int64{}}
	arr := map[uint64]int64{}
	type pending struct {
		node string
		at   int64
	}
	enq := map[uint64]pending{}
	svc := map[uint64]pending{}
	arrivals, exits := int64(0), int64(0)
	for _, o := range obs {
		name := ""
		if o.Kind != engine.ObsResource {
			name = p.NodeID(o.Node)
		}
		switch o.Kind {
		case engine.ObsArrival:
			arr[o.Entity] = o.Tick
			arrivals++
		case engine.ObsQueueEnter:
			enq[o.Entity] = pending{name, o.Tick}
		case engine.ObsServiceStart:
			q := enq[o.Entity]
			if q.node != name {
				panic("service start without queue enter at the same process")
			}
			delete(enq, o.Entity)
			r.queueArea[label[name]] += ovl(q.at, o.Tick, w, h)
			if q.at >= w && o.Tick <= h {
				r.waits[label[name]] = append(r.waits[label[name]], float64(o.Tick-q.at)/60000)
			}
			svc[o.Entity] = pending{name, o.Tick}
		case engine.ObsServiceEnd:
			s := svc[o.Entity]
			delete(svc, o.Entity)
			for _, rq := range requires[s.node] {
				r.busy[rq.ResourceID] += int64(rq.Units) * ovl(s.at, o.Tick, w, h)
			}
		case engine.ObsFailure:
			if w <= o.Tick && o.Tick <= h {
				r.failures++
			}
		case engine.ObsRework:
			if w <= o.Tick && o.Tick <= h {
				r.reworks++
			}
		case engine.ObsComplete:
			exits++
			if o.Value == 1 && w <= o.Tick && o.Tick <= h {
				r.completed++
				if arr[o.Entity] >= w {
					r.leads = append(r.leads, float64(o.Tick-arr[o.Entity])/60000)
				}
			}
		}
	}
	// open intervals close at H
	for _, q := range enq {
		r.queueArea[label[q.node]] += ovl(q.at, h, w, h)
	}
	for _, s := range svc {
		for _, rq := range requires[s.node] {
			r.busy[rq.ResourceID] += int64(rq.Units) * ovl(s.at, h, w, h)
		}
	}
	r.censored = arrivals - exits
	return r
}

func omean(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

func oq7(x []float64, p float64) float64 {
	s := append([]float64(nil), x...)
	sort.Float64s(s)
	hh := float64(len(s)-1) * p
	lo := int(math.Floor(hh))
	if lo+1 >= len(s) {
		return s[len(s)-1]
	}
	return s[lo] + (hh-float64(lo))*(s[lo+1]-s[lo])
}

func close9(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }

func checkSummary(t *testing.T, name string, got simulation.MetricSummary, vals []float64, pop simulation.Population, wantCI bool) {
	t.Helper()
	if len(vals) == 0 {
		if got.Status != simulation.Unavailable || got.Mean != nil || got.Reason == nil {
			t.Errorf("%s: want unavailable, got %+v", name, got)
		}
		return
	}
	if got.Status != simulation.Available || got.SampleCount != int64(len(vals)) || got.Population != pop ||
		!close9(*got.Mean, omean(vals)) || !close9(*got.P50, oq7(vals, .5)) || !close9(*got.P95, oq7(vals, .95)) {
		t.Errorf("%s: got %+v (mean %v p50 %v p95 %v), oracle n=%d mean %v p50 %v p95 %v", name, got, deref(got.Mean), deref(got.P50), deref(got.P95), len(vals), omean(vals), oq7(vals, .5), oq7(vals, .95))
	}
	if wantCI && len(vals) >= 2 {
		m := omean(vals)
		ss := 0.0
		for _, v := range vals {
			ss += (v - m) * (v - m)
		}
		half := studentT975[len(vals)-1] * math.Sqrt(ss/float64(len(vals)-1)/float64(len(vals)))
		ci := got.ConfidenceIntervalMean
		if ci == nil || !close9(ci.Lower, m-half) || !close9(ci.Upper, m+half) {
			t.Errorf("%s: CI %+v, oracle [%v, %v]", name, ci, m-half, m+half)
		}
	} else if got.ConfidenceIntervalMean != nil {
		t.Errorf("%s: unexpected CI %+v", name, got.ConfidenceIntervalMean)
	}
}

func oracleCompare(t *testing.T, in simulation.RunInput) {
	t.Helper()
	p, err := engine.Compile(in)
	if err != nil {
		var ie *engine.InputError
		errors.As(err, &ie)
		t.Fatalf("compile: %v %+v", err, ie.Issues)
	}
	b := engine.NewBudget(in.Limits.MaxEvents)
	var reps []Replication
	var ors []oracleRep
	for i := range p.Replications() {
		c := NewCollector(p)
		raw := &rawObs{}
		out, err := engine.RunReplication(context.Background(), p, uint32(i), b, tee2{c, raw})
		if err != nil {
			t.Fatalf("rep %d: %v", i, err)
		}
		c.Finish()
		reps = append(reps, Replication{Collector: c, Outcome: out})
		ors = append(ors, oracle(in.Model, p, raw.all))
	}
	m, s, _, err := Build(p, reps)
	if err != nil {
		t.Fatal(err)
	}
	w, h := p.WarmupTicks(), p.HorizonTicks()
	span := float64(h - w)
	var thr, fl, rw, pooled, repMeans []float64
	all := true
	waits := map[string][]float64{}
	for i, o := range ors {
		thr = append(thr, float64(o.completed))
		fl = append(fl, float64(o.failures))
		rw = append(rw, float64(o.reworks))
		pooled = append(pooled, o.leads...)
		if len(o.leads) > 0 {
			repMeans = append(repMeans, omean(o.leads))
		} else {
			all = false
		}
		for k, v := range o.waits {
			waits[k] = append(waits[k], v...)
		}
		ss := s[i]
		if ss.CompletedCount != o.completed || ss.Failures != o.failures || ss.Reworks != o.reworks || ss.CensoredEntities != o.censored {
			t.Errorf("rep %d summary %+v, oracle completed %d failures %d reworks %d censored %d", i, ss, o.completed, o.failures, o.reworks, o.censored)
		}
		if (ss.MeanLeadTimeMinutes == nil) != (len(o.leads) == 0) || (ss.MeanLeadTimeMinutes != nil && !close9(*ss.MeanLeadTimeMinutes, omean(o.leads))) {
			t.Errorf("rep %d mean lead %v, oracle %v", i, deref(ss.MeanLeadTimeMinutes), o.leads)
		}
		for _, r := range in.Model.Resources {
			want := float64(o.busy[r.ID]) / (float64(r.Capacity) * span)
			if !close9(ss.ResourceUtilization[r.ID], want) {
				t.Errorf("rep %d utilization %s %v, oracle %v", i, r.ID, ss.ResourceUtilization[r.ID], want)
			}
		}
	}
	checkSummary(t, "throughput", m.Throughput, thr, simulation.PopulationReplication, true)
	checkSummary(t, "failures", m.FailureCount, fl, simulation.PopulationReplication, true)
	checkSummary(t, "reworks", m.ReworkCount, rw, simulation.PopulationReplication, true)
	checkSummary(t, "lead", m.LeadTimeMinutes, pooled, simulation.PopulationEntity, false)
	if all {
		checkSummary(t, "repmean", m.AdditionalMetrics[ReplicationMeanLeadTime], repMeans, simulation.PopulationReplication, true)
	} else if m.AdditionalMetrics[ReplicationMeanLeadTime].Status != simulation.Unavailable {
		t.Errorf("repmean should be unavailable")
	}
	for _, r := range in.Model.Resources {
		var u []float64
		for _, o := range ors {
			u = append(u, float64(o.busy[r.ID])/(float64(r.Capacity)*span))
		}
		got := m.Resources[r.ID]
		if !close9(*got.UtilizationMean, omean(u)) || got.Capacity != r.Capacity || got.ObservationTicks != h-w {
			t.Errorf("resource %s %+v (util %v), oracle %v", r.ID, got, *got.UtilizationMean, omean(u))
		}
	}
	labels := map[string]bool{}
	for _, n := range in.Model.Nodes {
		if n.Type == model.NodeProcess {
			l := n.ID
			for _, q := range in.Model.Nodes {
				if q.Type == model.NodeQueue {
					for _, f := range in.Model.Flows {
						if f.From == q.ID && f.To == n.ID {
							l = q.ID
						}
					}
				}
			}
			labels[l] = true
		}
	}
	if len(labels) != len(m.Queues) {
		t.Errorf("queue keys %d, oracle %d", len(m.Queues), len(labels))
	}
	for l := range labels {
		got, ok := m.Queues[l]
		if !ok {
			t.Errorf("queue %s missing", l)
			continue
		}
		var ql []float64
		for _, o := range ors {
			ql = append(ql, float64(o.queueArea[l])/span)
		}
		if !close9(got.QueueLengthMean, omean(ql)) {
			t.Errorf("queue %s length %v, oracle %v", l, got.QueueLengthMean, omean(ql))
		}
		ws := waits[l]
		if int64(len(ws)) != got.SampleCount {
			t.Errorf("queue %s samples %d, oracle %d", l, got.SampleCount, len(ws))
		}
		if len(ws) == 0 {
			if got.Status != simulation.Unavailable || got.Reason == nil || got.WaitMeanMinutes != nil {
				t.Errorf("queue %s want unavailable %+v", l, got)
			}
		} else if got.Status != simulation.Available || !close9(*got.WaitMeanMinutes, omean(ws)) || !close9(*got.WaitP50Minutes, oq7(ws, .5)) || !close9(*got.WaitP95Minutes, oq7(ws, .95)) {
			t.Errorf("queue %s %+v, oracle mean %v p50 %v p95 %v", l, got, omean(ws), oq7(ws, .5), oq7(ws, .95))
		}
	}
}

func flow(id, from, to string, pr float64) model.Flow {
	return model.Flow{ID: id, From: from, To: to, Routing: model.Routing{Type: model.RoutingProbability, Probability: pr}}
}

func dist(typ model.DistributionType, a ...float64) model.Distribution {
	d := model.Distribution{Type: typ}
	switch typ {
	case model.DistConstant:
		d.Value = &a[0]
	case model.DistExponential:
		d.Mean = &a[0]
	case model.DistTriangular:
		d.Min, d.Mode, d.Max = &a[0], &a[1], &a[2]
	}
	return d
}

// multi: two sources; src -> inbox (explicit queue) -> p1 (R x2 + S x1);
// src2 -> p1 directly (bypassing inbox, same buffer); p1 -> d: 30% redo (rework)
// -> inbox; 10% bad (failure) -> gone (abandoned); 60% p2 (R x3) -> hold
// (delay) -> done.
func multi(t *testing.T) model.ModelIR {
	ir := golden(t, "deterministic-queue.json")
	ir.Resources = []model.Resource{
		{ID: "R", Type: ir.Resources[0].Type, Name: "R", Capacity: 5, QueueDiscipline: ir.Resources[0].QueueDiscipline},
		{ID: "S", Type: ir.Resources[0].Type, Name: "S", Capacity: 2, QueueDiscipline: ir.Resources[0].QueueDiscipline},
	}
	src := ir.Nodes[0]
	src.ID = "src"
	src.Arrival = &model.Arrival{Distribution: dist(model.DistExponential, 3)}
	src2 := src
	src2.ID = "src2"
	src2.Arrival = &model.Arrival{Distribution: dist(model.DistExponential, 7)}
	proc := ir.Nodes[1]
	p1 := proc
	p1.ID = "p1"
	p1.ServiceTime = &model.TimedDistribution{Distribution: dist(model.DistExponential, 4)}
	p1.Requires = []model.ResourceRequirement{{ResourceID: "R", Units: 2}, {ResourceID: "S", Units: 1}}
	p2 := proc
	p2.ID = "p2"
	p2.ServiceTime = &model.TimedDistribution{Distribution: dist(model.DistTriangular, 1, 3, 9)}
	p2.Requires = []model.ResourceRequirement{{ResourceID: "R", Units: 3}}
	sink := ir.Nodes[2]
	sink.ID = "done"
	ir.Nodes = []model.Node{src, src2, p1, p2, sink,
		{ID: "inbox", Type: model.NodeQueue}, {ID: "d", Type: model.NodeDecision},
		{ID: "redo", Type: model.NodeRework}, {ID: "bad", Type: model.NodeFailure},
		{ID: "gone", Type: model.NodeSink, Outcome: model.SinkAbandoned},
		{ID: "hold", Type: model.NodeDelay, DelayTime: &model.TimedDistribution{Distribution: dist(model.DistConstant, 2)}},
	}
	ir.Flows = []model.Flow{flow("a1", "src", "inbox", 1), flow("a2", "src2", "p1", 1), flow("a3", "inbox", "p1", 1),
		flow("b1", "p1", "d", 1), flow("c1", "d", "redo", 0.3), flow("c2", "d", "bad", 0.1), flow("c3", "d", "p2", 0.6),
		flow("e1", "redo", "inbox", 1), flow("e2", "bad", "gone", 1), flow("e3", "p2", "hold", 1), flow("e4", "hold", "done", 1)}
	ir.Metrics = nil
	ir.Views.TwoD.Positions = nil
	return ir
}

func TestOracleAgreement(t *testing.T) {
	type c struct {
		name string
		ir   model.ModelIR
		h, w float64
		reps int
	}
	cases := []c{
		{"default-delivery", golden(t, "default-delivery.json"), 10 * 24 * 60, 24 * 60, 6},
		{"default-delivery-w0", golden(t, "default-delivery.json"), 3 * 24 * 60, 0, 5},
		{"congested-W45-H204", golden(t, "congested-queue.json"), 204, 45, 1},
		{"congested-unavailable-queue", golden(t, "congested-queue.json"), 151, 150, 1},
		{"deterministic-W12", golden(t, "deterministic-queue.json"), 1010, 12, 1},
	}
	for i := 0; i < 12; i++ {
		cases = append(cases, c{fmt.Sprintf("multi-%d", i), multi(t), float64(200 + 97*i), float64(13 * i), 2 + i%5})
	}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			in := runInput(cs.ir, cs.h, cs.w, cs.reps)
			in.Config.Seed = 11
			oracleCompare(t, in)
		})
	}
}
