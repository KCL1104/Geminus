package proposals

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/kcl1104/geminus/internal/model/validate"
)

func fixture(t *testing.T, name string) map[string]jsontext.Value {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "contracts", name))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]jsontext.Value
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// D-062 (c), H-003 review L2: proposal.schema.json holds an invalid
// proposal to its validation and a failed one to its error, as 0012 holds
// their rows: neither may carry anything generated. Each member, with a
// value that is valid for a ready proposal, makes the resource invalid.
func TestInvalidAndFailedHoldNothingGenerated(t *testing.T) {
	ready := fixture(t, "valid-ready-proposal.json")
	ready["questions"] = jsontext.Value(`["Which step?"]`)
	ready["dataset_bindings"] = jsontext.Value(`[]`)
	content := []string{"summary", "assumptions", "risk_level", "confidence", "changes", "expected_effects", "questions",
		"patch_hash", "dataset_bindings", "server_risk_level", "provider_risk_claim"}
	for _, c := range []struct {
		fixture string
		members []string
	}{
		{"valid-proposal-invalid.json", content},
		{"valid-proposal-failed.json", append(content, "validation")},
	} {
		for _, member := range c.members {
			doc := fixture(t, c.fixture)
			doc[member] = ready[member]
			if member == "validation" {
				doc[member] = jsontext.Value(`{"valid":false,"issues":[]}`)
			}
			data, err := json.Marshal(doc, json.Deterministic(true))
			if err != nil {
				t.Fatal(err)
			}
			issues, err := validate.Document(data, "proposal.schema.json", "RESPONSE")
			if err != nil {
				t.Fatal(err)
			}
			if len(issues) == 0 {
				t.Errorf("%s with %s matches proposal.schema.json", c.fixture, member)
			}
		}
		// The fixture itself is valid.
		data, _ := json.Marshal(fixture(t, c.fixture))
		if issues, err := validate.Document(data, "proposal.schema.json", "RESPONSE"); err != nil || len(issues) > 0 {
			t.Errorf("%s: %v %v", c.fixture, issues, err)
		}
	}
}
