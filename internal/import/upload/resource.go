package upload

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	store "github.com/kcl1104/geminus/internal/storage/imports"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Import is import.schema.json, the import resource.
type Import struct {
	ID                      string         `json:"id"`
	OrganizationID          string         `json:"organization_id"`
	ProjectID               string         `json:"project_id"`
	ModelID                 string         `json:"model_id"`
	BaseVersionID           string         `json:"base_version_id"`
	Kind                    string         `json:"kind"`
	Filename                string         `json:"filename"`
	Status                  string         `json:"status"`
	MappingRevision         int64          `json:"mapping_revision"`
	TotalRows               int            `json:"total_rows"`
	InvalidRows             int            `json:"invalid_rows"`
	Profile                 jsontext.Value `json:"profile"`
	Preview                 jsontext.Value `json:"preview"`
	CanonicalPreview        jsontext.Value `json:"canonical_preview"`
	Mapping                 jsontext.Value `json:"mapping"`
	Validation              jsontext.Value `json:"validation"`
	DatasetVersionID        *string        `json:"dataset_version_id"`
	ProposalID              *string        `json:"proposal_id"`
	CreatedAt               string         `json:"created_at"`
	Error                   *ErrorEnvelope `json:"error"`
	MappingSuggestionStatus string         `json:"mapping_suggestion_status"`
	MappingSuggestion       jsontext.Value `json:"mapping_suggestion"`
}

// ErrorEnvelope is common.schema.json#/$defs/Error.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody is ErrorEnvelope.error.
type ErrorBody struct {
	Code      string        `json:"code"`
	Message   string        `json:"message"`
	Details   []ErrorDetail `json:"details"`
	RequestID string        `json:"request_id"`
}

// ErrorDetail is one ErrorBody.details entry.
type ErrorDetail struct {
	Path     string `json:"path"`
	Reason   string `json:"reason"`
	Code     string `json:"code,omitzero"`
	Severity string `json:"severity,omitzero"`
}

func raw(b []byte) jsontext.Value {
	if b == nil {
		return jsontext.Value("null")
	}
	return jsontext.Value(b)
}

// rawArray is b, or an empty array when nothing is stored.
func rawArray(b []byte) jsontext.Value {
	if b == nil {
		return jsontext.Value("[]")
	}
	return jsontext.Value(b)
}

func timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

var failureCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// envelope shows a stored failure as the contract's error, under the
// request behind the step that failed: the upload, or the save of the
// mapping revision whose validation failed (requestID only if that is
// unknown). Its job's failure (profile or validation) is shown the same
// way. The closed ErrorCode list has no import codes (CB-39, a question
// for the owner); until it has, the failure's own code is details[0].code
// and the error's code is:
//   - PAYLOAD_TOO_LARGE for a file beyond a limit (IMPORT_LIMIT_EXCEEDED);
//   - REQUEST_VALIDATION_FAILED for any other problem with the file
//     (encoding, CSV or JSON syntax, columns, shape, empty);
//   - SERVICE_UNAVAILABLE when the infrastructure failed until the
//     attempts were spent (LEASE_EXPIRED, WORKER_STOPPED,
//     OBJECT_STORAGE_FAILED, OBJECT_STORAGE_UNAVAILABLE): upload again;
//   - INTERNAL_ERROR otherwise (the stored upload does not match its
//     record or no longer reads as profiled, IMPORT_INPUT_INVALID: the
//     server cannot read what it stored, a cancelled job, an internal
//     failure).
func envelope(stored []byte, requestID string) (*ErrorEnvelope, error) {
	var f store.Failure
	if err := json.Unmarshal(stored, &f); err != nil || !failureCode.MatchString(f.Code) {
		return nil, fmt.Errorf("the stored failure is not a failure")
	}
	code, message := "INTERNAL_ERROR", "the file could not be processed"
	switch {
	case f.Code == "IMPORT_LIMIT_EXCEEDED":
		code, message = "PAYLOAD_TOO_LARGE", "the file is larger than an import may be"
	// IMPORT_INPUT_INVALID is the server not reading what it stored, not a
	// problem with the file (CB-39(a): INTERNAL_ERROR; T-017 review round 1, L3).
	case f.Code != store.InputInvalid && (strings.HasPrefix(f.Code, "IMPORT_") || strings.HasPrefix(f.Code, "CSV_") || strings.HasPrefix(f.Code, "JSON_")):
		code, message = "REQUEST_VALIDATION_FAILED", "the file cannot be read as uploaded"
	case f.Code == jobs.LeaseExpired || f.Code == "WORKER_STOPPED" || f.Code == store.ObjectStorageFailed || f.Code == store.ObjectStorageUnavailable:
		code, message = "SERVICE_UNAVAILABLE", "the file could not be processed; upload it again"
	}
	reason := f.Message
	if reason == "" {
		reason = message
	}
	is := model.NewIssue(f.Code, f.Path, reason, model.SeverityError) // bounds the path and reason (common.schema.json)
	return &ErrorEnvelope{Error: ErrorBody{Code: code, Message: message, RequestID: requestID,
		Details: []ErrorDetail{{Path: is.Path, Reason: is.Message, Code: f.Code, Severity: string(model.SeverityError)}}}}, nil
}

