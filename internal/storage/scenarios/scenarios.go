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
	"fmt"
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
	// AI is the label of a scenario an AI investigation saved (H-061,
	// 0019): set by the database from the transaction's acting-for scope,
	// read back with the investigation's requester and planner; nil for a
	// person's scenario (and on Insert's result).
	AI *Label
}

// Label is what a labelled row shows of the AI investigation that made it:
// the investigation, the person it was made for and its planner model.
type Label struct {
	InvestigationID, RequestedBy, PlannerModel string
}

// ErrNotFound: no such scenario in the organization.
var ErrNotFound = errors.New("scenarios: not found")

const columns = `s.id, s.model_id, s.base_version_id, s.name, s.patch, s.patch_hash, s.source_proposal_id, s.created_by, s.created_at,
	s.investigation_id, i.created_by, i.planner_model`

// from joins each scenario with the investigation that saved it, if one did
// (its label, 0019), in the same organization.
const from = ` FROM twin.scenarios s
	LEFT JOIN twin.investigations i ON i.organization_id=s.organization_id AND i.id=s.investigation_id`

// Insert stores s.
func Insert(ctx context.Context, tx pgx.Tx, s Scenario) (Scenario, error) {
	err := tx.QueryRow(ctx, `INSERT INTO twin.scenarios(id,organization_id,model_id,base_version_id,name,patch,patch_hash,source_proposal_id,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at`,
		s.ID, s.OrganizationID, s.ModelID, s.BaseVersionID, s.Name, string(s.Patch), s.PatchHash, s.SourceProposalID, s.CreatedBy).Scan(&s.CreatedAt)
	return s, err
}

func scan(row pgx.Row, org string) (Scenario, error) {
	s := Scenario{OrganizationID: org}
	var inv, requester, planner *string
	if err := row.Scan(&s.ID, &s.ModelID, &s.BaseVersionID, &s.Name, &s.Patch, &s.PatchHash, &s.SourceProposalID, &s.CreatedBy, &s.CreatedAt,
		&inv, &requester, &planner); err != nil {
		return Scenario{}, err
	}
	var err error
	s.AI, err = label(s.ID, inv, requester, planner)
	return s, err
}

// label is the AI label of scenario id, or nil for a person's; a label
// whose investigation cannot be read is an error, never left out.
func label(id string, inv, requester, planner *string) (*Label, error) {
	if inv == nil {
		return nil, nil
	}
	if requester == nil || planner == nil {
		return nil, fmt.Errorf("scenarios: scenario %s: its investigation %s is not visible", id, *inv)
	}
	return &Label{InvestigationID: *inv, RequestedBy: *requester, PlannerModel: *planner}, nil
}

// Get returns the scenario id of organization org.
func Get(ctx context.Context, tx pgx.Tx, org, id string) (Scenario, error) {
	s, err := scan(tx.QueryRow(ctx, `SELECT `+columns+from+` WHERE s.organization_id=$1 AND s.id=$2`, org, id), org)
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
		rows, err = tx.Query(ctx, `SELECT `+columns+from+` WHERE s.organization_id=$1 AND s.base_version_id=$2
			ORDER BY s.created_at, s.id LIMIT $3`, org, versionID, limit)
	} else {
		rows, err = tx.Query(ctx, `SELECT `+columns+from+` WHERE s.organization_id=$1 AND s.base_version_id=$2
			AND (s.created_at, s.id) > ($3, $4) ORDER BY s.created_at, s.id LIMIT $5`, org, versionID, after.CreatedAt, after.ID, limit)
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
