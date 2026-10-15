package engine

import (
	"encoding/json/v2"
	"fmt"
	"math"
	"math/rand/v2"
	"path/filepath"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
)

type engineVectors struct {
	ReplicationSeed struct {
		BaseSeed uint64 `json:"base_seed"`
		Index    uint32 `json:"index"`
	} `json:"replication_seed"`
	BeyondCap int64 `json:"beyond_cap_ticks"`
	Samplers  []struct {
		Name         string             `json:"name"`
		Unit         model.TimeUnit     `json:"unit"`
		Distribution model.Distribution `json:"distribution"`
		Samples      []float64          `json:"samples"`
		SamplesUnit  model.TimeUnit     `json:"samples_unit"`
		NodeID       string             `json:"node_id"`
		Purpose      string             `json:"purpose"`
		Expected     []int64            `json:"expected_ticks"`
		StateAfter   string             `json:"state_after"`
	} `json:"sampler_vectors"`
	EventOrder struct {
		Pushes []struct {
			Tick     int64 `json:"tick"`
			Priority uint8 `json:"priority"`
		} `json:"pushes"`
		PopSequence []uint64 `json:"pop_sequence"`
	} `json:"event_order"`
	Arrivals struct {
		Run struct {
			BaseSeed         uint64         `json:"base_seed"`
			ReplicationIndex uint32         `json:"replication_index"`
			Unit             model.TimeUnit `json:"unit"`
			Horizon          struct {
				Value float64        `json:"value"`
				Unit  model.TimeUnit `json:"unit"`
			} `json:"horizon"`
		} `json:"run"`
		Sources []struct {
			ID           string             `json:"id"`
			Distribution model.Distribution `json:"distribution"`
			MaxEntities  int                `json:"max_entities"`
		} `json:"sources"`
		Expected [][2]any `json:"expected"`
	} `json:"arrivals"`
}

