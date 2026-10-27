package aievals

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/photopipeline"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/proposals/photo"
)

// The live photo smoke (T-056 review round 1, I1: the first live SAM 3.1
// call; make ai-smoke-photo-live, cmd/ai-eval-live -smoke-photo). It runs
// the photo pipeline once, as the worker runs it, on the demo image
// (PhotoImage) and the bare line (PhotoBase), without the database:
//
//   - the image as the API stores it (photo.Sanitize: the kinds it decodes,
//     its pixels re-encoded, no metadata) and as the worker reads it back
//     (photo.Verify against its record, provider.NewImage);
//   - internal/ai/photopipeline, the worker's own code: the segmenter
//     (SAM 3.1, each of the pack's concepts reserved for before its call),
//     the server's checks of its answer, the judge (Muse Spark: the request
//     counted with count_tokens and reserved at the count and 10% before it
//     is sent, H-037) and the server's review and photo rules;
//
// through clients metered by the run's own cap (DefaultPhotoSmokeCap), the
// run recorded in the spend record like the other runs (kind
// "smoke-photo"). Its report (live-smoke-photo-<UTC start>-<run ID>-<model>.json)
// keeps the status, codes, the server's risk, the concepts and boxes
// counted, what SAM said it processed, the judge's count against the input
// tokens it was billed, the reservations and the cost: never a prompt, an
// answer's text, a box, the image's bytes or a key.
//
// After the first live run (01M5XDJ3T0TTGE55K3A3JVK29N: SAM 3.1 found none
// of the pack's concepts in the drawn floor plan), a run may ask for
// concepts of its own (PhotoConfig.Concepts: the smoke's -concepts or
// AI_SMOKE_PHOTO_CONCEPTS) to explore their wording through this same
// pipeline, cap and spend record; the report then says so, names them,
// and gives for each concept the boxes SAM found and each box's size as a
// share of the frame (never where it is).

// The photo smoke's inputs, under the repository root.
const (
	PhotoImage = "testdata/photos/electronics-line-demo.png"
	PhotoBase  = "testdata/photos/bare-line.json"
)

// PhotoSmokeKind is the photo smoke's kind in the spend record and its
// report's name.
const PhotoSmokeKind = "smoke-photo"

// DefaultPhotoSmokeCap is what one photo smoke may spend at most by
// default (AI_EVAL_LIVE_MAX_USD sets another): SAM's 8 concepts are
// reserved for at 3 images each in turn (USD 0.0075 each) and settled at
// what they billed, about USD 0.02; the judge's call, at T-055's count of
// the demo prompt (6,661 input tokens) and provider.ImageMaxOutputTokens,
// about USD 0.44: about USD 0.50 held at most.
const DefaultPhotoSmokeCap = spend.NanoPerUSD

// The checks of a photo smoke (PhotoReport.Gates): a ready proposal
// without the server's validation, a needs_input that kept changes, and a
// pipeline that ended in a failure instead of the server's outcome.
const GatePipelineFailed = "pipeline_failed"

// The observations of a photo smoke (PhotoReport.Warnings): what H-037 and
// the review's I1 ask the first live call to show.
const (
	// WarningCountExceeded: the judge was billed more input tokens than it
	// reserved for (the count and 10%): report it to the owner (H-037).
	WarningCountExceeded = "billed_input_over_reserved"
	// WarningImagesProcessed: SAM said it processed other than one image
	// for a concept.
	WarningImagesProcessed = "images_processed_not_one"
	// WarningFrame: SAM's frame for a concept is not the image's size.
	WarningFrame = "frame_not_the_image"
	// WarningNoBoxes: SAM found nothing in the demo image.
	WarningNoBoxes = "no_boxes"
)

// judgeProbe is the judge's backend as the run sees it: each quote (the
// provider's count and the input tokens reserved for) and each answer's
// usage, nothing of the request or the answer's text.
type judgeProbe struct {
	provider.Billable
	mu     sync.Mutex
	quotes []provider.Quote
	failed int // quotes that failed: nothing was reserved or sent
	usages []provider.Usage
	models []string
}

