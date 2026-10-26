package review

import (
	"strconv"
	"strings"

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
// The segments themselves are checked before the proposal is asked for:
// T-055's provider.SegmentClient checks every answer (the frame, each box
// inside it, distinct object IDs, a confidence from 0 to 1, at most
// provider.MaxSegmentsPerConcept a concept) and Segmentation turns what it
// refuses into the sentences below; the photo pipeline then takes at most
// MaxPhotoSegments in all (TooManySegments) and a frame of the image's own
// size (FrameMismatch). Every issue keeps a fixed sentence of the
// server's through Clean, at the path "" (a segment is no member of the
// proposal's documents).

// MaxPhotoSegments bounds the segments one photo proposal takes: a
// photo's line has some stations and conveyors, not hundreds, and every
// segment is a line of the vision model's prompt.
const MaxPhotoSegments = 64

// The segmenter's answer (T-056): the sentences of an unacceptable one.
const (
	SentTooManyBoxes = "the segmenter found more objects than a photo proposal takes"
	SentBoxOutside   = "a box of the segmenter's answer is not inside the image"
	SentBoxNumber    = "a confidence of the segmenter's answer is not a number from 0 to 1"
	SentBoxConcept   = "an object of the segmenter's answer is not of a concept the request named"
	SentBoxObject    = "the segmenter's answer names an object twice"
	SentBoxFrame     = "the segmenter's answer is not in the image's frame"
	SentSegmenter    = "the segmenter's answer does not pass the server's checks"
)

// The photo rules' own sentences.
const (
	sentPhotoSteps  = "the proposal adds more process steps than the photo shows boxes"
	sentPhotoNumber = "a step, flow or resource states a number that is neither a value of the base model nor, for a capacity, a count of the photo's boxes"
)

// photoSentences are added to OutputInvalid's sentences (clean.go).
var photoSentences = []string{SentTooManyBoxes, SentBoxOutside, SentBoxNumber, SentBoxConcept, SentBoxObject, SentBoxFrame, SentSegmenter,
	sentPhotoSteps, sentPhotoNumber}

// segmentSentences are the sentences of what provider.SegmentClient
// refuses, by the last token of its issue's path
// (/concepts/<i>/segments/<j>/<member>, /concepts/<i>/frame,
// /concepts/<i>/segments).
var segmentSentences = map[string]string{
	"box": SentBoxOutside, "confidence": SentBoxNumber, "concept": SentBoxConcept, "object_id": SentBoxObject, "frame": SentBoxFrame,
	"segments": SentTooManyBoxes,
}

// Segmentation is the issues of a segmentation the segmenter client
// refused (provider.CodeInvalidOutput's issues), in the review's own
// sentences: one issue per distinct sentence, at the path "", in the order
// first seen; an issue the table does not know is SentSegmenter. Never
// empty.
func Segmentation(refused []model.ValidationIssue) []model.ValidationIssue {
	var out []model.ValidationIssue
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, errorIssue(OutputInvalid, "", s))
		}
	}
	for _, is := range refused {
		last := is.Path[strings.LastIndexByte(is.Path, '/')+1:]
		if s, ok := segmentSentences[last]; ok {
			add(s)
		} else {
			add(SentSegmenter)
		}
	}
	if len(out) == 0 {
		add(SentSegmenter)
	}
	return out
}

// TooManySegments is the issue of a segmentation with more than
// MaxPhotoSegments segments.
func TooManySegments() []model.ValidationIssue {
	return []model.ValidationIssue{errorIssue(OutputInvalid, "", SentTooManyBoxes)}
}

// FrameMismatch is the issue of a segmentation whose frame is not the
// image's size (a turned frame: the stored image has no metadata to turn
// it, so the boxes would not be the image's).
func FrameMismatch() []model.ValidationIssue {
	return []model.ValidationIssue{errorIssue(OutputInvalid, "", SentBoxFrame)}
}

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
