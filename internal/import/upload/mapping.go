package upload

// T-017 (saveImportMapping; APPROVED D-009, D-011, D-043 (c); D-070, D-071
// PROPOSED; TEST-012, TEST-013): a person confirms how a profiled import's
// columns map to canonical event fields. In one transaction, with its audit
// event and idempotency response: the mapping becomes the next
// mapping_revision (a compare-and-set on the revision the person saw; a
// stale one is 409, never an overwrite or a merge), the import VALIDATING
// with the earlier validation cleared, that revision's import_validate job
// queued and the queued jobs of older revisions cancelled (a running one
// completes without writing: migration 0014's stale-job fence). The Twin
// never changes (D-009).

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/import/mapping"
	importprofile "github.com/kcl1104/geminus/internal/import/profile"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	store "github.com/kcl1104/geminus/internal/storage/imports"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/tenant"
)

// SaveRequest is http.schema.json#/$defs/SaveMapping.
type SaveRequest struct {
	ExpectedMappingRevision int64           `json:"expected_mapping_revision"`
	Mapping                 mapping.Mapping `json:"mapping"`
	Confirmed               bool            `json:"confirmed"`
	ConfirmedLowConfidence  []string        `json:"confirmed_low_confidence,omitzero"`
}

// The codes of a save the import's state refuses (409 LIFECYCLE_CONFLICT,
// details[0].code) and of a mapping no import of its kind takes (422).
const (
	CodeRevisionConflict = "MAPPING_REVISION_CONFLICT"
	CodeNotProfiled      = "IMPORT_NOT_PROFILED"
	CodeFinal            = "IMPORT_FINAL"
	CodeNotApplicable    = "MAPPING_NOT_APPLICABLE"
)

// ConflictError is a save the import's state refuses (409
// LIFECYCLE_CONFLICT): Code and Path are its detail's.
type ConflictError struct{ Code, Path, Reason, Message string }

func (e *ConflictError) Error() string { return "upload: " + e.Code }

// MappingError is a mapping the import cannot take (422
// REQUEST_VALIDATION_FAILED), with an issue per JSON Pointer into the body.
type MappingError struct{ Issues []model.ValidationIssue }

func (e *MappingError) Error() string {
	return fmt.Sprintf("upload: the mapping is not acceptable (%d issue(s))", len(e.Issues))
}

const mapOperation = "saveImportMapping"

// mapFingerprint is the request an Idempotency-Key stands for: the import
// and the body (API_DATABASE §7).
func mapFingerprint(id string, body []byte) (string, error) {
	canonical, err := hashing.Canonical(body)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(map[string]any{"import_id": id, "body": jsontext.Value(canonical)}, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	return hashing.Of(data)
}

// suggestion is the import's AI suggestion when it is ready (T-027), for
// D-070's confirmation rule; nil otherwise.
func suggestion(i store.Import) (*mapping.Suggestion, error) {
	if i.MappingSuggestionStatus != "ready" || i.MappingSuggestion == nil {
		return nil, nil
	}
	var s mapping.Suggestion
	if err := json.Unmarshal(i.MappingSuggestion, &s); err != nil {
		return nil, fmt.Errorf("import %s: the stored mapping suggestion: %w", i.ID, err)
	}
	return &s, nil
}

// columnsOf are the names of the import's profile columns.
func columnsOf(i store.Import) ([]string, error) {
	var p importprofile.Profile
	if err := json.Unmarshal(i.Profile, &p); err != nil {
		return nil, fmt.Errorf("import %s: the stored profile: %w", i.ID, err)
	}
	names := make([]string, len(p.Columns))
	for k, c := range p.Columns {
		names[k] = c.Name
	}
	return names, nil
}

// SaveMapping saves req (whose raw body is body) as import id's next
// mapping revision, or replays the response of the earlier request with
// the same Idempotency-Key. It returns the HTTP status (202) and body: the
// import, VALIDATING.
func (s *Service) SaveMapping(ctx context.Context, sc tenant.Scope, requestID, key, id string, body []byte, req SaveRequest) (int, []byte, error) {
	hash, err := mapFingerprint(id, body)
	if err != nil {
		return 0, nil, err
	}
	k := idempotency.Key{Org: sc.OrganizationID, Actor: sc.UserID, Operation: mapOperation, Key: key}
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
		org := m.OrganizationID
		// The validation jobs before the import (ADR-024's order).
		if err := store.LockValidations(ctx, tx, org, id); err != nil {
			return err
		}
		imp, err := store.Lock(ctx, tx, org, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		switch imp.Status {
		case store.Uploaded, store.Profiling:
			return &ConflictError{Code: CodeNotProfiled, Path: "", Reason: "the import's file is not profiled yet",
				Message: "the import cannot be mapped until its file is profiled"}
		case store.Failed, store.Committed:
			return &ConflictError{Code: CodeFinal, Path: "", Reason: "the import is " + string(imp.Status),
				Message: "the import is final and cannot be mapped"}
		}
		if imp.MappingRevision != req.ExpectedMappingRevision {
			return &ConflictError{Code: CodeRevisionConflict, Path: "/expected_mapping_revision", Reason: "the import's mapping is at another revision",
				Message: "the import's mapping changed since it was read; reload it and map again"}
		}
		if imp.Kind == "json_model" {
			return &MappingError{Issues: []model.ValidationIssue{model.NewIssue(CodeNotApplicable, "/mapping",
				"a JSON model import has no columns to map", model.SeverityError)}}
		}
		columns, err := columnsOf(imp)
		if err != nil {
			return err
		}
		sug, err := suggestion(imp)
		if err != nil {
			return err
		}
		if issues := mapping.Check(req.Mapping, columns, sug, req.ConfirmedLowConfidence); len(issues) > 0 {
			return &MappingError{Issues: issues}
		}
		doc, err := json.Marshal(req.Mapping)
		if err != nil {
			return err
		}
		rev, err := store.SaveMapping(ctx, tx, org, id, req.ExpectedMappingRevision, doc)
		if err != nil {
			return err // ErrRevision cannot happen: the row is locked and was checked
		}
		if _, err := jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: org, Type: jobs.ImportValidate,
			Resource: jobs.Resource{ImportID: id}, Payload: map[string]any{"import_id": id, "mapping_revision": rev, "request_id": requestID}},
			s.policy()); err != nil {
			return err
		}
		cancelled, err := store.CancelStaleValidations(ctx, tx, org, id, rev)
		if err != nil {
			return err
		}
		imported, metadata := 0, 0
		for _, e := range req.Mapping.Mappings {
			if e.TargetField != nil {
				imported++
				if *e.TargetField == mapping.Metadata {
					metadata++
				}
			}
		}
		// Counts only: no column name or value (SECURITY §9).
		if err := auditlog.AppendUser(ctx, tx, org, m.UserID, auditlog.Event{Action: "import.map", TargetType: "import", TargetID: id,
			RequestID: requestID, Metadata: map[string]any{"mapping_revision": rev, "columns_imported": imported,
				"columns_left_out": len(req.Mapping.Mappings) - imported, "metadata_columns": metadata,
				"confirmed_low_confidence": len(req.ConfirmedLowConfidence), "validations_cancelled": cancelled}}); err != nil {
			return err
		}
		saved, err := store.Get(ctx, tx, org, id)
		if err != nil {
			return err
		}
		res, err := public(saved, requestID)
		if err != nil {
			return err
		}
		data, err := json.Marshal(res)
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
