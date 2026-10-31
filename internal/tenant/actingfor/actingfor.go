// Package actingfor is the API's one organization-only scope (H-061 §6.1;
// D-008 as amended (c); PROPOSED ADR-019 R3b addendum): the investigation
// executor's discovery of the AI calls it is to carry out, in the
// organizations the deployment assigns (INVESTIGATION_ORGANIZATION_IDS, the
// worker's allowlist), one organization per transaction, never a
// cross-tenant scan, and with no user.
//
// It has exactly two statements, both fixed SQL and both organization-only
// (TestExactlyTwoFixedStatements pins them):
//   - Pending, read-only: the pending calls of running investigations, with
//     their requesters, and the running investigations whose requester holds
//     no owner or editor membership;
//   - EndLost: stops such an investigation with role_lost, which the
//     database (protect_investigation) allows only when it finds no owner
//     or editor membership for the requester.
//
// Every call itself is carried out in the requester's own scope
// (tenant.Scope with ActingFor), whose first lock is the requester's
// membership read FOR SHARE. Only internal/investigations/executor and
// cmd/api may link this package (architecture rule
// acting-for-only-in-the-executor). Like tenant.DB, it relies on the pool's
// tenant.Guard and a login tenant.CheckRuntimeRole accepted as the API role.
package actingfor

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/internal/scope"
)

// ErrNotAssigned: the deployment does not assign the organization to the
// executor (INVESTIGATION_ORGANIZATION_IDS).
var ErrNotAssigned = errors.New("actingfor: organization not assigned to the executor")

// MaxPending is the most rows one discovery returns: at most one pending
// call per running investigation, one running investigation per person.
const MaxPending = 200

// Scope is the executor's discovery scope.
type Scope struct {
	db       *postgres.DB
	assigned []string
}

// New returns the scope on pool for the assigned organizations, in
// deployment order. An ID that is not an organization ID, or one listed
// twice, is tenant.ErrInvalidScope.
func New(pool *pgxpool.Pool, assigned []string) (*Scope, error) {
	for i, o := range assigned {
		if !scope.OrgID.MatchString(o) || slices.Contains(assigned[:i], o) {
			return nil, fmt.Errorf("%w: assigned organization %q", tenant.ErrInvalidScope, o)
		}
	}
	return &Scope{db: postgres.OpenPool(pool), assigned: slices.Clone(assigned)}, nil
}

// Assigned lists the organizations to poll, in deployment order.
func (s *Scope) Assigned() []string { return slices.Clone(s.assigned) }

// Call is a pending call of a running investigation, with its requester,
// in whose scope it is carried out.
type Call struct {
	InvestigationID string
	Seq             int
	RequestedBy     string
}

// Lost is a running investigation whose requester no longer holds owner or
// editor; the executor ends it (EndLost) and carries out none of its calls.
type Lost struct {
	InvestigationID string
	RequestedBy     string
}

// Discovery is what Pending found in one organization.
type Discovery struct {
	Calls []Call
	Lost  []Lost
}

// pendingSQL is the discovery: one read-only statement on the partial index
// of pending calls. Each row also says whether its transaction is
// read-only, which Pending requires.
const pendingSQL = `SELECT 'call', c.investigation_id, c.seq, i.created_by, current_setting('transaction_read_only') = 'on'
  FROM twin.investigation_tool_calls c
  JOIN twin.investigations i ON i.organization_id = c.organization_id AND i.id = c.investigation_id
  WHERE c.organization_id = $1 AND c.status = 'pending' AND i.status = 'running'
UNION ALL
SELECT 'lost', i.id, 0, i.created_by, current_setting('transaction_read_only') = 'on'
  FROM twin.investigations i
  WHERE i.organization_id = $1 AND i.status = 'running' AND NOT EXISTS (SELECT 1 FROM twin.memberships m
    WHERE m.organization_id = i.organization_id AND m.user_id = i.created_by AND m.role IN ('owner', 'editor'))
ORDER BY 1, 2, 3
LIMIT $2`

// endLostSQL stops a running investigation whose requester holds no owner
// or editor membership (the database checks it again).
const endLostSQL = `UPDATE twin.investigations SET status = 'stopped', reason = 'role_lost'
  WHERE organization_id = $1 AND id = $2 AND status = 'running' AND NOT EXISTS (SELECT 1 FROM twin.memberships m
    WHERE m.organization_id = $1 AND m.user_id = investigations.created_by AND m.role IN ('owner', 'editor'))`

func (s *Scope) run(ctx context.Context, org string, opts pgx.TxOptions, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if !slices.Contains(s.assigned, org) {
		return ErrNotAssigned
	}
	return s.db.Tx(ctx, opts, func(ctx context.Context, tx pgx.Tx) error {
		if err := scope.Set(ctx, tx, org, ""); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}

// Pending returns org's pending calls and lost investigations, in
// investigation and seq order, in one read-only transaction.
func (s *Scope) Pending(ctx context.Context, org string) (Discovery, error) {
	var d Discovery
	err := s.run(ctx, org, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, pendingSQL, org, MaxPending)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var kind, inv, user string
			var seq int
			var readOnly bool
			if err := rows.Scan(&kind, &inv, &seq, &user, &readOnly); err != nil {
				return err
			}
			if !readOnly {
				return errors.New("actingfor: the discovery ran outside a read-only transaction")
			}
			if kind == "call" {
				d.Calls = append(d.Calls, Call{InvestigationID: inv, Seq: seq, RequestedBy: user})
			} else {
				d.Lost = append(d.Lost, Lost{InvestigationID: inv, RequestedBy: user})
			}
		}
		return rows.Err()
	})
	if err != nil {
		return Discovery{}, err
	}
	return d, nil
}

// EndLost stops investigation inv of org with role_lost if its requester
// holds no owner or editor membership; it reports whether it did.
func (s *Scope) EndLost(ctx context.Context, org, inv string) (bool, error) {
	if !scope.InvestigationID.MatchString(inv) {
		return false, tenant.ErrInvalidScope
	}
	var ended bool
	err := s.run(ctx, org, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, endLostSQL, org, inv)
		ended = tag.RowsAffected() == 1
		return err
	})
	return ended, err
}
