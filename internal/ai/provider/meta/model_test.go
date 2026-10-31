package meta_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/meta"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/config/aiapproval"
	"github.com/kcl1104/geminus/internal/secret"
)

// H-049: the person's choice of model on the local server. A request that
// names Muse Wonder 1.0 is quoted, counted and sent for it, and only an
// answer from it is taken; a model the approval does not list for the role
// is never sent. No test sends anything to the provider's real API.

var _ provider.ModelChooser = (*meta.Backend)(nil)

const wonder = "muse-wonder-1.0"

// fromWonder is r answered by Muse Wonder 1.0.
func fromWonder(r reply) reply {
	r.body = strings.Replace(r.body, `"model":"`+model+`"`, `"model":"`+wonder+`"`, 1)
	return r
}

func askModel(t *testing.T, c *provider.Client, chosen string, images ...provider.Image) (provider.ProposalResult, error) {
	t.Helper()
	return c.GenerateModelProposal(context.Background(), provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: sealed(t, images...),
		Attribution: attribution, Model: chosen})
}

// Muse Wonder chosen: the request's model is muse-wonder-1.0, the answer is
// Muse Wonder's, the spend is at Muse Wonder's price, and the metadata says
// which model answered. No model named: Muse Spark, as before.
func TestAChosenModelIsSentAndAnswers(t *testing.T) {
	s := newServer(t, map[string][]reply{messagesPath: {fromWonder(ok(answerJ)), ok(answerJ)}})
	c, b, m, _ := meteredClient(t, s, 10*spendUSD)
	meta.SetModels(b, model, wonder)
	res, err := askModel(t, c, wonder)
	if err != nil {
		t.Fatal(err)
	}
	if s.bodies[0]["model"] != wonder || res.Metadata.Model != wonder || res.Output.Status != "needs_input" {
		t.Errorf("sent %v, answered by %q", s.bodies[0]["model"], res.Metadata.Model)
	}
	price, _ := spend.PriceOf(wonder)
	if want := price.Cost(spend.Tokens{Input: 120, Output: 30}); m.Spent() != want || res.Metadata.CostNanoUSD != want {
		t.Errorf("spent %d, want %d at Muse Wonder's price", m.Spent(), want)
	}
	res, err = askModel(t, c, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.bodies[1]["model"] != model || res.Metadata.Model != model {
		t.Errorf("no model named: sent %v, answered by %q", s.bodies[1]["model"], res.Metadata.Model)
	}
	// H-065: Muse Wonder's own max_tokens; Muse Spark's is the budget's, as before.
	if s.bodies[0]["max_tokens"] != float64(128_000) || s.bodies[1]["max_tokens"] != float64(provider.DefaultBudget().MaxOutputTokens) {
		t.Errorf("max_tokens: Muse Wonder %v, Muse Spark %v", s.bodies[0]["max_tokens"], s.bodies[1]["max_tokens"])
	}
}

// An answer from another model than the one the person chose is refused,
// whichever was chosen: Muse Spark's answer to a Muse Wonder request, and
// Muse Wonder's to a Muse Spark one. The usage is settled at the price of the
// model asked for.
func TestAnAnswerFromAnotherModelThanTheChosenOneIsRefused(t *testing.T) {
	for chosen, r := range map[string]reply{wonder: ok(answerJ), model: fromWonder(ok(answerJ)), "": fromWonder(ok(answerJ))} {
		s := newServer(t, map[string][]reply{messagesPath: {r}})
		c, b, m, _ := meteredClient(t, s, 10*spendUSD)
		meta.SetModels(b, model, wonder)
		_, err := askModel(t, c, chosen)
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Code != provider.CodeInvalidOutput {
			t.Errorf("%q answered by another model: %v", chosen, err)
		}
		asked := chosen
		if asked == "" {
			asked = model
		}
		price, _ := spend.PriceOf(asked)
		if m.Spent() != price.Cost(spend.Tokens{Input: 120, Output: 30}) {
			t.Errorf("%q: spent %d", chosen, m.Spent())
		}
	}
}

// A model the approval does not list for the role is never asked: not by
// the client (it asks the backend first), not by the backend when called
// directly (Quote and Complete refuse it before any I/O), and never for the
// analyst, whose explanations stay on the approval's model.
func TestAModelTheApprovalDoesNotListIsNeverSent(t *testing.T) {
	s := newServer(t, map[string][]reply{messagesPath: {fromWonder(ok(answerJ))}})
	c, b, m, _ := meteredClient(t, s, 10*spendUSD)
	if _, err := askModel(t, c, wonder); provider.CodeOf(err) != provider.CodeModelNotApproved {
		t.Errorf("a backend without a model choice: %v", err)
	}
	meta.SetModels(b, model, wonder)
	for _, other := range []string{"muse-spark-1.3-contributor", "muse-wonder-1.0-contributor", "Muse-Wonder-1.0", "sam-3.1"} {
		if _, err := askModel(t, c, other); provider.CodeOf(err) != provider.CodeModelNotApproved {
			t.Errorf("%q: %v", other, err)
		}
	}
	if b.AcceptsModel(provider.RoleAnalyst, wonder) || b.AcceptsModel(provider.RoleImporter, wonder) || !b.AcceptsModel(provider.RoleScenario, wonder) {
		t.Error("AcceptsModel")
	}
	analyst := provider.Request{Prompt: sealed(t), OutputSchema: "analysis-output.schema.json", MaxOutputTokens: 100, Model: wonder, Reservation: "r1"}
	analyst.Prompt.Role = provider.RoleAnalyst
	if _, err := b.Quote(context.Background(), analyst); provider.CodeOf(err) != provider.CodeModelNotApproved {
		t.Errorf("the analyst's quote on Muse Wonder: %v", err)
	}
	if _, err := b.Complete(context.Background(), analyst); provider.CodeOf(err) != provider.CodeModelNotApproved {
		t.Errorf("the analyst on Muse Wonder: %v", err)
	}
	if n := len(s.bodies); n != 0 || m.Spent() != 0 || m.Committed() != 0 {
		t.Errorf("%d requests sent, spent %d, committed %d", n, m.Spent(), m.Committed())
	}
}

// The quote names the model asked for, so the reservation is at its price;
// a request with images is counted for it too. H-065: a Muse Wonder photo is
// sent with max_tokens 128,000 (provider.SlowModelMaxOutputTokens) and
// reserved at it, every attempt.
func TestTheQuoteAndTheCountAreOfTheChosenModel(t *testing.T) {
	s := newServer(t, map[string][]reply{countPath: {counted("3000")}, messagesPath: {fromWonder(ok(answerJ))}})
	b, _ := backend(t, s)
	meta.SetModels(b, model, wonder)
	text := provider.Request{Prompt: sealed(t), OutputSchema: "proposal-output.schema.json", MaxOutputTokens: 8192, Model: wonder}
	if q, err := b.Quote(context.Background(), text); err != nil || q.Model != wonder {
		t.Errorf("text quote %+v %v", q, err)
	}
	text.Model = ""
	if q, err := b.Quote(context.Background(), text); err != nil || q.Model != model {
		t.Errorf("no model named: quote %+v %v", q, err)
	}
	charges := &modelLog{}
	c, err := provider.NewMetered(b, provider.DefaultBudget(), charges)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := askModel(t, c, wonder, pngImage(t, 64, 48)); err != nil {
		t.Fatal(err)
	}
	if s.bodies[0]["model"] != wonder || s.bodies[1]["model"] != wonder {
		t.Errorf("the count asked for %v, the message for %v", s.bodies[0]["model"], s.bodies[1]["model"])
	}
	if _, ok := s.bodies[0]["max_tokens"]; ok || s.bodies[1]["max_tokens"] != float64(128_000) {
		t.Errorf("the count's max_tokens %v, the message's %v; want none and 128000", s.bodies[0]["max_tokens"], s.bodies[1]["max_tokens"])
	}
	price, _ := spend.PriceOf(wonder)
	per := price.AttemptBound(3300, provider.SlowModelMaxOutputTokens)
	if len(charges.charges) != 1 || charges.charges[0].Model != wonder || charges.charges[0].NanoUSD != per*int64(1+provider.DefaultBudget().MaxRetries) {
		t.Errorf("reserved %+v, want %d at Muse Wonder's price", charges.charges, per*3)
	}
}

// modelLog grants every charge and records it, its model included.
type modelLog struct{ charges []provider.Charge }

func (l *modelLog) Reserve(_ context.Context, c provider.Charge) (provider.Reservation, error) {
	l.charges = append(l.charges, c)
	return provider.Reservation{ID: "r1", Charge: c}, nil
}

func (l *modelLog) Settle(context.Context, provider.Reservation, provider.Settlement) error {
	return nil
}

// New takes the approval's model choice: H-029 lists Muse Wonder 1.0 for the
// proposal roles, never for the analyst; the backend's own model is still
// the approval's model, so a backend of Muse Wonder alone is refused.
func TestNewTakesTheApprovalsModelChoice(t *testing.T) {
	h029, ok := aiapproval.Recorded("H-029")
	if !ok {
		t.Fatal("H-029 is not recorded")
	}
	cfg := meta.Config{Approval: h029, Model: model, Credential: meta.Credential{Label: "meta", Key: secret.Secret(key)}}
	b, err := meta.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		role  provider.Role
		model string
		want  bool
	}{
		{provider.RoleModeler, wonder, true}, {provider.RoleScenario, wonder, true}, {provider.RoleModeler, model, true},
		{provider.RoleAnalyst, model, true}, {provider.RoleAnalyst, wonder, false}, {provider.RoleImporter, wonder, false},
		{provider.RoleModeler, "muse-spark-1.3-contributor", false}, {provider.RoleModeler, "", false},
	} {
		if got := b.AcceptsModel(c.role, c.model); got != c.want {
			t.Errorf("%s %q: %v", c.role, c.model, got)
		}
	}
	cfg.Model = wonder
	if _, err := meta.New(cfg); err == nil {
		t.Error("a backend whose own model is not the approval's")
	}
}