// AcceptsImages is the backend's (provider.ImageInput).
func (j *judgeProbe) AcceptsImages() bool {
	ii, ok := j.Billable.(provider.ImageInput)
	return ok && ii.AcceptsImages()
}

func (j *judgeProbe) Quote(ctx context.Context, req provider.Request) (provider.Quote, error) {
	q, err := j.Billable.Quote(ctx, req)
	j.mu.Lock()
	defer j.mu.Unlock()
	if err != nil {
		j.failed++
	} else {
		j.quotes = append(j.quotes, q)
	}
	return q, err
}

func (j *judgeProbe) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	resp, err := j.Billable.Complete(ctx, req)
	j.mu.Lock()
	defer j.mu.Unlock()
	j.usages = append(j.usages, resp.Usage)
	if resp.Model != "" {
		j.models = append(j.models, resp.Model)
	}
	return resp, err
}

// conceptCall is one concept's call to the segmentation backend, as the
// run records it: no box, no text.
type conceptCall struct {
	concept         string
	answered        bool
	imagesProcessed int
	width, height   int
	objects         int
	sizes           []BoxSize
	code            string
	unknownAttempts int
}

// ConceptBoxes is what SAM answered for one concept, as the report keeps
// it (T-056 after the first live run, to compare the wording of concepts):
// whether it answered, how many boxes, and each box's size as a share of
// the answer's frame. Never where a box is, its confidence or the image.
type ConceptBoxes struct {
	Concept  string    `json:"concept"`
	Answered bool      `json:"answered"`
	Boxes    int       `json:"boxes"`
	Sizes    []BoxSize `json:"sizes"`
}

// BoxSize is a box's width and height over its frame's, to 3 places (a
// station of the demo floor plan: 0.158 by 0.231; the whole frame: 1 by
// 1).
type BoxSize struct {
	Width  float64 `json:"width_share"`
	Height float64 `json:"height_share"`
}

// sizesOf is the sizes of an answer's boxes that lie in its frame, in the
// answer's order; a box outside it (the client refuses the answer) has
// none.
func sizesOf(a provider.SegmentAnswer) []BoxSize {
	out := []BoxSize{}
	w, h := a.Width, a.Height
	if w <= 0 || h <= 0 {
		return out
	}
	share := func(n, of int) float64 { return math.Round(float64(n)/float64(of)*1000) / 1000 }
	for _, s := range a.Segments {
		b := s.Box
		if b.X1 < 0 || b.Y1 < 0 || b.X1 > b.X2 || b.Y1 > b.Y2 || b.X2 >= w || b.Y2 >= h {
			continue
		}
		out = append(out, BoxSize{Width: share(b.X2-b.X1+1, w), Height: share(b.Y2-b.Y1+1, h)})
	}
	return out
}

// segmentProbe is the segmentation backend as the run sees it.
type segmentProbe struct {
	provider.BillableSegmenter
	mu    sync.Mutex
	calls []conceptCall
}

func (s *segmentProbe) SegmentOne(ctx context.Context, call provider.SegmentCall) (provider.SegmentAnswer, error) {
	ans, err := s.BillableSegmenter.SegmentOne(ctx, call)
	c := conceptCall{concept: call.Concept, answered: ans.Answered, imagesProcessed: ans.ImagesProcessed, width: ans.Width, height: ans.Height,
		objects: len(ans.Segments), sizes: sizesOf(ans), unknownAttempts: ans.UnknownAttempts}
	if err != nil {
		c.code = codeOf(err)
	}
	s.mu.Lock()
	s.calls = append(s.calls, c)
	s.mu.Unlock()
	return ans, err
}

// meterProbe is the run's meter: every reservation's model and amount, and
// the most the meter held at once.
type meterProbe struct {
	*provider.MemoryMeter
	mu      sync.Mutex
	charges []provider.Charge
	peak    int64
}

