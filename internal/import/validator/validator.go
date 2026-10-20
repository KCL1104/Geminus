// Package validator is the import_validate job handler (T-017, ADR-024;
// APPROVED D-009, D-043; D-071, D-072 PROPOSED; TEST-012, TEST-013): the
// adapter between the worker loop and the pure internal/import/validation.
// For one claimed job, the validation of one mapping revision, it reads
// the import under the lease, reads the stored upload outside any
// transaction (checking it is the bytes the import names: SHA-256 and
// size) and maps every row; it stores the error report when rows are not
// valid, or, when every row is, reads the upload again and stores the
// canonical dataset a commit freezes (T-018); and it records the outcome
// with the job's end in one transaction under the lease
// (handler.Run.Finish): VALIDATED, AWAITING_MAPPING to be mapped again, or
// FAILED. It never touches a model, a draft or a version (D-009), and only
// cmd/worker registers it.
//
// The stale-job fence: the job validates one revision, and writes its
// outcome only while the import is still VALIDATING at that revision; a
// person who saved a newer mapping meanwhile makes the job complete
// without writing anything (migration 0014 refuses such a write anyway).
//
// Rows that do not map are an outcome, not a job failure. The job fails,
// and is retried while attempts remain, when object storage fails; when it
// fails for good the import follows it to FAILED (0014). An upload that is
// missing, or is not what the import names, fails the import at once.
package validator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"regexp"

	"github.com/kcl1104/geminus/internal/import/mapping"
	importprofile "github.com/kcl1104/geminus/internal/import/profile"
	"github.com/kcl1104/geminus/internal/import/report"
	"github.com/kcl1104/geminus/internal/import/validation"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	store "github.com/kcl1104/geminus/internal/storage/imports"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// Config is the handler's setup.
type Config struct {
	// Objects holds the uploads, error reports and canonical datasets
	// (D-044); nil when object storage is disabled (every job then fails,
	// retryable).
	Objects objects.Store
	// Actor names the worker in audit events (WORKER_ID).
	Actor string
	// Limits bound the validation (validation.DefaultLimits when zero).
	Limits validation.Limits
	Log    *slog.Logger
}

// Handler runs import_validate jobs (handler.Handler).
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
		return nil, errors.New("validator: a log is required")
	case !actorRef.MatchString(cfg.Actor):
		return nil, errors.New("validator: the worker's name must start with a letter and use only letters, digits, '_', '.', ':' and '-' (at most 128) to name it in audit events")
	}
	if cfg.Limits == (validation.Limits{}) {
		cfg.Limits = validation.DefaultLimits
	}
	return &Handler{cfg: cfg}, nil
}

func failure(code, message string, retryable bool) error {
	return &jobs.Failure{Code: code, Message: message, Retryable: retryable}
}

// payload is the job's reference (internal/import/upload SaveMapping).
type payload struct {
	ImportID        string `json:"import_id"`
	MappingRevision int64  `json:"mapping_revision"`
	RequestID       string `json:"request_id"`
}

var formats = map[string]importprofile.Format{"csv_events": importprofile.CSV, "json_events": importprofile.JSONEvents}

// Validation is common.schema.json#/$defs/Validation: the import's
// validation of its current mapping revision.
type Validation struct {
	Valid  bool                    `json:"valid"`
	Issues []model.ValidationIssue `json:"issues"`
}

// outcome is what an attempt records on its import.
type outcome struct {
	failure            *store.Failure
	valid              bool
	validation         []byte
	preview            []byte
	rows, invalid, max int
	report, dataset    *store.Report
}

// Handle runs one attempt of an import_validate job.
func (h *Handler) Handle(ctx context.Context, hr handler.Run) error {
	job := hr.Job()
	var p payload
	if err := json.Unmarshal(job.Payload, &p); err != nil || p.ImportID != job.Resource.ImportID || !importIDs.MatchString(p.ImportID) ||
		p.MappingRevision < 1 || !reqIDs.MatchString(p.RequestID) {
		return failure(store.InputInvalid, "the job does not name its import, mapping revision and request", false)
	}
	org := job.OrganizationID
	var imp store.Import
	current := false
	if err := hr.Checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
		var err error
		imp, err = store.Lock(ctx, tx, org, p.ImportID)
		if errors.Is(err, store.ErrNotFound) {
			return failure(store.InputInvalid, "the job's import is not visible to its organization", false)
		}
		current = err == nil && imp.Status == store.Validating && imp.MappingRevision == p.MappingRevision
		return err
	}); err != nil {
		return err
	}
	var out outcome
	if current {
		var err error
		if out, err = h.validate(ctx, org, job.Attempt, imp, p.MappingRevision); err != nil {
			return err
		}
	}
	return hr.Finish(ctx, jobs.Completed, func(ctx context.Context, tx tenant.Tx) error {
		cur, err := store.Lock(ctx, tx, org, p.ImportID)
		if err != nil {
			return err
		}
		meta := map[string]any{"attempt": job.Attempt, "mapping_revision": p.MappingRevision}
		switch {
		case !current || cur.Status != store.Validating || cur.MappingRevision != p.MappingRevision:
			// A newer mapping, or the import ended: this revision's job
			// writes nothing (the stale-job fence).
			meta["outcome"] = "stale"
		case out.failure != nil:
			data, err := json.Marshal(out.failure)
			if err != nil {
				return err
			}
			if err := store.FailValidating(ctx, tx, org, cur.ID, p.MappingRevision, data); err != nil {
				return err
			}
			meta["outcome"], meta["code"] = string(store.Failed), out.failure.Code
		case out.valid:
			if err := store.Valid(ctx, tx, org, cur.ID, p.MappingRevision, out.validation, out.preview, *out.dataset); err != nil {
				return err
			}
			meta["outcome"], meta["rows"], meta["dataset_size_bytes"] = string(store.Validated), out.rows, out.dataset.Size
		default:
			if err := store.NotValid(ctx, tx, org, cur.ID, p.MappingRevision, out.validation, out.preview, out.invalid, *out.report); err != nil {
				return err
			}
			meta["outcome"], meta["rows"], meta["invalid_rows"], meta["issues"] = string(store.AwaitingMapping), out.rows, out.invalid, out.max
		}
		// Counts and codes only: no column name, value or file name (SECURITY §9).
		return auditlog.AppendWorker(ctx, tx, org, h.cfg.Actor, auditlog.Event{Action: "import.validate", TargetType: "import",
			TargetID: p.ImportID, RequestID: p.RequestID, Metadata: meta})
	})
}

