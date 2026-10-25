// Package profiler is the import_profile job handler (T-016, ADR-024): the
// adapter between the worker loop and the pure internal/import/profile. For
// one claimed job it marks the import PROFILING under the lease, reads the
// stored upload outside any transaction (checking it is the bytes the
// import names: SHA-256 and size), profiles it, stores the error report
// when rows are invalid, and records the outcome with the job's end in one
// transaction under the lease (handler.Run.Finish): AWAITING_MAPPING with
// the profile and preview, or FAILED with why the file cannot be imported.
// It never touches a model, a draft or a version (D-009), and only
// cmd/worker registers it.
//
// A file that cannot be profiled is an outcome, not a job failure: the job
// completes and the import is FAILED with the file's failure. The job
// fails, and is retried while attempts remain, when object storage fails
// (OBJECT_STORAGE_FAILED, OBJECT_STORAGE_UNAVAILABLE); when the job fails
// for good the import follows it to FAILED with the job's failure, in the
// same transaction (migration 0010). An upload that is missing or is not
// what its import names fails the import at once (UPLOAD_MISSING,
// UPLOAD_CORRUPT).
package profiler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"hash"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"time"

	importprofile "github.com/kcl1104/geminus/internal/import/profile"
	"github.com/kcl1104/geminus/internal/import/report"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	store "github.com/kcl1104/geminus/internal/storage/imports"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// Config is the handler's setup.
type Config struct {
	// Objects holds the uploads and error reports (D-044); nil when object
	// storage is disabled (every job then fails, retryable).
	Objects objects.Store
	// Actor names the worker in audit events (WORKER_ID).
	Actor string
	// Limits bound what a file may hold (importprofile.DefaultLimits when
	// zero).
	Limits importprofile.Limits
	Log    *slog.Logger
}

// Handler runs import_profile jobs (handler.Handler).
type Handler struct{ cfg Config }

var _ handler.Handler = (*Handler)(nil)

var (
	actorRef  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)
	importIDs = regexp.MustCompile(`^imp_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	reqIDs    = regexp.MustCompile(`^req_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
)

// New checks cfg.
func New(cfg Config) (*Handler, error) {
	switch {
	case cfg.Log == nil:
		return nil, errors.New("profiler: a log is required")
	case !actorRef.MatchString(cfg.Actor):
		return nil, errors.New("profiler: the worker's name must start with a letter and use only letters, digits, '_', '.', ':' and '-' (at most 128) to name it in audit events")
	}
	if cfg.Limits == (importprofile.Limits{}) {
		cfg.Limits = importprofile.DefaultLimits
	}
	return &Handler{cfg: cfg}, nil
}

func failure(code, message string, retryable bool) error {
	return &jobs.Failure{Code: code, Message: message, Retryable: retryable}
}

// payload is the job's reference (internal/import/upload Commit).
type payload struct {
	ImportID  string `json:"import_id"`
	RequestID string `json:"request_id"`
}

var formats = map[string]importprofile.Format{
	"csv_events": importprofile.CSV, "json_events": importprofile.JSONEvents, "json_model": importprofile.JSONModel,
}

// outcome is what an attempt records on its import.
type outcome struct {
	profile, preview []byte
	rows, invalid    int
	columns          int
	report           *store.Report
	failure          *store.Failure
}

// Handle runs one attempt of an import_profile job.
func (h *Handler) Handle(ctx context.Context, hr handler.Run) error {
	job := hr.Job()
	var p payload
	if err := json.Unmarshal(job.Payload, &p); err != nil || p.ImportID != job.Resource.ImportID || !importIDs.MatchString(p.ImportID) || !reqIDs.MatchString(p.RequestID) {
		return failure(store.InputInvalid, "the job does not name its import and request", false)
	}
	org := job.OrganizationID
	var imp store.Import
	if err := hr.Checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
		var err error
		imp, err = store.Lock(ctx, tx, org, p.ImportID)
		if errors.Is(err, store.ErrNotFound) {
			return failure(store.InputInvalid, "the job's import is not visible to its organization", false)
		}
		if err != nil {
			return err
		}
		if imp.Status != store.Uploaded && imp.Status != store.Profiling {
			return failure(store.InputInvalid, "the import is "+string(imp.Status)+", not waiting to be profiled", false)
		}
		return store.StartProfiling(ctx, tx, org, p.ImportID)
	}); err != nil {
		return err
	}
	began := time.Now()
	out, err := h.profile(ctx, org, job.Attempt, imp)
	if err != nil {
		return err
	}
	// The import's operational metric (T-035, D-095): counts and the
	// outcome, never a column name or value; the job's correlation comes
	// with ctx.
	result := "profiled"
	if out.failure != nil {
		result = "failed"
	}
	h.cfg.Log.InfoContext(ctx, "import profiled", "outcome", result, "rows", out.rows, "invalid_rows", out.invalid, "columns", out.columns,
		"size_bytes", imp.Size, "duration_ms", time.Since(began).Milliseconds())
	return hr.Finish(ctx, jobs.Completed, func(ctx context.Context, tx tenant.Tx) error {
		if _, err := store.Lock(ctx, tx, org, imp.ID); err != nil {
			return err
		}
		meta := map[string]any{"attempt": job.Attempt}
		if out.failure != nil {
			data, err := json.Marshal(out.failure)
			if err != nil {
				return err
			}
			if err := store.Fail(ctx, tx, org, imp.ID, data); err != nil {
				return err
			}
			meta["status"], meta["code"] = string(store.Failed), out.failure.Code
		} else {
			if err := store.Profiled(ctx, tx, org, imp.ID, out.profile, out.preview, out.rows, out.invalid, out.report); err != nil {
				return err
			}
			meta["status"], meta["rows"], meta["invalid_rows"], meta["columns"] = string(store.AwaitingMapping), out.rows, out.invalid, out.columns
			meta["error_report"] = out.report != nil
		}
		// Counts and codes only: no column name, value or file name (SECURITY §9).
		return auditlog.AppendWorker(ctx, tx, org, h.cfg.Actor, auditlog.Event{Action: "import.profile", TargetType: "import",
			TargetID: imp.ID, RequestID: p.RequestID, Metadata: meta})
	})
}

