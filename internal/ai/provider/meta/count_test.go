package meta_test

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/meta"
	"github.com/kcl1104/geminus/internal/ai/provider/structured"
	"github.com/kcl1104/geminus/internal/ai/spend"
)

// H-037 (the owner's answer to T-055 review round 1, M1): a request with
// images is counted with POST /v1/messages/count_tokens before it is
// reserved, and reserved at the count plus 10% (the output bound and the
// attempts as D-121 says); a text request keeps D-111's quote and makes no
// count call. A count that fails, or whose answer is not a sane count,
// fails the call before anything else is sent: nothing is reserved, so no
// reservation is ever smaller than the rule's.
//
// Sources (https://dev.meta.ai/docs, retrieved 2026-10-26; hashes in
// checks/evidence/2026-10-26-T-055-r1/sources.txt): /docs/protocols/messages
// "Send the same body as POST /v1/messages minus the max_tokens
// requirement. The response is { "input_tokens": <integer> }";
// /docs/token-counting "Images and files contribute to the input total";
// /docs/api-reference/messages/schemas: the count request's messages take
// image blocks, and its fields have no max_tokens.

// imageRequest is a request of the test prompt with images, as the client
// sends it under the default budget.
func imageRequest(t *testing.T, images ...provider.Image) provider.Request {
	t.Helper()
	b := provider.DefaultBudget()
	return provider.Request{Prompt: sealed(t, images...), OutputSchema: "proposal-output.schema.json", MaxOutputTokens: b.MaxOutputTokens,
		AttemptTimeout: b.AttemptTimeout, MaxRetries: b.MaxRetries}
}

// The count goes first, with the body the call sends but max_tokens, the
// key as a bearer token only; the reservation is every attempt at the
// count plus 10% and max_tokens.
func TestAnImageRequestIsCountedFirst(t *testing.T) {
	s := newServer(t, map[string][]reply{countPath: {counted("3000")}, messagesPath: {ok(answerJ)}})
	b, _ := backend(t, s)
	charges := &chargeLog{}
	budget := provider.DefaultBudget()
	c, err := provider.NewMetered(b, budget, charges)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ask(t, c, pngImage(t, 64, 48))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.order(); !slices.Equal(got, []string{countPath, messagesPath}) {
		t.Fatalf("requests %v, want the count, then the message", got)
	}
	count, msg := s.bodies[0], s.bodies[1]
	if keys := slices.Sorted(maps.Keys(count)); !slices.Equal(keys, []string{"messages", "model", "output_config", "system"}) {
		t.Errorf("the count's top-level fields %v", keys)
	}
	want := maps.Clone(msg)
	delete(want, "max_tokens")
	if !reflect.DeepEqual(count, want) {
		t.Error("the count's body is not the message's without max_tokens")
	}
	if h := s.headers[0]; h.Get("Authorization") != "Bearer "+key || h.Get("X-Api-Key") != "" || h.Get("Content-Type") != "application/json" {
		t.Errorf("the count's headers: bearer %v, x-api-key %q, content type %q", h.Get("Authorization") == "Bearer "+key, h.Get("X-Api-Key"), h.Get("Content-Type"))
	}
	p, _ := spend.PriceOf(model)
	if want := int64(1+budget.MaxRetries) * p.AttemptBound(3300, budget.MaxOutputTokens); len(charges.charges) != 1 || charges.charges[0] != want {
		t.Errorf("reserved %v, want %d (3 attempts at 3,000 + 10%% input tokens and max_tokens)", charges.charges, want)
	}
	m, _ := json.Marshal(res.Metadata)
	leaks(t, "the metadata", string(m))
}