// counted hashes and counts what is read through it.
type counted struct {
	r io.Reader
	h hash.Hash
	n int64
}

func (c *counted) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.h.Write(b[:n])
	c.n += int64(n)
	return n, err
}

// storeError marks a read of the stored upload that failed (as opposed to
// a file that does not read as profiled).
type storeError struct{ err error }

func (e storeError) Error() string { return e.err.Error() }
func (e storeError) Unwrap() error { return e.err }

type storeReader struct{ r io.Reader }

func (s storeReader) Read(b []byte) (int, error) {
	n, err := s.r.Read(b)
	if err != nil && err != io.EOF {
		err = storeError{err}
	}
	return n, err
}

var (
	corrupt = &store.Failure{Code: store.UploadCorrupt, Message: "the stored file is not the one that was uploaded (size or SHA-256)"}
	missing = &store.Failure{Code: store.UploadMissing, Message: "the uploaded file is not in object storage"}
	changed = &store.Failure{Code: store.UploadChanged, Message: "the stored file does not read as the file that was profiled"}
)

// read opens the stored upload of imp and runs pass on it, then reads the
// rest and checks the whole is the upload. The outcome's failure is set
// for an upload that is missing, is not the upload, or (with the right
// SHA-256 and size) does not read as profiled; err for the store, the
// context or anything else.
func (h *Handler) read(ctx context.Context, imp store.Import, pass func(io.Reader) error) (*store.Failure, error) {
	rc, err := h.cfg.Objects.Get(ctx, imp.UploadKey)
	if errors.Is(err, objects.ErrNotFound) {
		return missing, nil
	}
	if err != nil {
		return nil, h.storageFailure(ctx, err)
	}
	defer rc.Close()
	c := &counted{r: storeReader{io.LimitReader(rc, imp.Size+1)}, h: sha256.New()}
	perr := pass(c)
	var fail *importprofile.Failure
	var se storeError
	switch {
	case perr == nil, errors.As(perr, &fail), errors.Is(perr, validation.ErrChanged):
		if _, err := io.Copy(io.Discard, c); err != nil {
			return nil, h.storageFailure(ctx, err)
		}
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case errors.As(perr, &se):
		return nil, h.storageFailure(ctx, perr)
	default:
		return nil, perr
	}
	if c.n != imp.Size || "sha256:"+hex.EncodeToString(c.h.Sum(nil)) != imp.UploadSHA256 {
		return corrupt, nil
	}
	if perr != nil {
		return changed, nil
	}
	return nil, nil
}

