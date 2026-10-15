package model_test

import (
	"math"
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
)

// docs/SIMULATION_SPEC.md §2: ×unit ms, round half up, positive sub-tick → 1.
func TestTicks(t *testing.T) {
	for _, c := range []struct {
		v    float64
		unit model.TimeUnit
		want int64
	}{
		{0, model.Minute, 0},
		{1, model.Millisecond, 1},
		{0.5, model.Millisecond, 1},
		{1.5, model.Millisecond, 2},
		{2.5, model.Millisecond, 3}, // half up, not half to even
		{2.4999999999999996, model.Millisecond, 2},
		{0.49999999999999994, model.Millisecond, 1}, // rounds to 0, clamped: only an exact 0 is zero-time
		{0.0001, model.Second, 1},
		{math.SmallestNonzeroFloat64, model.Day, 1},
		{0.1, model.Minute, 6000},
		{1.5, model.Hour, 5_400_000},
		{120, model.Minute, 7_200_000},
		{365, model.Day, model.MaxDurationTicks},
		{float64(model.MaxDurationTicks) + 0.4, model.Millisecond, model.MaxDurationTicks},
	} {
		got, err := model.Ticks(c.v, c.unit)
		if err != nil || got != c.want {
			t.Errorf("Ticks(%v, %s) = %d, %v; want %d", c.v, c.unit, got, err, c.want)
		}
	}
	for _, c := range []struct {
		v    float64
		unit model.TimeUnit
	}{
		{-1, model.Second},
		{math.Copysign(0, -1) - math.SmallestNonzeroFloat64, model.Second},
		{math.NaN(), model.Second},
		{math.Inf(1), model.Second},
		{365.00001, model.Day},
		{float64(model.MaxDurationTicks) + 0.5, model.Millisecond},
		{1, "week"},
		{1, ""},
	} {
		if got, err := model.Ticks(c.v, c.unit); err == nil {
			t.Errorf("Ticks(%v, %q) = %d, want an error", c.v, c.unit, got)
		}
	}
}

// Ticks equals an exact big-number evaluation of the same rule on random inputs.
func TestTicksMatchesExactRounding(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	units := []model.TimeUnit{model.Millisecond, model.Second, model.Minute, model.Hour, model.Day}
	for range 200_000 {
		unit := units[r.IntN(len(units))]
		ms, _ := unit.Millis()
		v := r.Float64() * float64(model.MaxDurationTicks) / float64(ms)
		if r.IntN(4) == 0 {
			v = math.Floor(v*float64(ms)*2) / 2 / float64(ms) // near half ticks, where rounding direction matters
		}
		product := v * float64(ms)                               // the one binary64 rounding the spec allows
		exact := new(big.Float).SetPrec(256).SetFloat64(product) // wide enough that +0.5 is exact
		exact.Add(exact, big.NewFloat(0.5))
		want, _ := exact.Int(nil) // truncates toward zero == floor for x >= 0
		if want.Sign() == 0 && v > 0 {
			want.SetInt64(1)
		}
		got, err := model.Ticks(v, unit)
		if err != nil || got != want.Int64() {
			t.Fatalf("Ticks(%v, %s) = %d, %v; exact %s", v, unit, got, err, want)
		}
	}
}
