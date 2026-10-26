package electronicsline_test

import (
	"bytes"
	"encoding/csv"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
	"github.com/kcl1104/geminus/internal/model"
)

var _ domainpack.EventRulesProvider = electronicsline.Pack{}

func rules(t *testing.T, ir *model.ModelIR) func(domainpack.CanonicalEvent) []domainpack.EventIssue {
	t.Helper()
	check := pack.EventRules(ir)
	if check == nil {
		t.Fatal("no event rules for an electronics line model")
	}
	return check
}

func op(stage, outcome, product string) domainpack.CanonicalEvent {
	return domainpack.CanonicalEvent{CaseID: "SYN-A-000001", EventType: "operation", Timestamp: "2026-10-05T06:00:00+08:00",
		EndTimestamp: "2026-10-05T06:02:00+08:00", Stage: stage, ResourceID: "ict_tester_1", Outcome: outcome, Shift: "early", Product: product}
}

func downtime(stage, outcome string) domainpack.CanonicalEvent {
	return domainpack.CanonicalEvent{CaseID: "ict_tester_1", EventType: "downtime", Timestamp: "2026-10-05T07:00:00+08:00",
		EndTimestamp: "2026-10-05T07:20:00+08:00", Stage: stage, ResourceID: "ict_tester_1", Outcome: outcome}
}

// codes is the code and field of each issue, in order.
func codes(is []domainpack.EventIssue) []string {
	out := []string{}
	for _, i := range is {
		out = append(out, i.Code+"@"+i.Field)
	}
	return out
}

// D-059's data-quality rules of a station log, for the template: the
// pack's record types; a stage is a station of the model (a retest at its
// station) or one of its steps; an operation and a downtime have an end;
// an operation at a test station has a result, and an operation's result
// is pass or fail; a downtime's result is a reason code; the product is
// an entity type of the model, and with two families an operation names
// one (D-055). Each is a warning at its field (D-072 (b)) with a fixed
// message that quotes no value.
func TestEventRules(t *testing.T) {
	check := rules(t, tmpl(t))
	noEnd := op("ict", "pass", "board_a")
	noEnd.EndTimestamp = ""
	downNoEnd := downtime("fct", "breakdown")
	downNoEnd.EndTimestamp = ""
	unknownType := op("xray", "", "")
	unknownType.EventType = "maintenance"
	for name, tc := range map[string]struct {
		e    domainpack.CanonicalEvent
		want []string
	}{
		"a test":                       {op("ict", "pass", "board_a"), nil},
		"a failed test":                {op("fct", "fail", "board_b"), nil},
		"burn-in":                      {op("burn_in", "pass", "board_a"), nil},
		"SMT without a result":         {op("smt", "", "board_a"), nil},
		"packing without a result":     {op("pack", "", "board_b"), nil},
		"debug repaired":               {op("ict_debug", "pass", "board_a"), nil},
		"debug without a result":       {op("fct_debug", "", "board_a"), nil},
		"touch-up":                     {op("aoi_touchup", "", "board_a"), nil},
		"a step by its ID":             {op("ict_retest", "fail", "board_a"), nil},
		"a downtime":                   {downtime("ict", "breakdown"), nil},
		"a downtime without a reason":  {downtime("burn_in", ""), nil},
		"a downtime's reason code":     {downtime("smt", "PASS"), nil},
		"a downtime names no product":  {downtime("aoi", ""), nil},
		"an unknown record type":       {unknownType, []string{"EVENT_TYPE_UNKNOWN@event_type", "EVENT_STAGE_UNKNOWN@stage"}},
		"no stage":                     {op("", "pass", "board_a"), []string{"EVENT_STAGE_MISSING@stage"}},
		"an unknown station":           {op("xray", "pass", "board_a"), []string{"EVENT_STAGE_UNKNOWN@stage"}},
		"a station in capitals":        {op("ICT", "pass", "board_a"), []string{"EVENT_STAGE_UNKNOWN@stage"}},
		"a downtime at no station":     {downtime("", "breakdown"), []string{"EVENT_STAGE_MISSING@stage"}},
		"no end":                       {noEnd, []string{"EVENT_END_MISSING@end_timestamp"}},
		"a downtime without an end":    {downNoEnd, []string{"EVENT_END_MISSING@end_timestamp"}},
		"a test without a result":      {op("aoi", "", "board_a"), []string{"EVENT_OUTCOME_MISSING@outcome"}},
		"a retest without a result":    {op("fct", "", "board_b"), []string{"EVENT_OUTCOME_MISSING@outcome"}},
		"a result in capitals":         {op("ict", "PASS", "board_a"), []string{"EVENT_OUTCOME_UNKNOWN@outcome"}},
		"a result at SMT":              {op("smt", "ok", "board_a"), []string{"EVENT_OUTCOME_UNKNOWN@outcome"}},
		"no product":                   {op("ict", "pass", ""), []string{"EVENT_PRODUCT_MISSING@product"}},
		"an unknown product":           {op("ict", "pass", "board_c"), []string{"EVENT_PRODUCT_UNKNOWN@product"}},
		"a downtime's unknown product": {func() domainpack.CanonicalEvent { d := downtime("ict", ""); d.Product = "board_c"; return d }(), []string{"EVENT_PRODUCT_UNKNOWN@product"}},
		"everything at once": {domainpack.CanonicalEvent{CaseID: "x", EventType: "operation", Timestamp: "2026-10-05T06:00:00+08:00",
			Stage: "xray", Outcome: "PASS", Product: "board_c"},
			[]string{"EVENT_STAGE_UNKNOWN@stage", "EVENT_END_MISSING@end_timestamp", "EVENT_OUTCOME_UNKNOWN@outcome", "EVENT_PRODUCT_UNKNOWN@product"}},
	} {
		got := check(tc.e)
		if want := tc.want; !slices.Equal(codes(got), append([]string{}, want...)) {
			t.Errorf("%s: %v, want %v", name, codes(got), want)
		}
		for _, is := range got {
			if is.Message == "" || !strings.HasPrefix(is.Message, is.Field+": ") || strings.Contains(is.Message, "xray") ||
				strings.Contains(is.Message, "board_c") || strings.Contains(is.Message, "PASS") || strings.Contains(is.Message, "maintenance") {
				t.Errorf("%s: message %q", name, is.Message)
			}
		}
	}
}

