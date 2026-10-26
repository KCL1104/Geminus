package aispend

import (
	"time"

	"github.com/kcl1104/geminus/internal/ai/spend"
)

// PinDay books every reservation of m to day's UTC day instead of the
// database's, so that each test works on days of its own, far from today,
// and the tests do not see each other's amounts. It is compiled only into
// this package's tests: production code has no way to pick the day (review
// round 1, L3).
func PinDay(m *Meter, day time.Time) *Meter {
	m.day = spend.Day(day)
	return m
}
