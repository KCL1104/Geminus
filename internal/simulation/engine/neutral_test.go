package engine

import (
	"context"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// withoutValidator runs f with Compile validating nothing but the decoding,
// so that a test can compile a model the validator refuses on purpose (a
// neutral buffer on a cycle, which R1 refuses however large it is). The
// caller has checked everything else of the model with the validator.
func withoutValidator(t *testing.T, f func()) {
	t.Helper()
	defer func(v func([]byte) (validate.Result, error)) { validateIR = v }(validateIR)
	validateIR = func(data []byte) (validate.Result, error) {
		var ir model.ModelIR
		if err := model.Decode(data, &ir); err != nil {
			return validate.Result{}, err
		}
		return validate.Result{IR: &ir}, nil
	}
	f()
}

// neutral gives every explicit queue of ir a capacity of at least
// max_active_entities: such a buffer can never be full when an entity is
// routed into it, since the routed entity is one of the active ones.
func neutral(ir model.ModelIR, capacity int) (model.ModelIR, int) {
	out := ir
	out.Nodes = slices.Clone(ir.Nodes)
	n := 0
	for i := range out.Nodes {
		if out.Nodes[i].Type == model.NodeQueue {
			out.Nodes[i].Capacity = capacity
			n++
		}
	}
	return out, n
}

// ADR-026 D-047 step 2 and G-B6: the neutral setting gives exactly the
// observations (seq included) of the model without capacities, on the
// frozen kernel, and no Blocked or Unblocked observation.
func TestNeutralBuffersChangeNothing(t *testing.T) {
	checked, buffers := 0, 0
	for seed := range differentialModels / 5 {
		rng := rand.New(rand.NewPCG(uint64(seed), 4702))
		base := randomModel(t, rng)
		in := input(base)
		in.Config = config(float64(30+rng.IntN(240)), model.Minute)
		in.Config.Replications = 3
		in.Limits = differentialLimits[seed%len(differentialLimits)]
		p, err := Compile(in)
		if err != nil {
			continue
		}
		withCap, n := neutral(base, in.Limits.MaxActiveEntities+rng.IntN(3))
		if n == 0 {
			continue
		}
		nin := in
		nin.Model = withCap
		var np *Program
		withoutValidator(t, func() { np, err = Compile(nin) })
		if err != nil {
			t.Fatalf("seed %d: the neutral model does not compile: %v", seed, err)
		}
		if !np.HasFiniteBuffers() {
			t.Fatalf("seed %d: the neutral model has no finite buffer", seed)
		}
		checked++
		buffers += n
		index := uint32(seed % 3)
		nc := &collect{}
		_, nout, nerr := runKernel(context.Background(), np, index, NewBudget(200_000), nc)
		oc := &collect{}
		_, oout, oerr := runKernel010(context.Background(), p, index, NewBudget(200_000), oc)
		if d := sameRun(kernelRun{nc.all, nout, nerr}, kernelRun{oc.all, oout, oerr}); d != "" {
			t.Fatalf("seed %d: neutral buffers: %s", seed, d)
		}
		if slices.ContainsFunc(nc.all, func(o Observation) bool { return o.Kind == ObsBlocked || o.Kind == ObsUnblocked }) {
			t.Fatalf("seed %d: a neutral buffer blocked", seed)
		}
	}
	t.Logf("%d neutral models (%d buffers) identical to des-go-0.1.0", checked, buffers)
	if checked < 1000 {
		t.Fatalf("only %d neutral models", checked)
	}
}
