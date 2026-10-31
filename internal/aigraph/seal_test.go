package aigraph

// H-059 (slice E6; design §6.6, H-061 §5.3): the seal over the roles' move
// onto Eino. Every AI role runs on its Eino graph and reaches a model only
// through the metered ChatModel (einomodel.Metered), the investigator only
// through its own (einomodel.Planner, the ADK agent's model), so no second
// pipeline is left, and none of Eino's features the design keeps out is
// used:
//
//   - In non-test code, only internal/aigraph/einomodel (the adapters) and
//     internal/ai/provider (the metered client) name a client's model
//     method: provider.LLMProvider's role methods GenerateModelProposal,
//     AnalyzeSimulation and ProposeImportMapping, the client's tool turn
//     Turn (H-060) and its planner turn PlanTurn (H-061 §9, slice A5: the
//     investigator role's only path to a model, which only
//     einomodel.Planner calls; the worker's investigation handler and the
//     investigator eval both run the ADK agent over it). Any selector
//     counts, so a method value or a method expression does too. The one
//     exception is repair.Once's own second call: Once is H-057's reviewed
//     repair around a direct call, kept as the reference its tests and the
//     graphs' parity tests hold the graphs to; no non-test code uses it,
//     outside internal/ai/repair or in it.
//   - Nowhere, tests included: Eino's checkpoint stores (the rows are the
//     state, design §6.5): a graph's (WithCheckPointStore), an ADK runner's
//     (RunnerConfig's CheckPointStore field) and the ADK turn loop's
//     (TurnLoopConfig's Store with its CheckpointID: the turn loop is
//     refused as a whole, TurnLoopConfig and NewTurnLoop, as Store is too
//     common a name to refuse; H-061's investigator runs on adk.NewRunner),
//     the store's own types (CheckPointStore, CheckPointDeleter), the
//     checkpoint IDs a run loads from and writes to (WithCheckPointID, of
//     compose and of adk; WithWriteToCheckPointID), a resume from one
//     (ResumeWithParams) and a stored state's migration
//     (MigrateCheckpointState); its process-wide callback handlers
//     (AppendGlobalHandlers, InitCallbackHandlers; callbacks are attached
//     per run, §6.4); and the ADK's model retry and failover
//     (ModelRetryConfig, ModelFailoverConfig; the metered client retries
//     under its own reservation, §6.3).
//
// The check reads source (go/ast, no types): every .go file of every
// package of the module (go list ./...), whatever its build constraints. It
// sees names, not strings or comments.

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
)

// sealFile is one Go file of the module: its slash path from the module
// root, whether it is a test file, and its source.
type sealFile struct {
	path string
	test bool
	src  []byte
}

// sealFinding is one name the seal refuses, where it is.
type sealFinding struct {
	at   string // path:line
	name string
	why  string
}

func (f sealFinding) String() string { return f.at + ": " + f.name + ": " + f.why }

// sealReport is what the seal read and found.
type sealReport struct {
	findings []sealFinding
	files    int
	tests    int
	dirs     map[string]bool
	// allowed counts the model-method names it let pass, by "dir name"
	// (and "internal/ai/repair.Once GenerateModelProposal" for Once's).
	allowed map[string]int
}

// modelMethods are the client's model methods, which only the adapter and
// the client itself name in non-test code.
var modelMethods = map[string]string{
	"GenerateModelProposal": "provider.LLMProvider's proposal call, made only through the metered Eino ChatModel (einomodel.Metered) on a role graph",
	"AnalyzeSimulation":     "provider.LLMProvider's explanation call, made only through the metered Eino ChatModel (einomodel.Metered) on the analysis graph",
	"ProposeImportMapping":  "provider.LLMProvider's import-mapping call: the importer's future job calls it through the metered Eino ChatModel on a graph (design §6.4)",
	"Turn":                  "the client's tool turn (provider.Client.Turn, H-060), made only through the metered Eino ChatModel",
	"PlanTurn":              "the client's planner turn (provider.Client.PlanTurn, H-061 §9), made only through the investigator's Eino ChatModel (einomodel.Planner) on the ADK agent (internal/aigraph/investigator)",
}

// modelCallers are the directories whose non-test code may name them.
var modelCallers = []string{"internal/aigraph/einomodel", "internal/ai/provider"}

