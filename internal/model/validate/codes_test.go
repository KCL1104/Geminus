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

// The issue codes declared here, in internal/model/patch and in the
// server's review of AI proposals (internal/ai/review, T-025) are the
// contract's catalogue (common.schema.json#/$defs/ValidationCode), which the
// UI localizes (REQ-022): a new code without an entry fails here. The
// catalogue's other codes are the domain packs': DOMAIN_MISMATCH, every
// pack's, checked by the packs' tests, and the codes a pack declares in
// its codes.go (the electronics line's, T-051). The review's and the
// packs' files are read, not imported: the kernel's tests depend on no
// application layer or concrete pack.
func TestCodesMatchContract(t *testing.T) {
	constant := regexp.MustCompile(`(?m)^\t\w+\s*=\s*"([A-Z][A-Z0-9_]+)"`)
	var declared []string
	packs, err := filepath.Glob(filepath.Join("..", "..", "domainpack", "*", "codes.go"))
	if err != nil || len(packs) == 0 {
		t.Fatalf("no pack declares codes in codes.go: %v", err)
	}
	for _, f := range append([]string{"codes.go", filepath.Join("..", "patch", "patch.go"), filepath.Join("..", "..", "ai", "review", "codes.go")}, packs...) {
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
	if len(declared) < 26 || !slices.Equal(declared, want) {
		t.Errorf("declared codes %v, contract %v", declared, want)
	}
}
