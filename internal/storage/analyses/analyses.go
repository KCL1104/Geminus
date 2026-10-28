// Package analyses is the SQL of twin.ai_analyses (T-030, API_DATABASE §4,
// migrations 0001, 0002 and 0018). Every statement runs in a scoped
// transaction (tenant.DB.Run for the API, the worker's lease-fenced
// Checkpoint and Finish) and also filters by organization.
//
// The API inserts an analysis generating, with its request (the analyzed
// run, the baseline run, the question, the output locale), none of which
// changes later, and its one analysis job. The worker writes the analysis
// once, moving it to completed in the transaction that completes the job
// (Complete); when the job fails or is cancelled first, the analysis
// follows it to failed with the job's error (0018). A completed or failed
// analysis never changes.
package analyses

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Status is an analysis's state.
type Status string

const (
	Generating Status = "generating"
	Completed  Status = "completed"
	Failed     Status = "failed"
)

// Analysis is one row of twin.ai_analyses, with the request that created it.
type Analysis struct {
	ID, OrganizationID, RunID string
	BaselineRunID             *string
	Status                    Status
	Question                  *string
	OutputLocale              string
	// Analysis is the stored analysis-output JSON; nil unless completed.
	Analysis []byte
	// ProviderMetadata is the call's metadata (JSON object).
	ProviderMetadata []byte
	// Failure is the job's error (JSON); nil unless failed.
	Failure   []byte
	CreatedBy string
	CreatedAt time.Time
	// RequestID is the request that created it (its job's payload).
	RequestID string
}

// ErrNotFound: no such analysis in the organization (or no access).
var ErrNotFound = errors.New("analyses: not found")

// ErrNotGenerating: the analysis is no longer generating; nothing was
// written.
var ErrNotGenerating = errors.New("analyses: the analysis is not generating")

// ErrUnstorable: the database refused the analysis's content (an encoding
// PostgreSQL text or jsonb cannot hold, or a check).
var ErrUnstorable = errors.New("analyses: the database cannot store the analysis")

// Insert stores a generating, with its request.
func Insert(ctx context.Context, tx pgx.Tx, a Analysis) (Analysis, error) {
	err := tx.QueryRow(ctx, `INSERT INTO twin.ai_analyses(id,organization_id,run_id,baseline_run_id,status,question,output_locale,created_by)
		VALUES ($1,$2,$3,$4,'generating',$5,$6,$7) RETURNING created_at`,
		a.ID, a.OrganizationID, a.RunID, a.BaselineRunID, a.Question, a.OutputLocale, a.CreatedBy).Scan(&a.CreatedAt)
	if err != nil {
		return Analysis{}, err
	}
	a.Status, a.ProviderMetadata = Generating, []byte("{}")
	return a, nil
}

// The job's payload holds the request that created the analysis (one job
// at most, 0001).
const selectAnalysis = `SELECT a.id, a.run_id, a.baseline_run_id, a.status, a.question, a.output_locale, a.analysis, a.provider_metadata,
	  a.error, a.created_by, a.created_at, coalesce(j.payload->>'request_id', '')
	FROM twin.ai_analyses a LEFT JOIN twin.jobs j ON j.organization_id=a.organization_id AND j.ai_analysis_id=a.id
	WHERE a.organization_id=$1 AND a.id=$2`

func scan(row pgx.Row, org string) (Analysis, error) {
	a := Analysis{OrganizationID: org}
	var status string
	err := row.Scan(&a.ID, &a.RunID, &a.BaselineRunID, &status, &a.Question, &a.OutputLocale, &a.Analysis, &a.ProviderMetadata,
		&a.Failure, &a.CreatedBy, &a.CreatedAt, &a.RequestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Analysis{}, ErrNotFound
	}
	if err != nil {
		return Analysis{}, err
	}
	a.Status = Status(status)
	return a, nil
}

// Get returns the analysis id of organization org.
func Get(ctx context.Context, tx pgx.Tx, org, id string) (Analysis, error) {
	return scan(tx.QueryRow(ctx, selectAnalysis, org, id), org)
}

// Lock returns the analysis and holds its row until the transaction ends.
func Lock(ctx context.Context, tx pgx.Tx, org, id string) (Analysis, error) {
	return scan(tx.QueryRow(ctx, selectAnalysis+` FOR UPDATE OF a`, org, id), org)
}

// Complete writes the analysis and the call's metadata to the generating
// analysis id (the worker, in the transaction that completes its job; 0018
// refuses any other). Content the database refuses is ErrUnstorable.
func Complete(ctx context.Context, tx pgx.Tx, org, id string, analysis, metadata []byte) error {
	if metadata == nil {
		metadata = []byte("{}")
	}
	tag, err := tx.Exec(ctx, `UPDATE twin.ai_analyses SET status='completed', analysis=$3, provider_metadata=$4
		WHERE organization_id=$1 AND id=$2 AND status='generating'`, org, id, string(analysis), string(metadata))
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && (pg.Code == "22021" || pg.Code == "22P05" || pg.Code == "23514") {
			return fmt.Errorf("%w (SQLSTATE %s, constraint %q)", ErrUnstorable, pg.Code, pg.ConstraintName)
		}
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotGenerating
	}
	return nil
}
