package model

import (
	"fmt"
	"math"
)

// MaxDurationTicks is the largest duration any input may quantize to: 365 days
// of 1 ms ticks (D-005 horizon cap, applied to every duration by D-029).
const MaxDurationTicks int64 = 365 * 24 * 60 * 60 * 1000

// unitMillis maps every TimeUnit to ticks; a ModelTimeUnit converts with TimeUnit(u).
var unitMillis = map[TimeUnit]int64{
	Millisecond: 1, Second: 1000, Minute: 60_000, Hour: 3_600_000, Day: 86_400_000,
}

// Millis returns the ticks in one unit, and false for an unknown unit.
func (u TimeUnit) Millis() (int64, bool) {
	ms, ok := unitMillis[u]
	return ms, ok
}

// Ticks quantizes a non-negative duration to 1 ms ticks with the engine
// numerical profile (docs/SIMULATION_SPEC.md §2): one IEEE-754 binary64
// multiplication by the unit's milliseconds, then round half up. A positive
// value that rounds to 0 becomes 1 tick, so only an exact 0 is zero-time
// (D-033). Changing this function changes config hashes and engine results,
// so it requires a new engine_version.
func Ticks(value float64, unit TimeUnit) (int64, error) {
	ms, ok := unit.Millis()
	switch {
	case !ok:
		return 0, fmt.Errorf("unknown time unit %q", unit)
	case math.IsNaN(value) || math.IsInf(value, 0):
		return 0, fmt.Errorf("duration %g is not finite", value)
	case value < 0:
		return 0, fmt.Errorf("duration %g is negative", value)
	}
	// math.Round rounds half away from zero, which is half up for x >= 0, and
	// unlike math.Floor(x+0.5) it is exact for every binary64 input.
	t := math.Round(value * float64(ms))
	if t > float64(MaxDurationTicks) {
		return 0, fmt.Errorf("%g %s exceeds the 365 day duration limit", value, unit)
	}
	if t == 0 && value > 0 {
		return 1, nil
	}
	return int64(t), nil
}
