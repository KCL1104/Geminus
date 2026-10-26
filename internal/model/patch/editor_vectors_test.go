package patch

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"path/filepath"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// T-015: the 2D editor's patches. testdata/editor/patch-vectors.json is
// written by apps/web/tests/editor-vectors.test.ts (which fails when the
// editor's ops change without it): each vector names a base IR, the ops one
// editor action or one save of several produces, and the model hash (D-006)
// of the IR the editor then shows. The server's manual-draft policy must
// accept every patch (D-014, D-020: each change or removal right after the
// test of its element) and build that IR, schema-valid. The editor's
// sample line (apps/web/app/ui-gallery/editor/sample-line.ts) is a model
// the validator finds no error in.
func TestEditorVectors(t *testing.T) {
	var file struct {
		Bases   map[string]jsontext.Value `json:"bases"`
		Vectors []struct {
			Name     string         `json:"name"`
			Base     string         `json:"base"`
			Ops      jsontext.Value `json:"ops"`
			Expected string         `json:"expected_model_hash"`
		} `json:"vectors"`
	}
	data := contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "editor", "patch-vectors.json"))
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Vectors) < 20 {
		t.Fatalf("%d vectors; the editor writes at least 20", len(file.Vectors))
	}
	for _, name := range []string{"sample", "sample_placed"} {
		res, err := validate.IR(file.Bases[name])
		if err != nil {
			t.Fatal(err)
		}
		if res.IR == nil || !res.Valid() {
			t.Errorf("the sample line (%s) has errors: %v", name, res.Issues)
		}
	}
	for _, v := range file.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			base, ok := file.Bases[v.Base]
			if !ok {
				t.Fatalf("no base %q", v.Base)
			}
			r := mustApply(t, base, string(v.Ops), ManualDraft)
			if !r.Applied() {
				t.Fatalf("the server refuses the editor's patch: %v", r.PatchIssues)
			}
			if r.Validation.IR == nil {
				t.Fatalf("the patched draft is not schema-valid: %v", r.Validation.Issues)
			}
			got, err := hashing.Model(r.Candidate)
			if err != nil {
				t.Fatal(err)
			}
			if got != v.Expected {
				t.Errorf("the server builds %s\n%s\nthe editor shows %s", got, r.Candidate, v.Expected)
			}
		})
	}
}