const (
	repairDir    = "internal/ai/repair"
	repairImport = "github.com/kcl1104/geminus/" + repairDir
	onceWhy      = "repair.Once is H-057's repair around a direct model call, the reference for tests only: a role graph runs the repair as its own nodes (repair.Plan, repair.Settle)"
)

// notUsed are the names of Eino's features the design keeps out, refused
// anywhere, tests included.
var notUsed = map[string]string{
	"WithCheckPointStore":     checkpointWhy,
	"CheckPointStore":         checkpointWhy,
	"CheckPointDeleter":       checkpointWhy,
	"TurnLoopConfig":          turnLoopWhy,
	"NewTurnLoop":             turnLoopWhy,
	"CheckpointID":            turnLoopWhy,
	"WithCheckPointID":        "the checkpoint an Eino run loads from and writes to: " + checkpointWhy,
	"WithWriteToCheckPointID": "the checkpoint an Eino run writes to: " + checkpointWhy,
	"ResumeWithParams":        "an ADK run resumed from its checkpoint: " + checkpointWhy,
	"MigrateCheckpointState":  "a stored Eino checkpoint's state: " + checkpointWhy,
	"AppendGlobalHandlers":    "process-wide Eino callback handlers: callbacks are attached per run, never globally (design §6.4)",
	"InitCallbackHandlers":    "process-wide Eino callback handlers: callbacks are attached per run, never globally (design §6.4)",
	"ModelRetryConfig":        "Eino's model retry: the metered client retries under its own reservation (design §6.3, H-061 §5.3)",
	"ModelFailoverConfig":     "Eino's model failover: the model is the person's choice, checked and metered by the client (design §6.3, H-061 §5.3)",
}

const (
	checkpointWhy = "an Eino checkpoint store: the database rows are the state, no second store (design §6.5, H-061 §5.3)"
	turnLoopWhy   = "the ADK's turn loop, which keeps its turns in a checkpoint store (its Store and CheckpointID): the database rows are the state, " +
		"no second store (design §6.5, H-061 §5.3; the investigator runs on adk.NewRunner without a store)"
)

// seal checks files: the names notUsed refuses anywhere, and in non-test
// code the model methods outside modelCallers (but Once's own call) and any
// use of repair.Once. A file that does not parse is an error.
func seal(files []sealFile) (sealReport, error) {
	r := sealReport{dirs: map[string]bool{}, allowed: map[string]int{}}
	for _, f := range files {
		file, err := parse(f)
		if err != nil {
			return r, fmt.Errorf("seal: %w", err)
		}
		r.files++
		if f.test {
			r.tests++
		}
		dir := path.Dir(f.path)
		r.dirs[dir] = true
		find := func(n ast.Node, name, why string) {
			r.findings = append(r.findings, sealFinding{at: fmt.Sprintf("%s:%d", f.path, parsed.fset.Position(n.Pos()).Line), name: name, why: why})
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				if why, bad := notUsed[id.Name]; bad {
					find(id, id.Name, why)
				}
			}
			return true
		})
		if f.test {
			continue
		}
		// The name the file gives internal/ai/repair, if it imports it.
		repairName := ""
		for _, is := range file.Imports {
			if p, err := strconv.Unquote(is.Path.Value); err != nil || p != repairImport {
				continue
			}
			repairName = "repair"
			if is.Name != nil {
				repairName = is.Name.Name
			}
			if repairName == "." {
				find(is, "repair.Once", "a dot import of internal/ai/repair names Once unqualified; "+onceWhy)
			}
		}
		caller := slices.Contains(modelCallers, dir)
		for _, d := range file.Decls {
			fd, _ := d.(*ast.FuncDecl)
			once := dir == repairDir && fd != nil && fd.Recv == nil && fd.Name.Name == "Once"
			var visit func(ast.Node) bool
			visit = func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					m := n.Sel.Name
					if why, model := modelMethods[m]; model {
						switch {
						case caller:
							r.allowed[dir+" "+m]++
						case once && m == "GenerateModelProposal":
							r.allowed[repairDir+".Once "+m]++
						default:
							find(n.Sel, m, why)
						}
					}
					if x, ok := n.X.(*ast.Ident); ok && repairName != "" && x.Name == repairName && m == "Once" {
						find(n.Sel, "repair.Once", onceWhy)
					}
					// The selected name is no use of a name of this package.
					ast.Inspect(n.X, visit)
					return false
				case *ast.Ident:
					if dir == repairDir && n.Name == "Once" && (fd == nil || n != fd.Name) {
						find(n, "repair.Once", "the repair package's own code uses Once; "+onceWhy)
					}
				}
				return true
			}
			ast.Inspect(d, visit)
		}
	}
	return r, nil
}

