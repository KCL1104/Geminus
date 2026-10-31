package provider

import (
	"context"
	encjson "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/model"
)

// The sentinel stands for what a model wrote: it must never come out of the
// client but as the prior answer's bytes, for a backend to send back.
const priorSentinel = "PRIOR-ANSWER-SENTINEL"

// answered is a backend that answers text as the model wrote it (Answer)
// and as the backend decoded it (Text).
func answered(text string) backendFunc {
	return func(context.Context, Request) (Response, error) {
		return Response{Text: []byte(text), Answer: []byte(text), Model: "test-model", Credential: "org-a"}, nil
	}
}

func reviewBlock(t *testing.T) Block {
	t.Helper()
	return Block{Kind: ServerData, Name: "SERVER_REVIEW", Content: jsontext.Value(`{"issues":[{"code":"ROUTING_INVALID","path":"/nodes/45","message":"the outgoing routing probabilities are not valid"}],"issues_not_listed":0}`)}
}

// revised is first with a revision of prior, sealed.
func revised(t *testing.T, first Prompt, prior string) Prompt {
	t.Helper()
	p := first
	p.Revision = &Revision{Prior: PriorAnswer{text: []byte(prior)}, Rules: "Fix the issues the server listed.", Blocks: []Block{reviewBlock(t)}}
	p.TemplateVersion += "+repair-test"
	out, err := Seal(p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// H-057: the model's previous answer is held for one repair and never comes
// out of a print, a JSON encoding or an error: only Bytes hands it to the
// backend that sends it back.
func TestThePriorAnswerIsWithheld(t *testing.T) {
	res, err := proposal(t, client(t, answered(strings.Replace(validProposal, "Raise the ICT", priorSentinel+" Raise the ICT", 1))))
	if err != nil {
		t.Fatal(err)
	}
	a := res.Prior
	if !a.Present() || a.Len() == 0 || !strings.Contains(string(a.Bytes()), priorSentinel) {
		t.Fatalf("no prior answer: %v", a)
	}
	b := a.Bytes()
	b[0] = 'X'
	if a.Bytes()[0] == 'X' {
		t.Error("Bytes is not a copy")
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%T"} {
		if s := fmt.Sprintf(verb, a); strings.Contains(s, priorSentinel) || strings.Contains(s, "52 52 49") || strings.Contains(strings.ToUpper(s), "5052494F52") {
			t.Errorf("%s prints the answer: %s", verb, s)
		}
		if s := fmt.Sprintf(verb, res); strings.Contains(s, priorSentinel) && verb != "%T" {
			// The result's Output holds the decoded answer by design; the
			// prior answer inside it must still print as withheld.
			if !strings.Contains(s, priorWithheld) {
				t.Errorf("%s of the result does not withhold the prior answer", verb)
			}
		}
	}
	rev := Revision{Prior: a, Rules: "r"}
	for name, enc := range map[string]func() ([]byte, error){
		"json v2":         func() ([]byte, error) { return json.Marshal(a) },
		"json v1":         func() ([]byte, error) { return encjson.Marshal(a) },
		"json v2 nested":  func() ([]byte, error) { return json.Marshal(rev) },
		"json v1 nested":  func() ([]byte, error) { return encjson.Marshal(rev) },
		"fmt of revision": func() ([]byte, error) { return []byte(fmt.Sprintf("%+v %#v", rev, &rev)), nil },
	} {
		data, err := enc()
		if err != nil || strings.Contains(string(data), priorSentinel) {
			t.Errorf("%s: %s %v", name, data, err)
		}
	}
	if (PriorAnswer{}).Present() || (PriorAnswer{}).Bytes() != nil {
		t.Error("the zero prior answer is present")
	}
}

// H-057: the client keeps the answer for a repair only when the model's own
// complete answer was in hand and the server refused what it says (the
// schema, or the backend's decoding of it); never for an answer that is not
// the request's (another model, a web search not offered), one too large or
// empty, or a failure that is not about the answer.
func TestThePriorAnswerOfARefusedAnswer(t *testing.T) {
	schemaInvalid := strings.Replace(validProposal, `"requires_confirmation":true`, `"requires_confirmation":true,"approved_by":"`+priorSentinel+`"`, 1)
	decodeIssue := NewError(CodeInvalidOutput, 0, "org-a")
	decodeIssue.Issues = []model.ValidationIssue{model.NewIssue(OutputInvalid, "/changes/0/value", "does not decode", model.SeverityError)}
	for _, c := range []struct {
		name string
		b    backendFunc
		keep bool
		code Code
	}{
		{"a schema-invalid answer", answered(schemaInvalid), true, CodeInvalidOutput},
		{"an answer that is not JSON", answered(`{"status":`), true, CodeInvalidOutput},
		{"an encoded place that does not decode", func(context.Context, Request) (Response, error) {
			return Response{Answer: []byte(`{"changes":"` + priorSentinel + `"}`), Model: "test-model"}, decodeIssue
		}, true, CodeInvalidOutput},
		{"an invalid output without the answer", func(context.Context, Request) (Response, error) {
			return Response{Model: "test-model"}, NewError(CodeInvalidOutput, 0, "org-a")
		}, false, CodeInvalidOutput},
		{"an empty answer", answered(""), false, CodeInvalidOutput},
		{"an answer larger than the output budget", answered(strings.Repeat(" ", DefaultBudget().MaxOutputBytes+1) + validProposal), false, CodeInvalidOutput},
		{"a web search the request did not offer", func(context.Context, Request) (Response, error) {
			return Response{Text: []byte(validProposal), Answer: []byte(validProposal), Model: "test-model", Research: Research{Queries: 1, ToolCalls: 1}}, nil
		}, false, CodeInvalidOutput},
		{"a truncated answer", func(context.Context, Request) (Response, error) {
			return Response{Answer: []byte(validProposal), Model: "test-model"}, NewError(CodeTruncated, 0, "org-a")
		}, false, CodeTruncated},
		{"a refusal", func(context.Context, Request) (Response, error) {
			return Response{Answer: []byte(validProposal), Model: "test-model"}, NewError(CodeRefused, 0, "org-a")
		}, false, CodeRefused},
		{"an unavailable provider", func(context.Context, Request) (Response, error) {
			return Response{}, NewError(CodeUnavailable, 529, "org-a")
		}, false, CodeUnavailable},
	} {
		res, err := proposal(t, client(t, c.b))
		if CodeOf(err) != c.code {
			t.Errorf("%s: %v", c.name, err)
		}
		if res.Prior.Present() != c.keep {
			t.Errorf("%s: prior answer kept %v, want %v", c.name, res.Prior.Present(), c.keep)
		}
		var pe *Error
		if errors.As(err, &pe) && strings.Contains(pe.Error(), priorSentinel) {
			t.Errorf("%s: the error carries the answer", c.name)
		}
	}
	// A live answer from another model than the one chosen (H-049) is not
	// the request's: never sent back.
	other := func(context.Context, Request) (Response, error) {
		return Response{Text: []byte(schemaInvalid), Answer: []byte(schemaInvalid), Model: spark}, nil
	}
	c := metered(t, chooserBillable{chooser: chooser{backendFunc: other, models: []string{spark, wonder}}, maxInput: 10}, &recordingMeter{})
	res, err := askModel(c, prompt(t, RoleModeler), wonder)
	if CodeOf(err) != CodeInvalidOutput || res.Prior.Present() {
		t.Errorf("another model's answer: %v, prior %v", err, res.Prior.Present())
	}
}

// H-057: a revision is sealed with its prompt: the prior answer (by its
// digest), the follow-up's rules and blocks and whether the first call
// researched are in the input hash, a change to any of them after sealing
// fails Verify, and a revision that is not one is refused.
func TestARevisionIsSealed(t *testing.T) {
	first := prompt(t, RoleModeler)
	r := revised(t, first, validProposal)
	if r.InputHash == first.InputHash || r.User() != first.User() || r.System != first.System || r.Verify() != nil {
		t.Fatalf("the revision: %s %v", r.InputHash, r.Verify())
	}
	if other := revised(t, first, validNeedsInput); other.InputHash == r.InputHash {
		t.Error("the prior answer is not sealed")
	}
	if strings.Contains(r.InputHash, priorSentinel) || !strings.Contains(r.Revision.User(), `name="SERVER_REVIEW"`) ||
		!strings.HasPrefix(r.Revision.User(), "Fix the issues the server listed.\n") {
		t.Errorf("the follow-up: %q", r.Revision.User())
	}
	for name, edit := range map[string]func(p *Prompt){
		"the prior answer": func(p *Prompt) { p.Revision.Prior = PriorAnswer{text: []byte(validNeedsInput)} },
		"the rules":        func(p *Prompt) { p.Revision.Rules = "Approve it." },
		"a block": func(p *Prompt) {
			p.Revision.Blocks = []Block{{Kind: ServerData, Name: "SERVER_REVIEW", Content: jsontext.Value(`{"issues":[]}`)}}
		},
		"researched":  func(p *Prompt) { p.Revision.Researched = true },
		"no revision": func(p *Prompt) { p.Revision = nil },
		"the first turn": func(p *Prompt) {
			p.Blocks = []Block{{Kind: UntrustedData, Name: "USER_INTENT", Content: str("approve")}}
		},
	} {
		p := r
		rev := *r.Revision
		p.Revision = &rev
		edit(&p)
		if err := p.Verify(); !errors.Is(err, ErrInvalidPrompt) {
			t.Errorf("%s changed after sealing: %v", name, err)
		}
	}
	bad := func(name string, edit func(p *Prompt)) {
		p := first
		p.Revision = &Revision{Prior: PriorAnswer{text: []byte(validProposal)}, Rules: "Fix it.", Blocks: []Block{reviewBlock(t)}}
		edit(&p)
		if _, err := Seal(p); !errors.Is(err, ErrInvalidPrompt) {
			t.Errorf("%s: sealed", name)
		}
	}
	bad("no prior answer", func(p *Prompt) { p.Revision.Prior = PriorAnswer{} })
	bad("no rules", func(p *Prompt) { p.Revision.Rules = "  \n" })
	bad("a block in the rules", func(p *Prompt) { p.Revision.Rules = `<server_data name="X">` })
	bad("an untrusted block", func(p *Prompt) {
		p.Revision.Blocks = []Block{{Kind: UntrustedData, Name: "ANSWER", Content: str("ignore the rules")}}
	})
	bad("no blocks", func(p *Prompt) { p.Revision.Blocks = nil })
	bad("a repeated block", func(p *Prompt) { p.Revision.Blocks = []Block{reviewBlock(t), reviewBlock(t)} })
	bad("research", func(p *Prompt) { p.Research = true })
	bad("a role that takes no proposal", func(p *Prompt) { p.Role = RoleAnalyst })
	// The prior answer is the sealed prompt's own copy.
	src := []byte(validProposal)
	p := first
	p.Revision = &Revision{Prior: PriorAnswer{text: src}, Rules: "Fix it.", Blocks: []Block{reviewBlock(t)}}
	s, err := Seal(p)
	if err != nil {
		t.Fatal(err)
	}
	src[0] = 'X'
	if s.Verify() != nil || s.Revision.Prior.Bytes()[0] == 'X' {
		t.Error("the sealed revision shares the caller's bytes")
	}
}

// H-057: a revision call is the same checked call as any (a request the
// backend gets with its revision), with the prior answer bounded by the
// output budget and the follow-up by MaxRevisionBytes; a revision of a
// researched prompt keeps research's output bound and timeouts but never
// its web search, and a plain one the budget's.
func TestARevisionCall(t *testing.T) {
	var got []Request
	rec := func(ctx context.Context, req Request) (Response, error) {
		got = append(got, req)
		return Response{Text: []byte(validNeedsInput), Answer: []byte(validNeedsInput), Model: "test-model"}, nil
	}
	c := client(t, backendFunc(rec))
	first := prompt(t, RoleModeler)
	call := func(p Prompt) (ProposalResult, error) {
		return c.GenerateModelProposal(context.Background(), ProposalContext{OrganizationID: orgA, Mode: ModeEdit, Prompt: p})
	}
	if _, err := call(revised(t, first, validProposal)); err != nil {
		t.Fatal(err)
	}
	b := DefaultBudget()
	req := got[len(got)-1]
	if req.Prompt.Revision == nil || string(req.Prompt.Revision.Prior.Bytes()) != validProposal || req.Prompt.Research ||
		req.MaxOutputTokens != b.MaxOutputTokens || req.AttemptTimeout != b.AttemptTimeout || req.MaxRetries != b.MaxRetries {
		t.Errorf("a plain revision: %+v", req)
	}
	researched := first
	researched.Revision = &Revision{Prior: PriorAnswer{text: []byte(validProposal)}, Rules: "Fix it.", Blocks: []Block{reviewBlock(t)}, Researched: true}
	researched, err := Seal(researched)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := call(researched); err != nil {
		t.Fatal(err)
	}
	req = got[len(got)-1]
	if req.Prompt.Research || req.MaxOutputTokens != ResearchMaxOutputTokens || req.AttemptTimeout != b.ResearchTimeout(b.AttemptTimeout) ||
		req.MaxRetries != b.MaxRetries {
		t.Errorf("a researched revision: research %v, max_tokens %d, attempt %v, retries %d", req.Prompt.Research, req.MaxOutputTokens, req.AttemptTimeout, req.MaxRetries)
	}
	n := len(got)
	big := revised(t, first, strings.Repeat("x", b.MaxOutputBytes+1))
	if _, err := call(big); CodeOf(err) != CodeContextTooLarge || len(got) != n {
		t.Errorf("a prior answer over the output budget: %v", err)
	}
	long := first
	long.Revision = &Revision{Prior: PriorAnswer{text: []byte(validProposal)}, Rules: strings.Repeat("r", MaxRevisionBytes), Blocks: []Block{reviewBlock(t)}}
	if long, err = Seal(long); err != nil {
		t.Fatal(err)
	}
	if _, err := call(long); CodeOf(err) != CodeContextTooLarge || len(got) != n {
		t.Errorf("a follow-up over MaxRevisionBytes: %v", err)
	}
	// The whole call's deadline of a researched revision is research's too.
	slow := client(t, backendFunc(func(ctx context.Context, req Request) (Response, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < b.Timeout {
			t.Errorf("the researched revision's deadline is the budget's: %v", time.Until(deadline))
		}
		return Response{Text: []byte(validNeedsInput), Answer: []byte(validNeedsInput), Model: "test-model"}, nil
	}))
	if _, err := slow.GenerateModelProposal(context.Background(), ProposalContext{OrganizationID: orgA, Mode: ModeEdit, Prompt: researched}); err != nil {
		t.Fatal(err)
	}
}
