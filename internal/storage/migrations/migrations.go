// Package migrations applies the repository's migrations/ to a deployment's
// database (T-039): in order, each with its record in the same transaction,
// as a migration login that may act as twin_owner and is nothing else, and
// never twice. It is the only code that applies them outside the disposable
// test databases of scripts/run_db_tests.sh; cmd/migrate composes it.
//
// The record is geminus_migrations.applied (version, file name, SHA-256 of
// the file, time, login), owned by twin_owner and readable by nobody else.
// A database whose record does not match this build's files (an edited
// migration, one this build does not have, a twin schema with no record) is
// refused, never repaired: migrations already applied are never edited
// (AGENTS.md §3), and the operator decides what to do.
package migrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var (
	// ErrInvalidMigration: the files are not a sequence this package can
	// apply (a gap, a duplicate, a transaction statement outside the frame).
	ErrInvalidMigration = errors.New("migrations: invalid migration files")
	// ErrUnsafeLogin: the login is not a migration login (it is a superuser,
	// a runtime or provisioning login, or cannot act as twin_owner).
	ErrUnsafeLogin = errors.New("migrations: refusing the database login")
	// ErrState: the database's record is not this build's files.
	ErrState = errors.New("migrations: the database's migrations are not this build's")
)

// Migration is one NNNN_name.up.sql file.
type Migration struct {
	Version int
	Name    string // the file name
	SQL     string
	SHA256  string // lowercase hex of the file's bytes
	// framed: the file is BEGIN; … COMMIT; itself, and commit is where its
	// final COMMIT; starts. Otherwise it is run inside a BEGIN; … COMMIT;.
	framed bool
	commit int
}

var (
	upFile = regexp.MustCompile(`^([0-9]{4})_[a-z0-9_]+\.up\.sql$`)
	// A line that is only a transaction statement (PL/pgSQL's BEGIN and
	// END in a function body have no semicolon after BEGIN, and END is not
	// one of these).
	txLine = regexp.MustCompile(`(?im)^[ \t]*(BEGIN|COMMIT|ROLLBACK|START[ \t]+TRANSACTION|END[ \t]+TRANSACTION|ABORT)[ \t]*;[ \t]*$`)
)

// Load reads fsys's NNNN_name.up.sql files (other files are not
// migrations): numbered 0001 to N without a gap or duplicate, each either
// framed (its first statement BEGIN;, its last COMMIT;, no other
// transaction statement) or without any transaction statement.
func Load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMigration, err)
	}
	var ms []Migration
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		m := upFile.FindStringSubmatch(name)
		if m == nil {
			return nil, fmt.Errorf("%w: %s is not named NNNN_name.up.sql (lowercase letters, digits and '_')", ErrInvalidMigration, name)
		}
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalidMigration, name, err)
		}
		v, _ := strconv.Atoi(m[1])
		sum := sha256.Sum256(b)
		mig := Migration{Version: v, Name: name, SQL: string(b), SHA256: hex.EncodeToString(sum[:])}
		if err := mig.frame(); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalidMigration, name, err)
		}
		ms = append(ms, mig)
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	if len(ms) == 0 {
		return nil, fmt.Errorf("%w: no NNNN_name.up.sql file", ErrInvalidMigration)
	}
	for i, m := range ms {
		if m.Version != i+1 {
			return nil, fmt.Errorf("%w: %s: the migrations are numbered from 0001 without a gap or duplicate", ErrInvalidMigration, m.Name)
		}
	}
	return ms, nil
}

// frame finds the file's BEGIN; … COMMIT; frame, if it has one.
func (m *Migration) frame() error {
	if !utf8.ValidString(m.SQL) || strings.ContainsRune(m.SQL, 0) {
		return errors.New("not UTF-8 text")
	}
	statements := false
	for _, l := range strings.Split(m.SQL, "\n") {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "--") {
			statements = true
			break
		}
	}
	if !statements {
		return errors.New("no statement")
	}
	tx := txLine.FindAllStringSubmatchIndex(m.SQL, -1)
	if len(tx) == 0 {
		return nil
	}
	word := func(i int) string { return strings.ToUpper(m.SQL[tx[i][2]:tx[i][3]]) }
	if len(tx) != 2 || word(0) != "BEGIN" || word(1) != "COMMIT" {
		return errors.New("a transaction statement other than one BEGIN; first and one COMMIT; last")
	}
	if onlyComments(m.SQL[:tx[0][0]]) && onlyComments(m.SQL[tx[1][1]:]) {
		m.framed, m.commit = true, tx[1][0]
		return nil
	}
	return errors.New("a statement before its BEGIN; or after its COMMIT;")
}

