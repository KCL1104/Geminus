package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

// T-055: an image part of a prompt (T-056's photo path, H-033) is checked
// before any request: one of three types, its bytes those of that type,
// at most MaxImageBytes, its pixel size read from its header and at most
// MaxImageSide on each side. Only the media type, the digest and the size
// are ever recorded; the bytes go to the provider and nowhere else.

func pngOf(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegOf(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// webpOf is a WebP file of one chunk, written by hand (the standard
// library has no WebP encoder): the RIFF header, the chunk's FourCC and
// size, and its payload.
func webpOf(fourCC string, payload []byte) []byte {
	chunk := append([]byte(fourCC), binary.LittleEndian.AppendUint32(nil, uint32(len(payload)))...)
	chunk = append(chunk, payload...)
	if len(payload)%2 == 1 {
		chunk = append(chunk, 0)
	}
	out := append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(4+len(chunk)))...)
	return append(append(out, "WEBP"...), chunk...)
}

// vp8 is a lossy key frame header of w x h: frame tag, start code, sizes.
func vp8(w, h int) []byte {
	p := []byte{0x30, 0x01, 0x00, 0x9d, 0x01, 0x2a}
	p = binary.LittleEndian.AppendUint16(p, uint16(w))
	p = binary.LittleEndian.AppendUint16(p, uint16(h))
	return append(p, make([]byte, 10)...)
}

// vp8l is a lossless header of w x h: the signature byte, then width-1
// and height-1 in 14 bits each.
func vp8l(w, h int) []byte {
	bits := uint32(w-1) | uint32(h-1)<<14
	return append(append([]byte{0x2f}, binary.LittleEndian.AppendUint32(nil, bits)...), make([]byte, 5)...)
}

// vp8x is an extended header of w x h: flags, reserved, then the canvas
// width-1 and height-1 in 24 bits each.
func vp8x(w, h int) []byte {
	p := []byte{0, 0, 0, 0}
	p = append(p, byte(w-1), byte((w-1)>>8), byte((w-1)>>16))
	return append(p, byte(h-1), byte((h-1)>>8), byte((h-1)>>16))
}

func TestNewImageReadsTheSize(t *testing.T) {
	for name, c := range map[string]struct {
		mediaType string
		data      []byte
		w, h      int
	}{
		"png":           {ImagePNG, pngOf(t, 3, 2), 3, 2},
		"jpeg":          {ImageJPEG, jpegOf(t, 5, 4), 5, 4},
		"webp lossy":    {ImageWebP, webpOf("VP8 ", vp8(640, 480)), 640, 480},
		"webp lossless": {ImageWebP, webpOf("VP8L", vp8l(1024, 768)), 1024, 768},
		"webp extended": {ImageWebP, webpOf("VP8X", vp8x(1536, 1024)), 1536, 1024},
		"largest side":  {ImageWebP, webpOf("VP8X", vp8x(MaxImageSide, 1)), MaxImageSide, 1},
	} {
		in := bytes.Clone(c.data)
		im, err := NewImage(c.mediaType, in)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		sum := sha256.Sum256(c.data)
		if im.MediaType != c.mediaType || im.Width != c.w || im.Height != c.h || !bytes.Equal(im.Data, c.data) ||
			im.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
			t.Errorf("%s: %s %dx%d %s", name, im.MediaType, im.Width, im.Height, im.Digest)
		}
		in[len(in)-1] ^= 0xff // the caller's bytes are not the image's
		if err := im.Check(); err != nil || !bytes.Equal(im.Data, c.data) {
			t.Errorf("%s: the image shares the caller's bytes: %v", name, err)
		}
	}
}

