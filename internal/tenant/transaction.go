// Package tenant runs database work inside one organization's scope (T-007).
//
// Every request transaction sets app.organization_id / app.user_id with
// set_config(..., true), so the scope dies with the transaction and never
// stays on a pooled connection; RLS then confines every statement to that
// organization (migrations/0002_rls.up.sql). The scope is chosen by the API
// from a verified identity (D-008): the X-Organization-ID header only selects
// which membership to use, and Run checks that membership before fn runs.
// RLS is defence in depth; the application filters by organization as well.
//
// Implements D-008 (approved 2026-10-15).
package tenant

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/storage/postgres"
)

// Role is a membership role.
type Role string

const (
	Owner  Role = "owner"
	Editor Role = "editor"
	Viewer Role = "viewer"
)

// Tx and TxOptions are pgx's, under names services can use without
// importing the driver: services hold a transaction for the storage
// packages to use, and never write SQL themselves.
type (
	Tx        = pgx.Tx
	TxOptions = pgx.TxOptions
)

// Transaction modes for Run.
var (
	ReadWrite = TxOptions{}
	ReadOnly  = TxOptions{AccessMode: pgx.ReadOnly}
)

// Scope is the verified caller of one API request.
type Scope struct {
	OrganizationID string // selected via X-Organization-ID; authorized by membership
	UserID         string // resolved from the verified token's issuer and subject
}

// Membership is what Run found for the scope; fn decides what the role allows.
type Membership struct {
	OrganizationID string
	UserID         string
	Role           Role
}

var (
	// ErrNoMembership: the user is not a member of the selected organization.
	// The API answers it like a missing resource (404), never revealing
	// whether the organization exists.
	ErrNoMembership = errors.New("tenant: no membership in the selected organization")
	// ErrInvalidScope: an ID in the scope is not a D-001 ID of its kind.
	ErrInvalidScope = errors.New("tenant: invalid scope")
	// ErrNotAssigned: the worker is not assigned the organization (D-008 allowlist).
	ErrNotAssigned = errors.New("tenant: organization not assigned to this worker")
	// ErrUnknownUser: no user has the verified issuer and subject. Users are
	// provisioned by a controlled process (D-008), never created from a
	// token; the API answers 403 FORBIDDEN, since the caller is authenticated.
	ErrUnknownUser = errors.New("tenant: no user for the verified identity")
	// ErrInvalidIdentity: the issuer or subject is empty, too long, not
	// UTF-8 or holds a NUL (which PostgreSQL text cannot store); a verifier
	// never yields such an identity.
	ErrInvalidIdentity = errors.New("tenant: invalid identity")
)

