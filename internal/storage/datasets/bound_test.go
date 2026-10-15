package datasets_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/datasets"
)

// endless is an object larger than any limit; it counts what was read.
type endless struct{ read int64 }

func (e *endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	e.read += int64(len(p))
	return len(p), nil
}
func (e *endless) Close() error { return nil }

type endlessStore struct{ obj *endless }

func (s endlessStore) Open(context.Context, string, string) (io.ReadCloser, error) { return s.obj, nil }

// Resolve bounds how much of an object it reads; memory does not depend on
// the stored size.
func TestSnapshotReadIsBounded(t *testing.T) {
	obj := &endless{}
	r := resolve(t, endlessStore{obj}, reportIR(t), bindingFor(nil))
	onlyIssue(t, r, validate.DatasetBindingInvalid, "/dataset_bindings/0")
	if obj.read > datasets.MaxSnapshotBytes+64<<10 {
		t.Errorf("read %d bytes of an endless object", obj.read)
	}
}

type failing struct {
	readErr, closeErr error
	closed            bool
}

func (f *failing) Read(p []byte) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	return 0, io.EOF
}
func (f *failing) Close() error { f.closed = true; return f.closeErr }

type failingStore struct{ r *failing }

func (s failingStore) Open(context.Context, string, string) (io.ReadCloser, error) { return s.r, nil }

// Read and Close failures are infrastructure errors, never issues, and the
// reader is always closed.
func TestStoreReadErrors(t *testing.T) {
	for name, f := range map[string]*failing{
		"read":  {readErr: errors.New("connection reset")},
		"close": {closeErr: errors.New("close failed")},
	} {
		_, err := datasets.Resolve(context.Background(), failingStore{f}, org, reportIR(t), []model.DatasetBinding{bindingFor(nil)})
		want := f.readErr
		if want == nil {
			want = f.closeErr
		}
		if !errors.Is(err, want) {
			t.Errorf("%s: %v", name, err)
		}
		if !f.closed {
			t.Errorf("%s: reader not closed", name)
		}
	}
}
