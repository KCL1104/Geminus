package meta_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/meta"
	"github.com/kcl1104/geminus/internal/ai/provider/structured"
	"github.com/kcl1104/geminus/internal/ai/repair"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/prompts"
)

// H-068: live, under the strict json_schema output format, Muse Wonder 1.0
// wrote the projection's JSON texts badly (a string without its quotes,
// garbled text for a number), so every patch value was refused; the same
// request without the format answered clean JSON. A proposal request that
// asks Muse Wonder 1.0 (an edit, a scenario, research mode, a photo and
// H-057's repair) sends no output_config; its system text ends with the
// plainjson template (prompts/plainjson) and the answer's JSON Schema with
// native values (structured.Projection.Native), and its answer is read by
// structured.Projection.DecodeNative. Muse Spark's requests and decoding are
// unchanged (TestRoleRequestGoldens, TestRequestShape, TestEncodedValues).
// No test sends anything to the provider's real API.

// plainJSONSystem checks a Muse Wonder request's system text: one text
// block, the sealed system text, the plainjson template, then the native
// schema as one JSON line. It returns the text.
func plainJSONSystem(t *testing.T, what string, body map[string]any, system string) string {
	t.Helper()
	blocks, _ := body["system"].([]any)
	if len(blocks) != 1 {
		t.Fatalf("%s: %d system blocks", what, len(blocks))
	}
	blk, _ := blocks[0].(map[string]any)
	text, _ := blk["text"].(string)
	if blk["type"] != "text" || len(blk) != 2 {
		t.Fatalf("%s: system block %v", what, slices.Sorted(maps.Keys(blk)))
	}
	tmpl, version, err := prompts.Template("plainjson")
	if err != nil {
		t.Fatal(err)
	}
	head := system + "\n" + strings.TrimRight(tmpl, "\n") + "\n"
	rest, ok := strings.CutPrefix(text, head)
	if !ok || !strings.Contains(head, "prompt_template_version: "+version) {
		t.Fatalf("%s: the system text is not the sealed system text and the plainjson template", what)
	}
	proj, err := structured.Project("proposal-output.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(proj.Native)
	var want, got any
	_ = json.Unmarshal(raw, &want)
	if strings.Contains(rest, "\n") || json.Unmarshal([]byte(rest), &got) != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("%s: the system text does not end with the native schema as one JSON line", what)
	}
	return text
}