var (
	orgID  = regexp.MustCompile(`^org_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	userID = regexp.MustCompile(`^usr_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
)

// DB runs scoped transactions on a pool.
type DB struct{ db *postgres.DB }

// New wraps a pool. Configure the pool with Guard so a connection that comes
// back with any scope left on it is discarded.
func New(pool *pgxpool.Pool) *DB { return &DB{db: postgres.OpenPool(pool)} }

// ScopeSettings are the custom settings RLS policies read. Placeholder
// settings are not listed in pg_settings, so Guard checks each by name; a
// test keeps this list equal to what the migrations read.
var ScopeSettings = []string{"app.oidc_issuer", "app.oidc_subject", "app.organization_id", "app.user_id"}

// guardSQL is true only for a connection with no state that could carry one
// request's data or privileges into the next checkout: no scope setting, no
// role switch, no other session-level SET (e.g. search_path), no cursor
// WITH HOLD, temporary table, session advisory lock or LISTEN channel.
var guardSQL = func() string {
	q := "SELECT current_user = session_user"
	for _, s := range ScopeSettings {
		q += " AND coalesce(current_setting('" + s + "', true), '') = ''"
	}
	return q + ` AND NOT EXISTS (SELECT 1 FROM pg_settings WHERE source = 'session')
		AND NOT EXISTS (SELECT 1 FROM pg_cursors WHERE is_holdable)
		AND NOT EXISTS (SELECT 1 FROM pg_class WHERE relnamespace = pg_my_temp_schema())
		AND NOT EXISTS (SELECT 1 FROM pg_locks WHERE pid = pg_backend_pid() AND locktype = 'advisory')
		AND NOT EXISTS (SELECT 1 FROM pg_listening_channels())`
}()

// Guard is a pgxpool AfterRelease hook. Scope is transaction-local, so a
// released connection must carry no session state; one that does (a bug that
// used a session-level SET, a held cursor, a temp table...) is destroyed
// instead of reused. It is a safety net that costs one round trip per
// release; correct code never trips it.
func Guard(conn *pgx.Conn) bool {
	ctx, cancel := context.WithTimeout(context.Background(), postgres.RollbackTimeout)
	defer cancel()
	var clean bool
	err := conn.QueryRow(ctx, guardSQL).Scan(&clean)
	return err == nil && clean
}

func setScope(ctx context.Context, tx pgx.Tx, org, user string) error {
	_, err := tx.Exec(ctx, `SELECT set_config('app.organization_id', $1, true), set_config('app.user_id', $2, true)`, org, user)
	return err
}

// Run executes fn in one transaction scoped to s, after confirming that
// s.UserID is a member of s.OrganizationID. Errors from fn are classified by
// postgres.Classify; the transaction rolls back on error, panic or
// cancellation, which also clears the scope.
func (d *DB) Run(ctx context.Context, s Scope, opts pgx.TxOptions, fn func(ctx context.Context, tx pgx.Tx, m Membership) error) error {
	if !orgID.MatchString(s.OrganizationID) || !userID.MatchString(s.UserID) {
		return ErrInvalidScope
	}
	return d.db.Tx(ctx, opts, func(ctx context.Context, tx pgx.Tx) error {
		if err := setScope(ctx, tx, s.OrganizationID, s.UserID); err != nil {
			return err
		}
		var role Role
		err := tx.QueryRow(ctx, `SELECT role FROM twin.memberships WHERE organization_id=$1 AND user_id=$2`,
			s.OrganizationID, s.UserID).Scan(&role)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoMembership
		}
		if err != nil {
			return err
		}
		return fn(ctx, tx, Membership{OrganizationID: s.OrganizationID, UserID: s.UserID, Role: role})
	})
}

// ResolveUser returns the ID of the user with a verified token's issuer and
// subject (internal/auth). The lookup runs in its own read-only transaction
// whose identity settings are what the users RLS policy reads, so it can see
// only that user's row; the settings die with the transaction. The result
// becomes Scope.UserID for Run.
func (d *DB) ResolveUser(ctx context.Context, issuer, subject string) (string, error) {
	for _, s := range []string{issuer, subject} {
		if s == "" || !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return "", ErrInvalidIdentity
		}
	}
	if len(issuer) > 2048 || len(subject) > 255 {
		return "", ErrInvalidIdentity
	}
	var id string
	var readOnly bool
	err := d.db.Tx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.oidc_issuer', $1, true), set_config('app.oidc_subject', $2, true)`, issuer, subject); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT id, current_setting('transaction_read_only') = 'on' FROM twin.users
			WHERE oidc_issuer=$1 AND oidc_subject=$2`, issuer, subject).Scan(&id, &readOnly)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnknownUser
		}
		return err
	})
	if err != nil {
		return "", err
	}
	if !readOnly {
		return "", errors.New("tenant: identity lookup ran outside a read-only transaction")
	}
	if !userID.MatchString(id) {
		return "", fmt.Errorf("tenant: stored user ID is not a D-001 ID")
	}
	return id, nil
}

// Worker runs job work for the organizations a deployment assigns to it
// (D-008): one organization per transaction, never a cross-tenant scan, and
// never an organization taken from a job payload without being assigned.
type Worker struct {
	db       *DB
	assigned []string
}

// NewWorker returns a worker scope for the assigned organizations.
func NewWorker(d *DB, assigned []string) (*Worker, error) {
	for _, o := range assigned {
		if !orgID.MatchString(o) {
			return nil, fmt.Errorf("%w: assigned organization %q", ErrInvalidScope, o)
		}
	}
	return &Worker{db: d, assigned: slices.Clone(assigned)}, nil
}

// Assigned lists the organizations to poll, in deployment order.
func (w *Worker) Assigned() []string { return slices.Clone(w.assigned) }

// Run executes fn in one transaction scoped to org, which must be assigned.
func (w *Worker) Run(ctx context.Context, org string, opts pgx.TxOptions, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if !slices.Contains(w.assigned, org) {
		return ErrNotAssigned
	}
	return w.db.db.Tx(ctx, opts, func(ctx context.Context, tx pgx.Tx) error {
		if err := setScope(ctx, tx, org, ""); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}
