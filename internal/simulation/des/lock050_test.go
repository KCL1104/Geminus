package des

import (
	"encoding/json/v2"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model/hashing"
)

// des-go-0.5.0's own regression lock (engine output, not an oracle; ADR-026
// D-047 "新版本另立自己的鎖"; testdata/simulation/engine-golden-digests-
// 0.5.0.json): the batch and setup goldens with an expected result and
// their eight stochastic reference cases BT-S1–BT-S4 and SU-S1–SU-S4. The
// values are the hand-derived ones' (TestBatchGoldenResults,
// TestSetupGoldenResults) and the SimPy reference's; the lock only makes a
// later numerical change visible. The older locks still hold with
// engine_version set back (TestGoldenDigests, TestGoldenDigests020,
// TestGoldenDigests030, TestGoldenDigests040).
func TestGoldenDigests050(t *testing.T) {
	var lock struct {
		EngineVersion string            `json:"engine_version"`
		Digests       map[string]string `json:"digests"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/engine-golden-digests-0.5.0.json")), &lock); err != nil {
		t.Fatal(err)
	}
	if lock.EngineVersion != Version {
		t.Fatalf("lock for %s, engine %s", lock.EngineVersion, Version)
	}
	got := map[string]string{}
	for _, file := range []string{"testdata/simulation/batch-goldens.json", "testdata/simulation/setup-goldens.json"} {
		var doc struct {
			Cases     []goldenCase `json:"cases"`
			Reference struct {
				Cases []goldenCase `json:"cases"`
			} `json:"reference"`
		}
		if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), file)), &doc); err != nil {
			t.Fatal(err)
		}
		for _, c := range append(doc.Cases, doc.Reference.Cases...) {
			if c.Expected.Result == nil && !strings.HasPrefix(c.ID, "BT-S") && !strings.HasPrefix(c.ID, "SU-S") {
				continue
			}
			res := *run(t, &Engine{}, caseInput(t, c)).Result
			res.Reproducibility.ResultDigest = ""
			d, err := hashing.Result(res)
			if err != nil {
				t.Fatal(err)
			}
			got[c.ID] = d
		}
	}
	if len(got) != 28 || !reflect.DeepEqual(got, lock.Digests) {
		data, _ := json.Marshal(got, json.Deterministic(true))
		t.Errorf("digests %s\n locked %v", data, lock.Digests)
	}
}
