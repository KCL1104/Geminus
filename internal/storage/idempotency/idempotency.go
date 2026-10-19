// Package idempotency stores the response of a write so a retry with the
// same Idempotency-Key gets the same effect (D-011): same key and same
// request replays the stored response; same key with another request is
// ErrConflict. Keys are scoped by organization, authenticated actor and
// operation, and claimed inside the write's own transaction, so the effect,
// its audit event and the stored response commit together. Two requests
// racing on one key are serialised by the primary key: the second waits
// for the first to commit and then replays it.
package idempotency

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// TTL is how long a completed key replays (P0 retention; D-011).
const TTL = 24 * time.Hour

// Key identifies one client request.
type Key struct {
	Org, Actor string
	Operation  string // operation and resource, e.g. "createProject"
	Key        string // the client's Idempotency-Key, 8–128 characters
}

// Response is what a completed request answered.
type Response struct {
	Status int
	Body   []byte // JSON
}

var (
	// ErrConflict: the key was used for a different request.
	ErrConflict = errors.New("idempotency: key used for a different request")
	// ErrInProgress: the key's first request has not completed. Claim and
	// completion share one transaction, so a committed key is complete;
	// this is reported rather than assumed.
	ErrInProgress = errors.New("idempotency: key's first request has not completed")
)

// Claim claims k for a request whose canonical hash is hash. It returns the
// stored response of an earlier request with the same hash (replay), or
// ErrConflict for another hash, or (nil, nil): the key is claimed and the
// caller does the work and calls Complete in the same transaction. An
// expired key is claimed afresh.
func Claim(ctx context.Context, tx pgx.Tx, k Key, hash string) (*Response, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO twin.idempotency_keys(organization_id,actor_id,operation_scope,key,request_hash,expires_at)
		VALUES ($1,$2,$3,$4,$5,now()+make_interval(secs => $6)) ON CONFLICT DO NOTHING`, k.Org, k.Actor, k.Operation, k.Key, hash, TTL.Seconds())
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 1 {
		return nil, nil
	}
	// FOR UPDATE: retries racing on an expired key queue here, and each
	// reads the row as the one before it left it, so only the first
	// re-claims it (T-009 review M1).
	var stored string
	var status *int
	var body []byte
	var expired bool
	err = tx.QueryRow(ctx, `SELECT request_hash, response_status, response_body, expires_at <= now()
		FROM twin.idempotency_keys WHERE organization_id=$1 AND actor_id=$2 AND operation_scope=$3 AND key=$4 FOR UPDATE`,
		k.Org, k.Actor, k.Operation, k.Key).Scan(&stored, &status, &body, &expired)
	if err != nil {
		return nil, err
	}
	if expired {
		_, err := tx.Exec(ctx, `UPDATE twin.idempotency_keys SET request_hash=$5, response_status=NULL, response_body=NULL,
			completed_at=NULL, created_at=now(), expires_at=now()+make_interval(secs => $6)
			WHERE organization_id=$1 AND actor_id=$2 AND operation_scope=$3 AND key=$4`, k.Org, k.Actor, k.Operation, k.Key, hash, TTL.Seconds())
		return nil, err
	}
	switch {
	case stored != hash:
		return nil, ErrConflict
	case status == nil:
		return nil, ErrInProgress
	}
	return &Response{Status: *status, Body: body}, nil
}

// Seen reports whether k has an unexpired row: a request with this key was
// made before (completed or not). It reads only, so a request whose body is
// large (an upload, T-016) can tell before storing that body whether it can
// only be a replay or a conflict; Claim still decides.
func Seen(ctx context.Context, tx pgx.Tx, k Key) (bool, error) {
	var seen bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM twin.idempotency_keys
		WHERE organization_id=$1 AND actor_id=$2 AND operation_scope=$3 AND key=$4 AND expires_at > now())`,
		k.Org, k.Actor, k.Operation, k.Key).Scan(&seen)
	return seen, err
}

// Complete stores the response of a request Claim claimed in tx.
func Complete(ctx context.Context, tx pgx.Tx, k Key, r Response) error {
	tag, err := tx.Exec(ctx, `UPDATE twin.idempotency_keys SET response_status=$5, response_body=$6, completed_at=now()
		WHERE organization_id=$1 AND actor_id=$2 AND operation_scope=$3 AND key=$4 AND completed_at IS NULL`,
		k.Org, k.Actor, k.Operation, k.Key, r.Status, string(r.Body))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("idempotency: completing a key that was not claimed in this transaction")
	}
	return nil
}
