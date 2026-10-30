package provider

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/spend"
)

// H-049: a person chooses the model of a proposal (Muse Spark 1.3, the
// default, or Muse Wonder 1.0). At the provider boundary: a request that
// names a model goes only to a backend that can ask it and may for the role,
// is reserved at that model's price, is answered only by that model, and a
// slow model's call has the time a research call has, never more than D-007's
// job timeout.

const (
	spark  = "muse-spark-1.3"
	wonder = "muse-wonder-1.0"
)

// chooser is a test backend that may ask the models it names for the
// proposal roles.
type chooser struct {
	backendFunc
	models []string
}

func (c chooser) AcceptsModel(role Role, model string) bool {
	return (role == RoleModeler || role == RoleScenario) && slices.Contains(c.models, model)
}

func askModel(c *Client, p Prompt, model string) (ProposalResult, error) {
	mode := ModeEdit
	if p.Role == RoleScenario {
		mode = ModeScenario
	}
	return c.GenerateModelProposal(context.Background(), ProposalContext{OrganizationID: orgA, Mode: mode, Prompt: p, Model: model,
		Attribution: attribution})
}

// A request that names a model goes only to a backend that can ask another
// model than its own and may ask that one for the role: any other is refused
// before the backend is asked (model_not_approved, never retried), and never
// answered by the backend's own model instead. A request that names none is
// the backend's own model, as before.
func TestAModelGoesOnlyToABackendThatMay(t *testing.T) {
	called := 0
	var got []string
	b := backendFunc(func(_ context.Context, req Request) (Response, error) {
		called++
		got = append(got, req.Model)
		return Response{Text: []byte(validProposal), Model: "test-model"}, nil
	})
	if _, err := askModel(client(t, b), prompt(t, RoleModeler), wonder); CodeOf(err) != CodeModelNotApproved {
		t.Errorf("a backend that asks its own model only: %v", err)
	}
	only := chooser{backendFunc: b, models: []string{spark}}
	if _, err := askModel(client(t, only), prompt(t, RoleModeler), wonder); CodeOf(err) != CodeModelNotApproved {
		t.Errorf("a model the backend may not ask: %v", err)
	}
	both := chooser{backendFunc: b, models: []string{spark, wonder}}
	if _, err := askModel(client(t, both), prompt(t, RoleScenario), "muse-spark-1.3-contributor"); CodeOf(err) != CodeModelNotApproved {
		t.Errorf("a Contributor model: %v", err)
	}
	if _, err := askModel(client(t, Disabled{}), prompt(t, RoleModeler), wonder); CodeOf(err) != CodeNotConfigured {
		t.Errorf("disabled: %v", err)
	}
	if called != 0 {
		t.Fatalf("the backend was asked %d times", called)
	}
	if NewError(CodeModelNotApproved, 0, "").Retryable() {
		t.Error("a model the backend may not ask is retried")
	}
	for _, role := range []Role{RoleModeler, RoleScenario} {
		if _, err := askModel(client(t, both), prompt(t, role), wonder); err != nil {
			t.Fatalf("%s on %s: %v", role, wonder, err)
		}
	}
	if _, err := askModel(client(t, b), prompt(t, RoleModeler), ""); err != nil {
		t.Fatalf("no model named: %v", err)
	}
	if !slices.Equal(got, []string{wonder, wonder, ""}) {
		t.Errorf("the backend was asked for %q", got)
	}
	c := client(t, both)
	if !c.AcceptsModel(RoleModeler, wonder) || !c.AcceptsModel(RoleScenario, spark) || c.AcceptsModel(RoleAnalyst, wonder) ||
		c.AcceptsModel(RoleModeler, "other") || client(t, b).AcceptsModel(RoleModeler, spark) || client(t, Disabled{}).AcceptsModel(RoleModeler, spark) {
		t.Error("AcceptsModel")
	}
}

