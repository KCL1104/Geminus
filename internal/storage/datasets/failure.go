package datasets

import (
	"context"
	"errors"
)

// ReadError: the Store could not read the snapshot of a dataset version
// (Open, Read or Close failed with anything but ErrNotFound). It is the one
// infrastructure failure of Resolve: a caller may answer it as retryable
// (503 DATASET_STORAGE_FAILED on the API routes; D-061 (c), D-069 (b),
// H-022, D-087; T-054 review L1). Every other error of Resolve is the
// request's own context or a server fault (a schema that does not compile,
// a snapshot that passed the schema and does not decode, a manifest that
// passed the shape check and does not hash), never the storage's.
type ReadError struct {
	DatasetVersionID string
	Err              error
}

func (e *ReadError) Error() string {
	return "reading dataset version " + e.DatasetVersionID + ": " + e.Err.Error()
}

func (e *ReadError) Unwrap() error { return e.Err }

// Failure is the error an API route returns for err, an error of Resolve
// (or one that wraps it), ctx being the request's context (T-054 review
// L2). When the request has ended it is the context's error joined with
// err, whatever err is (the store's own failure included): every route
// answers it as an ended request (503, no details), and the store's error
// is still found by errors.Is and errors.As. Otherwise it is err itself.
func Failure(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if cerr := ctx.Err(); cerr != nil && !errors.Is(err, cerr) {
		return errors.Join(cerr, err)
	}
	return err
}

// StorageFailure reports whether err (an error of Resolve as Failure
// returns it, or one that wraps it) is a snapshot that could not be read
// while the request was alive, and returns that read failure: the routes
// answer it with 503 DATASET_STORAGE_FAILED and log it, never send it.
// A context error (the request ended, or a read says it did) is not one,
// nor any other error of Resolve (a server fault: 500, logged at ERROR).
func StorageFailure(err error) (*ReadError, bool) {
	var re *ReadError
	if !errors.As(err, &re) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, false
	}
	return re, true
}