// public is the import resource of i, as stored: an import whose profile
// or validation job failed or was cancelled is FAILED with the job's error
// in its own row (0010, 0014: the import follows its job).
func public(i store.Import, requestID string) (Import, error) {
	p := Import{ID: i.ID, OrganizationID: i.OrganizationID, ProjectID: i.ProjectID, ModelID: i.ModelID, BaseVersionID: i.BaseVersionID,
		Kind: i.Kind, Filename: i.Filename, Status: string(i.Status), MappingRevision: i.MappingRevision, TotalRows: i.TotalRows,
		InvalidRows: i.InvalidRows, Profile: raw(i.Profile), Preview: raw(i.Preview), CanonicalPreview: rawArray(i.CanonicalPreview),
		Mapping: raw(i.Mapping), Validation: raw(i.Validation),
		DatasetVersionID: i.DatasetVersionID, ProposalID: i.ProposalID, CreatedAt: timestamp(i.CreatedAt),
		MappingSuggestionStatus: i.MappingSuggestionStatus, MappingSuggestion: raw(i.MappingSuggestion)}
	if i.RequestID != "" {
		requestID = i.RequestID
	}
	if i.Status == store.Failed {
		e, err := envelope(i.Failure, requestID)
		if err != nil {
			return Import{}, fmt.Errorf("import %s: %w", i.ID, err)
		}
		p.Error = e
	}
	return p, nil
}

// Get returns import id.
func (s *Service) Get(ctx context.Context, sc tenant.Scope, requestID, id string) (Import, error) {
	var out Import
	err := s.DB.Run(ctx, sc, tenant.ReadSnapshot, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		i, err := store.Get(ctx, tx, m.OrganizationID, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out, err = public(i, requestID)
		return err
	})
	return out, err
}

// ErrorReport returns the error report of import id, the bytes its row
// names, after checking the caller may read the import (the report is its
// organization's only, SECURITY §6). The object is read after the
// transaction and served only if its size and SHA-256 match.
func (s *Service) ErrorReport(ctx context.Context, sc tenant.Scope, id string) ([]byte, error) {
	var report *store.Report
	err := s.DB.Run(ctx, sc, tenant.ReadSnapshot, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		i, err := store.Get(ctx, tx, m.OrganizationID, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if i.Report == nil {
			return ErrNoReport
		}
		if !store.OwnKey(m.OrganizationID, id, i.Report.Key) {
			return ErrReportCorrupt
		}
		report = i.Report
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.Objects == nil {
		return nil, ErrStorageUnavailable
	}
	rc, err := s.Objects.Get(ctx, report.Key)
	if errors.Is(err, objects.ErrNotFound) {
		return nil, ErrReportCorrupt
	}
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, report.Size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != report.Size || hashing.ContentHash(data) != report.SHA256 {
		return nil, ErrReportCorrupt
	}
	return data, nil
}
