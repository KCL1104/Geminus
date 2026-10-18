package validate_test

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
)

// The issue codes declared here and in internal/model/patch are the
// contract's catalogue (common.schema.json#/$defs/ValidationCode), which the
// UI localizes (REQ-022): a new code without an entry fails here. The
// catalogue's only other code is the P0 pack's DOMAIN_MISMATCH, checked by
// that pack's tests.
func TestCodesMatchContract(t *testing.T) {
	constant := regexp.MustCompile(`(?m)^\t\w+\s*=\s*"([A-Z][A-Z0-9_]+)"`)
	var declared []string
	for _, f := range []string{"codes.go", filepath.Join("..", "patch", "patch.go")} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range constant.FindAllSubmatch(src, -1) {
			declared = append(declared, string(m[1]))
		}
	}
	var doc struct {
		Defs struct {
			ValidationCode struct {
				Enum []string `json:"enum"`
			} `json:"ValidationCode"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "schemas/common.schema.json")), &doc); err != nil {
		t.Fatal(err)
	}
	want := slices.DeleteFunc(slices.Clone(doc.Defs.ValidationCode.Enum), func(c string) bool { return c == "DOMAIN_MISMATCH" })
	slices.Sort(declared)
	slices.Sort(want)
	if len(declared) < 20 || !slices.Equal(declared, want) {
		t.Errorf("declared codes %v, contract %v", declared, want)
	}
}
