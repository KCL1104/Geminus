package imports

// T-017 (migration 0014): a person's mapping, saved as the next revision,
// and the validation of each revision by its own import_validate job.
// 0014 holds every role to these steps; the statements also name the state
// and revision they expect, so a write that finds another changes nothing.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ErrNotValidating: the import is not VALIDATING at the revision the
// write names (a newer mapping was saved, or the import ended); nothing
// was written.
var ErrNotValidating = errors.New("imports: the import is not being validated at this mapping revision")

// ErrRevision: the import is not at the mapping revision the save expects,
// or cannot be mapped; nothing was written.
var ErrRevision = errors.New("imports: the import is not at the expected mapping revision")

// The validation job's own failure codes (internal/import/validator).
const (
	// UploadChanged: the stored upload no longer reads as the file that
	// was profiled (its rows or columns differ although its SHA-256 and
	// size match, which only a defect can cause); the import fails.
	UploadChanged = "UPLOAD_CHANGED"
)

// LockValidations locks the queued and running validation jobs of import
// id, in ID order, for the rest of tx: a save takes them before the import
// (ADR-024's order, the job then its resource, as the worker's Checkpoint
// and Finish do), so that it never waits for a worker that waits for it.
func LockValidations(ctx context.Context, tx pgx.Tx, org, id string) error {
	rows, err := tx.Query(ctx, `SELECT id FROM twin.jobs WHERE organization_id=$1 AND import_id=$2 AND type='import_validate'
		AND status IN ('queued','running') ORDER BY id FOR UPDATE`, org, id)
	if err != nil {
		return err
	}
	rows.Close()
	return rows.Err()
}

// SaveMapping saves mapping (JSON, import-mapping.schema.json) as the next
// mapping revision of import id, a profiled import at revision expected:
// it is VALIDATING, with the earlier validation, invalid rows, canonical
// preview and dataset, error report and error cleared. It returns the new
// revision; ErrRevision when the import is not at expected or cannot be
// mapped.
func SaveMapping(ctx context.Context, tx pgx.Tx, org, id string, expected int64, mapping []byte) (int64, error) {
	var rev int64
	err := tx.QueryRow(ctx, `UPDATE twin.imports SET mapping=$4, mapping_revision=mapping_revision+1, status='VALIDATING', validation=NULL,
		  invalid_rows=0, canonical_preview='[]', canonical_object_key=NULL, canonical_content_hash=NULL, canonical_size_bytes=NULL,
		  error_report_object_key=NULL, error_report_content_hash=NULL, error_report_size_bytes=NULL
		WHERE organization_id=$1 AND id=$2 AND mapping_revision=$3 AND status IN ('AWAITING_MAPPING','VALIDATING','VALIDATED')
		RETURNING mapping_revision`, org, id, expected, string(mapping)).Scan(&rev)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrRevision
	}
	return rev, err
}

// CancelStaleValidations cancels the queued validation jobs of import id
// of any revision but rev (a running one completes without writing, the
// stale-job fence), and returns how many it cancelled.
func CancelStaleValidations(ctx context.Context, tx pgx.Tx, org, id string, rev int64) (int, error) {
	tag, err := tx.Exec(ctx, `UPDATE twin.jobs SET status='cancelled'
		WHERE organization_id=$1 AND import_id=$2 AND type='import_validate' AND status='queued'
		  AND (payload->>'mapping_revision') IS DISTINCT FROM $3::bigint::text`, org, id, rev)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// Valid records that every row of import id maps under revision rev:
// VALIDATED with the validation (JSON), the canonical preview (JSON) and
// the canonical dataset.
func Valid(ctx context.Context, tx pgx.Tx, org, id string, rev int64, validation, preview []byte, dataset Report) error {
	tag, err := tx.Exec(ctx, `UPDATE twin.imports SET status='VALIDATED', validation=$4, invalid_rows=0, canonical_preview=$5,
		  canonical_object_key=$6, canonical_content_hash=$7, canonical_size_bytes=$8
		WHERE organization_id=$1 AND id=$2 AND status='VALIDATING' AND mapping_revision=$3`,
		org, id, rev, string(validation), string(preview), dataset.Key, dataset.SHA256, dataset.Size)
	return written(tag.RowsAffected(), err)
}

// NotValid records that rows of import id do not map under revision rev:
// AWAITING_MAPPING (to be mapped again) with the validation (JSON), the
// invalid rows, the canonical preview (JSON) and the error report.
func NotValid(ctx context.Context, tx pgx.Tx, org, id string, rev int64, validation, preview []byte, invalid int, report Report) error {
	tag, err := tx.Exec(ctx, `UPDATE twin.imports SET status='AWAITING_MAPPING', validation=$4, invalid_rows=$5, canonical_preview=$6,
		  error_report_object_key=$7, error_report_content_hash=$8, error_report_size_bytes=$9
		WHERE organization_id=$1 AND id=$2 AND status='VALIDATING' AND mapping_revision=$3`,
		org, id, rev, string(validation), invalid, string(preview), report.Key, report.SHA256, report.Size)
	return written(tag.RowsAffected(), err)
}

// FailValidating records why import id, VALIDATING at revision rev,
// cannot be imported (failure, JSON).
func FailValidating(ctx context.Context, tx pgx.Tx, org, id string, rev int64, failure []byte) error {
	tag, err := tx.Exec(ctx, `UPDATE twin.imports SET status='FAILED', error=$4
		WHERE organization_id=$1 AND id=$2 AND status='VALIDATING' AND mapping_revision=$3`, org, id, rev, string(failure))
	return written(tag.RowsAffected(), err)
}

func written(n int64, err error) error {
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotValidating
	}
	return nil
}
