package photo

import (
	"context"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
)

// T-056 on T-055: every image the server stores is one the provider client
// sends. The upload bounds are within provider.NewImage's, the types are
// two of its three, and a re-encoded image is a provider.Image with the
// same type, size and digest the record names.
func TestWhatIsStoredIsWhatTheProviderSends(t *testing.T) {
	if MaxBytes > provider.MaxImageBytes || MaxSide > provider.MaxImageSide {
		t.Fatalf("the upload bounds (%d bytes, %d px) exceed the provider's (%d bytes, %d px)", MaxBytes, MaxSide,
			provider.MaxImageBytes, provider.MaxImageSide)
	}
	if PNG != provider.ImagePNG || JPEG != provider.ImageJPEG {
		t.Fatalf("the media types are not the provider's")
	}
	ctx := context.Background()
	for name, data := range map[string][]byte{
		"png":  pngOf(t, picture(640, 360)),
		"jpeg": jpegOf(t, picture(640, 360)),
	} {
		im, err := Sanitize(ctx, data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		p, err := provider.NewImage(im.MediaType, im.Data)
		if err != nil {
			t.Fatalf("%s: the provider refuses a stored image: %v", name, err)
		}
		if p.Digest != im.SHA256 || p.Width != im.Width || p.Height != im.Height || p.MediaType != im.MediaType || int64(len(p.Data)) != im.Size() {
			t.Errorf("%s: %+v is not %+v", name, p.Digest, im.Record())
		}
	}
}