// Muse Wonder 1.0 is slow (H-049: the coordinator's probe took 117 s, more
// than the 90 s default attempt): a call that names it has a research call's
// timeouts (ResearchTimeout: scaled as the research output bound is, at most
// BudgetCaps.Timeout, the 300 s job timeout of D-007) for each attempt and
// for the whole call, and keeps its own output bound and retries. A call on
// Muse Spark 1.3 keeps the budget's own. No deployment budget gets Muse
// Wonder past the cap.
func TestASlowModelHasTheResearchTimeouts(t *testing.T) {
	var got Request
	var left time.Duration
	b := chooser{backendFunc: func(ctx context.Context, req Request) (Response, error) {
		got = req
		d, _ := ctx.Deadline()
		left = time.Until(d)
		return Response{Text: []byte(validProposal), Model: req.Model}, nil
	}, models: []string{spark, wonder}}
	budget := DefaultBudget()
	if _, err := askModel(client(t, b), prompt(t, RoleModeler), wonder); err != nil {
		t.Fatal(err)
	}
	if got.AttemptTimeout != budget.ResearchTimeout(budget.AttemptTimeout) || got.AttemptTimeout != BudgetCaps.Timeout ||
		got.MaxOutputTokens != budget.MaxOutputTokens || got.MaxRetries != budget.MaxRetries {
		t.Errorf("Muse Wonder: %+v", got)
	}
	if left <= budget.Timeout || left > BudgetCaps.Timeout {
		t.Errorf("Muse Wonder's call has %v left, want more than %v and at most %v", left, budget.Timeout, BudgetCaps.Timeout)
	}
	if _, err := askModel(client(t, b), prompt(t, RoleModeler), spark); err != nil {
		t.Fatal(err)
	}
	if got.AttemptTimeout != budget.AttemptTimeout || got.MaxOutputTokens != budget.MaxOutputTokens || left > budget.Timeout {
		t.Errorf("Muse Spark: %+v, %v left", got, left)
	}
	// A research call on Muse Wonder: the research bounds, which already
	// are the cap's; a deployment at the caps: still the cap.
	if _, err := askModel(client(t, researchChooser{chooser: b}), researchPrompt(t, RoleScenario), wonder); err != nil {
		t.Fatal(err)
	}
	if got.AttemptTimeout != BudgetCaps.Timeout || got.MaxOutputTokens != ResearchMaxOutputTokens || got.MaxRetries != ResearchMaxRetries ||
		left > BudgetCaps.Timeout {
		t.Errorf("Muse Wonder with research: %+v, %v left", got, left)
	}
	capped := client(t, b, func(b *Budget) {
		b.Timeout, b.AttemptTimeout, b.MaxOutputTokens = BudgetCaps.Timeout, BudgetCaps.Timeout, 1
	})
	if _, err := askModel(capped, prompt(t, RoleModeler), wonder); err != nil {
		t.Fatal(err)
	}
	if got.AttemptTimeout != BudgetCaps.Timeout || left > BudgetCaps.Timeout {
		t.Errorf("at the caps: %+v, %v left", got, left)
	}
	if !SlowModel(wonder) || SlowModel(spark) || SlowModel("") {
		t.Error("SlowModel")
	}
}

type researchChooser struct{ chooser }

func (researchChooser) AcceptsResearch(role Role) bool { return role.Researches() }

// chooserBillable is a billable chooser: its quote names the model the
// request asks for (its own, spark, when none), unless quoted is set.
type chooserBillable struct {
	chooser
	maxInput int64
	quoted   string
}

func (b chooserBillable) Quote(_ context.Context, req Request) (Quote, error) {
	m := req.Model
	if m == "" {
		m = spark
	}
	if b.quoted != "" {
		m = b.quoted
	}
	q := Quote{Model: m, MaxInputTokens: b.maxInput}
	if req.Prompt.Research {
		q.MaxToolCalls = 64
	}
	return q, nil
}

func (b chooserBillable) AcceptsResearch(role Role) bool { return role.Researches() }

