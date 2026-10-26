package contracttest

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// The photo pipeline's demo fixtures (T-056, testdata/photos): a synthetic
// floor plan drawn by scripts/gen-photo-demo, the segments the fake
// segmenter answers for it (T-055's provider.Segment, as JSON), the base
// model a photo proposal builds on, and the fake vision model's answer
// with placeholders for the proposal's ID pool.

// PhotoBox is a box in the image's pixels, its corners inclusive.
type PhotoBox struct {
	X1 int `json:"x1"`
	Y1 int `json:"y1"`
	X2 int `json:"x2"`
	Y2 int `json:"y2"`
}

// PhotoSegment is one object of a segments file.
type PhotoSegment struct {
	Concept    string   `json:"concept"`
	ObjectID   int      `json:"object_id"`
	Box        PhotoBox `json:"box"`
	Confidence float64  `json:"confidence"`
}

// PhotoSegments is a segments file (<name>.segments.json).
type PhotoSegments struct {
	Comment      string         `json:"$comment"`
	Image        string         `json:"image"`
	MediaType    string         `json:"media_type"`
	Width        int            `json:"width"`
	Height       int            `json:"height"`
	UploadDigest string         `json:"upload_digest"`
	StoredDigest string         `json:"stored_digest"`
	Concepts     []string       `json:"concepts"`
	Segments     []PhotoSegment `json:"segments"`
}

// ByConcept is the file's segments of each concept, in order.
func (s PhotoSegments) ByConcept() map[string][]PhotoSegment {
	out := map[string][]PhotoSegment{}
	for _, x := range s.Segments {
		out[x.Concept] = append(out[x.Concept], x)
	}
	return out
}

// PhotoDemo is one demo image: the PNG as uploaded and its segments file.
type PhotoDemo struct {
	Image    []byte
	Segments PhotoSegments
}

func photos(t testing.TB) string { return filepath.Join(Root(t), "testdata", "photos") }

// LoadPhotoDemo reads the demo image name ("electronics-line-demo",
// "electronics-line-injection") and its segments.
func LoadPhotoDemo(t testing.TB, name string) PhotoDemo {
	t.Helper()
	var d PhotoDemo
	d.Image = ReadFile(t, filepath.Join(photos(t), name+".png"))
	if err := json.Unmarshal(ReadFile(t, filepath.Join(photos(t), name+".segments.json")), &d.Segments, json.RejectUnknownMembers(true)); err != nil {
		t.Fatal(err)
	}
	return d
}

// BareLine is the base model of the photo demo (testdata/photos/bare-line.json)
// as it is on disk.
func BareLine(t testing.TB) []byte {
	t.Helper()
	return ReadFile(t, filepath.Join(photos(t), "bare-line.json"))
}

var placeholder = regexp.MustCompile(`^\$(node|flow|resource):([0-9]+)$`)

// PhotoAnswer is the fake vision model's answer for the demo image name
// (<name>.proposal.json), with each "$node:<n>", "$flow:<n>" and
// "$resource:<n>" string replaced by the n-th ID of that kind in pool
// (idpool.Pool's lists by kind).
func PhotoAnswer(t testing.TB, name string, pool map[string][]string) []byte {
	t.Helper()
	var doc struct {
		Comment string         `json:"$comment"`
		Answer  jsontext.Value `json:"answer"`
	}
	if err := json.Unmarshal(ReadFile(t, filepath.Join(photos(t), name+".proposal.json")), &doc, json.RejectUnknownMembers(true)); err != nil {
		t.Fatal(err)
	}
	var answer any
	if err := json.Unmarshal(doc.Answer, &answer); err != nil {
		t.Fatal(err)
	}
	var fill func(v any) any
	fill = func(v any) any {
		switch v := v.(type) {
		case string:
			if m := placeholder.FindStringSubmatch(v); m != nil {
				n, _ := strconv.Atoi(m[2])
				ids := pool[m[1]]
				if n >= len(ids) {
					t.Fatalf("%s: no %s ID %d in the pool", name, m[1], n)
				}
				return ids[n]
			}
			return v
		case []any:
			for i := range v {
				v[i] = fill(v[i])
			}
			return v
		case map[string]any:
			for k := range v {
				v[k] = fill(v[k])
			}
			return v
		}
		return v
	}
	out, err := json.Marshal(fill(answer), json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return out
}
