package comparisons

// DB-backed test of the comparison's transaction (T-029 review round 1 L6):
// the service reads both runs and both results in one read-only REPEATABLE
// READ transaction (D-089 (a)), observed from inside that transaction on a
// disposable database as the plain API login. Without
// GEMINUS_TEST_DATABASE_URL it skips (NOT RUN).

import (
	"context"
	"errors"
	"testing"

	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/tenant"
)

func TestCompareReadsOneReadOnlySnapshot(t *testing.T) {
	admin := dbtest.Admin(t)
	org := dbtest.NewOrg(t, admin, "https://test.invalid", tenant.Viewer)
	api := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 2, true)
	if err := tenant.CheckRuntimeRole(context.Background(), api, tenant.APIRole); err != nil {
		t.Fatal(err)
	}
	var isolation, readOnly string
	calls := 0
	// Never t.Parallel: inTransaction is package state (its comment).
	inTransaction = func(ctx context.Context, tx tenant.Tx) error {
		calls++
		return tx.QueryRow(ctx, `SELECT current_setting('transaction_isolation'), current_setting('transaction_read_only')`).Scan(&isolation, &readOnly)
	}
	t.Cleanup(func() { inTransaction = nil })
	s := &Service{DB: tenant.New(api)}
	sc := tenant.Scope{OrganizationID: org.ID, UserID: org.Members[tenant.Viewer].UserID}
	// No such runs: the comparison still opens its transaction, checks the
	// role, and refuses the baseline (404) after the hook has looked.
	_, err := s.Compare(context.Background(), sc, "sim_01M4YDQK00FGZZWH0PP15XPNFA", "sim_01M4YDQK00FGZZWH0PP15XPNFB")
	var re *RunError
	if !errors.As(err, &re) || re.Param != BaselineParam || !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v, want the baseline not found", err)
	}
	if calls != 1 || isolation != "repeatable read" || readOnly != "on" {
		t.Errorf("the comparison's transaction: %d call(s), isolation %q, read only %q; want one, repeatable read, on", calls, isolation, readOnly)
	}
}
