package analyzer

// What reaches the worker from the analyst's graph (checks/E2_VALIDATION.md,
// review round 1): the job's correlation on the call's log lines, and a
// panic with the stack where it was raised.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

const tJob = "job_01M4YDQK00FGZZWH0PP15XPNFE"

// unsettled reserves but cannot settle.
type unsettled struct{ m *provider.MemoryMeter }

func (u unsettled) Reserve(ctx context.Context, c provider.Charge) (provider.Reservation, error) {
	return u.m.Reserve(ctx, c)
}

func (unsettled) Settle(context.Context, provider.Reservation, provider.Settlement) error {
	return errors.New("the ledger is down")
}

// Finding 2: the worker's job correlation (observability.With on the
// attempt's context, D-093 (b)) is on every line the call writes, through
// the process's logging handler: the call that answered, its unsettled
// reservation and a call that failed.
func TestTheCallLinesCarryTheJobsCorrelation(t *testing.T) {
	for name, c := range map[string]struct {
		backend func(*fake.Backend) (*provider.Client, error)
		lines   []string
	}{
		"answered, its spend unsettled": {func(f *fake.Backend) (*provider.Client, error) {
			return provider.NewMetered(billed{f}, provider.DefaultBudget(), unsettled{provider.NewMemoryMeter(1_000_000_000_000)})
		}, []string{"ai provider call ended", "settling a live AI spend reservation failed"}},
		"failed": {func(f *fake.Backend) (*provider.Client, error) {
			f.Fail(provider.RoleAnalyst, tQuestion, provider.NewError(provider.CodeRefused, 0, ""))
			return provider.New(f, provider.DefaultBudget())
		}, []string{"ai provider call ended"}},
	} {
		t.Run(name, func(t *testing.T) {
			client, err := c.backend(fake.New())
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			reg, err := domainpack.NewRegistry(softwaredelivery.Pack{})
			if err != nil {
				t.Fatal(err)
			}
			h, err := New(Config{Provider: client, Packs: reg, Actor: "test-worker", Log: slog.New(observability.NewHandler(slog.NewJSONHandler(&buf, nil)))})
			if err != nil {
				t.Fatal(err)
			}
			ctx := observability.With(context.Background(), slog.String("organization_id", tOrg), slog.String("job_id", tJob))
			h.analyze(ctx, tOrg, fixture(t), tAttr)
			var got []string
			for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
				for _, msg := range c.lines {
					if strings.Contains(line, `"msg":"`+msg+`"`) {
						got = append(got, msg)
						if !strings.Contains(line, `"job_id":"`+tJob+`"`) || !strings.Contains(line, `"organization_id":"`+tOrg+`"`) {
							t.Errorf("%q has lost the job's correlation: %s", msg, line)
						}
					}
				}
			}
			if strings.Join(got, "|") != strings.Join(c.lines, "|") {
				t.Errorf("lines %q, want %q:\n%s", got, c.lines, buf.String())
			}
		})
	}
}

type panickingPacks struct{}

func (panickingPacks) Lookup(string) (domainpack.DomainPack, error) { panic("cell CASE-SENTINEL") }

// Finding 1: a panic in the graph leaves analyze as a handler.Reraised (the
// worker's contract, internal/worker) with the value it was raised with
// and a stack that names the frame that panicked, which Eino's recovery
// unwound; nothing is asked of the provider.
func TestAPanicReachesTheWorkerWithItsStack(t *testing.T) {
	f := fake.New()
	client, err := provider.New(f, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	h := setup(t, client, panickingPacks{}, provider.Budget{}, &buf)
	defer func() {
		v := recover()
		r, ok := v.(handler.Reraised)
		if !ok {
			t.Fatalf("recovered %#v, not a handler.Reraised", v)
		}
		if r.PanicValue() != "cell CASE-SENTINEL" || !strings.Contains(r.PanicStack(), "analyzer.panickingPacks.Lookup(") {
			t.Errorf("recovered %#v with the stack\n%s", r.PanicValue(), r.PanicStack())
		}
		if len(f.Requests()) != 0 || buf.Len() != 0 {
			t.Errorf("%d requests; logged %s", len(f.Requests()), buf.String())
		}
	}()
	h.analyze(context.Background(), tOrg, fixture(t), tAttr)
	t.Error("no panic")
}
