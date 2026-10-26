// Package photo is the server's handling of a photo or floor plan someone
// sends for a proposal (T-056, H-033; SECURITY §6): which files it takes,
// how large, and what of them it keeps. It reads and writes bytes only; the
// proposal service stores the result in private object storage and the
// proposal job forwards it to the AI.
//
//   - Only PNG and JPEG, told by the file's first bytes: a file name or a
//     declared Content-Type is never trusted, and SVG, GIF, WebP, HEIC,
//     PDF, archives and executables are refused.
//   - Only the kinds of them that decode into at most 4 bytes a pixel
//     (T-056 review round 1, M2): an 8-bit PNG that is not interlaced, and
//     a sequential 8-bit JPEG (baseline or extended, Huffman-coded) of one
//     component or three YCbCr ones, each component in one scan. A
//     progressive, lossless or arithmetic-coded JPEG, a CMYK, YCCK or RGB
//     one, a 16-bit or an interlaced PNG are refused from their headers
//     (ErrEncoding): image/jpeg and image/png decode those into several
//     times the image (a 310 KB progressive CMYK JPEG of 4096×4096 took
//     330 MiB).
//   - At most MaxBytes, and MinSide to MaxSide pixels on each side, read
//     from the header before any pixel is decoded (a decompression bomb is
//     refused from its header). So one Sanitize allocates at most
//     MaxDecodeBytes, the re-encoding stopping at MaxBytes; and at most
//     cap(decodeSlots) decodes run at once in a process, so concurrent
//     uploads take at most cap(decodeSlots) × MaxDecodeBytes.
//   - The image is decoded and its pixels encoded again in the same format.
//     Nothing else of the upload survives: no EXIF (GPS, the camera, the
//     time), XMP, ICC profile, text chunk, comment or byte after the end of
//     the image. What is stored, and so all a provider is ever sent, is the
//     re-encoded image. EXIF orientation is not applied: the pixels are
//     kept in the order the file stores them.
//   - Verify checks bytes read back from storage against the record the
//     server kept (hash, size, type, pixel size) before they go anywhere.
package photo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
)

const (
	// MaxBytes bounds an upload and the image the server stores from it
	// (the database's size_bytes CHECK, migration 0017): 5 MiB, the most
	// T-055's provider client sends (provider.MaxImageBytes); MaxSide is
	// within its pixel bound too (a test keeps both).
	MaxBytes = 5 << 20
	// MinSide and MaxSide bound the image's width and height in pixels.
	MinSide = 32
	MaxSide = 4096
	// MaxDecodeBytes bounds what one Sanitize allocates in all (an upper
	// bound of the heap it holds): the decoded image (at most 4 bytes a
	// pixel: 64 MiB at MaxSide×MaxSide), the decoders' and encoders' state
	// and the re-encoding (at most MaxBytes). Measured with Go 1.27.3 (T-056
	// review round 1, checks/T-056_VALIDATION.md): at most 66 MiB for the
	// worst upload accepted; TestADecodeStaysWithinItsBound keeps it.
	MaxDecodeBytes = 80 << 20
	// The media types an image may have.
	PNG  = "image/png"
	JPEG = "image/jpeg"
)

// jpegQuality is the quality of a re-encoded JPEG: close to the upload,
// with no metadata.
const jpegQuality = 90