func onlyComments(s string) bool {
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "--") {
			return false
		}
	}
	return true
}

// record is the statement that records m, in m's own transaction. The
// values are a number and text Load has checked (a file name of
// [a-z0-9_.], a hex digest), so they are literals.
func (m Migration) record() string {
	return fmt.Sprintf("INSERT INTO geminus_migrations.applied (version, name, sha256) VALUES (%d, '%s', '%s');", m.Version, m.Name, m.SHA256)
}

// script is what is sent for m: the file as it is, with its record before
// its COMMIT (or the file inside BEGIN; … record; COMMIT;).
func (m Migration) script() string {
	if m.framed {
		return m.SQL[:m.commit] + m.record() + "\nCOMMIT;\n"
	}
	return "BEGIN;\n" + m.SQL + "\n" + m.record() + "\nCOMMIT;\n"
}

// CheckLogin refuses a login that is not a migration login: it must be the
// session's role, able to act as twin_owner (inheriting its privileges),
// a member of none of twin_api, twin_worker and twin_provisioner (a
// superuser is a member of every role), and without SUPERUSER, BYPASSRLS,
// CREATEROLE, CREATEDB or REPLICATION. The error names attributes and
// roles, never a password.
func CheckLogin(ctx context.Context, conn *pgx.Conn) error {
	var (
		login                                       string
		same, owner, super, bypass, createRole, cdb bool
		replication                                 bool
		runtime                                     []string
	)
	err := conn.QueryRow(ctx, `
		SELECT r.rolname, current_user = session_user,
		       EXISTS (SELECT 1 FROM pg_roles o WHERE o.rolname = 'twin_owner' AND pg_has_role(r.oid, o.oid, 'USAGE')),
		       r.rolsuper, r.rolbypassrls, r.rolcreaterole, r.rolcreatedb, r.rolreplication,
		       ARRAY(SELECT x.rolname::text FROM pg_roles x
		             WHERE x.rolname IN ('twin_api', 'twin_worker', 'twin_provisioner') AND pg_has_role(r.oid, x.oid, 'MEMBER')
		             ORDER BY x.rolname)
		FROM pg_roles r WHERE r.rolname = session_user`).
		Scan(&login, &same, &owner, &super, &bypass, &createRole, &cdb, &replication, &runtime)
	if err != nil {
		return fmt.Errorf("migrations: reading the login: %w", err)
	}
	var problems []string
	for _, p := range []struct {
		bad  bool
		what string
	}{
		{!same, "runs as a role other than its login"},
		{!owner, "cannot act as twin_owner (run db/bootstrap_roles.sql and grant twin_owner to the migration login)"},
		{super, "is a superuser"}, {bypass, "has BYPASSRLS"}, {createRole, "has CREATEROLE"}, {cdb, "has CREATEDB"},
		{replication, "has REPLICATION"},
		{len(runtime) > 0, "is a member of " + strings.Join(runtime, ", ")},
	} {
		if p.bad {
			problems = append(problems, p.what)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: login %q %s", ErrUnsafeLogin, login, strings.Join(problems, ", "))
	}
	return nil
}

// lockKey is the advisory lock that makes concurrent runs take turns.
const lockKey = 0x6765_6d69_6e75_7301 // "geminus" + 1

// Applied lists the recorded versions, checked against ms (ErrState when
// they do not match). A database without the record has none, unless it
// has a twin schema, which this package did not make (ErrState).
func Applied(ctx context.Context, conn *pgx.Conn, ms []Migration) ([]int, error) {
	var hasRecord, hasTwin bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('geminus_migrations.applied') IS NOT NULL,
		EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = 'twin')`).Scan(&hasRecord, &hasTwin); err != nil {
		return nil, fmt.Errorf("migrations: reading the record: %w", err)
	}
	if !hasRecord {
		if hasTwin {
			return nil, fmt.Errorf("%w: the database has a twin schema but no migration record (it was migrated by other means)", ErrState)
		}
		return nil, nil
	}
	rows, err := conn.Query(ctx, `SELECT version, name, sha256 FROM geminus_migrations.applied ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("migrations: reading the record: %w", err)
	}
	var applied []int
	for rows.Next() {
		var v int
		var name, sum string
		if err := rows.Scan(&v, &name, &sum); err != nil {
			rows.Close()
			return nil, fmt.Errorf("migrations: reading the record: %w", err)
		}
		switch {
		case v != len(applied)+1:
			rows.Close()
			return nil, fmt.Errorf("%w: the record has a gap before version %d", ErrState, v)
		case v > len(ms):
			rows.Close()
			return nil, fmt.Errorf("%w: version %d (%s) is applied but this build does not have it", ErrState, v, name)
		case ms[v-1].Name != name || ms[v-1].SHA256 != sum:
			rows.Close()
			return nil, fmt.Errorf("%w: %s was applied with other content than this build's %s (a migration once applied is never edited)", ErrState, name, ms[v-1].Name)
		}
		applied = append(applied, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrations: reading the record: %w", err)
	}
	if len(applied) == 0 && hasTwin {
		return nil, fmt.Errorf("%w: the database has a twin schema but no migration recorded (it was migrated by other means)", ErrState)
	}
	return applied, nil
}

