// Package upload is the import upload service and its HTTP adapter (T-016,
// API_DATABASE §5, D-007, D-009, D-035, D-043, D-044; TEST-012, TEST-029):
// it streams an uploaded CSV or JSON file into private object storage,
// records the import with its profile job, and serves the import and its
// error report. SQL lives in internal/storage/imports; the job that
// profiles the file is internal/import/profiler's.
//
// An upload is three steps, so that nothing is stored for a caller who may
// not upload, and the file is read once, streaming:
//   - Begin checks, before any of the body is read, that the caller may
//     upload (owner or editor, D-008) and that object storage exists.
//   - Receive checks what the upload names, then streams the file to a key
//     the server makes (tenant/<org>/imports/<import>/upload, D-035) while
//     computing its SHA-256 and size, at most MaxBytes whether or not the
//     request said how long it is; archives and executables are refused
//     from the file's name and first bytes before anything is stored. A
//     retry of a request (its Idempotency-Key already used) is only hashed,
//     never stored again.
//   - Commit records the import UPLOADED, its import_profile job, its audit
//     event and the idempotency response in one transaction (D-011): 202
//     means the job is durably queued. Nothing about the Twin changes, now
//     or when the file is profiled (D-009).
//
// An object stored by a request that then fails (a refused trailing part,
// a lost race on the key, a failed commit) is an orphan for the controlled
// maintenance D-044 leaves for later; nothing ever names it.
package upload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	store "github.com/kcl1104/geminus/internal/storage/imports"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/tenant"
)

var (
	// ErrNotFound: no such import, project, model or version in the
	// organization (or no access); the same for all (TEST-009).
	ErrNotFound = errors.New("upload: not found")
	// ErrStorageUnavailable: no object storage is configured (503; D-044).
	ErrStorageUnavailable = errors.New("upload: object storage is not configured")
	// ErrTooLarge: the file is larger than MaxBytes; nothing was kept (413).
	ErrTooLarge = errors.New("upload: the file is too large")
	// ErrNoReport: the import has no error report (404).
	ErrNoReport = errors.New("upload: the import has no error report")
	// ErrReportCorrupt: the stored report is missing or is not the one the
	// import names (size or SHA-256), or is named outside the import's own
	// key prefix: nothing is served (500, fail closed).
	ErrReportCorrupt = errors.New("upload: the stored error report does not match its record")
	// ErrRetry: the key was used before this request read the file and is
	// free now (it expired in between): retry the request (503).
	ErrRetry = errors.New("upload: retry the request")
)

// RequestError is an upload whose fields are not acceptable (422
// REQUEST_VALIDATION_FAILED); Issues point at the fields (/project_id,
// /file, ...).
type RequestError struct{ Issues []model.ValidationIssue }

func (e *RequestError) Error() string {
	return fmt.Sprintf("upload: invalid upload (%d issue(s))", len(e.Issues))
}

func invalid(path, message string) *RequestError {
	return &RequestError{Issues: []model.ValidationIssue{model.NewIssue(validate.ConstraintFailed, path, message, model.SeverityError)}}
}

// FormatError is a file of a format no import accepts: an archive or an
// executable (415 UNSUPPORTED_MEDIA_TYPE, IMPORT_FORMAT_REJECTED).
type FormatError struct{ Format string }

func (e *FormatError) Error() string { return "upload: " + e.Format + " is not accepted" }

// FormatRejected is FormatError's code in error details.
const FormatRejected = "IMPORT_FORMAT_REJECTED"

// DefaultMaxBytes is D-007's upload limit (the database's size_bytes CHECK).
const DefaultMaxBytes = 50_000_000

// Service runs upload operations for a verified scope.
type Service struct {
	DB *tenant.DB
	// Objects holds the uploads and error reports (D-044); nil when object
	// storage is disabled, so uploads fail closed (503).
	Objects objects.Store
	// MaxBytes bounds a file (DefaultMaxBytes when 0; tests lower it).
	MaxBytes int64
	// Policy is the queue's (jobs.DefaultPolicy when zero).
	Policy jobs.Policy
}

// The roles each operation requires (x-required-roles).
var (
	writeRoles = tenant.Writers
	readRoles  = tenant.Members
)

const uploadOperation = "uploadImport"

func (s *Service) maxBytes() int64 {
	if s.MaxBytes <= 0 {
		return DefaultMaxBytes
	}
	return s.MaxBytes
}