// The vocabulary is the model's: one family needs no product; a model
// whose steps carry no station attributes knows its steps by ID only, and
// has no test station whose result is required; a model of another domain
// has no rules; the rules change neither the model nor the event.
func TestEventRulesFollowTheModel(t *testing.T) {
	one := tmpl(t)
	one.EntityTypes = one.EntityTypes[:1]
	if got := rules(t, one)(op("ict", "pass", "")); len(got) != 0 {
		t.Errorf("one family: %v", codes(got))
	}
	if got := rules(t, one)(op("ict", "pass", "board_b")); !slices.Equal(codes(got), []string{"EVENT_PRODUCT_UNKNOWN@product"}) {
		t.Errorf("one family, the other's product: %v", codes(got))
	}
	bare := tmpl(t)
	for i := range bare.Nodes {
		bare.Nodes[i].DomainAttributes = nil
	}
	check := rules(t, bare)
	if got := check(op("pack", "", "board_a")); !slices.Equal(codes(got), []string{"EVENT_STAGE_UNKNOWN@stage"}) {
		t.Errorf("no attributes, station pack: %v", codes(got))
	}
	if got := check(op("pack_ship", "", "board_a")); len(got) != 0 {
		t.Errorf("no attributes, step pack_ship: %v", codes(got))
	}
	if got := check(op("ict", "", "board_a")); len(got) != 0 {
		t.Errorf("no attributes, step ict without a result: %v", codes(got))
	}
	other := tmpl(t)
	other.Domain = "software_delivery"
	if pack.EventRules(other) != nil || pack.EventRules(nil) != nil {
		t.Error("rules for a model of another domain")
	}
	ir := tmpl(t)
	before, err := json.Marshal(ir, json.Deterministic(true)) // shift_capacity is a map
	if err != nil {
		t.Fatal(err)
	}
	check = rules(t, ir)
	e := op("xray", "PASS", "board_c")
	kept := e
	check(e)
	after, err := json.Marshal(ir, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !reflect.DeepEqual(e, kept) {
		t.Error("the rules changed the model or the event")
	}
}

// datasetFile reads a file of the synthetic data set (T-052).
func datasetFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "datasets", "electronics-line", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// events maps a station log of the data set as its README says (D-059):
// serial, record type, station, equipment, start, end, result, shift and
// product; the operator is not imported, synthetic goes to metadata.
func events(t *testing.T, data []byte) []domainpack.CanonicalEvent {
	t.Helper()
	recs, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var out []domainpack.CanonicalEvent
	for _, r := range recs[1:] {
		out = append(out, domainpack.CanonicalEvent{CaseID: r[0], EventType: r[1], Stage: r[2], ResourceID: r[3], Timestamp: r[4],
			EndTimestamp: r[5], Outcome: r[6], Shift: r[7], Product: r[8], Metadata: []byte(`{"synthetic":"` + r[10] + `"}`)})
	}
	return out
}

// The synthetic log (T-052) breaks no rule of its model, the template; each
// negative fixture breaks exactly the one its name says, in one row (the
// one with an end before its start is D-072's, not the pack's).
func TestTheSyntheticLogKeepsTheRules(t *testing.T) {
	check := rules(t, tmpl(t))
	all := events(t, datasetFile(t, "station-log-synthetic.csv"))
	if len(all) < 20000 {
		t.Fatalf("%d events", len(all))
	}
	for i, e := range all {
		if got := check(e); len(got) != 0 {
			t.Fatalf("row %d: %v", i, codes(got))
		}
	}
	for name, want := range map[string]string{
		"end-before-start.csv": "", "unknown-station.csv": "EVENT_STAGE_UNKNOWN@stage", "unknown-product.csv": "EVENT_PRODUCT_UNKNOWN@product",
		"missing-product.csv": "EVENT_PRODUCT_MISSING@product", "unknown-record-type.csv": "EVENT_TYPE_UNKNOWN@event_type",
		"missing-test-result.csv": "EVENT_OUTCOME_MISSING@outcome", "unknown-test-result.csv": "EVENT_OUTCOME_UNKNOWN@outcome",
	} {
		var found []string
		for _, e := range events(t, datasetFile(t, "negative/"+name)) {
			found = append(found, codes(check(e))...)
		}
		if (want == "" && len(found) != 0) || (want != "" && !slices.Equal(found, []string{want})) {
			t.Errorf("%s: %v, want %q", name, found, want)
		}
	}
}