// parsed holds each source's syntax tree by its path and content, so a
// planted tree parses only the planted file again. The trees are only read.
var parsed = struct {
	mu    sync.Mutex
	fset  *token.FileSet
	files map[parseKey]*ast.File
}{fset: token.NewFileSet(), files: map[parseKey]*ast.File{}}

type parseKey struct {
	path string
	sum  [sha256.Size]byte
}

func parse(f sealFile) (*ast.File, error) {
	k := parseKey{f.path, sha256.Sum256(f.src)}
	parsed.mu.Lock()
	defer parsed.mu.Unlock()
	if file, ok := parsed.files[k]; ok {
		return file, nil
	}
	file, err := parser.ParseFile(parsed.fset, f.path, f.src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	parsed.files[k] = file
	return file, nil
}

var tree struct {
	once  sync.Once
	files []sealFile
	err   error
}

// treeFiles are the module's Go files: each package directory go list
// reports for ./..., every .go file in it.
func treeFiles(t *testing.T) []sealFile {
	t.Helper()
	root := contracttest.Root(t)
	tree.once.Do(func() {
		cmd := exec.Command("go", "list", "-e", "-f", "{{.Dir}}", "./...")
		cmd.Dir = root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			tree.err = fmt.Errorf("go list: %w: %s", err, stderr.String())
			return
		}
		for _, dir := range strings.Fields(string(out)) {
			entries, err := os.ReadDir(dir)
			if err != nil {
				tree.err = err
				return
			}
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
					continue
				}
				full := filepath.Join(dir, e.Name())
				src, err := os.ReadFile(full)
				if err != nil {
					tree.err = err
					return
				}
				rel, err := filepath.Rel(root, full)
				if err != nil {
					tree.err = err
					return
				}
				tree.files = append(tree.files, sealFile{path: filepath.ToSlash(rel), test: strings.HasSuffix(e.Name(), "_test.go"), src: src})
			}
		}
	})
	if tree.err != nil {
		t.Fatal(tree.err)
	}
	return tree.files
}