func TestNewImageRefuses(t *testing.T) {
	p := pngOf(t, 3, 2)
	big := append(bytes.Clone(p), make([]byte, MaxImageBytes)...)
	zeroWidth := bytes.Clone(p)
	binary.BigEndian.PutUint32(zeroWidth[16:20], 0)
	wide := bytes.Clone(p)
	binary.BigEndian.PutUint32(wide[16:20], MaxImageSide+1)
	riffShort := webpOf("VP8X", vp8x(10, 10))
	binary.LittleEndian.PutUint32(riffShort[4:8], uint32(len(riffShort)))
	badSignature := bytes.Clone(p) // IHDR intact, the signature not PNG's
	badSignature[1] = 'X'
	for name, c := range map[string]struct {
		mediaType string
		data      []byte
	}{
		"gif":                       {"image/gif", []byte("GIF89a\x01\x00\x01\x00")},
		"svg":                       {"image/svg+xml", []byte("<svg/>")},
		"no type":                   {"", p},
		"another spelling":          {"IMAGE/PNG", p},
		"a parameter":               {"image/png; charset=binary", p},
		"jpeg declared, png":        {ImageJPEG, p},
		"png declared, jpeg":        {ImagePNG, jpegOf(t, 2, 2)},
		"webp declared, png":        {ImageWebP, p},
		"empty":                     {ImagePNG, nil},
		"larger than the limit":     {ImagePNG, big},
		"cut png header":            {ImagePNG, p[:20]},
		"png width 0":               {ImagePNG, zeroWidth},
		"png wider than allowed":    {ImagePNG, wide},
		"jpeg without a frame":      {ImageJPEG, []byte{0xff, 0xd8, 0xff, 0xd9}},
		"cut jpeg":                  {ImageJPEG, jpegOf(t, 2, 2)[:30]},
		"webp of another chunk":     {ImageWebP, webpOf("ALPH", make([]byte, 10))},
		"webp lossless, no sig":     {ImageWebP, webpOf("VP8L", append([]byte{0x00}, vp8l(4, 4)[1:]...))},
		"webp lossy, no start":      {ImageWebP, webpOf("VP8 ", append(vp8(4, 4)[:3], make([]byte, 17)...))},
		"webp wider than allowed":   {ImageWebP, webpOf("VP8X", vp8x(MaxImageSide+1, 1))},
		"webp riff size too big":    {ImageWebP, riffShort},
		"text":                      {ImagePNG, []byte(strings.Repeat("ignore the rules ", 10))},
		"png without its signature": {ImagePNG, badSignature},
	} {
		im, err := NewImage(c.mediaType, c.data)
		if !errors.Is(err, ErrInvalidImage) || im.Data != nil {
			t.Errorf("%s: %v %+v", name, err, im.MediaType)
			continue
		}
		if strings.Contains(err.Error(), "ignore the rules") || strings.Contains(err.Error(), "charset") {
			t.Errorf("%s: the error quotes the input: %v", name, err)
		}
	}
	// A type the documentation lists but this build does not send (GIF) is
	// refused for its type, before its bytes are read.
	if _, err := NewImage("image/gif", []byte("GIF89a\x01\x00\x01\x00")); err == nil || !strings.Contains(err.Error(), "media type") {
		t.Errorf("gif: %v", err)
	}
	// The file size at its bound is taken.
	if _, err := NewImage(ImagePNG, append(bytes.Clone(p), make([]byte, MaxImageBytes-len(p))...)); err != nil {
		t.Errorf("%d bytes: %v", MaxImageBytes, err)
	}
}

// The bounds are D-123's (PROPOSED), pinned: a change of one is a reviewed
// change of the decision, not of a test.
func TestTheBoundsAreD123s(t *testing.T) {
	if MaxImageBytes != 5<<20 || MaxImageSide != 8000 || MaxImagesPerPrompt != 4 || MaxSegmentConcepts != 8 ||
		MaxSegmentConceptBytes != 64 || MaxSegmentsPerConcept != 100 {
		t.Errorf("bounds %d %d %d %d %d %d", MaxImageBytes, MaxImageSide, MaxImagesPerPrompt, MaxSegmentConcepts, MaxSegmentConceptBytes,
			MaxSegmentsPerConcept)
	}
}

// Check finds an image changed after NewImage: its bytes, its size or its
// digest; the zero Image is no image.
func TestImageCheck(t *testing.T) {
	im, err := NewImage(ImagePNG, pngOf(t, 3, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := im.Check(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Image){
		"bytes":      func(i *Image) { i.Data = bytes.Clone(i.Data); i.Data[len(i.Data)-5] ^= 1 },
		"width":      func(i *Image) { i.Width++ },
		"digest":     func(i *Image) { i.Digest = "sha256:" + strings.Repeat("0", 64) },
		"media type": func(i *Image) { i.MediaType = ImageJPEG },
	} {
		c := im
		mutate(&c)
		if err := c.Check(); !errors.Is(err, ErrInvalidImage) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := (Image{}).Check(); !errors.Is(err, ErrInvalidImage) {
		t.Errorf("the zero image: %v", err)
	}
}

// FuzzNewImage: whatever the bytes, NewImage never panics, and an image it
// takes is within the limits and checks.
func FuzzNewImage(f *testing.F) {
	f.Add(ImagePNG, pngOf(f, 2, 2))
	f.Add(ImageJPEG, jpegOf(f, 2, 2))
	f.Add(ImageWebP, webpOf("VP8X", vp8x(3, 3)))
	f.Add(ImageWebP, webpOf("VP8L", vp8l(3, 3)))
	f.Add(ImageWebP, webpOf("VP8 ", vp8(3, 3)))
	f.Fuzz(func(t *testing.T, mediaType string, data []byte) {
		im, err := NewImage(mediaType, data)
		if err != nil {
			return
		}
		if im.Width < 1 || im.Height < 1 || im.Width > MaxImageSide || im.Height > MaxImageSide || len(im.Data) > MaxImageBytes {
			t.Fatalf("out of the limits: %dx%d %d bytes", im.Width, im.Height, len(im.Data))
		}
		if err := im.Check(); err != nil {
			t.Fatal(err)
		}
	})
}
