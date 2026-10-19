// Package imports is the SQL of twin.imports for uploads and their
// profiling (T-016, API_DATABASE §5, migrations 0001, 0002 and 0010). Every
// statement runs in a scoped transaction (tenant.DB.Run for the API, the
// worker's lease-fenced Checkpoint and Finish) and also filters by
// organization.
//
// The API inserts an import UPLOADED with its upload's object key, SHA-256
// and size, which never change, and its one profile job; the worker moves
// it to PROFILING and then to AWAITING_MAPPING with its profile, preview
// and error report, or to FAILED with why; when the job fails or is
// cancelled first, the import follows it to FAILED with the job's error
// (0010 holds every role to that).
package imports

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/storage/objects"
)

// Status is an import's state.
type Status string

const (
	Uploaded        Status = "UPLOADED"
	Profiling       Status = "PROFILING"
	AwaitingMapping Status = "AWAITING_MAPPING"
	Validated       Status = "VALIDATED"
	Committed       Status = "COMMITTED"
	Failed          Status = "FAILED"
)

// Report names an import's error report in object storage (D-044).
type Report struct {
	Key, SHA256 string
	Size        int64
}

// Import is one row of twin.imports, with the request that uploaded it.
type Import struct {
	ID, OrganizationID, ProjectID, ModelID, BaseVersionID string
	Kind, Filename                                        string
	UploadKey, UploadSHA256                               string
	Size                                                  int64
	Status                                                Status
	MappingRevision                                       int64
	TotalRows, InvalidRows                                int
	// The JSON columns as stored; nil for SQL NULL.
	Profile, Preview, Mapping, Validation, MappingSuggestion []byte
	MappingSuggestionStatus                                  string
	// Failure is why the import FAILED (the stored failure JSON); nil
	// otherwise.
	Failure                      []byte
	Report                       *Report
	DatasetVersionID, ProposalID *string
	CreatedBy                    string
	CreatedAt                    time.Time
	// RequestID is the request that uploaded the file (its profile job's
	// payload).
	RequestID string
}

// Failure is the error JSON of a FAILED import: why its file cannot be
// imported (a profile failure, with a JSON Pointer into the file) or why
// its profile job could not profile it (the job's jobs.Failure, without
// one, which the import takes when it follows its job, 0010).
type Failure struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Path      string `json:"path,omitzero"`
	Retryable bool   `json:"retryable"`
}

// The profile job's own failure codes (internal/import/profiler).
const (
	// ObjectStorageFailed, ObjectStorageUnavailable: the store failed, or
	// the worker has none; retried.
	ObjectStorageFailed      = "OBJECT_STORAGE_FAILED"
	ObjectStorageUnavailable = "OBJECT_STORAGE_UNAVAILABLE"
	// UploadMissing, UploadCorrupt: the stored upload is not there, or is
	// not what the import names; the import fails.
	UploadMissing = "UPLOAD_MISSING"
	UploadCorrupt = "UPLOAD_CORRUPT"
	// InputInvalid: the job does not name its import, or its import is not
	// one it can profile; not retried.
	InputInvalid = "IMPORT_INPUT_INVALID"
	// JobCancelled: the profile job ended cancelled, without an error,
	// before it profiled the file (0010 records it; an operator's action,
	// the API having no route that cancels one).
	JobCancelled = "JOB_CANCELLED"
)

// OwnKey reports whether key is a server-made key under import id's own
// prefix of org's imports area: tenant/<org>/imports/<import>/...
func OwnKey(org, id, key string) bool {
	return objects.InArea(org, objects.Imports, key) && strings.HasPrefix(key, "tenant/"+org+"/imports/"+id+"/")
}

// ErrNotFound: no such import in the organization.
var ErrNotFound = errors.New("imports: not found")

// ErrNotProfiling: the import is not in the state the write needs; nothing
// was written.
var ErrNotProfiling = errors.New("imports: the import is not being profiled")

// Insert stores i UPLOADED.
func Insert(ctx context.Context, tx pgx.Tx, i Import) (Import, error) {
	err := tx.QueryRow(ctx, `INSERT INTO twin.imports(id,organization_id,project_id,model_id,base_version_id,kind,filename,
		  upload_object_key,upload_content_hash,size_bytes,status,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'UPLOADED',$11) RETURNING created_at`,
		i.ID, i.OrganizationID, i.ProjectID, i.ModelID, i.BaseVersionID, i.Kind, i.Filename, i.UploadKey, i.UploadSHA256, i.Size,
		i.CreatedBy).Scan(&i.CreatedAt)
	if err != nil {
		return Import{}, err
	}
	i.Status = Uploaded
	return i, nil
}

const columns = `i.id, i.project_id, i.model_id, i.base_version_id, i.kind, i.filename, i.upload_object_key, i.upload_content_hash,
	i.size_bytes, i.status, i.mapping_revision, i.total_rows, i.invalid_rows, i.profile, i.preview, i.mapping, i.validation,
	i.mapping_suggestion_status, i.mapping_suggestion, i.error, i.error_report_object_key, i.error_report_content_hash,
	i.error_report_size_bytes, i.dataset_version_id, i.proposal_id, i.created_by, i.created_at,
	coalesce(j.payload->>'request_id', '')`

