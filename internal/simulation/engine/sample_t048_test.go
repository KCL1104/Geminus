package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
)

// T-048's part of the numerical profile (D-056; D-075): the pinned exp and
// AS241, the lognormal and Weibull samplers, the priority of failures and
// repairs (D-048) and the breakdown streams (D-051), against the
// independent Python port (scripts/engine_reference.py,
// testdata/simulation/engine-vectors.json), bit for bit.

type t048Vectors struct {
	ReplicationSeed struct {
		BaseSeed uint64 `json:"base_seed"`
		Index    uint32 `json:"index"`
	} `json:"replication_seed"`
	Exp struct {
		Seed      string            `json:"seed"`
		Inputs    int               `json:"inputs"`
		Digest    string            `json:"digest"`
		Constants map[string]string `json:"constants"`
		Edges     []struct {
			X   string `json:"x"`
			Exp string `json:"exp"`
		} `json:"edges"`
	} `json:"exp_profile"`
	Ndtri struct {
		Seed      string            `json:"seed"`
		Inputs    int               `json:"inputs"`
		Digest    string            `json:"digest"`
		Constants map[string]string `json:"constants"`
		Edges     []struct {
			P     string `json:"p"`
			Ndtri string `json:"ndtri"`
		} `json:"edges"`
	} `json:"ndtri_profile"`
	Lognormal []struct {
		Mean  float64 `json:"mean"`
		CV    float64 `json:"cv"`
		Mu    string  `json:"mu"`
		Sigma string  `json:"sigma"`
	} `json:"lognormal_params"`
	Breakdowns struct {
		ResourceID    string             `json:"resource_id"`
		Capacity      int                `json:"capacity"`
		Unit          model.TimeUnit     `json:"unit"`
		TimeToFailure model.Distribution `json:"time_to_failure"`
		TimeToRepair  model.Distribution `json:"time_to_repair"`
		Units         []struct {
			Unit     int     `json:"unit"`
			TTF      []int64 `json:"ttf"`
			TTFState string  `json:"ttf_state_after"`
			TTR      []int64 `json:"ttr"`
			TTRState string  `json:"ttr_state_after"`
		} `json:"units"`
	} `json:"breakdown_streams"`
	EventOrder struct {
		Pushes []struct {
			Tick     int64 `json:"tick"`
			Priority uint8 `json:"priority"`
		} `json:"pushes"`
		PopSequence []uint64 `json:"pop_sequence"`
	} `json:"event_order_resource_state"`
}

func loadT048Vectors(t *testing.T) t048Vectors {
	t.Helper()
	var v t048Vectors
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/engine-vectors.json")), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func hexBits(t *testing.T, s string) float64 {
	t.Helper()
	b, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		t.Fatal(err)
	}
	return math.Float64frombits(b)
}

func bitsOf(x float64) string { return fmt.Sprintf("%016x", math.Float64bits(x)) }

// exp is the pinned exponential: its constants by bit pattern, its edges
// (overflow, underflow, the near-zero shortcut, subnormal results) and 10^6
// inputs from near 0 to ±512 bit for bit like the Python port.
func TestExpProfile(t *testing.T) {
	p := loadT048Vectors(t).Exp
	constants := map[string]float64{"ln2_hi": expLn2Hi, "ln2_lo": expLn2Lo, "log2e": expLog2e, "overflow": expOverflow,
		"underflow": expUnderflow, "near_zero": expNearZero, "p1": expP1, "p2": expP2, "p3": expP3, "p4": expP4, "p5": expP5}
	if len(p.Constants) != len(constants) || p.Inputs < 1_000_000 || len(p.Edges) < 20 {
		t.Fatalf("exp profile: %d constants, %d inputs, %d edges", len(p.Constants), p.Inputs, len(p.Edges))
	}
	for name, v := range constants {
		if got := bitsOf(v); got != p.Constants[name] {
			t.Errorf("constant %s = %s, reference %s", name, got, p.Constants[name])
		}
	}
	for _, e := range p.Edges {
		x := hexBits(t, e.X)
		if got := bitsOf(exp(x)); got != e.Exp {
			t.Errorf("exp(%v) bits %s, reference %s", x, got, e.Exp)
		}
	}
	seed, err := strconv.ParseUint(p.Seed, 16, 64)
	if err != nil {
		t.Fatal(err)
	}
	r := NewSplitMix64(seed)
	h := sha256.New()
	var buf [8]byte
	for i := range p.Inputs {
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(exp(float64((r.Uniform()-0.5)*math.Ldexp(1, i%11)))))
		h.Write(buf[:])
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != p.Digest {
		t.Errorf("exp digest %s, reference %s", got, p.Digest)
	}
}

