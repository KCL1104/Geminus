// Package workerscope runs job work in the organizations a deployment
// assigns to the worker (D-008): one organization per transaction, never a
// cross-tenant scan, and never an organization taken from a job payload
// without being assigned. It checks no membership, so only the worker may
// link it: the architecture check refuses it anywhere else (T-019; ADR-019
// R3b). Like tenant.DB, it relies on the pool's tenant.Guard and a login
// that tenant.CheckRuntimeRole accepted as the worker role.
package workerscope

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

// ErrNotAssigned: the worker is not assigned the organization (D-008 allowlist).
var ErrNotAssigned = errors.New("workerscope: organization not assigned to this worker")

// Worker is the worker's database scope.
type Worker struct {
	db       *postgres.DB
	assigned []string
}

// New returns a worker scope on pool for the assigned organizations, in
// deployment order. An ID that is not an organization ID, or one listed
// twice, is tenant.ErrInvalidScope.
func New(pool *pgxpool.Pool, assigned []string) (*Worker, error) {
	for i, o := range assigned {
		if !scope.OrgID.MatchString(o) || slices.Contains(assigned[:i], o) {
			return nil, fmt.Errorf("%w: assigned organization %q", tenant.ErrInvalidScope, o)
		}
	}
	return &Worker{db: postgres.OpenPool(pool), assigned: slices.Clone(assigned)}, nil
}

// Assigned lists the organizations to poll, in deployment order.
func (w *Worker) Assigned() []string { return slices.Clone(w.assigned) }

// Run executes fn in one transaction scoped to org, which must be assigned.
// Errors are classified as in tenant.DB.Run; the transaction rolls back on
// error, panic or cancellation, which also clears the scope.
func (w *Worker) Run(ctx context.Context, org string, opts pgx.TxOptions, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if !slices.Contains(w.assigned, org) {
		return ErrNotAssigned
	}
	return w.db.Tx(ctx, opts, func(ctx context.Context, tx pgx.Tx) error {
		if err := scope.Set(ctx, tx, org, ""); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}
