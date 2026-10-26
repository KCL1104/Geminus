package engine

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// The hand-derived goldens of changeovers (ADR-026 D-047, D-048, D-049,
// D-051, D-052, D-055; T-050 interpretations D-097, PROPOSED;
// testdata/simulation/setup-goldens.json, HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN,
// each case with its derivation). The Result members of the same cases
// are checked in internal/simulation/des.

// setupExtras are what setup-goldens.json adds to calendar-goldens.json's
// expected facts.
type setupExtras struct {
	ID       string `json:"id"`
	Expected struct {
		Setups    [][6]jsontext.Value `json:"setups"`
		SetupSeqs []uint64            `json:"setup_seqs"`
		Streams   *struct {
			Resource   string `json:"resource"`
			Capacities []int  `json:"capacities"`
			Min        int    `json:"min_changeovers_per_unit"`
		} `json:"setup_streams"`
	} `json:"expected"`
}

func setupGoldens(t testing.TB) ([]calendarCase, map[string]setupExtras) {
	t.Helper()
	data := contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/setup-goldens.json"))
	var doc struct {
		Status string         `json:"status"`
		Cases  []calendarCase `json:"cases"`
	}
	var extras struct {
		Cases []setupExtras `json:"cases"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &extras); err != nil {
		t.Fatal(err)
	}
	if doc.Status != "HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN" || len(doc.Cases) == 0 || len(doc.Cases) != len(extras.Cases) {
		t.Fatalf("setup goldens: status %q, %d cases", doc.Status, len(doc.Cases))
	}
	out := map[string]setupExtras{}
	for _, x := range extras.Cases {
		out[x.ID] = x
	}
	return doc.Cases, out
}

// setupSeeds are the models of setup-goldens.json, for the fuzzers.
func setupSeeds(tb testing.TB) []jsontext.Value {
	tb.Helper()
	cases, _ := setupGoldens(tb)
	var out []jsontext.Value
	for _, c := range cases {
		out = append(out, c.Model)
	}
	return out
}

// observedSetups lists every Setup observation as
// entity@process/resource#unit@time+minutes, with the sequence number of
// its event, and checks D-055's order: within the grant's event, right
// after the bundle's ObsResource and right before the entity's
// ServiceStart at the same process; a resource that changes over, a
// process that needs it.
func observedSetups(t *testing.T, p *Program, obs []Observation) (setups []string, seqs []uint64) {
	t.Helper()
	ms, _ := p.unit.Millis()
	at := func(tick int64) string { return fmt.Sprint(float64(tick) / float64(ms)) }
	ids := map[uint64]string{}
	for i, o := range obs {
		switch o.Kind {
		case ObsArrival:
			ids[o.Entity] = FormatEntityID(0, p.nodes[o.Node].id, uint64(o.Value))
		case ObsSetup:
			if p.resources[o.Resource].setup == nil || p.nodes[o.Node].setupRes != o.Resource || o.At < 0 || o.At >= p.resources[o.Resource].capacity || o.Value < 0 {
				t.Errorf("Setup %+v", o)
			}
			if i == 0 || i+1 >= len(obs) {
				t.Fatalf("Setup %+v at the edge of the run", o)
			}
			before, after := obs[i-1], obs[i+1]
			if before.Kind != ObsResource || before.Seq != o.Seq || before.Tick != o.Tick {
				t.Errorf("Setup %+v not right after its grant's ObsResource: %+v", o, before)
			}
			if after.Kind != ObsServiceStart || after.Entity != o.Entity || after.Node != o.Node || after.Seq != o.Seq || after.Tick != o.Tick {
				t.Errorf("Setup %+v not right before the entity's ServiceStart: %+v", o, after)
			}
			setups = append(setups, fmt.Sprintf("%s@%s/%s#%d@%s+%s", ids[o.Entity], p.nodes[o.Node].id, p.resources[o.Resource].id, o.At, at(o.Tick), at(o.Value)))
			seqs = append(seqs, o.Seq)
		}
	}
	return setups, seqs
}

func (x setupExtras) want(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, s := range x.Expected.Setups {
		var entity, process, resource string
		var unit, at, minutes float64
		if json.Unmarshal(s[0], &entity) != nil || json.Unmarshal(s[1], &process) != nil || json.Unmarshal(s[2], &resource) != nil ||
			json.Unmarshal(s[3], &unit) != nil || json.Unmarshal(s[4], &at) != nil || json.Unmarshal(s[5], &minutes) != nil {
			t.Fatalf("%s: bad setup %v", x.ID, s)
		}
		out = append(out, fmt.Sprintf("%s@%s/%s#%v@%v+%v", entity, process, resource, unit, at, minutes))
	}
	return out
}

// Every setup golden but the streams (G-SU5, below): service starts,
// completions, blocks, downtimes, waiting failures, units on shift, every
// changeover with its unit, time and length and the sequence number of
// its DISPATCH, and the event count, against the hand derivation. These
// fix D-055 (the unit set up for the entity's type first, else the one of
// least index; the transition, else the default, else 0; no changeover
// for the first piece without initial; changeover then service in one
// SERVICE_COMPLETE) and its interactions: finish-first under breakdowns
// and calendars, unit identity of failures (D-097 (c)), a blocked entity
// keeping its unit.
func TestSetupGoldens(t *testing.T) {
	cases, extras := setupGoldens(t)
	ran := 0
	for _, c := range cases {
		x := extras[c.ID]
		if x.Expected.Streams != nil {
			continue
		}
		ran++
		t.Run(c.ID, func(t *testing.T) {
			p := c.compile(t)
			if !p.HasSetups() {
				t.Fatal("HasSetups is false")
			}
			col := &collect{}
			out, err := RunReplication(context.Background(), p, 0, NewBudget(1_000_000), col)
			if err != nil {
				t.Fatal(err)
			}
			if out.Created != out.Exited+out.Censored {
				t.Errorf("accounting %+v", out)
			}
			compareCalendarFacts(t, observedCalendarFacts(t, p, col.all), c.wantFacts(t), false)
			got, seqs := observedSetups(t, p, col.all)
			if want := x.want(t); !slices.Equal(got, want) {
				t.Errorf("changeovers %v\n hand-derived %v", got, want)
			}
			if !slices.Equal(seqs, x.Expected.SetupSeqs) {
				t.Errorf("Setup sequence numbers %v, hand-derived %v", seqs, x.Expected.SetupSeqs)
			}
			if c.Expected.Events == nil || out.Events != *c.Expected.Events {
				t.Errorf("%d events, hand-derived %v", out.Events, c.Expected.Events)
			}
		})
	}
	if ran != 8 {
		t.Errorf("%d setup goldens ran, want 8", ran)
	}
}

// G-SU5 (ADR-026 D-055 G-S5): unit u's changeovers draw from (resource ID,
// setup/<u>) in order, so one more unit of capacity does not shift a
// unit's draws. For each capacity the k-th Setup of unit u must have the
// k-th draw of that stream through the default's sampler, computed here
// from the stream itself.
func TestSetupStreamsPerUnit(t *testing.T) {
	cases, extras := setupGoldens(t)
	i := slices.IndexFunc(cases, func(c calendarCase) bool { return c.ID == "G-SU5" })
	c, w := cases[i], extras["G-SU5"].Expected.Streams
	var base model.ModelIR
	if err := model.Decode(c.Model, &base); err != nil {
		t.Fatal(err)
	}
	for _, capacity := range w.Capacities {
		ir := base
		ir.Resources = slices.Clone(base.Resources)
		r := slices.IndexFunc(ir.Resources, func(x model.Resource) bool { return x.ID == w.Resource })
		ir.Resources[r].Capacity = capacity
		in := input(ir)
		in.Config = c.Config
		p, err := Compile(in)
		if err != nil {
			t.Fatal(err)
		}
		col := &collect{}
		if _, err := RunReplication(context.Background(), p, 0, NewBudget(1_000_000), col); err != nil {
			t.Fatal(err)
		}
		res := slices.IndexFunc(p.resources, func(x cresource) bool { return x.id == w.Resource })
		got := make([][]int64, capacity)
		for _, o := range col.all {
			if o.Kind == ObsSetup {
				got[o.At] = append(got[o.At], o.Value)
			}
		}
		for u := range capacity {
			if len(got[u]) < w.Min {
				t.Fatalf("capacity %d: unit %d changed over %d times, want at least %d", capacity, u, len(got[u]), w.Min)
			}
			stream := Stream(ReplicationSeed(p.seed, 0), w.Resource, PurposeSetupPrefix+strconv.Itoa(u))
			for k, v := range got[u] {
				if want := p.resources[res].setup.def.draw(stream); v != want {
					t.Fatalf("capacity %d, unit %d, changeover %d: %d ticks, the stream's draw is %d", capacity, u, k, v, want)
				}
			}
		}
	}
}

// D-055's P0 limits and the setups' own rules are the validator's
// (SETUP_INVALID, UNKNOWN_REFERENCE); Compile checks them again, so a
// model past the validator never runs a bundle the kernel's unit identity
// does not handle (one unit of at most one resource with setups, none in a
// batch process) or a transition it cannot look up. On
// deterministic-queue.json (arrival -> service -> done, resource server),
// validator bypassed.
func TestCompileChecksSetups(t *testing.T) {
	one := constant(1)
	for name, edit := range map[string]func(ir *model.ModelIR){
		"two units": func(ir *model.ModelIR) {
			ir.Resources[0].Capacity = 2
			ir.Resources[0].Setups = &model.Setups{Default: &one}
			ir.Nodes[1].Requires[0].Units = 2
		},
		"two resources with setups": func(ir *model.ModelIR) {
			ir.Resources = append(ir.Resources, model.Resource{ID: "server_b", Type: ir.Resources[0].Type, Name: "B", Capacity: 1,
				QueueDiscipline: ir.Resources[0].QueueDiscipline, Setups: &model.Setups{Default: &one}})
			ir.Resources[0].Setups = &model.Setups{Default: &one}
			ir.Nodes[1].Requires = append(ir.Nodes[1].Requires, model.ResourceRequirement{ResourceID: "server_b", Units: 1})
		},
		"a batch process": func(ir *model.ModelIR) {
			ir.Resources[0].Setups = &model.Setups{Default: &one}
			ir.Nodes[1].Batch = &model.Batch{MinSize: 2, MaxSize: 2}
		},
		"a transition to itself": func(ir *model.ModelIR) {
			ir.Resources[0].Setups = &model.Setups{Transitions: []model.SetupTransition{{From: "change", To: "change", Distribution: one}}}
		},
		"an unknown initial": func(ir *model.ModelIR) {
			ir.Resources[0].Setups = &model.Setups{Initial: "nope", Default: &one}
		},
	} {
		t.Run(name, func(t *testing.T) {
			ir := golden(t, "deterministic-queue.json")
			ir.Resources = slices.Clone(ir.Resources)
			ir.Nodes = slices.Clone(ir.Nodes)
			ir.Nodes[1].Requires = slices.Clone(ir.Nodes[1].Requires)
			edit(&ir)
			var err error
			withoutValidator(t, func() { _, err = Compile(input(ir)) })
			var ie *InputError
			if !errors.As(err, &ie) || len(ie.Issues) != 1 || ie.Issues[0].Code != validate.SetupInvalid {
				t.Fatalf("want one SETUP_INVALID, got %v", err)
			}
		})
	}
}
