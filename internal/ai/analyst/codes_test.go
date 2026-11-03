package analyst_test

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/analyst"
)

// H-086 (R): the one re-ask of a refused explanation lists each issue with
// the fixed rule its code stands for (aicontext.CheckRule). Every code the
// check declares is in Codes, and has its rule; the follow-up knows no
// other code.
func TestEveryCheckCodeHasItsRule(t *testing.T) {
	src, err := os.ReadFile("analyst.go")
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, m := range regexp.MustCompile(`(?m)^\s+[A-Z][A-Za-z]+\s+=\s+"(ANALYSIS_[A-Z_]+)"$`).FindAllStringSubmatch(string(src), -1) {
		declared = append(declared, m[1])
	}
	codes, rules := slices.Clone(analyst.Codes), aicontext.CheckCodes()
	slices.Sort(declared)
	slices.Sort(codes)
	slices.Sort(rules)
	if len(declared) != 10 || !slices.Equal(declared, codes) || !slices.Equal(codes, rules) {
		t.Errorf("declared %v\nCodes %v\nrules %v", declared, codes, rules)
	}
	for _, c := range analyst.Codes {
		if r, ok := aicontext.CheckRule(c); !ok || len(r) < 40 {
			t.Errorf("%s: rule %q", c, r)
		}
	}
	if _, ok := aicontext.CheckRule("ROUTING_INVALID"); ok {
		t.Error("a proposal's code has a grounding rule")
	}
}