// The margin is 10% rounded up, never past the context window (no request
// is billed for more input: input_tokens + max_tokens must fit in it). A
// count's other members are not read.
func TestTheCountsMargin(t *testing.T) {
	for n, want := range map[string]int64{
		"1": 2, "9": 10, "10": 11, "11": 13, "3000": 3300, "3001": 3302,
		"953250": 1_048_575, "953251": meta.ContextWindowTokens, "1048576": meta.ContextWindowTokens,
	} {
		s := newServer(t, map[string][]reply{countPath: {counted(n)}})
		b, _ := backend(t, s)
		q, err := b.Quote(context.Background(), imageRequest(t, pngImage(t, 8, 8)))
		if err != nil || q.Model != model || q.MaxInputTokens != want || s.count(countPath) != 1 {
			t.Errorf("a count of %s: %+v %v (%d counts), want %d", n, q, err, s.count(countPath), want)
		}
		// T-056 review round 1: the quote says the count itself too (the
		// photo smoke reports it against the tokens billed).
		if fmt.Sprint(q.Counted) != n {
			t.Errorf("a count of %s: Counted %d", n, q.Counted)
		}
	}
	// A text request is not counted: its quote is the adapter's bound.
	ts := newServer(t, map[string][]reply{})
	tb, _ := backend(t, ts)
	if q, err := tb.Quote(context.Background(), provider.Request{Prompt: sealed(t), OutputSchema: "proposal-output.schema.json"}); err != nil || q.Counted != 0 {
		t.Errorf("a text request: %+v %v", q, err)
	}
	s := newServer(t, map[string][]reply{countPath: {{status: 200, body: `{"object":"BODY-SENTINEL","input_tokens":3000,"extra":[1,2]}`}}})
	b, _ := backend(t, s)
	if q, err := b.Quote(context.Background(), imageRequest(t, pngImage(t, 8, 8))); err != nil || q.MaxInputTokens != 3300 {
		t.Errorf("other members: %+v %v", q, err)
	}
}

// A count that fails, or whose answer is not one integer from 1 to the
// context window in the documented shape, ends the call with a typed code
// before the message is sent: nothing is reserved or spent, so no
// reservation is smaller than the count's rule (or the whole window's).
// The count's attempts follow the call's rules: retried as the call's are,
// the key refused as on a call, no redirect followed, the response bound,
// the documented error types only, and never the provider's text.
func TestACountThatCannotBeTrustedSendsNothing(t *testing.T) {
	trap := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("the count followed a redirect (bearer sent: %v)", r.Header.Get("Authorization") != "")
	}))
	defer trap.Close()
	body := func(b string) reply {
		return reply{status: 200, header: map[string]string{"Content-Type": "application/json"}, body: b}
	}
	unavailable, rejected, refused := provider.CodeUnavailable, provider.CodeRejected, provider.CodeCredentialsExhausted
	for name, c := range map[string]struct {
		r         reply
		code      provider.Code
		hits      int
		errorType string // and the status with it; none for an answer that cannot be read
	}{
		"zero":               {counted("0"), unavailable, 1, ""},
		"negative":           {counted("-3000"), unavailable, 1, ""},
		"a fraction":         {counted("3000.5"), unavailable, 1, ""},
		"an exponent":        {counted("3e3"), unavailable, 1, ""},
		"a string":           {counted(`"3000"`), unavailable, 1, ""},
		"null":               {counted("null"), unavailable, 1, ""},
		"true":               {counted("true"), unavailable, 1, ""},
		"an object":          {counted(`{"tokens":3000}`), unavailable, 1, ""},
		"above the window":   {counted("1048577"), unavailable, 1, ""},
		"past int64":         {counted("99999999999999999999"), unavailable, 1, ""},
		"no input_tokens":    {body(`{"tokens":3000}`), unavailable, 1, ""},
		"another spelling":   {body(`{"Input_Tokens":3000}`), unavailable, 1, ""},
		"an array":           {body(`[3000]`), unavailable, 1, ""},
		"a bare number":      {body(`3000`), unavailable, 1, ""},
		"not JSON":           {body(`input_tokens: 3000`), unavailable, 1, ""},
		"two documents":      {body(`{"input_tokens":3000}{"input_tokens":3000}`), unavailable, 1, ""},
		"duplicate members":  {body(`{"input_tokens":3000,"input_tokens":3000}`), unavailable, 1, ""},
		"invalid UTF-8":      {body("{\"input_tokens\":3000,\"note\":\"\xff\"}"), unavailable, 1, ""},
		"empty":              {body(""), unavailable, 1, ""},
		"a 201":              {reply{status: 201, body: counted("3000").body}, unavailable, 1, ""},
		"too large":          {body(`{"input_tokens":3000` + strings.Repeat(" ", meta.MaxResponseBytes) + `}`), unavailable, 1, ""},
		"lost connection":    {reply{hangUp: true}, unavailable, 3, ""},
		"cut body":           {reply{status: 200, body: counted("3000").body, cutBody: true}, unavailable, 3, ""},
		"400":                {apiError(400, "invalid_request_error"), rejected, 1, "invalid_request_error"},
		"404 without a body": {reply{status: 404}, rejected, 1, meta.ErrorTypeOther},
		"413":                {apiError(413, "invalid_request_error"), rejected, 1, "invalid_request_error"},
		"401":                {apiError(401, "authentication_error"), refused, 1, "authentication_error"},
		"402":                {apiError(402, "billing_error"), refused, 1, meta.ErrorTypeOther},
		"403":                {apiError(403, "permission_error"), refused, 1, "permission_error"},
		"429":                {apiError(429, "rate_limit_error"), provider.CodeRateLimited, 3, "rate_limit_error"},
		"500":                {apiError(500, "api_error"), unavailable, 3, "api_error"},
		"503":                {apiError(503, "overloaded_error"), unavailable, 3, "overloaded_error"},
		"504":                {apiError(504, "api_error"), unavailable, 1, "api_error"},
		"an unknown type":    {apiError(400, "weird_error"), rejected, 1, meta.ErrorTypeOther},
		"a redirect":         {reply{status: 307, header: map[string]string{"Location": trap.URL + "/v1/messages/count_tokens"}}, rejected, 1, meta.ErrorTypeOther},
	} {
		s := newServer(t, map[string][]reply{countPath: {c.r}, messagesPath: {ok(answerJ)}})
		cl, _, m, _ := meteredClient(t, s, 100*spendUSD)
		res, err := ask(t, cl, pngImage(t, 64, 48))
		status := 0
		if c.errorType != "" {
			status = c.r.status
		}
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Code != c.code || pe.Status != status || pe.ErrorType != c.errorType {
			t.Errorf("%s: %#v, want %s (status %d, type %q)", name, err, c.code, status, c.errorType)
			continue
		}
		if s.count(countPath) != c.hits || s.count(messagesPath) != 0 {
			t.Errorf("%s: %d counts (want %d) and %d messages (want none)", name, s.count(countPath), c.hits, s.count(messagesPath))
		}
		if m.Committed() != 0 || m.Spent() != 0 || res.Metadata.SpendReservation != "" || res.Metadata.CostNanoUSD != 0 {
			t.Errorf("%s: committed %d, spent %d, metadata %+v: a count that cannot be trusted reserves nothing", name, m.Committed(), m.Spent(), res.Metadata)
		}
		if (c.code == provider.CodeRateLimited || c.code == provider.CodeUnavailable) != pe.Retryable() {
			t.Errorf("%s: retryable %v", name, pe.Retryable())
		}
		md, _ := json.Marshal(res.Metadata)
		leaks(t, name, fmt.Sprintf("%v %+v %#v %s", err, err, err, md), s.srv.URL, trap.URL)
	}
}