// The tree holds the seal: no finding, and the check is not vacuous (it
// read the packages that move onto the graphs and the ones that may call a
// model, and saw the adapter's and the client's calls and Once's).
func TestTheSealHolds(t *testing.T) {
	r, err := seal(treeFiles(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.findings {
		t.Error(f)
	}
	for _, dir := range []string{
		"internal/aigraph/einomodel", "internal/aigraph/analysis", "internal/aigraph/proposal", "internal/aigraph/photo",
		"internal/ai/provider", "internal/ai/repair", "internal/ai/photopipeline", "internal/proposals/generator",
		"internal/analyses/analyzer", "internal/aievals", "cmd/worker", "cmd/ai-eval-live", "cmd/api", "internal/aigraph",
		"internal/aigraph/investigator", "internal/investigator", "internal/ai/investigator",
	} {
		if !r.dirs[dir] {
			t.Errorf("the seal did not read %s", dir)
		}
	}
	if r.files < 500 || r.tests < 200 {
		t.Errorf("the seal read %d files, %d of them tests", r.files, r.tests)
	}
	for _, k := range []string{
		"internal/aigraph/einomodel GenerateModelProposal", "internal/aigraph/einomodel AnalyzeSimulation",
		"internal/aigraph/einomodel Turn", "internal/ai/provider Turn", "internal/aigraph/einomodel PlanTurn",
	} {
		if r.allowed[k] == 0 {
			t.Errorf("the seal saw no %q: the check would be vacuous", k)
		}
	}
	if n := r.allowed["internal/ai/repair.Once GenerateModelProposal"]; n != 1 {
		t.Errorf("repair.Once's own call seen %d times, want 1", n)
	}
	t.Logf("read %d files (%d tests) in %d directories; let pass %v", r.files, r.tests, len(r.dirs), r.allowed)
}

// planted is the tree with extra appended to the file at p, or, when no
// such file is in the tree and add is set, with a new file p of source
// extra.
func planted(t *testing.T, p, extra string, add bool) []sealFile {
	t.Helper()
	files := slices.Clone(treeFiles(t))
	for i, f := range files {
		if f.path == p {
			if add {
				t.Fatalf("%s is in the tree already", p)
			}
			files[i].src = append(slices.Clone(f.src), "\n"+extra+"\n"...)
			return files
		}
	}
	if !add {
		t.Fatalf("%s is not in the tree", p)
	}
	return append(files, sealFile{path: p, test: strings.HasSuffix(p, "_test.go"), src: []byte(extra)})
}

const repairPath = `"github.com/kcl1104/geminus/internal/ai/repair"`

// Each planted name is found, once, in the file it was planted in.
func TestTheSealFindsAPlantedName(t *testing.T) {
	for _, c := range []struct {
		name, path, extra string
		add               bool
		want              string
	}{
		{"a direct proposal call in the generator", "internal/proposals/generator/generator.go",
			"func zzPlanted(ctx context.Context, c provider.LLMProvider) { c.GenerateModelProposal(ctx, provider.ProposalContext{}) }", false, "GenerateModelProposal"},
		{"an explanation call in the analyzer", "internal/analyses/analyzer/analyzer.go",
			"func zzPlanted(ctx context.Context, c provider.LLMProvider) { c.AnalyzeSimulation(ctx, provider.AnalysisContext{}) }", false, "AnalyzeSimulation"},
		{"a method expression of the import mapping in the live eval", "internal/aievals/live.go",
			"var zzPlanted = (*provider.Client).ProposeImportMapping", false, "ProposeImportMapping"},
		{"a method value of the proposal call in the photo pipeline", "internal/ai/photopipeline/photopipeline.go",
			"func zzPlanted(c provider.LLMProvider) any { return c.GenerateModelProposal }", false, "GenerateModelProposal"},
		{"a tool turn in the photo graph", "internal/aigraph/photo/photo.go",
			"func zzPlanted(ctx context.Context, c *provider.Client) { c.Turn(ctx, provider.TurnContext{}) }", false, "Turn"},
		// The merge of H-061 A5 and A6 with E6: the planner turn
		// (provider.Client.PlanTurn) is a model method too, named only by
		// einomodel.Planner.
		{"a planner turn in the investigation handler", "internal/investigator/handler.go",
			"func zzPlanted(ctx context.Context, h *Handler) { h.cfg.Planner.PlanTurn(ctx, provider.PlanContext{}) }", false, "PlanTurn"},
		{"a planner turn in the investigator eval", "internal/aievals/investigator.go",
			"func zzPlanted(ctx context.Context, c *provider.Client) { c.PlanTurn(ctx, provider.PlanContext{}) }", false, "PlanTurn"},
		{"a planner turn in the agent", "internal/aigraph/investigator/agent.go",
			"func zzPlanted(p einomodel.PlanTurner) any { return p.PlanTurn }", false, "PlanTurn"},
		{"a method expression of the planner turn in the worker's main", "cmd/worker/main.go",
			"var zzPlanted = (*provider.Client).PlanTurn", false, "PlanTurn"},
		{"a direct call in the proposal graph", "internal/aigraph/proposal/proposal.go",
			"func zzPlanted(ctx context.Context, c provider.LLMProvider) { c.GenerateModelProposal(ctx, provider.ProposalContext{}) }", false, "GenerateModelProposal"},
		{"a direct call in a provider adapter", "internal/ai/provider/meta/meta.go",
			"func zzPlanted(ctx context.Context, c provider.LLMProvider) { c.GenerateModelProposal(ctx, provider.ProposalContext{}) }", false, "GenerateModelProposal"},
		{"a direct call in the worker's main", "cmd/worker/main.go",
			"func zzPlanted(ctx context.Context, c provider.LLMProvider) { c.AnalyzeSimulation(ctx, provider.AnalysisContext{}) }", false, "AnalyzeSimulation"},
		{"a direct call in the repair package outside Once", "internal/ai/repair/repair.go",
			"func zzPlanted(ctx context.Context, c provider.LLMProvider) { c.GenerateModelProposal(ctx, provider.ProposalContext{}) }", false, "GenerateModelProposal"},
		{"repair.Once in the generator", "internal/proposals/generator/generator.go",
			"func zzPlanted(ctx context.Context, c provider.LLMProvider) { repair.Once(ctx, c, provider.ProposalContext{}, provider.PriorAnswer{}, review.Outcome{}, nil, nil) }", false, "repair.Once"},
		{"repair.Once under another name", "internal/proposals/generator/zz_planted.go",
			"package generator\n\nimport fix " + repairPath + "\n\nvar zzPlanted = fix.Once\n", true, "repair.Once"},
		{"a dot import of repair", "internal/proposals/generator/zz_planted.go",
			"package generator\n\nimport . " + repairPath + "\n\nvar zzPlanted = Plan\n", true, "repair.Once"},
		{"Once from the repair package's own code", "internal/ai/repair/repair.go",
			"var zzPlanted = Once", false, "repair.Once"},
		{"a checkpoint store on a graph", "internal/aigraph/proposal/proposal.go",
			"var zzPlanted = compose.WithCheckPointStore(nil)", false, "WithCheckPointStore"},
		{"a runner's checkpoint store", "internal/investigator/zz_planted.go",
			"package investigator\n\nvar zzPlanted = adk.RunnerConfig{CheckPointStore: nil}\n", true, "CheckPointStore"},
		{"global callbacks in a graph", "internal/aigraph/analysis/graph.go",
			"func init() { callbacks.AppendGlobalHandlers() }", false, "AppendGlobalHandlers"},
		{"global callbacks in a test", "internal/aigraph/einomodel/metered_test.go",
			"func init() { callbacks.InitCallbackHandlers(nil) }", false, "InitCallbackHandlers"},
		{"the ADK's model retry", "internal/investigator/zz_planted.go",
			"package investigator\n\nvar zzPlanted = adk.ChatModelAgentConfig{ModelRetryConfig: nil}\n", true, "ModelRetryConfig"},
		{"the ADK's model failover", "internal/investigator/zz_planted_test.go",
			"package investigator\n\nvar zzPlanted = adk.ChatModelAgentConfig{ModelFailoverConfig: nil}\n", true, "ModelFailoverConfig"},
		// E6 review round 1, finding 2: the ADK's other checkpoint store (the
		// turn loop's Store and CheckpointID) and the checkpoint IDs a run
		// loads from and writes to.
		{"a turn loop's checkpoint store", "internal/investigator/zz_planted.go",
			"package investigator\n\nvar zzPlanted = adk.TurnLoopConfig[string, *schema.Message]{Store: s}\n", true, "TurnLoopConfig"},
		{"a turn loop", "internal/aigraph/analysis/graph.go",
			"var zzPlanted = adk.NewTurnLoop[string, *schema.Message]", false, "NewTurnLoop"},
		{"a turn loop's checkpoint ID", "internal/investigator/zz_planted.go",
			"package investigator\n\nfunc zzPlanted(c config) string { return c.CheckpointID }\n", true, "CheckpointID"},
		{"a graph run's checkpoint ID", "internal/aigraph/proposal/proposal.go",
			"var zzPlanted = compose.WithCheckPointID(\"x\")", false, "WithCheckPointID"},
		{"an agent run's checkpoint ID in a test", "internal/investigator/zz_planted_test.go",
			"package investigator\n\nvar zzPlanted = adk.WithCheckPointID(\"x\")\n", true, "WithCheckPointID"},
		{"a checkpoint to write to", "internal/aigraph/photo/photo.go",
			"var zzPlanted = compose.WithWriteToCheckPointID(\"x\")", false, "WithWriteToCheckPointID"},
		{"a runner resumed from a checkpoint", "internal/investigator/zz_planted.go",
			"package investigator\n\nfunc zzPlanted(ctx context.Context, r *adk.Runner) { r.ResumeWithParams(ctx, \"x\", nil) }\n", true, "ResumeWithParams"},
		{"a checkpoint store's deleter", "internal/investigator/zz_planted.go",
			"package investigator\n\nvar zzPlanted adk.CheckPointDeleter\n", true, "CheckPointDeleter"},
		{"a checkpoint's state migrated", "internal/aigraph/proposal/proposal.go",
			"var zzPlanted, _ = compose.MigrateCheckpointState(nil, nil, nil)", false, "MigrateCheckpointState"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, err := seal(planted(t, c.path, c.extra, c.add))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.findings) != 1 || r.findings[0].name != c.want || !strings.HasPrefix(r.findings[0].at, c.path+":") {
				t.Errorf("found %v, want %s in %s", r.findings, c.want, c.path)
			}
		})
	}
}

