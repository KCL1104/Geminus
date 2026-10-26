package photo

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand/v2"
	"runtime"
	"testing"
	"time"
)

// T-056 review round 1, M2 (D-130 (b), SECURITY §6): what one upload may
// make the server decode is bounded. The kinds of PNG and JPEG whose
// decoding takes several times the image (a progressive JPEG keeps every
// block's coefficients, a CMYK or YCCK JPEG a fourth plane and a copy, an
// RGB JPEG a copy, a 16-bit PNG 8 bytes a pixel, an interlaced PNG every
// pass beside the image) are refused from their headers, before any pixel
// is decoded; what is accepted decodes into at most 4 bytes a pixel; the
// re-encoding stops at MaxBytes; and at most cap(decodeSlots) decodes run
// at once. So one Sanitize allocates at most MaxDecodeBytes, and a process
// at most cap(decodeSlots) times that.

// allocated is what f allocates in all (runtime.MemStats.TotalAlloc, an
// upper bound of the heap it holds at its peak).
func allocated(f func()) uint64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// gradient is a w×h image of model m (Gray, NRGBA, RGBA, Gray16 or
// NRGBA64) that compresses well; its pixels are written directly, so that
// a 4096×4096 one is quick to make under the race detector too.
func gradient(w, h int, m color.Model) image.Image {
	r := image.Rect(0, 0, w, h)
	px := func(pix []uint8, stride, bytesPer int) {
		for y := 0; y < h; y++ {
			row := pix[y*stride : y*stride+w*bytesPer]
			for x := 0; x < w; x++ {
				v := [4]uint8{uint8(x >> 4), uint8(y >> 4), uint8((x + y) >> 5), 255 - uint8(x>>5)}
				for c := 0; c < bytesPer; c++ {
					row[x*bytesPer+c] = v[c%4]
				}
			}
		}
	}
	switch m {
	case color.GrayModel:
		im := image.NewGray(r)
		px(im.Pix, im.Stride, 1)
		return im
	case color.RGBAModel:
		im := image.NewRGBA(r)
		px(im.Pix, im.Stride, 4)
		for i := 3; i < len(im.Pix); i += 4 {
			im.Pix[i] = 255
		}
		return im
	case color.Gray16Model:
		im := image.NewGray16(r)
		px(im.Pix, im.Stride, 2)
		return im
	case color.NRGBA64Model:
		im := image.NewNRGBA64(r)
		px(im.Pix, im.Stride, 8)
		return im
	}
	im := image.NewNRGBA(r)
	px(im.Pix, im.Stride, 4)
	return im
}

// noise is a w×h opaque image of random pixels (a fixed seed).
func noise(w, h int) *image.RGBA {
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(56, 1))
	for i := range im.Pix {
		im.Pix[i] = uint8(rng.Uint32())
	}
	for i := 3; i < len(im.Pix); i += 4 {
		im.Pix[i] = 255
	}
	return im
}

func jpegAt(t testing.TB, im image.Image, quality int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, im, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// segment is the offset of the first marker segment m (0xFF m) in a JPEG
// whose segments before it the test wrote (no entropy-coded data before).
func segment(t testing.TB, data []byte, m byte) int {
	t.Helper()
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xff {
			t.Fatalf("no marker at %d", i)
		}
		if data[i+1] == m {
			return i
		}
		i += 2 + int(binary.BigEndian.Uint16(data[i+2:]))
	}
	t.Fatalf("no marker %#x", m)
	return 0
}

// withIHDR is a PNG with its IHDR's bit depth and interlace method
// replaced, the chunk's CRC made again.
func withIHDR(data []byte, depth, interlace byte) []byte {
	out := bytes.Clone(data)
	out[24], out[28] = depth, interlace
	binary.BigEndian.PutUint32(out[29:], crc32.ChecksumIEEE(out[12:29]))
	return out
}

// withSOF is a JPEG (as image/jpeg writes it: SOF0 before the scan) whose
// start-of-frame marker is sof, and whose components' sampling factors
// are hv when hv is not 0.
func withSOF(t testing.TB, data []byte, sof, hv byte) []byte {
	out := bytes.Clone(data)
	i := segment(t, out, 0xc0)
	out[i+1] = sof
	if hv != 0 {
		for c := 0; c < int(out[i+9]); c++ {
			out[i+11+3*c] = hv
		}
	}
	return out
}