// bigExp is e^x to 256 bits: e^(x/2^24) by its Taylor series, squared 24
// times (|x/2^24| < 5e-5, so 30 terms are far more than enough and the
// squarings lose a few bits of 256).
func bigExp(x float64) *big.Float {
	const prec = 256
	r := new(big.Float).SetPrec(prec).SetFloat64(x)
	r.SetMantExp(r, -24)
	sum, term, n := new(big.Float).SetPrec(prec).SetInt64(1), new(big.Float).SetPrec(prec).SetInt64(1), new(big.Float).SetPrec(prec)
	for i := int64(1); i < 30; i++ {
		term.Mul(term, r)
		term.Quo(term, n.SetInt64(i))
		sum.Add(sum, term)
	}
	for range 24 {
		sum.Mul(sum, sum)
	}
	return sum
}

// The pinned exponential is a correct exponential: less than one ulp from
// the true e^x (computed to 256 bits; the bound Go documents for the
// algorithm it ports) wherever the result is a normal number, exact at 0,
// and +Inf, 0 or NaN at the special values. math.Exp is no reference: on
// amd64 it is assembly with its own last-bit errors (exp(-493.8360700786439)
// is one ulp above the correctly rounded value, the pinned one one ulp
// below, both less than one ulp from the truth).
func TestExpAccuracy(t *testing.T) {
	q := rand.New(rand.NewPCG(9, 10))
	n := 300_000
	if raceEnabled {
		n = 30_000
	}
	for range n {
		x := (q.Float64() - 0.5) * 1420
		got := exp(x)
		truth := bigExp(x)
		cr, _ := truth.Float64()
		if cr < 0x1p-1022 || math.IsInf(cr, 0) {
			continue
		}
		ulp := math.Nextafter(cr, math.Inf(1)) - cr
		diff := new(big.Float).SetPrec(256).Sub(new(big.Float).SetPrec(256).SetFloat64(got), truth)
		if diff.Abs(diff).Cmp(new(big.Float).SetFloat64(ulp)) >= 0 {
			t.Fatalf("exp(%v) = %v, true value %s (correctly rounded %v)", x, got, truth.Text('g', 25), cr)
		}
	}
	if exp(0) != 1 || exp(1000) != math.Inf(1) || exp(-1000) != 0 || !math.IsNaN(exp(math.NaN())) || exp(math.Inf(-1)) != 0 {
		t.Error("special values")
	}
}

// ndtri is AS241: its 48 constants by bit pattern, its edges (both ends of
// SplitMix64's uniform, 0.5, the branch boundaries |q| = 0.425 and r = 5)
// and 10^6 uniforms bit for bit; and it inverts the normal CDF.
func TestNdtriProfile(t *testing.T) {
	p := loadT048Vectors(t).Ndtri
	if len(p.Constants) != 48 || p.Inputs < 1_000_000 || len(p.Edges) < 15 {
		t.Fatalf("ndtri profile: %d constants, %d inputs, %d edges", len(p.Constants), p.Inputs, len(p.Edges))
	}
	for name, cs := range map[string]*[8]float64{"a": &as241A, "b": &as241B, "c": &as241C, "d": &as241D, "e": &as241E, "f": &as241F} {
		for i, v := range cs {
			if got := bitsOf(v); got != p.Constants[name+strconv.Itoa(i)] {
				t.Errorf("constant %s%d = %s, reference %s", name, i, got, p.Constants[name+strconv.Itoa(i)])
			}
		}
	}
	for _, e := range p.Edges {
		u := hexBits(t, e.P)
		if got := bitsOf(ndtri(u)); got != e.Ndtri {
			t.Errorf("ndtri(%v) bits %s, reference %s", u, got, e.Ndtri)
		}
	}
	seed, err := strconv.ParseUint(p.Seed, 16, 64)
	if err != nil {
		t.Fatal(err)
	}
	r := NewSplitMix64(seed)
	h := sha256.New()
	var buf [8]byte
	for range p.Inputs {
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(ndtri(r.Uniform())))
		h.Write(buf[:])
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != p.Digest {
		t.Errorf("ndtri digest %s, reference %s", got, p.Digest)
	}
	// Φ(ndtri(p)) = p to about 1e-14 relative, on the tail's side.
	q := rand.New(rand.NewPCG(11, 12))
	for range 500_000 {
		u := q.Float64()
		if u == 0 {
			continue
		}
		back := 0.5 * math.Erfc(-ndtri(u)/math.Sqrt2)
		if tail := min(u, 1-u); math.Abs(back-u) > 1e-13*tail {
			t.Fatalf("Φ(ndtri(%v)) = %v", u, back)
		}
	}
}

