package photo_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/compose"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/aigraph/photo"
)

// E5 review round 1: the graph's two vision calls go through the metered
// Eino ChatModel (einomodel.Metered, H-059), never around it. Two checks,
// because results alone cannot tell (the adapter hands the client's result
// and error on unchanged): the source makes no client call of its own, and
// at run time each client call is made inside one of the adapter's own
// calls.

// The package's own code calls no model method of a client: the adapter's
// Generate is the only way to the vision model (the AST seal design §6.6
// gives E6 for the whole repository, here for this package now). Turn is
// the client's tool turn, which a photo proposal never takes.
func TestNoCallAroundTheMeteredChatModel(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{"GenerateModelProposal": true, "AnalyzeSimulation": true, "ProposeImportMapping": true, "Turn": true}
	fset := token.NewFileSet()
	var parsed, adapters, generates int
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed++
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if forbidden[sel.Sel.Name] {
				t.Errorf("%s: %s is a client's model call around the metered ChatModel", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "einomodel" && sel.Sel.Name == "New" {
				adapters++
			}
			if sel.Sel.Name == "Generate" {
				generates++
			}
			return true
		})
	}
	if parsed == 0 || adapters == 0 || generates == 0 {
		t.Fatalf("%d files, %d einomodel.New, %d Generate: the check would be vacuous", parsed, adapters, generates)
	}
}

type meteredKey struct{}

// watched is the spy with the adapter's mark on each call's context.
type watched struct {
	*spy
	mu     sync.Mutex
	inside []bool
}

func (w *watched) GenerateModelProposal(ctx context.Context, pc provider.ProposalContext) (provider.ProposalResult, error) {
	w.mu.Lock()
	w.inside = append(w.inside, ctx.Value(meteredKey{}) == true)
	w.mu.Unlock()
	return w.spy.GenerateModelProposal(ctx, pc)
}

// Each vision call is made inside one call of the adapter: its OnStart
// before the client is asked (the context the client gets is the one the
// adapter's OnStart returned), then its OnEnd, or its OnError when the call
// failed; one pair per call, nothing else from the adapter. The handler is
// the caller's, for this run only (callbacks attached per run, design
// §6.4).
func TestTheCallsGoThroughTheMeteredChatModel(t *testing.T) {
	for _, c := range []struct {
		name string
		fail bool
		want []string
	}{
		{"both calls answered", false, []string{"start", "end", "start", "end"}},
		{"both calls failed", true, []string{"start", "error", "start", "error"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			var events []string
			add := func(info *callbacks.RunInfo, e string) bool {
				if info == nil || info.Type != "GeminusMetered" {
					return false
				}
				mu.Lock()
				defer mu.Unlock()
				events = append(events, e)
				return true
			}
			h := callbacks.NewHandlerBuilder().
				OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
					if add(info, "start") {
						return context.WithValue(ctx, meteredKey{}, true)
					}
					return ctx
				}).
				OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackOutput) context.Context {
					add(info, "end")
					return ctx
				}).
				OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, _ error) context.Context {
					add(info, "error")
					return ctx
				}).Build()
			ctx := callbacks.InitCallbacks(context.Background(), &callbacks.RunInfo{Name: "photo-proposal-test"}, h)

			s := newSteps(t)
			s.after = photo.ToReview
			v := &watched{spy: &spy{}}
			if c.fail {
				v.answer = func(pc provider.ProposalContext) (provider.ProposalResult, error) {
					return provider.ProposalResult{Metadata: provider.Metadata{InputHash: pc.Prompt.InputHash}}, provider.NewError(provider.CodeRateLimited, 429, "meta")
				}
			}
			if err := photo.Run(ctx, v, s); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(v.inside, []bool{true, true}) {
				t.Errorf("the client's calls inside the adapter's: %v, want both", v.inside)
			}
			if !slices.Equal(events, c.want) {
				t.Errorf("the adapter's callbacks %v, want %v", events, c.want)
			}
			if !slices.Equal(v.nodes, []string{"generate", "generate_2"}) {
				t.Errorf("calls in %v", v.nodes)
			}
		})
	}
}

// A step's panic goes on to the caller only once the graph's run has ended
// (its node told, the run ended there): no goroutine of the run outlives
// Run. The graph's own end (Eino's graph callback, on the run's goroutine)
// is slowed, so a Run that did not wait for it would be recovered first.
func TestAPanicWaitsForTheRunToEnd(t *testing.T) {
	var mu sync.Mutex
	var graphEnded []string
	end := func(info *callbacks.RunInfo, how string) {
		if info == nil || info.Component != compose.ComponentOfGraph || info.Name != "photo" {
			return
		}
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		graphEnded = append(graphEnded, how)
		mu.Unlock()
	}
	h := callbacks.NewHandlerBuilder().
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackOutput) context.Context {
			end(info, "end")
			return ctx
		}).
		OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, _ error) context.Context {
			end(info, "error")
			return ctx
		}).Build()
	ctx := callbacks.InitCallbacks(context.Background(), &callbacks.RunInfo{Name: "photo-proposal-test"}, h)
	for _, at := range []string{"gate", "answered", "repaired"} {
		t.Run(at, func(t *testing.T) {
			mu.Lock()
			graphEnded = nil
			mu.Unlock()
			s := newSteps(t)
			s.after, s.panicAt = photo.ToReview, at
			var seen []string
			func() {
				defer func() {
					if recover() == nil {
						t.Error("no panic")
					}
					mu.Lock()
					seen = slices.Clone(graphEnded)
					mu.Unlock()
				}()
				_ = photo.Run(ctx, &spy{}, s)
			}()
			if len(seen) != 1 {
				t.Errorf("when the caller recovered the panic, the graph's run had ended %d times (%v), want once", len(seen), seen)
			}
		})
	}
}
