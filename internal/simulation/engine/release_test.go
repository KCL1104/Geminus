package engine

import (
	"encoding/json/v2"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// D-053's numerical profile (review L4) against the independent Python
// reference (scripts/engine_reference.py, release_schedules in
// testdata/simulation/engine-vectors.json), bit for bit: P = Λ(period),
// Λ⁻¹(a) with the one-step correction of r into (0, P] (G-R6: P = 0.3,
// a = 502.5 and others whose r falls outside), the segment search, the
// clamp, the quantization; and whole arrival runs (G-R2: a poisson
// schedule's first arrivals from a fixed seed, a schedule with rate-0
// segments, a deterministic takt with awkward rates, a fast one in seconds
// where the one-tick floor applies) with the arrival stream's and a's
// final states, so a poisson source draws exactly once per arrival plus
// the one after the horizon.

type releaseVectors struct {
	ReleaseSchedules struct {
		Correction releaseSteps `json:"correction"`
		Profile    releaseSteps `json:"profile"`
		Boundary   releaseSteps `json:"boundary"`
		Clamp      releaseSteps `json:"clamp"`
		Runs       []struct {
			ID               string                `json:"id"`
			Source           string                `json:"source"`
			Schedule         model.ReleaseSchedule `json:"schedule"`
			Unit             string                `json:"unit"`
			BaseSeed         int64                 `json:"base_seed"`
			ReplicationIndex uint32                `json:"replication_index"`
			Horizon          simulation.Duration   `json:"horizon"`
			TotalBits        string                `json:"total_bits"`
			ArrivalTicks     []int64               `json:"arrival_ticks"`
			ABitsAfter       string                `json:"a_bits_after"`
			StreamStateAfter string                `json:"stream_state_after"`
		} `json:"runs"`
	} `json:"release_schedules"`
}

type releaseSteps struct {
	Schedule  model.ReleaseSchedule `json:"schedule"`
	Unit      string                `json:"unit"`
	TotalBits string                `json:"total_bits"`
	Steps     []struct {
		ABits     string `json:"a_bits"`
		Corrected int    `json:"corrected"`
		TauBits   string `json:"tau_bits"`
		Tick      int64  `json:"tick"`
	} `json:"steps"`
}

func loadReleaseVectors(t *testing.T) releaseVectors {
	t.Helper()
	var v releaseVectors
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/engine-vectors.json")), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.ReleaseSchedules.Runs) == 0 || len(v.ReleaseSchedules.Correction.Steps) == 0 {
		t.Fatal("no release-schedule vectors")
	}
	return v
}

func TestReleaseProfileVectors(t *testing.T) {
	v := loadReleaseVectors(t)
	corrected := 0
	// Boundary: G-R1's rule with a rate-0 segment between two positive
	// ones; a = cumEnd_0 is released at the first segment's end (3, 9, 15),
	// never at the third's start.
	// Clamp: P = 0.1 x 3 rounds up, so a = P computes τ past the segment's
	// end; τ is clamped to it (3, not 3.0000000000000004).
	if len(v.ReleaseSchedules.Boundary.Steps) == 0 || len(v.ReleaseSchedules.Clamp.Steps) == 0 {
		t.Fatal("no boundary or clamp vectors")
	}
	for _, set := range []releaseSteps{v.ReleaseSchedules.Correction, v.ReleaseSchedules.Profile, v.ReleaseSchedules.Boundary, v.ReleaseSchedules.Clamp} {
		s, err := newReleaser(set.Schedule, model.TimeUnit(set.Unit))
		if err != nil {
			t.Fatal(err)
		}
		if got := bitsOf(s.total); got != set.TotalBits {
			t.Errorf("P = %s, reference %s", got, set.TotalBits)
		}
		for _, st := range set.Steps {
			a := hexBits(t, st.ABits)
			if got := bitsOf(s.tau(a)); got != st.TauBits {
				t.Errorf("a = %v: τ %s, reference %s", a, got, st.TauBits)
			}
			if got := s.tick(a); got != st.Tick {
				t.Errorf("a = %v: tick %d, reference %d", a, got, st.Tick)
			}
			if st.Corrected != 0 {
				corrected++
			}
		}
	}
	// G-R6: the correction steps are exercised both ways.
	if corrected < 5 {
		t.Errorf("only %d vectors need the correction of r", corrected)
	}
}

