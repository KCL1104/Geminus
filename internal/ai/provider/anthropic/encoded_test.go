package anthropic_test

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/anthropic"
	"github.com/kcl1104/geminus/internal/aievals"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// The live smoke of 2026-10-26 was refused (AI_REQUEST_REJECTED, 0
// tokens): count_tokens with the same output_config.format answered 400
// invalid_request_error "output_config.format.schema: Empty schema ({})
// that accepts any JSON value is not supported." These tests hold the
// fix on the local test server: the request's schema is inside the
// official limits for every role, a JSON Patch value comes as a JSON text
// in a string and is decoded before the full schema is checked, and what
// does not decode is an invalid answer.

// rolePrompt is a sealed prompt of role.
func rolePrompt(t *testing.T, role provider.Role) provider.Prompt {
	t.Helper()
	intent, _ := jsontext.AppendQuote(nil, "raise the ICT tester capacity")
	p, err := provider.Seal(provider.Prompt{OrganizationID: org, Role: role, TemplateVersion: string(role) + "-test", Locale: locale.English,
		System: "rules", Blocks: []provider.Block{{Kind: provider.UntrustedData, Name: "USER_INTENT", Content: intent}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The regression the smoke found: the schema each role's request carries
// has no empty schema and nothing else the official limits refuse
// (anthropic.SubsetViolations, the guard of guard_test.go).
func TestTheRequestSchemaIsInsideTheLimits(t *testing.T) {
	attr := provider.Attribution{TargetType: "ai_proposal", TargetID: "aip_01M4YDQK00TA00000000000001", RequestID: "req_01M4YDQK00TA00000000000001"}
	for _, role := range provider.Roles {
		t.Run(string(role), func(t *testing.T) {
			s := newServer(t, map[string][]reply{keyA: {ok(answerJ)}})
			c, _ := client(t, s, config(keyA))
			ctx, p := context.Background(), rolePrompt(t, role)
			switch role {
			case provider.RoleModeler, provider.RoleScenario:
				mode := provider.ModeEdit
				if role == provider.RoleScenario {
					mode = provider.ModeScenario
				}
				_, _ = c.GenerateModelProposal(ctx, provider.ProposalContext{OrganizationID: org, Mode: mode, Prompt: p, Attribution: attr})
			case provider.RoleAnalyst:
				attr.TargetType = "ai_analysis"
				_, _ = c.AnalyzeSimulation(ctx, provider.AnalysisContext{OrganizationID: org, Prompt: p, Attribution: attr})
			case provider.RoleImporter:
				attr.TargetType = "import"
				_, _ = c.ProposeImportMapping(ctx, provider.MappingContext{OrganizationID: org, Prompt: p, Attribution: attr})
			}
			if len(s.bodies) != 1 {
				t.Fatalf("%d requests", len(s.bodies))
			}
			schema := s.bodies[0]["output_config"].(map[string]any)["format"].(map[string]any)["schema"]
			raw, _ := json.Marshal(schema)
			if v := anthropic.SubsetViolations(schema); len(v) > 0 || strings.Contains(string(raw), "{}") {
				t.Errorf("the %s request's schema: %q", role, v)
			}
			if role == provider.RoleModeler && strings.Count(string(raw), "JSON text") != 3 {
				t.Errorf("the modeler's schema has %d encoded values, want 3", strings.Count(string(raw), "JSON text"))
			}
		})
	}
}

// encodedChanges is MOD-002's patch and more, as the projection asks for
// it: each value a JSON text in a string (a string with its quotes, a
// number, an object, null, false, an array, escapes).
const encodedChanges = `[{"op":"test","path":"/resources/1/id","value":"\"ci_runner\""},` +
	`{"op":"replace","path":"/resources/1/capacity","value":"6"},` +
	`{"op":"add","path":"/nodes/-","value":"{\"id\":\"node_new\",\"type\":\"delay\",\"tags\":[1,-0.5e3,null]}"},` +
	`{"op":"replace","path":"/metadata/note","value":"null"},` +
	`{"op":"replace","path":"/flag","value":" false "},` +
	`{"op":"add","path":"/list/-","value":"[\"a\",{\"b\":12345678901234567890}]"},` +
	`{"op":"test","path":"/name","value":"\"Ünïcödé \\\"quoted\\\" \\u00e9\""},` +
	`{"op":"remove","path":"/resources/2"}]`

func proposed(changes string) string {
	return `{"status":"proposed","summary":"Raise the CI runner capacity from 3 to 6.","assumptions":[],"risk_level":"low","confidence":0.9,` +
		`"changes":` + changes + `,"expected_effects":["Waiting may fall; a simulation will tell."],"requires_confirmation":true}`
}

// An answer whose values are JSON texts decodes into the canonical patch:
// the output's values are the JSON values themselves, the raw answer
// passes the full 2020-12 schema, and its hash is of the decoded answer.
func TestEncodedValuesDecode(t *testing.T) {
	s := newServer(t, map[string][]reply{keyA: {ok(proposed(encodedChanges))}})
	c, _ := client(t, s, config(keyA))
	res, err := ask(t, c)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`"ci_runner"`, `6`, `{"id":"node_new","type":"delay","tags":[1,-0.5e3,null]}`, `null`, `false`,
		`["a",{"b":12345678901234567890}]`, `"Ünïcödé \"quoted\" \u00e9"`, ``}
	if len(res.Output.Changes) != len(want) {
		t.Fatalf("changes %+v", res.Output.Changes)
	}
	for i, op := range res.Output.Changes {
		if string(op.Value) != want[i] {
			t.Errorf("change %d (%s %s): value %s, want %s", i, op.Op, op.Path, op.Value, want[i])
		}
	}
	if issues, err := validate.Document(res.Raw, "proposal-output.schema.json", provider.OutputInvalid); err != nil || len(issues) > 0 {
		t.Errorf("the decoded answer under the full schema: %v %v", issues, err)
	}
	if strings.Contains(string(res.Raw), `\"ci_runner\"`) || res.Metadata.OutputHash == "" {
		t.Errorf("the raw answer is not the decoded one: %s (hash %q)", res.Raw, res.Metadata.OutputHash)
	}
}

// What does not decode is an invalid answer (invalid_output, the code
// the pipeline already turns into an invalid proposal, never ready), with
// the place and a fixed message, never the answer's text; the call is
// still settled for what it was billed.
func TestEncodedValuesThatDoNotDecode(t *testing.T) {
	for name, value := range map[string]string{
		"not JSON":      `"Line A SENTINEL"`,
		"trailing data": `"6 SENTINEL"`,
		"garbage after": `"{}]"`,
		"a duplicate":   `"{\"a\":1,\"a\":2}"`,
		"too large":     `"\"` + strings.Repeat("a", anthropic.MaxEncodedValueBytes) + `\""`,
		"not a string":  `6`,
	} {
		t.Run(name, func(t *testing.T) {
			changes := `[{"op":"test","path":"/resources/1/id","value":"\"ci_runner\""},{"op":"replace","path":"/resources/1/capacity","value":` + value + `}]`
			s := newServer(t, map[string][]reply{keyA: {ok(proposed(changes))}})
			c, _, m := meteredClient(t, s, config(keyA), 10*spendUSD)
			res, err := ask(t, c)
			var pe *provider.Error
			if !errors.As(err, &pe) || pe.Code != provider.CodeInvalidOutput || pe.Retryable() || len(pe.Issues) != 1 ||
				pe.Issues[0].Path != "/changes/1/value" || pe.Issues[0].Code != provider.OutputInvalid {
				t.Fatalf("%v %+v", err, pe)
			}
			if msg := fmt.Sprintf("%v %+v %#v", err, pe.Issues, res); strings.Contains(msg, "SENTINEL") || strings.Contains(msg, "Line A") {
				t.Errorf("the error holds the answer's text: %s", msg)
			}
			if res.Raw != nil || res.Metadata.Usage.InputTokens != 120 || m.Spent() == 0 {
				t.Errorf("raw %s usage %+v spent %d", res.Raw, res.Metadata.Usage, m.Spent())
			}
		})
	}
}

// Review round 1, M1, end to end over HTTP: the reviewer's answer (Q1) on
// MOD-002's base, a test and a replace whose values are not JSON texts
// (no inner quotes) and an add, then a remove, of a value nested deeper
// than MaxEncodedValueDepth. Before the fix, at 9998 to 10000 levels the
// rewrite failed, the answer went on undecoded with no issue, and the
// server's review made it ready with the raw strings as values. Now each
// of the three places is an issue (none is lost), the call is
// invalid_output with no answer, and the live eval's case ends invalid,
// never ready.
func TestADeepValueMakesTheAnswerInvalid(t *testing.T) {
	for _, depth := range []int{anthropic.MaxEncodedValueDepth + 1, 9997, 9998, 9999, 10000} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			deep, err := json.Marshal(strings.Repeat("[", depth) + strings.Repeat("]", depth))
			if err != nil {
				t.Fatal(err)
			}
			answer := `{"status":"proposed","summary":"Rename the model.","assumptions":[],"risk_level":"low","confidence":0.9,"changes":[` +
				`{"op":"test","path":"/nodes/1/id","value":"code_review"},` +
				`{"op":"add","path":"/nodes/1/x","value":` + string(deep) + `},` +
				`{"op":"remove","path":"/nodes/1/x"},` +
				`{"op":"replace","path":"/name","value":"Renamed by a raw string"}],"expected_effects":[],"requires_confirmation":true}`

			s := newServer(t, map[string][]reply{keyA: {ok(answer)}})
			c, _ := client(t, s, config(keyA))
			res, err := ask(t, c)
			var pe *provider.Error
			if !errors.As(err, &pe) || pe.Code != provider.CodeInvalidOutput || res.Raw != nil || res.Output.Changes != nil {
				t.Fatalf("the client: %v, %d changes", err, len(res.Output.Changes))
			}
			var paths []string
			for _, is := range pe.Issues {
				paths = append(paths, is.Path)
				if is.Code != provider.OutputInvalid || strings.Contains(is.Message, "code_review") || strings.Contains(is.Message, "Renamed") {
					t.Errorf("issue %+v", is)
				}
			}
			slices.Sort(paths)
			if want := []string{"/changes/0/value", "/changes/1/value", "/changes/3/value"}; !slices.Equal(paths, want) {
				t.Errorf("issues at %q, want %q", paths, want)
			}

			s = newServer(t, map[string][]reply{keyA: {ok(answer)}})
			r := liveRun(t, s, aievals.SmokeCase)
			o := r.Outcomes[0]
			if o.Status != "invalid" || o.Passed || !slices.Contains(o.Issues, provider.OutputInvalid) || o.PatchHash != "" || o.ServerRisk != "" || o.Risk != "" {
				t.Errorf("the live eval's case: %+v", o)
			}
			for gate, n := range r.Gates {
				if n != 0 {
					t.Errorf("gate %s: %d", gate, n)
				}
			}
		})
	}
}

