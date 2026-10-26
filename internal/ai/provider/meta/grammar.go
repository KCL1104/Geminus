package meta

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/kcl1104/geminus/internal/ai/provider"
)

// SAM's output grammar (https://dev.meta.ai/docs/sam/reading-segmentation,
// "Line structure", retrieved 2026-10-26), for one image:
//
//	line      ::= FRAME record ( "," record )*
//	record    ::= OBJECT_ID box mask
//	box       ::= "<|box;x1=" INT ";y1=" INT ";x2=" INT ";y2=" INT ";w=" INT ";h=" INT [ CONF ] "|>"
//	mask      ::= "<|mask;x=0;y=0" [ CONF ] ";data=" H "," W "," ENC PAYLOAD "|>"
//	CONF      ::= ";c=" DECIMAL    ; confidence, 0 to 1; only with include_confidence
//	FRAME     ::= "<" INT "f>"
//	ENC       ::= "~" | "!"
//	PAYLOAD   ::= <base85 bytes>   ; may contain "~" or "!"
//
// "A single image is emitted as frame <0f>: one line"; "Zero matches is a
// valid outcome. The stream completes with an empty output_text". The
// adapter asks for confidence, so every box and mask carries c, "the same
// value as on the record's box". A mask's raster is "smaller than the
// frame"; its payload is checked for printable characters only and
// dropped (the photo path uses the boxes).
//
// Anything else is no segmentation: another frame or a second line (a
// video), prose, JSON, a token the grammar does not name, a field out of
// order, a number out of range, a repeated object, more than
// provider.MaxSegmentsPerConcept records. The provider's text is never
// kept or quoted: the error is fixed.

var errGrammar = errors.New("meta: the output does not follow the segmentation grammar")

const (
	intRE = `(0|[1-9][0-9]{0,6})`
	decRE = `((?:0|1)(?:\.[0-9]{1,20})?)`
)

var (
	boxRE = regexp.MustCompile(`^x1=` + intRE + `;y1=` + intRE + `;x2=` + intRE + `;y2=` + intRE + `;w=` + intRE + `;h=` + intRE + `;c=` + decRE + `$`)
	// The mask's fields, then its marker and payload (printable ASCII but
	// space).
	maskRE = regexp.MustCompile(`^x=0;y=0;c=` + decRE + `;data=` + intRE + `,` + intRE + `,([~!])([\x21-\x7e]+)$`)
	idRE   = regexp.MustCompile(`^` + intRE)
	// nextRecord is what follows a record's end when another record
	// follows: the separator, an object ID and a box token.
	nextRecord = regexp.MustCompile(`^,` + intRE + `<\|box;`)
)

// parseSegmentation reads one image's segmentation of concept: the frame
// (0 x 0 when nothing matched) and a segment per record.
func parseSegmentation(text, concept string) (int, int, []provider.Segment, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, 0, nil, nil
	}
	rest, ok := strings.CutPrefix(text, "<0f>")
	if !ok || strings.ContainsAny(rest, "\r\n") {
		return 0, 0, nil, errGrammar
	}
	var segs []provider.Segment
	ids := map[int]bool{}
	w, h := 0, 0
	for {
		if len(segs) == provider.MaxSegmentsPerConcept {
			return 0, 0, nil, errGrammar // one more record than the bound
		}
		id := idRE.FindString(rest)
		if id == "" {
			return 0, 0, nil, errGrammar
		}
		rest = rest[len(id):]
		box, after, ok := token(rest, "<|box;")
		if !ok {
			return 0, 0, nil, errGrammar
		}
		b := boxRE.FindStringSubmatch(box)
		if b == nil {
			return 0, 0, nil, errGrammar
		}
		rest = after
		mask, after, ok := maskToken(rest)
		if !ok {
			return 0, 0, nil, errGrammar
		}
		mk := maskRE.FindStringSubmatch(mask)
		if mk == nil {
			return 0, 0, nil, errGrammar
		}
		rest = after
		n := func(s string) int { v, _ := strconv.Atoi(s); return v }
		oid, x1, y1, x2, y2, fw, fh := n(id), n(b[1]), n(b[2]), n(b[3]), n(b[4]), n(b[5]), n(b[6])
		c, err := strconv.ParseFloat(b[7], 64)
		mc, merr := strconv.ParseFloat(mk[1], 64)
		if err != nil || merr != nil || c > 1 || mc != c {
			return 0, 0, nil, errGrammar // the mask's confidence is the box's
		}
		mh, mw := n(mk[2]), n(mk[3])
		switch {
		case fw < 1 || fh < 1 || (w != 0 && (fw != w || fh != h)):
			return 0, 0, nil, errGrammar
		case mh < 1 || mw < 1 || mh > fh || mw > fw:
			return 0, 0, nil, errGrammar
		case ids[oid]:
			return 0, 0, nil, errGrammar
		}
		w, h, ids[oid] = fw, fh, true
		segs = append(segs, provider.Segment{Concept: concept, ObjectID: oid, Box: provider.Box{X1: x1, Y1: y1, X2: x2, Y2: y2}, Confidence: c})
		if rest == "" {
			return w, h, segs, nil
		}
		if !strings.HasPrefix(rest, ",") {
			return 0, 0, nil, errGrammar
		}
		rest = rest[1:]
	}
}

// token reads a token that opens with open and closes at the first "|>"
// (a box's fields hold no "|"): its fields and what follows it.
func token(s, open string) (string, string, bool) {
	body, ok := strings.CutPrefix(s, open)
	if !ok {
		return "", "", false
	}
	fields, after, ok := strings.Cut(body, "|>")
	return fields, after, ok && !strings.Contains(fields, "|")
}

// maskToken reads a mask token: its payload may hold any printable
// character, "|>" included, so the token closes at the first "|>" that the
// end of the line or the next record follows.
func maskToken(s string) (string, string, bool) {
	body, ok := strings.CutPrefix(s, "<|mask;")
	if !ok {
		return "", "", false
	}
	from := 0
	for {
		i := strings.Index(body[from:], "|>")
		if i < 0 {
			return "", "", false
		}
		end := from + i
		after := body[end+2:]
		if after == "" || nextRecord.MatchString(after) {
			return body[:end], after, true
		}
		from = end + 1
	}
}
