package idpool

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

func golden(t *testing.T) *model.ModelIR {
	t.Helper()
	var ir model.ModelIR
	data := contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "golden-models", "default-delivery.json"))
	if err := model.Decode(data, &ir); err != nil {
		t.Fatal(err)
	}
	return &ir
}

// The Go type is the schema's IdAllocationPool, member for member.
func TestSchemaParity(t *testing.T) {
	contracttest.LoadSchemas(t).Parity(t, reflect.TypeFor[Pool](), "common.schema.json#/$defs/IdAllocationPool")
	for _, c := range contracttest.Cases(t) {
		if c.Schema != "schemas/common.schema.json#/$defs/IdAllocationPool" {
			continue
		}
		data := contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), c.Instance))
		var p Pool
		err := model.Decode(data, &p)
		if err == nil {
			err = p.Validate()
		}
		if (err == nil) != c.Valid {
			t.Errorf("%s: Validate = %v, fixture valid = %v", c.ID, err, c.Valid)
		}
		if c.Valid {
			contracttest.RoundTrip[Pool](t, c.ID, data)
		}
	}
}

// A new pool: 16 IDs of each kind, "<kind>_<lowercase ULID>", all distinct,
// none used anywhere in the base IR, valid under the schema, and different
// from the next pool.
func TestNew(t *testing.T) {
	base := golden(t)
	p, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := model.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	if issues, err := validate.Document(data, "common.schema.json#/$defs/IdAllocationPool", "X"); err != nil || len(issues) != 0 {
		t.Fatalf("the pool fails its schema: %v %v", issues, err)
	}
	used := entityIDs(base)
	seen := map[string]bool{}
	for _, k := range Kinds {
		ids := p.IDs(k)
		if len(ids) != PerKind {
			t.Errorf("%s: %d IDs", k, len(ids))
		}
		for _, id := range ids {
			if !strings.HasPrefix(id, string(k)+"_") || strings.ToLower(id) != id || used[id] || seen[id] {
				t.Errorf("%s: bad or repeated ID %q", k, id)
			}
			seen[id] = true
		}
	}
	q, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Equal(p.Node, q.Node) {
		t.Error("two pools are the same")
	}
}

// IDs the base IR already uses are never handed out, even when the
// generator happens to produce them.
func TestNewSkipsUsedIDs(t *testing.T) {
	base := golden(t)
	clash := "node_01m4ydqk00ta00000000000001"
	base.Nodes = append(base.Nodes, model.Node{ID: clash, Type: model.NodeSink})
	n := 0
	gen := func() string {
		n++
		if n <= 3 {
			return strings.TrimPrefix(clash, "node_") // the first three draws clash or repeat
		}
		return strings.ToLower(ulidFor(n))
	}
	p, err := newWith(base, gen)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(p.Node, clash) || p.Validate() != nil {
		t.Errorf("pool %v holds a used ID or is invalid", p.Node)
	}
	if _, err := newWith(base, func() string { return "01m4ydqk00ta00000000000001" }); !errors.Is(err, ErrExhausted) {
		t.Errorf("a generator that only repeats: %v, want ErrExhausted", err)
	}
	if _, err := New(nil); err == nil {
		t.Error("New(nil) must fail")
	}
}

func ulidFor(n int) string {
	s := "01M4YDQK00TA" + strings.Repeat("0", 14)
	d := []byte(s)
	for i := len(d) - 1; n > 0 && i >= 12; i-- {
		d[i] = "0123456789"[n%10]
		n /= 10
	}
	return string(d)
}

func TestValidate(t *testing.T) {
	good, err := New(golden(t))
	if err != nil {
		t.Fatal(err)
	}
	mutate := map[string]func(p *Pool){
		"15 flows":       func(p *Pool) { p.Flow = p.Flow[:15] },
		"17 nodes":       func(p *Pool) { p.Node = append(p.Node, "node_01m4ydqk00ta99999999999999") },
		"no resources":   func(p *Pool) { p.Resource = nil },
		"wrong kind":     func(p *Pool) { p.Resource[3] = "node_01m4ydqk00ta99999999999999" },
		"uppercase":      func(p *Pool) { p.Node[0] = "node_01M4YDQK00TA99999999999999" },
		"out of range":   func(p *Pool) { p.Node[0] = "node_81m4ydqk00ta99999999999999" },
		"not crockford":  func(p *Pool) { p.Node[0] = "node_01m4ydqk00tu99999999999999" },
		"short":          func(p *Pool) { p.Node[0] = "node_01m4ydqk00ta9999999999999" },
		"repeated":       func(p *Pool) { p.Node[5] = p.Node[4] },
		"server ID form": func(p *Pool) { p.Node[0] = "mdl_01M4YDQK00TA00000000000001" },
	}
	for name, m := range mutate {
		p := good.clone()
		m(&p)
		var pe *Error
		if err := p.Validate(); !errors.As(err, &pe) || !strings.HasPrefix(pe.Path, "/") {
			t.Errorf("%s: Validate = %v, want *Error with a JSON Pointer", name, err)
		}
	}
}