func (m *meterProbe) Reserve(ctx context.Context, c provider.Charge) (provider.Reservation, error) {
	r, err := m.MemoryMeter.Reserve(ctx, c)
	if err == nil {
		m.mu.Lock()
		m.charges = append(m.charges, c)
		m.peak = max(m.peak, m.Committed())
		m.mu.Unlock()
	}
	return r, err
}

// PhotoRun is a photo smoke's two clients over the backends it is given
// (cmd/ai-eval-live: the live Meta adapters; the tests: local test servers
// or fakes), metered by the run's own cap, each call recorded for the
// report.
type PhotoRun struct {
	Judge     *provider.Client
	Segmenter *provider.SegmentClient
	budget    provider.Budget
	judge     *judgeProbe
	seg       *segmentProbe
	meter     *meterProbe
}

// NewPhotoRun builds the run's clients as the worker builds its photo path
// (cmd/worker photoPath: provider.NewMetered for the judge,
// provider.NewMeteredSegmentClient for the segmenter, under the
// proposals' budget), metered by a meter of capNano.
func NewPhotoRun(judge provider.Billable, seg provider.BillableSegmenter, budget provider.Budget, capNano int64) (*PhotoRun, error) {
	if judge == nil || seg == nil {
		return nil, errors.New("aievals: the photo smoke needs a judge and a segmenter")
	}
	r := &PhotoRun{budget: budget, judge: &judgeProbe{Billable: judge}, seg: &segmentProbe{BillableSegmenter: seg},
		meter: &meterProbe{MemoryMeter: provider.NewMemoryMeter(capNano)}}
	var err error
	if r.Judge, err = provider.NewMetered(r.judge, budget, r.meter); err != nil {
		return nil, err
	}
	if r.Segmenter, err = provider.NewMeteredSegmentClient(r.seg, budget, r.meter); err != nil {
		return nil, err
	}
	return r, nil
}

// Spent and Committed are the run's meter's (settled; settled and open).
func (r *PhotoRun) Spent() int64     { return r.meter.Spent() }
func (r *PhotoRun) Committed() int64 { return r.meter.Committed() }

// PhotoConfig describes one photo smoke.
type PhotoConfig struct {
	Packs PackLookup
	// Root is the repository root (PhotoImage and PhotoBase are read from
	// it).
	Root string
	// Locale is the proposal's output locale ("en").
	Locale string
	// Provider, Approval, Model and SegmentationModel describe the run in
	// the report: the photo path's live provider, its recorded approval,
	// the judge's model and SAM's.
	Provider, Approval, Model, SegmentationModel string
	CapNanoUSD                                   int64
	// RunID is the run's ID in the spend record.
	RunID string
	// Concepts, when not nil, are what the segmenter is asked for instead
	// of the pack's (the smoke's -concepts or AI_SMOKE_PHOTO_CONCEPTS: T-056
	// after the first live run, to explore the wording live through this
	// pipeline): checked as the segmenter client checks the pack's
	// (provider.CheckConcepts) before any call, and named in the report. The
	// worker never takes them: its concepts are always its pack's.
	Concepts []string
	// Now dates the report; time.Now when nil.
	Now func() time.Time
}

