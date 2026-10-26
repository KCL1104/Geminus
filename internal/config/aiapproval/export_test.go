package aiapproval

import "testing"

// RecordForTest records r under r.ID for the duration of t, in place of
// what the build records under that ID. It is compiled only into this
// package's tests, so only the tests in this directory can approve live
// AI, never production code or another package's tests.
func RecordForTest(t *testing.T, r Record) {
	t.Helper()
	before, had := recorded[r.ID]
	recorded[r.ID] = r.clone()
	t.Cleanup(func() {
		if had {
			recorded[r.ID] = before
		} else {
			delete(recorded, r.ID)
		}
	})
}
