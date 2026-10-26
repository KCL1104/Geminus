package proposals

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/proposals/photo"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
)

// T-056: a proposal asked for with a photo says so in every state, with
// what the server stored of the image (type, pixels, size, hash) and
// nothing else: no key, no file name.
func TestPublicShowsThePhoto(t *testing.T) {
	im := &store.Image{ObjectKey: "tenant/org_01M4YDQK00Y5H2929M9WWZWTVH/assets/proposal-images/aip_01M4YDQK00ADN261CME3WV5HZ6/image",
		SHA256: "sha256:" + strings.Repeat("7", 64), Size: 21843, MediaType: "image/png", Width: 1200, Height: 520}
	for _, s := range []store.Status{store.Generating, store.NeedsInput, store.Ready, store.Invalid, store.Failed, store.Approved, store.Rejected} {
		r := row(s)
		r.Intent, r.Image = "", im
		if s == store.Invalid {
			r.Validation = []byte(`{"valid":false,"issues":[{"code":"AI_OUTPUT_INVALID","path":"","message":"m","severity":"error"}]}`)
		}
		if s == store.NeedsInput {
			r.Patch = nil
		}
		p, err := Public(r, "")
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if !members(t, p)["source_image"] || p.SourceImage == nil || *p.SourceImage != (SourceImage{MediaType: "image/png", Width: 1200,
			Height: 520, SizeBytes: 21843, ContentHash: im.SHA256}) {
			t.Errorf("%s: %+v", s, p.SourceImage)
		}
	}
	if p, err := Public(row(store.Ready), ""); err != nil || p.SourceImage != nil || members(t, p)["source_image"] {
		t.Errorf("a proposal in words: %+v %v", p.SourceImage, err)
	}
}

// The photo's refusals have their contract codes, each pointing at the
// file with a code of its own.
func TestRefusedPhoto(t *testing.T) {
	for err, want := range map[error]struct {
		code   httpapi.Code
		detail string
	}{
		photo.ErrFormat:       {httpapi.UnsupportedMediaType, CodeImageFormatRejected},
		photo.ErrTooLarge:     {httpapi.PayloadTooLarge, ""},
		photo.ErrEmpty:        {httpapi.RequestValidationFailed, CodeImageInvalid},
		photo.ErrInvalid:      {httpapi.RequestValidationFailed, CodeImageInvalid},
		photo.ErrPixels:       {httpapi.RequestValidationFailed, CodeImagePixels},
		ErrStorageUnavailable: {httpapi.ServiceUnavailable, ""},
		ErrRetry:              {httpapi.ServiceUnavailable, ""},
	} {
		var he *httpapi.Error
		if !errors.As(Refused(err), &he) || he.Code != want.code {
			t.Errorf("%v: %v, want %s", err, Refused(err), want.code)
			continue
		}
		if want.detail != "" && (len(he.Details) != 1 || he.Details[0].Path != "/file" || he.Details[0].Code != want.detail) {
			t.Errorf("%v: details %+v", err, he.Details)
		}
	}
	if !slices.ContainsFunc(Routes, func(r Route) bool {
		return r.OperationID == "createImageProposal" && r.Method == "POST" && r.Path == "/v1/models/{id}/ai-proposals/image"
	}) {
		t.Error("no createImageProposal route")
	}
}