// PhotoReport is a photo smoke's record
// (checks/ai-evals/live-smoke-photo-*.json): no prompt, answer text, box
// (only its size as a share of the frame), image byte or key.
type PhotoReport struct {
	Suite             string `json:"suite"`
	Kind              string `json:"kind"`
	Provider          string `json:"provider"`
	Model             string `json:"model"`
	SegmentationModel string `json:"segmentation_model"`
	// Served are the models the provider named in its answers (the judge's
	// and SAM's).
	Served   []string `json:"served_models"`
	Approval string   `json:"approval"`
	Date     string   `json:"date"`
	RunID    string   `json:"run_id,omitzero"`
	Locale   string   `json:"output_locale"`
	Template string   `json:"prompt_template_version,omitzero"`
	// ConceptsOverridden: the segmenter was asked for the run's own
	// concepts (PhotoConfig.Concepts), not the pack's; Segmenter.ConceptList
	// names them.
	ConceptsOverridden bool `json:"concepts_overridden"`
	Image              struct {
		Path          string `json:"path"`
		UploadBytes   int    `json:"upload_bytes"`
		MediaType     string `json:"media_type"`
		Width         int    `json:"width"`
		Height        int    `json:"height"`
		StoredBytes   int64  `json:"stored_bytes"`
		StoredSHA256  string `json:"stored_sha256"`
		VerifiedAsJob bool   `json:"verified_as_the_worker_does"`
	} `json:"image"`
	Base struct {
		Path   string `json:"path"`
		Domain string `json:"domain"`
		Hash   string `json:"ir_hash"`
	} `json:"base"`
	Price struct {
		Source        string `json:"source"`
		Retrieved     string `json:"retrieved"`
		ImageNanoUSD  int64  `json:"segmentation_image_nano_usd"`
		InputPerMTok  string `json:"judge_input_usd_per_mtok"`
		OutputPerMTok string `json:"judge_output_usd_per_mtok"`
	} `json:"price"`
	// Status is ready, needs_input, invalid or failed (the pipeline ended
	// in a failure), or not_run.
	Status string `json:"status"`
	// Code is a failure's code (AI_..., the generator's codes).
	Code              string   `json:"code,omitzero"`
	ProviderStatus    int      `json:"provider_status,omitzero"`
	ProviderErrorType string   `json:"provider_error_type,omitzero"`
	Issues            []string `json:"issues,omitzero"`
	ServerRisk        string   `json:"server_risk_level,omitzero"`
	ProviderRisk      string   `json:"provider_risk_claim,omitzero"`
	Risk              string   `json:"risk_level,omitzero"`
	PatchHash         string   `json:"patch_hash,omitzero"`
	NewProcessSteps   int      `json:"new_process_steps,omitzero"`
	Segmenter         struct {
		Asked    bool `json:"asked"`
		Concepts int  `json:"concepts"`
		// ConceptList is the concepts asked for, in order; ByConcept what
		// each call answered (boxes and their sizes as shares of the frame).
		ConceptList      []string       `json:"concept_list"`
		ByConcept        []ConceptBoxes `json:"by_concept"`
		Calls            int            `json:"calls"`
		Answered         int            `json:"concepts_answered"`
		WithBoxes        int            `json:"concepts_with_boxes"`
		Boxes            int            `json:"boxes"`
		BoxesByConcept   map[string]int `json:"boxes_by_concept"`
		ImagesProcessed  []int          `json:"images_processed"`
		ImagesBilled     int            `json:"images_billed"`
		Frames           []string       `json:"frames"`
		UnknownAttempts  int            `json:"unknown_attempts"`
		Reservations     int            `json:"reservations"`
		ReservedNanoUSD  int64          `json:"reserved_nano_usd"`
		CostNanoUSD      int64          `json:"cost_nano_usd"`
		OverReservation  bool           `json:"over_reservation"`
		LatencyMillis    int64          `json:"latency_ms"`
		ConceptCallCodes []string       `json:"concept_call_codes,omitzero"`
	} `json:"segmenter"`
	Judge struct {
		Asked bool `json:"asked"`
		// Counted is the provider's count of the request (count_tokens,
		// H-037), ReservedInput the input tokens reserved for (the count and
		// 10%), BilledInput what the answer said it billed.
		Quotes          int     `json:"quotes"`
		QuotesFailed    int     `json:"quotes_failed"`
		Counted         int64   `json:"counted_input_tokens"`
		ReservedInput   int64   `json:"reserved_input_tokens"`
		BilledInput     int64   `json:"billed_input_tokens"`
		BilledOverCount float64 `json:"billed_over_counted,omitzero"`
		Output          int64   `json:"output_tokens"`
		Thinking        *int64  `json:"thinking_tokens"` // null: the provider did not report it
		MaxOutput       int64   `json:"max_output_tokens"`
		Calls           int     `json:"calls"`
		Reservations    int     `json:"reservations"`
		ReservedNanoUSD int64   `json:"reserved_nano_usd"`
		CostNanoUSD     int64   `json:"cost_nano_usd"`
		OverReservation bool    `json:"over_reservation"`
		LatencyMillis   int64   `json:"latency_ms"`
	} `json:"judge"`
	Cost struct {
		NanoUSD          int64  `json:"nano_usd"`
		USD              string `json:"usd"`
		CommittedNanoUSD int64  `json:"committed_nano_usd"`
		PeakNanoUSD      int64  `json:"peak_held_nano_usd"`
		CapUSD           string `json:"cap_usd"`
		CapNanoUSD       int64  `json:"cap_nano_usd"`
	} `json:"cost"`
	Gates    map[string]int `json:"gates"`
	Warnings map[string]int `json:"observations"`
	Complete bool           `json:"complete"`
	NotRun   []string       `json:"not_run"`
}

