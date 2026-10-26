package migrations

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/tenant"
)

// migratorLogin is dbtest's migration login.
const migratorLogin = dbtest.MigratorLogin

// freshDB is an empty disposable database (dbtest.EmptyDatabase).
func freshDB(t *testing.T) (string, func(login string) string) {
	t.Helper()
	return "", dbtest.EmptyDatabase(t)
}

func connect(t *testing.T, rawURL string) *pgx.Conn {
	t.Helper()
	c, err := pgx.Connect(context.Background(), rawURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return c
}

func repoMigrations(t *testing.T) []Migration {
	t.Helper()
	ms, err := Load(os.DirFS(filepath.Join("..", "..", "..", "migrations")))
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func versions(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i + 1
	}
	return out
}

// T-039: the migration login applies every migration of this build to an
// empty database once, with its record; a second run applies nothing. The
// schema belongs to twin_owner, the runtime logins pass their own startup
// check on the result (least privilege unchanged) and cannot read the
// record.
func TestApplyOnAFreshDatabase(t *testing.T) {
	_, urlFor := freshDB(t)
	ctx := context.Background()
	ms := repoMigrations(t)
	conn := connect(t, urlFor(migratorLogin))
	done, err := Apply(ctx, conn, ms)
	if err != nil || !slices.Equal(done, versions(len(ms))) {
		t.Fatalf("Apply: %v %v", done, err)
	}
	if done, err := Apply(ctx, conn, ms); err != nil || len(done) != 0 {
		t.Errorf("a second Apply: %v %v", done, err)
	}
	if applied, err := Applied(ctx, conn, ms); err != nil || !slices.Equal(applied, versions(len(ms))) {
		t.Errorf("Applied: %v %v", applied, err)
	}
	var twinOwner, recordOwner, by string
	var n int
	if err := conn.QueryRow(ctx, `SELECT (SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'twin'),
		(SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'geminus_migrations.applied'::regclass),
		(SELECT count(*) FROM geminus_migrations.applied), (SELECT min(applied_by) FROM geminus_migrations.applied)`).
		Scan(&twinOwner, &recordOwner, &n, &by); err != nil {
		t.Fatal(err)
	}
	if twinOwner != "twin_owner" || recordOwner != "twin_owner" || n != len(ms) || by != migratorLogin {
		t.Errorf("owners %q %q, %d records by %q", twinOwner, recordOwner, n, by)
	}
	for login, role := range map[string]string{dbtest.APILogin: tenant.APIRole, dbtest.WorkerLogin: tenant.WorkerRole} {
		pool := dbtest.Pool(t, urlFor(login), 2, true)
		if err := tenant.CheckRuntimeRole(ctx, pool, role); err != nil {
			t.Errorf("%s on the migrated database: %v", login, err)
		}
		if _, err := pool.Exec(ctx, `SELECT count(*) FROM geminus_migrations.applied`); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s reads the record: %v", login, err)
		}
	}
}

