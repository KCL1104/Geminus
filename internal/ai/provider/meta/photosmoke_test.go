package meta_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/meta"
	"github.com/kcl1104/geminus/internal/ai/provider/structured"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/aievals"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/packs"
)

// projected is an answer as the model writes it under the projected schema
// the adapter sends (internal/ai/provider/structured): each open value (a
// patch value) as a JSON text in a string; the adapter decodes it back.
// Written apart from structured's Decode, as the Anthropic adapter's tests
// do.
func projected(t *testing.T, answer []byte) string {
	t.Helper()
	p, err := structured.Project("proposal-output.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(answer, &doc); err != nil {
		t.Fatal(err)
	}
	var encode func(v any, steps []structured.Step) any
	encode = func(v any, steps []structured.Step) any {
		if len(steps) == 0 {
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			return string(raw)
		}
		st := steps[0]
		if st.Items {
			arr, ok := v.([]any)
			if !ok {
				return v
			}
			for i := range arr {
				arr[i] = encode(arr[i], steps[1:])
			}
			return arr
		}
		obj, ok := v.(map[string]any)
		if !ok {
			return v
		}
		for _, c := range st.When {
			if obj[c.Member] != c.Const {
				return v
			}
		}
		if m, ok := obj[st.Member]; ok {
			obj[st.Member] = encode(m, steps[1:])
		}
		return obj
	}
	for _, l := range p.Encoded {
		doc = encode(doc, l.Steps)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// T-056 review round 1: the photo smoke (cmd/ai-eval-live -smoke-photo,
// the first live SAM 3.1 call) over the two Meta adapters and a local
// server of the Responses API (SAM 3.1: one request per concept, the demo
// image's boxes in the documented grammar) and of the Messages API (the
// count first, then the call: the demo answer with the prompt's own ID
// pool). No request leaves the machine.

// photoAPI is the local server: SAM answers each concept with the demo's
// boxes in the image's frame; count_tokens answers count (or fails with
// countStatus); Messages answers the demo proposal, billed at billed input
// tokens.
type photoAPI struct {
	t           *testing.T
	srv         *httptest.Server
	mu          sync.Mutex
	paths       []string
	raw         []string
	count       string
	countStatus int
	billed      int
}

var poolBlock = regexp.MustCompile(`(?s)name="ID_ALLOCATION_POOL">\n(.*?)\n</`)

func newPhotoAPI(t *testing.T) *photoAPI {
	a := &photoAPI{t: t, count: "6661", billed: 6702}
	demo := contracttest.LoadPhotoDemo(t, "electronics-line-demo")
	by := demo.Segments.ByConcept()
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		a.mu.Lock()
		a.paths = append(a.paths, r.Method+" "+r.URL.Path)
		a.raw = append(a.raw, string(data))
		a.mu.Unlock()
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Errorf("a request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case responsesPath:
			content := body["input"].([]any)[0].(map[string]any)["content"].([]any)
			concept := content[0].(map[string]any)["text"].(string)
			var records []string
			for _, x := range by[concept] {
				c := strconv.FormatFloat(x.Confidence, 'f', -1, 64)
				records = append(records, strconv.Itoa(x.ObjectID)+"<|box;x1="+strconv.Itoa(x.Box.X1)+";y1="+strconv.Itoa(x.Box.Y1)+";x2="+
					strconv.Itoa(x.Box.X2)+";y2="+strconv.Itoa(x.Box.Y2)+";w=1200;h=520;c="+c+"|><|mask;x=0;y=0;c="+c+";data=4,4,~abcd|>")
			}
			text := ""
			if len(records) > 0 {
				text = "<0f>" + strings.Join(records, ",")
			}
			_, _ = io.WriteString(w, response(text).body)
		case countPath:
			if a.countStatus != 0 {
				w.WriteHeader(a.countStatus)
				_, _ = io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"BODY-SENTINEL"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"input_tokens":`+a.count+`}`)
		case messagesPath:
			var pool map[string][]string
			for _, m := range body["messages"].([]any) {
				for _, c := range m.(map[string]any)["content"].([]any) {
					if text, ok := c.(map[string]any)["text"].(string); ok {
						if g := poolBlock.FindStringSubmatch(text); g != nil {
							if err := json.Unmarshal([]byte(g[1]), &pool); err != nil {
								t.Errorf("the pool block: %v", err)
							}
						}
					}
				}
			}
			answer := projected(t, contracttest.PhotoAnswer(t, "electronics-line-demo", pool))
			content, _ := json.Marshal([]map[string]any{{"type": "text", "text": answer}})
			_, _ = io.WriteString(w, message("end_turn", string(content), `{"input_tokens":`+strconv.Itoa(a.billed)+`,"output_tokens":1100}`).body)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *photoAPI) hits() ([]string, []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.paths), slices.Clone(a.raw)
}

// photoRun runs the smoke on a's adapters under capNano, as Main does
// after its gate (aievals.NewPhotoRun, ReadPhotoInputs, Run).
func photoRun(t *testing.T, a *photoAPI, capNano int64) (*aievals.PhotoReport, *aievals.PhotoRun) {
	t.Helper()
	judge, err := meta.NewForTest(config(), a.srv.URL+"/", a.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config()
	cfg.Model = samModel
	seg, err := meta.NewSegmenterForTest(cfg, a.srv.URL+"/", a.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []any{judge, seg} {
		meta.SetSleep(b, func(ctx context.Context, _ time.Duration) error { return ctx.Err() })
	}
	run, err := aievals.NewPhotoRun(judge, seg, provider.DefaultBudget(), capNano)
	if err != nil {
		t.Fatal(err)
	}
	root := contracttest.Root(t)
	reg, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	in, err := aievals.ReadPhotoInputs(context.Background(), root, reg)
	if err != nil {
		t.Fatal(err)
	}
	r, err := run.Run(context.Background(), aievals.PhotoConfig{Packs: reg, Root: root, Locale: "en", Provider: "meta", Approval: "H-029", Model: model,
		SegmentationModel: samModel, CapNanoUSD: capNano, RunID: "01M5X00000000000000000PH0T",
		Now: func() time.Time { return time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC) }}, in)
	if err != nil {
		t.Fatal(err)
	}
	return r, run
}

// The demo over HTTP: 8 Responses requests (each concept reserved for
// before its call, each billed one image), then the count, then one
// Messages request with the same body but max_tokens; the answer is
// reviewed and ready. The report has SAM's images, the count against the
// tokens billed, the reservations and the cost at Meta's prices, and
// nothing of the image, the prompt, the answer, a box, the key or the
// server.
func TestThePhotoSmokeOverHTTP(t *testing.T) {
	a := newPhotoAPI(t)
	r, run := photoRun(t, a, spend.NanoPerUSD)
	paths, raw := a.hits()
	want := append(slices.Repeat([]string{responsesPath}, 8), countPath, messagesPath)
	if !slices.Equal(paths, want) {
		t.Fatalf("requests %v", paths)
	}
	samPrice, _ := spend.ImagePriceOf(samModel)
	price, _ := spend.PriceOf(model)
	reserved := 3 * price.AttemptBound(7328, provider.ImageMaxOutputTokens)
	cost := price.Cost(spend.Tokens{Input: 6702, Output: 1100})
	if r.Status != "ready" || !r.Ok() || r.ServerRisk == "" || r.PatchHash == "" || r.NewProcessSteps != 4 {
		t.Fatalf("%+v", r)
	}
	if s := r.Segmenter; s.Calls != 8 || s.Answered != 8 || !slices.Equal(s.ImagesProcessed, slices.Repeat([]int{1}, 8)) || s.Reservations != 8 ||
		s.ReservedNanoUSD != 24*samPrice || s.CostNanoUSD != 8*samPrice || !slices.Equal(s.Frames, []string{"1200x520"}) || s.Boxes == 0 {
		t.Errorf("segmenter %+v", s)
	}
	if g := r.Judge; g.Counted != 6661 || g.ReservedInput != 7328 || g.BilledInput != 6702 || g.Reservations != 1 || g.ReservedNanoUSD != reserved ||
		g.CostNanoUSD != cost || g.BilledOverCount <= 1 || g.BilledOverCount > 1.1 {
		t.Errorf("judge %+v (reserved %d, cost %d)", g, reserved, cost)
	}
	if r.Cost.NanoUSD != 8*samPrice+cost || run.Spent() != r.Cost.NanoUSD || r.Price.Source != spend.MetaPriceSource || r.Price.ImageNanoUSD != samPrice {
		t.Errorf("cost %+v price %+v", r.Cost, r.Price)
	}
	// The count's body is the call's but max_tokens (H-037).
	var countBody, callBody map[string]any
	_ = json.Unmarshal([]byte(raw[8]), &countBody)
	_ = json.Unmarshal([]byte(raw[9]), &callBody)
	delete(callBody, "max_tokens")
	if _, has := countBody["max_tokens"]; has || len(countBody) != len(callBody) {
		t.Errorf("the count's body: %d members, the call's %d", len(countBody), len(callBody))
	}
	data, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	img := contracttest.LoadPhotoDemo(t, "electronics-line-demo").Image
	leaks(t, "the report", string(data), key, "SENTINEL", a.srv.URL, "127.0.0.1", "Add the AOI", "floor plan shows", "ID_ALLOCATION_POOL",
		base64.StdEncoding.EncodeToString(img)[:64], "<|box", "x1=")
}

// H-037 over HTTP: a count that fails (a 500, retried, then given up; or
// an answer that is no sane count) sends no Messages request and reserves
// nothing for the judge; the 8 SAM requests were made and paid, and the
// report says the pipeline failed and how.
func TestThePhotoSmokeStopsAtAFailedCount(t *testing.T) {
	samPrice, _ := spend.ImagePriceOf(samModel)
	for name, c := range map[string]struct {
		status   int
		count    string
		code     string
		counts   int
		provider int
	}{
		"a 500":             {500, "", "AI_UNAVAILABLE", 3, 500},
		"not a count":       {0, "1.5", "AI_UNAVAILABLE", 1, 0},
		"beyond the window": {0, "1048577", "AI_UNAVAILABLE", 1, 0},
	} {
		t.Run(name, func(t *testing.T) {
			a := newPhotoAPI(t)
			a.countStatus, a.count = c.status, c.count
			r, run := photoRun(t, a, spend.NanoPerUSD)
			paths, _ := a.hits()
			if slices.Contains(paths, messagesPath) || slices.Index(paths, countPath) != 8 || len(paths) != 8+c.counts {
				t.Fatalf("requests %v", paths)
			}
			if r.Status != "failed" || r.Code != c.code || r.Ok() || r.Judge.Calls != 0 || r.Judge.Reservations != 0 || r.Judge.QuotesFailed != 1 ||
				r.Segmenter.CostNanoUSD != 8*samPrice || run.Spent() != 8*samPrice || r.ProviderStatus != c.provider {
				t.Errorf("%+v", r)
			}
			t.Logf("%s: %s", name, r.Code)
		})
	}
}
