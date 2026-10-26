package anthropic

import "github.com/kcl1104/geminus/internal/ai/provider/structured"

// The projection of the output schema and the decoding of its encoded
// values (T-053 schema follow-up and its review) live in
// internal/ai/provider/structured since T-055, which moved them there
// unchanged so that the Meta adapter shares them. These aliases keep this
// package's names.
type (
	Projection = structured.Projection
	Location   = structured.Location
	Step       = structured.Step
	Cond       = structured.Cond
)

// Project projects a canonical output schema (a file in schemas/).
func Project(file string) (Projection, error) { return structured.Project(file) }

const (
	EncodedDescription   = structured.EncodedDescription
	MaxEncodedValueBytes = structured.MaxEncodedValueBytes
	MaxEncodedValueDepth = structured.MaxEncodedValueDepth
)
