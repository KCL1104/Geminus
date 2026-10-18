package memberships_test

// Membership changes under concurrency, from the independent review of
// T-010 (round 1): refused requests take no lock, the owner set a change
// judges is current, many owners changing at once never deadlock, and a
// replay still needs the role.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/memberships"
)

// join makes user a member with role, as the table owner.
func (e *env) join(t *testing.T, user string, role tenant.Role) {
	t.Helper()
	err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,true)`, e.org.ID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO twin.memberships(organization_id,user_id,role) VALUES ($1,$2,$3)`, e.org.ID, user, string(role))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// userAt makes a user whose ID sorts by at (its ULID time).
func (e *env) userAt(t *testing.T, at time.Time) string {
	t.Helper()
	id, sub := "usr_"+ids.ULID(at), "sub-"+ids.New("x")
	err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.oidc_issuer',$1,true), set_config('app.oidc_subject',$2,true)`, issuer, sub); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO twin.users(id,oidc_issuer,oidc_subject) VALUES ($1,$2,$3)`, id, issuer, sub)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) ownerCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.admin.QueryRow(context.Background(), `SELECT count(*) FROM twin.memberships WHERE organization_id=$1 AND role='owner'`, e.org.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// holdWrite starts user's write (Run ReadWrite share-locks its membership
// row) and keeps it open until the returned func is called.
func (e *env) holdWrite(t *testing.T, user string) func() error {
	t.Helper()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- e.db.Run(context.Background(), e.as(user), tenant.ReadWrite, func(context.Context, tenant.Tx, tenant.Membership) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	var once sync.Once
	var err error
	stop := func() error {
		once.Do(func() { close(release); err = <-done })
		return err
	}
	t.Cleanup(func() { _ = stop() })
	return stop
}

// A request that is refused (not a member, or not an owner) takes no lock:
// it does not wait for an owner's write, and another owner's write does
// not wait for it (T-010 review M1).
func TestRefusedRequestsTakeNoLock(t *testing.T) {
	for _, who := range []string{"non-member", "viewer"} {
		t.Run(who, func(t *testing.T) {
			e := setup(t)
			ctx := context.Background()
			other := dbtest.NewUser(t, e.admin, issuer).UserID
			e.join(t, other, tenant.Owner)
			caller, want := dbtest.NewUser(t, e.admin, issuer).UserID, tenant.ErrNoMembership
			if who == "viewer" {
				caller, want = e.org.Members[tenant.Viewer].UserID, tenant.ErrForbidden
			}
			stop := e.holdWrite(t, other) // an owner in the middle of a write
			refused := make(chan error, 1)
			go func() {
				_, err := e.svc.Update(ctx, e.as(caller), requestID(), e.org.Members[tenant.Editor].UserID, memberships.UpdateRequest{Role: tenant.Viewer})
				refused <- err
			}()
			select {
			case err := <-refused:
				if !errors.Is(err, want) {
					t.Errorf("refused with %v, want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				t.Errorf("the refused request waited for an owner's write")
			}
			// The first owner can still write while the other's write is open.
			wrote := make(chan error, 1)
			go func() {
				wrote <- e.db.Run(ctx, e.scope(tenant.Owner), tenant.ReadWrite, func(context.Context, tenant.Tx, tenant.Membership) error { return nil })
			}()
			select {
			case err := <-wrote:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(2 * time.Second):
				t.Errorf("an owner's write waited for a refused request")
			}
			if err := stop(); err != nil {
				t.Error(err)
			}
		})
	}
}

// A change holds every owner membership FOR UPDATE until it ends (D-036):
// while one waits for its target's write, another owner's write waits for
// it, and a member's write that is neither does not (ADR-023).
func TestOwnerRowsLockedDuringAChange(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a, ed, v := e.org.Members[tenant.Owner].UserID, e.org.Members[tenant.Editor].UserID, e.org.Members[tenant.Viewer].UserID
	b := dbtest.NewUser(t, e.admin, issuer).UserID
	e.join(t, b, tenant.Owner)
	stop := e.holdWrite(t, ed)
	changed := make(chan error, 1)
	go func() {
		_, err := e.svc.Update(ctx, e.as(a), requestID(), ed, memberships.UpdateRequest{Role: tenant.Viewer})
		changed <- err
	}()
	time.Sleep(300 * time.Millisecond)
	write := func(user string) chan error {
		c := make(chan error, 1)
		go func() {
			c <- e.db.Run(ctx, e.as(user), tenant.ReadWrite, func(context.Context, tenant.Tx, tenant.Membership) error { return nil })
		}()
		return c
	}
	owner, viewer := write(b), write(v)
	select {
	case err := <-viewer:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		t.Error("a member who is neither an owner nor the target waited for the change")
	}
	select {
	case err := <-owner:
		t.Errorf("an owner's write did not wait for the change: %v", err)
		owner <- nil
	case <-time.After(300 * time.Millisecond):
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if err := <-changed; err != nil {
		t.Fatal(err)
	}
	if err := <-owner; err != nil {
		t.Error(err)
	}
}

// An owner made by a change that commits while another change waits is in
// the set that change judges: no false "last owner" (T-010 review M3).
func TestOwnerSetIsCurrent(t *testing.T) {
	for _, c := range []string{"demote self", "demote the new owner", "remove the new owner"} {
		t.Run(c, func(t *testing.T) {
			e := setup(t)
			ctx := context.Background()
			a, ed := e.org.Members[tenant.Owner].UserID, e.org.Members[tenant.Editor].UserID
			stop := e.holdWrite(t, ed) // the promotion waits for the editor's write
			promoted := make(chan error, 1)
			go func() {
				_, err := e.svc.Update(ctx, e.as(a), requestID(), ed, memberships.UpdateRequest{Role: tenant.Owner})
				promoted <- err
			}()
			time.Sleep(300 * time.Millisecond)
			second := make(chan error, 1)
			go func() {
				var err error
				switch c {
				case "demote self":
					_, err = e.svc.Update(ctx, e.as(a), requestID(), a, memberships.UpdateRequest{Role: tenant.Editor})
				case "demote the new owner":
					_, err = e.svc.Update(ctx, e.as(a), requestID(), ed, memberships.UpdateRequest{Role: tenant.Viewer})
				case "remove the new owner":
					err = e.svc.Delete(ctx, e.as(a), requestID(), ed)
				}
				second <- err
			}()
			time.Sleep(300 * time.Millisecond)
			if err := stop(); err != nil {
				t.Fatal(err)
			}
			if err := <-promoted; err != nil {
				t.Fatalf("promotion: %v", err)
			}
			if err := <-second; err != nil {
				t.Errorf("%s after the promotion: %v", c, err)
			}
			if e.ownerCount(t) != 1 {
				t.Errorf("%d owners", e.ownerCount(t))
			}
		})
	}
}

// Three owners and more members change memberships at random while members
// write and owners list: no deadlock or other unexpected error, and never
// an organization without an owner (T-010 review M2: two owners alone did
// not show the cycles).
func TestMembershipChangesUnderLoad(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	users := []string{e.org.Members[tenant.Owner].UserID, e.org.Members[tenant.Editor].UserID, e.org.Members[tenant.Viewer].UserID}
	for range 3 {
		u := dbtest.NewUser(t, e.admin, issuer).UserID
		e.join(t, u, tenant.Editor)
		users = append(users, u)
	}
	for _, u := range users[1:3] {
		if _, err := e.svc.Update(ctx, e.as(users[0]), requestID(), u, memberships.UpdateRequest{Role: tenant.Owner}); err != nil {
			t.Fatal(err)
		}
	}
	var stop atomic.Bool
	var zero, unexpected, changed atomic.Int64
	var firstUnexpected atomic.Pointer[string]
	var wg sync.WaitGroup
	wg.Go(func() {
		for !stop.Load() {
			var n int
			if err := e.admin.QueryRow(ctx, `SELECT count(*) FROM twin.memberships WHERE organization_id=$1 AND role='owner'`, e.org.ID).Scan(&n); err == nil && n == 0 {
				zero.Add(1)
			}
			time.Sleep(2 * time.Millisecond)
		}
	})
	roles := []tenant.Role{tenant.Owner, tenant.Editor, tenant.Viewer}
	deadline := time.Now().Add(5 * time.Second)
	for w := range 10 {
		wg.Go(func() {
			r := rand.New(rand.NewPCG(uint64(w), 7))
			for time.Now().Before(deadline) {
				caller, target := users[r.IntN(len(users))], users[r.IntN(len(users))]
				var err error
				op := r.IntN(6)
				switch op {
				case 0, 1:
					_, err = e.svc.Update(ctx, e.as(caller), requestID(), target, memberships.UpdateRequest{Role: roles[r.IntN(3)]})
				case 2:
					err = e.svc.Delete(ctx, e.as(caller), requestID(), target)
				case 3:
					_, _, err = e.add(t, e.as(caller), target, roles[r.IntN(3)])
				case 4:
					err = e.db.Run(ctx, e.as(caller), tenant.ReadWrite, func(context.Context, tenant.Tx, tenant.Membership) error {
						time.Sleep(time.Duration(r.IntN(5)) * time.Millisecond)
						return nil
					})
				case 5:
					_, err = e.svc.List(ctx, e.as(caller), "", 100)
				}
				if err == nil && op <= 3 {
					changed.Add(1)
				}
				if err != nil && !errors.Is(err, memberships.ErrLastOwner) && !errors.Is(err, tenant.ErrForbidden) && !errors.Is(err, tenant.ErrNoMembership) &&
					!errors.Is(err, memberships.ErrNotFound) && !errors.Is(err, memberships.ErrExists) {
					if unexpected.Add(1) == 1 {
						s := err.Error()
						firstUnexpected.Store(&s)
					}
				}
			}
		})
	}
	time.Sleep(time.Until(deadline) + 200*time.Millisecond)
	stop.Store(true)
	wg.Wait()
	t.Logf("%d membership changes succeeded", changed.Load())
	if changed.Load() < 50 {
		t.Errorf("only %d membership changes succeeded: the load did not exercise them", changed.Load())
	}
	if n := unexpected.Load(); n > 0 {
		t.Errorf("%d unexpected errors, the first: %s", n, *firstUnexpected.Load())
	}
	if zero.Load() > 0 || e.ownerCount(t) < 1 {
		t.Errorf("an organization without an owner (%d observations)", zero.Load())
	}
}

// Two changes whose owner locks use different plans (a sequential scan
// returns rows in heap order, here not user_id order) while both owners are
// writing neither deadlock nor fail (T-010 review L1, from the reviewer's
// probe).
func TestOwnerLockAcrossPlans(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	opts := func(o string) *memberships.Service {
		return &memberships.Service{DB: tenant.New(dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin)+"&options="+o, 4, true))}
	}
	seq := opts("-c%20enable_indexscan%3Doff%20-c%20enable_bitmapscan%3Doff%20-c%20enable_indexonlyscan%3Doff")
	idx := opts("-c%20enable_seqscan%3Doff%20-c%20enable_bitmapscan%3Doff")
	for round := range 3 {
		base := time.Now().Add(-time.Hour)
		a, b, v := e.userAt(t, base), e.userAt(t, base.Add(time.Minute)), e.userAt(t, base.Add(2*time.Minute))
		o := &env{svc: e.svc, db: e.db, admin: e.admin, org: dbtest.NewOrg(t, e.admin, issuer)}
		o.join(t, b, tenant.Owner) // b first in the heap, a first by user_id
		o.join(t, a, tenant.Owner)
		o.join(t, v, tenant.Viewer)
		stopA, stopB := o.holdWrite(t, a), o.holdWrite(t, b)
		r1, r2 := make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := seq.Update(ctx, o.as(a), requestID(), v, memberships.UpdateRequest{Role: tenant.Editor})
			r1 <- err
		}()
		time.Sleep(100 * time.Millisecond)
		go func() {
			_, err := idx.Update(ctx, o.as(b), requestID(), v, memberships.UpdateRequest{Role: tenant.Viewer})
			r2 <- err
		}()
		time.Sleep(100 * time.Millisecond)
		if err := stopB(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		if err := stopA(); err != nil {
			t.Fatal(err)
		}
		if err1, err2 := <-r1, <-r2; err1 != nil || err2 != nil {
			t.Errorf("round %d: sequential-scan change %v, index-scan change %v", round, err1, err2)
		}
	}
}

// A replay of an add still needs the role: a demoted or removed owner gets
// no replay (D-011; T-010 review L2).
func TestReplayNeedsTheRole(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a, b := e.org.Members[tenant.Owner].UserID, e.org.Members[tenant.Editor].UserID
	if _, err := e.svc.Update(ctx, e.as(a), requestID(), b, memberships.UpdateRequest{Role: tenant.Owner}); err != nil {
		t.Fatal(err)
	}
	u := dbtest.NewUser(t, e.admin, issuer)
	body := fmt.Sprintf(`{"user_id":%q,"role":"viewer"}`, u.UserID)
	k, req := key(), memberships.CreateRequest{UserID: u.UserID, Role: tenant.Viewer}
	if status, _, err := e.svc.Create(ctx, e.as(a), requestID(), k, []byte(body), req); err != nil || status != 201 {
		t.Fatalf("add: %d %v", status, err)
	}
	if _, err := e.svc.Update(ctx, e.as(b), requestID(), a, memberships.UpdateRequest{Role: tenant.Editor}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Create(ctx, e.as(a), requestID(), k, []byte(body), req); !errors.Is(err, tenant.ErrForbidden) {
		t.Errorf("a replay by a demoted owner: %v", err)
	}
	if err := e.svc.Delete(ctx, e.as(b), requestID(), a); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Create(ctx, e.as(a), requestID(), k, []byte(body), req); !errors.Is(err, tenant.ErrNoMembership) {
		t.Errorf("a replay by a removed owner: %v", err)
	}
}