// Every proposal path on Muse Wonder 1.0 sends no output_config and asks
// for plain JSON in the system text; its count, for a photo, is the same
// body but max_tokens. The user turn is the one Muse Spark gets. Muse Spark
// still gets the strict format on the same backend.
func TestAMuseWonderProposalAsksForPlainJSON(t *testing.T) {
	for name, c := range map[string]struct {
		mode   provider.Mode
		prompt provider.Prompt
		order  []string
	}{
		"an edit":       {provider.ModeEdit, sealed(t), []string{messagesPath}},
		"a scenario":    {provider.ModeScenario, roleSealed(t, provider.RoleScenario, "USER_INTENT", "add a second shift"), []string{messagesPath}},
		"research mode": {provider.ModeScenario, researchSealed(t, provider.RoleScenario), []string{messagesPath}},
		"a photo":       {provider.ModeEdit, sealed(t, pngImage(t, 64, 48)), []string{countPath, messagesPath}},
	} {
		s := newServer(t, map[string][]reply{messagesPath: {fromWonder(ok(answerJ)), ok(answerJ)}, countPath: {counted("3000")}})
		cl, b, _, _ := meteredClient(t, s, 100*spendUSD)
		meta.SetModels(b, model, wonder)
		in := provider.ProposalContext{OrganizationID: org, Mode: c.mode, Prompt: c.prompt, Attribution: attribution, Model: wonder}
		res, err := cl.GenerateModelProposal(context.Background(), in)
		if err != nil || res.Output.Status != "needs_input" || res.Metadata.Model != wonder {
			t.Fatalf("%s: %+v %v", name, res.Output, err)
		}
		if got := s.order(); !slices.Equal(got, c.order) {
			t.Fatalf("%s: requests %v", name, got)
		}
		var sent string
		for i, body := range s.bodies {
			want := []string{"messages", "model", "system"}
			if s.paths[i] == messagesPath {
				want = append(want, "max_tokens")
				if c.prompt.Research {
					want = append(want, "tools")
				}
			}
			slices.Sort(want)
			if keys := slices.Sorted(maps.Keys(body)); !slices.Equal(keys, want) {
				t.Errorf("%s, %s: fields %v, want %v (no output_config)", name, s.paths[i], keys, want)
			}
			if body["model"] != wonder {
				t.Errorf("%s: model %v", name, body["model"])
			}
			text := plainJSONSystem(t, name, body, c.prompt.System)
			if sent != "" && text != sent {
				t.Errorf("%s: the count and the message have different system texts", name)
			}
			sent = text
			msgs, _ := body["messages"].([]any)
			content, _ := msgs[0].(map[string]any)["content"].([]any)
			last, _ := content[len(content)-1].(map[string]any)
			if len(msgs) != 1 || last["text"] != c.prompt.User() {
				t.Errorf("%s: the user turn is not the prompt's", name)
			}
			leaks(t, name, string(s.raw[i]))
		}
		if c.mode == provider.ModeEdit && len(c.prompt.Images) == 0 {
			// The quote of a text request holds what is sent: the system text
			// with the instruction and the schema, the user turn and D-111's
			// allowance; no projected schema is sent, so none is quoted.
			q, err := b.Quote(context.Background(), provider.Request{Prompt: c.prompt, OutputSchema: "proposal-output.schema.json", Model: wonder})
			if want := int64(len(sent)+len(c.prompt.User())) + meta.QuoteOverheadTokens; err != nil || q.MaxInputTokens != want || q.Model != wonder {
				t.Errorf("%s: quote %+v %v, want %d", name, q, err, want)
			}
		}
		// Muse Spark on the same backend: the strict format, the system text
		// as sealed.
		in.Model = ""
		if _, err := cl.GenerateModelProposal(context.Background(), in); err != nil {
			t.Fatalf("%s on Muse Spark: %v", name, err)
		}
		spark := s.bodies[len(s.bodies)-1]
		if _, ok := spark["output_config"]; !ok || !reflect.DeepEqual(spark["system"], []any{map[string]any{"type": "text", "text": c.prompt.System}}) {
			t.Errorf("%s on Muse Spark: output_config %v, system %v", name, ok, spark["system"])
		}
	}
}

// A Muse Wonder request with nothing to say its answer's format (a
// projection without a native schema, which Project never makes) is never
// sent: the quote refuses it, so nothing is reserved, and Complete refuses
// it before any I/O. Muse Spark's request on the same projection still goes.
func TestAMuseWonderRequestWithoutANativeSchemaIsNeverSent(t *testing.T) {
	s := newServer(t, map[string][]reply{messagesPath: {ok(answerJ)}})
	c, b, m, _ := meteredClient(t, s, 100*spendUSD)
	meta.SetModels(b, model, wonder)
	meta.SetProject(b, func(file string) (structured.Projection, error) {
		p, err := structured.Project(file)
		p.Native = nil
		return p, err
	})
	if _, err := askModel(t, c, wonder); provider.CodeOf(err) != provider.CodeInvalidRequest || s.count(messagesPath) != 0 || m.Committed() != 0 {
		t.Errorf("no native schema: %v, %d requests, %d reserved", err, s.count(messagesPath), m.Committed())
	}
	req := provider.Request{Prompt: sealed(t), OutputSchema: "proposal-output.schema.json", Reservation: "r1", Model: wonder, MaxOutputTokens: 100,
		AttemptTimeout: time.Second}
	if _, err := b.Complete(context.Background(), req); provider.CodeOf(err) != provider.CodeInvalidRequest || s.count(messagesPath) != 0 {
		t.Errorf("Complete: %v", err)
	}
	if _, err := askModel(t, c, ""); err != nil || s.count(messagesPath) != 1 {
		t.Errorf("Muse Spark on the same projection: %v", err)
	}
}

