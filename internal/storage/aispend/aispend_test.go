package aispend_test

// DB-backed tests of the spend ledger (T-053; migration 0016) as the plain
// worker login through the worker scope. Without GEMINUS_TEST_DATABASE_URL
// they skip (NOT RUN); make db-go-test sets GEMINUS_REQUIRE_DB_TESTS=1, so
// there a skip is a failure. Every test works on UTC days of its own, far
// from today, and compares totals with what they held when it began, so
// the tests do not see each other's (or an earlier run's) amounts.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/aispend"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
)

const issuer = "https://test.invalid"

type env struct {
	admin      *pgxpool.Pool
	worker     *workerscope.Worker
	a, b       dbtest.Org
	impA, impB string
}

func setup(t *testing.T) *env {
	t.Helper()
	admin := dbtest.Admin(t)
	e := &env{admin: admin, a: dbtest.NewOrg(t, admin, issuer, tenant.Owner), b: dbtest.NewOrg(t, admin, issuer, tenant.Owner)}
	wp := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.WorkerLogin), 16, true)
	if err := tenant.CheckRuntimeRole(context.Background(), wp, tenant.WorkerRole); err != nil {
		t.Fatal(err)
	}
	w, err := workerscope.New(wp, []string{e.a.ID, e.b.ID})
	if err != nil {
		t.Fatal(err)
	}
	e.worker = w
	e.impA, e.impB = dbtest.NewImport(t, admin, e.a), dbtest.NewImport(t, admin, e.b)
	return e
}

func (e *env) run(ctx context.Context, org string, fn func(ctx context.Context, tx tenant.Tx) error) error {
	return e.worker.Run(ctx, org, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error { return fn(ctx, tx) })
}

// meter is a worker meter whose reservations go to at's UTC day (a day of
// the test's own; aispend.PinDay).
func (e *env) meter(t *testing.T, l spend.Limits, at time.Time) *aispend.Meter {
	t.Helper()
	m, err := aispend.New(aispend.Config{Run: e.run, Actor: "worker-t053", Limits: l})
	if err != nil {
		t.Fatal(err)
	}
	return aispend.PinDay(m, at)
}