// beforeEOI inserts a marker segment just before the final EOI, after the
// entropy-coded data.
func beforeEOI(data []byte, marker byte, payload []byte) []byte {
	seg := []byte{0xff, marker, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	seg = append(seg, payload...)
	end := len(data) - 2
	return append(append(append([]byte{}, data[:end]...), seg...), data[end:]...)
}

// junkBefore inserts bytes that are no marker before the segment m:
// image/jpeg skips them, as libjpeg does.
func junkBefore(t testing.TB, data []byte, m byte) []byte {
	i := segment(t, data, m)
	return append(append(append([]byte{}, data[:i]...), 0x12, 0x34, 0x00), data[i:]...)
}

// rgbIDs is a JPEG whose three components are named R, G and B, in its
// frame and in its scan (image/jpeg then decodes it as RGB).
func rgbIDs(t testing.TB, data []byte) []byte {
	b := bytes.Clone(data)
	sof, sos := segment(t, b, 0xc0), segment(t, b, 0xda)
	for c, id := range []byte("RGB") {
		b[sof+10+3*c], b[sos+5+2*c] = id, id
	}
	return b
}

// adobe is an APP14 "Adobe" payload with the colour transform t (0: RGB or
// CMYK, 1: YCbCr, 2: YCCK).
func adobe(t byte) []byte {
	return []byte{'A', 'd', 'o', 'b', 'e', 0, 100, 0, 0, 0, 0, t}
}

// fourComponents is a JPEG header of a 4-component (CMYK) baseline frame of
// w×h with an Adobe marker, then garbage.
func fourComponents(w, h int) []byte {
	out := []byte{0xff, 0xd8}
	out = append(out, 0xff, 0xee, 0, 14)
	out = append(out, adobe(0)...)
	sof := []byte{8, byte(h >> 8), byte(h), byte(w >> 8), byte(w), 4, 1, 0x11, 0, 2, 0x11, 0, 3, 0x11, 0, 4, 0x11, 0}
	out = append(out, 0xff, 0xc0, 0, byte(len(sof)+2))
	out = append(out, sof...)
	return append(out, bytes.Repeat([]byte{0x55}, 256)...)
}

// The kinds that blow up are refused from the header with ErrEncoding (415,
// IMAGE_FORMAT_REJECTED): nothing is decoded, so a 4096×4096 one allocates
// next to nothing; the same pixels as an 8-bit, non-interlaced PNG or a
// baseline JPEG are accepted.
func TestSanitizeRefusesWhatTakesTooMuchMemory(t *testing.T) {
	ctx := context.Background()
	small := jpegOf(t, picture(64, 64))
	big := jpegAt(t, gradient(MaxSide, MaxSide, color.RGBAModel), 80)
	scan := segment(t, small, 0xda)
	for name, data := range map[string][]byte{
		"a 16-bit gray PNG":                    pngOf(t, gradient(MaxSide, MaxSide, color.Gray16Model)),
		"a 16-bit RGBA PNG":                    pngOf(t, gradient(MaxSide, MaxSide, color.NRGBA64Model)),
		"an interlaced PNG":                    withIHDR(pngOf(t, gradient(MaxSide, MaxSide, color.NRGBAModel)), 8, 1),
		"a progressive JPEG":                   withSOF(t, big, 0xc2, 0),
		"a progressive 4:4:4 JPEG":             withSOF(t, big, 0xc2, 0x11),
		"a lossless JPEG":                      withSOF(t, small, 0xc3, 0),
		"an arithmetic-coded JPEG":             withSOF(t, small, 0xc9, 0),
		"a CMYK JPEG":                          fourComponents(MaxSide, MaxSide),
		"a 12-bit JPEG":                        func() []byte { b := bytes.Clone(small); b[segment(t, b, 0xc0)+4] = 12; return b }(),
		"an RGB JPEG (Adobe transform 0)":      withSegment(small, 0xee, adobe(0)),
		"an RGB JPEG, marked after its scan":   beforeEOI(small, 0xee, adobe(0)),
		"an RGB JPEG by its component IDs":     rgbIDs(t, small),
		"a component in two scans":             append(append(bytes.Clone(small[:len(small)-2]), small[scan:len(small)-2]...), 0xff, 0xd9),
		"a progressive JPEG behind extra data": junkBefore(t, withSOF(t, small, 0xc2, 0), 0xc2),
	} {
		var err error
		n := allocated(func() { _, err = Sanitize(ctx, data) })
		if !errors.Is(err, ErrEncoding) {
			t.Errorf("%s: %v, want ErrEncoding", name, err)
		}
		if n > 1<<20 {
			t.Errorf("%s: it allocated %.1f MiB: it was decoded", name, float64(n)/(1<<20))
		}
	}
	// The vectors are real: image/jpeg decodes such a file into an RGBA
	// copy, whether its Adobe marker comes first or after its scan, or its
	// components are named R, G and B.
	for _, data := range [][]byte{withSegment(small, 0xee, adobe(0)), beforeEOI(small, 0xee, adobe(0)), rgbIDs(t, small)} {
		if m, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		} else if _, ok := m.(*image.RGBA); !ok {
			t.Errorf("image/jpeg decoded an Adobe transform 0 JPEG as %T", m)
		}
	}
	// What stays accepted: 8-bit PNG of every colour type the encoder
	// writes, baseline JPEG in colour and grey, a YCbCr Adobe marker, an
	// extended-sequential (SOF1) frame.
	for name, data := range map[string][]byte{
		"an 8-bit RGBA PNG":           pngOf(t, gradient(64, 64, color.NRGBAModel)),
		"an 8-bit RGB PNG":            pngOf(t, gradient(64, 64, color.RGBAModel)),
		"an 8-bit gray PNG":           pngOf(t, gradient(64, 64, color.GrayModel)),
		"a paletted PNG":              pngOf(t, image.NewPaletted(image.Rect(0, 0, 64, 64), color.Palette{color.Black, color.White})),
		"a baseline JPEG":             small,
		"a gray baseline JPEG":        jpegOf(t, gradient(64, 64, color.GrayModel)),
		"a YCbCr Adobe marker":        withSegment(small, 0xee, adobe(1)),
		"an extended-sequential JPEG": withSOF(t, small, 0xc1, 0),
		"extra data between segments": junkBefore(t, small, 0xc0),
	} {
		if _, err := Sanitize(ctx, data); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// worstAccepted are the uploads the header check lets through that make
// the decoder allocate most, at MaxSide×MaxSide: an 8-bit PNG with alpha
// and one without (4 bytes a pixel), a baseline 4:4:4 JPEG header (3
// bytes a pixel: the rest garbage, which still decodes into the full
// image), and a JPEG of noise at quality 30 (4.5 MB), whose re-encoding
// at jpegQuality would be 11 MB.
func worstAccepted(t *testing.T) map[string][]byte {
	return map[string][]byte{
		"an 8-bit RGBA PNG":        pngOf(t, gradient(MaxSide, MaxSide, color.NRGBAModel)),
		"an 8-bit RGB PNG":         pngOf(t, gradient(MaxSide, MaxSide, color.RGBAModel)),
		"a baseline 4:4:4 header":  withSOF(t, jpegAt(t, gradient(MaxSide, MaxSide, color.RGBAModel), 80), 0xc0, 0x11),
		"noise re-encoded too big": jpegAt(t, noise(MaxSide, MaxSide), 30),
	}
}

// One Sanitize of the worst upload the server accepts allocates at most
// MaxDecodeBytes (measured: about 67 MiB, Go 1.27.3), whether it is
// accepted, refused after its decode (ErrTooLarge, ErrInvalid) or not.
func TestADecodeStaysWithinItsBound(t *testing.T) {
	ctx := context.Background()
	for name, data := range worstAccepted(t) {
		if len(data) > MaxBytes {
			t.Fatalf("%s: the test's file is %d bytes", name, len(data))
		}
		var err error
		n := allocated(func() { _, err = Sanitize(ctx, data) })
		t.Logf("%s: %d bytes in, %v, allocated %.1f MiB", name, len(data), err, float64(n)/(1<<20))
		if n > MaxDecodeBytes {
			t.Errorf("%s: one decode allocated %d bytes, more than MaxDecodeBytes (%d)", name, n, MaxDecodeBytes)
		}
		switch name {
		case "noise re-encoded too big":
			if !errors.Is(err, ErrTooLarge) {
				t.Errorf("%s: %v, want ErrTooLarge", name, err)
			}
		case "a baseline 4:4:4 header":
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Errorf("%s: %v", name, err)
			}
		default:
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
	// The process: at most cap(decodeSlots) of them at once.
	if cap(decodeSlots) != 2 || cap(decodeSlots)*MaxDecodeBytes > 160<<20 {
		t.Errorf("%d decode slots of %d bytes", cap(decodeSlots), MaxDecodeBytes)
	}
}

// A third decode waits for one of the two slots, as long as its context
// allows; the header checks do not wait.
func TestAtMostTwoDecodesAtOnce(t *testing.T) {
	data := pngOf(t, picture(64, 64))
	for range cap(decodeSlots) {
		decodeSlots <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := Sanitize(ctx, data)
	// With every slot taken, a header check still answers at once (a
	// check that waited would end with this context's error instead).
	hctx, hcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer hcancel()
	_, refused := Sanitize(hctx, withIHDR(data, 16, 0))
	for range cap(decodeSlots) {
		<-decodeSlots
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a third decode did not wait: %v", err)
	}
	if !errors.Is(refused, ErrEncoding) {
		t.Errorf("a header check waited for a slot: %v", refused)
	}
	if _, err := Sanitize(context.Background(), data); err != nil {
		t.Errorf("after the slots are free: %v", err)
	}
}

// The re-encoding is cut at MaxBytes: the buffer never holds more.
func TestTheReencodingStopsAtMaxBytes(t *testing.T) {
	w := capped{max: 10}
	if n, err := w.Write([]byte("0123456789")); n != 10 || err != nil {
		t.Fatalf("%d %v", n, err)
	}
	if n, err := w.Write([]byte("x")); n != 0 || !errors.Is(err, ErrTooLarge) || len(w.buf) != 10 {
		t.Errorf("%d %v %q", n, err, w.buf)
	}
}
