package api_test

// H-052: what SAM 3.1 found, drawn over the photo on the proposal page,
// through the whole stack on the disposable database (as photo_test.go):
// getProposal's generated_by.segmenter.boxes, the boxes the worker kept,
// and getProposalImage, the stored photo for any member of the proposal's
// organization, after the server checked it against its record. Without
// GEMINUS_TEST_DATABASE_URL they skip (NOT RUN).

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/tenant"
)

type boxView struct {
	Concept    string  `json:"concept"`
	X1         int     `json:"x1"`
	Y1         int     `json:"y1"`
	X2         int     `json:"x2"`
	Y2         int     `json:"y2"`
	Confidence float64 `json:"confidence"`
}

func TestThePhotoAndItsBoxes(t *testing.T) {
	e := setupPhotos(t)
	editor, viewer := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	outsider := e.b.Members[tenant.Owner]
	mdl, mv := e.bareLine(t, e.a)
	demo := contracttest.LoadPhotoDemo(t, "electronics-line-demo")
	e.demoSegments(demo, -1, demo.Segments.Width, demo.Segments.Height)
	r := e.photo(t, editor.Subject, e.a.ID, mdl, ids.New("key"), photoParts(mv, "", demo.Image))
	if r.status != 202 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	id := decode[struct {
		ProposalID string `json:"proposal_id"`
	}](t, r).ProposalID
	path := "/v1/ai-proposals/" + id + "/image"

	served := func(t *testing.T, r resp) {
		t.Helper()
		h := r.header
		if r.status != 200 || !bytes.Equal(r.body, demo.Image) || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("Cache-Control") != "no-store" || h.Get("Content-Length") != fmt.Sprint(len(demo.Image)) {
			t.Fatalf("%d %v, %d bytes", r.status, h, len(r.body))
		}
		if id := h.Get("X-Request-ID"); len(id) != 30 {
			t.Errorf("X-Request-ID %q", id)
		}
	}
	// While the AI works the photo is there, for any member: a viewer too.
	served(t, e.do("GET", path, viewer.Subject, e.a.ID, nil, ""))

	e.vision.Script(provider.RoleModeler, demo.Segments.StoredDigest, contracttest.PhotoAnswer(t, "electronics-line-demo", e.poolOf(t, id)))
	if p := e.generatedPhoto(t, editor.Subject, e.a.ID, id); p.Status != "ready" {
		t.Fatalf("%+v", p)
	}
	served(t, e.do("GET", path, editor.Subject, e.a.ID, nil, ""))

	// The boxes the segmenter found, in its order, as getProposal shows them.
	got := e.do("GET", "/v1/ai-proposals/"+id, viewer.Subject, e.a.ID, nil, "")
	valid(t, got, "proposal.schema.json")
	g := decode[struct {
		GeneratedBy struct {
			Segmenter struct {
				Objects int       `json:"objects"`
				Boxes   []boxView `json:"boxes"`
			} `json:"segmenter"`
		} `json:"generated_by"`
	}](t, got).GeneratedBy.Segmenter
	if g.Objects != 10 || len(g.Boxes) != 10 {
		t.Fatalf("%d objects, %d boxes", g.Objects, len(g.Boxes))
	}
	for i, s := range demo.Segments.Segments {
		want := boxView{Concept: s.Concept, X1: s.Box.X1, Y1: s.Box.Y1, X2: s.Box.X2, Y2: s.Box.Y2, Confidence: s.Confidence}
		if g.Boxes[i] != want {
			t.Errorf("box %d: %+v, want %+v", i, g.Boxes[i], want)
		}
	}
	if bytes.Contains(got.body, []byte("object_id")) {
		t.Error("an object ID is shown")
	}

	// Nobody outside the organization reads it, whichever organization
	// they name; no ID, no token, no photo.
	refused := func(t *testing.T, r resp, status int, want string) {
		t.Helper()
		if r.status != status || code(t, r) != want || bytes.Contains(r.body, []byte("PNG")) {
			t.Errorf("%d %s, want %d %s", r.status, r.body, status, want)
		}
	}
	refused(t, e.do("GET", path, outsider.Subject, e.b.ID, nil, ""), 404, "NOT_FOUND")
	refused(t, e.do("GET", path, outsider.Subject, e.a.ID, nil, ""), 404, "NOT_FOUND")
	refused(t, e.do("GET", path, "", e.a.ID, nil, ""), 401, "UNAUTHENTICATED")
	refused(t, e.do("GET", "/v1/ai-proposals/"+ids.New("aip")+"/image", editor.Subject, e.a.ID, nil, ""), 404, "NOT_FOUND")
	refused(t, e.do("GET", "/v1/ai-proposals/not-an-id/image", editor.Subject, e.a.ID, nil, ""), 400, "INVALID_REQUEST")

	// A proposal asked for in words has no photo.
	words := e.do("POST", "/v1/models/"+mdl+"/ai-proposals", editor.Subject, e.a.ID, map[string]string{"Idempotency-Key": ids.New("key")},
		fmt.Sprintf(`{"base_version_id":%q,"mode":"edit","intent":"Add a second AOI machine."}`, mv))
	if words.status != 202 {
		t.Fatalf("words: %d %s", words.status, words.body)
	}
	wordsID := decode[struct {
		ProposalID string `json:"proposal_id"`
	}](t, words).ProposalID
	refused(t, e.do("GET", "/v1/ai-proposals/"+wordsID+"/image", editor.Subject, e.a.ID, nil, ""), 404, "NOT_FOUND")

	// Bytes changed in storage after they were recorded: a server error, and no byte of them.
	var key string
	if err := e.admin.QueryRow(context.Background(), `SELECT object_key FROM twin.ai_proposal_images WHERE proposal_id=$1`, id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(e.root, filepath.FromSlash(key))
	stored, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	stored[len(stored)-1] ^= 0xff
	if err := os.WriteFile(file, stored, 0o600); err != nil {
		t.Fatal(err)
	}
	refused(t, e.do("GET", path, editor.Subject, e.a.ID, nil, ""), 500, "INTERNAL_ERROR")
}
