package electronicsline

import (
	"slices"

	"github.com/kcl1104/geminus/internal/domainpack"
)

var _ domainpack.PhotoVocabulary = Pack{}

// photoConcepts are what a segmenter looks for in a photo or floor plan of
// the line (T-056, H-033), as T-055's segmentation request takes them
// (at most 8 lowercase noun phrases, one call and one SAM image each): for
// a photo, the machine of the stations of §2 in line order (SMT, AOI, ICT,
// FCT, packing) and the conveyor between stations (a line buffer, §3.1);
// for a drawn floor plan, its shapes: a rectangle (a station, whose kind
// the vision model reads from its label) and an arrow (the flow). The
// order is the order the segments reach the photo prompt in.
//
// The shapes came after the first live run (2026-10-27, run
// 01M5XDJ3T0TTGE55K3A3JVK29N): SAM 3.1 answered every concept with no box
// on the drawn demo floor plan. SAM segments the things a short noun phrase
// names, concrete nouns best, and its docs count the squares of a drawing
// by "square" (https://dev.meta.ai/docs/sam/segmenting, "Write the concept
// prompt"), while fine-grained terms outside its training are its known
// weakness (SAM 3 paper, arXiv 2511.16719, appendix B). To stay within 8
// images, "burn-in chamber" and "workstation" gave way: the demo shows
// neither, and on a floor plan either is a labelled rectangle (§13;
// PROPOSED D-131 (b)).
var photoConcepts = []string{"smt line", "aoi machine", "ict tester", "fct tester", "packing station", "conveyor belt",
	"rectangle", "arrow"}

// PhotoConcepts returns a copy of the segmenter's concepts.
func (Pack) PhotoConcepts() []string { return slices.Clone(photoConcepts) }
