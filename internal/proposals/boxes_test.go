package proposals

import (
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// H-052: generated_by.segmenter.boxes, the objects SAM 3.1 found, drawn over
// the photo on the proposal page. The server shows the stored boxes only
// when every one passes its checks again, as the contract states them: as
// many as objects, each concept a short noun phrase, each box inside the
// stored image's frame with its corners in order, each confidence from 0 to
// 1. Otherwise (or for a proposal stored before the boxes were kept) only
// the object count is shown, never part of the boxes and never an error.
func TestGeneratedByShowsTheBoxes(t *testing.T) {
	const box1 = `{"concept":"smt line","x1":40,"y1":190,"x2":229,"y2":309,"confidence":0.93}`
	const box2 = `{"concept":"conveyor belt","x1":230,"y1":242,"x2":329,"y2":257,"confidence":0.81}`
	meta := func(image, segments, boxes string) []byte {
		return fmt.Appendf(nil, `{"provider":"meta","model":"muse-spark-1.3",%s"segmenter":{"provider":"meta","model":"sam-3.1","concepts":8,"segments":%s%s}}`,
			image, segments, boxes)
	}
	const image = `"image":{"digest":"sha256:bb","media_type":"image/png","width":1200,"height":520},`
	got := FromMetadata(meta(image, "2", `,"boxes":[`+box1+`,`+box2+`]`))
	want := []SegmentBox{
		{Concept: "smt line", X1: 40, Y1: 190, X2: 229, Y2: 309, Confidence: 0.93},
		{Concept: "conveyor belt", X1: 230, Y1: 242, X2: 329, Y2: 257, Confidence: 0.81},
	}
	if got == nil || got.Segmenter == nil || got.Segmenter.Objects != 2 || !slices.Equal(got.Segmenter.Boxes, want) {
		t.Fatalf("got %+v", got)
	}
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"boxes":[{"concept":"smt line","x1":40,"y1":190,"x2":229,"y2":309,"confidence":0.93},`) ||
		strings.Contains(string(out), "object_id") || strings.Contains(string(out), "sha256") {
		t.Errorf("shown as %s", out)
	}

	// The corners at the frame's last pixel are inside; a box of one pixel is a box.
	edge := `{"concept":"arrow","x1":1199,"y1":519,"x2":1199,"y2":519,"confidence":0}`
	if g := FromMetadata(meta(image, "1", `,"boxes":[`+edge+`]`)); g == nil || g.Segmenter == nil || len(g.Segmenter.Boxes) != 1 {
		t.Errorf("the last pixel: %+v", g)
	}

	for name, c := range map[string]struct{ image, segments, boxes string }{
		"stored before the boxes were kept": {image, "2", ``},
		"no box was found":                  {image, "0", `,"boxes":[]`},
		"fewer boxes than objects":          {image, "2", `,"boxes":[` + box1 + `]`},
		"more boxes than objects":           {image, "1", `,"boxes":[` + box1 + `,` + box2 + `]`},
		"no image to frame them":            {``, "2", `,"boxes":[` + box1 + `,` + box2 + `]`},
		"an image too wide":                 {`"image":{"width":4097,"height":520},`, "2", `,"boxes":[` + box1 + `,` + box2 + `]`},
		"past the right edge":               {image, "1", `,"boxes":[{"concept":"arrow","x1":0,"y1":0,"x2":1200,"y2":10,"confidence":0.5}]`},
		"past the bottom edge":              {image, "1", `,"boxes":[{"concept":"arrow","x1":0,"y1":0,"x2":10,"y2":520,"confidence":0.5}]`},
		"a negative corner":                 {image, "1", `,"boxes":[{"concept":"arrow","x1":-1,"y1":0,"x2":10,"y2":10,"confidence":0.5}]`},
		"corners out of order":              {image, "1", `,"boxes":[{"concept":"arrow","x1":20,"y1":0,"x2":10,"y2":10,"confidence":0.5}]`},
		"a confidence over 1":               {image, "1", `,"boxes":[{"concept":"arrow","x1":0,"y1":0,"x2":10,"y2":10,"confidence":1.5}]`},
		"a negative confidence":             {image, "1", `,"boxes":[{"concept":"arrow","x1":0,"y1":0,"x2":10,"y2":10,"confidence":-0.1}]`},
		"a concept in capitals":             {image, "1", `,"boxes":[{"concept":"Arrow","x1":0,"y1":0,"x2":10,"y2":10,"confidence":0.5}]`},
		"a concept with markup":             {image, "1", `,"boxes":[{"concept":"<b>arrow</b>","x1":0,"y1":0,"x2":10,"y2":10,"confidence":0.5}]`},
		"a list of concepts":                {image, "1", `,"boxes":[{"concept":"arrow, belt","x1":0,"y1":0,"x2":10,"y2":10,"confidence":0.5}]`},
		"an empty concept":                  {image, "1", `,"boxes":[{"concept":"","x1":0,"y1":0,"x2":10,"y2":10,"confidence":0.5}]`},
		"a concept too long":                {image, "1", `,"boxes":[{"concept":"` + strings.Repeat("a", 65) + `","x1":0,"y1":0,"x2":10,"y2":10,"confidence":0.5}]`},
		"a box that is not an object":       {image, "1", `,"boxes":[7]`},
		"boxes that are not a list":         {image, "1", `,"boxes":{"concept":"arrow"}`},
	} {
		t.Run(name, func(t *testing.T) {
			g := FromMetadata(meta(c.image, c.segments, c.boxes))
			if g == nil || g.Segmenter == nil || g.Segmenter.Boxes != nil || g.Segmenter.Provider != "meta" || g.Model != "muse-spark-1.3" {
				t.Fatalf("got %+v", g)
			}
		})
	}
}
