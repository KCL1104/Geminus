package validate_test

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// D-023: issue order must not depend on input order. Position keys are free
// strings, so tokens such as "01", "+1", "-1" and indices beyond int64 occur
// (review of T-004: Atoi overflow and "1" == "01" == "+1").
func TestSortIsTotal(t *testing.T) {
	tokens := []string{"0", "1", "01", "001", "+1", "-1", "2", "10", "9",
		"9223372036854775807", "9223372036854775808", "18446744073709551616", "a", "end", "~0", "~1", ""}
	var in []model.ValidationIssue
	for _, tok := range tokens {
		for _, sev := range []model.Severity{model.SeverityError, model.SeverityWarning} {
			in = append(in, model.ValidationIssue{Code: "VIEW_REFERENCE_INVALID", Path: "/views/2d/positions/" + tok, Message: "m", Severity: sev})
		}
	}
	want := validate.Sort(in)
	r := rand.New(rand.NewPCG(1, 2))
	for range 500 {
		shuffled := slices.Clone(in)
		r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		if got := validate.Sort(shuffled); !slices.Equal(got, want) {
			t.Fatalf("order depends on input order:\n got %v\nwant %v", got, want)
		}
	}
	var paths []string
	for _, i := range want[:12] {
		paths = append(paths, i.Path[len("/views/2d/positions/"):])
	}
	// Array indices first, numerically and without overflow; then other tokens byte-wise.
	if exp := []string{"0", "0", "1", "1", "2", "2", "9", "9", "10", "10", "9223372036854775807", "9223372036854775807"}; !slices.Equal(paths, exp) {
		t.Errorf("index order %v, want %v", paths, exp)
	}
	if len(want) != len(in) {
		t.Errorf("distinct issues were merged: %d of %d", len(want), len(in))
	}
}
