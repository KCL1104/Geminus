package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"math"
	"math/rand/v2"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
		DigestDraws  int                `json:"digest_draws"`
		Digest       string             `json:"digest"`
		DigestState  string             `json:"digest_state_after"`
	} `json:"sampler_vectors"`
	LogProfile struct {
		Seed      string            `json:"seed"`
		Inputs    int               `json:"inputs"`
		Digest    string            `json:"digest"`
		Constants map[string]string `json:"constants"`
		Edges     []struct {
			X   string `json:"x"`
			Log string `json:"log"`
		} `json:"edges"`
	} `json:"log_profile"`
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
		Expected     [][2]any          `json:"expected"`
		StreamStates map[string]string `json:"stream_states_after"`
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

// samplerOf compiles a vector case's sampler.
func samplerOf(t *testing.T, d model.Distribution, unit model.TimeUnit, samples []float64, samplesUnit model.TimeUnit) *sampler {
	t.Helper()
	var empirical []int64
	for _, s := range samples {
		tk, err := model.Ticks(s, samplesUnit)
		if err != nil {
			t.Fatal(err)
		}
		empirical = append(empirical, tk)
	}
	s, err := newSampler(d, unit, empirical)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Over 10^5 draws per case the samplers agree with the reference bit for
// bit: the hash covers every tick count and, for continuous samplers, the
// binary64 value before quantization, so a different formula, operation
// order or logarithm fails here even when no tick changes (review of T-020).
func TestSamplerDigests(t *testing.T) {
	v := loadVectors(t)
	rep := ReplicationSeed(v.ReplicationSeed.BaseSeed, v.ReplicationSeed.Index)
	for _, c := range v.Samplers {
		t.Run(c.Name, func(t *testing.T) {
			if c.DigestDraws < 100_000 {
				t.Fatalf("digest over %d draws", c.DigestDraws)
			}
			s := samplerOf(t, c.Distribution, c.Unit, c.Samples, c.SamplesUnit)
			r := Stream(rep, c.NodeID, c.Purpose)
			h := sha256.New()
			var buf [16]byte
			for range c.DigestDraws {
				if !s.continuous() {
					binary.LittleEndian.PutUint64(buf[:8], uint64(s.draw(r)))
					h.Write(buf[:8])
					continue
				}
				x := s.value(r.Uniform())
				binary.LittleEndian.PutUint64(buf[:8], uint64(s.ticksOf(x)))
				binary.LittleEndian.PutUint64(buf[8:], math.Float64bits(x))
				h.Write(buf[:])
			}
			if got := hex.EncodeToString(h.Sum(nil)); got != c.Digest {
				t.Errorf("digest %s, reference %s", got, c.Digest)
			}
			if got := fmt.Sprintf("%016x", r.state); got != c.DigestState {
				t.Errorf("stream state %s, reference %s", got, c.DigestState)
			}
		})
	}
}

// ln is the pinned logarithm: bit-identical to the reference's port on its
// edge inputs and on 10^6 inputs 1-U, whatever GOARCH's math.Log does.
func TestLogProfile(t *testing.T) {
	lp := loadVectors(t).LogProfile
	if lp.Inputs < 1_000_000 || len(lp.Edges) == 0 {
		t.Fatalf("log profile with %d inputs, %d edges", lp.Inputs, len(lp.Edges))
	}
	// The coefficients by bit pattern: a 1-ulp typo would almost never change
	// a result bit, so the digest alone cannot catch it.
	constants := map[string]float64{"ln2_hi": ln2Hi, "ln2_lo": ln2Lo, "l1": lnL1, "l2": lnL2, "l3": lnL3, "l4": lnL4,
		"l5": lnL5, "l6": lnL6, "l7": lnL7, "sqrt_half": sqrtHalf}
	if len(lp.Constants) != len(constants) {
		t.Fatalf("%d reference constants, %d in ln", len(lp.Constants), len(constants))
	}
	for name, v := range constants {
		if got := fmt.Sprintf("%016x", math.Float64bits(v)); got != lp.Constants[name] {
			t.Errorf("constant %s = %s, reference %s", name, got, lp.Constants[name])
		}
	}
	for _, e := range lp.Edges {
		xb, err1 := strconv.ParseUint(e.X, 16, 64)
		lb, err2 := strconv.ParseUint(e.Log, 16, 64)
		if err1 != nil || err2 != nil {
			t.Fatal(err1, err2)
		}
		if got := math.Float64bits(ln(math.Float64frombits(xb))); got != lb {
			t.Errorf("ln(%v) bits %016x, reference %016x", math.Float64frombits(xb), got, lb)
		}
	}
	seed, err := strconv.ParseUint(lp.Seed, 16, 64)
	if err != nil {
		t.Fatal(err)
	}
	r := NewSplitMix64(seed)
	h := sha256.New()
	var buf [8]byte
	for range lp.Inputs {
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(ln(1-r.Uniform())))
		h.Write(buf[:])
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != lp.Digest {
		t.Errorf("log digest %s, reference %s", got, lp.Digest)
	}
}

// The pinned logarithm is a correct logarithm: within one ulp of math.Log on
// the whole domain 1-U can take, and exact where the result is exact.
func TestLogAccuracy(t *testing.T) {
	ulp := func(x float64) float64 { return math.Nextafter(x, math.Inf(1)) - x }
	r, q := NewSplitMix64(99), rand.New(rand.NewPCG(7, 8))
	for i := range 2_000_000 {
		x := 1 - r.Uniform()
		if i%2 == 1 {
			x = math.Ldexp(x, -q.IntN(1000)) // also tiny inputs, far below 2^-53
		}
		got, want := ln(x), math.Log(x)
		if math.Abs(got-want) > ulp(math.Abs(want)) {
			t.Fatalf("ln(%v) = %v, math.Log %v", x, got, want)
		}
	}
	if ln(1) != 0 || ln(0.5) != -math.Ln2 || !math.IsInf(ln(0), -1) || !math.IsNaN(ln(-1)) || !math.IsNaN(ln(math.NaN())) || !math.IsInf(ln(math.Inf(1)), 1) {
		t.Error("special values")
	}
}

// A triangular sample stays inside [min, max] even when rounding would step
// outside: with min = mode = 1e-300 s and the smallest uniform 2^-53,
// max - sqrt(...) rounds to exactly 0, which would be a zero interarrival
// that D-033 forbids (review of T-020).
func TestTriangularStaysInRange(t *testing.T) {
	lo, hi := 1e-300, 10.0
	s, err := newSampler(model.Distribution{Type: model.DistTriangular, Min: &lo, Mode: &lo, Max: &hi}, model.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &SplitMix64{state: 0x61c8864680b583eb} // 2^64 - golden gamma: next output 0, U = 2^-53
	if got := s.draw(r); got < 1 {
		t.Errorf("draw %d ticks at U=2^-53; min %g s is positive", got, lo)
	}
	q := rand.New(rand.NewPCG(5, 6))
	for range 200_000 {
		a, b, c := q.Float64()*10, q.Float64()*10, q.Float64()*10
		lo, mode, hi := min(a, b, c), a+b+c-min(a, b, c)-max(a, b, c), max(a, b, c)
		if lo == hi {
			continue
		}
		s, err := newSampler(model.Distribution{Type: model.DistTriangular, Min: &lo, Mode: &mode, Max: &hi}, model.Minute, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range []float64{0x1p-53, 1 - 0x1p-53, q.Float64()} {
			if x := s.value(u); x < lo || x > hi {
				t.Fatalf("triangular(%v, %v, %v) at U=%v gave %v", lo, mode, hi, u, x)
			}
		}
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
		"unknown type":        {model.Distribution{Type: "gamma"}, model.Minute, nil},
		// T-048 (D-056, D-075): the validator's ranges, again.
		"lognormal without cv":    {model.Distribution{Type: model.DistLognormal, Mean: &two}, model.Minute, nil},
		"lognormal zero mean":     {model.Distribution{Type: model.DistLognormal, Mean: &zero, CV: &one}, model.Minute, nil},
		"lognormal cv 0":          {model.Distribution{Type: model.DistLognormal, Mean: &two, CV: &zero}, model.Minute, nil},
		"lognormal cv over 10":    {model.Distribution{Type: model.DistLognormal, Mean: &two, CV: &big}, model.Minute, nil},
		"lognormal mean over cap": {model.Distribution{Type: model.DistLognormal, Mean: &big, CV: &one}, model.Day, nil},
		"lognormal infinite mean": {model.Distribution{Type: model.DistLognormal, Mean: &inf, CV: &one}, model.Minute, nil},
		"weibull without scale":   {model.Distribution{Type: model.DistWeibull, Shape: &two}, model.Minute, nil},
		"weibull zero scale":      {model.Distribution{Type: model.DistWeibull, Shape: &two, Scale: &zero}, model.Minute, nil},
		"weibull shape 0":         {model.Distribution{Type: model.DistWeibull, Shape: &zero, Scale: &two}, model.Minute, nil},
		"weibull shape over 20":   {model.Distribution{Type: model.DistWeibull, Shape: &big, Scale: &two}, model.Minute, nil},
		"weibull scale over cap":  {model.Distribution{Type: model.DistWeibull, Shape: &two, Scale: &big}, model.Day, nil},
		"unknown unit":            {model.Distribution{Type: model.DistConstant, Value: &one}, "week", nil},
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

// The numerical profile allows only correctly rounded or exact math
// functions in engine code (sqrt, round, floor, frexp, ...). Platform
// transcendentals such as math.Log differ between GOARCHs (amd64 and s390x
// use assembly), and on amd64 a swap to math.Log would pass every other test,
// so the sources are scanned for them.
func TestNoPlatformTranscendentals(t *testing.T) {
	banned := regexp.MustCompile(`\bmath\.(Log|Log1p|Log2|Log10|Exp|Exp2|Expm1|Pow|Pow10|Sin|Cos|Tan|Asin|Acos|Atan|Atan2|Sinh|Cosh|Tanh|Gamma|Lgamma|Erf|Erfc|Erfinv|Cbrt|Hypot|FMA)\b`)
	files, err := filepath.Glob(filepath.Join(contracttest.Root(t), "internal/simulation/engine/*.go"))
	if err != nil || len(files) == 0 {
		t.Fatal(err, files)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		for i, line := range strings.Split(string(contracttest.ReadFile(t, f)), "\n") {
			if code, _, _ := strings.Cut(line, "//"); banned.MatchString(code) {
				t.Errorf("%s:%d uses a platform-dependent math function: %s", filepath.Base(f), i+1, strings.TrimSpace(line))
			}
		}
	}
}