func (s *Service) policy() jobs.Policy {
	if s.Policy == (jobs.Policy{}) {
		return jobs.DefaultPolicy
	}
	return s.Policy
}

// Kinds are the import kinds (import.schema.json kind).
var Kinds = []string{"csv_events", "json_events", "json_model"}

// Fields are an upload's form fields and the file's name.
type Fields struct {
	ProjectID     string `json:"project_id"`
	ModelID       string `json:"model_id"`
	BaseVersionID string `json:"base_version_id"`
	Kind          string `json:"kind"`
	Filename      string `json:"filename"`
}

var (
	projectIDs = regexp.MustCompile(`^prj_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	modelIDs   = regexp.MustCompile(`^mdl_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	versionIDs = regexp.MustCompile(`^mv_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	importIDs  = regexp.MustCompile(`^imp_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
)

// Check reports what is wrong with f's form fields (the file name is the
// HTTP adapter's to check), JSON Pointers at the fields.
func (f Fields) Check() []model.ValidationIssue {
	var issues []model.ValidationIssue
	add := func(path, message string) {
		issues = append(issues, model.NewIssue(validate.ConstraintFailed, path, message, model.SeverityError))
	}
	if !projectIDs.MatchString(f.ProjectID) {
		add("/project_id", "project_id must be a project ID")
	}
	if !modelIDs.MatchString(f.ModelID) {
		add("/model_id", "model_id must be a model ID")
	}
	if !versionIDs.MatchString(f.BaseVersionID) {
		add("/base_version_id", "base_version_id must be a model version ID")
	}
	known := false
	for _, k := range Kinds {
		known = known || f.Kind == k
	}
	if !known {
		add("/kind", "kind must be csv_events, json_events or json_model")
	}
	return issues
}

// Begin checks, before the body is read, that the caller may upload in the
// scope's organization and that uploads can be stored.
func (s *Service) Begin(ctx context.Context, sc tenant.Scope) error {
	if s.Objects == nil {
		return ErrStorageUnavailable
	}
	return s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		return m.Require(writeRoles...)
	})
}

// Received is a file Receive read: its SHA-256 and size, and where it is
// stored ("" when it was only hashed, its request a retry).
type Received struct {
	ImportID, Key string
	SHA256        string
	Size          int64
}

// targets checks that the project, model and version f names exist in org
// and belong together.
func targets(ctx context.Context, tx tenant.Tx, org string, f Fields) error {
	t, err := store.Targets(ctx, tx, org, f.ProjectID, f.ModelID, f.BaseVersionID)
	if err != nil {
		return err
	}
	switch {
	case !t.Project || !t.Model || !t.Version:
		return ErrNotFound
	case t.ModelProject != f.ProjectID:
		return invalid("/model_id", "the model is not in project_id")
	case t.VersionModel != f.ModelID:
		return invalid("/base_version_id", "the version is not a version of model_id")
	}
	return nil
}

func (s *Service) key(sc tenant.Scope, key string) idempotency.Key {
	return idempotency.Key{Org: sc.OrganizationID, Actor: sc.UserID, Operation: uploadOperation, Key: key}
}

// SourceError is a failure to read the uploaded file from the request (the
// client went away, the body was cut off or too large), as opposed to a
// failure of the store.
type SourceError struct{ Err error }

func (e *SourceError) Error() string { return "upload: reading the file: " + e.Err.Error() }
func (e *SourceError) Unwrap() error { return e.Err }

type source struct{ r io.Reader }

func (s source) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && err != io.EOF {
		err = &SourceError{Err: err}
	}
	return n, err
}