// E6 review round 1, finding 2: the reviewer's plant, an in-memory store
// handed to the ADK's turn loop to resume turns, is refused for each
// checkpoint name it uses (the store itself is the field Store, a name the
// seal cannot refuse alone), in non-test and test code alike.
func TestTheSealFindsATurnLoopsCheckpointStore(t *testing.T) {
	const plant = "package investigator\n\nfunc zzPlanted(invID string) any {\n\treturn adk.NewTurnLoop(adk.TurnLoopConfig[string, *schema.Message]{Store: memStore{}, CheckpointID: invID})\n}\n"
	for _, p := range []string{"internal/aigraph/investigator/zz_planted.go", "internal/aigraph/investigator/zz_planted_test.go"} {
		r, err := seal(planted(t, p, plant, true))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, f := range r.findings {
			if !strings.HasPrefix(f.at, p+":") {
				t.Errorf("a finding outside the plant: %v", f)
			}
			names = append(names, f.name)
		}
		slices.Sort(names)
		if want := []string{"CheckpointID", "NewTurnLoop", "TurnLoopConfig"}; !slices.Equal(names, want) {
			t.Errorf("%s: found %v, want %v", p, names, want)
		}
	}
}

// What the seal lets pass: a test's direct call, the adapter's and the
// client's own, sync.Once, and a name in a comment or a string.
func TestTheSealLetsPass(t *testing.T) {
	for _, c := range []struct {
		name, path, extra string
		add               bool
	}{
		{"a test's direct call", "internal/proposals/generator/graph_test.go",
			"func zzPlanted(ctx context.Context, c provider.LLMProvider) { c.GenerateModelProposal(ctx, provider.ProposalContext{}); c.Turn(ctx) }", false},
		{"the adapter's call", "internal/aigraph/einomodel/zz_planted.go",
			"package einomodel\n\nfunc zzPlanted(c provider.LLMProvider) any { return c.ProposeImportMapping }\n", true},
		{"the planner adapter's turn", "internal/aigraph/einomodel/zz_planted.go",
			"package einomodel\n\nfunc zzPlanted(ctx context.Context, p PlanTurner) { p.PlanTurn(ctx, provider.PlanContext{}) }\n", true},
		{"a test's planner turn", "internal/investigator/zz_planted_test.go",
			"package investigator\n\nfunc zzPlanted(ctx context.Context, c *provider.Client) { c.PlanTurn(ctx, provider.PlanContext{}) }\n", true},
		{"the client's call", "internal/ai/provider/provider.go",
			"func zzPlanted(ctx context.Context, c LLMProvider) { c.AnalyzeSimulation(ctx, AnalysisContext{}) }", false},
		{"sync.Once in the repair package", "internal/ai/repair/repair.go",
			"var zzPlanted sync.Once", false},
		{"names in a comment and a string", "internal/proposals/generator/generator.go",
			"// c.GenerateModelProposal(ctx, pc) and compose.WithCheckPointStore(s)\nvar zzPlanted = \"repair.Once AppendGlobalHandlers ModelRetryConfig\"", false},
		// H-061 §5.3's runner, with no checkpoint store, and a field called
		// Store on something else (E6 review round 1, finding 2).
		{"the investigator's runner without a store", "internal/aigraph/investigator/zz_planted.go",
			"package investigator\n\nfunc zzPlanted(ctx context.Context, a adk.Agent) *adk.Runner {\n\treturn adk.NewRunner(ctx, adk.RunnerConfig{Agent: a, EnableStreaming: false})\n}\n", true},
		{"a field called Store", "internal/proposals/generator/generator.go",
			"func zzPlanted(c Config) any { return c.Store }", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, err := seal(planted(t, c.path, c.extra, c.add))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.findings) != 0 {
				t.Errorf("found %v", r.findings)
			}
		})
	}
}

// A file that does not parse is an error, not a pass.
func TestTheSealRefusesWhatItCannotRead(t *testing.T) {
	if _, err := seal([]sealFile{{path: "internal/x/x.go", src: []byte("package x\n\nfunc {")}}); err == nil {
		t.Error("a file that does not parse passed")
	}
}
