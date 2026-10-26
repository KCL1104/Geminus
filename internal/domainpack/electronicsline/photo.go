package electronicsline

import (
	"slices"

	"github.com/kcl1104/geminus/internal/domainpack"
)

var _ domainpack.PhotoVocabulary = Pack{}

// photoPrompts are what a segmenter looks for in a photo or floor plan of
// the line (T-056, H-033): the machine of each station of §2 in line order
// (SMT, AOI, ICT, FCT, burn-in, debug, packing), the conveyor or rack
// between stations (a line buffer, §3.1), then the generic words for what
// the others miss. A box names its prompt by index, so the order is part
// of the contract with the photo prompt and the test fixtures.
var photoPrompts = []string{"SMT line", "AOI machine", "ICT tester", "FCT tester", "burn-in chamber", "debug workstation", "packing station",
	"conveyor", "workstation", "machine"}

// PhotoPrompts returns a copy of the segmenter's text prompts.
func (Pack) PhotoPrompts() []string { return slices.Clone(photoPrompts) }
