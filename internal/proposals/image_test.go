package proposals

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/proposals/photo"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
)

// Review round 1, L4: the photo requests of one key run one after the
// other in this process (the later ones then find the key used and store
// nothing); other keys do not wait; a request waits only as long as its
// context allows, and the table keeps no key nobody holds or waits for.
func TestImageKeysRunOneAfterTheOther(t *testing.T) {
	var l keyLocks
	k := idempotency.Key{Org: "org_a", Actor: "usr_a", Operation: createImageOperation, Key: "same"}
	release, err := l.lock(context.Background(), k)
	if err != nil {
		t.Fatal(err)
	}
	other, err := l.lock(context.Background(), idempotency.Key{Org: "org_a", Actor: "usr_a", Operation: createImageOperation, Key: "other"})
	if err != nil {
		t.Fatalf("another key waited: %v", err)
	}
	other()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := l.lock(ctx, k); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a second request with the key did not wait: %v", err)
	}
	got := make(chan error, 1)
	go func() {
		r, err := l.lock(context.Background(), k)
		if err == nil {
			r()
		}
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("the waiting request ran before the first released the key: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	release()
	if err := <-got; err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.held) != 0 {
		t.Errorf("keys kept: %v", l.held)
	}
}

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
		photo.ErrEncoding:     {httpapi.UnsupportedMediaType, CodeImageFormatRejected},
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

// Review round 1, M2: a file that is no PNG or JPEG and one of a kind the
// server does not decode are both 415 IMAGE_FORMAT_REJECTED at /file, each
// with its own message and reason: openapi.yaml's createImageProposal 415
// examples.
func TestRefusedFormatsAreTheContracts(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "..", "openapi", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]bool{}
	for _, err := range []error{photo.ErrFormat, photo.ErrEncoding} {
		var he *httpapi.Error
		if !errors.As(Refused(err), &he) || he.Code != httpapi.UnsupportedMediaType || len(he.Details) != 1 {
			t.Fatalf("%v: %+v", err, Refused(err))
		}
		reasons[he.Details[0].Reason] = true
		for _, line := range []string{"code: " + string(he.Code), "message: " + he.Message, "- path: " + he.Details[0].Path,
			"reason: " + he.Details[0].Reason, "code: " + he.Details[0].Code} {
			if !strings.Contains(string(spec), "\n                      "+line+"\n") && !strings.Contains(string(spec), "\n                        "+line+"\n") {
				t.Errorf("%v: openapi.yaml's createImageProposal examples have no %q", err, line)
			}
		}
	}
	if len(reasons) != 2 {
		t.Error("the two refusals give one reason")
	}
}

// The T-056 follow-up (D-124): a photo refused because this deployment's
// photo path is off is 503 with the code the proposals' availability would
// have (AI_PROVIDER_NOT_CONFIGURED, or AI_POLICY_NOT_APPROVED for a live
// provider that is not on), and details[0].code AI_IMAGE_UNSUPPORTED, the
// code the worker would fail the photo with; a proposal in words refused
// for AI is as before, without details. The bodies are openapi.yaml's
// createImageProposal 503 examples.
func TestRefusedPhotoPathOff(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "..", "openapi", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for a, want := range map[Availability]httpapi.Code{NotConfigured: httpapi.AIProviderNotConfigured, PolicyNotApproved: httpapi.AIPolicyNotApproved} {
		var he *httpapi.Error
		if !errors.As(Refused(&UnavailableError{Availability: a, Photo: true}), &he) || he.Code != want {
			t.Errorf("photos, %v: %v, want %s", a, Refused(&UnavailableError{Availability: a, Photo: true}), want)
			continue
		}
		if len(he.Details) != 1 || he.Details[0].Path != "" || he.Details[0].Code != CodeImageUnsupported || he.Details[0].Reason == "" {
			t.Errorf("photos, %v: details %+v", a, he.Details)
			continue
		}
		for _, line := range []string{"code: " + string(he.Code), "message: " + he.Message, "reason: " + he.Details[0].Reason, "code: " + CodeImageUnsupported} {
			if !strings.Contains(string(spec), "\n                      "+line+"\n") && !strings.Contains(string(spec), "\n                        "+line+"\n") {
				t.Errorf("photos, %v: openapi.yaml's createImageProposal examples have no %q", a, line)
			}
		}
		// The proposals in words are refused as they were.
		if !errors.As(Refused(&UnavailableError{Availability: a}), &he) || he.Code != want || len(he.Details) != 0 {
			t.Errorf("words, %v: %+v", a, he)
		}
	}
	if CodeImageUnsupported != "AI_IMAGE_UNSUPPORTED" {
		t.Errorf("CodeImageUnsupported is %q", CodeImageUnsupported)
	}
	// A service that was not told otherwise takes no photo (fail closed),
	// whatever it says of proposals in words.
	if (Service{AI: Available}).Photo != NotConfigured {
		t.Error("the zero photo path takes photos")
	}
}