// Apply checks the login, then applies the migrations of ms the database
// has not recorded, in order, each with its record in its own transaction;
// it returns the versions it applied. Concurrent runs take turns (an
// advisory lock), so each version is applied once. On an error nothing of
// that migration is kept; the ones before it stay applied.
func Apply(ctx context.Context, conn *pgx.Conn, ms []Migration) ([]int, error) {
	if err := CheckLogin(ctx, conn); err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(lockKey)); err != nil {
		return nil, fmt.Errorf("migrations: waiting for another run: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, int64(lockKey)) }()
	// The record, owned by twin_owner (the login's CREATE comes from it),
	// with nothing for anyone else.
	if _, err := conn.Exec(ctx, `BEGIN;
		SET LOCAL ROLE twin_owner;
		CREATE SCHEMA IF NOT EXISTS geminus_migrations;
		REVOKE ALL ON SCHEMA geminus_migrations FROM PUBLIC;
		CREATE TABLE IF NOT EXISTS geminus_migrations.applied (
		  version integer PRIMARY KEY CHECK (version > 0),
		  name text NOT NULL UNIQUE CHECK (name ~ '^[0-9]{4}_[a-z0-9_]+\.up\.sql$'),
		  sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
		  applied_at timestamptz NOT NULL DEFAULT now(),
		  applied_by text NOT NULL DEFAULT session_user);
		REVOKE ALL ON geminus_migrations.applied FROM PUBLIC;
		COMMIT;`); err != nil {
		rollback(conn)
		return nil, fmt.Errorf("migrations: creating the record: %w", err)
	}
	var schemaOwner string
	if err := conn.QueryRow(ctx, `SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'geminus_migrations'`).Scan(&schemaOwner); err != nil {
		return nil, fmt.Errorf("migrations: reading the record: %w", err)
	}
	if schemaOwner != "twin_owner" {
		return nil, fmt.Errorf("%w: geminus_migrations belongs to %q, not twin_owner", ErrState, schemaOwner)
	}
	applied, err := Applied(ctx, conn, ms)
	if err != nil {
		return nil, err
	}
	var done []int
	for _, m := range ms[len(applied):] {
		if _, err := conn.Exec(ctx, m.script()); err != nil {
			rollback(conn)
			return done, fmt.Errorf("migrations: %s: %w", m.Name, err)
		}
		var ok bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM geminus_migrations.applied WHERE version = $1 AND sha256 = $2)`,
			m.Version, m.SHA256).Scan(&ok); err != nil || !ok {
			return done, fmt.Errorf("%w: %s ran but its record is missing", ErrState, m.Name)
		}
		done = append(done, m.Version)
	}
	return done, nil
}

// rollback ends a transaction a failed script left open (its BEGIN ran,
// the error skipped the rest of the script).
func rollback(conn *pgx.Conn) { _, _ = conn.Exec(context.Background(), "ROLLBACK") }
