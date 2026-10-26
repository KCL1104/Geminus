package engine

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"maps"
	"path/filepath"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
)

// observationDigest050 is observationDigestAt's encoding with the batch
// number as a ninth word (des-go-0.5.0: ObsBatchStart is kind 17, ObsSetup
// kind 18, Observation.Batch new; T-050).
func observationDigest050(all []Observation) string {
	h := sha256.New()
	var buf [72]byte
	for _, o := range all {
		for i, v := range []uint64{uint64(o.Kind), uint64(o.Tick), o.Seq, o.Entity, uint64(int64(o.Node)), uint64(int64(o.Resource)), uint64(o.Value), uint64(int64(o.At)), o.Batch} {
			binary.LittleEndian.PutUint64(buf[8*i:], v)
		}
		h.Write(buf[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// lock050Cases are the models des-go-0.5.0's observation lock pins: every
// case of batch-goldens.json and setup-goldens.json the validator accepts
// (all but G-BT13) and their stochastic reference cases, replication 0.
func lock050Cases(t *testing.T) []calendarCase {
	t.Helper()
	var out []calendarCase
	for _, file := range []string{"testdata/simulation/batch-goldens.json", "testdata/simulation/setup-goldens.json"} {
		var doc struct {
			Cases     []calendarCase `json:"cases"`
			Reference struct {
				Cases []calendarCase `json:"cases"`
			} `json:"reference"`
		}
		if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), file)), &doc); err != nil {
			t.Fatal(err)
		}
		for _, c := range append(doc.Cases, doc.Reference.Cases...) {
			if c.Validator == nil {
				out = append(out, c)
			}
		}
	}
	return out
}

// The observation stream of every batch and setup golden the validator
// accepts and of their eight stochastic reference cases (replication 0)
// is pinned (testdata/simulation/kernel-observation-digests-0.5.0.json, a
// regression lock taken from des-go-0.5.0 after the hand-derived goldens
// and the SimPy reference passed, not an oracle). Any change to batches,
// changeovers, their observations or sequence numbers shows up here. The
// older locks hold unchanged.
func TestBatchSetupObservationDigests(t *testing.T) {
	var lock struct {
		EngineVersion string `json:"engine_version"`
		Cases         []struct {
			ID           string `json:"id"`
			Observations int    `json:"observations"`
			Digest       string `json:"sha256"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/kernel-observation-digests-0.5.0.json")), &lock); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	var order []string
	for _, c := range lock050Cases(t) {
		col := &collect{}
		if _, err := RunReplication(context.Background(), compile(t, c.input(t)), 0, NewBudget(10_000_000), col); err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		got[c.ID] = fmt.Sprintf("%d %s", len(col.all), observationDigest050(col.all))
		order = append(order, c.ID)
	}
	want := map[string]string{}
	for _, c := range lock.Cases {
		want[c.ID] = fmt.Sprintf("%d %s", c.Observations, c.Digest)
	}
	if lock.EngineVersion != "des-go-0.5.0" || len(got) != 32 || !maps.Equal(got, want) {
		var rows []string
		for _, id := range order {
			rows = append(rows, fmt.Sprintf("%s=%s", id, got[id]))
		}
		t.Errorf("observation digests\n got  %v\n want %v", rows, want)
	}
}
