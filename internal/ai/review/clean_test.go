package review

import (
	"encoding/json/v2"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// marker is provider-written text that must never reach a stored issue.
const marker = "PROVIDER_TEXT_MARKER"

// stubPack is the software delivery pack with its domain rules replaced:
// it returns issues, whatever the model.
type stubPack struct {
	softwaredelivery.Pack
	issues []model.ValidationIssue
}

func (p stubPack) Validate(*model.ModelIR) []model.ValidationIssue { return slices.Clone(p.issues) }

// noMarker fails if an issue of o quotes the marker in its message or path.
func noMarker(t *testing.T, name string, o Outcome) {
	t.Helper()
	if o.Validation == nil {
		t.Fatalf("%s: no validation (%q)", name, o.Status)
	}
	for _, is := range o.Validation.Issues {
		if strings.Contains(is.Message, marker) || strings.Contains(is.Path, marker) {
			t.Errorf("%s: a %s outcome keeps provider text: %s %q %q", name, o.Status, is.Code, is.Path, is.Message)
		}
	}
	wantClean(t, o)
}

// D-062 (c), AI_SPEC §4 item 6, SECURITY §7: an invalid proposal keeps
// nothing the provider wrote, not even inside an issue. Every message is a
// fixed sentence of the server's, and every path holds only array indexes
// and member names the schemas know, cut at the first other token (H-003
// review M1). The same holds for the warnings a ready proposal keeps.
func TestInvalidKeepsNoProviderText(t *testing.T) {
	for name, c := range map[string]struct {
		mode    provider.Mode
		changes string
	}{
		"dangling reference": {provider.ModeEdit, `[{"op":"test","path":"/flows/0/id","value":"f1"},{"op":"replace","path":"/flows/0/to","value":"` + marker + `_node"}]`},
		"extra member": {provider.ModeEdit, `[{"op":"test","path":"/nodes/2/id","value":"ci"},` +
			`{"op":"add","path":"/nodes/2/` + marker + ` visit http:~1~1evil.example","value":1}]`},
		"pattern":          {provider.ModeEdit, `[{"op":"test","path":"/flows/0/id","value":"f1"},{"op":"replace","path":"/flows/0/to","value":"` + marker + ` with spaces"}]`},
		"enum":             {provider.ModeEdit, `[{"op":"test","path":"/resources/0/id","value":"reviewer"},{"op":"replace","path":"/resources/0/queue_discipline","value":"` + marker + `"}]`},
		"wrong test value": {provider.ModeEdit, `[{"op":"test","path":"/resources/1/id","value":"` + marker + `"},{"op":"replace","path":"/resources/1/capacity","value":6}]`},
		"forbidden path":   {provider.ModeEdit, `[{"op":"replace","path":"/views/2d/positions/` + marker + `","value":{"x":1,"y":2}}]`},
		"scenario path":    {provider.ModeScenario, `[{"op":"replace","path":"/` + marker + `","value":1}]`},
		"missing target":   {provider.ModeEdit, `[{"op":"test","path":"/nodes/9/id","value":"x"},{"op":"replace","path":"/nodes/9/` + marker + `","value":1}]`},
		"unknown entity type": {provider.ModeEdit, `[{"op":"test","path":"/nodes/0/id","value":"change_arrival"},` +
			`{"op":"replace","path":"/nodes/0/entity_type","value":"` + marker + `"}]`},
		"duplicate id": {provider.ModeEdit, `[{"op":"test","path":"/resources/0/id","value":"reviewer"},{"op":"replace","path":"/resources/0/name","value":"` + marker + `"},` +
			`{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/id","value":"reviewer"}]`},
	} {
		t.Run(name, func(t *testing.T) {
			o := run(t, input(t, c.mode, proposed(ops(t, c.changes), "low")))
			if o.Status != Invalid {
				t.Fatalf("status %q, want invalid (%v)", o.Status, codes(o))
			}
			noMarker(t, name, o)
		})
	}

	// The provider client's issues (T-024 invalid_output) and a later
	// stage's (the dataset binding stage) are cleaned the same way.
	quoted := []model.ValidationIssue{
		model.NewIssue(provider.OutputInvalid, "/"+marker, "members not allowed: "+marker, model.SeverityError),
		model.NewIssue(provider.OutputInvalid, "/changes/0/value/"+marker+"/x", marker, model.SeverityError),
	}
	noMarker(t, "invalid output", InvalidOutput(quoted))
	raise := `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`
	ready := run(t, input(t, provider.ModeEdit, proposed(ops(t, raise), "low")))
	unbound := []model.ValidationIssue{
		model.NewIssue(validate.DatasetBindingInvalid, "/nodes/2/service_time/distribution/samples_ref", "dataset:"+marker+" has no dataset binding", model.SeverityError),
		model.NewIssue(validate.DatasetBindingInvalid, "/dataset_bindings/0/"+marker, "stored snapshot is "+marker, model.SeverityError),
	}
	noMarker(t, "refused", ready.Refuse(unbound))
	warned := ready.Refuse([]model.ValidationIssue{model.NewIssue(validate.DatasetBindingInvalid, "/x/"+marker, marker, model.SeverityWarning)})
	if warned.Status != Ready {
		t.Fatalf("a warning made it %q", warned.Status)
	}
	noMarker(t, "a later warning", warned)

	// The domain pack's issues too, errors and warnings.
	in := input(t, provider.ModeEdit, proposed(ops(t, raise), "low"))
	in.Pack = stubPack{issues: []model.ValidationIssue{model.NewIssue("PACK_RULE", "/nodes/2/"+marker, "node "+marker+" breaks a rule", model.SeverityError)}}
	o := run(t, in)
	if o.Status != Invalid {
		t.Fatalf("a pack error left it %q", o.Status)
	}
	noMarker(t, "pack error", o)
	in.Pack = stubPack{issues: []model.ValidationIssue{model.NewIssue("PACK_RULE", "/nodes/2/"+marker, marker, model.SeverityWarning)}}
	if o = run(t, in); o.Status != Ready {
		t.Fatalf("a pack warning made it %q", o.Status)
	}
	noMarker(t, "pack warning", o)
}

// A path keeps its array indexes and the schemas' member names up to the
// first other token; a message keeps a fixed sentence of its code.
func TestClean(t *testing.T) {
	for path, want := range map[string]string{
		"":                                   "",
		"/":                                  "",
		"nodes/1":                            "",
		"/" + marker:                         "",
		"/nodes/2/service_time/distribution": "/nodes/2/service_time/distribution",
		"/nodes/2/" + marker + "/x":          "/nodes/2",
		"/nodes/01":                          "/nodes",
		"/views/2d/positions/ci":             "/views/2d/positions",
		"/views/2d/positions/ci/x":           "/views/2d/positions",
		"/changes/3/value/name":              "/changes/3/value/name",
		"/changes/3/path":                    "/changes/3/path",
		"/expected_effects/0":                "/expected_effects/0",
		"/dataset_bindings/0/content_hash":   "/dataset_bindings/0/content_hash",
		"/metadata/" + marker:                "/metadata",
		"/nodes/2/a~1b":                      "/nodes/2",
		"/requires_confirmation":             "/requires_confirmation",
		"/nodes/9/requires/0/resource_id/" + marker: "/nodes/9/requires/0/resource_id",
	} {
		if got := knownPath(path); got != want {
			t.Errorf("knownPath(%q) = %q, want %q", path, got, want)
		}
	}
	for _, c := range []struct{ in, want model.ValidationIssue }{
		{model.NewIssue(validate.UnknownReference, "/flows/0/to", `no node "x"`, model.SeverityError),
			model.NewIssue(validate.UnknownReference, "/flows/0/to", sentences[validate.UnknownReference][0], model.SeverityError)},
		{model.NewIssue(OutputInvalid, "/requires_confirmation", sentConfirmation, model.SeverityError),
			model.NewIssue(OutputInvalid, "/requires_confirmation", sentConfirmation, model.SeverityError)},
		{model.NewIssue(IDPoolViolation, "/nodes/9/id", poolReasons[idpool.WrongKind], model.SeverityError),
			model.NewIssue(IDPoolViolation, "/nodes/9/id", poolReasons[idpool.WrongKind], model.SeverityError)},
		{model.NewIssue(IDPoolViolation, "/nodes/9/id", "node x is not pooled", model.SeverityError),
			model.NewIssue(IDPoolViolation, "/nodes/9/id", poolReasons[idpool.NotInPool], model.SeverityError)},
		{model.NewIssue("PACK_RULE", "/nodes/2", "a pack's own words", model.SeverityWarning),
			model.NewIssue("PACK_RULE", "/nodes/2", otherSentence, model.SeverityWarning)},
		{model.NewIssue(validate.LimitExceeded, "", "12 more issues are not listed", model.SeverityError),
			model.NewIssue(validate.LimitExceeded, "", "12 more issues are not listed", model.SeverityError)},
		{model.NewIssue(validate.LimitExceeded, "", "12 more issues are not listed "+marker, model.SeverityError),
			model.NewIssue(validate.LimitExceeded, "", sentences[validate.LimitExceeded][0], model.SeverityError)},
	} {
		if got := Clean(c.in); got != c.want {
			t.Errorf("Clean(%+v) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// Every code of the catalogue has its fixed sentence, so the UI's English
// fallback is never a stage's own wording.
func TestEveryCodeHasASentence(t *testing.T) {
	var doc struct {
		Defs struct {
			ValidationCode struct {
				Enum []string `json:"enum"`
			} `json:"ValidationCode"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "schemas", "common.schema.json")), &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range doc.Defs.ValidationCode.Enum {
		if len(sentences[c]) == 0 {
			t.Errorf("%s has no sentence", c)
		}
	}
	if len(sentences) != len(doc.Defs.ValidationCode.Enum) {
		t.Errorf("%d sentences for %d codes", len(sentences), len(doc.Defs.ValidationCode.Enum))
	}
	for code, ss := range sentences {
		for _, s := range ss {
			if s == "" || strings.ContainsAny(s, "%\"") {
				t.Errorf("%s: %q is not a fixed sentence", code, s)
			}
		}
	}
}