// Receive checks what f names (404 when it is not the organization's, 422
// when the parts do not belong together) and reads the file from r: it is
// refused if it is an archive or an executable (FormatError) or empty, and
// otherwise stored under a new key, or, when key was used before, only
// hashed.
func (s *Service) Receive(ctx context.Context, sc tenant.Scope, key string, f Fields, r io.Reader) (Received, error) {
	if s.Objects == nil {
		return Received{}, ErrStorageUnavailable
	}
	r = source{r: r}
	var seen bool
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(writeRoles...); err != nil {
			return err
		}
		if err := targets(ctx, tx, m.OrganizationID, f); err != nil {
			return err
		}
		var err error
		seen, err = idempotency.Seen(ctx, tx, s.key(sc, key))
		return err
	})
	if err != nil {
		return Received{}, err
	}
	head := make([]byte, SniffBytes)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return Received{}, err
	}
	head = head[:n]
	if n == 0 {
		return Received{}, invalid("/file", "the file is empty")
	}
	if format := Sniff(f.Filename, head); format != "" {
		return Received{}, &FormatError{Format: format}
	}
	file := io.MultiReader(bytes.NewReader(head), r)
	if seen {
		// A replay or a conflict: Commit decides from the hash alone.
		h := sha256.New()
		size, err := io.Copy(h, io.LimitReader(file, s.maxBytes()+1))
		if err != nil {
			return Received{}, err
		}
		if size > s.maxBytes() {
			return Received{}, ErrTooLarge
		}
		return Received{SHA256: "sha256:" + hex.EncodeToString(h.Sum(nil)), Size: size}, nil
	}
	id := ids.New("imp")
	objectKey, err := objects.NewKey(sc.OrganizationID, objects.Imports, id, "upload")
	if err != nil {
		return Received{}, err
	}
	obj, err := s.Objects.Put(ctx, objectKey, file, s.maxBytes())
	if errors.Is(err, objects.ErrTooLarge) {
		return Received{}, ErrTooLarge
	}
	if err != nil {
		return Received{}, err
	}
	return Received{ImportID: id, Key: obj.Key, SHA256: obj.SHA256, Size: obj.Size}, nil
}

// Accepted is http.schema.json#/$defs/ImportAccepted.
type Accepted struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// fingerprint is the request an Idempotency-Key stands for: the fields,
// the file name and the file's SHA-256 and size (API_DATABASE §7).
func fingerprint(f Fields, r Received) (string, error) {
	data, err := json.Marshal(map[string]any{"project_id": f.ProjectID, "model_id": f.ModelID, "base_version_id": f.BaseVersionID,
		"kind": f.Kind, "filename": f.Filename, "file_sha256": r.SHA256, "file_size": r.Size}, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	return hashing.Of(data)
}

// Commit records the import of the file Receive stored, its profile job and
// its audit event, or replays the response of the earlier request with the
// same Idempotency-Key (same request: the same answer; another one:
// idempotency.ErrConflict). It returns the HTTP status (202) and body.
func (s *Service) Commit(ctx context.Context, sc tenant.Scope, requestID, key string, f Fields, r Received) (int, []byte, error) {
	hash, err := fingerprint(f, r)
	if err != nil {
		return 0, nil, err
	}
	k := s.key(sc, key)
	var status int
	var out []byte
	err = s.DB.Run(ctx, sc, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(writeRoles...); err != nil {
			return err
		}
		replay, err := idempotency.Claim(ctx, tx, k, hash)
		if err != nil {
			return err
		}
		if replay != nil {
			status = replay.Status
			out, err = hashing.Canonical(replay.Body)
			return err
		}
		if r.Key == "" {
			return ErrRetry // only hashed, and the key is free after all
		}
		if err := targets(ctx, tx, m.OrganizationID, f); err != nil {
			return err
		}
		imp, err := store.Insert(ctx, tx, store.Import{ID: r.ImportID, OrganizationID: m.OrganizationID, ProjectID: f.ProjectID,
			ModelID: f.ModelID, BaseVersionID: f.BaseVersionID, Kind: f.Kind, Filename: f.Filename, UploadKey: r.Key,
			UploadSHA256: r.SHA256, Size: r.Size, CreatedBy: m.UserID})
		if err != nil {
			return err
		}
		if _, err := jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: m.OrganizationID, Type: jobs.ImportProfile,
			Resource: jobs.Resource{ImportID: imp.ID}, Payload: map[string]any{"import_id": imp.ID, "request_id": requestID}}, s.policy()); err != nil {
			return err
		}
		// What was uploaded by its hash and size only; no file name or
		// content (SECURITY §9).
		if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{Action: "import.upload", TargetType: "import",
			TargetID: imp.ID, RequestID: requestID, Metadata: map[string]any{"kind": f.Kind, "model_id": f.ModelID,
				"base_version_id": f.BaseVersionID, "size_bytes": r.Size, "upload_content_hash": r.SHA256}}); err != nil {
			return err
		}
		data, err := json.Marshal(Accepted{ID: imp.ID, Status: string(store.Uploaded)})
		if err != nil {
			return err
		}
		if out, err = hashing.Canonical(data); err != nil {
			return err
		}
		status = 202
		return idempotency.Complete(ctx, tx, k, idempotency.Response{Status: status, Body: out})
	})
	return status, out, err
}
