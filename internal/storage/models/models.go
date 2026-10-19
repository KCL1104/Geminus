// Package models is the SQL of twin.models, twin.model_versions,
// twin.model_drafts and twin.model_version_datasets (T-012, ADR-022). Every
// statement runs in a tenant.DB.Run transaction (RLS) and also filters by
// organization (the application filter AGENTS.md requires next to RLS).
// Versions are immutable: there is no UPDATE for them here, and the
// database refuses one (TEST-004).
package models

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/model"
)

// Model is one row of twin.models.
type Model struct {
	ID, OrganizationID, ProjectID string
	Name, Domain                  string
	LatestVersionID               string
	CreatedBy                     string
	CreatedAt, UpdatedAt          time.Time
}

// Version is one row of twin.model_versions with its dataset manifest.
type Version struct {
	ID, OrganizationID, ModelID string
	Version                     int64
	SchemaVersion               string
	ParentVersionID             *string
	SourceProposalID            *string
	IR                          []byte // the canonical text its hash covers (D-040)
	Hash                        string
	DatasetBindings             []model.DatasetBinding // byte order of logical_ref (D-034)
	CreatedBy                   string
	CreatedAt                   time.Time
}

// Draft is the one row of twin.model_drafts of a model.
type Draft struct {
	OrganizationID, ModelID, BaseVersionID string
	Revision                               int64
	IR                                     []byte
	Hash                                   string
	Issues                                 []model.ValidationIssue
	UpdatedBy                              string
	UpdatedAt                              time.Time
}

var (
	// ErrNotFound: no such row in the organization.
	ErrNotFound = errors.New("models: not found")
	// ErrStale: the draft is not at the expected revision any more.
	ErrStale = errors.New("models: stale draft revision")
)

// InsertModel stores m; latest_version_id must name the version inserted
// in the same transaction (the foreign key is checked at commit).
func InsertModel(ctx context.Context, tx pgx.Tx, m Model) (Model, error) {
	err := tx.QueryRow(ctx, `INSERT INTO twin.models(id,organization_id,project_id,name,domain,latest_version_id,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at, updated_at`,
		m.ID, m.OrganizationID, m.ProjectID, m.Name, m.Domain, m.LatestVersionID, m.CreatedBy).Scan(&m.CreatedAt, &m.UpdatedAt)
	return m, err
}

const modelColumns = `id, project_id, name, domain, latest_version_id, created_by, created_at, updated_at`

func scanModel(row pgx.Row, org string) (Model, error) {
	m := Model{OrganizationID: org}
	var latest *string
	err := row.Scan(&m.ID, &m.ProjectID, &m.Name, &m.Domain, &latest, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Model{}, ErrNotFound
	}
	if latest != nil {
		m.LatestVersionID = *latest
	}
	return m, err
}

// GetModel returns the model id of organization org.
func GetModel(ctx context.Context, tx pgx.Tx, org, id string) (Model, error) {
	return scanModel(tx.QueryRow(ctx, `SELECT `+modelColumns+` FROM twin.models WHERE organization_id=$1 AND id=$2`, org, id), org)
}

// LockModel returns the model and holds its row until the transaction
// ends. Every write of a model, its draft or its versions takes this lock
// first, so they run one after another (ADR-022).
func LockModel(ctx context.Context, tx pgx.Tx, org, id string) (Model, error) {
	return scanModel(tx.QueryRow(ctx, `SELECT `+modelColumns+` FROM twin.models WHERE organization_id=$1 AND id=$2 FOR UPDATE`, org, id), org)
}

// SetLatest makes version the model's latest and gives the model its name.
func SetLatest(ctx context.Context, tx pgx.Tx, org, id, versionID, name string) error {
	tag, err := tx.Exec(ctx, `UPDATE twin.models SET latest_version_id=$3, name=$4, updated_at=now() WHERE organization_id=$1 AND id=$2`,
		org, id, versionID, name)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return err
}

// Position is a place in the (created_at, id) order of a project's models.
type Position struct {
	CreatedAt time.Time
	ID        string
}

