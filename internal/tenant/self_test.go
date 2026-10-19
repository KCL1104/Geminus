package tenant_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
)

// join makes user a member of org with role, as provisioning or an owner would.
func join(t *testing.T, p *pgxpool.Pool, org, user, role string) {
	t.Helper()
	err := postgres.OpenPool(p).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,true)`, org); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO twin.memberships(organization_id,user_id,role) VALUES ($1,$2,$3)`, org, user, role)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// column is the sorted text of a one-column query.
func column(t *testing.T, ctx context.Context, tx pgx.Tx, sql string, args ...any) []string {
	t.Helper()
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out) // byte order, whatever the database's collation
	return out
}

// D-045: RunSelf is a read-only transaction scoped to the user alone. As
// the API login, it sees the user's own memberships across organizations
// and the organizations they belong to (migration 0008), and no other row:
// not another member's membership, not an organization the user does not
// belong to, not even a project of the user's own organization.
func TestRunSelf(t *testing.T) {
	adm := admin(t)
	a, b, c := newOrg(t, adm), newOrg(t, adm), newOrg(t, adm)
	join(t, adm, b.id, a.user, "viewer")
	d := tenant.New(as(t, apiLogin, 2, true))
	ctx := context.Background()
	var memberships, organizations []string
	var projects, users int
	var org, user, readOnly string
	err := d.RunSelf(ctx, a.user, func(ctx context.Context, tx pgx.Tx) error {
		memberships = column(t, ctx, tx, `SELECT organization_id || ' ' || user_id || ' ' || role FROM twin.memberships`)
		organizations = column(t, ctx, tx, `SELECT id || ' ' || name FROM twin.organizations`)
		return tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM twin.projects), (SELECT count(*) FROM twin.users),
			current_setting('app.organization_id'), current_setting('app.user_id'), current_setting('transaction_read_only')`).
			Scan(&projects, &users, &org, &user, &readOnly)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{a.id + " " + a.user + " owner", b.id + " " + a.user + " viewer"}
	slices.Sort(want)
	if !slices.Equal(memberships, want) {
		t.Errorf("memberships %v, want only the user's own %v", memberships, want)
	}
	wantOrgs := []string{a.id + " Org", b.id + " Org"}
	slices.Sort(wantOrgs)
	if !slices.Equal(organizations, wantOrgs) {
		t.Errorf("organizations %v, want %v (not %s)", organizations, wantOrgs, c.id)
	}
	if projects != 0 || users != 0 {
		t.Errorf("the self scope sees %d projects and %d users", projects, users)
	}
	if org != "" || user != a.user || readOnly != "on" {
		t.Errorf("scope organization %q, user %q, read-only %q", org, user, readOnly)
	}
	// Another user sees only their own: c's owner, a member of c alone.
	err = d.RunSelf(ctx, c.user, func(ctx context.Context, tx pgx.Tx) error {
		memberships = column(t, ctx, tx, `SELECT organization_id || ' ' || user_id FROM twin.memberships`)
		organizations = column(t, ctx, tx, `SELECT id FROM twin.organizations`)
		return nil
	})
	if err != nil || !slices.Equal(memberships, []string{c.id + " " + c.user}) || !slices.Equal(organizations, []string{c.id}) {
		t.Errorf("c's owner sees %v %v (%v)", memberships, organizations, err)
	}
	// A user ID nobody has sees nothing.
	err = d.RunSelf(ctx, newID(t, "usr"), func(ctx context.Context, tx pgx.Tx) error {
		memberships = column(t, ctx, tx, `SELECT organization_id FROM twin.memberships UNION ALL SELECT id FROM twin.organizations`)
		return nil
	})
	if err != nil || len(memberships) != 0 {
		t.Errorf("an unknown user ID sees %v (%v)", memberships, err)
	}
}

// RunSelf is read-only and returns fn's error; its scope is
// transaction-local by itself (no Guard needed, none tripped) after
// success, error, panic and cancellation.
func TestRunSelfModeAndScope(t *testing.T) {
	adm := admin(t)
	a := newOrg(t, adm)
	ctx := context.Background()
	for _, guard := range []bool{true, false} {
		pool := as(t, apiLogin, 1, guard)
		d := tenant.New(pool)
		clean := func(after string) {
			t.Helper()
			if org, user, same := scopeOf(t, pool); org != "" || user != "" || !same {
				t.Errorf("guard %v, after %s: org=%q user=%q same role=%v", guard, after, org, user, same)
			}
			var n int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM twin.memberships) + (SELECT count(*) FROM twin.organizations)`).Scan(&n); err != nil || n != 0 {
				t.Errorf("guard %v, after %s: the reused connection sees %d rows (%v)", guard, after, n, err)
			}
		}
		clean("connect")
		start := pool.Stat().NewConnsCount()
		if err := d.RunSelf(ctx, a.user, func(context.Context, pgx.Tx) error { return nil }); err != nil {
			t.Fatal(err)
		}
		clean("success")
		err := d.RunSelf(ctx, a.user, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE twin.memberships SET role='viewer' WHERE user_id=$1`, a.user)
			return err
		})
		if code(err) != "25006" {
			t.Errorf("guard %v: a write in RunSelf: %v, want 25006 (read-only transaction)", guard, err)
		}
		clean("a refused write")
		boom := errors.New("boom")
		if err := d.RunSelf(ctx, a.user, func(context.Context, pgx.Tx) error { return boom }); !errors.Is(err, boom) {
			t.Errorf("guard %v: fn's error: %v", guard, err)
		}
		clean("error")
		func() {
			defer func() { _ = recover() }()
			_ = d.RunSelf(ctx, a.user, func(context.Context, pgx.Tx) error { panic("kaboom") })
		}()
		clean("panic")
		if n := pool.Stat().NewConnsCount(); guard && n != start {
			t.Errorf("connections were replaced during ordinary runs (%d -> %d): scope outlived its transaction", start, n)
		}
		// A cancelled query closes its connection (pgx), so this comes last.
		cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		err = d.RunSelf(cctx, a.user, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `SELECT pg_sleep(5)`)
			return err
		})
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("guard %v: cancelled run: %v", guard, err)
		}
		clean("cancellation")
	}
	if role := memberRole(t, adm, a.id, a.user); role != "owner" {
		t.Errorf("the refused write changed the role to %q", role)
	}
}

func memberRole(t *testing.T, adm *pgxpool.Pool, org, user string) string {
	t.Helper()
	var role string
	if err := adm.QueryRow(context.Background(), `SELECT role FROM twin.memberships WHERE organization_id=$1 AND user_id=$2`, org, user).Scan(&role); err != nil {
		t.Fatal(err)
	}
	return role
}

// RunSelf locks nothing: a membership change does not wait for it (ADR-023:
// membership rows are locked only by RunOwners and Run's FOR SHARE), and it
// cannot lock a row (a read-only transaction takes no row lock; the 0008
// policies would not let it lock one either, db/tests/self.sql).
func TestRunSelfTakesNoLock(t *testing.T) {
	adm := admin(t)
	a := newOrg(t, adm)
	d := tenant.New(as(t, apiLogin, 2, true))
	ctx := context.Background()
	for _, lock := range []string{"FOR SHARE", "FOR UPDATE", "FOR KEY SHARE"} {
		err := d.RunSelf(ctx, a.user, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `SELECT 1 FROM twin.memberships WHERE user_id=$1 `+lock, a.user)
			return err
		})
		if code(err) != "25006" {
			t.Errorf("SELECT %s in RunSelf: %v, want 25006 (read-only transaction)", lock, err)
		}
	}
	inside, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var seen int
	go func() {
		done <- d.RunSelf(ctx, a.user, func(ctx context.Context, tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM twin.memberships WHERE user_id=$1`, a.user).Scan(&seen); err != nil {
				return err
			}
			close(inside)
			<-release
			return nil
		})
	}()
	select {
	case <-inside:
	case err := <-done:
		t.Fatalf("RunSelf ended early: %v", err)
	}
	wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	_, err := adm.Exec(wctx, `UPDATE twin.memberships SET role='editor' WHERE organization_id=$1 AND user_id=$2`, a.id, a.user)
	cancel()
	close(release)
	if err != nil {
		t.Errorf("a role change during RunSelf: %v (it waited for a lock RunSelf should not hold)", err)
	}
	if err := <-done; err != nil {
		t.Errorf("RunSelf: %v", err)
	}
	if seen != 1 {
		t.Errorf("RunSelf saw %d of the user's memberships, want 1", seen)
	}
}
