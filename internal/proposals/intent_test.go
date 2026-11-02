package proposals

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/model/validate"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
)

// H-079: a proposal carries the request it was asked for with, in words,
// as the row stores it (intent), read-only and in every state, so the
// model page's earlier requests show the person's words and can put them
// back in the composer. A photo proposal was asked for without words
// (migration 0017: its intent is empty), so it has none, and listProposals
// keeps the words as getProposal shows them. Either way the resource is
// proposal.schema.json's.
func TestIntent(t *testing.T) {
	for _, s := range []store.Status{store.Generating, store.NeedsInput, store.Ready, store.Invalid, store.Failed, store.Approved, store.Rejected, store.Conflicted} {
		r := row(s)
		if s == store.Invalid {
			r.Validation = []byte(`{"valid":false,"issues":[{"code":"ID_POOL_VIOLATION","path":"/nodes/9/id","message":"m","severity":"error"}]}`)
		}
		if s == store.NeedsInput {
			r.Patch = nil
		}
		r.Intent = "Raise the tester capacity to 3.\nKeep the rest."
		p, err := Public(r, "")
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if p.Intent != r.Intent || listed(p).Intent != r.Intent {
			t.Errorf("%s: intent %q (listed %q), want the stored words", s, p.Intent, listed(p).Intent)
		}
		members(t, p)
	}
	photo := row(store.Generating)
	photo.Intent = ""
	photo.Image = &store.Image{ObjectKey: "tenant/org/assets/proposal-images/aip/image", SHA256: "sha256:" + strings.Repeat("1", 64), Size: 21843,
		MediaType: "image/png", Width: 1200, Height: 520}
	p, err := Public(photo, "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(listed(p))
	if err != nil {
		t.Fatal(err)
	}
	if p.Intent != "" || strings.Contains(string(data), `"intent"`) {
		t.Errorf("a photo proposal has words: %s", data)
	}
	if issues, err := validate.Document(data, "proposal.schema.json", "RESPONSE"); err != nil || len(issues) > 0 {
		t.Errorf("%s: %v %v", data, issues, err)
	}
	// The schema refuses words on a photo proposal: asked for one way or the other, never both.
	withWords := strings.Replace(string(data), `"mode":"edit"`, `"mode":"edit","intent":"Add these stations."`, 1)
	if issues, err := validate.Document([]byte(withWords), "proposal.schema.json", "RESPONSE"); err == nil && len(issues) == 0 {
		t.Errorf("words on a photo proposal pass the schema: %s", withWords)
	}
}
