//go:build race

package engine

// raceEnabled: this test binary is built with the race detector (go test
// -race, make backend-race). See frozenDifferentialModels.
const raceEnabled = true
