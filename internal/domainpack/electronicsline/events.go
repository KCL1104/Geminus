package electronicsline

import (
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
)

// The record types of a station log (D-059, ELECTRONICS_LINE §6): one
// board's step at a station, and one machine's downtime.
const (
	EventOperation = "operation"
	EventDowntime  = "downtime"
)

// The results of an operation (§6): a test passes or fails; at a repair
// pass is repaired and fail scrapped. A downtime's result is a reason code
// of the user's, not in the vocabulary.
const (
	OutcomePass = "pass"
	OutcomeFail = "fail"
)

// The data-quality warnings of a station log imported for an electronics
// line model (D-059; D-072 (b): the pack's rules are warnings, the import
// is still VALIDATED). They are import issue codes, not model validation
// codes, so they are not in codes.go (common.schema.json ValidationCode).
const (
	// EventTypeUnknown: the record type is neither operation nor downtime.
	EventTypeUnknown = "EVENT_TYPE_UNKNOWN"
	// EventStageMissing: an operation or a downtime names no station.
	EventStageMissing = "EVENT_STAGE_MISSING"
	// EventStageUnknown: the stage is neither a station of the model (a
	// station process's station attribute) nor the ID of one of its steps.
	EventStageUnknown = "EVENT_STAGE_UNKNOWN"
	// EventEndMissing: an operation or a downtime has no end_timestamp
	// (§6: inference needs it).
	EventEndMissing = "EVENT_END_MISSING"
	// EventOutcomeMissing: an operation at a test station (AOI, ICT, FCT,
	// burn-in, as the model has them) has no result.
	EventOutcomeMissing = "EVENT_OUTCOME_MISSING"
	// EventOutcomeUnknown: an operation's result is neither pass nor fail.
	EventOutcomeUnknown = "EVENT_OUTCOME_UNKNOWN"
	// EventProductMissing: an operation of a model with two or more entity
	// types names no product (D-055, D-059).
	EventProductMissing = "EVENT_PRODUCT_MISSING"
	// EventProductUnknown: the product is not an entity type of the model.
	EventProductUnknown = "EVENT_PRODUCT_UNKNOWN"
)

// vocabulary is what a station log of one model may say.
type vocabulary struct {
	stages   map[string]bool // station names and step IDs
	tests    map[string]bool // stages whose operations have a result
	products map[string]bool
	multi    bool
}

// EventRules implements domainpack.EventRulesProvider: the data-quality
// rules of D-059 for the events of a station log imported for ir, nil for
// a model of another domain. The vocabulary is the model's: its stations
// (the station attribute of a station process; a retest is logged at its
// station) and the IDs of its steps (process nodes: touch-up, debug and
// any other step); its test stations (aoi, ict, fct, burn_in, as stations
// of the model) and their steps' IDs; its entity types.
func (Pack) EventRules(ir *model.ModelIR) func(domainpack.CanonicalEvent) []domainpack.EventIssue {
	if ir == nil || ir.Domain != ID {
		return nil
	}
	v := vocabulary{stages: map[string]bool{}, tests: map[string]bool{}, products: map[string]bool{}, multi: len(ir.EntityTypes) > 1}
	for _, n := range ir.Nodes {
		if n.Type == model.NodeProcess {
			v.stages[n.ID] = true
		}
	}
	for _, sn := range read(ir).nodesAt {
		v.stages[sn.kind.name] = true
		if sn.kind.test {
			v.tests[sn.kind.name] = true
			v.tests[ir.Nodes[sn.index].ID] = true
		}
	}
	for _, et := range ir.EntityTypes {
		v.products[et.ID] = true
	}
	return v.check
}

func warn(field, code, message string) domainpack.EventIssue {
	return domainpack.EventIssue{Field: field, Code: code, Message: field + ": " + message}
}

// check applies the rules to one event, its issues in the order of the
// fields: event_type, stage, end_timestamp, outcome, product.
func (v vocabulary) check(e domainpack.CanonicalEvent) []domainpack.EventIssue {
	var out []domainpack.EventIssue
	op, down := e.EventType == EventOperation, e.EventType == EventDowntime
	if !op && !down {
		out = append(out, warn("event_type", EventTypeUnknown, "not a record type of the electronics line (operation or downtime)"))
	}
	switch {
	case e.Stage == "" && (op || down):
		out = append(out, warn("stage", EventStageMissing, "an operation or a downtime names its station"))
	case e.Stage != "" && !v.stages[e.Stage]:
		out = append(out, warn("stage", EventStageUnknown, "not a station or a step of the model"))
	}
	if e.EndTimestamp == "" && (op || down) {
		out = append(out, warn("end_timestamp", EventEndMissing, "an operation or a downtime has an end (needed for cycle and repair times)"))
	}
	if op {
		switch {
		case e.Outcome == "" && v.tests[e.Stage]:
			out = append(out, warn("outcome", EventOutcomeMissing, "an operation at a test station has a result (pass or fail)"))
		case e.Outcome != "" && e.Outcome != OutcomePass && e.Outcome != OutcomeFail:
			out = append(out, warn("outcome", EventOutcomeUnknown, "an operation's result is pass or fail"))
		}
	}
	switch {
	case e.Product != "" && !v.products[e.Product]:
		out = append(out, warn("product", EventProductUnknown, "not an entity type of the model"))
	case e.Product == "" && op && v.multi:
		out = append(out, warn("product", EventProductMissing, "the model has several products, so an operation names its product"))
	}
	return out
}