// counted hashes and counts what is read through it.
type counted struct {
	r io.Reader
	h hash.Hash
	n int64
}

func (c *counted) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.h.Write(p[:n])
	c.n += int64(n)
	return n, err
}

// storeError marks a read of the stored upload that failed (as opposed to
// a file that cannot be profiled).
type storeError struct{ err error }

func (e storeError) Error() string { return e.err.Error() }
func (e storeError) Unwrap() error { return e.err }

type storeReader struct{ r io.Reader }

func (s storeReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && err != io.EOF {
		err = storeError{err}
	}
	return n, err
}

// profile reads and profiles the stored upload of imp, outside any
// transaction, and stores the error report.
func (h *Handler) profile(ctx context.Context, org string, attempt int, imp store.Import) (outcome, error) {
	if !store.OwnKey(org, imp.ID, imp.UploadKey) {
		return outcome{failure: &store.Failure{Code: store.UploadCorrupt, Message: "the import names an object outside its own key prefix"}}, nil
	}
	format, ok := formats[imp.Kind]
	if !ok {
		return outcome{}, failure(store.InputInvalid, "the import's kind is unknown", false)
	}
	if h.cfg.Objects == nil {
		return outcome{}, failure(store.ObjectStorageUnavailable, "this worker has no object storage", true)
	}
	rc, err := h.cfg.Objects.Get(ctx, imp.UploadKey)
	if errors.Is(err, objects.ErrNotFound) {
		return outcome{failure: &store.Failure{Code: store.UploadMissing, Message: "the uploaded file is not in object storage"}}, nil
	}
	if err != nil {
		return outcome{}, h.storageFailure(ctx, err)
	}
	defer rc.Close()
	// One byte more than the upload is enough to tell an object that is
	// longer than it.
	c := &counted{r: storeReader{io.LimitReader(rc, imp.Size+1)}, h: sha256.New()}
	res, perr := importprofile.Read(ctx, c, format, h.cfg.Limits)
	var fail *importprofile.Failure
	switch {
	case errors.As(perr, &fail), perr == nil:
		// Read the rest: the whole stored object must be the upload.
		if _, err := io.Copy(io.Discard, c); err != nil {
			return outcome{}, h.storageFailure(ctx, err)
		}
	case ctx.Err() != nil:
		return outcome{}, ctx.Err()
	default:
		return outcome{}, h.storageFailure(ctx, perr)
	}
	if c.n != imp.Size || "sha256:"+hex.EncodeToString(c.h.Sum(nil)) != imp.UploadSHA256 {
		return outcome{failure: &store.Failure{Code: store.UploadCorrupt, Message: "the stored file is not the one that was uploaded (size or SHA-256)"}}, nil
	}
	if fail != nil {
		return outcome{failure: &store.Failure{Code: fail.Code, Message: fail.Message, Path: fail.Path}}, nil
	}
	profile, err := json.Marshal(res.Profile)
	if err != nil {
		return outcome{}, err
	}
	preview, err := json.Marshal(res.Preview)
	if err != nil {
		return outcome{}, err
	}
	out := outcome{profile: profile, preview: preview, rows: res.Profile.Rows, invalid: res.InvalidRows, columns: len(res.Profile.Columns)}
	if res.InvalidRows > 0 {
		entries := make([]report.Entry, len(res.RowErrors))
		for i, e := range res.RowErrors {
			entries[i] = report.Entry{Path: e.Path(), Line: e.Line, Column: e.Column, Code: e.Code, Message: e.Message, Value: e.Value}
		}
		data, err := report.Render(entries, res.MoreRowErrors, report.MaxBytes)
		if err != nil {
			return outcome{}, err
		}
		// Each attempt its own key: a retry never meets an earlier
		// attempt's object.
		key, err := objects.NewKey(org, objects.Imports, imp.ID, "profile-errors-"+strconv.Itoa(attempt)+".csv")
		if err != nil {
			return outcome{}, err
		}
		obj, err := h.cfg.Objects.Put(ctx, key, bytes.NewReader(data), report.MaxBytes)
		if err != nil {
			return outcome{}, h.storageFailure(ctx, err)
		}
		out.report = &store.Report{Key: obj.Key, SHA256: obj.SHA256, Size: obj.Size}
	}
	return out, nil
}

// storageFailure is how a failed read or write of object storage ends the
// attempt: retried (the store may be back), unless the attempt is stopping.
func (h *Handler) storageFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	h.cfg.Log.WarnContext(ctx, "object storage failed while profiling an import", "error", err.Error())
	return failure(store.ObjectStorageFailed, "object storage failed while the file was profiled", true)
}
