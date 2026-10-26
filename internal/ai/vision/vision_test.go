package vision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/model"
)

const orgA = "org_01M4YDQK00TA00000000000000"

var attribution = provider.Attribution{TargetType: "ai_proposal", TargetID: "aip_01M4YDQK00TA00000000000001", RequestID: "req_01M4YDQK00TA00000000000001"}

func testImage() provider.Image {
	data := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("line", 40))
	s := sha256.Sum256(data)
	return provider.Image{MediaType: "image/png", Data: data, Width: 1200, Height: 520, SHA256: "sha256:" + hex.EncodeToString(s[:])}
}

var prompts = []string{"SMT line", "AOI machine", "conveyor"}

func box(prompt int, x, y, w, h float64) Box {
	return Box{Prompt: prompt, X: x, Y: y, Width: w, Height: h, Score: 0.9}
}

// segmenterFunc is a test segmenter.
type segmenterFunc struct {
	f     func(ctx context.Context, req Request) (Response, error)
	calls atomic.Int32
	mu    sync.Mutex
	reqs  []Request
}

func (s *segmenterFunc) Name() string { return "test-segmenter" }
func (s *segmenterFunc) Segment(ctx context.Context, req Request) (Response, error) {
	s.calls.Add(1)
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()
	return s.f(ctx, req)
}

func answering(boxes ...Box) *segmenterFunc {
	return &segmenterFunc{f: func(context.Context, Request) (Response, error) {
		return Response{Boxes: boxes, Model: "test-sam", Credential: "meta-a", ProviderRequestID: "rq-1"}, nil
	}}
}