// D-056's μ and σ of each lognormal case, by bit pattern.
func TestLognormalParameters(t *testing.T) {
	cases := loadT048Vectors(t).Lognormal
	if len(cases) < 5 {
		t.Fatalf("%d lognormal cases", len(cases))
	}
	for _, c := range cases {
		mean, cv := c.Mean, c.CV
		s, err := newSampler(model.Distribution{Type: model.DistLognormal, Mean: &mean, CV: &cv}, model.Minute, nil)
		if err != nil {
			t.Fatal(err)
		}
		if bitsOf(s.mu) != c.Mu || bitsOf(s.sigma) != c.Sigma {
			t.Errorf("lognormal(%v, %v): μ %s σ %s, reference %s %s", mean, cv, bitsOf(s.mu), bitsOf(s.sigma), c.Mu, c.Sigma)
		}
	}
}

// D-051: unit u's TTF and TTR come from the streams (resource ID, ttf/<u>)
// and (resource ID, ttr/<u>), each draw as the Python reference draws it.
func TestBreakdownStreamVectors(t *testing.T) {
	v := loadT048Vectors(t)
	b := v.Breakdowns
	rep := ReplicationSeed(v.ReplicationSeed.BaseSeed, v.ReplicationSeed.Index)
	ttf, err := newSampler(b.TimeToFailure, b.Unit, nil)
	if err != nil {
		t.Fatal(err)
	}
	ttr, err := newSampler(b.TimeToRepair, b.Unit, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Units) != b.Capacity || b.Capacity < 3 {
		t.Fatalf("%d units", len(b.Units))
	}
	for _, u := range b.Units {
		for _, c := range []struct {
			purpose string
			s       *sampler
			want    []int64
			state   string
		}{{"ttf", ttf, u.TTF, u.TTFState}, {"ttr", ttr, u.TTR, u.TTRState}} {
			r := Stream(rep, b.ResourceID, c.purpose+"/"+strconv.Itoa(u.Unit))
			var got []int64
			for range c.want {
				got = append(got, c.s.draw(r))
			}
			if !slices.Equal(got, c.want) || fmt.Sprintf("%016x", r.state) != c.state {
				t.Errorf("unit %d %s: %v (state %016x), reference %v (%s)", u.Unit, c.purpose, got, r.state, c.want, c.state)
			}
		}
	}
}

// D-048: priority 15 sits between arrivals (10) and the dispatch (20).
func TestEventOrderWithResourceState(t *testing.T) {
	v := loadT048Vectors(t).EventOrder
	if PriorityResourceState != 15 || len(v.Pushes) == 0 {
		t.Fatalf("priority %d, %d pushes", PriorityResourceState, len(v.Pushes))
	}
	var q eventQueue
	for _, p := range v.Pushes {
		q.schedule(event{Tick: p.Tick, Priority: p.Priority})
	}
	var got []uint64
	for e, ok := q.next(); ok; e, ok = q.next() {
		got = append(got, e.Seq)
	}
	if !slices.Equal(got, v.PopSequence) {
		t.Errorf("pop order %v, reference %v", got, v.PopSequence)
	}
}

