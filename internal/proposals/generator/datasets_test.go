package generator

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/objects"
)

// outcomeOf is a generation as the tests read it.
func outcomeOf(g generation, err error) (review.Outcome, *provider.Metadata, error) {
	return g.outcome, g.meta, err
}

// memObjects is object storage in memory. Each Get notes how many provider
// requests the fake had seen by then.
type memObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
	err     error
	f       *fake.Backend
	seen    []int
}

func (m *memObjects) Put(context.Context, string, io.Reader, int64) (objects.Object, error) {
	return objects.Object{}, errors.New("read only")
}

func (m *memObjects) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen = append(m.seen, len(m.f.Requests()))
	if m.err != nil {
		return nil, m.err
	}
	data, ok := m.objects[key]
	if !ok {
		return nil, objects.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// withData is st on a base whose code review takes its time from frozen
// samples, with a manifest that also binds a dataset the model does not use
// (TWIN_IR_SPEC §5.2), and the objects that hold both snapshots.
func withData(t *testing.T, st started) (started, map[string][]byte) {
	t.Helper()
	org := st.proposal.OrganizationID
	var doc map[string]any
	if err := json.Unmarshal(st.version.IR, &doc); err != nil {
		t.Fatal(err)
	}
	review := doc["nodes"].([]any)[1].(map[string]any)
	review["service_time"] = map[string]any{"distribution": map[string]any{"type": "empirical", "samples_ref": "dataset:review_duration"}}
	raw, _ := json.Marshal(doc)
	ir, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	st.version.IR, st.version.Hash = ir, hashing.Sum(ir)
	st.keys, st.version.DatasetBindings = map[string]string{}, nil
	stored := map[string][]byte{}
	for i, ref := range []string{"dataset:review_duration", "dataset:unused"} {
		dsv := fmt.Sprintf("dsv_01M4YDQK00ADN261CME3WV5HZ%d", i)
		data := fmt.Appendf(nil, `{"dataset_version_id":%q,"organization_id":%q,"logical_ref":%q,"unit":"minute","samples":[120,240,360]}`, dsv, org, ref)
		key := "tenant/" + org + "/datasets/" + dsv + ".json"
		st.keys[dsv], stored[key] = key, data
		st.version.DatasetBindings = append(st.version.DatasetBindings,
			model.DatasetBinding{LogicalRef: ref, DatasetVersionID: dsv, ContentHash: hashing.ContentHash(data), Unit: model.Minute, SampleCount: 3})
	}
	return st, stored
}

// H-003 review L3: the base's dataset snapshots are read and verified
// before the provider is asked, so a storage failure fails the attempt
// before any paid call (and may be retried: nothing was spent), and the
// binding stage after the call reads no storage at all.
func TestSnapshotsAreReadBeforeTheProvider(t *testing.T) {
	ctx := context.Background()
	raise := `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`
	setup := func(store func(*fake.Backend) objects.Store) (*Handler, *fake.Backend, started) {
		f := scripted(t, "Six runners.", reference(raise))
		cfg := Config{Provider: client(t, f), Packs: packs(t), Actor: "worker-1", Log: discard()}
		if store != nil {
			cfg.Objects = store(f)
		}
		h, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		st, _ := withData(t, stored(t, "edit", "Six runners."))
		return h, f, st
	}
	_, objs := withData(t, stored(t, "edit", "Six runners."))
	for name, c := range map[string]struct {
		store     func(*fake.Backend) objects.Store
		retryable bool
	}{
		"storage fails":     {func(f *fake.Backend) objects.Store { return &memObjects{err: errors.New("connection reset"), f: f} }, true},
		"no object storage": {nil, false},
		"a snapshot changed": {func(f *fake.Backend) objects.Store {
			changed := map[string][]byte{}
			for k, v := range objs {
				changed[k] = append(slices.Clone(v), ' ')
			}
			return &memObjects{objects: changed, f: f}
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			h, f, st := setup(c.store)
			_, meta, err := outcomeOf(h.generate(ctx, st.proposal.OrganizationID, st, testAttribution))
			var jf *jobs.Failure
			if !errors.As(err, &jf) || jf.Code != DatasetStorageFailed || jf.Retryable != c.retryable || meta != nil {
				t.Errorf("%v %v", err, meta)
			}
			if n := len(f.Requests()); n != 0 {
				t.Errorf("the provider was asked %d times", n)
			}
		})
	}
	// Storage that works: read once per binding, all before the call.
	var mem *memObjects
	h, f, st := setup(func(f *fake.Backend) objects.Store { mem = &memObjects{objects: objs, f: f}; return mem })
	o, _, err := outcomeOf(h.generate(ctx, st.proposal.OrganizationID, st, testAttribution))
	if err != nil || o.Status != review.Ready {
		t.Fatalf("%+v %v", o.Validation, err)
	}
	if len(f.Requests()) != 1 || !slices.Equal(mem.seen, []int{0, 0}) {
		t.Errorf("reads at provider requests %v, %d requests", mem.seen, len(f.Requests()))
	}
}

// H-003 review L5: a ready proposal binds its base version's whole dataset
// manifest, used or not, in D-034 order, as a version published from a
// draft does (internal/models, TWIN_IR_SPEC §5.2; D-062 (g)): T-026
// freezes the approved version's manifest from it. A candidate that stops
// using a dataset still binds it.
func TestAReadyProposalBindsTheBasesManifest(t *testing.T) {
	ctx := context.Background()
	constant := `[{"op":"test","path":"/nodes/1/id","value":"code_review"},` +
		`{"op":"replace","path":"/nodes/1/service_time/distribution","value":{"type":"constant","value":240}}]`
	f := scripted(t, "Fixed review time.", reference(constant))
	st, objs := withData(t, stored(t, "edit", "Fixed review time."))
	h, err := New(Config{Provider: client(t, f), Packs: packs(t), Actor: "worker-1", Log: discard(), Objects: &memObjects{objects: objs, f: f}})
	if err != nil {
		t.Fatal(err)
	}
	gen, err := h.generate(ctx, st.proposal.OrganizationID, st, testAttribution)
	if err != nil || gen.outcome.Status != review.Ready {
		t.Fatalf("%+v %v", gen.outcome.Validation, err)
	}
	if slices.ContainsFunc(gen.outcome.Candidate.Nodes, func(n model.Node) bool {
		return slices.ContainsFunc(n.Distributions(), func(d model.NodeDistribution) bool { return d.Distribution.Type == model.DistEmpirical })
	}) {
		t.Fatal("the candidate still uses a dataset")
	}
	// What Finish writes (Handle passes the generation's manifest).
	g, err := generated(gen.outcome, *gen.meta, gen.manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(g.DatasetBindings, st.version.DatasetBindings) || len(g.DatasetBindings) != 2 {
		t.Errorf("the proposal binds %v, want the base's %v", g.DatasetBindings, st.version.DatasetBindings)
	}
	// In D-034 order, whatever order a manifest came in.
	st.version.DatasetBindings = []model.DatasetBinding{st.version.DatasetBindings[1], st.version.DatasetBindings[0]}
	if gen, err = h.generate(ctx, st.proposal.OrganizationID, st, testAttribution); err != nil || gen.outcome.Status != review.Ready ||
		gen.manifest[0].LogicalRef != "dataset:review_duration" || len(gen.manifest) != 2 {
		t.Errorf("%v %v", gen.manifest, err)
	}
}
