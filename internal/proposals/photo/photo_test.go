package photo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"testing"
)

// picture is a w×h image with a few coloured bars, so that a re-encoded
// copy can be told from a blank one.
func picture(w, h int) *image.RGBA {
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{R: uint8(x * 7), G: uint8(y * 5), B: 200, A: 255}
			if (x/16)%2 == 0 {
				c = color.RGBA{R: 30, G: 120, B: uint8(x), A: 255}
			}
			im.Set(x, y, c)
		}
	}
	return im
}

func pngOf(t testing.TB, im image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegOf(t testing.TB, im image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, im, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withChunk inserts a PNG chunk before IEND.
func withChunk(data []byte, kind string, payload []byte) []byte {
	iend := bytes.LastIndex(data, []byte("IEND")) - 4
	var c bytes.Buffer
	_ = binary.Write(&c, binary.BigEndian, uint32(len(payload)))
	c.WriteString(kind)
	c.Write(payload)
	_ = binary.Write(&c, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(kind), payload...)))
	out := append([]byte{}, data[:iend]...)
	out = append(out, c.Bytes()...)
	return append(out, data[iend:]...)
}

// withSegment inserts a JPEG marker segment right after SOI.
func withSegment(data []byte, marker byte, payload []byte) []byte {
	seg := []byte{0xff, marker, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	out := append([]byte{}, data[:2]...)
	out = append(out, seg...)
	out = append(out, payload...)
	return append(out, data[2:]...)
}

const secret = "GPS 25.0330N 121.5654E owner@example.com"

// The stored image is the pixels and nothing else: no text chunk, EXIF,
// colour profile, comment or byte after the end of the file survives, the
// format and size stay, and the pixels decode the same (PNG is lossless).
func TestSanitizeKeepsOnlyThePixels(t *testing.T) {
	ctx := context.Background()
	src := picture(120, 64)
	dirtyPNG := withChunk(withChunk(withChunk(pngOf(t, src), "tEXt", []byte("Comment\x00"+secret)), "eXIf", []byte("MM\x00*"+secret)),
		"iCCP", []byte("profile\x00\x00"+secret))
	dirtyPNG = append(dirtyPNG, []byte("<script>"+secret+"</script>")...)
	exif := append([]byte("Exif\x00\x00MM\x00*"), []byte(secret)...)
	dirtyJPEG := withSegment(withSegment(jpegOf(t, src), 0xe1, exif), 0xfe, []byte(secret))
	dirtyJPEG = append(dirtyJPEG, []byte("PK\x03\x04"+secret)...)
	for name, c := range map[string]struct {
		data      []byte
		mediaType string
	}{"png": {dirtyPNG, PNG}, "jpeg": {dirtyJPEG, JPEG}} {
		im, err := Sanitize(ctx, c.data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if im.MediaType != c.mediaType || im.Width != 120 || im.Height != 64 {
			t.Errorf("%s: %s %dx%d", name, im.MediaType, im.Width, im.Height)
		}
		for _, leak := range []string{secret, "GPS", "Exif", "eXIf", "tEXt", "iCCP", "<script>", "PK\x03\x04"} {
			if bytes.Contains(im.Data, []byte(leak)) {
				t.Errorf("%s: the stored image still holds %q", name, leak)
			}
		}
		if Format(im.Data) != c.mediaType {
			t.Errorf("%s: stored as %q", name, Format(im.Data))
		}
		if im.SHA256 != "sha256:"+hexSum(im.Data) || im.Size() != int64(len(im.Data)) {
			t.Errorf("%s: hash or size of the stored bytes: %s %d", name, im.SHA256, im.Size())
		}
		decoded, _, err := image.Decode(bytes.NewReader(im.Data))
		if err != nil || decoded.Bounds() != src.Bounds() {
			t.Fatalf("%s: the stored image does not decode: %v", name, err)
		}
		if name == "png" {
			for _, p := range []image.Point{{0, 0}, {17, 3}, {119, 63}} {
				r1, g1, b1, a1 := decoded.At(p.X, p.Y).RGBA()
				r2, g2, b2, a2 := src.At(p.X, p.Y).RGBA()
				if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
					t.Errorf("png: pixel %v changed", p)
				}
			}
		}
		// The same upload is always stored as the same bytes.
		again, err := Sanitize(ctx, c.data)
		if err != nil || again.SHA256 != im.SHA256 {
			t.Errorf("%s: not deterministic: %s %s %v", name, im.SHA256, again.SHA256, err)
		}
	}
}

// Only PNG and JPEG, told by the first bytes: a name or a declared type
// cannot make anything else one.
func TestFormatIsReadFromTheBytes(t *testing.T) {
	for name, data := range map[string][]byte{
		"gif":     []byte("GIF89a\x10\x00\x10\x00"),
		"webp":    []byte("RIFF\x24\x00\x00\x00WEBPVP8 "),
		"bmp":     []byte("BM\x36\x00\x00\x00"),
		"tiff":    []byte("II*\x00\x08\x00\x00\x00"),
		"svg":     []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"pdf":     []byte("%PDF-1.7\n"),
		"heic":    []byte("\x00\x00\x00\x18ftypheic"),
		"zip":     []byte("PK\x03\x04\x14\x00"),
		"elf":     []byte("\x7fELF\x02\x01\x01"),
		"text":    []byte("station,flow\nSMT,AOI\n"),
		"png sig": []byte("\x89PNG\r\n"), // cut before the signature ends
	} {
		if Format(data) != "" {
			t.Errorf("%s: read as %q", name, Format(data))
		}
		if _, err := Sanitize(context.Background(), data); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: %v, want ErrFormat", name, err)
		}
	}
	if Format(pngOf(t, picture(40, 40))) != PNG || Format(jpegOf(t, picture(40, 40))) != JPEG {
		t.Error("PNG or JPEG not recognised")
	}
}

