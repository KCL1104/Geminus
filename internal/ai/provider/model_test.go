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
// is reserved at that model's price, and is answered only by that model.
// H-065: a slow model's call (Muse Wonder 1.0) has its own bounds on every
// proposal path, 128,000 output tokens and 900 s; Muse Spark 1.3 keeps its
// own, within D-007's 300 s.

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

// H-065: Muse Wonder 1.0 is slow (live on 2026-10-31 it used all 8,192
// output tokens of a scenario in 61 s, and all 32,000 of a photo in 91 s,
// both truncated). Every call that names it has its own bounds, whatever the
// deployment's budget: max_tokens SlowModelMaxOutputTokens (128,000) and
// SlowModelTimeout (900 s) for each attempt and for the whole call, in words
// (an edit and a scenario), with research, with images and for its H-057
// repair (plain, after research and of a photo). Its retries are the call's
// own: the budget's, none with research. Muse Spark 1.3 keeps the bounds it
// had on each of those paths, never past BudgetCaps (D-007's 300 s job
// timeout).
func TestASlowModelHasItsOwnBounds(t *testing.T) {
	if SlowModelMaxOutputTokens != 128_000 || SlowModelTimeout != 900*time.Second {
		t.Fatalf("Muse Wonder's bounds are %d tokens and %v, want H-065's 128,000 and 900 s", SlowModelMaxOutputTokens, SlowModelTimeout)
	}
	if SlowModelMaxOutputTokens <= BudgetCaps.MaxOutputTokens || SlowModelTimeout <= BudgetCaps.Timeout || BudgetCaps.Timeout != 300*time.Second {
		t.Fatalf("the caps of Muse Spark (%d tokens, %v) are not below Muse Wonder's own", BudgetCaps.MaxOutputTokens, BudgetCaps.Timeout)
	}
	var got Request
	var left time.Duration
	b := everyPath{chooser{backendFunc: func(ctx context.Context, req Request) (Response, error) {
		got = req
		d, _ := ctx.Deadline()
		left = time.Until(d)
		return Response{Text: []byte(validNeedsInput), Answer: []byte(validNeedsInput), Model: req.Model}, nil
	}, models: []string{spark, wonder}}}
	budget := DefaultBudget()
	researched := func(first Prompt) Prompt {
		p := first
		p.Revision = &Revision{Prior: PriorAnswer{text: []byte(validProposal)}, Rules: "Fix it.", Blocks: []Block{reviewBlock(t)}, Researched: true}
		out, err := Seal(p)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, c := range []struct {
		path string
		p    Prompt
		// What Muse Spark 1.3 gets on this path: max_tokens, the attempt's
		// timeout, the most the whole call has and the retries (Muse Wonder's
		// retries are the same).
		tokens         int64
		attempt, whole time.Duration
		retries        int
	}{
		{"an edit", prompt(t, RoleModeler), budget.MaxOutputTokens, budget.AttemptTimeout, budget.Timeout, budget.MaxRetries},
		{"a scenario", prompt(t, RoleScenario), budget.MaxOutputTokens, budget.AttemptTimeout, budget.Timeout, budget.MaxRetries},
		{"research", researchPrompt(t, RoleScenario), ResearchMaxOutputTokens, BudgetCaps.Timeout, BudgetCaps.Timeout, ResearchMaxRetries},
		{"a photo", imagePrompt(t, testImage(t, 3, 2)), ImageMaxOutputTokens, BudgetCaps.Timeout, BudgetCaps.Timeout, budget.MaxRetries},
		{"a repair", revised(t, prompt(t, RoleModeler), validProposal), budget.MaxOutputTokens, budget.AttemptTimeout, budget.Timeout, budget.MaxRetries},
		{"a repair after research", researched(prompt(t, RoleScenario)), ResearchMaxOutputTokens, BudgetCaps.Timeout, BudgetCaps.Timeout, budget.MaxRetries},
		{"a photo's repair", revised(t, imagePrompt(t, testImage(t, 3, 2)), validProposal), ImageMaxOutputTokens, BudgetCaps.Timeout, BudgetCaps.Timeout, budget.MaxRetries},
	} {
		if _, err := askModel(client(t, b), c.p, wonder); err != nil {
			t.Fatalf("%s on Muse Wonder: %v", c.path, err)
		}
		if got.Model != wonder || got.MaxOutputTokens != SlowModelMaxOutputTokens || got.AttemptTimeout != SlowModelTimeout || got.MaxRetries != c.retries {
			t.Errorf("%s on Muse Wonder: model %q, max_tokens %d, attempt %v, retries %d", c.path, got.Model, got.MaxOutputTokens, got.AttemptTimeout, got.MaxRetries)
		}
		if left <= BudgetCaps.Timeout || left > SlowModelTimeout {
			t.Errorf("%s on Muse Wonder: the call has %v left, want more than %v and at most %v", c.path, left, BudgetCaps.Timeout, SlowModelTimeout)
		}
		if _, err := askModel(client(t, b), c.p, spark); err != nil {
			t.Fatalf("%s on Muse Spark: %v", c.path, err)
		}
		if got.Model != spark || got.MaxOutputTokens != c.tokens || got.AttemptTimeout != c.attempt || got.MaxRetries != c.retries || left > c.whole || left > BudgetCaps.Timeout {
			t.Errorf("%s on Muse Spark: max_tokens %d, attempt %v, retries %d, %v left; want %d, %v, %d, at most %v",
				c.path, got.MaxOutputTokens, got.AttemptTimeout, got.MaxRetries, left, c.tokens, c.attempt, c.retries, c.whole)
		}
	}
	// No deployment budget moves Muse Wonder's bounds: not one at the caps,
	// not a small one.
	for name, mutate := range map[string]func(*Budget){
		"at the caps": func(b *Budget) {
			b.Timeout, b.AttemptTimeout, b.MaxOutputTokens = BudgetCaps.Timeout, BudgetCaps.Timeout, BudgetCaps.MaxOutputTokens
		},
		"small": func(b *Budget) { b.Timeout, b.AttemptTimeout, b.MaxOutputTokens = 2*time.Second, time.Second, 1 },
	} {
		if _, err := askModel(client(t, b, mutate), prompt(t, RoleModeler), wonder); err != nil {
			t.Fatal(err)
		}
		if got.MaxOutputTokens != SlowModelMaxOutputTokens || got.AttemptTimeout != SlowModelTimeout || left <= BudgetCaps.Timeout || left > SlowModelTimeout {
			t.Errorf("a budget %s: Muse Wonder got max_tokens %d, attempt %v, %v left", name, got.MaxOutputTokens, got.AttemptTimeout, left)
		}
	}
	// A deployment still cannot configure a call past D-007's 300 s.
	over := DefaultBudget()
	over.Timeout = SlowModelTimeout
	if over.Validate() == nil {
		t.Error("a deployment budget of 900 s validates")
	}
	if !SlowModel(wonder) || SlowModel(spark) || SlowModel("") {
		t.Error("SlowModel")
	}
}

// everyPath is a chooser that also researches and takes images, so one
// backend serves every proposal path.
type everyPath struct{ chooser }

func (everyPath) AcceptsResearch(role Role) bool { return role.Researches() }
func (everyPath) AcceptsImages() bool            { return true }

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
// and its own max_tokens, SlowModelMaxOutputTokens: H-065's worst case,
// every retry reserved), Muse Spark 1.3's otherwise, at the budget's
// max_tokens as before; a repair is reserved the same way; with research,
// its search price too. A backend whose quote names another model than the
// request asks for is refused before anything is reserved or sent; an answer
// from another model than the one asked for is refused, its usage settled
// all the same.
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
	repair := revised(t, prompt(t, RoleModeler), validProposal)
	for _, c := range []struct {
		what           string
		p              Prompt
		model, answers string
		want           int64
	}{
		{"an edit", prompt(t, RoleModeler), wonder, wonder, wonderPrice.AttemptBound(maxIn, SlowModelMaxOutputTokens) * attempts},
		{"a repair", repair, wonder, wonder, wonderPrice.AttemptBound(maxIn, SlowModelMaxOutputTokens) * attempts},
		{"an edit", prompt(t, RoleModeler), spark, spark, sparkPrice.AttemptBound(maxIn, budget.MaxOutputTokens) * attempts},
		{"a repair", repair, spark, spark, sparkPrice.AttemptBound(maxIn, budget.MaxOutputTokens) * attempts},
		{"an edit", prompt(t, RoleModeler), "", spark, sparkPrice.AttemptBound(maxIn, budget.MaxOutputTokens) * attempts},
	} {
		m := &recordingMeter{}
		b := chooserBillable{chooser: chooser{backendFunc: answered(c.answers), models: []string{spark, wonder}}, maxInput: maxIn}
		res, err := askModel(metered(t, b, m), c.p, c.model)
		if err != nil {
			t.Fatalf("%s on %q: %v", c.what, c.model, err)
		}
		if len(m.charges) != 1 || m.charges[0].NanoUSD != c.want || m.charges[0].Model != c.answers {
			t.Errorf("%s on %q: reserved %+v, want %d at %s", c.what, c.model, m.charges, c.want, c.answers)
		}
		price, _ := spend.PriceOf(c.answers)
		if len(m.settled) != 1 || m.settled[0].NanoUSD != price.Cost(spend.Tokens{Input: 2000, Output: 900}) || res.Metadata.Model != c.answers {
			t.Errorf("%s on %q: settled %+v, model %q", c.what, c.model, m.settled, res.Metadata.Model)
		}
	}
	// USD 0.08 of input and 2.56 of output an attempt, three attempts.
	if w := wonderPrice.AttemptBound(maxIn, SlowModelMaxOutputTokens) * attempts; w != 7_920_000_000 {
		t.Errorf("the Muse Wonder edit reservation is %d nano-USD", w)
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
	want := wonderPrice.AttemptBound(1_048_576, SlowModelMaxOutputTokens) + 64*search
	if len(m.charges) != 1 || m.charges[0].NanoUSD != want || m.charges[0].Model != wonder {
		t.Errorf("research on Muse Wonder: reserved %+v, want %d", m.charges, want)
	}
	if want != 6_914_304_000 { // USD 4.194304 + 2.56 + 0.16: within H-065's USD 40 daily caps
		t.Errorf("the Muse Wonder research reservation is %d nano-USD", want)
	}
	// Research on Muse Spark: as before (H-046 (2)).
	m = &recordingMeter{}
	b = chooserBillable{chooser: chooser{backendFunc: answered(spark), models: []string{spark, wonder}}, maxInput: 1_048_576}
	if _, err := askModel(metered(t, b, m), researchPrompt(t, RoleModeler), spark); err != nil {
		t.Fatal(err)
	}
	sparkSearch, _ := spend.SearchPriceOf(spark)
	if want := sparkPrice.AttemptBound(1_048_576, ResearchMaxOutputTokens) + 64*sparkSearch; len(m.charges) != 1 || m.charges[0].NanoUSD != want {
		t.Errorf("research on Muse Spark: reserved %+v, want %d", m.charges, want)
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