// Ok reports whether the smoke reached the server's outcome and no gate
// failed.
func (r *PhotoReport) Ok() bool {
	if !r.Complete {
		return false
	}
	for _, n := range r.Gates {
		if n != 0 {
			return false
		}
	}
	return true
}

// Marshal is the report as written: indented JSON with a final newline.
func (r *PhotoReport) Marshal() ([]byte, error) { return marshalReport(r) }

// codeOf is a failure's code as the generator names it: the provider
// client's code in upper case after AI_.
func codeOf(err error) string {
	if c := provider.CodeOf(err); c != "" {
		return "AI_" + strings.ToUpper(string(c))
	}
	return "ERROR"
}

// PhotoInputs are the smoke's demo image and base model, read and checked
// as the API and the worker do, before anything is spent.
type PhotoInputs struct {
	upload   int
	stored   photo.Image
	image    provider.Image
	baseIR   []byte
	ir       model.ModelIR
	pack     domainpack.DomainPack
	concepts []string
}

// ReadPhotoInputs reads PhotoImage and PhotoBase under root: the image as
// the API stores it (photo.Sanitize) and as the worker reads it back
// (photo.Verify against its record, provider.NewImage), the base as a
// synthetic organization's model (never a tenant's), its pack and the
// pack's concepts. An error means the smoke cannot run; nothing was spent.
func ReadPhotoInputs(ctx context.Context, root string, packs PackLookup) (*PhotoInputs, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(PhotoImage)))
	if err != nil {
		return nil, fmt.Errorf("aievals: the demo image: %w", err)
	}
	in := &PhotoInputs{upload: len(data)}
	if in.stored, err = photo.Sanitize(ctx, data); err != nil {
		return nil, fmt.Errorf("aievals: the demo image is not one the API stores: %w", err)
	}
	if err := photo.Verify(in.stored.Data, in.stored.Record()); err != nil {
		return nil, fmt.Errorf("aievals: the demo image does not verify: %w", err)
	}
	if in.image, err = provider.NewImage(in.stored.MediaType, in.stored.Data); err != nil {
		return nil, fmt.Errorf("aievals: the demo image is not one the provider client sends: %w", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(PhotoBase)))
	if err != nil {
		return nil, fmt.Errorf("aievals: the demo base: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("aievals: the demo base: %w", err)
	}
	doc["model_id"] = evalMdl
	raw, _ = json.Marshal(doc)
	if in.baseIR, err = hashing.Canonical(raw); err != nil {
		return nil, fmt.Errorf("aievals: the demo base: %w", err)
	}
	if err := model.Decode(in.baseIR, &in.ir); err != nil {
		return nil, fmt.Errorf("aievals: the demo base: %w", err)
	}
	if in.pack, err = packs.Lookup(in.ir.Domain); err != nil {
		return nil, errors.New("aievals: no pack for the demo base's domain")
	}
	if in.concepts = photopipeline.Concepts(in.pack); in.concepts == nil {
		return nil, errors.New("aievals: the demo base's pack names nothing to look for in a photo")
	}
	return in, nil
}

