package engine_test

import (
	"encoding/json/v2"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/simulation/engine"
)

type rngVectors struct {
	Status           string   `json:"status"`
	InitialState     string   `json:"splitmix64_initial_state_hex"`
	Outputs          []string `json:"outputs_hex"`
	BaseSeed         uint64   `json:"base_seed"`
	ReplicationSeeds []struct {
		Index uint32 `json:"index"`
		Seed  string `json:"seed_hex_le_value"`
	} `json:"replication_seed_vectors"`
	StreamReplicationSeed string `json:"stream_replication_seed"`
	Streams               []struct {
		NodeID   string    `json:"node_id"`
		Purpose  string    `json:"purpose"`
		Seed     string    `json:"seed_hex"`
		Uniforms []float64 `json:"first_uniforms"`
	} `json:"stream_seed_vectors"`
}

func hex64(t *testing.T, s string) uint64 {
	t.Helper()
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TEST-019 (RNG part): the Go profile reproduces the vectors that the
// independent Python reference (scripts/check_reference_math.py) checks.
func TestRNGVectors(t *testing.T) {
	var v rngVectors
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/rng-vectors.json")), &v); err != nil {
		t.Fatal(err)
	}
	r := engine.NewSplitMix64(hex64(t, v.InitialState))
	for i, want := range v.Outputs {
		if got := fmt.Sprintf("%016x", r.Next()); got != want {
			t.Errorf("output %d: %s, want %s", i, got, want)
		}
	}
	for _, rs := range v.ReplicationSeeds {
		if got := fmt.Sprintf("%016x", engine.ReplicationSeed(v.BaseSeed, rs.Index)); got != rs.Seed {
			t.Errorf("replication %d seed %s, want %s", rs.Index, got, rs.Seed)
		}
	}
	rep := hex64(t, v.StreamReplicationSeed)
	if len(v.Streams) < 5 {
		t.Fatal("stream vectors missing")
	}
	for _, s := range v.Streams {
		if got := fmt.Sprintf("%016x", engine.StreamSeed(rep, s.NodeID, s.Purpose)); got != s.Seed {
			t.Errorf("stream %q/%q seed %s, want %s", s.NodeID, s.Purpose, got, s.Seed)
		}
		g := engine.Stream(rep, s.NodeID, s.Purpose)
		for i, want := range s.Uniforms {
			if got := g.Uniform(); got != want {
				t.Errorf("stream %q/%q uniform %d: %v, want %v", s.NodeID, s.Purpose, i, got, want)
			}
		}
	}
}

func TestUniformBounds(t *testing.T) {
	// The extreme outputs map strictly inside (0,1).
	lo := (float64(uint64(0)>>12) + 0.5) / (1 << 52)
	hi := (float64(uint64(math.MaxUint64)>>12) + 0.5) / (1 << 52)
	if !(lo > 0 && hi < 1) {
		t.Fatalf("bounds %v %v", lo, hi)
	}
	r := engine.NewSplitMix64(0)
	for range 1_000_000 {
		if u := r.Uniform(); !(u > 0 && u < 1) {
			t.Fatalf("uniform %v outside (0,1)", u)
		}
	}
}

func TestStreamsAreIndependentAndLengthPrefixed(t *testing.T) {
	rep := engine.ReplicationSeed(7, 0)
	seen := map[uint64]string{}
	for _, node := range []string{"a", "ab", "b", ""} {
		for _, purpose := range []string{engine.PurposeArrival, engine.PurposeService, engine.PurposeDelay, engine.PurposeRouting, engine.PurposeTrace, "c", "bc"} {
			s := engine.StreamSeed(rep, node, purpose)
			if prev, dup := seen[s]; dup {
				t.Errorf("streams %s and %s/%s share a seed", prev, node, purpose)
			}
			seen[s] = node + "/" + purpose
		}
	}
	if engine.StreamSeed(rep, "ab", "c") == engine.StreamSeed(rep, "a", "bc") {
		t.Error("length prefixes do not separate (ab,c) from (a,bc)")
	}
	if engine.ReplicationSeed(7, 0) == engine.ReplicationSeed(7, 1) || engine.ReplicationSeed(7, 0) == engine.ReplicationSeed(8, 0) {
		t.Error("replication seeds collide")
	}
}