func TestReleaseRunVectors(t *testing.T) {
	v := loadReleaseVectors(t)
	for _, run := range v.ReleaseSchedules.Runs {
		t.Run(run.ID, func(t *testing.T) {
			sched := run.Schedule
			in := input(sourcesModel(t, []model.Node{{ID: run.Source, Arrival: &model.Arrival{Schedule: &sched}}}))
			in.Model.TimeUnit = model.ModelTimeUnit(run.Unit)
			in.Config = simulation.RunConfig{Horizon: run.Horizon, Warmup: simulation.Duration{Value: 0, Unit: run.Horizon.Unit},
				Replications: int(run.ReplicationIndex) + 1, Seed: run.BaseSeed, TraceLevel: simulation.TraceNone}
			p := compile(t, in)
			got, r, err := runArrivals(t, p, run.ReplicationIndex, nil)
			if err != nil {
				t.Fatal(err)
			}
			ticks := []int64{}
			for i, a := range got {
				ticks = append(ticks, a.tick)
				if want := fmt.Sprintf("r%d/%s/%d", run.ReplicationIndex, run.Source, i+1); a.id != want {
					t.Fatalf("arrival %d is %s, want %s", i, a.id, want)
				}
			}
			if !slices.Equal(ticks, run.ArrivalTicks) {
				t.Errorf("%d arrivals, reference %d; first difference at %d", len(ticks), len(run.ArrivalTicks), firstDiff(ticks, run.ArrivalTicks))
			}
			src := p.index[run.Source]
			if got := bitsOf(p.nodes[src].release.total); got != run.TotalBits {
				t.Errorf("P %s, reference %s", got, run.TotalBits)
			}
			if got := bitsOf(r.acc[src]); got != run.ABitsAfter {
				t.Errorf("a after the run %s (%v), reference %s (%v)", got, r.acc[src], run.ABitsAfter, hexBits(t, run.ABitsAfter))
			}
			if s := r.streams[streamKey{src, PurposeArrival}]; s == nil || fmt.Sprintf("%016x", s.state) != run.StreamStateAfter {
				t.Errorf("arrival stream state %v, reference %s (wrong number of draws)", s, run.StreamStateAfter)
			}
		})
	}
}

func firstDiff(a, b []int64) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

// D-053 G-R3 (common random numbers): doubling one segment's rate in a
// scenario moves the arrivals but not the draws: the k-th arrival of both
// runs uses the same X_k, so a_k is the same and τ_k = Λ⁻¹(a_k) of each.
func TestReleaseScheduleCommonRandomNumbers(t *testing.T) {
	base := model.ReleaseSchedule{Process: model.ReleasePoisson, Period: 60, Segments: []model.RateSegment{{Start: 0, Rate: 0.5}, {Start: 30, Rate: 0.25}}}
	double := base
	double.Segments = []model.RateSegment{{Start: 0, Rate: 1}, {Start: 30, Rate: 0.25}}
	run := func(s model.ReleaseSchedule) ([]int64, *releaser) {
		in := input(sourcesModel(t, []model.Node{{ID: "src", Arrival: &model.Arrival{Schedule: &s, MaxEntities: 400}}}))
		in.Config = config(100_000, model.Minute)
		p := compile(t, in)
		got, _, err := runArrivals(t, p, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		var ticks []int64
		for _, a := range got {
			ticks = append(ticks, a.tick)
		}
		return ticks, p.nodes[p.index["src"]].release
	}
	a, ra := run(base)
	b, rb := run(double)
	if len(a) != 400 || len(b) != 400 {
		t.Fatalf("%d and %d arrivals", len(a), len(b))
	}
	// Recover a_k from the base run's draws: the same stream, X_k = -ln(1 - U_k).
	rng := Stream(ReplicationSeed(7, 0), "src", PurposeArrival)
	acc, lastA, lastB, same := 0.0, int64(-1), int64(-1), 0
	for k := range 400 {
		acc += -ln(1 - rng.Uniform())
		wantA, wantB := max(ra.tick(acc), lastA+1), max(rb.tick(acc), lastB+1)
		if a[k] != wantA || b[k] != wantB {
			t.Fatalf("arrival %d: %d and %d, Λ⁻¹(a_k) of each %d and %d", k+1, a[k], b[k], wantA, wantB)
		}
		lastA, lastB = a[k], b[k]
		if a[k] == b[k] {
			same++
		}
	}
	// The doubled first half moves most arrivals, and never adds or drops one.
	if same > 200 || math.Abs(float64(a[399]-b[399])) == 0 {
		t.Errorf("%d of 400 arrivals unchanged by the scenario", same)
	}
}