// Run runs the photo smoke once on in. A provider failure is the report's
// outcome (failed, with its code); the run's cap or a cancellation stops
// it (incomplete).
func (r *PhotoRun) Run(ctx context.Context, cfg PhotoConfig, in *PhotoInputs) (*PhotoReport, error) {
	if cfg.Provider == "" || in == nil {
		return nil, errors.New("aievals: the provider and the inputs are required")
	}
	now := time.Now
	if cfg.Now != nil {
		now = cfg.Now
	}
	rep := &PhotoReport{Suite: "T-056 live photo smoke (the photo pipeline once: segmenter, count_tokens, judge, server review; review round 1, I1)",
		Kind: PhotoSmokeKind, Provider: cfg.Provider, Model: cfg.Model, SegmentationModel: cfg.SegmentationModel, Approval: cfg.Approval,
		Date: now().UTC().Format(time.DateOnly), RunID: cfg.RunID, Locale: cfg.Locale, Complete: true,
		Gates:    map[string]int{GateReadyInvalid: 0, GateNeedsInputChanges: 0, GatePipelineFailed: 0},
		Warnings: map[string]int{WarningOverReservation: 0, WarningCountExceeded: 0, WarningImagesProcessed: 0, WarningFrame: 0, WarningNoBoxes: 0},
		NotRun: []string{
			"the database, object storage, the job queue and the spend ledger (the worker's; make integration-test with the fakes)",
			"approval of the proposal: a person approves it through the API",
			"any image but the demo image, any other base model, zh-Hant output",
		}}
	rep.Segmenter.BoxesByConcept = map[string]int{}
	concepts := in.concepts
	if cfg.Concepts != nil {
		if err := provider.CheckConcepts(cfg.Concepts); err != nil {
			return nil, fmt.Errorf("aievals: the concepts to ask for: %w", err)
		}
		concepts, rep.ConceptsOverridden = slices.Clone(cfg.Concepts), true
	}
	rep.Segmenter.ConceptList, rep.Segmenter.ByConcept = slices.Clone(concepts), []ConceptBoxes{}
	rep.Cost.CapNanoUSD, rep.Cost.CapUSD = cfg.CapNanoUSD, spend.FormatUSD(cfg.CapNanoUSD)
	rep.Price.Source, rep.Price.Retrieved = spend.PriceSource, spend.PriceRetrieved
	if source, retrieved, ok := spend.SourceOf(cfg.Model); ok {
		rep.Price.Source, rep.Price.Retrieved = source, retrieved
	}
	if p, ok := spend.PriceOf(cfg.Model); ok {
		rep.Price.InputPerMTok, rep.Price.OutputPerMTok = perMTok(p, spend.Tokens{Input: 1_000_000}), perMTok(p, spend.Tokens{Output: 1_000_000})
	}
	rep.Price.ImageNanoUSD, _ = spend.ImagePriceOf(cfg.SegmentationModel)
	img, ir := in.image, in.ir
	rep.Image.Path, rep.Image.UploadBytes, rep.Image.MediaType, rep.Image.Width, rep.Image.Height = PhotoImage, in.upload, img.MediaType, img.Width, img.Height
	rep.Image.StoredBytes, rep.Image.StoredSHA256, rep.Image.VerifiedAsJob = in.stored.Size(), in.stored.SHA256, true
	rep.Base.Path, rep.Base.Domain, rep.Base.Hash = PhotoBase, ir.Domain, hashing.Sum(in.baseIR)
	pool, err := idpool.New(&ir)
	if err != nil {
		return nil, fmt.Errorf("aievals: %w", err)
	}
	rep.Segmenter.Concepts = len(concepts)

	// Before anything is spent, as the generator does.
	if !photopipeline.TakesImages(r.Judge) {
		rep.Status, rep.Code, rep.Gates[GatePipelineFailed] = "failed", "AI_IMAGE_UNSUPPORTED", 1
		r.finish(rep)
		return rep, nil
	}
	aip := ids.New("aip")
	res, err := photopipeline.Propose(ctx, photopipeline.Clients{Segmenter: r.Segmenter, Vision: r.Judge}, photopipeline.Request{
		OrganizationID: evalOrg,
		Base:           aicontext.Version{OrganizationID: evalOrg, ModelID: evalMdl, VersionID: evalMv, IR: &ir},
		BaseIR:         in.baseIR,
		Pack:           in.pack,
		Concepts:       concepts,
		Pool:           aicontext.Pool{OrganizationID: evalOrg, ProposalID: aip, IDs: pool},
		Image:          img,
		OutputLocale:   cfg.Locale,
		Budget:         r.budget,
		Attribution:    provider.Attribution{TargetType: "ai_proposal", TargetID: aip, RequestID: ids.New("req")},
	}, photopipeline.Observer{})
	if s := res.Segmenter; s != nil {
		rep.Segmenter.Asked = true
		rep.Segmenter.ImagesBilled, rep.Segmenter.CostNanoUSD, rep.Segmenter.LatencyMillis = s.Images, s.CostNanoUSD, s.LatencyMillis
		rep.Segmenter.Reservations, rep.Segmenter.OverReservation = len(s.SpendReservations), s.SpendOverReservation
		if s.Model != "" {
			rep.Served = append(rep.Served, s.Model)
		}
	}
	if seg := res.Segmentation; seg != nil {
		rep.Segmenter.Boxes = len(seg.Segments)
		for _, x := range seg.Segments {
			rep.Segmenter.BoxesByConcept[x.Concept]++
		}
		rep.Segmenter.WithBoxes = len(rep.Segmenter.BoxesByConcept)
	}
	if v := res.Vision; v != nil {
		rep.Judge.Asked = true
		rep.Template = v.TemplateVersion
		rep.Judge.BilledInput, rep.Judge.Output, rep.Judge.CostNanoUSD = v.Usage.InputTokens, v.Usage.OutputTokens, v.CostNanoUSD
		rep.Judge.Thinking, rep.Judge.MaxOutput = v.Usage.ThinkingTokens, provider.ImageMaxOutputTokens
		rep.Judge.OverReservation, rep.Judge.LatencyMillis = v.SpendOverReservation, v.LatencyMillis
		if v.Model != "" {
			rep.Served = append(rep.Served, v.Model)
		}
	}
	switch {
	case err != nil:
		rep.Status, rep.Gates[GatePipelineFailed] = "failed", 1
		var ce *photopipeline.ContextError
		var re *photopipeline.ReviewError
		var pe *provider.Error
		switch {
		case errors.As(err, &ce) && ce.Code == aicontext.TooLarge:
			rep.Code = "AI_CONTEXT_TOO_LARGE"
		case errors.As(err, &ce), errors.As(err, &re):
			rep.Code = "PROPOSAL_INPUT_INVALID"
		default:
			rep.Code = codeOf(err)
			if errors.As(err, &pe) && pe.Status != 0 {
				rep.ProviderStatus, rep.ProviderErrorType = pe.Status, pe.ErrorType
			}
		}
		if rep.Code == "AI_QUOTA_EXCEEDED" || rep.Code == "AI_CANCELLED" || ctx.Err() != nil {
			rep.Complete = false
		}
	default:
		out := res.Outcome
		rep.Status = string(out.Status)
		if out.Validation != nil {
			for _, is := range out.Validation.Issues {
				rep.Issues = append(rep.Issues, is.Code)
			}
			slices.Sort(rep.Issues)
			rep.Issues = slices.Compact(rep.Issues)
		}
		if out.Status == review.Ready {
			rep.ServerRisk, rep.ProviderRisk, rep.Risk, rep.PatchHash = string(out.ServerRisk), string(out.ProviderRisk), string(out.Risk), out.PatchHash
			if out.Validation == nil || !out.Validation.Valid || out.PatchHash == "" {
				rep.Gates[GateReadyInvalid]++
			}
			rep.NewProcessSteps = newSteps(&ir, out.Candidate)
		}
		if out.Status == review.NeedsInput && (out.Changes != nil || out.Risk != "") {
			rep.Gates[GateNeedsInputChanges]++
		}
	}
	r.finish(rep)
	return rep, nil
}