// The count runs within the call's deadline (the budget's Timeout) and
// stops when the caller cancels; neither sends the message or reserves.
func TestTheCountKeepsTheCallsDeadline(t *testing.T) {
	slow := counted("3000")
	slow.delay = 2 * time.Second
	s := newServer(t, map[string][]reply{countPath: {slow}, messagesPath: {ok(answerJ)}})
	c, _, m, _ := meteredClient(t, s, 100*spendUSD, func(b *provider.Budget) { b.Timeout, b.AttemptTimeout = 300*time.Millisecond, 300*time.Millisecond })
	start := time.Now()
	_, err := ask(t, c, pngImage(t, 8, 8))
	if provider.CodeOf(err) != provider.CodeTimeout || !errors.Is(err, context.DeadlineExceeded) || s.count(messagesPath) != 0 || m.Committed() != 0 {
		t.Errorf("past the deadline: %v (%d messages, %d reserved)", err, s.count(messagesPath), m.Committed())
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Errorf("took %v", d)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)
	c, _, m, _ = meteredClient(t, s, 100*spendUSD)
	if _, err := askCtx(t, ctx, c, pngImage(t, 8, 8)); provider.CodeOf(err) != provider.CodeCancelled || !errors.Is(err, context.Canceled) ||
		s.count(messagesPath) != 0 || m.Committed() != 0 {
		t.Errorf("cancelled: %v (%d messages, %d reserved)", err, s.count(messagesPath), m.Committed())
	}
}

