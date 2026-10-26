package electronicsline

import (
	"slices"

	"github.com/kcl1104/geminus/internal/domainpack"
)

var _ domainpack.PhotoVocabulary = Pack{}

// photoConcepts are what a segmenter looks for in a photo or floor plan of
// the line (T-056, H-033), as T-055's segmentation request takes them
// (at most 8 lowercase noun phrases, one call each): the machine of each
// station of §2 in line order (SMT, AOI, ICT, FCT, burn-in, packing), the
// conveyor between stations (a line buffer, §3.1), then the workstation of
// a manual step (debug, rework). The order is the order the segments reach
// the photo prompt in.
var photoConcepts = []string{"smt line", "aoi machine", "ict tester", "fct tester", "burn-in chamber", "packing station",
	"conveyor belt", "workstation"}

// PhotoConcepts returns a copy of the segmenter's concepts.
func (Pack) PhotoConcepts() []string { return slices.Clone(photoConcepts) }
