package validate_test

import (
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/model/validate"
)

// loop is the minimal IR with work in a rework loop (work -> d -> work | end)
// and the given service and arrival distributions.
func loop(t *testing.T, service, arrival map[string]any) validate.Result {
	t.Helper()
	r := run(t, edit(t, func(ir map[string]any) {
		obj(obj(nodes(ir)[1])["service_time"])["distribution"] = service
		if arrival != nil {
			obj(obj(nodes(ir)[0])["arrival"])["distribution"] = arrival
		}
		ir["nodes"] = append(nodes(ir), map[string]any{"id": "d", "type": "decision"})
		obj(flows(ir)[1])["to"] = "d"
		ir["flows"] = append(flows(ir), flow("d_work", "d", "work", 0.5), flow("d_end", "d", "end", 0.5))
	}))
	if !r.Valid() {
		t.Fatalf("semantic stage must pass before bindings: %v", r.Issues)
	}
	return r
}

var empirical = map[string]any{"type": "empirical", "samples_ref": "dataset:review_duration"}

// D-033: an empirical distribution is zero-time when its snapshot has a sample
// that quantizes to 0 ticks; the zero-time cycle check reruns at binding time.
func TestBindingsZeroTime(t *testing.T) {
	r := loop(t, empirical, nil)
	if got := validate.Bindings(r.IR, nil); len(got) != 0 {
		t.Errorf("no zero samples: %v", got)
	}
	if got := validate.Bindings(r.IR, map[string]bool{"dataset:other": true}); len(got) != 0 {
		t.Errorf("a zero sample in an unused dataset: %v", got)
	}
	got := validate.Bindings(r.IR, map[string]bool{"dataset:review_duration": true})
	if len(got) != 1 || got[0].Code != validate.ZeroTimeCycle || got[0].Path != "/nodes/1" {
		t.Errorf("zero sample in a loop: %v, want ZERO_TIME_CYCLE at /nodes/1", got)
	}

	// A positive constant elsewhere in the loop does not help: the cycle is
	// work -> d -> work and only work takes time.
	r = loop(t, map[string]any{"type": "constant", "value": 1}, nil)
	if got := validate.Bindings(r.IR, map[string]bool{"dataset:review_duration": true}); len(got) != 0 {
		t.Errorf("non-empirical loop: %v", got)
	}
}

// D-033: a source interarrival that can be 0 is rejected for empirical too.
func TestBindingsZeroArrival(t *testing.T) {
	r := loop(t, map[string]any{"type": "constant", "value": 1}, empirical)
	if got := validate.Bindings(r.IR, nil); len(got) != 0 {
		t.Errorf("positive samples: %v", got)
	}
	got := validate.Bindings(r.IR, map[string]bool{"dataset:review_duration": true})
	if len(got) != 1 || got[0].Code != validate.DistributionInvalid || got[0].Path != "/nodes/0/arrival/distribution" {
		t.Errorf("zero interarrival sample: %v", got)
	}

	// Both findings belong to the same stage and are reported together, sorted.
	r = loop(t, empirical, empirical)
	got = validate.Bindings(r.IR, map[string]bool{"dataset:review_duration": true})
	var paths []string
	for _, i := range got {
		paths = append(paths, i.Path+" "+i.Code)
	}
	if want := []string{"/nodes/0/arrival/distribution DISTRIBUTION_INVALID", "/nodes/1 ZERO_TIME_CYCLE"}; !slices.Equal(paths, want) {
		t.Errorf("issues %v, want %v", paths, want)
	}
}
