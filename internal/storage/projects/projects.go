// Package projects is the SQL of twin.projects. Every statement runs in a
// tenant.DB.Run transaction (RLS) and also filters by organization (the
// application filter AGENTS.md requires next to RLS).
package projects

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Project is one row.
type Project struct {
	ID             string
	OrganizationID string
	Name           string
	CreatedBy      string
	CreatedAt      time.Time
}

// ErrNotFound: no such project in the organization.
var ErrNotFound = errors.New("projects: not found")

// Insert stores p; the database sets CreatedAt.
func Insert(ctx context.Context, tx pgx.Tx, p Project) (Project, error) {
	err := tx.QueryRow(ctx, `INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,$3,$4) RETURNING created_at`,
		p.ID, p.OrganizationID, p.Name, p.CreatedBy).Scan(&p.CreatedAt)
	return p, err
}

// Get returns the project id of organization org.
func Get(ctx context.Context, tx pgx.Tx, org, id string) (Project, error) {
	p := Project{ID: id, OrganizationID: org}
	err := tx.QueryRow(ctx, `SELECT name, created_by, created_at FROM twin.projects WHERE organization_id=$1 AND id=$2`, org, id).
		Scan(&p.Name, &p.CreatedBy, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return p, err
}

// Position is a place in the (created_at, id) order.
type Position struct {
	CreatedAt time.Time
	ID        string
}

// List returns up to limit projects of org in (created_at, id) order,
// after the position if one is given.
func List(ctx context.Context, tx pgx.Tx, org string, after *Position, limit int) ([]Project, error) {
	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = tx.Query(ctx, `SELECT id, name, created_by, created_at FROM twin.projects WHERE organization_id=$1
			ORDER BY created_at, id LIMIT $2`, org, limit)
	} else {
		rows, err = tx.Query(ctx, `SELECT id, name, created_by, created_at FROM twin.projects WHERE organization_id=$1
			AND (created_at, id) > ($2, $3) ORDER BY created_at, id LIMIT $4`, org, after.CreatedAt, after.ID, limit)
	}
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Project, error) {
		p := Project{OrganizationID: org}
		err := r.Scan(&p.ID, &p.Name, &p.CreatedBy, &p.CreatedAt)
		return p, err
	})
	return out, err
}
