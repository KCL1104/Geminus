package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
)

type fixedCredit struct {
	step  int64
	calls int
}

func (c *fixedCredit) Grant(_ context.Context, granted int64) (int64, error) {
	c.calls++
	return granted + c.step, nil
}

// A credit budget spends exactly what is granted: a replication of 100
// events waits for the grants it needs and no more, and without a credit
// it ends where the grant ends, as if that were the budget (T-023, D-007).
func TestCreditBudget(t *testing.T) {
	in := input(golden(t, "deterministic-queue.json"))
	in.Config = config(1010, model.Minute)
	p := compile(t, in)

	c := &fixedCredit{step: 30}
	b := NewCreditBudget(1000, 10, c)
	if _, err := arrivals(t, p, 0, b); err != nil {
		t.Fatalf("100 events granted 10 then 30 at a time: %v", err)
	}
	if c.calls != 3 || b.Granted() != 100 || b.Used() != 100 {
		t.Errorf("%d grants, granted %d, used %d; want 3, 100, 100", c.calls, b.Granted(), b.Used())
	}

	b = NewCreditBudget(1000, 40, nil)
	if _, err := arrivals(t, p, 0, b); !errors.Is(err, ErrEventBudget) || b.Granted() != 40 {
		t.Errorf("no credit after 40: %v, granted %d", err, b.Granted())
	}
	if b := NewCreditBudget(50, 80, c); b.Granted() != 50 {
		t.Errorf("a first grant above the budget is the budget: %d", b.Granted())
	}
	if b := NewCreditBudget(50, -1, c); b.Granted() != 0 {
		t.Errorf("a negative first grant is none: %d", b.Granted())
	}
	if b := NewBudget(75); b.Granted() != 75 {
		t.Errorf("a plain budget is granted at once: %d", b.Granted())
	}
}
