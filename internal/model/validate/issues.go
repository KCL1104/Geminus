package validate

import (
	"slices"
	"strconv"
	"strings"

	"github.com/kcl1104/geminus/internal/model"
)

// Pointer builds an RFC 6901 JSON Pointer from unescaped tokens.
func Pointer(tokens ...string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(strings.NewReplacer("~", "~0", "/", "~1").Replace(t))
	}
	return b.String()
}

func idx(i int) string { return strconv.Itoa(i) }

type issues []model.ValidationIssue

func (s *issues) add(code, path, message string) {
	*s = append(*s, model.ValidationIssue{Code: code, Path: path, Message: message, Severity: model.SeverityError})
}

func (s *issues) warn(code, path, message string) {
	*s = append(*s, model.ValidationIssue{Code: code, Path: path, Message: message, Severity: model.SeverityWarning})
}

func (s issues) hasError() bool {
	return slices.ContainsFunc(s, func(i model.ValidationIssue) bool { return i.Severity == model.SeverityError })
}

// Sort orders issues by JSON Pointer, comparing tokens left to right with
// array indices numerically, then by code and message (D-023), and drops
// exact duplicates.
func Sort(in []model.ValidationIssue) []model.ValidationIssue {
	out := slices.Clone(in)
	slices.SortStableFunc(out, func(a, b model.ValidationIssue) int {
		if c := comparePointers(a.Path, b.Path); c != 0 {
			return c
		}
		if c := strings.Compare(a.Code, b.Code); c != 0 {
			return c
		}
		return strings.Compare(a.Message, b.Message)
	})
	return slices.Compact(out)
}

func comparePointers(a, b string) int {
	ta, tb := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(ta) && i < len(tb); i++ {
		if c := compareToken(ta[i], tb[i]); c != 0 {
			return c
		}
	}
	return len(ta) - len(tb)
}

func compareToken(a, b string) int {
	na, ea := strconv.Atoi(a)
	nb, eb := strconv.Atoi(b)
	switch {
	case ea == nil && eb == nil:
		return na - nb
	case ea == nil:
		return -1
	case eb == nil:
		return 1
	}
	return strings.Compare(a, b)
}