func loadVectors(t *testing.T) engineVectors {
	t.Helper()
	var v engineVectors
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/engine-vectors.json")), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// The samplers reproduce the independent Python reference
// (scripts/engine_reference.py) tick for tick, including how many draws
// each consumes (a constant consumes none).
func TestSamplerVectors(t *testing.T) {
	v := loadVectors(t)
	if v.BeyondCap != beyondCap {
		t.Fatalf("beyond-cap ticks %d, reference %d", beyondCap, v.BeyondCap)
	}
	rep := ReplicationSeed(v.ReplicationSeed.BaseSeed, v.ReplicationSeed.Index)
	if len(v.Samplers) < 10 {
		t.Fatalf("only %d sampler vectors", len(v.Samplers))
	}
	for _, c := range v.Samplers {
		t.Run(c.Name, func(t *testing.T) {
			var empirical []int64
			for _, s := range c.Samples {
				tk, err := model.Ticks(s, c.SamplesUnit)
				if err != nil {
					t.Fatal(err)
				}
				empirical = append(empirical, tk)
			}
			s, err := newSampler(c.Distribution, c.Unit, empirical)
			if err != nil {
				t.Fatal(err)
			}
			r := Stream(rep, c.NodeID, c.Purpose)
			for i, want := range c.Expected {
				if got := s.draw(r); got != want {
					t.Fatalf("draw %d: %d ticks, reference %d", i, got, want)
				}
			}
			if got := fmt.Sprintf("%016x", r.state); got != c.StateAfter {
				t.Errorf("stream state %s, reference %s (wrong number of draws)", got, c.StateAfter)
			}
		})
	}
}

// quantize is model.Ticks for every in-range value; it only differs by
// saturating at beyondCap instead of failing (review rule: one numerical
// profile).
func TestQuantizeMatchesTicks(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	values := []float64{0, 5e-324, 0.4999999, 0.49999999999999994, 0.5, 1.5, 2.5, 1e-9, 365.0 * 24 * 60 * 60 * 1000}
	for range 20000 {
		values = append(values, r.Float64()*math.Pow(10, float64(r.IntN(14))-3))
	}
	for _, unit := range []model.TimeUnit{model.Millisecond, model.Second, model.Minute, model.Hour, model.Day} {
		ms, _ := unit.Millis()
		for _, x := range values {
			want, err := model.Ticks(x, unit)
			got := quantize(x, float64(ms))
			if err != nil {
				if got != beyondCap {
					t.Fatalf("%g %s: over the cap but quantize gave %d", x, unit, got)
				}
				continue
			}
			if got != want {
				t.Fatalf("%g %s: quantize %d, model.Ticks %d", x, unit, got, want)
			}
		}
	}
	if quantize(math.MaxFloat64, 1) != beyondCap {
		t.Error("huge sample does not saturate")
	}
}

// Every sample is a valid duration: within [0, beyondCap], an exponential
// never zero, an empirical always one of its values, and never a panic.
func TestSamplerBounds(t *testing.T) {
	one, ten, mean, tiny := 1.0, 10.0, 2.0, 5e-324
	cases := map[string]struct {
		d     model.Distribution
		vals  []int64
		check func(int64) bool
	}{
		"exponential":       {model.Distribution{Type: model.DistExponential, Mean: &mean}, nil, func(x int64) bool { return x >= 1 && x <= beyondCap }},
		"tiny exponential":  {model.Distribution{Type: model.DistExponential, Mean: &tiny}, nil, func(x int64) bool { return x == 1 }},
		"triangular":        {model.Distribution{Type: model.DistTriangular, Min: &one, Mode: &one, Max: &ten}, nil, func(x int64) bool { return x >= 60_000 && x <= 600_000 }},
		"empirical one":     {model.Distribution{Type: model.DistEmpirical, SamplesRef: "dataset:x"}, []int64{42}, func(x int64) bool { return x == 42 }},
		"empirical several": {model.Distribution{Type: model.DistEmpirical, SamplesRef: "dataset:x"}, []int64{0, 7, 9}, func(x int64) bool { return x == 0 || x == 7 || x == 9 }},
	}
	for name, c := range cases {
		s, err := newSampler(c.d, model.Minute, c.vals)
		if err != nil {
			t.Fatal(name, err)
		}
		r := NewSplitMix64(uint64(len(name)))
		for range 100000 {
			if x := s.draw(r); !c.check(x) {
				t.Fatalf("%s: sample %d out of bounds", name, x)
			}
		}
	}
}

// newSampler refuses what it cannot sample, instead of panicking later.
func TestSamplerRejects(t *testing.T) {
	neg, zero, two, one, inf := -1.0, 0.0, 2.0, 1.0, math.Inf(1)
	big := 400.0
	for name, c := range map[string]struct {
		d    model.Distribution
		unit model.TimeUnit
		vals []int64
	}{
		"missing value":       {model.Distribution{Type: model.DistConstant}, model.Minute, nil},
		"constant over cap":   {model.Distribution{Type: model.DistConstant, Value: &big}, model.Day, nil},
		"exponential zero":    {model.Distribution{Type: model.DistExponential, Mean: &zero}, model.Minute, nil},
		"exponential inf":     {model.Distribution{Type: model.DistExponential, Mean: &inf}, model.Minute, nil},
		"triangular order":    {model.Distribution{Type: model.DistTriangular, Min: &two, Mode: &one, Max: &two}, model.Minute, nil},
		"triangular negative": {model.Distribution{Type: model.DistTriangular, Min: &neg, Mode: &one, Max: &two}, model.Minute, nil},
		"triangular over cap": {model.Distribution{Type: model.DistTriangular, Min: &one, Mode: &one, Max: &big}, model.Day, nil},
		"empirical empty":     {model.Distribution{Type: model.DistEmpirical, SamplesRef: "dataset:x"}, model.Minute, nil},
		"unknown type":        {model.Distribution{Type: "lognormal"}, model.Minute, nil},
		"unknown unit":        {model.Distribution{Type: model.DistConstant, Value: &one}, "week", nil},
	} {
		if _, err := newSampler(c.d, c.unit, c.vals); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// The event order is (tick, priority, sequence), as the reference sorts it.
func TestEventOrderVector(t *testing.T) {
	v := loadVectors(t)
	var q eventQueue
	for _, p := range v.EventOrder.Pushes {
		q.schedule(event{Tick: p.Tick, Priority: p.Priority})
	}
	var got []uint64
	for e, ok := q.next(); ok; e, ok = q.next() {
		got = append(got, e.Seq)
	}
	if fmt.Sprint(got) != fmt.Sprint(v.EventOrder.PopSequence) {
		t.Errorf("pop order %v, reference %v", got, v.EventOrder.PopSequence)
	}
}

// Property: for random schedules every pop returns the earliest event
// present, by (tick, priority, seq), ticks never go backwards, and nothing is
// lost. (Popped keys are not globally increasing: an event scheduled during a
// tick at a lower priority, e.g. a zero-duration service completing during a
// dispatch, legitimately runs next.)
func TestEventQueueOrder(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	priorities := []uint8{PriorityServiceComplete, PriorityImmediate, PriorityDispatch}
	for trial := range 300 {
		var q eventQueue
		var shadow []event
		now := int64(0)
		pop := func() {
			e, _ := q.next()
			earliest := 0
			for i := range shadow {
				if shadow[i].before(shadow[earliest]) {
					earliest = i
				}
			}
			if shadow[earliest] != e {
				t.Fatalf("trial %d: popped %+v, earliest present is %+v", trial, e, shadow[earliest])
			}
			if e.Tick < now {
				t.Fatalf("trial %d: time went back from %d to %d", trial, now, e.Tick)
			}
			if p, ok := q.peek(); ok && p.before(e) {
				t.Fatalf("trial %d: peek %+v is earlier than the popped %+v", trial, p, e)
			}
			shadow = append(shadow[:earliest], shadow[earliest+1:]...)
			now = e.Tick
		}
		for range 200 {
			if r.IntN(3) > 0 || q.Len() == 0 {
				shadow = append(shadow, q.schedule(event{Tick: now + int64(r.IntN(5)), Priority: priorities[r.IntN(3)]}))
				continue
			}
			pop()
		}
		for q.Len() > 0 {
			pop()
		}
		if len(shadow) != 0 {
			t.Fatalf("trial %d: %d events lost", trial, len(shadow))
		}
	}
}
