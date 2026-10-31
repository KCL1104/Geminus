package proposals

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/storage/objects"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
)

// H-052 review round 1 (L1, L2): reading a proposal's photo reads no more
// than its record's size and one byte, and tells a storage outage (503,
// retry) and a request that ended (its context's error, as is) from a
// photo that is not its record's (500).

// flakyObjects is an objects.Store whose Get answers get().
type flakyObjects struct {
	get func(ctx context.Context) (io.ReadCloser, error)
}

func (f flakyObjects) Put(context.Context, string, io.Reader, int64) (objects.Object, error) {
	return objects.Object{}, errors.New("not used")
}
func (f flakyObjects) Get(ctx context.Context, _ string) (io.ReadCloser, error) { return f.get(ctx) }

// countingReader counts the bytes read from r.
type countingReader struct {
	r    io.Reader
	read int64
	err  error // returned once r is exhausted, instead of io.EOF
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
	if err == io.EOF && c.err != nil {
		return n, c.err
	}
	return n, err
}
func (c *countingReader) Close() error { return nil }

func photoRecord(t *testing.T, size int64) store.Image {
	t.Helper()
	key, err := objects.NewKey(readOrg, objects.Assets, "proposal-images", readPhoto, "image")
	if err != nil {
		t.Fatal(err)
	}
	return store.Image{ObjectKey: key, SHA256: "sha256:" + strings.Repeat("a", 64), Size: size, MediaType: "image/png", Width: 40, Height: 32}
}

func TestAPhotoIsReadNoFurtherThanItsRecord(t *testing.T) {
	big := &countingReader{r: strings.NewReader(strings.Repeat("x", 6<<20))}
	objs := flakyObjects{get: func(context.Context) (io.ReadCloser, error) { return big, nil }}
	data, err := readStoredImage(context.Background(), objs, readOrg, photoRecord(t, 100))
	if !errors.Is(err, ErrImageCorrupt) || data != nil {
		t.Fatalf("%d bytes, %v", len(data), err)
	}
	if big.read > 101 {
		t.Errorf("read %d bytes of a 100-byte record", big.read)
	}
}

func TestAStorageOutageIsUnavailableAndAnEndedRequestIsItsOwn(t *testing.T) {
	rec := photoRecord(t, 100)
	outage := errors.New("s3: connection refused")
	for name, get := range map[string]func(context.Context) (io.ReadCloser, error){
		"the store refuses": func(context.Context) (io.ReadCloser, error) { return nil, outage },
		"the read breaks": func(context.Context) (io.ReadCloser, error) {
			return &countingReader{r: strings.NewReader("abc"), err: outage}, nil
		},
	} {
		data, err := readStoredImage(context.Background(), flakyObjects{get: get}, readOrg, rec)
		if !errors.Is(err, ErrImageStorage) || !errors.Is(err, outage) || data != nil {
			t.Errorf("%s: %d bytes, %v", name, len(data), err)
		}
		var he *httpapi.Error
		if e := Refused(err); !errors.As(e, &he) || he.Code != httpapi.ServiceUnavailable || strings.Contains(he.Message, "s3") {
			t.Errorf("%s: shown as %v", name, e)
		}
	}

	// The request ended (the client went away): its context's error, as is,
	// whatever the store says then.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ended := flakyObjects{get: func(ctx context.Context) (io.ReadCloser, error) { return nil, errors.Join(outage, ctx.Err()) }}
	if data, err := readStoredImage(ctx, ended, readOrg, rec); err != context.Canceled || data != nil {
		t.Errorf("ended: %d bytes, %v", len(data), err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	midway := flakyObjects{get: func(context.Context) (io.ReadCloser, error) {
		return &countingReader{r: strings.NewReader("abc"), err: errors.New("read: use of closed connection")}, nil
	}}
	cancel()
	if data, err := readStoredImage(ctx, midway, readOrg, rec); err != context.Canceled || data != nil {
		t.Errorf("ended while reading: %d bytes, %v", len(data), err)
	}
}
