package postgres_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/storage/postgres"
)

func TestClassify(t *testing.T) {
	secret := "org_01M4YDQK00SECRETVALVE0000"
	for _, c := range []struct {
		code string
		kind error
	}{
		{"55000", postgres.ErrImmutable}, {"23505", postgres.ErrConflict}, {"23503", postgres.ErrReference},
		{"23514", postgres.ErrConstraint}, {"42501", postgres.ErrPermission}, {"40001", postgres.ErrRetryable},
		{"40P01", postgres.ErrRetryable}, {"22P02", postgres.ErrUnclassed},
	} {
		pg := &pgconn.PgError{Code: c.code, ConstraintName: "c1", Message: "server message",
			Detail: "Key (id)=(" + secret + ") already exists."}
		if c.code == "22P02" {
			pg.Message = `invalid input syntax for type bigint: "` + secret + `"`
		}
		err := postgres.Classify(fmt.Errorf("repo: %w", pg))
		var e *postgres.Error
		if !errors.Is(err, c.kind) || !errors.As(err, &e) || e.SQLState != c.code {
			t.Errorf("%s: got %v", c.code, err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("%s: error quotes row data: %v", c.code, err)
		}
	}
	plain := errors.New("not a database error")
	if postgres.Classify(plain) != plain || postgres.Classify(nil) != nil || postgres.Classify(context.Canceled) != context.Canceled {
		t.Error("non-database errors must pass through unchanged")
	}
	cancelled := postgres.Classify(fmt.Errorf("%w: %w", context.Canceled, &pgconn.PgError{Code: "57014"}))
	if !errors.Is(cancelled, context.Canceled) || !errors.Is(cancelled, postgres.ErrUnclassed) {
		t.Errorf("a cancelled query keeps context.Canceled: %v", cancelled)
	}
}

// --- Integration tests: a disposable dt_test_* database after `make db-test`.

func pool(t *testing.T, maxConns int32) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("GEMINUS_TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("GEMINUS_REQUIRE_DB_TESTS") == "1" {
			t.Fatal("GEMINUS_REQUIRE_DB_TESTS=1 but GEMINUS_TEST_DATABASE_URL is not set")
		}
		t.Skip("NOT RUN: set GEMINUS_TEST_DATABASE_URL to a disposable dt_test_* database prepared by `make db-test`")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal("invalid GEMINUS_TEST_DATABASE_URL")
	}
	cfg.MaxConns = maxConns
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	var name string
	var schema bool
	if err := p.QueryRow(context.Background(),
		`SELECT current_database(), EXISTS (SELECT 1 FROM pg_namespace WHERE nspname='twin')`).Scan(&name, &schema); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(name, "dt_test_") || !schema {
		t.Fatalf("refusing database %q (twin schema: %v): integration tests need a disposable dt_test_* database with migrations applied", name, schema)
	}
	return p
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newID returns a fresh D-001 ID so repeated runs never collide.
func newID(t *testing.T, prefix string) string {
	t.Helper()
	b := []byte{"01234567"[randInt(t, 8)]}
	for range 25 {
		b = append(b, crockford[randInt(t, 32)])
	}
	return prefix + "_" + string(b)
}

func randInt(t *testing.T, n int64) int64 {
	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		t.Fatal(err)
	}
	return v.Int64()
}

type tenant struct{ org, user, subject, project string }

// scope sets the tenant context transaction-locally and switches role.
func (tn tenant) scope(ctx context.Context, tx pgx.Tx, role string) error {
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SELECT set_config('app.oidc_issuer','https://test.invalid',true),
		set_config('app.oidc_subject',$1,true), set_config('app.organization_id',$2,true), set_config('app.user_id',$3,true)`,
		tn.subject, tn.org, tn.user)
	return err
}

// newTenant commits a user, organization, membership and project.
func newTenant(t *testing.T, db *postgres.DB) tenant {
	t.Helper()
	tn := tenant{org: newID(t, "org"), user: newID(t, "usr"), subject: newID(t, "sub"), project: newID(t, "prj")}
	err := db.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := tn.scope(ctx, tx, "twin_owner"); err != nil {
			return err
		}
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO twin.users(id,oidc_issuer,oidc_subject) VALUES ($1,'https://test.invalid',$2)`, []any{tn.user, tn.subject}},
			{`INSERT INTO twin.organizations(id,organization_id,name) VALUES ($1,$1,'Tenant')`, []any{tn.org}},
			{`INSERT INTO twin.memberships(organization_id,user_id,role) VALUES ($1,$2,'owner')`, []any{tn.org, tn.user}},
			{`INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'Project',$3)`, []any{tn.project, tn.org, tn.user}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	return tn
}