// The reservation of a call that names a model is at that model's price:
// Muse Wonder 1.0's for a Muse Wonder call (each attempt at its input bound
// and max_tokens, every retry reserved), Muse Spark 1.3's otherwise; with
// research, its search price too. A backend whose quote names another model
// than the request asks for is refused before anything is reserved or sent;
// an answer from another model than the one asked for is refused, its usage
// settled all the same.
func TestAChosenModelIsReservedAtItsPrice(t *testing.T) {
	answered := func(model string) backendFunc {
		return func(_ context.Context, req Request) (Response, error) {
			return Response{Text: []byte(validProposal), Model: model, Usage: Usage{InputTokens: 2000, OutputTokens: 900}}, nil
		}
	}
	const maxIn = 20_000
	budget := DefaultBudget()
	wonderPrice, _ := spend.PriceOf(wonder)
	sparkPrice, _ := spend.PriceOf(spark)
	attempts := int64(1 + budget.MaxRetries)
	for _, c := range []struct {
		model, answers string
		want           int64
	}{
		{wonder, wonder, wonderPrice.AttemptBound(maxIn, budget.MaxOutputTokens) * attempts},
		{spark, spark, sparkPrice.AttemptBound(maxIn, budget.MaxOutputTokens) * attempts},
		{"", spark, sparkPrice.AttemptBound(maxIn, budget.MaxOutputTokens) * attempts},
	} {
		m := &recordingMeter{}
		b := chooserBillable{chooser: chooser{backendFunc: answered(c.answers), models: []string{spark, wonder}}, maxInput: maxIn}
		res, err := askModel(metered(t, b, m), prompt(t, RoleModeler), c.model)
		if err != nil {
			t.Fatalf("%q: %v", c.model, err)
		}
		if len(m.charges) != 1 || m.charges[0].NanoUSD != c.want || m.charges[0].Model != c.answers {
			t.Errorf("%q: reserved %+v, want %d at %s", c.model, m.charges, c.want, c.answers)
		}
		price, _ := spend.PriceOf(c.answers)
		if len(m.settled) != 1 || m.settled[0].NanoUSD != price.Cost(spend.Tokens{Input: 2000, Output: 900}) || res.Metadata.Model != c.answers {
			t.Errorf("%q: settled %+v, model %q", c.model, m.settled, res.Metadata.Model)
		}
	}
	if w, s := wonderPrice.AttemptBound(maxIn, budget.MaxOutputTokens), sparkPrice.AttemptBound(maxIn, budget.MaxOutputTokens); w <= s {
		t.Errorf("Muse Wonder's bound %d is not above Muse Spark's %d", w, s)
	}

	// Research on Muse Wonder: one attempt, the whole bound at Muse Wonder's
	// prices and every step at its search price.
	m := &recordingMeter{}
	b := chooserBillable{chooser: chooser{backendFunc: answered(wonder), models: []string{spark, wonder}}, maxInput: 1_048_576}
	if _, err := askModel(metered(t, b, m), researchPrompt(t, RoleModeler), wonder); err != nil {
		t.Fatal(err)
	}
	search, _ := spend.SearchPriceOf(wonder)
	want := wonderPrice.AttemptBound(1_048_576, ResearchMaxOutputTokens) + 64*search
	if len(m.charges) != 1 || m.charges[0].NanoUSD != want || m.charges[0].Model != wonder {
		t.Errorf("research on Muse Wonder: reserved %+v, want %d", m.charges, want)
	}
	if want != 4_994_304_000 { // USD 4.194304 + 0.64 + 0.16: just under D-110's USD 5 organization day
		t.Errorf("the Muse Wonder research reservation is %d nano-USD", want)
	}

	// A quote of another model than the one asked for: nothing reserved or
	// sent.
	sent := false
	m = &recordingMeter{}
	lying := chooserBillable{chooser: chooser{backendFunc: func(context.Context, Request) (Response, error) {
		sent = true
		return Response{}, nil
	}, models: []string{spark, wonder}}, maxInput: maxIn, quoted: spark}
	if _, err := askModel(metered(t, lying, m), prompt(t, RoleModeler), wonder); CodeOf(err) != CodeInvalidRequest || sent || len(m.charges) != 0 {
		t.Errorf("a quote of another model: %v, sent %v, charges %d", err, sent, len(m.charges))
	}

	// An answer from another model than the one asked for: refused, and
	// settled at the price of the model the request was reserved for.
	m = &recordingMeter{}
	swapped := chooserBillable{chooser: chooser{backendFunc: answered(spark), models: []string{spark, wonder}}, maxInput: maxIn}
	res, err := askModel(metered(t, swapped, m), prompt(t, RoleModeler), wonder)
	if CodeOf(err) != CodeInvalidOutput || len(m.settled) != 1 || m.settled[0].NanoUSD != wonderPrice.Cost(spend.Tokens{Input: 2000, Output: 900}) {
		t.Errorf("an answer from another model: %v, settled %+v", err, m.settled)
	}
	if res.Output.Status != "" {
		t.Error("the swapped answer was taken")
	}
}
