package datasets_test

// T-054 review round 1 (L1, L2, L3): the one classification of the binding
// stage's errors that every API route uses (models, runs, approval,
// scenarios).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/storage/datasets"
)

// L1: only a snapshot the Store could not read (Open, Read or Close failed
// with anything but ErrNotFound) is a ReadError, which names the dataset
// version and wraps the store's error; a snapshot that is not there is an
// issue of the data, not an error.
func TestReadError(t *testing.T) {
	good := root(t, "testdata/contracts/valid-dataset-snapshot.json")
	boom := errors.New("object storage unavailable")
	for name, s := range map[string]datasets.Store{
		"open":  &store{err: boom},
		"read":  failingStore{&failing{readErr: boom}},
		"close": failingStore{&failing{closeErr: boom}},
	} {
		binding := bindingFor(good)
		if name != "open" {
			binding = bindingFor(nil)
		}
		_, err := datasets.Resolve(context.Background(), s, org, reportIR(t), []model.DatasetBinding{binding})
		var re *datasets.ReadError
		if !errors.As(err, &re) || re.DatasetVersionID != dsv || !errors.Is(err, boom) {
			t.Errorf("%s: %#v", name, err)
			continue
		}
		if got, ok := datasets.StorageFailure(err); !ok || got != re {
			t.Errorf("%s: StorageFailure %v %v", name, got, ok)
		}
		if want := "reading dataset version " + dsv + ": object storage unavailable"; err.Error() != want {
			t.Errorf("%s: %q, want %q", name, err, want)
		}
	}
	// Not there: an issue, no error.
	if r := resolve(t, &store{}, reportIR(t), bindingFor(good)); r.Valid() {
		t.Error("a missing snapshot resolved")
	}
	// A read that says the request ended is a ReadError too, but no storage
	// failure: the request's context error.
	_, err := datasets.Resolve(context.Background(), &store{err: fmt.Errorf("get: %w", context.DeadlineExceeded)}, org, reportIR(t),
		[]model.DatasetBinding{bindingFor(good)})
	var re *datasets.ReadError
	if !errors.As(err, &re) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a read past its deadline: %v", err)
	}
	if _, ok := datasets.StorageFailure(err); ok {
		t.Errorf("a read past its deadline is a storage failure: %v", err)
	}
}

// The rule every route applies to an error of Resolve (Failure, then
// StorageFailure): a read failure while the request is alive is a storage
// failure (503 DATASET_STORAGE_FAILED); a request that ended keeps its
// context's error whatever the store said (L2: the routes answer it as an
// ended request); a read that says the request ended is no storage
// failure; any other error is a server fault (L1: 500, logged at ERROR).
func TestFailure(t *testing.T) {
	cause := errors.New("read tcp 10.0.0.1: connection reset by peer")
	read := &datasets.ReadError{DatasetVersionID: dsv, Err: cause}
	fault := fmt.Errorf("hashing a shape-checked manifest: %w", errors.New("invalid"))
	decode := fmt.Errorf("schema-valid snapshot does not decode: %w", io.ErrUnexpectedEOF)
	cancelledRead := &datasets.ReadError{DatasetVersionID: dsv, Err: fmt.Errorf("get: %w", context.Canceled)}
	lateRead := &datasets.ReadError{DatasetVersionID: dsv, Err: fmt.Errorf("get: %w", context.DeadlineExceeded)}
	live := context.Background()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, c := range []struct {
		name    string
		ctx     context.Context
		err     error
		storage bool    // StorageFailure reports it
		is      []error // errors.Is holds for each
		same    bool    // Failure returns err itself
	}{
		{"a live request, a read failure", live, read, true, []error{cause}, true},
		{"a live request, a server fault", live, fault, false, nil, true},
		{"a live request, a snapshot that does not decode", live, decode, false, []error{io.ErrUnexpectedEOF}, true},
		{"a live request, a cancelled read", live, cancelledRead, false, []error{context.Canceled}, true},
		{"a live request, a read past its deadline", live, lateRead, false, []error{context.DeadlineExceeded}, true},
		{"a cancelled request, the store's own error", cancelled, read, false, []error{context.Canceled, cause}, false},
		{"a cancelled request, a server fault", cancelled, fault, false, []error{context.Canceled}, false},
		{"a cancelled request, a cancelled read", cancelled, cancelledRead, false, []error{context.Canceled}, true},
		{"a cancelled request, its own error", cancelled, context.Canceled, false, []error{context.Canceled}, true},
		{"a request past its deadline, the store's own error", expired, read, false, []error{context.DeadlineExceeded, cause}, false},
		{"a request past its deadline, a server fault", expired, fault, false, []error{context.DeadlineExceeded}, false},
	} {
		got := datasets.Failure(c.ctx, c.err)
		if got == nil || !errors.Is(got, c.err) {
			t.Errorf("%s: %v loses the error", c.name, got)
			continue
		}
		if (got == c.err) != c.same {
			t.Errorf("%s: %v (the same error: %v, want %v)", c.name, got, got == c.err, c.same)
		}
		for _, want := range c.is {
			if !errors.Is(got, want) {
				t.Errorf("%s: %v is not %v", c.name, got, want)
			}
		}
		if re, ok := datasets.StorageFailure(got); ok != c.storage || ok && re != read {
			t.Errorf("%s: StorageFailure %v %v, want %v", c.name, re, ok, c.storage)
		}
	}
	if _, ok := datasets.StorageFailure(nil); ok {
		t.Error("nil is a storage failure")
	}
	for _, ctx := range []context.Context{live, cancelled, expired} {
		if got := datasets.Failure(ctx, nil); got != nil {
			t.Errorf("no error became %v", got)
		}
	}
}
