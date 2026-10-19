package aiapproval

import "testing"

// RecordForTest records id for the duration of t. It is compiled only
// into this package's tests, so only the tests in this directory can
// approve live AI, never production code or another package's tests.
func RecordForTest(t *testing.T, id string) {
	t.Helper()
	recorded[id] = true
	t.Cleanup(func() { delete(recorded, id) })
}