func projectExists(t *testing.T, db *postgres.DB, tn tenant, id string) bool {
	t.Helper()
	var n int
	err := db.Tx(context.Background(), pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		if err := tn.scope(ctx, tx, "twin_api"); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM twin.projects WHERE id=$1`, id).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func insertProject(tn tenant, id string) func(context.Context, pgx.Tx) error {
	return func(ctx context.Context, tx pgx.Tx) error {
		if err := tn.scope(ctx, tx, "twin_api"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, id, tn.org, tn.user)
		return err
	}
}

func TestTxCommitRollbackPanic(t *testing.T) {
	db := postgres.OpenPool(pool(t, 2))
	tn := newTenant(t, db)
	if !projectExists(t, db, tn, tn.project) {
		t.Fatal("committed project is not visible")
	}

	boom := errors.New("boom")
	failed := newID(t, "prj")
	err := db.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := insertProject(tn, failed)(ctx, tx); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || projectExists(t, db, tn, failed) {
		t.Errorf("error from fn: got %v; the insert must be rolled back", err)
	}

	// One connection: if a panic leaked it (no rollback), the next
	// transaction could never start and would hit the deadline.
	single := postgres.OpenPool(pool(t, 1))
	panicked := newID(t, "prj")
	func() {
		defer func() {
			if p := recover(); p != "kaboom" {
				t.Errorf("panic not re-raised: %v", p)
			}
		}()
		_ = single.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			if err := insertProject(tn, panicked)(ctx, tx); err != nil {
				return err
			}
			panic("kaboom")
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var n int
	if err := single.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := tn.scope(ctx, tx, "twin_api"); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM twin.projects WHERE id=$1`, panicked).Scan(&n)
	}); err != nil || n != 0 {
		t.Errorf("after a panic: %v, %d rows; the connection must be released and nothing committed", err, n)
	}
}

// Tenant context and role set with SET LOCAL / set_config(...,true) never
// outlive the transaction on a pooled connection (MaxConns=1 forces reuse).
func TestScopeIsTransactionLocal(t *testing.T) {
	db := postgres.OpenPool(pool(t, 1))
	tn := newTenant(t, db)
	var admin string
	if err := db.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_user`).Scan(&admin)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		return tn.scope(ctx, tx, "twin_api")
	}); err != nil {
		t.Fatal(err)
	}
	var org, user, role string
	if err := db.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT coalesce(current_setting('app.organization_id',true),''),
			coalesce(current_setting('app.user_id',true),''), current_user`).Scan(&org, &user, &role)
	}); err != nil {
		t.Fatal(err)
	}
	if org != "" || user != "" || role != admin {
		t.Errorf("context leaked into the next transaction: org=%q user=%q role=%q (want role %q)", org, user, role, admin)
	}

	// Sensitivity check: a session-level setting does leak, so the test above
	// would have caught a helper that used it.
	leak := postgres.OpenPool(pool(t, 1))
	_ = leak.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,false)`, tn.org)
		return err
	})
	_ = leak.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT coalesce(current_setting('app.organization_id',true),'')`).Scan(&org)
	})
	if org != tn.org {
		t.Errorf("control: a session-level set_config should persist on the connection, got %q", org)
	}
}

