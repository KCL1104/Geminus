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
	*s = append(*s, model.NewIssue(code, path, message, model.SeverityError))
}

func (s *issues) warn(code, path, message string) {
	*s = append(*s, model.NewIssue(code, path, message, model.SeverityWarning))
}

func (s issues) hasError() bool {
	return slices.ContainsFunc(s, func(i model.ValidationIssue) bool { return i.Severity == model.SeverityError })
}

// Sort orders issues by JSON Pointer, comparing tokens left to right with
// array indices numerically, then by code, message and severity (D-023), and
// drops exact duplicates. The order is total, so it never depends on the
// order issues were found in.
func Sort(in []model.ValidationIssue) []model.ValidationIssue {
	out := slices.Clone(in)
	slices.SortFunc(out, func(a, b model.ValidationIssue) int {
		if c := comparePointers(a.Path, b.Path); c != 0 {
			return c
		}
		if c := strings.Compare(a.Code, b.Code); c != 0 {
			return c
		}
		if c := strings.Compare(a.Message, b.Message); c != 0 {
			return c
		}
		return strings.Compare(string(a.Severity), string(b.Severity))
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

// compareToken puts array indices (RFC 6901: "0" or a digit string without a
// leading zero) first, in numeric order of any length, and every other token
// after them in byte order.
func compareToken(a, b string) int {
	ia, ib := isIndex(a), isIndex(b)
	switch {
	case ia && ib:
		if c := len(a) - len(b); c != 0 {
			return c
		}
	case ia:
		return -1
	case ib:
		return 1
	}
	return strings.Compare(a, b)
}

func isIndex(t string) bool {
	if t == "" || (t[0] == '0' && len(t) > 1) {
		return false
	}
	for i := 0; i < len(t); i++ {
		if t[i] < '0' || t[i] > '9' {
			return false
		}
	}
	return true
}