// A key refused on the count is refused for the calls after it, as on a
// call (one key, D-121 (d)): a later call names it in FailedOver and sends
// nothing, and a later image request is not counted again.
func TestARefusedCountRefusesTheKey(t *testing.T) {
	s := newServer(t, map[string][]reply{countPath: {apiError(401, "authentication_error")}, messagesPath: {ok(answerJ)}})
	c, b := client(t, s)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	meta.SetClock(b, func() time.Time { return now })
	if _, err := ask(t, c, pngImage(t, 8, 8)); provider.CodeOf(err) != provider.CodeCredentialsExhausted {
		t.Fatal(err)
	}
	res, err := ask(t, c)
	if provider.CodeOf(err) != provider.CodeCredentialsExhausted || s.count(messagesPath) != 0 || !slices.Equal(res.Metadata.FailedOver, []string{"meta"}) {
		t.Errorf("a text call after the refused count: %v %+v (%d messages)", err, res.Metadata, s.count(messagesPath))
	}
	if _, err := ask(t, c, pngImage(t, 8, 8)); provider.CodeOf(err) != provider.CodeCredentialsExhausted || s.count(countPath) != 1 {
		t.Errorf("an image call after it: %v (%d counts)", err, s.count(countPath))
	}
}

// A count answered with 429 is retried as a call is (backoff and
// Retry-After), then the call goes on with the count it got.
func TestTheCountIsRetried(t *testing.T) {
	s := newServer(t, map[string][]reply{countPath: {apiError(429, "rate_limit_error"), counted("3000")}, messagesPath: {ok(answerJ)}})
	b, w := backend(t, s)
	charges := &chargeLog{}
	budget := provider.DefaultBudget()
	c, err := provider.NewMetered(b, budget, charges)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ask(t, c, pngImage(t, 8, 8)); err != nil {
		t.Fatal(err)
	}
	if got := s.order(); !slices.Equal(got, []string{countPath, countPath, messagesPath}) || len(w.all()) != 1 {
		t.Errorf("requests %v, waits %v", got, w.all())
	}
	p, _ := spend.PriceOf(model)
	if want := int64(1+budget.MaxRetries) * p.AttemptBound(3300, budget.MaxOutputTokens); len(charges.charges) != 1 || charges.charges[0] != want {
		t.Errorf("reserved %v, want %d", charges.charges, want)
	}
}

// A text request is never counted: D-111's quote, no count request (the
// server has no count script, so one would fail the test).
func TestTextIsNeverCounted(t *testing.T) {
	s := newServer(t, map[string][]reply{messagesPath: {ok(answerJ)}})
	b, _ := backend(t, s)
	charges := &chargeLog{}
	budget := provider.DefaultBudget()
	c, err := provider.NewMetered(b, budget, charges)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ask(t, c); err != nil {
		t.Fatal(err)
	}
	if got := s.order(); !slices.Equal(got, []string{messagesPath}) {
		t.Errorf("requests %v", got)
	}
	req := provider.Request{Prompt: sealed(t), OutputSchema: "proposal-output.schema.json", MaxOutputTokens: budget.MaxOutputTokens}
	proj, _ := structured.Project(req.OutputSchema)
	schema, _ := json.Marshal(proj.Schema)
	text := int64(len(req.Prompt.System)+len(req.Prompt.User())+len(schema)) + meta.QuoteOverheadTokens
	q, err := b.Quote(context.Background(), req)
	p, _ := spend.PriceOf(model)
	if err != nil || q.MaxInputTokens != text || len(charges.charges) != 1 || charges.charges[0] != int64(1+budget.MaxRetries)*p.AttemptBound(text, budget.MaxOutputTokens) {
		t.Errorf("quote %+v %v, reserved %v; want D-111's %d", q, err, charges.charges, text)
	}
	if s.count(countPath) != 0 {
		t.Errorf("%d counts", s.count(countPath))
	}
}

// What the call would refuse is not counted either: no request leaves for
// images or a role the approval's data policy does not name, nor for a
// schema outside the documented limits.
func TestTheCountSendsOnlyWhatTheApprovalNames(t *testing.T) {
	s := newServer(t, map[string][]reply{countPath: {counted("3000")}})
	b, _ := backend(t, s)
	meta.SetImages(b, false)
	if _, err := b.Quote(context.Background(), imageRequest(t, pngImage(t, 8, 8))); provider.CodeOf(err) != provider.CodePolicyNotApproved {
		t.Errorf("no images approved: %v", err)
	}
	meta.SetImages(b, true)
	meta.SetRoles(b, "scenario")
	if _, err := b.Quote(context.Background(), imageRequest(t, pngImage(t, 8, 8))); provider.CodeOf(err) != provider.CodePolicyNotApproved {
		t.Errorf("a role the approval does not serve: %v", err)
	}
	b2, _ := backend(t, s)
	meta.SetProject(b2, func(string) (structured.Projection, error) { return structured.Projection{Schema: nested(11)}, nil })
	if _, err := b2.Quote(context.Background(), imageRequest(t, pngImage(t, 8, 8))); err == nil {
		t.Error("a schema outside the limits was quoted")
	}
	if s.count(countPath) != 0 {
		t.Errorf("%d counts", s.count(countPath))
	}
}

