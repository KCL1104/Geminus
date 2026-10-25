package calendar

import (
	"math/rand/v2"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
)

// brute is S(t) straight from the definition (D-052): the exception
// containing t if any, else the shift of the period containing t mod
// period, else 0. It shares nothing with Pattern but the inputs.
type brute struct {
	period     int64
	capacity   int
	shifts     []Shift
	exceptions []Exception
}

func (b brute) at(t int64) int {
	for _, e := range b.exceptions {
		if e.Start <= t && t < e.End {
			if e.Open {
				return b.capacity
			}
			return 0
		}
	}
	x := t % b.period
	for _, s := range b.shifts {
		if s.Start <= x && x < s.End {
			return s.Units
		}
	}
	return 0
}

func (b brute) periodic(t int64) int { return brute{b.period, b.capacity, b.shifts, nil}.at(t) }

// randomBrute is a random calendar of a short period (in ticks), with
// shift capacities and sometimes exceptions.
func randomBrute(rng *rand.Rand) brute {
	b := brute{period: 1 + rng.Int64N(40), capacity: 1 + rng.IntN(3)}
	at := int64(0)
	for range 1 + rng.IntN(4) {
		start := at + rng.Int64N(3)
		end := start + 1 + rng.Int64N(6)
		if end > b.period {
			break
		}
		b.shifts = append(b.shifts, Shift{start, end, rng.IntN(b.capacity + 1)})
		at = end
	}
	if len(b.shifts) == 0 {
		b.shifts = []Shift{{0, b.period, b.capacity}}
	}
	at = 0
	for range rng.IntN(4) {
		start := at + rng.Int64N(60)
		end := start + 1 + rng.Int64N(30)
		b.exceptions = append(b.exceptions, Exception{start, end, rng.IntN(2) == 0})
		at = end
	}
	return b
}

func (b brute) pattern(t *testing.T) *Pattern {
	t.Helper()
	p, err := New(b.period, b.capacity, b.shifts, b.exceptions)
	if err != nil {
		t.Fatalf("%+v: %v", b, err)
	}
	return p
}

const span = 400 // ticks examined per random pattern

// Every query against the definition, tick by tick, on random patterns.
func TestPatternAgainstTheDefinition(t *testing.T) {
	rng := rand.New(rand.NewPCG(49, 1))
	for n := range 3000 {
		b := randomBrute(rng)
		p := b.pattern(t)
		on := int64(0)
		for tick := int64(0); tick < span; tick++ {
			if got, want := p.At(tick), b.at(tick); got != want {
				t.Fatalf("pattern %d %+v: S(%d) = %d, want %d", n, b, tick, got, want)
			}
			if got, want := p.Periodic(tick), b.periodic(tick); got != want {
				t.Fatalf("pattern %d: periodic S(%d) = %d, want %d", n, tick, got, want)
			}
			if got := p.OnTicks(tick); got != on {
				t.Fatalf("pattern %d %+v: Θ(%d) = %d, want %d", n, b, tick, got, on)
			}
			if b.at(tick) > 0 {
				on++
			}
			// NextChange: the first b > tick with S(b) != S(b-1), within span.
			want, ok := int64(-1), false
			for x := tick + 1; x < span; x++ {
				if b.at(x) != b.at(x-1) {
					want, ok = x, true
					break
				}
			}
			got, gok := p.NextChange(tick)
			if ok && (!gok || got != want) || !ok && gok && got < span {
				t.Fatalf("pattern %d %+v: NextChange(%d) = %d %v, want %d %v", n, b, tick, got, gok, want, ok)
			}
			for units := 0; units <= b.capacity+1; units++ {
				for _, exc := range []bool{true, false} {
					s := b.at
					if !exc {
						s = b.periodic
					}
					wantAt, wantBelow := int64(-1), int64(-1)
					for x := tick; x < span && (wantAt < 0 || wantBelow < 0); x++ {
						if wantAt < 0 && s(x) >= units {
							wantAt = x
						}
						if wantBelow < 0 && s(x) < units {
							wantBelow = x
						}
					}
					if g, ok := p.NextAtLeast(tick, units, exc); wantAt >= 0 && (!ok || g != wantAt) || wantAt < 0 && ok && g < span {
						t.Fatalf("pattern %d %+v: NextAtLeast(%d, %d, %v) = %d %v, want %d", n, b, tick, units, exc, g, ok, wantAt)
					}
					if g, ok := p.NextBelow(tick, units, exc); wantBelow >= 0 && (!ok || g != wantBelow) || wantBelow < 0 && ok && g < span {
						t.Fatalf("pattern %d %+v: NextBelow(%d, %d, %v) = %d %v, want %d", n, b, tick, units, exc, g, ok, wantBelow)
					}
				}
			}
		}
		// FailureAt: the least t with Θ(t) >= Θ(up) + ttf, by counting.
		for range 20 {
			up, ttf := rng.Int64N(span/2), 1+rng.Int64N(span/4)
			want, count := int64(-1), int64(0)
			for x := up; x < span; x++ {
				if count >= ttf {
					want = x
					break
				}
				if b.at(x) > 0 {
					count++
				}
			}
			got, ok := p.FailureAt(up, ttf, span-1)
			if want >= 0 && (!ok || got != want) || want < 0 && ok {
				t.Fatalf("pattern %d %+v: FailureAt(%d, %d) = %d %v, want %d", n, b, up, ttf, got, ok, want)
			}
		}
	}
}