// encodedForm is a native answer as the projection asks Muse Spark for it:
// each patch value as a JSON text in a string.
func encodedForm(changes ...[2]string) (native, encoded string) {
	var n, e []string
	for _, c := range changes {
		head := c[0]
		if c[1] == "" {
			n, e = append(n, head+`}`), append(e, head+`}`)
			continue
		}
		n, e = append(n, head+`,"value":`+c[1]+`}`), append(e, head+`,"value":`+string(mustQuote(c[1]))+`}`)
	}
	wrap := func(list []string) string {
		return `{"status":"proposed","summary":"Raise it.","assumptions":["Service times stay the same."],"risk_level":"low","confidence":0.9,` +
			`"changes":[` + strings.Join(list, ",") + `],"expected_effects":["The queue may fall; simulate to know."],"requires_confirmation":true}`
	}
	return wrap(n), wrap(e)
}

// A Muse Wonder answer in plain JSON, with a value of every kind, is taken:
// the output's values are the values, and the answer the server reviews,
// stores and hashes is byte for byte the one Muse Spark's answer of the
// same proposal in the projection's form gives.
func TestMuseWonderAnswersInPlainJSON(t *testing.T) {
	changes := [][2]string{
		{`{"op":"test","path":"/resources/0/id"`, `"fct_tester"`},
		{`{"op":"replace","path":"/resources/0/capacity"`, `4`},
		{`{"op":"replace","path":"/buffers/0/capacity"`, `40`},
		{`{"op":"replace","path":"/routing/0/probability"`, `0.25`},
		{`{"op":"replace","path":"/nodes/0/domain_attributes/rework"`, `true`},
		{`{"op":"replace","path":"/nodes/0/domain_attributes/note"`, `null`},
		{`{"op":"add","path":"/nodes/-"`, `{"id":"node_9","kind":"process","name":"Second ICT tester","domain_attributes":{"station":"ict"}}`},
		{`{"op":"add","path":"/resources/0/tags"`, `["ict", 2, false]`},
		{`{"op":"remove","path":"/flows/3"`, ``},
	}
	native, encoded := encodedForm(changes...)
	s := newServer(t, map[string][]reply{messagesPath: {fromWonder(ok(native)), ok(encoded)}})
	c, b, _, _ := meteredClient(t, s, 100*spendUSD)
	meta.SetModels(b, model, wonder)
	w, err := askModel(t, c, wonder)
	if err != nil {
		t.Fatal(err)
	}
	m, err := askModel(t, c, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Output.Changes) != len(changes) {
		t.Fatalf("%d changes", len(w.Output.Changes))
	}
	for i, ch := range changes {
		if ch[1] == "" {
			continue
		}
		var got, want any
		if json.Unmarshal(w.Output.Changes[i].Value, &got) != nil || json.Unmarshal([]byte(ch[1]), &want) != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("change %d: value %s, want %s", i, w.Output.Changes[i].Value, ch[1])
		}
	}
	if string(w.Raw) != string(m.Raw) || w.Metadata.OutputHash == "" || w.Metadata.OutputHash != m.Metadata.OutputHash {
		t.Errorf("the Muse Wonder answer is not the Muse Spark one:\n%s\n%s", w.Raw, m.Raw)
	}
	if !reflect.DeepEqual(w.Output, m.Output) || string(w.Prior.Bytes()) != native {
		t.Errorf("the outputs differ, or the prior is not the answer as written")
	}
}

