// Package photo is the server's handling of a photo or floor plan someone
// sends for a proposal (T-056, H-033; SECURITY §6): which files it takes,
// how large, and what of them it keeps. It reads and writes bytes only; the
// proposal service stores the result in private object storage and the
// proposal job forwards it to the AI.
//
//   - Only PNG and JPEG, told by the file's first bytes: a file name or a
//     declared Content-Type is never trusted, and SVG, GIF, WebP, HEIC,
//     PDF, archives and executables are refused.
//   - At most MaxBytes, and MinSide to MaxSide pixels on each side, read
//     from the header before any pixel is decoded (a decompression bomb is
//     refused from its header). At most decodeSlots decodes run at once in
//     a process, so concurrent uploads cannot take all its memory.
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
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
)

const (
	// MaxBytes bounds an upload and the image the server stores from it
	// (the database's size_bytes CHECK, migration 0017).
	MaxBytes = 10_000_000
	// MinSide and MaxSide bound the image's width and height in pixels.
	MinSide = 32
	MaxSide = 4096
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

// decodeSlots bounds the decodes that run at once in this process: an
// image of MaxSide² pixels takes about 64 MiB while it is decoded.
var decodeSlots = make(chan struct{}, 2)

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
	var out bytes.Buffer
	if mediaType == PNG {
		err = png.Encode(&out, pixels)
	} else {
		err = jpeg.Encode(&out, pixels, &jpeg.Options{Quality: jpegQuality})
	}
	if err != nil {
		return Image{}, ErrInvalid
	}
	if out.Len() > MaxBytes {
		return Image{}, ErrTooLarge
	}
	return Image{MediaType: mediaType, Data: out.Bytes(), Width: cfg.Width, Height: cfg.Height, SHA256: sum(out.Bytes())}, nil
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
