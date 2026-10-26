// Package provisioning is the operator's controlled bootstrap of tenants
// (D-008, T-010): it creates users and organizations with their first
// owner (and, when an organization is created, one more member: T-057). The
// API never does this: its role has no INSERT on twin.users or
// twin.organizations, and no route exposes it. An operator runs it through
// cmd/provision-tenant with a login that may act as twin_provisioner and is
// neither the schema owner nor a runtime login. Each operation is one
// transaction that switches to twin_provisioner with SET LOCAL (it ends with
// the transaction) and sets the settings the RLS policies read.
// twin_provisioner can only insert users, organizations, memberships and
// audit events, and read users (D-042, migrations/0004_provisioner.up.sql).
package provisioning

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/postgres"
)

// Actor is how provisioning appears in audit events (actor_type system).
const Actor = "provision-tenant"

var (
	// ErrInvalidInput: an issuer and subject no verified token can carry
	// (auth.ValidIdentity), or an organization name that is empty, blank,
	// longer than 200 characters, starts or ends with a space, or has a
	// character that is not printable (control, format such as U+202E or
	// U+200B, a separator other than the ASCII space, private use).
	ErrInvalidInput = errors.New("provisioning: invalid input")
	// ErrNotOperator: the login cannot act as twin_provisioner, is also the
	// schema owner (twin_owner) or a runtime login (twin_api, twin_worker),
	// or has an attribute it does not need.
	ErrNotOperator = errors.New("provisioning: the login is not an operator login")
)

// CheckOperator confirms that the login may SET ROLE twin_provisioner, is
// not a member of twin_owner or a runtime role and has none of the
// SUPERUSER, BYPASSRLS, CREATEROLE, CREATEDB or REPLICATION attributes:
// provisioning must not run as the schema owner, the API or the worker, the
// API must not be able to provision, and the provisioning login gets no
// more than it needs (D-042, db/README.md). A superuser is also a member of
// every role.
func CheckOperator(ctx context.Context, db *postgres.DB) error {
	return db.Tx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var provisioner, other, attrs bool
		err := tx.QueryRow(ctx, `SELECT pg_has_role(session_user,'twin_provisioner','SET'),
			pg_has_role(session_user,'twin_owner','MEMBER') OR pg_has_role(session_user,'twin_api','MEMBER') OR pg_has_role(session_user,'twin_worker','MEMBER'),
			rolsuper OR rolbypassrls OR rolcreaterole OR rolcreatedb OR rolreplication
			FROM pg_roles WHERE rolname = session_user`).Scan(&provisioner, &other, &attrs)
		if err != nil {
			return err
		}
		if !provisioner || other || attrs {
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
		if err := asProvisioner(ctx, tx); err != nil {
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
	orgID, userID, _, err = organization(ctx, db, name, issuer, subject, nil)
	return orgID, userID, err
}

// Member is one more person an organization gets when it is created, beside
// its first owner (T-057: the judge's demo account of the hackathon
// deployment, H-035 (2)). Role is "editor" or "viewer": more owners come
// only from an owner, through the membership API (ADR-023). It is the only
// way provisioning adds anyone but the owner: migration 0004 lets it insert
// memberships only into the organization its own transaction created.
type Member struct {
	Issuer, Subject, Role string
}

// OrganizationWithMember is Organization with member added in the same
// transaction (created as a user if there is none), recorded in the same
// audit event. A member that is no editor or viewer, has an identity no
// token can carry or is the owner again is ErrInvalidInput, and nothing is
// written.
func OrganizationWithMember(ctx context.Context, db *postgres.DB, name, issuer, subject string, member Member) (orgID, ownerID, memberID string, err error) {
	return organization(ctx, db, name, issuer, subject, &member)
}

func organization(ctx context.Context, db *postgres.DB, name, issuer, subject string, member *Member) (orgID, userID, memberID string, err error) {
	if err := identity(issuer, subject); err != nil {
		return "", "", "", err
	}
	if !validName(name) {
		return "", "", "", fmt.Errorf("%w: organization name", ErrInvalidInput)
	}
	meta := map[string]any{}
	if member != nil {
		if err := identity(member.Issuer, member.Subject); err != nil {
			return "", "", "", err
		}
		if member.Role != "editor" && member.Role != "viewer" {
			return "", "", "", fmt.Errorf("%w: a member is an editor or a viewer", ErrInvalidInput)
		}
		if member.Issuer == issuer && member.Subject == subject {
			return "", "", "", fmt.Errorf("%w: the member is the owner", ErrInvalidInput)
		}
	}
	orgID = ids.New("org")
	err = db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := asProvisioner(ctx, tx); err != nil {
			return err
		}
		var err error
		if userID, _, err = user(ctx, tx, issuer, subject); err != nil {
			return err
		}
		meta["owner_user_id"] = userID
		if member != nil {
			if memberID, _, err = user(ctx, tx, member.Issuer, member.Subject); err != nil {
				return err
			}
			meta["member_user_id"], meta["member_role"] = memberID, member.Role
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
		if member != nil {
			if _, err := tx.Exec(ctx, `INSERT INTO twin.memberships(organization_id,user_id,role) VALUES ($1,$2,$3)`, orgID, memberID, member.Role); err != nil {
				return err
			}
		}
		// Through auditlog, the one path to twin.audit_events (T-035): the
		// event is checked against internal/audit's catalogue first. An
		// operator's run has no HTTP request, so it gets a request ID of its
		// own.
		return auditlog.AppendSystem(ctx, tx, orgID, Actor, auditlog.Event{Action: "organization.provision", TargetType: "organization",
			TargetID: orgID, RequestID: ids.New("req"), Metadata: meta})
	})
	if err != nil {
		return "", "", "", err
	}
	return orgID, userID, memberID, nil
}

func asProvisioner(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SET LOCAL ROLE twin_provisioner`)
	return err
}

// user finds or creates the user. The users policy shows a row only to a
// transaction whose identity settings name it. Concurrent calls for one
// identity create it once: the loser's insert does nothing and the select
// that follows sees the winner's row.
func user(ctx context.Context, tx pgx.Tx, issuer, subject string) (string, bool, error) {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.oidc_issuer',$1,true), set_config('app.oidc_subject',$2,true)`, issuer, subject); err != nil {
		return "", false, err
	}
	id := ids.New("usr")
	tag, err := tx.Exec(ctx, `INSERT INTO twin.users(id,oidc_issuer,oidc_subject) VALUES ($1,$2,$3)
		ON CONFLICT (oidc_issuer, oidc_subject) DO NOTHING`, id, issuer, subject)
	if err != nil {
		return "", false, err
	}
	if tag.RowsAffected() == 1 {
		return id, true, nil
	}
	err = tx.QueryRow(ctx, `SELECT id FROM twin.users WHERE oidc_issuer=$1 AND oidc_subject=$2`, issuer, subject).Scan(&id)
	return id, false, err
}

// identity accepts what a verified token can carry, so every provisioned
// user can sign in.
func identity(issuer, subject string) error {
	if !auth.ValidIdentity(issuer, subject) {
		return fmt.Errorf("%w: the issuer must be an absolute https URL and the subject 1 to 255 bytes, both printable", ErrInvalidInput)
	}
	return nil
}

func validName(name string) bool {
	return utf8.ValidString(name) && strings.TrimSpace(name) == name && name != "" && utf8.RuneCountInString(name) <= 200 &&
		!strings.ContainsFunc(name, func(r rune) bool { return !unicode.IsPrint(r) })
}
