package engine

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

func lineFlowModel(t *testing.T) model.ModelIR {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/contracts/valid-line-flow-ir.json")), &ir); err != nil {
		t.Fatal(err)
	}
	return ir
}

// des-go-0.1.0 executes no line-flow primitive (ADR-026; T-047–T-051 add
// them). A model using one never compiles, so it is never simulated as if
// the member were absent: the validator's primitive gate refuses it first,
// with paths under /model (D-063 PROPOSED).
func TestCompileRefusesLineFlowPrimitives(t *testing.T) {
	_, err := Compile(input(lineFlowModel(t)))
	var ie *InputError
	if !errors.As(err, &ie) || !errors.Is(err, ErrInvalidInput) || len(ie.Issues) != 19 {
		t.Fatalf("want 19 PRIMITIVE_NOT_SUPPORTED issues, got %v", err)
	}
	for _, is := range ie.Issues {
		if is.Code != validate.PrimitiveNotSupported || is.Severity != model.SeverityError || is.Path[:7] != "/model/" {
			t.Errorf("issue %+v", is)
		}
	}
	issueExactly(t, err, "/model/nodes/4/capacity", validate.PrimitiveNotSupported)
	issueExactly(t, err, "/model/resources/1/breakdowns", validate.PrimitiveNotSupported)
}

// The kernel keeps its own list of what it executes, apart from what the
// validator checks: should a validator rule land before its kernel, Compile
// still refuses the model. Checked on a decoded model, past the validator.
func TestKernelRefusesWhatItDoesNotExecute(t *testing.T) {
	for _, p := range validate.Primitives {
		if kernelExecutes[p] {
			t.Errorf("%s is executed; update this test with the kernel task that implements it", p)
		}
	}
	ir := lineFlowModel(t)
	issues := unsupported(&ir)
	want := validate.Uses(&ir)
	if len(issues) != len(want) || len(want) != 19 {
		t.Fatalf("%d issues for %d uses", len(issues), len(want))
	}
	for i, u := range want {
		if issues[i] != validate.NotSupported(u) {
			t.Errorf("issue %d: %+v, want %+v", i, issues[i], validate.NotSupported(u))
		}
	}
	// Every golden model, none of which has a line-flow member, compiles as before.
	paths, _ := filepath.Glob(filepath.Join(contracttest.Root(t), "testdata/golden-models/*.json"))
	for _, p := range paths {
		g := golden(t, filepath.Base(p))
		if got := unsupported(&g); len(got) != 0 {
			t.Errorf("%s: %v", filepath.Base(p), got)
		}
	}
	if !slices.Equal(unsupported(&model.ModelIR{}), nil) {
		t.Error("an empty model uses a primitive")
	}
}