func TestSanitizeRefuses(t *testing.T) {
	ctx := context.Background()
	if _, err := Sanitize(ctx, nil); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty: %v", err)
	}
	good := pngOf(t, picture(64, 64))
	if _, err := Sanitize(ctx, good[:len(good)/2]); !errors.Is(err, ErrInvalid) {
		t.Errorf("a cut PNG: %v", err)
	}
	j := jpegOf(t, picture(64, 64))
	if _, err := Sanitize(ctx, j[:len(j)/3]); !errors.Is(err, ErrInvalid) {
		t.Errorf("a cut JPEG: %v", err)
	}
	for name, size := range map[string]image.Point{"too narrow": {31, 64}, "too low": {64, 31}, "too wide": {MaxSide + 1, 32}, "too high": {32, MaxSide + 1}} {
		if _, err := Sanitize(ctx, pngOf(t, image.NewGray(image.Rect(0, 0, size.X, size.Y)))); !errors.Is(err, ErrPixels) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A header that claims a huge image is refused from the header, before
	// any pixel is decoded (a decompression bomb: the rest is garbage).
	bomb := append([]byte{}, good[:33]...)
	binary.BigEndian.PutUint32(bomb[16:], 60000)
	binary.BigEndian.PutUint32(bomb[20:], 60000)
	binary.BigEndian.PutUint32(bomb[29:], crc32.ChecksumIEEE(bomb[12:29]))
	bomb = append(bomb, bytes.Repeat([]byte{0}, 64)...)
	if _, err := Sanitize(ctx, bomb); !errors.Is(err, ErrPixels) {
		t.Errorf("a 60000x60000 header: %v", err)
	}
	if _, err := Sanitize(ctx, append(good, make([]byte, MaxBytes)...)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("more than MaxBytes: %v", err)
	}
	// A cancelled request is the context's error, whatever the bytes.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Sanitize(cctx, good); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

// Read takes at most MaxBytes and says so when there is more, whether or
// not the request said how long it is.
func TestRead(t *testing.T) {
	data, err := Read(strings.NewReader("abc"))
	if err != nil || string(data) != "abc" {
		t.Errorf("%q %v", data, err)
	}
	if _, err := Read(io.LimitReader(zeros{}, MaxBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("MaxBytes+1: %v", err)
	}
	if data, err := Read(io.LimitReader(zeros{}, MaxBytes)); err != nil || len(data) != MaxBytes {
		t.Errorf("MaxBytes: %d %v", len(data), err)
	}
}

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

// The worker forwards only bytes that are the record's: their hash and
// size, a PNG or JPEG of the recorded type and pixel size.
func TestVerify(t *testing.T) {
	im, err := Sanitize(context.Background(), pngOf(t, picture(80, 40)))
	if err != nil {
		t.Fatal(err)
	}
	rec := im.Record()
	if err := Verify(im.Data, rec); err != nil {
		t.Fatalf("the record's own bytes: %v", err)
	}
	flipped := append([]byte{}, im.Data...)
	flipped[len(flipped)/2] ^= 1
	for name, c := range map[string]struct {
		data []byte
		rec  Record
	}{
		"a changed byte":  {flipped, rec},
		"a short read":    {im.Data[:len(im.Data)-1], rec},
		"another type":    {im.Data, Record{MediaType: JPEG, Width: 80, Height: 40, Size: rec.Size, SHA256: rec.SHA256}},
		"another width":   {im.Data, Record{MediaType: PNG, Width: 81, Height: 40, Size: rec.Size, SHA256: rec.SHA256}},
		"another height":  {im.Data, Record{MediaType: PNG, Width: 80, Height: 41, Size: rec.Size, SHA256: rec.SHA256}},
		"no bytes at all": {nil, rec},
	} {
		if err := Verify(c.data, c.rec); !errors.Is(err, ErrMismatch) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