var (
	// ErrFormat: the file is not a PNG or a JPEG (415, IMAGE_FORMAT_REJECTED).
	ErrFormat = errors.New("photo: not a PNG or JPEG")
	// ErrEmpty: the file has no bytes (422 at /file, IMAGE_INVALID).
	ErrEmpty = errors.New("photo: the file is empty")
	// ErrInvalid: the file starts as a PNG or JPEG but does not decode
	// (422 at /file, IMAGE_INVALID).
	ErrInvalid = errors.New("photo: the image does not decode")
	// ErrEncoding: the file is a PNG or a JPEG of a kind the server does not
	// decode, because decoding it takes several times the image: a
	// progressive, lossless or arithmetic-coded JPEG, a CMYK, YCCK or RGB
	// one, one of another precision than 8 bits or with a component in more
	// than one scan; a 16-bit or an interlaced PNG (415,
	// IMAGE_FORMAT_REJECTED). Told from the headers, before any pixel is
	// decoded.
	ErrEncoding = errors.New("photo: a PNG or JPEG of a kind the server does not decode")
	// ErrPixels: the image is narrower or lower than MinSide, or wider or
	// higher than MaxSide (422 at /file, IMAGE_PIXELS_EXCEEDED).
	ErrPixels = errors.New("photo: the image's size in pixels is out of range")
	// ErrTooLarge: the file, or the image re-encoded, has more than MaxBytes
	// (413); nothing was kept.
	ErrTooLarge = errors.New("photo: the file is too large")
	// ErrMismatch: bytes read back from storage are not the ones the record
	// names (the worker forwards nothing).
	ErrMismatch = errors.New("photo: the stored image is not the one its record names")
)

// Image is a re-encoded image: its media type, bytes, size in pixels and
// the SHA-256 of the bytes ("sha256:<hex>").
type Image struct {
	MediaType     string
	Data          []byte
	Width, Height int
	SHA256        string
}

// Size is the image's size in bytes.
func (im Image) Size() int64 { return int64(len(im.Data)) }

// Record is what the server keeps of a stored image (twin.ai_proposal_images).
type Record struct {
	MediaType     string
	Width, Height int
	Size          int64
	SHA256        string
}

// Record is the record of im.
func (im Image) Record() Record {
	return Record{MediaType: im.MediaType, Width: im.Width, Height: im.Height, Size: im.Size(), SHA256: im.SHA256}
}

var (
	pngSignature  = []byte("\x89PNG\r\n\x1a\n")
	jpegSignature = []byte{0xff, 0xd8, 0xff}
)

// Format is the media type of a file from its first bytes: PNG, JPEG, or
// "" for anything else.
func Format(head []byte) string {
	switch {
	case bytes.HasPrefix(head, pngSignature):
		return PNG
	case bytes.HasPrefix(head, jpegSignature):
		return JPEG
	}
	return ""
}

// Read reads at most MaxBytes from r: ErrTooLarge when r has more.
func Read(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBytes {
		return nil, ErrTooLarge
	}
	return data, nil
}

// decodeSlots bounds the decodes that run at once in this process: each
// allocates at most MaxDecodeBytes, so two take at most 160 MiB.
var decodeSlots = make(chan struct{}, 2)

// inspect refuses, from its headers, a PNG or JPEG whose decoding takes
// more than 4 bytes a pixel (ErrEncoding); a file whose structure cannot
// be read is ErrInvalid. It decodes no pixel.
func inspect(data []byte, mediaType string) error {
	if mediaType == PNG {
		return inspectPNG(data)
	}
	return inspectJPEG(data)
}

// inspectPNG reads the IHDR chunk, which the PNG specification puts first:
// its bit depth (16 refused: image/png decodes it into 8 bytes a pixel)
// and interlace method (Adam7 refused: image/png keeps every pass beside
// the image).
func inspectPNG(data []byte) error {
	if len(data) < 33 || binary.BigEndian.Uint32(data[8:]) != 13 || string(data[12:16]) != "IHDR" {
		return ErrInvalid
	}
	if depth, interlace := data[24], data[28]; depth > 8 || interlace != 0 {
		return ErrEncoding
	}
	return nil
}

// The JPEG markers inspectJPEG reads (ITU T.81 Table B.1).
const (
	markerSOF0  = 0xc0 // baseline sequential, Huffman
	markerSOF1  = 0xc1 // extended sequential, Huffman
	markerDHT   = 0xc4
	markerJPG   = 0xc8
	markerDAC   = 0xcc
	markerSOF15 = 0xcf
	markerRST0  = 0xd0
	markerRST7  = 0xd7
	markerSOI   = 0xd8
	markerEOI   = 0xd9
	markerSOS   = 0xda
	markerAPP14 = 0xee
	markerTEM   = 0x01
)

