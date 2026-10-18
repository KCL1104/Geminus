package memberships_test

// Membership management against the disposable database as the plain API
// login (TEST-009 for memberships, TEST-011 for provisioning, D-036).

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/memberships"
)

const issuer = "https://test.invalid"

type env struct {
	svc   *memberships.Service
	db    *tenant.DB
	admin *pgxpool.Pool
	org   dbtest.Org
}

func setup(t *testing.T) *env {
	t.Helper()
	admin := dbtest.Admin(t)
	db := tenant.New(dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 8, true))
	return &env{svc: &memberships.Service{DB: db}, db: db, admin: admin,
		org: dbtest.NewOrg(t, admin, issuer, tenant.Owner, tenant.Editor, tenant.Viewer)}
}

func (e *env) scope(r tenant.Role) tenant.Scope {
	return tenant.Scope{OrganizationID: e.org.ID, UserID: e.org.Members[r].UserID}
}

func (e *env) as(user string) tenant.Scope {
	return tenant.Scope{OrganizationID: e.org.ID, UserID: user}
}

func requestID() string { return ids.New("req") }

func key() string { return "key-" + ids.New("k") }

func (e *env) add(t *testing.T, sc tenant.Scope, user string, role tenant.Role) (int, []byte, error) {
	t.Helper()
	body := fmt.Sprintf(`{"user_id":%q,"role":%q}`, user, role)
	return e.svc.Create(context.Background(), sc, requestID(), key(), []byte(body), memberships.CreateRequest{UserID: user, Role: role})
}

