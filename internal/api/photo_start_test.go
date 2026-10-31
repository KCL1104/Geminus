package api_test

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/tenant"
)

// H-062: a new model starts from a photo or floor plan, as the new-model
// page does it, through the whole stack: an editor creates a model from the
// electronics line pack's photo_start template over HTTP (createModel), then
// sends the demo floor plan as a photo proposal on its version 1
// (createImageProposal); the worker runs the fake segmenter and the fake
// vision model, the server reviews the answer and it is ready; a person
// approves it and version 2 has the stations the photo shows. A viewer may
// not create the model; photo_start under another domain is refused by the
// contract; the same key replays the create. No live AI. Without
// GEMINUS_TEST_DATABASE_URL it skips (NOT RUN).
func TestAModelStartedFromAPhoto(t *testing.T) {
	e := setupPhotos(t)
	editor, viewer := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	r := e.create(editor.Subject, e.a.ID, "key-"+t.Name()+"-prj", `{"name":"Photos"}`)
	if r.status != 201 {
		t.Fatalf("project: %d %s", r.status, r.body)
	}
	models := "/v1/projects/" + decode[project](t, r).ID + "/models"
	r = e.do("POST", models, editor.Subject, e.a.ID, map[string]string{"Idempotency-Key": "key-" + t.Name() + "-other"},
		`{"name":"x","domain":"production_logistics","template":"photo_start"}`)
	if r.status != 422 || code(t, r) != "REQUEST_VALIDATION_FAILED" {
		t.Errorf("photo_start under another domain: %d %s", r.status, r.body)
	}
	body := `{"name":"Line from a photo","domain":"electronics_line","template":"photo_start","locale":"en"}`
	r = e.do("POST", models, viewer.Subject, e.a.ID, map[string]string{"Idempotency-Key": "key-" + t.Name() + "-viewer"}, body)
	if r.status != 403 || code(t, r) != "FORBIDDEN" {
		t.Errorf("a viewer: %d %s", r.status, r.body)
	}
	key := map[string]string{"Idempotency-Key": "key-" + t.Name() + "-mdl"}
	r = e.do("POST", models, editor.Subject, e.a.ID, key, body)
	if r.status != 201 {
		t.Fatalf("model: %d %s", r.status, r.body)
	}
	valid(t, r, "http.schema.json#/$defs/Model")
	m := decode[modelSummary](t, r)
	if again := e.do("POST", models, editor.Subject, e.a.ID, key, body); again.status != 201 || decode[modelSummary](t, again).ID != m.ID {
		t.Errorf("the replay: %d %s", again.status, again.body)
	}
	if m.Name != "Line from a photo" || m.LatestVersion.Version != 1 {
		t.Fatalf("model %+v", m)
	}
	d := e.do("GET", "/v1/models/"+m.ID+"/draft", viewer.Subject, e.a.ID, nil, "")
	if d.status != 200 {
		t.Fatalf("draft: %d %s", d.status, d.body)
	}
	valid(t, d, "http.schema.json#/$defs/Draft")
	draft := decode[struct {
		IR struct {
			Domain string `json:"domain"`
			Nodes  []struct {
				ID string `json:"id"`
			} `json:"nodes"`
			Metadata map[string]any `json:"metadata"`
		} `json:"ir"`
		Issues []jsontext.Value `json:"issues"`
	}](t, d)
	var nodes []string
	for _, n := range draft.IR.Nodes {
		nodes = append(nodes, n.ID)
	}
	if draft.IR.Domain != "electronics_line" || len(draft.Issues) != 0 || strings.Join(nodes, " ") != "release q_smt smt shipped" ||
		draft.IR.Metadata["synthetic"] != true || draft.IR.Metadata["template"] != "photo_start" {
		t.Errorf("draft %q, issues %s, nodes %v, metadata %v", draft.IR.Domain, draft.Issues, nodes, draft.IR.Metadata)
	}

	// The page's second call: the photo on the new model's version 1.
	demo := contracttest.LoadPhotoDemo(t, "electronics-line-demo")
	e.demoSegments(demo, -1, demo.Segments.Width, demo.Segments.Height)
	r = e.photo(t, editor.Subject, e.a.ID, m.ID, "photo-key-"+t.Name(), photoParts(m.LatestVersion.ID, "en", demo.Image))
	if r.status != 202 {
		t.Fatalf("photo: %d %s", r.status, r.body)
	}
	valid(t, r, "http.schema.json#/$defs/ProposalAccepted")
	id := decode[struct {
		ProposalID string `json:"proposal_id"`
	}](t, r).ProposalID
	e.vision.Script(provider.RoleModeler, demo.Segments.StoredDigest, contracttest.PhotoAnswer(t, "electronics-line-demo", e.poolOf(t, id)))
	p := e.generatedPhoto(t, editor.Subject, e.a.ID, id)
	if p.Status != "ready" || p.Mode != "edit" || p.SourceImage == nil || p.SourceImage.ContentHash != demo.Segments.StoredDigest || p.ServerRiskLevel == nil {
		t.Fatalf("%+v %v", p, p.Validation.Issues)
	}
	a := e.do("POST", "/v1/ai-proposals/"+id+"/approve", editor.Subject, e.a.ID, map[string]string{"Idempotency-Key": "approve-" + t.Name()},
		fmt.Sprintf(`{"expected_base_version_id":%q}`, m.LatestVersion.ID))
	if a.status != 201 {
		t.Fatalf("approve: %d %s", a.status, a.body)
	}
	var ir string
	if err := e.admin.QueryRow(context.Background(), `SELECT v.ir FROM twin.model_versions v JOIN twin.models m ON m.latest_version_id=v.id
		WHERE m.id=$1 AND v.version=2`, m.ID).Scan(&ir); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`"station":"smt"`, `"station":"aoi"`, `"station":"ict"`, `"station":"fct"`, `"station":"pack"`, `"capacity":2`} {
		if !strings.Contains(ir, s) {
			t.Errorf("version 2 has no %s", s)
		}
	}
}