// validate validates every row of imp's upload under its mapping of
// revision rev, outside any transaction, and stores the error report or
// the canonical dataset.
func (h *Handler) validate(ctx context.Context, org string, attempt int, imp store.Import, rev int64) (outcome, error) {
	if !store.OwnKey(org, imp.ID, imp.UploadKey) {
		return outcome{failure: &store.Failure{Code: store.UploadCorrupt, Message: "the import names an object outside its own key prefix"}}, nil
	}
	format, ok := formats[imp.Kind]
	if !ok {
		return outcome{}, failure(store.InputInvalid, "the import's kind has no rows to map", false)
	}
	var m mapping.Mapping
	if err := json.Unmarshal(imp.Mapping, &m); err != nil {
		return outcome{}, failure(store.InputInvalid, "the import's mapping cannot be read", false)
	}
	var prof importprofile.Profile
	var preview []jsontext.Value
	if json.Unmarshal(imp.Profile, &prof) != nil || json.Unmarshal(imp.Preview, &preview) != nil {
		return outcome{}, failure(store.InputInvalid, "the import's profile cannot be read", false)
	}
	columns := make([]string, len(prof.Columns))
	for i, c := range prof.Columns {
		columns[i] = c.Name
	}
	plan, err := mapping.Compile(m, columns)
	if err != nil {
		return outcome{}, failure(store.InputInvalid, "the import's mapping does not fit its columns", false)
	}
	if h.cfg.Objects == nil {
		return outcome{}, failure(store.ObjectStorageUnavailable, "this worker has no object storage", true)
	}
	lim := h.cfg.Limits
	lim.PreviewRows = min(lim.PreviewRows, len(preview)) // the canonical preview covers the raw preview's rows
	var res validation.Outcome
	fail, err := h.read(ctx, imp, func(r io.Reader) error {
		var err error
		res, err = validation.Validate(ctx, r, format, plan, lim)
		return err
	})
	if err != nil {
		return outcome{}, err
	}
	if fail == nil && res.Rows != imp.TotalRows {
		fail = changed
	}
	if fail != nil {
		return outcome{failure: fail}, nil
	}
	issues := res.Issues
	if issues == nil {
		issues = []model.ValidationIssue{}
	}
	out := outcome{valid: res.Valid(), rows: res.Rows, invalid: res.InvalidRows, max: len(issues)}
	if out.validation, err = json.Marshal(Validation{Valid: out.valid, Issues: issues}); err != nil {
		return outcome{}, err
	}
	if out.preview, err = json.Marshal(res.Preview); err != nil {
		return outcome{}, err
	}
	if !out.valid {
		data, err := report.Render(res.Errors, res.MoreErrors, report.MaxBytes)
		if err != nil {
			return outcome{}, err
		}
		// Each revision and attempt its own key: a retry never meets an
		// earlier attempt's object.
		key, err := objects.NewKey(org, objects.Imports, imp.ID, fmt.Sprintf("validation-errors-r%d-a%d.csv", rev, attempt))
		if err != nil {
			return outcome{}, err
		}
		obj, err := h.cfg.Objects.Put(ctx, key, bytes.NewReader(data), report.MaxBytes)
		if err != nil {
			return outcome{}, h.storageFailure(ctx, err)
		}
		out.report = &store.Report{Key: obj.Key, SHA256: obj.SHA256, Size: obj.Size}
		return out, nil
	}
	dataset, fail, err := h.write(ctx, org, attempt, imp, rev, format, plan, lim)
	if err != nil {
		return outcome{}, err
	}
	if fail == nil && dataset.Size != res.DatasetBytes {
		fail = changed
	}
	if fail != nil {
		return outcome{failure: fail}, nil
	}
	out.dataset = &dataset
	return out, nil
}

var errStopped = errors.New("validator: the dataset was not stored")

// write reads imp's upload again and stores its canonical dataset (pass
// 2), the writer feeding object storage through a pipe so that neither
// the file nor the dataset is ever held whole.
func (h *Handler) write(ctx context.Context, org string, attempt int, imp store.Import, rev int64, format importprofile.Format,
	plan *mapping.Plan, lim validation.Limits) (store.Report, *store.Failure, error) {
	key, err := objects.NewKey(org, objects.Imports, imp.ID, fmt.Sprintf("canonical-r%d-a%d.json", rev, attempt))
	if err != nil {
		return store.Report{}, nil, err
	}
	pr, pw := io.Pipe()
	type result struct {
		fail *store.Failure
		err  error
	}
	done := make(chan result, 1)
	go func() {
		fail, err := h.read(ctx, imp, func(r io.Reader) error {
			_, err := validation.Write(ctx, r, format, plan, lim, pw)
			return err
		})
		switch {
		case err != nil:
			pw.CloseWithError(err)
		case fail != nil:
			pw.CloseWithError(errStopped) // nothing is stored for a file that does not read as validated
		default:
			pw.Close()
		}
		done <- result{fail, err}
	}()
	obj, perr := h.cfg.Objects.Put(ctx, key, pr, lim.MaxDatasetBytes)
	pr.CloseWithError(errStopped) // a writer the store stopped reading stops too
	r := <-done
	if errors.Is(r.err, errStopped) {
		r.err = nil // the store stopped reading: its own error says why
	}
	switch {
	case r.err != nil:
		return store.Report{}, nil, r.err
	case r.fail != nil:
		return store.Report{}, r.fail, nil
	case errors.Is(perr, objects.ErrTooLarge):
		return store.Report{}, changed, nil // Validate measured it within the limit
	case perr != nil:
		return store.Report{}, nil, h.storageFailure(ctx, perr)
	}
	return store.Report{Key: obj.Key, SHA256: obj.SHA256, Size: obj.Size}, nil, nil
}

// storageFailure is how a failed read or write of object storage ends the
// attempt: retried (the store may be back), unless the attempt is stopping.
func (h *Handler) storageFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	h.cfg.Log.WarnContext(ctx, "object storage failed while validating an import", "error", err.Error())
	return failure(store.ObjectStorageFailed, "object storage failed while the file was validated", true)
}
