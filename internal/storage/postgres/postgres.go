// Package postgres is the PostgreSQL foundation repositories build on (T-006):
// a connection pool, a transaction helper and typed errors for the database
// guards in migrations/.
//
// It deliberately has no tenant API yet. Setting app.organization_id and
// app.user_id per transaction (SET LOCAL via set_config(..., true)) is the
// T-007 scope helper, gated by D-008. Until then callers pass the settings
// themselves inside Tx; because set_config(..., true) is transaction-local,
// nothing outlives the transaction on a pooled connection (tested here).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RollbackTimeout bounds the rollback sent after fn fails. It runs on a context
// detached from the caller's, so a cancelled request still releases its locks.
const RollbackTimeout = 5 * time.Second

// DB is a pool of connections to one database.
type DB struct {
	pool *pgxpool.Pool
}

// Open connects with a libpq-style URL or DSN and checks the connection. The
// error never contains the DSN, which may carry a password.
func Open(ctx context.Context, dsn string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("postgres: invalid connection string")
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: creating pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: connecting: %w", err)
	}
	return &DB{pool: pool}, nil
}

// OpenPool wraps an already configured pool (tests use it to bound MaxConns).
func OpenPool(pool *pgxpool.Pool) *DB { return &DB{pool: pool} }

// Close releases every connection.
func (db *DB) Close() { db.pool.Close() }

// Tx runs fn in one transaction. It commits when fn returns nil and rolls back
// when fn returns an error, when ctx is cancelled, or when fn panics (the
// panic is re-raised after the rollback). Errors pass through Classify.
func (db *DB) Tx(ctx context.Context, opts pgx.TxOptions, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	tx, err := db.pool.BeginTx(ctx, opts)
	if err != nil {
		return Classify(fmt.Errorf("postgres: begin: %w", err))
	}
	defer func() {
		if p := recover(); p != nil {
			rollback(ctx, tx)
			panic(p)
		}
	}()
	if err := fn(ctx, tx); err != nil {
		rollback(ctx, tx)
		return Classify(err)
	}
	if err := tx.Commit(ctx); err != nil {
		rollback(ctx, tx)
		// Deferred constraints (manifest checks, latest_version_id) fail here.
		return Classify(fmt.Errorf("postgres: commit: %w", err))
	}
	return nil
}

func rollback(ctx context.Context, tx pgx.Tx) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), RollbackTimeout)
	defer cancel()
	// A failed rollback leaves a broken connection, which pgxpool discards.
	_ = tx.Rollback(rctx)
}

// Kinds of database failure callers act on. Match them with errors.Is.
var (
	ErrImmutable  = errors.New("immutable record")                  // 55000 from the immutability guards
	ErrConflict   = errors.New("unique constraint violated")        // 23505
	ErrReference  = errors.New("foreign key violated")              // 23503: missing or cross-tenant parent
	ErrConstraint = errors.New("check constraint violated")         // 23514: CHECK, domain or manifest check
	ErrPermission = errors.New("permission denied")                 // 42501: privileges or RLS WITH CHECK
	ErrRetryable  = errors.New("serialization failure or deadlock") // 40001, 40P01
	ErrUnclassed  = errors.New("database error")                    // any other SQLSTATE
)

// Error is a classified database error. Its message names the SQLSTATE and
// constraint, plus the server message for the classified kinds, whose
// messages name relations and constraints only. It never carries the server's
// DETAIL (row values for unique and foreign key violations) or the message of
// an unclassified error (e.g. 22P02 quotes the rejected input).
type Error struct {
	Kind       error
	SQLState   string
	Constraint string
	Message    string
	cause      error
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("postgres: %v (SQLSTATE %s)", e.Kind, e.SQLState)
	if e.Constraint != "" {
		msg += ": constraint " + e.Constraint
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// Is makes errors.Is(err, ErrConflict) and friends work.
func (e *Error) Is(target error) bool { return target == e.Kind }

// Unwrap exposes context errors (cancellation) behind the database error.
func (e *Error) Unwrap() error { return e.cause }

var kinds = map[string]error{
	"55000": ErrImmutable, "23505": ErrConflict, "23503": ErrReference, "23514": ErrConstraint,
	"42501": ErrPermission, "40001": ErrRetryable, "40P01": ErrRetryable,
}

// Classify turns a *pgconn.PgError anywhere in err's chain into an *Error.
// Errors without a SQLSTATE (cancellation, I/O) are returned unchanged.
func Classify(err error) error {
	var pg *pgconn.PgError
	if err == nil || !errors.As(err, &pg) {
		return err
	}
	kind, ok := kinds[pg.Code]
	if !ok {
		kind = ErrUnclassed
	}
	var cause error
	if ctxErr := contextErr(err); ctxErr != nil {
		cause = ctxErr
	}
	msg := pg.Message
	if !ok {
		msg = ""
	}
	return &Error{Kind: kind, SQLState: pg.Code, Constraint: pg.ConstraintName, Message: msg, cause: cause}
}

func contextErr(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	}
	return nil
}