// The database guards surface as typed errors that never quote row data.
func TestGuardsAreClassified(t *testing.T) {
	db := postgres.OpenPool(pool(t, 2))
	a, b := newTenant(t, db), newTenant(t, db)
	model, version := newID(t, "mdl"), newID(t, "mv")
	ir := fmt.Sprintf(`{"schema_version":"0.1","model_id":%q}`, model)
	hash := "sha256:" + strings.Repeat("0", 64)
	if err := db.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := a.scope(ctx, tx, "twin_api"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO twin.models(id,organization_id,project_id,name,domain,created_by)
			VALUES ($1,$2,$3,'M','software_delivery',$4)`, model, a.org, a.project, a.user); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,ir,hash,created_by)
			VALUES ($1,$2,$3,1,'0.1',$4,$5,$6)`, version, a.org, model, ir, hash, a.user)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	as := func(tn tenant, role, sql string, args ...any) error {
		return db.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			if err := tn.scope(ctx, tx, role); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, sql, args...)
			return err
		})
	}
	for _, c := range []struct {
		name string
		err  error
		kind error
	}{
		{"runtime role cannot update a version", as(a, "twin_api", `UPDATE twin.model_versions SET hash=$1 WHERE id=$2`, hash, version), postgres.ErrPermission},
		{"even the owner cannot update a version", as(a, "twin_owner", `UPDATE twin.model_versions SET hash=$1 WHERE id=$2`, hash, version), postgres.ErrImmutable},
		{"malformed ID", as(a, "twin_api", `INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ('prj_bad',$1,'P',$2)`, a.org, a.user), postgres.ErrConstraint},
		{"duplicate ID", as(a, "twin_api", `INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, a.project, a.org, a.user), postgres.ErrConflict},
		{"cross-tenant parent", as(a, "twin_api", `INSERT INTO twin.models(id,organization_id,project_id,name,domain,created_by) VALUES ($1,$2,$3,'M','software_delivery',$4)`,
			newID(t, "mdl"), a.org, b.project, a.user), postgres.ErrReference},
		{"cross-tenant write", as(a, "twin_api", `INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, newID(t, "prj"), b.org, a.user), postgres.ErrPermission},
		{"manifest checked at commit", as(a, "twin_api", `INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,ir,hash,dataset_bindings,created_by)
			VALUES ($1,$2,$3,2,'0.1',$4,$5,'[{"logical_ref":"dataset:x"}]',$6)`, newID(t, "mv"), a.org, model, ir, hash, a.user), postgres.ErrConstraint},
	} {
		if !errors.Is(c.err, c.kind) {
			t.Errorf("%s: got %v, want %v", c.name, c.err, c.kind)
			continue
		}
		for _, secret := range []string{a.project, b.project, version} {
			if strings.Contains(c.err.Error(), secret) {
				t.Errorf("%s: error quotes row data: %v", c.name, c.err)
			}
		}
	}
}

// Cancellation rolls back and releases locks; the pool stays usable.
func TestCancellationReleasesLocks(t *testing.T) {
	db := postgres.OpenPool(pool(t, 2))
	tn := newTenant(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := tn.scope(ctx, tx, "twin_api"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT 1 FROM twin.projects WHERE id=$1 FOR UPDATE`, tn.project); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT pg_sleep(10)`)
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("got %v after %v; want a prompt context.DeadlineExceeded", err, time.Since(start))
	}
	if err := db.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := tn.scope(ctx, tx, "twin_api"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '2s'`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT 1 FROM twin.projects WHERE id=$1 FOR UPDATE`, tn.project)
		return err
	}); err != nil {
		t.Errorf("the cancelled transaction still holds its lock: %v", err)
	}
}

// Review of T-006: a repository's own typed error (e.g. a stale revision) must
// survive Classify even when it wraps a database error, while the message
// still carries no server text.
func TestClassifyKeepsCallerErrors(t *testing.T) {
	errStale := errors.New("stale revision")
	secret := "SECRET-ROW-VALUE"
	pg := &pgconn.PgError{Code: "22012", Message: "division by zero " + secret}
	err := postgres.Classify(fmt.Errorf("%w: %w", errStale, pg))
	if !errors.Is(err, errStale) || !errors.Is(err, postgres.ErrUnclassed) {
		t.Errorf("caller sentinel or kind lost: %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("message quotes the server: %v", err)
	}
	// Classifying twice (a helper inside fn already classified) keeps the first result.
	if again := postgres.Classify(fmt.Errorf("outer: %w", err)); !errors.Is(again, errStale) || strings.Contains(again.Error(), secret) {
		t.Errorf("reclassified: %v", again)
	}
}

// fn ending its goroutine with runtime.Goexit (t.FailNow inside fn) must
// still roll back and release the connection.
func TestTxGoexit(t *testing.T) {
	single := postgres.OpenPool(pool(t, 1))
	tn := newTenant(t, postgres.OpenPool(pool(t, 1)))
	left := newID(t, "prj")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = single.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			if err := insertProject(tn, left)(ctx, tx); err != nil {
				return err
			}
			runtime.Goexit()
			return nil
		})
	}()
	<-done
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var n int
	if err := single.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := tn.scope(ctx, tx, "twin_api"); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM twin.projects WHERE id=$1`, left).Scan(&n)
	}); err != nil || n != 0 {
		t.Errorf("after Goexit: %v, %d rows; the connection must be released and nothing committed", err, n)
	}
}