// The reservation of a demo-sized photo proposal fits the default
// organization cap (D-110 (b): USD 5 a day) many times over; under the
// context-window bound it fitted once (review round 1, M1).
//
// The prompt has the demo photo prompt's sizes (aicontext.ImageProposal of
// testdata/photos/electronics-line-demo, measured 2026-10-26: system 12,207
// bytes, user text 5,967 bytes) and the demo image itself (a 1200x520 PNG).
// What Meta counts for it is not known before a live call (NOT RUN), so the
// local server answers a pessimistic count: the D-111 bound of its text
// (every byte a token, the projected schema, 4096) and 15,000 tokens for the
// image, ten times what /docs/image-understanding measured for a ~1280px
// image ("roughly 1,300-1,500 tokens"). The photo path's segmentation is
// reserved too: eight concepts (the electronics pack's PhotoConcepts), each
// (1 + retries) images at SAM 3.1's price.
func TestADemoPhotoProposalFitsTheOrganizationCap(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "photos", "electronics-line-demo.png"))
	if err != nil {
		t.Fatal(err)
	}
	im, err := provider.NewImage(provider.ImagePNG, data)
	if err != nil {
		t.Fatal(err)
	}
	if im.Width != 1200 || im.Height != 520 {
		t.Fatalf("the demo image is %dx%d", im.Width, im.Height)
	}
	user, _ := jsontext.AppendQuote(nil, strings.Repeat("u", 5900))
	p, err := provider.Seal(provider.Prompt{OrganizationID: org, Role: provider.RoleModeler, TemplateVersion: "modeler-test", Locale: locale.English,
		System: strings.Repeat("s", 12207), Blocks: []provider.Block{{Kind: provider.UntrustedData, Name: "USER_INTENT", Content: user}}, Images: []provider.Image{im}})
	if err != nil {
		t.Fatal(err)
	}
	proj, _ := structured.Project("proposal-output.schema.json")
	schema, _ := json.Marshal(proj.Schema)
	text := int64(len(p.System)+len(p.User())+len(schema)) + meta.QuoteOverheadTokens
	count := text + 15_000
	s := newServer(t, map[string][]reply{countPath: {counted(strconv.FormatInt(count, 10))}, messagesPath: {ok(answerJ)}})
	b, _ := backend(t, s)
	charges := &chargeLog{}
	budget := provider.DefaultBudget()
	c, err := provider.NewMetered(b, budget, charges)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GenerateModelProposal(context.Background(), provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: p,
		Attribution: attribution}); err != nil {
		t.Fatal(err)
	}
	price, _ := spend.PriceOf(model)
	attempts := int64(1 + budget.MaxRetries)
	want := attempts * price.AttemptBound(count+(count+9)/10, budget.MaxOutputTokens)
	if len(charges.charges) != 1 || charges.charges[0] != want {
		t.Fatalf("reserved %v, want %d", charges.charges, want)
	}
	samPrice, _ := spend.ImagePriceOf("sam-3.1")
	segmentation := 8 * attempts * samPrice
	window := attempts * price.AttemptBound(meta.ContextWindowTokens, budget.MaxOutputTokens)
	fits, before := spend.DefaultOrganizationDay/(want+segmentation), spend.DefaultOrganizationDay/(window+segmentation)
	t.Logf("demo photo proposal: text %d bytes + image, count %d tokens, reserved %d input tokens per attempt; judge %d nano-USD (USD %.4f), segmentation %d nano-USD; "+
		"%d fit the organization's USD 5 a day (the context-window bound: %d nano-USD, USD %.4f, %d fit)",
		len(p.System)+len(p.User()), count, count+(count+9)/10, want, float64(want)/1e9, segmentation, fits, window, float64(window)/1e9, before)
	if fits < 10 || before != 1 {
		t.Errorf("%d demo photo proposals fit the organization cap (want at least 10); %d under the old bound", fits, before)
	}
}