// Only a migration login applies migrations: a superuser, a runtime login,
// a login that is also a runtime role's member and one with an attribute
// that bypasses the database's checks are refused before anything is
// written.
func TestApplyRefusesUnsafeLogins(t *testing.T) {
	_, urlFor := freshDB(t)
	ctx := context.Background()
	ms := repoMigrations(t)
	admin := connect(t, urlFor(""))
	// A member of twin_owner and twin_api; a member of twin_owner with
	// every attribute that bypasses the database's checks.
	const both, attributes = "dt_test_migrator_api_login", "dt_test_migrator_attributes_login"
	for name, create := range map[string]string{
		both:       `CREATE ROLE ` + both + ` LOGIN IN ROLE twin_owner, twin_api`,
		attributes: `CREATE ROLE ` + attributes + ` LOGIN BYPASSRLS CREATEROLE CREATEDB REPLICATION IN ROLE twin_owner`,
	} {
		if _, err := admin.Exec(ctx, `DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+name+`') THEN
				`+create+`;
			END IF;
			EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
			END $$`); err != nil {
			t.Fatal(err)
		}
	}
	// Roles are cluster-wide: these do not stay.
	pool := dbtest.Admin(t)
	t.Cleanup(func() {
		for _, name := range []string{both, attributes} {
			if _, err := pool.Exec(context.Background(), `DROP ROLE IF EXISTS `+name); err != nil {
				t.Errorf("dropping %s: %v", name, err)
			}
		}
	})
	// Each is refused for its own reasons ("" is the cluster's superuser,
	// also a member of every role).
	for login, why := range map[string][]string{
		"":                 {"is a superuser"},
		dbtest.APILogin:    {"cannot act as twin_owner", "is a member of twin_api"},
		dbtest.WorkerLogin: {"cannot act as twin_owner", "is a member of twin_worker"},
		both:               {"is a member of twin_api"},
		attributes:         {"has BYPASSRLS", "has CREATEROLE", "has CREATEDB", "has REPLICATION"},
	} {
		conn := connect(t, urlFor(login))
		done, err := Apply(ctx, conn, ms)
		if !errors.Is(err, ErrUnsafeLogin) || len(done) != 0 {
			t.Errorf("login %q: %v %v", login, done, err)
			continue
		}
		for _, w := range why {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("login %q: %v, want %q", login, err, w)
			}
		}
	}
	var record bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname IN ('twin', 'geminus_migrations'))`).Scan(&record); err != nil || record {
		t.Errorf("a refused login wrote something: %v %v", record, err)
	}
}

// A database whose record is not this build's is refused and left as it
// is: an applied migration edited since, a version this build does not
// have, and a twin schema without a record.
func TestApplyRefusesAForeignState(t *testing.T) {
	_, urlFor := freshDB(t)
	ctx := context.Background()
	ms := repoMigrations(t)
	conn := connect(t, urlFor(migratorLogin))
	if _, err := Apply(ctx, conn, ms[:3]); err != nil {
		t.Fatal(err)
	}
	edited := slices.Clone(ms)
	edited[1].SHA256 = strings.Repeat("0", 64)
	if done, err := Apply(ctx, conn, edited); !errors.Is(err, ErrState) || len(done) != 0 {
		t.Errorf("an edited migration: %v %v", done, err)
	}
	if done, err := Apply(ctx, conn, ms[:2]); !errors.Is(err, ErrState) || len(done) != 0 {
		t.Errorf("a database ahead of the build: %v %v", done, err)
	}
	if applied, err := Applied(ctx, conn, ms); err != nil || !slices.Equal(applied, []int{1, 2, 3}) {
		t.Errorf("the record changed: %v %v", applied, err)
	}
	_, urlFor2 := freshDB(t)
	admin := connect(t, urlFor2(""))
	if _, err := admin.Exec(ctx, `CREATE SCHEMA twin AUTHORIZATION twin_owner`); err != nil {
		t.Fatal(err)
	}
	if done, err := Apply(ctx, connect(t, urlFor2(migratorLogin)), ms); !errors.Is(err, ErrState) || len(done) != 0 {
		t.Errorf("a twin schema without a record: %v %v", done, err)
	}
}

// A migration that fails keeps nothing of itself (its statements and its
// record roll back together) and the ones before it stay applied; the
// connection is usable again and a corrected file is applied next time.
func TestAFailedMigrationKeepsNothing(t *testing.T) {
	_, urlFor := freshDB(t)
	ctx := context.Background()
	first := "BEGIN;\nSET LOCAL ROLE twin_owner;\nCREATE SCHEMA t039x;\nCREATE TABLE t039x.a (x int);\nCOMMIT;\n"
	ms, err := Load(fstest.MapFS{
		"0001_a.up.sql": {Data: []byte(first)},
		"0002_b.up.sql": {Data: []byte("CREATE TABLE t039x.b (x int);\nSELECT 1/0;\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	conn := connect(t, urlFor(migratorLogin))
	done, err := Apply(ctx, conn, ms)
	if err == nil || !slices.Equal(done, []int{1}) || !strings.Contains(err.Error(), "0002_b.up.sql") {
		t.Fatalf("Apply: %v %v", done, err)
	}
	var b bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('t039x.b') IS NOT NULL`).Scan(&b); err != nil || b {
		t.Errorf("the failed migration's table: %v %v", b, err)
	}
	fixed, err := Load(fstest.MapFS{
		"0001_a.up.sql": {Data: []byte(first)},
		"0002_b.up.sql": {Data: []byte("CREATE TABLE t039x.b (x int);\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if done, err := Apply(ctx, conn, fixed); err != nil || !slices.Equal(done, []int{2}) {
		t.Errorf("the corrected file: %v %v", done, err)
	}
}

// Two runs at once (two deploys) take turns: every version is applied
// once, by one of them, and neither fails.
func TestConcurrentRunsTakeTurns(t *testing.T) {
	_, urlFor := freshDB(t)
	ms := repoMigrations(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var all []int
	for range 2 {
		conn := connect(t, urlFor(migratorLogin))
		wg.Go(func() {
			done, err := Apply(context.Background(), conn, ms)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			all = append(all, done...)
			mu.Unlock()
		})
	}
	wg.Wait()
	slices.Sort(all)
	if !slices.Equal(all, versions(len(ms))) {
		t.Errorf("applied %v", all)
	}
}