// Check (D-046): every node, flow and resource the candidate has and the
// base does not must take its ID from the pool's list of its own kind, an
// ID the base does not use.
func TestCheck(t *testing.T) {
	base := golden(t)
	p, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	clone := func() *model.ModelIR {
		data, err := model.Encode(base)
		if err != nil {
			t.Fatal(err)
		}
		var c model.ModelIR
		if err := model.Decode(data, &c); err != nil {
			t.Fatal(err)
		}
		return &c
	}

	// No new entity, or capacity changes only: nothing to check.
	same := clone()
	same.Resources[0].Capacity++
	if v := p.Check(base, same); len(v) != 0 {
		t.Errorf("an unchanged topology: %+v", v)
	}

	ok := clone()
	ok.Resources = append(ok.Resources, model.Resource{ID: p.Resource[0], Type: model.ResourceMachine, Name: "x", Capacity: 1, QueueDiscipline: model.FIFO})
	ok.Nodes = append(ok.Nodes, model.Node{ID: p.Node[0], Type: model.NodeDelay})
	ok.Flows = append(ok.Flows, model.Flow{ID: p.Flow[15], From: ok.Nodes[0].ID, To: p.Node[0]})
	if v := p.Check(base, ok); len(v) != 0 {
		t.Errorf("entities from the pool: %+v", v)
	}

	bad := clone()
	bad.Nodes = append(bad.Nodes,
		model.Node{ID: "invented_node", Type: model.NodeDelay},                   // not from the pool
		model.Node{ID: p.Flow[0], Type: model.NodeDelay},                         // the flow list
		model.Node{ID: "node_01m4ydqk00ta99999999999999", Type: model.NodeDelay}) // pool-shaped, not in this pool
	bad.Flows = append(bad.Flows, model.Flow{ID: base.Nodes[0].ID, From: "a", To: "b"}) // a base node's ID reused for a flow
	bad.Resources[0].ID = "renamed_resource"                                            // an identity change is a new entity
	got := p.Check(base, bad)
	want := []Violation{
		{Kind: Node, ID: "invented_node", Path: "/nodes/" + strconv.Itoa(len(base.Nodes)) + "/id", Reason: NotInPool},
		{Kind: Node, ID: p.Flow[0], Path: "/nodes/" + strconv.Itoa(len(base.Nodes)+1) + "/id", Reason: WrongKind},
		{Kind: Node, ID: "node_01m4ydqk00ta99999999999999", Path: "/nodes/" + strconv.Itoa(len(base.Nodes)+2) + "/id", Reason: NotInPool},
		{Kind: Flow, ID: base.Nodes[0].ID, Path: "/flows/" + strconv.Itoa(len(base.Flows)) + "/id", Reason: UsedInBase},
		{Kind: Resource, ID: "renamed_resource", Path: "/resources/0/id", Reason: NotInPool},
	}
	slices.SortFunc(want, func(a, b Violation) int { return strings.Compare(a.Path, b.Path) })
	if !slices.Equal(got, want) {
		t.Errorf("violations\n got %+v\nwant %+v", got, want)
	}

	// A pool that (by a bug elsewhere) holds an ID the base uses does not
	// let the candidate reuse it.
	leaky := p.clone()
	leaky.Node[1] = base.Nodes[1].ID
	reuse := clone()
	reuse.Nodes = append(reuse.Nodes, model.Node{ID: base.Nodes[1].ID + "_x", Type: model.NodeDelay})
	reuse.Flows = append(reuse.Flows, model.Flow{ID: base.Nodes[1].ID, From: "a", To: "b"})
	if v := leaky.Check(base, reuse); len(v) != 2 {
		t.Errorf("reuse through a leaky pool: %+v", v)
	}
	if v := p.Check(nil, ok); len(v) != 1 || v[0].Reason != Missing {
		t.Errorf("no base: %+v", v)
	}
}