// ListModels returns up to limit models of project in (created_at, id)
// order, after the position if one is given.
func ListModels(ctx context.Context, tx pgx.Tx, org, project string, after *Position, limit int) ([]Model, error) {
	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = tx.Query(ctx, `SELECT `+modelColumns+` FROM twin.models WHERE organization_id=$1 AND project_id=$2
			ORDER BY created_at, id LIMIT $3`, org, project, limit)
	} else {
		rows, err = tx.Query(ctx, `SELECT `+modelColumns+` FROM twin.models WHERE organization_id=$1 AND project_id=$2
			AND (created_at, id) > ($3, $4) ORDER BY created_at, id LIMIT $5`, org, project, after.CreatedAt, after.ID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Model
	for rows.Next() {
		m, err := scanModel(rows, org)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// InsertVersion stores v and its dataset bindings, in the order the
// database compares them at commit (byte order of logical_ref).
func InsertVersion(ctx context.Context, tx pgx.Tx, v Version) (Version, error) {
	bindings := v.DatasetBindings
	if bindings == nil {
		bindings = []model.DatasetBinding{}
	}
	manifest, err := json.Marshal(bindings)
	if err != nil {
		return Version{}, fmt.Errorf("encoding the manifest: %w", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,parent_version_id,source_proposal_id,ir,hash,dataset_bindings,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING created_at`,
		v.ID, v.OrganizationID, v.ModelID, v.Version, v.SchemaVersion, v.ParentVersionID, v.SourceProposalID, v.IR, v.Hash, manifest, v.CreatedBy).Scan(&v.CreatedAt); err != nil {
		return Version{}, err
	}
	for _, b := range bindings {
		if _, err := tx.Exec(ctx, `INSERT INTO twin.model_version_datasets(organization_id,model_id,model_version_id,logical_ref,dataset_version_id,content_hash,unit,sample_count)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, v.OrganizationID, v.ModelID, v.ID, b.LogicalRef, b.DatasetVersionID, b.ContentHash, string(b.Unit), b.SampleCount); err != nil {
			return Version{}, err
		}
	}
	v.DatasetBindings = bindings
	return v, nil
}

const versionColumns = `id, model_id, version, schema_version, parent_version_id, source_proposal_id, ir, hash, dataset_bindings, created_by, created_at`

func scanVersion(row pgx.Row, org string) (Version, error) {
	v := Version{OrganizationID: org}
	var manifest []byte
	err := row.Scan(&v.ID, &v.ModelID, &v.Version, &v.SchemaVersion, &v.ParentVersionID, &v.SourceProposalID, &v.IR, &v.Hash, &manifest, &v.CreatedBy, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	if err != nil {
		return Version{}, err
	}
	if err := json.Unmarshal(manifest, &v.DatasetBindings); err != nil {
		return Version{}, fmt.Errorf("version %s: stored manifest: %w", v.ID, err)
	}
	return v, nil
}

// GetVersion returns the version id of organization org.
func GetVersion(ctx context.Context, tx pgx.Tx, org, id string) (Version, error) {
	return scanVersion(tx.QueryRow(ctx, `SELECT `+versionColumns+` FROM twin.model_versions WHERE organization_id=$1 AND id=$2`, org, id), org)
}

// ListVersions returns up to limit versions of the model, newest first,
// before version number before if it is set.
func ListVersions(ctx context.Context, tx pgx.Tx, org, modelID string, before *int64, limit int) ([]Version, error) {
	var rows pgx.Rows
	var err error
	if before == nil {
		rows, err = tx.Query(ctx, `SELECT `+versionColumns+` FROM twin.model_versions WHERE organization_id=$1 AND model_id=$2
			ORDER BY version DESC LIMIT $3`, org, modelID, limit)
	} else {
		rows, err = tx.Query(ctx, `SELECT `+versionColumns+` FROM twin.model_versions WHERE organization_id=$1 AND model_id=$2
			AND version < $3 ORDER BY version DESC LIMIT $4`, org, modelID, *before, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		v, err := scanVersion(rows, org)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// InsertDraft stores the model's draft.
func InsertDraft(ctx context.Context, tx pgx.Tx, d Draft) (Draft, error) {
	issues, err := encodeIssues(d.Issues)
	if err != nil {
		return Draft{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO twin.model_drafts(organization_id,model_id,base_version_id,revision,ir,hash,issues,updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING updated_at`,
		d.OrganizationID, d.ModelID, d.BaseVersionID, d.Revision, d.IR, d.Hash, issues, d.UpdatedBy).Scan(&d.UpdatedAt)
	return d, err
}

// GetDraft returns the draft of the model.
func GetDraft(ctx context.Context, tx pgx.Tx, org, modelID string) (Draft, error) {
	d := Draft{OrganizationID: org, ModelID: modelID}
	var issues []byte
	err := tx.QueryRow(ctx, `SELECT base_version_id, revision, ir, hash, issues, updated_by, updated_at FROM twin.model_drafts
		WHERE organization_id=$1 AND model_id=$2`, org, modelID).Scan(&d.BaseVersionID, &d.Revision, &d.IR, &d.Hash, &issues, &d.UpdatedBy, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, err
	}
	if err := json.Unmarshal(issues, &d.Issues); err != nil {
		return Draft{}, fmt.Errorf("draft of %s: stored issues: %w", modelID, err)
	}
	return d, nil
}

// UpdateDraft replaces the draft if it is still at revision expected; d's
// Revision is the new one. Anything else is ErrStale: the caller re-reads
// rather than overwrite (TEST-005).
func UpdateDraft(ctx context.Context, tx pgx.Tx, d Draft, expected int64) (Draft, error) {
	issues, err := encodeIssues(d.Issues)
	if err != nil {
		return Draft{}, err
	}
	err = tx.QueryRow(ctx, `UPDATE twin.model_drafts SET base_version_id=$3, revision=$4, ir=$5, hash=$6, issues=$7, updated_by=$8, updated_at=now()
		WHERE organization_id=$1 AND model_id=$2 AND revision=$9 RETURNING updated_at`,
		d.OrganizationID, d.ModelID, d.BaseVersionID, d.Revision, d.IR, d.Hash, issues, d.UpdatedBy, expected).Scan(&d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrStale
	}
	return d, err
}

func encodeIssues(issues []model.ValidationIssue) ([]byte, error) {
	if issues == nil {
		issues = []model.ValidationIssue{}
	}
	data, err := json.Marshal(issues)
	if err != nil {
		return nil, fmt.Errorf("encoding draft issues: %w", err)
	}
	return data, nil
}
