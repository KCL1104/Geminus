//go:build !race

package engine

// raceEnabled: this test binary is built without the race detector (go
// test, make backend-test). See frozenDifferentialModels.
const raceEnabled = false