// role is user's role in the organization, "" for none, read as the table
// owner.
func (e *env) role(t *testing.T, user string) tenant.Role {
	t.Helper()
	var r string
	err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT role FROM twin.memberships WHERE organization_id=$1 AND user_id=$2`, e.org.ID, user).Scan(&r)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tenant.Role(r)
}

func (e *env) audit(t *testing.T) []string {
	t.Helper()
	var out []string
	err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT action||' '||target_id||' '||metadata::text FROM twin.audit_events WHERE organization_id=$1 AND actor_type='user' ORDER BY created_at, id`, e.org.ID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func matches(t *testing.T, def string, out []byte) {
	t.Helper()
	issues, err := validate.Document(out, "http.schema.json#/$defs/"+def, "SCHEMA")
	if err != nil || len(issues) > 0 {
		t.Errorf("%s does not match the contract: %v %v\n%s", def, err, issues, out)
	}
}

// Only an owner manages memberships; other members are forbidden and
// others are not members (TEST-009, SECURITY §2).
func TestRoles(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	outsider := dbtest.NewUser(t, e.admin, issuer)
	other := dbtest.NewOrg(t, e.admin, issuer, tenant.Owner)
	for _, c := range []struct {
		sc   tenant.Scope
		want error
	}{
		{e.scope(tenant.Editor), tenant.ErrForbidden},
		{e.scope(tenant.Viewer), tenant.ErrForbidden},
		{e.as(outsider.UserID), tenant.ErrNoMembership},
		{e.as(other.Members[tenant.Owner].UserID), tenant.ErrNoMembership}, // another organization's owner
	} {
		target := e.org.Members[tenant.Viewer].UserID
		if _, err := e.svc.List(ctx, c.sc, "", 10); !errors.Is(err, c.want) {
			t.Errorf("list as %s: %v", c.sc.UserID, err)
		}
		if _, _, err := e.add(t, c.sc, outsider.UserID, tenant.Viewer); !errors.Is(err, c.want) {
			t.Errorf("add as %s: %v", c.sc.UserID, err)
		}
		if _, err := e.svc.Update(ctx, c.sc, requestID(), target, memberships.UpdateRequest{Role: tenant.Editor}); !errors.Is(err, c.want) {
			t.Errorf("update as %s: %v", c.sc.UserID, err)
		}
		if err := e.svc.Delete(ctx, c.sc, requestID(), target); !errors.Is(err, c.want) {
			t.Errorf("delete as %s: %v", c.sc.UserID, err)
		}
	}
	if e.role(t, e.org.Members[tenant.Viewer].UserID) != tenant.Viewer || e.role(t, outsider.UserID) != "" || len(e.audit(t)) != 0 {
		t.Errorf("a refused call changed something: %v", e.audit(t))
	}
	// An owner of this organization cannot reach another's members.
	if _, err := e.svc.Update(ctx, e.scope(tenant.Owner), requestID(), other.Members[tenant.Owner].UserID, memberships.UpdateRequest{Role: tenant.Viewer}); !errors.Is(err, memberships.ErrNotFound) {
		t.Errorf("another organization's member: %v", err)
	}
}

// An owner adds a known user, who can work in the organization at once;
// the add is idempotent per key and audited.
func TestCreate(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	owner := e.scope(tenant.Owner)
	u := dbtest.NewUser(t, e.admin, issuer)
	body := fmt.Sprintf(`{"user_id":%q,"role":"editor"}`, u.UserID)
	k := key()
	status, out, err := e.svc.Create(ctx, owner, requestID(), k, []byte(body), memberships.CreateRequest{UserID: u.UserID, Role: tenant.Editor})
	if err != nil || status != 201 {
		t.Fatalf("create: %d %v", status, err)
	}
	matches(t, "Membership", out)
	var m memberships.Membership
	if err := json.Unmarshal(out, &m); err != nil || m.UserID != u.UserID || m.Role != tenant.Editor || m.OrganizationID != e.org.ID {
		t.Errorf("membership %+v %v", m, err)
	}
	if err := e.db.Run(ctx, e.as(u.UserID), tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		return m.Require(tenant.Writers...)
	}); err != nil {
		t.Errorf("the new member: %v", err)
	}
	// Replay: the same answer, nothing added twice.
	status2, out2, err := e.svc.Create(ctx, owner, requestID(), k, []byte(" "+body), memberships.CreateRequest{UserID: u.UserID, Role: tenant.Editor})
	if err != nil || status2 != 201 || string(out2) != string(out) {
		t.Errorf("replay: %d %v", status2, err)
	}
	other := fmt.Sprintf(`{"user_id":%q,"role":"viewer"}`, u.UserID)
	if _, _, err := e.svc.Create(ctx, owner, requestID(), k, []byte(other), memberships.CreateRequest{UserID: u.UserID, Role: tenant.Viewer}); !errors.Is(err, idempotency.ErrConflict) {
		t.Errorf("same key, other body: %v", err)
	}
	if _, _, err := e.add(t, owner, u.UserID, tenant.Viewer); !errors.Is(err, memberships.ErrExists) {
		t.Errorf("adding a member again: %v", err)
	}
	if _, _, err := e.add(t, owner, ids.New("usr"), tenant.Viewer); !errors.Is(err, memberships.ErrUnknownUser) {
		t.Errorf("an unknown user: %v", err)
	}
	if _, _, err := e.add(t, owner, "usr_x", tenant.Viewer); !errors.Is(err, memberships.ErrInvalidRequest) {
		t.Errorf("not a user ID: %v", err)
	}
	if _, _, err := e.add(t, owner, u.UserID, "admin"); !errors.Is(err, memberships.ErrInvalidRequest) {
		t.Errorf("not a role: %v", err)
	}
	if got := e.audit(t); !slices.Equal(got, []string{"membership.create " + u.UserID + ` {"role": "editor"}`}) {
		t.Errorf("audit: %v", got)
	}
}

// Role changes keep at least one owner (D-036); setting the same role is
// no change.
func TestUpdate(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	owner, editor := e.scope(tenant.Owner), e.org.Members[tenant.Editor].UserID
	if _, err := e.svc.Update(ctx, owner, requestID(), owner.UserID, memberships.UpdateRequest{Role: tenant.Editor}); !errors.Is(err, memberships.ErrLastOwner) {
		t.Errorf("demoting the only owner: %v", err)
	}
	m, err := e.svc.Update(ctx, owner, requestID(), editor, memberships.UpdateRequest{Role: tenant.Owner})
	if err != nil || m.Role != tenant.Owner {
		t.Fatalf("promoting: %+v %v", m, err)
	}
	matches(t, "Membership", mustJSON(t, m))
	if _, err := e.svc.Update(ctx, owner, requestID(), editor, memberships.UpdateRequest{Role: tenant.Owner}); err != nil {
		t.Errorf("the same role: %v", err)
	}
	// With two owners, one may step down; then the other is the last.
	if _, err := e.svc.Update(ctx, owner, requestID(), owner.UserID, memberships.UpdateRequest{Role: tenant.Viewer}); err != nil {
		t.Errorf("stepping down: %v", err)
	}
	if _, err := e.svc.Update(ctx, e.as(editor), requestID(), editor, memberships.UpdateRequest{Role: tenant.Editor}); !errors.Is(err, memberships.ErrLastOwner) {
		t.Errorf("the last owner stepping down: %v", err)
	}
	if _, err := e.svc.Update(ctx, e.as(editor), requestID(), ids.New("usr"), memberships.UpdateRequest{Role: tenant.Editor}); !errors.Is(err, memberships.ErrNotFound) {
		t.Errorf("not a member: %v", err)
	}
	if _, err := e.svc.Update(ctx, e.as(editor), requestID(), owner.UserID, memberships.UpdateRequest{Role: "admin"}); !errors.Is(err, memberships.ErrInvalidRequest) {
		t.Errorf("not a role: %v", err)
	}
	want := []string{
		"membership.update " + editor + ` {"role": "owner", "previous_role": "editor"}`,
		"membership.update " + owner.UserID + ` {"role": "viewer", "previous_role": "owner"}`,
	}
	if got := e.audit(t); !slices.Equal(got, want) {
		t.Errorf("audit: %v", got)
	}
}

