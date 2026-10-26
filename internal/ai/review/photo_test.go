package review

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
)

// T-056: a proposal asked for with a photo may add only the steps the photo
// shows and no number of its own. The server checks what it can check: no
// more new process steps than the photo's boxes, and every number of the
// candidate a value the base model already has, except a resource's
// capacity, which may also be a count of the boxes.

func bareLine(t *testing.T) *model.ModelIR {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "photos", "bare-line.json")), &ir); err != nil {
		t.Fatal(err)
	}
	return &ir
}

func f(v float64) *float64 { return &v }

// station is a new process step with the SMT line's service time, as the
// photo template says to copy it.
func station(id, station, resource string) model.Node {
	return model.Node{ID: id, Type: model.NodeProcess, Name: "Station",
		ServiceTime:      &model.TimedDistribution{Distribution: model.Distribution{Type: "triangular", Min: f(0.9), Mode: f(1), Max: f(1.4)}},
		Requires:         []model.ResourceRequirement{{ResourceID: resource, Units: 1}},
		DomainAttributes: []byte(`{"station":"` + station + `","pass":"first"}`)}
}

func flow(id, from, to string, p float64) model.Flow {
	return model.Flow{ID: id, From: from, To: to, Routing: model.Routing{Type: "probability", Probability: p}}
}

// withStations is the bare line with an AOI and an ICT station and their
// buffers after SMT; ICT has two testers.
func withStations(t *testing.T) *model.ModelIR {
	t.Helper()
	c := bareLine(t)
	c.Resources = append(c.Resources,
		model.Resource{ID: "resource_aoi", Type: "machine", Role: "aoi", Name: "AOI", Capacity: 1, QueueDiscipline: "fifo"},
		model.Resource{ID: "resource_ict", Type: "machine", Role: "ict_tester", Name: "ICT", Capacity: 2, QueueDiscipline: "fifo"})
	c.Nodes = append(c.Nodes, model.Node{ID: "node_q_aoi", Type: model.NodeQueue, Name: "Conveyor"}, station("node_aoi", "aoi", "resource_aoi"),
		model.Node{ID: "node_q_ict", Type: model.NodeQueue, Name: "Conveyor"}, station("node_ict", "ict", "resource_ict"))
	c.Flows[2].To = "node_q_aoi"
	c.Flows = append(c.Flows, flow("flow_1", "node_q_aoi", "node_aoi", 1), flow("flow_2", "node_aoi", "node_q_ict", 1),
		flow("flow_3", "node_q_ict", "node_ict", 1), flow("flow_4", "node_ict", "shipped", 1))
	return c
}

func at(issues []model.ValidationIssue) []string {
	var out []string
	for _, is := range issues {
		out = append(out, is.Path)
	}
	return out
}

func TestPhotoAcceptsWhatThePhotoShows(t *testing.T) {
	base := bareLine(t)
	if issues := Photo(base, withStations(t), 6); len(issues) != 0 {
		t.Fatalf("two stations, six boxes: %v", issues)
	}
	// The base itself, or a rename only, has nothing new.
	renamed := bareLine(t)
	renamed.Nodes[2].Name = "SMT line 2"
	if issues := Photo(base, renamed, 0); len(issues) != 0 {
		t.Errorf("a rename with no boxes: %v", issues)
	}
}

func TestPhotoRefusesMoreStepsThanBoxes(t *testing.T) {
	base := bareLine(t)
	issues := Photo(base, withStations(t), 1)
	if !slices.Equal(at(issues), []string{"/changes"}) || issues[0].Code != OutputInvalid || issues[0].Message != sentPhotoSteps ||
		issues[0].Severity != model.SeverityError {
		t.Fatalf("two stations, one box: %+v", issues)
	}
	if issues := Photo(base, withStations(t), 0); !slices.Equal(at(issues), []string{"/changes"}) {
		t.Errorf("no boxes: %+v", issues)
	}
}

// A number the photo cannot show and the base does not have is the AI's
// own: a service time, a buffer size, a yield, a breakdown, a capacity
// beyond the boxes. A changed number of an existing step counts too.
func TestPhotoRefusesNumbersOfItsOwn(t *testing.T) {
	base := bareLine(t)
	for name, c := range map[string]struct {
		edit func(*model.ModelIR)
		want []string
	}{
		"an invented service time": {func(c *model.ModelIR) {
			c.Nodes[5].ServiceTime.Distribution = model.Distribution{Type: "triangular", Min: f(2), Mode: f(3), Max: f(5)}
		}, []string{"/nodes/5"}},
		"a buffer size": {func(c *model.ModelIR) { c.Nodes[4].Capacity = 37 }, []string{"/nodes/4"}},
		"a yield": {func(c *model.ModelIR) {
			c.Flows[4].Routing.Probability = 0.95
		}, []string{"/flows/4/routing/probability"}},
		"a capacity beyond the boxes": {func(c *model.ModelIR) { c.Resources[2].Capacity = 7 }, []string{"/resources/2/capacity"}},
		"a breakdown": {func(c *model.ModelIR) {
			c.Resources[1].Breakdowns = &model.Breakdowns{
				TimeToFailure: model.TimedDistribution{Distribution: model.Distribution{Type: "exponential", Mean: f(600)}},
				TimeToRepair:  model.TimedDistribution{Distribution: model.Distribution{Type: "constant", Value: f(20)}}}
		}, []string{"/resources/1"}},
		"an existing step's service time": {func(c *model.ModelIR) {
			c.Nodes[2].ServiceTime.Distribution = model.Distribution{Type: "constant", Value: f(0.75)}
		}, []string{"/nodes/2"}},
		"more units than the base requires": {func(c *model.ModelIR) { c.Nodes[5].Requires[0].Units = 3 }, []string{"/nodes/5"}},
	} {
		cand := withStations(t)
		c.edit(cand)
		issues := Photo(base, cand, 6)
		if !slices.Equal(at(issues), c.want) {
			t.Errorf("%s: %v, want %v", name, at(issues), c.want)
			continue
		}
		for _, is := range issues {
			if is.Code != OutputInvalid || is.Message != sentPhotoNumber || is.Severity != model.SeverityError {
				t.Errorf("%s: %+v", name, is)
			}
		}
	}
	// A capacity that is a value of the base is allowed whatever the boxes.
	cand := withStations(t)
	if issues := Photo(base, cand, 1); slices.Contains(at(issues), "/resources/1/capacity") {
		t.Errorf("capacity 1 with one box: %v", issues)
	}
}

// What the photo rule says survives the server's cleaning unchanged: its
// sentences are the review's own, its paths known members.
func TestPhotoIssuesAreClean(t *testing.T) {
	base := bareLine(t)
	cand := withStations(t)
	cand.Resources[2].Capacity = 9
	cand.Flows[4].Routing.Probability = 0.5
	issues := Photo(base, cand, 1)
	if len(issues) != 3 {
		t.Fatalf("%v", issues)
	}
	for _, is := range issues {
		if Clean(is) != is {
			t.Errorf("cleaning changed %+v into %+v", is, Clean(is))
		}
	}
	for _, s := range []string{SentTooManyBoxes, SentBoxOutside, SentBoxNumber, SentBoxPrompt, SentBoxLabel} {
		is := model.NewIssue(OutputInvalid, "", s, model.SeverityError)
		if Clean(is) != is {
			t.Errorf("the box sentence %q is not kept", s)
		}
	}
}