// The lognormal and Weibull samplers have the right distribution (D-056:
// "KS 檢定與前兩階動差"; tolerances fixed before the run): over 10^5
// unquantized draws per case, the Kolmogorov–Smirnov distance to the
// exact CDF (computed with the platform's math, in this test only) is
// below 1.95/sqrt(n) (alpha about 0.001), and the sample mean and variance
// are within 6 standard errors of the exact moments (when the fourth
// moment, and so the variance's standard error, is finite and modest).
func TestLognormalWeibullStatistics(t *testing.T) {
	const n = 100_000
	type tc struct {
		d        model.Distribution
		cdf      func(x float64) float64
		mean, sd float64 // of the distribution
		kurt     float64 // E[(X-μ)^4]/σ^4, for the variance's standard error; 0: skip the variance
	}
	ln := func(mean, cv float64) tc {
		s2 := math.Log1p(cv * cv)
		mu := math.Log(mean) - s2/2
		sd := mean * cv
		k := math.Exp(4*s2) + 2*math.Exp(3*s2) + 3*math.Exp(2*s2) - 3
		if cv > 0.6 {
			k = 0
		}
		return tc{model.Distribution{Type: model.DistLognormal, Mean: &mean, CV: &cv},
			func(x float64) float64 { return 0.5 * math.Erfc(-(math.Log(x)-mu)/(math.Sqrt(s2)*math.Sqrt2)) }, mean, sd, k}
	}
	wb := func(shape, scale float64) tc {
		g := func(k float64) float64 { return math.Gamma(1 + k/shape) }
		mean := scale * g(1)
		v := scale * scale * (g(2) - g(1)*g(1))
		m4 := math.Pow(scale, 4) * (g(4) - 4*g(3)*g(1) + 6*g(2)*g(1)*g(1) - 3*math.Pow(g(1), 4))
		k := m4 / (v * v)
		if shape < 1 {
			k = 0
		}
		return tc{model.Distribution{Type: model.DistWeibull, Shape: &shape, Scale: &scale},
			func(x float64) float64 { return -math.Expm1(-math.Pow(x/scale, shape)) }, mean, math.Sqrt(v), k}
	}
	cases := map[string]tc{
		"lognormal 30, 0.5": ln(30, 0.5), "lognormal 4, 2": ln(4, 2), "lognormal 1, 0.05": ln(1, 0.05),
		"weibull 1.5, 8": wb(1.5, 8), "weibull 0.5, 3": wb(0.5, 3), "weibull 20, 30": wb(20, 30), "weibull 1, 48": wb(1, 48),
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s, err := newSampler(c.d, model.Minute, nil)
			if err != nil {
				t.Fatal(err)
			}
			r := Stream(ReplicationSeed(48, 0), name, PurposeService)
			x := make([]float64, n)
			sum := 0.0
			for i := range x {
				x[i] = s.value(r.Uniform())
				sum += x[i]
			}
			slices.Sort(x)
			ks := 0.0
			for i, v := range x {
				f := c.cdf(v)
				ks = max(ks, math.Abs(f-float64(i)/n), math.Abs(float64(i+1)/n-f))
			}
			if ks > 1.95/math.Sqrt(n) {
				t.Errorf("KS distance %v", ks)
			}
			mean := sum / n
			if math.Abs(mean-c.mean) > 6*c.sd/math.Sqrt(n) {
				t.Errorf("mean %v, exact %v (sd %v)", mean, c.mean, c.sd)
			}
			if c.kurt > 0 {
				ss := 0.0
				for _, v := range x {
					ss += (v - mean) * (v - mean)
				}
				variance := ss / (n - 1)
				if se := c.sd * c.sd * math.Sqrt((c.kurt-1)/n); math.Abs(variance-c.sd*c.sd) > 6*se {
					t.Errorf("variance %v, exact %v (se %v)", variance, c.sd*c.sd, se)
				}
			}
		})
	}
}

// Every lognormal and Weibull sample is a valid duration of at least one
// tick (D-056: never zero-time), whatever the parameters in range, and the
// tail beyond the cap saturates (D-038 (b)).
func TestLognormalWeibullBounds(t *testing.T) {
	tiny, one, ten, big, small, hi := 1e-300, 1.0, 10.0, 365.0, 0.1, 20.0
	cases := map[string]model.Distribution{
		"lognormal tiny mean": {Type: model.DistLognormal, Mean: &tiny, CV: &one},
		"lognormal cv 10":     {Type: model.DistLognormal, Mean: &one, CV: &ten},
		"lognormal 365 days":  {Type: model.DistLognormal, Mean: &big, CV: &ten},
		"weibull shape 0.1":   {Type: model.DistWeibull, Shape: &small, Scale: &big},
		"weibull shape 20":    {Type: model.DistWeibull, Shape: &hi, Scale: &tiny},
	}
	for name, d := range cases {
		s, err := newSampler(d, model.Day, nil)
		if err != nil {
			t.Fatal(name, err)
		}
		r := NewSplitMix64(uint64(len(name)))
		for _, u := range []float64{0x1p-53, 1 - 0x1p-53} {
			if x := s.ticksOf(s.value(u)); x < 1 || x > beyondCap {
				t.Fatalf("%s at U=%v: %d ticks", name, u, x)
			}
		}
		for range 100_000 {
			if x := s.draw(r); x < 1 || x > beyondCap {
				t.Fatalf("%s: sample %d out of bounds", name, x)
			}
		}
	}
}