func client(t *testing.T, s Segmenter, mutate ...func(*Budget)) *Client {
	t.Helper()
	b := DefaultBudget()
	for _, m := range mutate {
		m(&b)
	}
	c, err := New(s, b)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func input() Input {
	return Input{OrganizationID: orgA, Image: testImage(), Prompts: prompts, Attribution: attribution}
}

func messages(issues []model.ValidationIssue) []string {
	var out []string
	for _, is := range issues {
		if is.Code != review.OutputInvalid || is.Path != "" || is.Severity != model.SeverityError {
			out = append(out, "unexpected issue shape")
		}
		out = append(out, is.Message)
	}
	return out
}

// The server checks every box before it goes anywhere: inside the image,
// with an area, finite, a score from 0 to 1, a prompt of the request, a
// label that is text, and at most the budget's number of boxes. Each issue
// is a fixed sentence at the root: nothing of the answer is kept.
func TestCheckBoxes(t *testing.T) {
	const w, h = 1200, 520
	good := []Box{box(0, 0, 0, 300, 200), box(1, 900, 320, 300, 200), {Prompt: 2, X: 10.5, Y: 20.25, Width: 1, Height: 1, Score: 0, Label: "AOI-2"}}
	if issues := CheckBoxes(good, w, h, len(prompts), 64); len(issues) != 0 {
		t.Fatalf("good boxes: %v", issues)
	}
	if issues := CheckBoxes(nil, w, h, len(prompts), 64); len(issues) != 0 {
		t.Fatalf("no boxes: %v", issues)
	}
	nan, inf := math.NaN(), math.Inf(1)
	for name, c := range map[string]struct {
		b    Box
		want string
	}{
		"NaN x":             {box(0, nan, 0, 10, 10), review.SentBoxNumber},
		"infinite width":    {box(0, 0, 0, inf, 10), review.SentBoxNumber},
		"-infinite y":       {box(0, 0, math.Inf(-1), 10, 10), review.SentBoxNumber},
		"a huge height":     {box(0, 0, 0, 10, 1e308), review.SentBoxOutside},
		"left of 0":         {box(0, -1, 0, 10, 10), review.SentBoxOutside},
		"above 0":           {box(0, 0, -0.5, 10, 10), review.SentBoxOutside},
		"past the right":    {box(0, 1195, 0, 10, 10), review.SentBoxOutside},
		"past the bottom":   {box(0, 0, 515, 10, 10), review.SentBoxOutside},
		"no width":          {box(0, 0, 0, 0, 10), review.SentBoxOutside},
		"negative height":   {box(0, 0, 100, 10, -20), review.SentBoxOutside},
		"a score above 1":   {Box{Prompt: 0, Width: 1, Height: 1, Score: 1.5}, review.SentBoxNumber},
		"a negative score":  {Box{Prompt: 0, Width: 1, Height: 1, Score: -0.1}, review.SentBoxNumber},
		"a NaN score":       {Box{Prompt: 0, Width: 1, Height: 1, Score: nan}, review.SentBoxNumber},
		"no such prompt":    {box(3, 0, 0, 10, 10), review.SentBoxPrompt},
		"a negative prompt": {box(-1, 0, 0, 10, 10), review.SentBoxPrompt},
		"a label not UTF-8": {Box{Prompt: 0, Width: 1, Height: 1, Score: 0.5, Label: "\xff\xfe"}, review.SentBoxLabel},
		"a label with NUL":  {Box{Prompt: 0, Width: 1, Height: 1, Score: 0.5, Label: "AOI\x00"}, review.SentBoxLabel},
		"a label too long":  {Box{Prompt: 0, Width: 1, Height: 1, Score: 0.5, Label: strings.Repeat("x", MaxLabelBytes+1)}, review.SentBoxLabel},
	} {
		issues := CheckBoxes(append([]Box{good[0]}, c.b), w, h, len(prompts), 64)
		if got := messages(issues); len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	many := make([]Box, 65)
	for i := range many {
		many[i] = box(0, 0, 0, 10, 10)
	}
	if got := messages(CheckBoxes(many, w, h, len(prompts), 64)); len(got) != 1 || got[0] != review.SentTooManyBoxes {
		t.Errorf("65 boxes: %q", got)
	}
	// Several bad boxes say each sentence once.
	bad := []Box{box(0, nan, 0, 1, 1), box(0, inf, 0, 1, 1), box(9, 0, 0, 1, 1)}
	if got := messages(CheckBoxes(bad, w, h, len(prompts), 64)); len(got) != 2 {
		t.Errorf("repeated sentences: %q", got)
	}
}

// Every issue a check returns survives the server's cleaning as it is.
func TestBoxIssuesAreClean(t *testing.T) {
	for _, is := range CheckBoxes([]Box{box(0, -1, 0, 1, 1), box(9, 0, 0, 1, 1), {Score: 2, Width: 1, Height: 1}, {Label: "\xff", Width: 1, Height: 1}}, 10, 10, 1, 64) {
		if review.Clean(is) != is {
			t.Errorf("%+v cleaned to %+v", is, review.Clean(is))
		}
	}
}

func TestNew(t *testing.T) {
	if _, err := New(nil, DefaultBudget()); err == nil {
		t.Error("no segmenter")
	}
	if _, err := New(&billableSeg{}, DefaultBudget()); err == nil {
		t.Error("a billable segmenter without a meter")
	}
	if _, err := NewMetered(&billableSeg{}, DefaultBudget(), nil); err == nil {
		t.Error("no meter")
	}
	for name, edit := range map[string]func(*Budget){
		"no timeout":          func(b *Budget) { b.Timeout = 0 },
		"attempt > timeout":   func(b *Budget) { b.AttemptTimeout = b.Timeout + time.Second },
		"negative retries":    func(b *Budget) { b.MaxRetries = -1 },
		"no boxes":            func(b *Budget) { b.MaxBoxes = 0 },
		"too many boxes":      func(b *Budget) { b.MaxBoxes = 1001 },
		"no concurrent calls": func(b *Budget) { b.MaxConcurrentPerOrganization = 0 },
	} {
		b := DefaultBudget()
		edit(&b)
		if _, err := New(answering(), b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A checked call: the request is the server's (image, prompts, the
// budget's box limit), the boxes come back checked, with the call's
// metadata (labels and counts, never the answer).
func TestSegment(t *testing.T) {
	s := answering(box(0, 10, 10, 300, 200), box(2, 320, 60, 80, 40))
	res, err := client(t, s).Segment(context.Background(), input())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Boxes) != 2 || res.Boxes[1].Prompt != 2 {
		t.Errorf("boxes %+v", res.Boxes)
	}
	m := res.Metadata
	if m.Provider != "test-segmenter" || m.Model != "test-sam" || m.Credential != "meta-a" || m.Boxes != 2 || m.ProviderRequestID != "rq-1" {
		t.Errorf("metadata %+v", m)
	}
	req := s.reqs[0]
	if req.Image.SHA256 != testImage().SHA256 || len(req.Prompts) != 3 || req.MaxBoxes != DefaultBudget().MaxBoxes || req.Reservation != "" {
		t.Errorf("request %+v", req)
	}
	// No boxes is an answer: nothing was found.
	if res, err := client(t, answering()).Segment(context.Background(), input()); err != nil || len(res.Boxes) != 0 {
		t.Errorf("no boxes: %+v %v", res, err)
	}
}

// An answer with a box the server refuses is invalid_output, with the
// issues and nothing of the boxes.
func TestAnUnacceptableAnswerIsInvalidOutput(t *testing.T) {
	s := answering(box(0, 10, 10, 300, 200), box(0, 1100, 10, 300, 200))
	res, err := client(t, s).Segment(context.Background(), input())
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Code != provider.CodeInvalidOutput || len(pe.Issues) != 1 || pe.Issues[0].Message != review.SentBoxOutside {
		t.Fatalf("%v", err)
	}
	if len(res.Boxes) != 0 || res.Metadata.Provider != "test-segmenter" {
		t.Errorf("an invalid answer kept boxes or lost its metadata: %+v", res)
	}
}

// The server's own mistakes are refused before any call: another
// organization, an image that is not its own hash, no prompt, too many or
// an empty or odd prompt.
func TestTheRequestIsChecked(t *testing.T) {
	bad := testImage()
	bad.Data = append([]byte{}, bad.Data...)
	bad.Data[9] ^= 1
	for name, edit := range map[string]func(*Input){
		"a bad organization": func(in *Input) { in.OrganizationID = "org_x" },
		"a changed image":    func(in *Input) { in.Image = bad },
		"no image":           func(in *Input) { in.Image = provider.Image{} },
		"no prompts":         func(in *Input) { in.Prompts = nil },
		"too many prompts":   func(in *Input) { in.Prompts = make([]string, MaxPrompts+1); fillPrompts(in.Prompts) },
		"an empty prompt":    func(in *Input) { in.Prompts = []string{"SMT line", ""} },
		"a long prompt":      func(in *Input) { in.Prompts = []string{strings.Repeat("a", 65)} },
		"a control char":     func(in *Input) { in.Prompts = []string{"SMT\nline"} },
	} {
		s := answering()
		in := input()
		edit(&in)
		if _, err := client(t, s).Segment(context.Background(), in); provider.CodeOf(err) != provider.CodeInvalidRequest || s.calls.Load() != 0 {
			t.Errorf("%s: %v after %d calls", name, err, s.calls.Load())
		}
	}
	for _, code := range []provider.Code{provider.CodeNotConfigured, provider.CodePolicyNotApproved} {
		if _, err := client(t, Disabled{Code: code}).Segment(context.Background(), input()); provider.CodeOf(err) != code {
			t.Errorf("disabled %s: %v", code, err)
		}
	}
}

func fillPrompts(p []string) {
	for i := range p {
		p[i] = "machine " + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
}

// The call ends with its context: cancelled by the caller, or timed out by
// the budget; a segmenter's typed error passes through, any other is
// unavailable; an organization has a bounded number of calls in flight.
func TestFailures(t *testing.T) {
	blocking := &segmenterFunc{f: func(ctx context.Context, _ Request) (Response, error) {
		<-ctx.Done()
		return Response{}, ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client(t, blocking).Segment(ctx, input()); provider.CodeOf(err) != provider.CodeCancelled || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled before: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if _, err := client(t, blocking).Segment(ctx, input()); provider.CodeOf(err) != provider.CodeCancelled || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled during: %v", err)
	}
	short := func(b *Budget) { b.Timeout, b.AttemptTimeout = 30*time.Millisecond, 30*time.Millisecond }
	if _, err := client(t, blocking, short).Segment(context.Background(), input()); provider.CodeOf(err) != provider.CodeTimeout ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("timeout: %v", err)
	}
	limited := &segmenterFunc{f: func(context.Context, Request) (Response, error) {
		return Response{}, provider.NewError(provider.CodeRateLimited, 429, "meta-a")
	}}
	if _, err := client(t, limited).Segment(context.Background(), input()); provider.CodeOf(err) != provider.CodeRateLimited {
		t.Errorf("rate limited: %v", err)
	}
	plain := &segmenterFunc{f: func(context.Context, Request) (Response, error) { return Response{}, errors.New("boom: secret detail") }}
	_, err := client(t, plain).Segment(context.Background(), input())
	if provider.CodeOf(err) != provider.CodeUnavailable || strings.Contains(err.Error(), "secret") {
		t.Errorf("plain error: %v", err)
	}
	// One call in flight per organization under this budget: a second is
	// refused at once, another organization's goes through.
	release := make(chan struct{})
	started := make(chan struct{})
	held := &segmenterFunc{f: func(ctx context.Context, _ Request) (Response, error) {
		close(started)
		<-release
		return Response{}, nil
	}}
	one := client(t, held, func(b *Budget) { b.MaxConcurrentPerOrganization = 1 })
	done := make(chan error)
	go func() { _, err := one.Segment(context.Background(), input()); done <- err }()
	<-started
	if _, err := one.Segment(context.Background(), input()); provider.CodeOf(err) != provider.CodeConcurrencyLimited {
		t.Errorf("a second call: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Errorf("the first call: %v", err)
	}
}
