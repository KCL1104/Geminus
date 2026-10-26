package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

// Image is an image part of a prompt or a segmentation request (T-055,
// for T-056's photo path, H-033): the bytes the worker read from private
// storage, checked before any request. Make one with NewImage; the zero
// Image is no image. Only MediaType, Digest and the size are ever recorded
// (the prompt's InputHash covers the digest); the bytes go to the provider
// and nowhere else. H-029 and H-033 allow demo images only: the server
// never sends a customer's photo.
type Image struct {
	// MediaType is one of ImagePNG, ImageJPEG and ImageWebP, and the bytes
	// are of that type.
	MediaType string
	Data      []byte
	// Width and Height are the pixel size the image's header gives (before
	// any orientation its metadata names).
	Width, Height int
	// Digest is "sha256:<hex>" of Data.
	Digest string
}

// The image types a prompt may carry: the ones both Meta's image
// understanding (https://dev.meta.ai/docs/image-understanding, "Supported
// MIME types", retrieved 2026-10-26) and the Muse Image output formats
// (webp, png, jpeg; https://dev.meta.ai/docs/image-generation) name.
const (
	ImagePNG  = "image/png"
	ImageJPEG = "image/jpeg"
	ImageWebP = "image/webp"
)

// The limits of an image (PROPOSED D-120): engineering bounds well inside
// the provider's (Meta: 50 MB inline and 50 images a request), so that a
// request stays small and a photo's pixel size bounds the boxes a
// segmentation can return.
const (
	MaxImageBytes      = 5 << 20
	MaxImageSide       = 8000
	MaxImagesPerPrompt = 4
)

// ErrInvalidImage: the bytes are not an image this build sends. The error
// says which rule, never what the bytes hold.
var ErrInvalidImage = errors.New("ai provider: invalid image")

func invalidImage(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidImage, reason) }

