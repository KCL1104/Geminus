package electronicsline

import (
	"fmt"
	"strconv"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// rules are ELECTRONICS_LINE §3.4 on an IR of this domain: rule 1 (a
// station's primary resource), rule 3 (a retest shares it with the first
// test) and rule 2 (a test result's one fail flow), with the warnings about
// attributes the pack cannot use. Rule 4 (the template's synthetic
// labelling) is the template's, and rule 5 keeps the generic validator's
// rules out of here. Messages name only the pack's own station and role
// names, never a value of the model.
func rules(ir *model.ModelIR) []model.ValidationIssue {
	l := read(ir)
	out := append([]model.ValidationIssue(nil), l.warnings...)
	requires := func(i int) string { return validate.Pointer("nodes", strconv.Itoa(i), "requires") }
	for _, s := range l.nodesAt {
		if s.matches != 1 {
			out = append(out, model.NewIssue(StationResourceInvalid, requires(s.index),
				fmt.Sprintf("a %s station step needs exactly one resource of role %s; it has %d", s.kind.name, s.kind.role, s.matches), model.SeverityError))
		}
	}
	for _, s := range l.nodesAt {
		if !s.retest || s.primary < 0 {
			continue
		}
		firsts, withPrimary, shared := 0, 0, false
		for _, f := range l.nodesAt {
			if f.retest || f.kind != s.kind {
				continue
			}
			firsts++
			if f.primary >= 0 {
				withPrimary++
				shared = shared || f.primary == s.primary
			}
		}
		switch {
		case firsts == 0:
			out = append(out, model.NewIssue(RetestResourceMismatch, requires(s.index),
				fmt.Sprintf("a %s retest needs a first test of its station (pass first)", s.kind.name), model.SeverityError))
		case withPrimary > 0 && !shared:
			// A first test without its primary resource is reported by rule
			// 1; the retest is checked once that holds (no cascade).
			out = append(out, model.NewIssue(RetestResourceMismatch, requires(s.index),
				fmt.Sprintf("a %s retest must use the %s resource of its station's first test", s.kind.name, s.kind.role), model.SeverityError))
		}
	}
	for _, r := range l.results {
		if r.fails != 1 {
			out = append(out, model.NewIssue(TestResultNotIdentified, validate.Pointer("nodes", strconv.Itoa(r.index)),
				fmt.Sprintf("a test result needs exactly one flow to a failure node, the fail branch; it has %d, so the first-pass yield is unavailable", r.fails),
				model.SeverityWarning))
		}
	}
	return validate.Sort(out)
}