// from joins each import with its profile job (one at most, 0010; the API
// queues it with the import).
const from = ` FROM twin.imports i LEFT JOIN twin.jobs j
	  ON j.organization_id=i.organization_id AND j.import_id=i.id AND j.type='import_profile'`

func scan(row pgx.Row, org string) (Import, error) {
	i := Import{OrganizationID: org}
	var status string
	var key, hash *string
	var size *int64
	err := row.Scan(&i.ID, &i.ProjectID, &i.ModelID, &i.BaseVersionID, &i.Kind, &i.Filename, &i.UploadKey, &i.UploadSHA256,
		&i.Size, &status, &i.MappingRevision, &i.TotalRows, &i.InvalidRows, &i.Profile, &i.Preview, &i.Mapping, &i.Validation,
		&i.MappingSuggestionStatus, &i.MappingSuggestion, &i.Failure, &key, &hash, &size, &i.DatasetVersionID, &i.ProposalID,
		&i.CreatedBy, &i.CreatedAt, &i.RequestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Import{}, ErrNotFound
	}
	if err != nil {
		return Import{}, err
	}
	i.Status = Status(status)
	if key != nil && hash != nil && size != nil {
		i.Report = &Report{Key: *key, SHA256: *hash, Size: *size}
	}
	return i, nil
}

// Get returns the import id of organization org.
func Get(ctx context.Context, tx pgx.Tx, org, id string) (Import, error) {
	return scan(tx.QueryRow(ctx, `SELECT `+columns+from+` WHERE i.organization_id=$1 AND i.id=$2`, org, id), org)
}

// Lock returns the import and holds its row until the transaction ends.
func Lock(ctx context.Context, tx pgx.Tx, org, id string) (Import, error) {
	return scan(tx.QueryRow(ctx, `SELECT `+columns+from+` WHERE i.organization_id=$1 AND i.id=$2 FOR UPDATE OF i`, org, id), org)
}

// StartProfiling moves an UPLOADED import to PROFILING (the worker, as its
// profile job's attempt starts); one already PROFILING (a retry) stays.
func StartProfiling(ctx context.Context, tx pgx.Tx, org, id string) error {
	tag, err := tx.Exec(ctx, `UPDATE twin.imports SET status='PROFILING'
		WHERE organization_id=$1 AND id=$2 AND status IN ('UPLOADED','PROFILING')`, org, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotProfiling
	}
	return nil
}

// Profiled records the profile of a PROFILING import, which then awaits
// its mapping: the profile and preview (JSON), the row counts and the error
// report, if any.
func Profiled(ctx context.Context, tx pgx.Tx, org, id string, profile, preview []byte, rows, invalid int, report *Report) error {
	var key, hash *string
	var size *int64
	if report != nil {
		key, hash, size = &report.Key, &report.SHA256, &report.Size
	}
	tag, err := tx.Exec(ctx, `UPDATE twin.imports SET status='AWAITING_MAPPING', profile=$3, preview=$4, total_rows=$5, invalid_rows=$6,
		  error_report_object_key=$7, error_report_content_hash=$8, error_report_size_bytes=$9
		WHERE organization_id=$1 AND id=$2 AND status='PROFILING'`, org, id, string(profile), string(preview), rows, invalid, key, hash, size)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotProfiling
	}
	return nil
}

// Fail records why a PROFILING import cannot be imported (failure, JSON).
func Fail(ctx context.Context, tx pgx.Tx, org, id string, failure []byte) error {
	tag, err := tx.Exec(ctx, `UPDATE twin.imports SET status='FAILED', error=$3 WHERE organization_id=$1 AND id=$2 AND status='PROFILING'`,
		org, id, string(failure))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotProfiling
	}
	return nil
}

// Target is what an upload names, as the organization has it: whether the
// project, the model and the version exist, the model's project and the
// version's model.
type Target struct {
	Project, Model, Version    bool
	ModelProject, VersionModel string
}

// Targets looks up the project, model and version an upload names.
func Targets(ctx context.Context, tx pgx.Tx, org, project, model, version string) (Target, error) {
	var t Target
	var modelProject, versionModel *string
	err := tx.QueryRow(ctx, `SELECT
		  EXISTS (SELECT 1 FROM twin.projects WHERE organization_id=$1 AND id=$2),
		  (SELECT project_id FROM twin.models WHERE organization_id=$1 AND id=$3),
		  (SELECT model_id FROM twin.model_versions WHERE organization_id=$1 AND id=$4)`,
		org, project, model, version).Scan(&t.Project, &modelProject, &versionModel)
	if err != nil {
		return Target{}, err
	}
	if modelProject != nil {
		t.Model, t.ModelProject = true, *modelProject
	}
	if versionModel != nil {
		t.Version, t.VersionModel = true, *versionModel
	}
	return t, nil
}
