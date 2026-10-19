package provider

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
)

// The output types are the provider-output schemas, member for member, and
// every fixture valid under those schemas round-trips through them.
func TestSchemaParity(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	s.Parity(t, reflect.TypeFor[ProposalOutput](), "proposal-output.schema.json#")
	s.Parity(t, reflect.TypeFor[MappingOutput](), "ai-mapping.schema.json#")
	s.Parity(t, reflect.TypeFor[AnalysisOutput](), "analysis-output.schema.json#")
	n := 0
	for _, c := range contracttest.Cases(t) {
		if !c.Valid {
			continue
		}
		data := contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), c.Instance))
		switch c.Schema {
		case "schemas/proposal-output.schema.json":
			contracttest.RoundTrip[ProposalOutput](t, c.ID, data)
		case "schemas/ai-mapping.schema.json":
			contracttest.RoundTrip[MappingOutput](t, c.ID, data)
		case "schemas/analysis-output.schema.json":
			contracttest.RoundTrip[AnalysisOutput](t, c.ID, data)
		default:
			continue
		}
		n++
	}
	if n < 4 {
		t.Errorf("only %d provider-output fixtures round-tripped", n)
	}
	for _, r := range Roles {
		if r.OutputSchema() == "" {
			t.Errorf("role %s has no output schema", r)
		}
	}
}