// The full schema still decides after the decoding. A patch value is any
// JSON value under it, so a decoded value cannot break it by itself; what
// the projection dropped can: the path's pattern, the confidence bound,
// at least one change, the summary's length. Each is an invalid answer.
func TestDecodedAnswersMeetTheFullSchema(t *testing.T) {
	good := `[{"op":"replace","path":"/resources/1/capacity","value":"6"}]`
	for name, answer := range map[string]string{
		"a path without its slash":  proposed(`[{"op":"replace","path":"resources/1/capacity","value":"6"}]`),
		"confidence above 1":        strings.Replace(proposed(good), `"confidence":0.9`, `"confidence":1.5`, 1),
		"no change":                 proposed(`[]`),
		"an empty summary":          strings.Replace(proposed(good), `"summary":"Raise the CI runner capacity from 3 to 6."`, `"summary":""`, 1),
		"a decoded value in a move": proposed(`[{"op":"move","path":"/a","value":"6"}]`),
	} {
		s := newServer(t, map[string][]reply{keyA: {ok(answer)}})
		c, _ := client(t, s, config(keyA))
		res, err := ask(t, c)
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Code != provider.CodeInvalidOutput || len(pe.Issues) == 0 || res.Raw != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// T-053 schema follow-up: a provider error records the HTTP status and the
// error type from the API's fixed list
// (https://platform.claude.com/docs/en/api/errors, 2026-10-26), "other" for
// any other; never the message. A call that ended without an error status
// names neither.
func TestTheProviderErrorType(t *testing.T) {
	for _, c := range []struct {
		r      reply
		code   provider.Code
		status int
		kind   string
	}{
		{apiError(400, "invalid_request_error"), provider.CodeRejected, 400, "invalid_request_error"},
		{apiError(404, "not_found_error"), provider.CodeRejected, 404, "not_found_error"},
		{apiError(413, "request_too_large"), provider.CodeRejected, 413, "request_too_large"},
		{apiError(409, "conflict_error"), provider.CodeUnavailable, 409, "conflict_error"},
		{apiError(429, "rate_limit_error"), provider.CodeRateLimited, 429, "rate_limit_error"},
		{apiError(500, "api_error"), provider.CodeUnavailable, 500, "api_error"},
		{apiError(504, "timeout_error"), provider.CodeUnavailable, 504, "timeout_error"},
		{apiError(529, "overloaded_error"), provider.CodeUnavailable, 529, "overloaded_error"},
		{apiError(400, "made_up_error"), provider.CodeRejected, 400, "other"},
		{apiError(400, "invalid_request_error BODY-SENTINEL"), provider.CodeRejected, 400, "other"},
		{apiError(422, "Invalid_Request_Error"), provider.CodeRejected, 422, "other"},
		{reply{status: 400, body: `{"type":"error","error":{"message":"BODY-SENTINEL"}}`}, provider.CodeRejected, 400, "other"},
		{reply{status: 400, body: `BODY-SENTINEL, not JSON`}, provider.CodeRejected, 400, "other"},
	} {
		name := fmt.Sprintf("%d %s", c.r.status, c.kind)
		s := newServer(t, map[string][]reply{keyA: {c.r}})
		cl, _ := client(t, s, config(keyA), func(b *provider.Budget) { b.MaxRetries = 0 })
		_, err := ask(t, cl)
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Code != c.code || pe.Status != c.status || pe.ErrorType != c.kind || pe.Credential != "org-a" {
			t.Errorf("%s: %v %+v", name, err, pe)
			continue
		}
		if msg := fmt.Sprintf("%v %+v %#v", err, err, err); strings.Contains(msg, "BODY-SENTINEL") || !strings.Contains(err.Error(), c.kind) {
			t.Errorf("%s: %s", name, msg)
		}
	}
	// A 401 after an attempt that may have been billed fails the call with
	// the 401's status and type (review round 1, M3).
	s := newServer(t, map[string][]reply{keyA: {{hangUp: true}, apiError(401, "authentication_error")}, keyB: {ok(answerJ)}})
	cl, _ := client(t, s, config(keyA, keyB))
	if _, err := ask(t, cl); provider.CodeOf(err) != provider.CodeUnavailable || !hasType(err, 401, "authentication_error") {
		t.Errorf("401 after a lost connection: %v", err)
	}
	// Every credential refused: no single status, no type.
	s = newServer(t, map[string][]reply{keyA: {apiError(402, "billing_error")}, keyB: {apiError(401, "authentication_error")}})
	cl, _ = client(t, s, config(keyA, keyB))
	if _, err := ask(t, cl); provider.CodeOf(err) != provider.CodeCredentialsExhausted || !hasType(err, 0, "") {
		t.Errorf("both refused: %v", err)
	}
	// No answer at all: no status, no type.
	s = newServer(t, map[string][]reply{keyA: {{hangUp: true}}})
	cl, _ = client(t, s, config(keyA), func(b *provider.Budget) { b.MaxRetries = 0 })
	if _, err := ask(t, cl); provider.CodeOf(err) != provider.CodeUnavailable || !hasType(err, 0, "") {
		t.Errorf("a lost connection: %v", err)
	}
	// The sentinels still match an error with a type.
	if !errors.Is(&provider.Error{Code: provider.CodeInvalidOutput, Status: 400, ErrorType: "other"}, provider.ErrInvalidOutput) {
		t.Error("errors.Is no longer matches by code")
	}
}

func hasType(err error, status int, kind string) bool {
	var pe *provider.Error
	return errors.As(err, &pe) && pe.Status == status && pe.ErrorType == kind
}