// NewImage checks data as an image of mediaType and returns it with its
// size and digest; the Image holds a copy of data.
func NewImage(mediaType string, data []byte) (Image, error) {
	switch mediaType {
	case ImagePNG, ImageJPEG, ImageWebP:
	default:
		return Image{}, invalidImage("the media type is not image/png, image/jpeg or image/webp")
	}
	switch {
	case len(data) == 0:
		return Image{}, invalidImage("no bytes")
	case len(data) > MaxImageBytes:
		return Image{}, invalidImage(fmt.Sprintf("larger than %d bytes", MaxImageBytes))
	}
	w, h, err := sizeOf(mediaType, data)
	if err != nil {
		return Image{}, err
	}
	sum := sha256.Sum256(data)
	return Image{MediaType: mediaType, Data: bytes.Clone(data), Width: w, Height: h, Digest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

// Check reports whether im is still what NewImage made: a known type, its
// bytes of that type and size, and their digest.
func (im Image) Check() error {
	again, err := NewImage(im.MediaType, im.Data)
	if err != nil {
		return err
	}
	if again.Width != im.Width || again.Height != im.Height || again.Digest != im.Digest {
		return invalidImage("changed after it was checked")
	}
	return nil
}

// sizeOf reads the pixel size from the header of the image type, which
// the bytes must have.
func sizeOf(mediaType string, data []byte) (int, int, error) {
	var w, h int
	var err error
	switch mediaType {
	case ImagePNG:
		w, h, err = pngSize(data)
	case ImageJPEG:
		w, h, err = jpegSize(data)
	case ImageWebP:
		w, h, err = webpSize(data)
	}
	if err != nil {
		return 0, 0, err
	}
	if w < 1 || h < 1 || w > MaxImageSide || h > MaxImageSide {
		return 0, 0, invalidImage(fmt.Sprintf("the pixel size is not between 1 and %d on each side", MaxImageSide))
	}
	return w, h, nil
}

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// pngSize: the signature, then the IHDR chunk (length 13) with the width
// and height (PNG specification §11.2.2).
func pngSize(d []byte) (int, int, error) {
	if len(d) < 33 || !bytes.Equal(d[:8], pngSignature) || binary.BigEndian.Uint32(d[8:12]) != 13 || string(d[12:16]) != "IHDR" {
		return 0, 0, invalidImage("not a PNG file")
	}
	return int(binary.BigEndian.Uint32(d[16:20]) & 0x7fffffff), int(binary.BigEndian.Uint32(d[20:24]) & 0x7fffffff), nil
}

// jpegSize: SOI, then the segments up to the first start-of-frame marker
// (SOF0–SOF15 but DHT, JPG and DAC), whose header holds the height and
// width (ITU-T T.81 B.2.2). A scan before any frame, or the end of the
// bytes, is no JPEG this build takes.
func jpegSize(d []byte) (int, int, error) {
	bad := invalidImage("not a JPEG file with a frame header")
	if len(d) < 4 || d[0] != 0xff || d[1] != 0xd8 {
		return 0, 0, bad
	}
	i := 2
	for i < len(d) {
		if d[i] != 0xff {
			return 0, 0, bad
		}
		for i < len(d) && d[i] == 0xff { // fill bytes
			i++
		}
		if i >= len(d) {
			return 0, 0, bad
		}
		marker := d[i]
		i++
		switch {
		case marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7): // no length
			continue
		case marker == 0xd8 || marker == 0xd9 || marker == 0xda: // SOI again, EOI or a scan before a frame
			return 0, 0, bad
		}
		if i+2 > len(d) {
			return 0, 0, bad
		}
		length := int(binary.BigEndian.Uint16(d[i : i+2]))
		if length < 2 || i+length > len(d) {
			return 0, 0, bad
		}
		if marker >= 0xc0 && marker <= 0xcf && marker != 0xc4 && marker != 0xc8 && marker != 0xcc {
			if length < 7 {
				return 0, 0, bad
			}
			return int(binary.BigEndian.Uint16(d[i+5 : i+7])), int(binary.BigEndian.Uint16(d[i+3 : i+5])), nil
		}
		i += length
	}
	return 0, 0, bad
}

// webpSize: the RIFF header and the first chunk, VP8 (lossy), VP8L
// (lossless) or VP8X (extended), each with the size in its own form
// (https://developers.google.com/speed/webp/docs/riff_container).
func webpSize(d []byte) (int, int, error) {
	bad := invalidImage("not a WebP file")
	if len(d) < 30 || string(d[:4]) != "RIFF" || string(d[8:12]) != "WEBP" {
		return 0, 0, bad
	}
	if riff := int64(binary.LittleEndian.Uint32(d[4:8])); riff+8 > int64(len(d)) || riff < 22 {
		return 0, 0, bad
	}
	if chunk := int64(binary.LittleEndian.Uint32(d[16:20])); 20+chunk > int64(len(d)) || chunk < 10 {
		return 0, 0, bad
	}
	p := d[20:]
	switch string(d[12:16]) {
	case "VP8 ":
		// frame tag (3 bytes, a key frame has bit 0 clear), start code,
		// then 14-bit width and height.
		if p[0]&1 != 0 || p[3] != 0x9d || p[4] != 0x01 || p[5] != 0x2a {
			return 0, 0, bad
		}
		return int(binary.LittleEndian.Uint16(p[6:8]) & 0x3fff), int(binary.LittleEndian.Uint16(p[8:10]) & 0x3fff), nil
	case "VP8L":
		if p[0] != 0x2f {
			return 0, 0, bad
		}
		bits := binary.LittleEndian.Uint32(p[1:5])
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1, nil
	case "VP8X":
		w := int(p[4]) | int(p[5])<<8 | int(p[6])<<16
		h := int(p[7]) | int(p[8])<<8 | int(p[9])<<16
		return w + 1, h + 1, nil
	}
	return 0, 0, bad
}
