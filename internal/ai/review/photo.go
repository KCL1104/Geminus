package review

import (
	"strconv"

	"github.com/kcl1104/geminus/internal/model"
)

// The photo pipeline's rules (T-056, H-033). A proposal asked for with a
// photo or floor plan is reviewed like any edit (Review), and then by
// Photo: the server cannot see what the photo shows, but it can hold the
// proposal to what a photo can support.
//
//   - Steps: no more new process steps than the segmenter's boxes. Each new
//     station is something the photo shows; buffers, decisions and sinks are
//     not counted (they are the flow between the boxes).
//   - Numbers: a photo shows no service time, buffer size, yield, breakdown
//     or shift. Every number of the candidate (a new step's, a changed
//     step's, a new flow's or resource's; review's values: capacities,
//     units, distribution parameters, probabilities, calendars, schedules,
//     batches, constraints) must be a value the base model already has, so
//     the AI can only copy what the model states, and say so in its
//     assumptions; the one exception is a resource's capacity, which may
//     also be a count of the boxes (1 to their number: two ICT testers in
//     the photo). Anything else is the AI's own number, and the proposal is
//     invalid.
//
// The boxes themselves are checked before the proposal is asked for
// (internal/ai/vision.CheckBoxes), with the sentences below, so that every
// issue keeps a fixed sentence of the server's through Clean.

// The segmenter's answer (T-056): the sentences of an unacceptable box.
const (
	SentTooManyBoxes = "the segmenter found more boxes than a photo proposal takes"
	SentBoxOutside   = "a box of the segmenter's answer lies outside the image or has no area"
	SentBoxNumber    = "a box of the segmenter's answer is not a finite number, or its score is not from 0 to 1"
	SentBoxPrompt    = "a box of the segmenter's answer names no text prompt of the request"
	SentBoxLabel     = "a box's label is not text"
)

// The photo rules' own sentences.
const (
	sentPhotoSteps  = "the proposal adds more process steps than the photo shows boxes"
	sentPhotoNumber = "a step, flow or resource states a number that is neither a value of the base model nor, for a capacity, a count of the photo's boxes"
)

// photoSentences are added to OutputInvalid's sentences (clean.go).
var photoSentences = []string{SentTooManyBoxes, SentBoxOutside, SentBoxNumber, SentBoxPrompt, SentBoxLabel, sentPhotoSteps, sentPhotoNumber}

// numbers is every number values reads of an IR, with the queues' finite
// capacities, which values leaves out.
func numbers(ir *model.ModelIR) []float64 {
	out := values(ir)
	for _, n := range ir.Nodes {
		if n.Capacity > 0 {
			out = append(out, float64(n.Capacity))
		}
	}
	return out
}

// Photo is the photo rules on a candidate the review found ready: base is
// the base version's IR, boxes the number of boxes the segmenter found and
// the server checked. Every issue is an error.
func Photo(base, candidate *model.ModelIR, boxes int) []model.ValidationIssue {
	var out []model.ValidationIssue
	bad := func(path, message string) { out = append(out, errorIssue(OutputInvalid, path, message)) }
	known := map[float64]bool{}
	for _, v := range numbers(base) {
		known[v] = true
	}
	inBase := map[string]bool{}
	for _, n := range base.Nodes {
		inBase[n.ID] = true
	}
	steps := 0
	for _, n := range candidate.Nodes {
		if n.Type == model.NodeProcess && !inBase[n.ID] {
			steps++
		}
	}
	if steps > boxes {
		bad("/changes", sentPhotoSteps)
	}
	unknown := func(vs []float64) bool {
		for _, v := range vs {
			if !known[v] {
				return true
			}
		}
		return false
	}
	for i := range candidate.Nodes {
		if unknown(numbers(&model.ModelIR{Nodes: candidate.Nodes[i : i+1]})) {
			bad("/nodes/"+strconv.Itoa(i), sentPhotoNumber)
		}
	}
	for i, fl := range candidate.Flows {
		if !known[fl.Routing.Probability] {
			bad("/flows/"+strconv.Itoa(i)+"/routing/probability", sentPhotoNumber)
		}
	}
	for i, r := range candidate.Resources {
		if c := r.Capacity; !known[float64(c)] && (c < 1 || c > boxes) {
			bad("/resources/"+strconv.Itoa(i)+"/capacity", sentPhotoNumber)
		}
		// values lists the capacity first; the rest are the resource's
		// distributions and units on shift.
		if rest := values(&model.ModelIR{Resources: candidate.Resources[i : i+1]})[1:]; unknown(rest) {
			bad("/resources/"+strconv.Itoa(i), sentPhotoNumber)
		}
	}
	if unknown(values(&model.ModelIR{Calendars: candidate.Calendars})) {
		bad("/calendars", sentPhotoNumber)
	}
	if unknown(values(&model.ModelIR{Constraints: candidate.Constraints})) {
		bad("/constraints", sentPhotoNumber)
	}
	return out
}
