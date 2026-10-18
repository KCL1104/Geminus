// Package provisioning is the operator's controlled bootstrap of tenants
// (D-008, T-010): it creates users and organizations with their first
// owner. The API never does this: its role has no INSERT on twin.users or
// twin.organizations, and no route exposes it. An operator runs it through
// cmd/provision-tenant with a login that may act as twin_owner and is not a
// runtime login. Each operation is one transaction that switches to
// twin_owner with SET LOCAL (it ends with the transaction) and sets the
// settings the RLS policies read.
package provisioning

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/postgres"
)

// Actor is how provisioning appears in audit events (actor_type system).
const Actor = "provision-tenant"

var (
	// ErrInvalidInput: an issuer, subject or organization name the schema
	// would refuse (empty, too long, not UTF-8, U+0000 or control
	// characters in a name).
	ErrInvalidInput = errors.New("provisioning: invalid input")
	// ErrNotOperator: the login cannot act as twin_owner, or is also a
	// runtime login (twin_api or twin_worker).
	ErrNotOperator = errors.New("provisioning: the login is not an operator login")
)

// CheckOperator confirms that the login may SET ROLE twin_owner and is not
// a member of a runtime role: provisioning must not run as the API or the
// worker, and the API must not be able to provision. A superuser is a
// member of every role, so it is refused too: provisioning has a login of
// its own (db/README.md).
func CheckOperator(ctx context.Context, db *postgres.DB) error {
	return db.Tx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var owner, runtime bool
		err := tx.QueryRow(ctx, `SELECT pg_has_role(session_user,'twin_owner','SET'),
			pg_has_role(session_user,'twin_api','MEMBER') OR pg_has_role(session_user,'twin_worker','MEMBER')`).Scan(&owner, &runtime)
		if err != nil {
			return err
		}
		if !owner || runtime {
			return ErrNotOperator
		}
		return nil
	})
}

// User returns the ID of the user with the issuer and subject, creating it
// if there is none; created says which.
func User(ctx context.Context, db *postgres.DB, issuer, subject string) (id string, created bool, err error) {
	if err := identity(issuer, subject); err != nil {
		return "", false, err
	}
	err = db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := asOwner(ctx, tx); err != nil {
			return err
		}
		id, created, err = user(ctx, tx, issuer, subject)
		return err
	})
	return id, created, err
}

// Organization creates an organization named name whose first owner is the
// user with the issuer and subject (created if there is none), and records
// the event in the organization's audit log.
func Organization(ctx context.Context, db *postgres.DB, name, issuer, subject string) (orgID, userID string, err error) {
	if err := identity(issuer, subject); err != nil {
		return "", "", err
	}
	if name == "" || utf8.RuneCountInString(name) > 200 || !utf8.ValidString(name) || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", "", fmt.Errorf("%w: organization name", ErrInvalidInput)
	}
	orgID = ids.New("org")
	err = db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := asOwner(ctx, tx); err != nil {
			return err
		}
		var err error
		if userID, _, err = user(ctx, tx, issuer, subject); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,true)`, orgID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO twin.organizations(id,organization_id,name) VALUES ($1,$1,$2)`, orgID, name); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO twin.memberships(organization_id,user_id,role) VALUES ($1,$2,'owner')`, orgID, userID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO twin.audit_events(id,organization_id,actor_type,actor_id,action,target_type,target_id,request_id,metadata)
			VALUES ($1,$2,'system',$3,'organization.provision','organization',$4,$5,jsonb_build_object('owner_user_id',$6::text))`,
			ids.New("aud"), orgID, Actor, orgID, ids.New("req"), userID)
		return err
	})
	if err != nil {
		return "", "", err
	}
	return orgID, userID, nil
}

func asOwner(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`)
	return err
}

// user finds or creates the user. The users policy shows a row only to a
// transaction whose identity settings name it.
func user(ctx context.Context, tx pgx.Tx, issuer, subject string) (string, bool, error) {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.oidc_issuer',$1,true), set_config('app.oidc_subject',$2,true)`, issuer, subject); err != nil {
		return "", false, err
	}
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM twin.users WHERE oidc_issuer=$1 AND oidc_subject=$2`, issuer, subject).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	id = ids.New("usr")
	_, err = tx.Exec(ctx, `INSERT INTO twin.users(id,oidc_issuer,oidc_subject) VALUES ($1,$2,$3)`, id, issuer, subject)
	return id, true, err
}

// identity applies the limits tenant.ResolveUser applies to a verified
// identity, so every provisioned user can sign in.
func identity(issuer, subject string) error {
	for _, s := range []string{issuer, subject} {
		if s == "" || !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return fmt.Errorf("%w: issuer and subject must be non-empty UTF-8 without U+0000", ErrInvalidInput)
		}
	}
	if len(issuer) > 2048 || len(subject) > 255 {
		return fmt.Errorf("%w: issuer or subject too long", ErrInvalidInput)
	}
	return nil
}