// What Muse Wonder writes around or beside one JSON object, a member the
// proposal schema does not name, a value written as a JSON text in a
// string, a value over the projection's bounds or a type the schema does
// not take is refused: invalid_output, not retried, issues with paths and
// fixed messages, never the text. The model's answer is handed back for
// H-057's one repair, but for an answer over the output budget. Muse Spark
// still refuses a native value: its request asks for JSON texts.
func TestMuseWonderAnswersThatAreRefused(t *testing.T) {
	good, encoded := encodedForm([2]string{`{"op":"test","path":"/resources/0/id"`, `"ict_tester"`}, [2]string{`{"op":"replace","path":"/resources/0/capacity"`, `4`})
	with := func(old, new string) string { return strings.Replace(good, old, new, 1) }
	big := strings.Repeat("x", structured.MaxEncodedValueBytes)
	for name, c := range map[string]struct {
		text  string
		path  string // the first issue's; "-" for any
		prior bool
	}{
		"prose before":              {"Here is the proposal for SECRET-SENTINEL:\n" + good, "", true},
		"a code fence":              {"```json\n" + good + "\n```", "", true},
		"two objects":               {good + good, "", true},
		"a trusted member":          {with(`"requires_confirmation":true`, `"requires_confirmation":true,"approved":true`), "-", true},
		"a member of a change":      {with(`"value":4}`, `"value":4,"from":"/resources/1/capacity"}`), "-", true},
		"a member of a source":      {with(`"requires_confirmation":true`, `"sources":[{"url":"https://example.com/a","note":"SECRET-SENTINEL"}],"requires_confirmation":true`), "-", true},
		"a JSON text of a string":   {with(`"value":"ict_tester"`, `"value":"\"ict_tester\""`), "/changes/0/value", true},
		"the projection's form":     {encoded, "/changes/0/value", true},
		"a value over 64 KiB":       {with(`"value":4}`, `"value":"`+big+`"}`), "/changes/1/value", true},
		"a type the schema refuses": {with(`"confidence":0.9`, `"confidence":"high"`), "-", true},
		"a needs_input with changes": {`{"status":"needs_input","summary":"Which?","assumptions":[],"questions":["Which?"],"changes":[],"requires_confirmation":true}`,
			"-", true},
		"over the output budget": {with(`"Service times stay the same."`, `"`+strings.Repeat("y", 300<<10)+`"`), "", false},
	} {
		s := newServer(t, map[string][]reply{messagesPath: {fromWonder(ok(c.text))}})
		cl, b, _, _ := meteredClient(t, s, 100*spendUSD)
		meta.SetModels(b, model, wonder)
		res, err := askModel(t, cl, wonder)
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Code != provider.CodeInvalidOutput || pe.Retryable() || len(pe.Issues) == 0 {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if c.path != "-" && pe.Issues[0].Path != c.path {
			t.Errorf("%s: issues %+v, want the first at %q", name, pe.Issues, c.path)
		}
		for _, is := range pe.Issues {
			if is.Code != provider.OutputInvalid {
				t.Errorf("%s: issue %+v", name, is)
			}
		}
		leaks(t, name, fmt.Sprintf("%v %+v %+v", err, pe.Issues, res.Metadata), "SECRET-SENTINEL", "ict_tester", "xxxx", "yyyy")
		if res.Prior.Present() != c.prior || (c.prior && string(res.Prior.Bytes()) != c.text) {
			t.Errorf("%s: prior %v, want %v", name, res.Prior.Present(), c.prior)
		}
		if len(s.bodies) != 1 {
			t.Errorf("%s: %d requests", name, len(s.bodies))
		}
	}
	// Muse Spark asks for JSON texts: a native value is refused at its place
	// (the string ict_tester is no JSON text, the number 4 no string).
	s := newServer(t, map[string][]reply{messagesPath: {ok(good)}})
	cl, _ := client(t, s)
	_, err := ask(t, cl)
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Code != provider.CodeInvalidOutput || len(pe.Issues) != 2 {
		t.Fatalf("Muse Spark's native answer: %v", err)
	}
	if paths := []string{pe.Issues[0].Path, pe.Issues[1].Path}; !slices.Contains(paths, "/changes/0/value") || !slices.Contains(paths, "/changes/1/value") {
		t.Errorf("Muse Spark's native answer: issues at %v", paths)
	}
}

// H-057's repair of a Muse Wonder answer the server refused for a value
// written as a JSON text: the job's own path (repair.Once, aicontext.Revise)
// sends one more Muse Wonder request, again without output_config and with
// the same system text, the model's answer as it wrote it as the assistant
// turn and the server's follow-up naming the place; its answer is reviewed
// as the first was.
func TestTheRepairOfAMuseWonderAnswer(t *testing.T) {
	refused := `{"status":"proposed","summary":"Raise it.","assumptions":[],"risk_level":"low","confidence":0.9,` +
		`"changes":[{"op":"test","path":"/resources/0/id","value":"\"ict_tester\""},{"op":"replace","path":"/resources/0/capacity","value":3}],` +
		`"expected_effects":["The queue may fall; simulate to know."],"requires_confirmation":true}`
	s := newServer(t, map[string][]reply{messagesPath: {fromWonder(ok(refused)), fromWonder(ok(answerJ))}})
	c, b, _, _ := meteredClient(t, s, 100*spendUSD)
	meta.SetModels(b, model, wonder)
	first := provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: sealed(t), Attribution: attribution, Model: wonder}
	res, err := c.GenerateModelProposal(context.Background(), first)
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Code != provider.CodeInvalidOutput || len(pe.Issues) != 1 || pe.Issues[0].Path != "/changes/0/value" || !res.Prior.Present() {
		t.Fatalf("the first answer: %v, prior %v", err, res.Prior.Present())
	}
	checked := 0
	out, rec, err := repair.Once(context.Background(), c, first, res.Prior, review.InvalidOutput(pe.Issues),
		func(o provider.ProposalOutput) (review.Outcome, error) {
			checked++
			return review.Outcome{Status: review.NeedsInput, Summary: o.Summary}, nil
		}, nil)
	if err != nil || !rec.Revised() || rec.Outcome != repair.NeedsInput || out.Status != review.NeedsInput || checked != 1 || rec.Call.Model != wonder {
		t.Fatalf("the repair: %+v %+v %v", out, rec, err)
	}
	if len(s.bodies) != 2 {
		t.Fatalf("%d requests", len(s.bodies))
	}
	body := s.bodies[1]
	if keys := slices.Sorted(maps.Keys(body)); !slices.Equal(keys, []string{"max_tokens", "messages", "model", "system"}) {
		t.Errorf("the repair's fields %v", keys)
	}
	if body["model"] != wonder || body["max_tokens"] != float64(provider.SlowModelMaxOutputTokens) {
		t.Errorf("the repair: model %v, max_tokens %v", body["model"], body["max_tokens"])
	}
	if text := plainJSONSystem(t, "the repair", body, first.Prompt.System); text != plainJSONSystem(t, "the first", s.bodies[0], first.Prompt.System) {
		t.Error("the repair's system text is not the first's")
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("%d messages", len(msgs))
	}
	if want := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": refused}}}; !reflect.DeepEqual(msgs[1], want) {
		t.Errorf("the assistant turn %v", msgs[1])
	}
	follow, _ := msgs[2].(map[string]any)["content"].([]any)
	text, _ := follow[0].(map[string]any)["text"].(string)
	if msgs[2].(map[string]any)["role"] != "user" || !strings.Contains(text, "SERVER_REVIEW") || !strings.Contains(text, `"path":"/changes/0/value"`) {
		t.Errorf("the follow-up does not name the place")
	}
	leaks(t, "the repair request", string(s.raw[1]))
}