// inspectJPEG walks the file's markers as image/jpeg does: every marker
// segment by its length, the bytes that are no marker skipped (libjpeg's
// rule, which image/jpeg follows), each scan's entropy-coded data up to the
// next marker; up to the end of the image. It refuses (ErrEncoding):
//
//   - a frame that is not sequential and Huffman-coded (SOF2 progressive,
//     whose decoder keeps every block's coefficients, 4 bytes a sample;
//     SOF3 lossless; SOF5–SOF15), or not of 8-bit precision;
//   - a frame of other than 1 or 3 components (CMYK and YCCK are 4: a
//     fourth plane and a copy);
//   - three components image/jpeg would decode as RGB into a copy: an
//     Adobe APP14 marker with the colour transform 0 anywhere in the file
//     (image/jpeg reads one after the scan too), or the component IDs R, G
//     and B;
//   - a component in more than one scan (a sequential JPEG codes each
//     component in one, ITU T.81 §B.2.3; more would only make the decoder
//     go over the image again).
//
// Everything else image/jpeg decodes into the image itself: 1 byte a pixel
// for grey, at most 3 for YCbCr.
func inspectJPEG(data []byte) error {
	var (
		frame   bool
		comps   = map[byte]bool{} // the frame's components, by ID
		scanned = map[byte]bool{} // the components a scan has coded
		ids     []byte
		rgb     bool
	)
	i := 2 // after SOI
	for {
		// The next marker: 0xFF, any fill bytes, then its code; other bytes
		// are skipped as image/jpeg skips them.
		for i < len(data) && data[i] != 0xff {
			i++
		}
		for i < len(data) && data[i] == 0xff {
			i++
		}
		if i >= len(data) {
			return ErrInvalid // no end of image
		}
		m := data[i]
		i++
		switch {
		case m == 0x00:
			continue // a stuffed 0xFF outside a scan: no marker
		case m == markerEOI:
			if !frame || len(scanned) == 0 {
				return ErrInvalid
			}
			if rgb || (len(ids) == 3 && ids[0] == 'R' && ids[1] == 'G' && ids[2] == 'B') {
				return ErrEncoding
			}
			return nil
		case m >= markerRST0 && m <= markerRST7, m == markerSOI, m == markerTEM:
			continue // no length (a stray restart marker is ignored, as image/jpeg does)
		}
		if i+2 > len(data) {
			return ErrInvalid
		}
		n := int(binary.BigEndian.Uint16(data[i:]))
		if n < 2 || i+n > len(data) {
			return ErrInvalid
		}
		seg := data[i+2 : i+n]
		i += n
		switch {
		case m == markerSOF0 || m == markerSOF1:
			if frame || len(seg) < 6 {
				return ErrInvalid
			}
			frame = true
			nf := int(seg[5])
			if seg[0] != 8 || (nf != 1 && nf != 3) {
				return ErrEncoding
			}
			if len(seg) < 6+3*nf {
				return ErrInvalid
			}
			for c := range nf {
				id := seg[6+3*c]
				comps[id] = true
				ids = append(ids, id)
			}
		case m >= markerSOF0 && m <= markerSOF15 && m != markerDHT && m != markerJPG && m != markerDAC:
			return ErrEncoding // progressive, lossless, arithmetic-coded or hierarchical
		case m == markerAPP14:
			if len(seg) >= 12 && string(seg[:5]) == "Adobe" && seg[11] == 0 {
				rgb = true
			}
		case m == markerSOS:
			if !frame || len(seg) < 1 || len(seg) < 1+2*int(seg[0]) {
				return ErrInvalid
			}
			for c := range int(seg[0]) {
				id := seg[1+2*c]
				if !comps[id] {
					return ErrInvalid
				}
				if scanned[id] {
					return ErrEncoding
				}
				scanned[id] = true
			}
			// The entropy-coded data, up to the next marker: 0xFF 0x00 is a
			// stuffed byte, 0xFF then RST0–RST7 a restart, more 0xFF fill.
			for i < len(data) {
				if data[i] == 0xff && i+1 < len(data) {
					if next := data[i+1]; next != 0x00 && (next < markerRST0 || next > markerRST7) && next != 0xff {
						break
					}
				}
				i++
			}
		}
	}
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// config reads the header of data, a PNG or JPEG: its size in pixels,
// checked before any pixel is decoded.
func config(data []byte, mediaType string) (image.Config, error) {
	var cfg image.Config
	var err error
	if mediaType == PNG {
		cfg, err = png.DecodeConfig(bytes.NewReader(data))
	} else {
		cfg, err = jpeg.DecodeConfig(bytes.NewReader(data))
	}
	if err != nil {
		return image.Config{}, ErrInvalid
	}
	if cfg.Width < MinSide || cfg.Height < MinSide || cfg.Width > MaxSide || cfg.Height > MaxSide {
		return image.Config{}, ErrPixels
	}
	return cfg, nil
}

// Sanitize checks data, a file someone sent, and returns its pixels encoded
// again in the same format: PNG or JPEG by its first bytes (ErrFormat
// otherwise), at most MaxBytes (ErrTooLarge), MinSide to MaxSide pixels on
// each side (ErrPixels, from the header), and decodable to its end
// (ErrInvalid). The same data always gives the same bytes. It waits for a
// decode slot as long as ctx allows.
func Sanitize(ctx context.Context, data []byte) (Image, error) {
	if err := ctx.Err(); err != nil {
		return Image{}, err
	}
	switch {
	case len(data) == 0:
		return Image{}, ErrEmpty
	case len(data) > MaxBytes:
		return Image{}, ErrTooLarge
	}
	mediaType := Format(data)
	if mediaType == "" {
		return Image{}, ErrFormat
	}
	if err := inspect(data, mediaType); err != nil {
		return Image{}, err
	}
	cfg, err := config(data, mediaType)
	if err != nil {
		return Image{}, err
	}
	select {
	case decodeSlots <- struct{}{}:
		defer func() { <-decodeSlots }()
	case <-ctx.Done():
		return Image{}, ctx.Err()
	}
	var pixels image.Image
	if mediaType == PNG {
		pixels, err = png.Decode(bytes.NewReader(data))
	} else {
		pixels, err = jpeg.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return Image{}, ErrInvalid
	}
	if b := pixels.Bounds(); b.Dx() != cfg.Width || b.Dy() != cfg.Height {
		return Image{}, ErrInvalid
	}
	// The re-encoding, cut at MaxBytes: room for the upload's size and as
	// much again, then once room for MaxBytes.
	out := &capped{buf: make([]byte, 0, min(MaxBytes, 2*len(data)+64<<10)), max: MaxBytes}
	if mediaType == PNG {
		err = png.Encode(out, pixels)
	} else {
		err = jpeg.Encode(out, pixels, &jpeg.Options{Quality: jpegQuality})
	}
	switch {
	case errors.Is(err, ErrTooLarge):
		return Image{}, ErrTooLarge
	case err != nil:
		return Image{}, ErrInvalid
	}
	return Image{MediaType: mediaType, Data: out.buf, Width: cfg.Width, Height: cfg.Height, SHA256: sum(out.buf)}, nil
}

// capped is the re-encoding's buffer: a write past max bytes fails with
// ErrTooLarge and keeps nothing more, so an encoding larger than MaxBytes
// stops there instead of growing a buffer for all of it.
type capped struct {
	buf []byte
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if len(p) > c.max-len(c.buf) {
		return 0, ErrTooLarge
	}
	if len(c.buf)+len(p) > cap(c.buf) {
		grown := make([]byte, len(c.buf), c.max)
		copy(grown, c.buf)
		c.buf = grown
	}
	c.buf = append(c.buf, p...)
	return len(p), nil
}

// Verify reports whether data, read back from storage, is the image want
// records: its SHA-256 and size, and a PNG or JPEG of its type whose header
// states its size in pixels (ErrMismatch otherwise). It decodes no pixels.
func Verify(data []byte, want Record) error {
	if int64(len(data)) != want.Size || sum(data) != want.SHA256 || Format(data) != want.MediaType {
		return ErrMismatch
	}
	cfg, err := config(data, want.MediaType)
	if err != nil || cfg.Width != want.Width || cfg.Height != want.Height {
		return ErrMismatch
	}
	return nil
}
