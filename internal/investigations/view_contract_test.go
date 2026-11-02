package investigations

import (
	"reflect"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
)

// The investigation's Go members are investigation.schema.json's
// Investigation, nested ones too: H-079's intent (the person's words, as
// stored) is in both, and a member added to one and not the other fails
// here, not in a client.
func TestInvestigationMatchesTheContract(t *testing.T) {
	contracttest.LoadSchemas(t).Parity(t, reflect.TypeFor[Investigation](), "investigation.schema.json#/$defs/Investigation")
}