// newSteps counts the candidate's process steps the base does not have.
func newSteps(base, candidate *model.ModelIR) int {
	if candidate == nil {
		return 0
	}
	in := map[string]bool{}
	for _, n := range base.Nodes {
		in[n.ID] = true
	}
	n := 0
	for _, x := range candidate.Nodes {
		if x.Type == model.NodeProcess && !in[x.ID] {
			n++
		}
	}
	return n
}

// finish fills what the probes saw: SAM's per-concept answers, the judge's
// quotes and usage, the reservations, what the run cost, and the
// observations.
func (r *PhotoRun) finish(rep *PhotoReport) {
	r.seg.mu.Lock()
	frames := map[string]bool{}
	for _, c := range r.seg.calls {
		rep.Segmenter.Calls++
		rep.Segmenter.ByConcept = append(rep.Segmenter.ByConcept, ConceptBoxes{Concept: c.concept, Answered: c.answered, Boxes: c.objects, Sizes: c.sizes})
		rep.Segmenter.UnknownAttempts += c.unknownAttempts
		if c.code != "" {
			rep.Segmenter.ConceptCallCodes = append(rep.Segmenter.ConceptCallCodes, c.code)
		}
		if !c.answered {
			continue
		}
		rep.Segmenter.Answered++
		rep.Segmenter.ImagesProcessed = append(rep.Segmenter.ImagesProcessed, c.imagesProcessed)
		if c.imagesProcessed != 1 {
			rep.Warnings[WarningImagesProcessed]++
		}
		if c.objects > 0 {
			frames[strconv.Itoa(c.width)+"x"+strconv.Itoa(c.height)] = true
			if c.width != rep.Image.Width || c.height != rep.Image.Height {
				rep.Warnings[WarningFrame]++
			}
		}
	}
	r.seg.mu.Unlock()
	for f := range frames {
		rep.Segmenter.Frames = append(rep.Segmenter.Frames, f)
	}
	slices.Sort(rep.Segmenter.Frames)
	if rep.Segmenter.Asked && rep.Segmenter.Answered == rep.Segmenter.Concepts && rep.Segmenter.Boxes == 0 && rep.Status != "failed" {
		rep.Warnings[WarningNoBoxes]++
	}

	r.judge.mu.Lock()
	rep.Judge.Quotes, rep.Judge.QuotesFailed, rep.Judge.Calls = len(r.judge.quotes), r.judge.failed, len(r.judge.usages)
	if n := len(r.judge.quotes); n > 0 {
		q := r.judge.quotes[n-1]
		rep.Judge.Counted, rep.Judge.ReservedInput = q.Counted, q.MaxInputTokens
	}
	r.judge.mu.Unlock()
	if rep.Judge.Counted > 0 && rep.Judge.BilledInput > 0 {
		rep.Judge.BilledOverCount = float64(rep.Judge.BilledInput) / float64(rep.Judge.Counted)
	}
	if rep.Judge.ReservedInput > 0 && rep.Judge.BilledInput > rep.Judge.ReservedInput {
		rep.Warnings[WarningCountExceeded]++
	}

	r.meter.mu.Lock()
	for _, c := range r.meter.charges {
		if c.Model == rep.SegmentationModel {
			rep.Segmenter.ReservedNanoUSD += c.NanoUSD
		} else {
			rep.Judge.Reservations++
			rep.Judge.ReservedNanoUSD += c.NanoUSD
		}
	}
	rep.Cost.PeakNanoUSD = r.meter.peak
	r.meter.mu.Unlock()
	if rep.Segmenter.OverReservation || rep.Judge.OverReservation {
		rep.Warnings[WarningOverReservation]++
	}
	slices.Sort(rep.Served)
	rep.Served = slices.Compact(rep.Served)
	if rep.Served == nil {
		rep.Served = []string{}
	}
	rep.Cost.NanoUSD, rep.Cost.CommittedNanoUSD = r.Spent(), r.Committed()
	rep.Cost.USD = spend.FormatUSD(rep.Cost.NanoUSD)
}

// perMTok is a price's cost of tokens, in US dollars.
func perMTok(p spend.Price, t spend.Tokens) string { return spend.FormatUSD(p.Cost(t)) }