// NextFit (D-050) against the definition on random bundles of two or three.
func TestNextFitAgainstTheDefinition(t *testing.T) {
	rng := rand.New(rand.NewPCG(49, 2))
	for n := range 3000 {
		var bundle []Need
		var brutes []brute
		var units []int
		for range 2 + rng.IntN(2) {
			b := randomBrute(rng)
			u := 1 + rng.IntN(b.capacity)
			brutes, units = append(brutes, b), append(units, u)
			bundle = append(bundle, Need{b.pattern(t), u})
		}
		for _, exc := range []bool{true, false} {
			from := rng.Int64N(100)
			want := int64(-1)
			for x := from; x < span && want < 0; x++ {
				fit := true
				for i, b := range brutes {
					s := b.at(x)
					if !exc {
						s = b.periodic(x)
					}
					fit = fit && s >= units[i]
				}
				if fit {
					want = x
				}
			}
			got, ok := NextFit(bundle, from, span-1, exc)
			if want >= 0 && (!ok || got != want) || want < 0 && ok {
				t.Fatalf("bundle %d: NextFit(%d, %v) = %d %v, want %d", n, from, exc, got, ok, want)
			}
		}
	}
}

// Hand cases: ADR-026 G-F6's clock (period 10, shift [0, 6), TTF 8 fails at
// 12; repaired at 13, the next at 25), a target reached at a shift's end,
// open and closed exceptions, and the LCM cap.
func TestHandCases(t *testing.T) {
	p, err := New(10, 1, []Shift{{0, 6, 1}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if at, ok := p.FailureAt(0, 8, 100); !ok || at != 12 {
		t.Errorf("G-F6 first failure %d %v, want 12", at, ok)
	}
	if at, ok := p.FailureAt(13, 8, 100); !ok || at != 25 {
		t.Errorf("G-F6 second failure %d %v, want 25", at, ok)
	}
	if at, ok := p.FailureAt(0, 6, 100); !ok || at != 6 {
		t.Errorf("a target at the shift's end: %d %v, want 6 (the end's tick)", at, ok)
	}
	if _, ok := p.FailureAt(0, 8, 11); ok {
		t.Error("a failure after the limit was found")
	}
	if b, ok := p.NextChange(0); !ok || b != 6 {
		t.Errorf("NextChange(0) %d %v", b, ok)
	}
	if b, ok := p.NextChange(6); !ok || b != 10 {
		t.Errorf("NextChange(6) %d %v", b, ok)
	}
	// G-C6: a closed exception [10, 15) on an always-on calendar postpones
	// the failure of TTF 12 from 12 to 17.
	q, err := New(10, 1, []Shift{{0, 10, 1}}, []Exception{{10, 15, false}})
	if err != nil {
		t.Fatal(err)
	}
	if at, ok := q.FailureAt(0, 12, 100); !ok || at != 17 {
		t.Errorf("G-C6 failure %d %v, want 17", at, ok)
	}
	if !(q.At(9) == 1 && q.At(10) == 0 && q.At(15) == 1) || q.AlwaysOn() {
		t.Error("G-C6 pattern")
	}
	if b, ok := q.NextChange(0); !ok || b != 10 {
		t.Errorf("G-C6 NextChange(0) %d %v", b, ok)
	}
	if b, ok := q.NextChange(10); !ok || b != 15 {
		t.Errorf("G-C6 NextChange(10) %d %v", b, ok)
	}
	if _, ok := q.NextChange(15); ok {
		t.Error("G-C6: a change after the exception")
	}
	// G-C8: an open exception [5, 8) extends the shift [0, 5): no change at 5.
	o, err := New(10, 1, []Shift{{0, 5, 1}}, []Exception{{5, 8, true}})
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := o.NextChange(0); !ok || b != 8 {
		t.Errorf("G-C8 NextChange(0) %d %v, want 8", b, ok)
	}
	// A constant pattern never changes.
	if c := Always(3); !c.Constant() || !c.AlwaysOn() || c.At(12345) != 3 {
		t.Error("Always")
	}
	if l, ok := LCM(432000*60000, 10080*60000, model.MaxDurationTicks); ok {
		t.Errorf("300 days and 7 days: %d", l)
	}
	if l, ok := LCM(7*60000, 11*60000, model.MaxDurationTicks); !ok || l != 77*60000 {
		t.Errorf("LCM(7, 11 min) %d %v", l, ok)
	}
}

// New refuses what the validator's shape rules refuse.
func TestNewRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		period int64
		shifts []Shift
		exc    []Exception
	}{
		"overlap":      {10, []Shift{{0, 6, 1}, {5, 8, 1}}, nil},
		"empty":        {10, []Shift{{3, 3, 1}}, nil},
		"past period":  {10, []Shift{{0, 11, 1}}, nil},
		"too many":     {10, []Shift{{0, 5, 2}}, nil},
		"exc overlap":  {10, []Shift{{0, 5, 1}}, []Exception{{0, 10, true}, {9, 12, false}}},
		"exc too late": {10, []Shift{{0, 5, 1}}, []Exception{{0, model.MaxDurationTicks + 1, true}}},
		"no period":    {0, []Shift{{0, 5, 1}}, nil},
	} {
		if _, err := New(c.period, 1, c.shifts, c.exc); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
