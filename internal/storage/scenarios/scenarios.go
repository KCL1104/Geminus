// Package scenarios is the SQL of twin.scenarios (API_DATABASE §8): an
// immutable what-if description, a base version and an ordered JSON Patch
// (D-004, D-021). T-026 stores the scenario an approved scenario proposal
// creates; T-028 stores the scenarios people save and reads and lists
// both kinds. Every statement runs in a tenant.DB.Run transaction (RLS)
// and also filters by organization. Scenarios are immutable: there is no
// UPDATE or DELETE here, and the database refuses both (0001); one names
// its source proposal at most once (0001), and only as that approved
// proposal's own effect (0013); the API creates one only for the scoped
// user, at the time of its transaction (0015).
package scenarios

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Scenario is one row of twin.scenarios.
type Scenario struct {
	ID, OrganizationID, ModelID, BaseVersionID string
	Name                                       string
	// Patch is the JSON Patch array as stored (RFC 8785 text when the
	// server wrote it; jsonb when read back); PatchHash is D-019's hash.
	Patch            []byte
	PatchHash        string
	SourceProposalID *string
	CreatedBy        string
	CreatedAt        time.Time
}

// ErrNotFound: no such scenario in the organization.
var ErrNotFound = errors.New("scenarios: not found")

const columns = `id, model_id, base_version_id, name, patch, patch_hash, source_proposal_id, created_by, created_at`

// Insert stores s.
func Insert(ctx context.Context, tx pgx.Tx, s Scenario) (Scenario, error) {
	err := tx.QueryRow(ctx, `INSERT INTO twin.scenarios(id,organization_id,model_id,base_version_id,name,patch,patch_hash,source_proposal_id,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at`,
		s.ID, s.OrganizationID, s.ModelID, s.BaseVersionID, s.Name, string(s.Patch), s.PatchHash, s.SourceProposalID, s.CreatedBy).Scan(&s.CreatedAt)
	return s, err
}

func scan(row pgx.Row, org string) (Scenario, error) {
	s := Scenario{OrganizationID: org}
	err := row.Scan(&s.ID, &s.ModelID, &s.BaseVersionID, &s.Name, &s.Patch, &s.PatchHash, &s.SourceProposalID, &s.CreatedBy, &s.CreatedAt)
	return s, err
}

// Get returns the scenario id of organization org.
func Get(ctx context.Context, tx pgx.Tx, org, id string) (Scenario, error) {
	s, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM twin.scenarios WHERE organization_id=$1 AND id=$2`, org, id), org)
	if errors.Is(err, pgx.ErrNoRows) {
		return Scenario{}, ErrNotFound
	}
	return s, err
}

// Position is a scenario's place in a list: its creation time, then its ID.
type Position struct {
	CreatedAt time.Time
	ID        string
}

// List returns up to limit scenarios of organization org whose base is
// version versionID, in (created_at, id) order, after position after if
// it is set: the people's and those approved proposals created alike.
func List(ctx context.Context, tx pgx.Tx, org, versionID string, after *Position, limit int) ([]Scenario, error) {
	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = tx.Query(ctx, `SELECT `+columns+` FROM twin.scenarios WHERE organization_id=$1 AND base_version_id=$2
			ORDER BY created_at, id LIMIT $3`, org, versionID, limit)
	} else {
		rows, err = tx.Query(ctx, `SELECT `+columns+` FROM twin.scenarios WHERE organization_id=$1 AND base_version_id=$2
			AND (created_at, id) > ($3, $4) ORDER BY created_at, id LIMIT $5`, org, versionID, after.CreatedAt, after.ID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Scenario
	for rows.Next() {
		s, err := scan(rows, org)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