func (e *env) totals(t *testing.T, org string, at time.Time) aispend.Totals {
	t.Helper()
	var out aispend.Totals
	if err := e.run(context.Background(), org, func(ctx context.Context, tx tenant.Tx) error {
		var err error
		out, err = aispend.TotalsAt(ctx, tx, org, at)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func charge(org, imp string, n int64) provider.Charge {
	return provider.Charge{OrganizationID: org, Role: provider.RoleModeler, Model: "claude-opus-5-5", NanoUSD: n,
		Attribution: provider.Attribution{TargetType: "import", TargetID: imp, RequestID: ids.New("req")}}
}

// roomy are limits no test passes unless it means to.
var roomy = spend.Limits{Month: 1 << 50, Day: 1 << 49, OrganizationDay: 1 << 48}

// events counts org's audit events of action on target.
func (e *env) events(t *testing.T, org, action, target string) int {
	t.Helper()
	var n int
	if err := e.admin.QueryRow(context.Background(), `SELECT count(*) FROM twin.audit_events WHERE organization_id = $1 AND action = $2 AND target_id = $3`,
		org, action, target).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Reserve moves the three totals by the charge; Settle moves them by what
// the call cost minus the charge (a release when it cost nothing); each
// write is audited; the reservation keeps the billed tokens.
func TestReserveAndSettle(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	at := time.Date(2201, 3, 14, 23, 59, 0, 0, time.UTC)
	m := e.meter(t, roomy, at)
	before := e.totals(t, e.a.ID, at)
	r, err := m.Reserve(ctx, charge(e.a.ID, e.impA, 1000))
	if err != nil {
		t.Fatal(err)
	}
	if got := e.totals(t, e.a.ID, at); got != (aispend.Totals{Month: before.Month + 1000, Day: before.Day + 1000, OrganizationDay: before.OrganizationDay + 1000}) {
		t.Fatalf("after reserving: %+v (before %+v)", got, before)
	}
	usage := provider.Usage{InputTokens: 10, OutputTokens: 5, CacheReadInputTokens: 2}
	if err := m.Settle(ctx, r, provider.Settlement{NanoUSD: 300, Usage: usage, Outcome: provider.SpendUsage, Credential: "org-b"}); err != nil {
		t.Fatal(err)
	}
	if got := e.totals(t, e.a.ID, at); got != (aispend.Totals{Month: before.Month + 300, Day: before.Day + 300, OrganizationDay: before.OrganizationDay + 300}) {
		t.Fatalf("after settling: %+v", got)
	}
	var state, outcome, label string
	var settled, in, out, cr int64
	if err := e.admin.QueryRow(ctx, `SELECT state, outcome, settled_nano_usd, input_tokens, output_tokens, cache_read_tokens, credential_label
		FROM twin.ai_spend_reservations WHERE id = $1`, r.ID).Scan(&state, &outcome, &settled, &in, &out, &cr, &label); err != nil {
		t.Fatal(err)
	}
	if state != "settled" || outcome != "usage" || settled != 300 || in != 10 || out != 5 || cr != 2 || label != "org-b" {
		t.Errorf("reservation %s %s %d %d %d %d %s", state, outcome, settled, in, out, cr, label)
	}
	// A release: nothing billed, the charge comes back.
	r2, err := m.Reserve(ctx, charge(e.a.ID, e.impA, 700))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Settle(ctx, r2, provider.Settlement{Outcome: provider.SpendNone}); err != nil {
		t.Fatal(err)
	}
	if got := e.totals(t, e.a.ID, at); got.OrganizationDay != before.OrganizationDay+300 || got.Month != before.Month+300 {
		t.Errorf("after the release: %+v", got)
	}
	if e.events(t, e.a.ID, "ai_spend.reserve", e.impA) != 2 || e.events(t, e.a.ID, "ai_spend.settle", e.impA) != 2 {
		t.Error("the writes are not audited")
	}
	// Settled once.
	if err := m.Settle(ctx, r, provider.Settlement{NanoUSD: 0, Outcome: provider.SpendNone}); !errors.Is(err, aispend.ErrSettled) {
		t.Errorf("settled twice: %v", err)
	}
	if got := e.totals(t, e.a.ID, at); got.OrganizationDay != before.OrganizationDay+300 {
		t.Errorf("a second settlement changed the totals: %+v", got)
	}
	// The other organization's day is not this one's.
	if got := e.totals(t, e.b.ID, at); got.OrganizationDay != 0 || got.Month != before.Month+300 {
		t.Errorf("B: %+v", got)
	}
}

// Each limit refuses a charge it cannot hold, before any call, with the
// limit's name; a refused charge changes no total and is audited.
func TestLimitsRefuse(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	at := time.Date(2202, 6, 1, 0, 0, 0, 0, time.UTC)
	base := e.totals(t, e.a.ID, at)
	refused := func(err error, limit string) bool {
		var pe *provider.Error
		return errors.As(err, &pe) && pe.Code == provider.CodeQuotaExceeded && pe.Limit == limit
	}
	// An organization's day.
	m := e.meter(t, spend.Limits{Month: base.Month + 1_000_000, Day: base.Day + 1_000_000, OrganizationDay: 1000}, at)
	if _, err := m.Reserve(ctx, charge(e.a.ID, e.impA, 600)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Reserve(ctx, charge(e.a.ID, e.impA, 600)); !refused(err, provider.LimitOrganizationDay) {
		t.Errorf("organization day: %v", err)
	}
	if _, err := m.Reserve(ctx, charge(e.a.ID, e.impA, 1001)); !refused(err, provider.LimitOrganizationDay) {
		t.Errorf("a charge above the limit: %v", err)
	}
	if got := e.totals(t, e.a.ID, at); got.OrganizationDay != 600 || got.Day != base.Day+600 || got.Month != base.Month+600 {
		t.Errorf("a refusal moved the totals: %+v", got)
	}
	if e.events(t, e.a.ID, "ai_spend.refuse", e.impA) != 2 {
		t.Error("the refusals are not audited")
	}
	// The deployment's day, which B's charges reach too.
	m = e.meter(t, spend.Limits{Month: base.Month + 1_000_000, Day: base.Day + 1000, OrganizationDay: 1000}, at)
	if _, err := m.Reserve(ctx, charge(e.b.ID, e.impB, 400)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Reserve(ctx, charge(e.b.ID, e.impB, 1)); !refused(err, provider.LimitDay) {
		t.Errorf("deployment day: %v", err)
	}
	// The month, across days.
	next := at.Add(24 * time.Hour)
	m = e.meter(t, spend.Limits{Month: base.Month + 1500, Day: base.Month + 1500, OrganizationDay: base.Month + 1500}, next)
	if _, err := m.Reserve(ctx, charge(e.a.ID, e.impA, 500)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Reserve(ctx, charge(e.a.ID, e.impA, 1)); !refused(err, provider.LimitMonth) {
		t.Errorf("month: %v", err)
	}
}

// Concurrent reservations from two meters on two pools (two worker
// processes) never take a limit past its value: exactly as many are
// granted as the limit holds, and the totals are exactly theirs.
func TestConcurrentReservationsNeverOvershoot(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	at := time.Date(2203, 9, 9, 12, 0, 0, 0, time.UTC)
	base := e.totals(t, e.a.ID, at)
	l := spend.Limits{Month: base.Month + 1<<40, Day: base.Day + 1000, OrganizationDay: 700}
	// Another pool and scope on the same database: a second process.
	w2, err := workerscope.New(dbtest.Pool(t, dbtest.LoginURL(t, dbtest.WorkerLogin), 16, true), []string{e.a.ID, e.b.ID})
	if err != nil {
		t.Fatal(err)
	}
	second := &env{admin: e.admin, worker: w2, a: e.a, b: e.b, impA: e.impA, impB: e.impB}
	meters := []*aispend.Meter{e.meter(t, l, at), second.meter(t, l, at)}
	var wg sync.WaitGroup
	var grantedA, grantedB, refusedN atomic.Int64
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			org, imp, n := e.a.ID, e.impA, &grantedA
			if i%2 == 1 {
				org, imp, n = e.b.ID, e.impB, &grantedB
			}
			_, err := meters[i%2].Reserve(ctx, charge(org, imp, 100))
			var pe *provider.Error
			switch {
			case err == nil:
				n.Add(100)
			case errors.As(err, &pe) && pe.Code == provider.CodeQuotaExceeded:
				refusedN.Add(1)
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	a, b := e.totals(t, e.a.ID, at), e.totals(t, e.b.ID, at)
	if grantedA.Load()+grantedB.Load() != 1000 || refusedN.Load() != 30 || grantedA.Load() > 700 || grantedB.Load() > 700 {
		t.Errorf("granted A %d B %d, refused %d", grantedA.Load(), grantedB.Load(), refusedN.Load())
	}
	if a.Day != base.Day+1000 || a.OrganizationDay != grantedA.Load() || b.OrganizationDay != grantedB.Load() {
		t.Errorf("totals A %+v B %+v", a, b)
	}
}

// A settlement is its organization's: another's reservation is not found,
// and the reservation's own day and month move, whenever it is settled.
func TestSettleKeepsItsDayAndOrganization(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	at := time.Date(2204, 1, 31, 23, 59, 59, 0, time.UTC)
	m := e.meter(t, roomy, at)
	base := e.totals(t, e.a.ID, at)
	r, err := m.Reserve(ctx, charge(e.a.ID, e.impA, 2000))
	if err != nil {
		t.Fatal(err)
	}
	stolen := r
	stolen.Charge.OrganizationID = e.b.ID
	if err := m.Settle(ctx, stolen, provider.Settlement{Outcome: provider.SpendNone}); !errors.Is(err, aispend.ErrNotFound) {
		t.Errorf("another organization settled it: %v", err)
	}
	later := e.meter(t, roomy, at.Add(2*time.Hour)) // February
	nextBase := e.totals(t, e.a.ID, at.Add(2*time.Hour))
	if err := later.Settle(ctx, r, provider.Settlement{NanoUSD: 5, Outcome: provider.SpendUnknown, UnknownAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	if got := e.totals(t, e.a.ID, at); got.Month != base.Month+5 || got.OrganizationDay != base.OrganizationDay+5 {
		t.Errorf("January: %+v", got)
	}
	if got := e.totals(t, e.a.ID, at.Add(2*time.Hour)); got != nextBase {
		t.Errorf("February moved: %+v, was %+v", got, nextBase)
	}
	for name, s := range map[string]provider.Settlement{
		"negative":    {NanoUSD: -1, Outcome: provider.SpendUsage},
		"no outcome":  {NanoUSD: 1},
		"not a label": {NanoUSD: 1, Outcome: provider.SpendUsage, Credential: "Org A"},
	} {
		if err := m.Settle(ctx, provider.Reservation{ID: ids.New("asr"), Charge: r.Charge}, s); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A write whose audit event cannot be written commits nothing (D-091 fail
// closed); a charge for another organization's target is refused by the
// database; neither moves a total.
func TestFailClosed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	at := time.Date(2205, 5, 5, 5, 5, 5, 0, time.UTC)
	m := e.meter(t, roomy, at)
	base := e.totals(t, e.a.ID, at)
	stop := dbtest.RefuseAudit(t, e.admin, e.a.ID)
	_, err := m.Reserve(ctx, charge(e.a.ID, e.impA, 100))
	var pe *provider.Error
	if err == nil || errors.As(err, &pe) {
		t.Errorf("reserved without its audit event: %v", err)
	}
	stop()
	if _, err := m.Reserve(ctx, charge(e.a.ID, e.impB, 100)); err == nil {
		t.Error("a charge for another organization's import was reserved")
	}
	bad := charge(e.a.ID, e.impA, 100)
	bad.Attribution.TargetType = "simulation"
	if _, err := m.Reserve(ctx, bad); err == nil {
		t.Error("a target the ledger keeps no column for")
	}
	if got := e.totals(t, e.a.ID, at); got != base {
		t.Errorf("totals moved: %+v, was %+v", got, base)
	}
}