// A removed member's next request is refused; the only owner cannot be
// removed (D-036).
func TestDelete(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	owner, viewer := e.scope(tenant.Owner), e.org.Members[tenant.Viewer].UserID
	if err := e.svc.Delete(ctx, owner, requestID(), owner.UserID); !errors.Is(err, memberships.ErrLastOwner) {
		t.Errorf("removing the only owner: %v", err)
	}
	if err := e.svc.Delete(ctx, owner, requestID(), viewer); err != nil {
		t.Fatal(err)
	}
	if err := e.db.Run(ctx, e.as(viewer), tenant.ReadOnly, func(context.Context, tenant.Tx, tenant.Membership) error { return nil }); !errors.Is(err, tenant.ErrNoMembership) {
		t.Errorf("the removed member's next request: %v", err)
	}
	if err := e.svc.Delete(ctx, owner, requestID(), viewer); !errors.Is(err, memberships.ErrNotFound) {
		t.Errorf("removing again: %v", err)
	}
	if got := e.audit(t); !slices.Equal(got, []string{"membership.delete " + viewer + ` {"role": "viewer"}`}) {
		t.Errorf("audit: %v", got)
	}
}

// A removal waits for a write the member already started, and then takes
// effect (ADR-019: a write share-locks its membership row).
func TestRemovalWaitsForAWrite(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	editor := e.org.Members[tenant.Editor].UserID
	started, release, wrote := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		wrote <- e.db.Run(ctx, e.as(editor), tenant.ReadWrite, func(context.Context, tenant.Tx, tenant.Membership) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	removed := make(chan error, 1)
	go func() { removed <- e.svc.Delete(ctx, e.scope(tenant.Owner), requestID(), editor) }()
	select {
	case err := <-removed:
		t.Fatalf("the removal did not wait for the write: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	close(release)
	if err := <-wrote; err != nil {
		t.Errorf("the write: %v", err)
	}
	if err := <-removed; err != nil {
		t.Errorf("the removal: %v", err)
	}
	if err := e.db.Run(ctx, e.as(editor), tenant.ReadWrite, func(context.Context, tenant.Tx, tenant.Membership) error { return nil }); !errors.Is(err, tenant.ErrNoMembership) {
		t.Errorf("the next write: %v", err)
	}
}

// Two owners demoting each other at once: one wins, the other is refused
// as the last owner, and neither deadlocks (T-009 re-review R1). An owner's
// own writes in between do not deadlock with membership changes either.
func TestOwnersChangeAtOnce(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a, b := e.org.Members[tenant.Owner].UserID, e.org.Members[tenant.Editor].UserID
	for round := range 15 {
		// Whoever is the owner makes the other one an owner too.
		x, y := a, b
		if e.role(t, a) != tenant.Owner {
			x, y = b, a
		}
		if _, err := e.svc.Update(ctx, e.as(x), requestID(), y, memberships.UpdateRequest{Role: tenant.Owner}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make([]error, 3)
		wg.Go(func() {
			_, errs[0] = e.svc.Update(ctx, e.as(a), requestID(), b, memberships.UpdateRequest{Role: tenant.Editor})
		})
		wg.Go(func() {
			_, errs[1] = e.svc.Update(ctx, e.as(b), requestID(), a, memberships.UpdateRequest{Role: tenant.Editor})
		})
		wg.Go(func() {
			errs[2] = e.db.Run(ctx, e.as(a), tenant.ReadWrite, func(context.Context, tenant.Tx, tenant.Membership) error {
				time.Sleep(20 * time.Millisecond)
				return nil
			})
		})
		wg.Wait()
		won := 0
		for i, err := range errs[:2] {
			switch {
			case err == nil:
				won++
			case errors.Is(err, memberships.ErrLastOwner), errors.Is(err, tenant.ErrForbidden):
				// refused: the other demoted this caller first, or the
				// caller is the last owner
			default:
				t.Errorf("round %d, owner %d: %v", round, i, err)
			}
		}
		if errs[2] != nil && !errors.Is(errs[2], tenant.ErrNoMembership) {
			t.Errorf("round %d, owner's write: %v", round, errs[2])
		}
		owners := 0
		for _, u := range []string{a, b} {
			if e.role(t, u) == tenant.Owner {
				owners++
			}
		}
		if won != 1 || owners != 1 {
			t.Fatalf("round %d: %d changes applied, %d owners left (%v)", round, won, owners, errs)
		}
	}
}

// Members page in user ID order, and a cursor only works for this list.
func TestList(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	owner := e.scope(tenant.Owner)
	var want []string
	for _, m := range e.org.Members {
		want = append(want, m.UserID)
	}
	slices.Sort(want)
	var got []string
	cursor := ""
	for {
		page, err := e.svc.List(ctx, owner, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		matches(t, "Memberships", mustJSON(t, page))
		for _, m := range page.Items {
			got = append(got, m.UserID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
		if _, err := e.svc.List(ctx, tenant.Scope{OrganizationID: ids.New("org"), UserID: owner.UserID}, cursor, 1); !errors.Is(err, memberships.ErrInvalidCursor) {
			t.Errorf("a cursor in another organization: %v", err)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("listed %v, want %v", got, want)
	}
	for _, c := range []string{"x", cursor + "A"} {
		if _, err := e.svc.List(ctx, owner, c, 10); !errors.Is(err, memberships.ErrInvalidCursor) {
			t.Errorf("cursor %q: %v", c, err)
		}
	}
	for _, limit := range []int{0, 101} {
		if _, err := e.svc.List(ctx, owner, "", limit); !errors.Is(err, memberships.ErrInvalidLimit) {
			t.Errorf("limit %d: %v", limit, err)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The routes are the contract's: path, method and x-required-roles of each
// operation equal planning/api-operations.json.
func TestRoutesMatchContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "planning", "api-operations.json"))
	if err != nil {
		t.Fatal(err)
	}
	type operation struct {
		Method      string   `json:"method"`
		Path        string   `json:"path"`
		OperationID string   `json:"operation_id"`
		Roles       []string `json:"roles"`
	}
	var doc struct {
		Operations []operation `json:"operations"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, r := range memberships.Routes {
		i := slices.IndexFunc(doc.Operations, func(o operation) bool { return o.OperationID == r.OperationID })
		if i < 0 {
			t.Errorf("%s is not a contract operation", r.OperationID)
			continue
		}
		var roles []string
		for _, x := range r.Roles {
			roles = append(roles, string(x))
		}
		if o := doc.Operations[i]; o.Method != r.Method || o.Path != r.Path || !slices.Equal(o.Roles, roles) {
			t.Errorf("%s: route %s %s %v, contract %s %s %v", r.OperationID, r.Method, r.Path, roles, o.Method, o.Path, o.Roles)
		}
	}
}

// A caller made an owner while waiting for the owner lock is not in the set
// the lock returned: RunOwners locks again instead of judging "the last
// owner" from an incomplete set (ADR-023).
func TestPromotedWhileWaiting(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a, c := e.org.Members[tenant.Owner].UserID, e.org.Members[tenant.Editor].UserID
	tx, err := e.admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM twin.memberships WHERE organization_id=$1 AND role='owner' ORDER BY user_id FOR UPDATE`, e.org.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE twin.memberships SET role='owner' WHERE organization_id=$1 AND user_id=$2`, e.org.ID, c); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.svc.Update(ctx, e.as(c), requestID(), a, memberships.UpdateRequest{Role: tenant.Editor})
		done <- err
	}()
	time.Sleep(500 * time.Millisecond)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Errorf("demoting the other owner after being made one: %v", err)
	}
	if e.role(t, a) != tenant.Editor || e.role(t, c) != tenant.Owner {
		t.Errorf("roles: a %s, c %s", e.role(t, a), e.role(t, c))
	}
}
