# E0 validation: Eino pinned, ADR-031, the architecture rules (H-058, H-059)

- Slice: E0 of [H-056_INVESTIGATIONS_ON_EINO.md](../docs/design/H-056_INVESTIGATIONS_ON_EINO.md) (§6.1 version and ADR, §6.2 placement and architecture rules).
- Decisions: H-058, H-059 (APPROVED: build the AI roles on Eino), H-060 (APPROVED: the design's §6.1–§6.4 stand; §5 and §6.5 are superseded by the pending architecture delta H-061). The sonic choice (compat path) is the coordinator's. [ADR-031](../docs/adr/ADR-031-eino.md) is **PROPOSED**; the agent approved nothing.
- Requirements and rules: AGENTS.md §2 (the generic engine imports no domain package, HTTP, pgx, LLM SDK; AI cannot act around the server), §3 (exact versions, licenses, ADR), REQ-003, T-042 (the architecture guard), ADR-011 (two-week rule), D-093 (a) and D-094 (stderr exception, proposed).
- Product behaviour: unchanged. No package imports Eino; no binary links it; no prompt, schema, OpenAPI, migration, UI or AI path changed.

## 1. What changed

| File | Change |
|---|---|
| `go.mod`, `go.sum` | `github.com/cloudwego/eino v0.9.21` and its closure, 24 `// indirect` requirements and 93 `go.sum` lines; no existing version changed. eino-ext not added. |
| `scripts/check-architecture/rules.go` | 9 new rules (26 → 35): `eino-fenced`, `no-eino-ext`, `no-eino-ext-in-tests`, `no-eino-in-kernel-packs-transport`, `no-eino-in-kernel-packs-transport-tests`, `json-without-jit`, `aigraph-has-no-storage` (future), `aigraph-only-in-the-worker`, `photopipeline-is-pure-but-its-graph`. Changed: `ai-is-pure-but-the-adapter` excepts `./internal/ai/photopipeline` (which the new rule holds to the same lists plus one gateway); `ai-suggests-only` gains `bring` of google/uuid's `database/sql/driver`; `appLayers` gains `./internal/investigations/...`. Lists `eino`, `einoUsers`, `einoExt`, `einoClosure`, `einoBrings`, `sonicJIT`, `liveAdapters`; helper `without`. |
| `scripts/check-architecture/eino_test.go` (new) | `TestEinoPlacement` (7 forbidden placements through overlays), `TestEinoAgreedPlacementsConform` (the agreed placements over the 16-configuration matrix: no violation at all), `TestSonicRunsItsCompatPath` (builds and runs a program linking the seven Eino packages), `TestEinoBringsOnlyTheirImports`, `TestPhotopipelineRuleKeepsTheAIPackagesLists`. |
| `scripts/check-architecture/check_test.go` | 38 probes added to `TestProbesAreCaught`; `TestPolicy` states the new policy independently of rules.go (and `internal/investigations` among the app layers); `TestSyntheticGraph` gains the photo pipeline, the review and the photo graph (a package for the new rule; the gateway allowed, the review refused). No existing assertion was changed, removed or skipped. |
| `docs/adr/ADR-031-eino.md` (new) | Decision, version and hashes, licenses of every new module, what may import Eino, why no eino-ext, the sonic compat path and its stderr line (proposed exception), the adapter contract for E1, NOT RUN. |
| `docs/THIRD_PARTY_NOTICES.md` | New section "Go modules brought by Eino". |
| `docs/adr/ADR-011-toolchain-bootstrap.md`, `docs/ARCHITECTURE.md` §2 | One paragraph each, pointing to ADR-031. |
| `checks/README.md`, `checks/docs-check.json` | This report's row; the docs check's counts. |

## 2. Findings while building it

1. **Eino's closure reaches `database/sql/driver`** (google/uuid), so `ai-suggests-only` (which forbids `database/sql/...` for every AI package, with no gateway) would refuse the photo pipeline's gateway of §6.2 once the photo graph exists. The design names only `ai-is-pure-but-the-adapter`. Fixed narrowly with the `bring` already reviewed for `db-only-in-storage` (T-035 I8): only uuid's driver import; database/sql or a driver through uuid, and the driver through anything else, still fail (`TestEinoBringsOnlyTheirImports`).
2. **`net` with cgo imports `C`**, which no third-party allowlist trusts, so a rule that allows `net` transitively and has an allowlist fails on the cgo configurations (seen in `TestEinoAgreedPlacementsConform`). `aigraph-has-no-storage` therefore forbids the whole I/O list, `net` included, across the closure, and brings exactly the four imports that open nothing: uuid's `database/sql/driver` and `net`, jsonschema's `net` (the type `net.IP`), easyjson `buffer`'s `net` (`net.Buffers`), logrus's `golang.org/x/sys`. This is stricter than the design's text (which forbade only HTTP, processes and SQL).
3. **sonic prints one line**: on go1.27 sonic v1.15.0 builds its compat path and `sonic/ast`'s `init` writes `WARNING: sonic/ast only supports (go1.17~1.26 and amd64 CPU) or (go1.20~1.26 and arm64 CPU), but your environment is not suitable and will fallback to encoding/json` to stderr. One line exactly with the seven packages (sonic's `decoder` and `encoder` would add one each; they are not linked). ADR-031 proposes it as the one exception to D-093 (a) and D-094; `TestSonicRunsItsCompatPath` pins it.
4. **Nothing stops minimal version selection from moving sonic to a native release** if any future module asks for one; `json-without-jit` (new, not in the design) fails in that case, so the coordinator's choice is enforced, not just written.
5. **Not added, on purpose**: the design's `investigations-only-in-the-api` and `proposal-effects-only-in-the-api` (§6.2). They encode §5 and §6.5 (effects only in the page's requests, the API's advance graph), which H-060 supersedes; where the loop runs and with which DB role is H-061's, pending. `eino-fenced` allows `internal/investigations/loop` as §6.2 says; `aigraph-only-in-the-worker` keeps it from linking a role graph until H-061 decides.
6. **`go mod tidy` removes the pin** while nothing imports Eino (`go mod tidy -diff` exit 1, every new line removed). Recorded in ADR-031; the overlay probes fail to load if it happens.

## 3. Commands and results

Toolchain: go1.27.3 linux/amd64 (`PATH=~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH`), Python from `.venv` (symlinked from the main checkout, not committed). No live AI call; no `.env` read; no database used.

| Command | Exit | Result |
|---|---|---|
| `curl https://proxy.golang.org/github.com/cloudwego/eino/@v/v0.9.21.info` | 0 | `"Time":"2026-09-23T08:56:40Z"`, hash `ba04fde8641057055c358d7ab5d3015a9ba825e1`; `@v/v0.9.22.info`: 2026-10-19T10:03:00Z (12 days: under two weeks); `@latest`: v0.9.22 |
| `curl …/eino/@v/v0.9.21.mod` | 0 | `go 1.18`; sonic v1.15.0, eino-contrib/jsonschema v1.0.3, gonja v1.5.3, uuid v1.6.0, … (ADR-031) |
| `go get github.com/cloudwego/eino@v0.9.21` | 0 | `go: added github.com/cloudwego/eino v0.9.21`; go.sum `h1:kBAyAjsnPq5NDWjt0VdmP9HY/XVUHs05LoeqybudPRQ=` and `/go.mod h1:OBD1mrkfkt/pJa4rkg1P0VnaMeOVl7l8IAdEqY//3IQ=`, equal to the design's |
| `go get` of `compose`, `schema`, `components/model`, `components/tool`, `components/tool/utils`, `callbacks`, `adk` at `@v0.9.21` | 0 | no output; `go.mod` +24 lines, all `// indirect`, `go.sum` +93, no line removed |
| `go mod verify` | 0 | `all modules verified` |
| `go mod why -m github.com/cloudwego/eino` | 0 | `(main module does not need module github.com/cloudwego/eino)` |
| `go mod tidy -diff` | 1 | would remove every requirement this slice adds (finding 6); not applied |
| `go list -deps` of the seven packages, 16 configurations (a scratch script, not committed) | 0 | 23 modules on some platform (ADR-031's table); no sonic loader, JIT, golang-asm, base64x, cpuid or x/arch on any |
| License files read from the module cache (scratch scripts, not committed) | 0 | Apache-2.0, MIT, BSD-2/3-Clause, yaml.v3 MIT+Apache-2.0; sonic's bundled notices Boost-1.0, BSD-3, Apache-2.0, MIT; no copyleft |
| Scratch program linking the seven packages (overlay build, not committed) | 0 | stdout `sonic.APIKind: UseStdJSON (compat: encoding/json)`, a one-node graph ran, a message marshalled; stderr: the one line of finding 3 |
| Tests first: `go test -count=1 -run 'TestPhotopipelineRuleKeepsTheAIPackagesLists\|TestProbesAreCaught/(the-engine-imports-eino\|the-graph-reads-storage\|a-handler-links-the-json-jit\|the-photo-pipeline-goes-online)$\|TestEinoPlacement/the_API' ./scripts/check-architecture/` before the rules | 1 | FAIL as intended: `photopipeline-is-pure-but-its-graph is gone`; no `no-eino-in-kernel-packs-transport`, `aigraph-has-no-storage`, `json-without-jit`, `aigraph-only-in-the-worker` violation (the kernel's old rules did already refuse Eino) |
| Baseline `go test -count=1 ./scripts/check-architecture/` with Eino in go.mod, rules unchanged | 0 | `ok … 475.876s` |
| `go test -count=1 -run '…synthetic and policy tests…' -v ./scripts/check-architecture/` | 0 | `TestSyntheticGraph`, `TestPatterns`, `TestEveryRuleEntryIsEnforced`, `TestPolicy`, `TestEinoBringsOnlyTheirImports`, `TestPhotopipelineRuleKeepsTheAIPackagesLists`, `TestTheUUIDGatewayBringsOnlyTheDriver` PASS |
| `go test -count=1 -run 'TestEinoPlacement\|TestSonicRunsItsCompatPath' -v ./scripts/check-architecture/` | 0 | 7 placements PASS; `TestSonicRunsItsCompatPath` PASS (1.34 s) |
| `go test -count=1 -run TestEinoAgreedPlacementsConform -v ./scripts/check-architecture/` | 1, then 0 | first run: `aigraph-has-no-storage … depends on C via … github.com/google/uuid -> net -> C` on the cgo configurations (finding 2); after the fix: PASS (19.57 s) |
| `go test -count=1 -run 'TestProbesAreCaught/(…the 38 new probes…)$' -v ./scripts/check-architecture/` | 0 | 38 PASS (60.4 s) |
| Mutation: drop `liveAdapters` and `net` from `aigraph-has-no-storage`, run `TestPolicy` and `TestEinoBringsOnlyTheirImports`, restore | 1 | 13 failures reported (the tests see the change); rules.go restored from the saved copy |
| `make architecture-check` | 0 | `PASS architecture: 101 packages, 35 rules, 16 build configurations` (26 rules before); run again on the final tree: the same line |
| `make backend-test` | 0 | gofmt clean, `go vet` clean, `GEMINUS_FULL_DIFFERENTIAL=1 go test` of `./cmd/... ./internal/... ./schemas/... ./scripts/... ./prompts/...`: 86 packages `ok`, 15 without test files, none failed; `scripts/check-architecture` 575.3 s; wall time 10 m 5 s. Started before two comment-only edits of rules.go, so the checker's package was run again alone (next row) |
| `go test -count=1 ./scripts/check-architecture/` on the final tree | 0 | `ok github.com/kcl1104/geminus/scripts/check-architecture 414.655s` (baseline before the rules: 475.9 s, under a different machine load) |
| `PATH=.venv/bin:$PATH make spec-check` | 0 | every check PASS (contracts, planning, docs, SQL inventory, reference arithmetic and engine vectors, decisions, TS contracts fresh, hashes, DB tests fresh, deploy); the only machine-written change is `checks/docs-check.json` (163 → 165 Markdown files, 305 → 314 relative links) |
| `make ai-eval` | 0 | `ok internal/ai/corpus`; the fake-provider reports `checks/ai-evals/fake-provider.json`, `fake-review.json`, `fake-analyst.json` rewritten byte-identical (no diff) |
| `make backend-build`; `go version -m` of `bin/api`, `bin/worker`, `bin/ai-eval-live`, `bin/migrate`, `bin/provision-tenant` | 0 | built; no module matching cloudwego, bytedance, gonja, logrus or eino-contrib in any of them (no binary links Eino) |
| `gofmt -l cmd internal schemas scripts prompts` on the final tree | 0 | no file listed |

## 4. NOT RUN

- **govulncheck** (v1.9.0) on the repository with Eino added: only network to the Go module proxy was in scope. No package links Eino, so a source-mode scan would not reach it; CI runs it on every push; E1 must run it once it links Eino (ADR-031).
- `make backend-race`: no Go code changed; the race detector has nothing new to see. NOT RUN.
- `make integration-test`, `make db-test`: no SQL, migration, RLS or DB code changed. NOT RUN.
- `make frontend-test`, `make e2e-test`: no web change. NOT RUN.
- `make ai-eval-live`, any live provider call: not allowed in this slice, and no AI path changed. NOT RUN.
- `make build-containers`, `make staging-smoke`: not run; the Dockerfiles' `go mod download` will fetch the new modules, which no binary links. NOT RUN.
- sonic v1.15.4 (or any native release) on go1.27: not downloaded or built; that it supports go1.27 natively is the design's statement.

## 5. PROPOSED (for review; nothing here is approved)

- ADR-031 as a whole, including the stderr exception to D-093 (a) and D-094 (one constant line per process that links Eino).
- `json-without-jit` and the stricter `aigraph-has-no-storage` (findings 2 and 4).
- The `ai-suggests-only` uuid `bring` and the photo pipeline's rule and gateway, added before the photo graph exists (design §6.2 asks E0 to add them).
- `investigations-only-in-the-api` and `proposal-effects-only-in-the-api` left for H-061 (finding 5).
- E1: import Eino, run `go mod tidy` (the requirements become direct), run govulncheck, and keep `TestSonicRunsItsCompatPath` green.
- A rule refusing Eino's multi-agent packages (`adk/prebuilt/...`, `flow/agent/multiagent/...`), for AGENTS.md §7's "no autonomous multi-agent framework": the design uses one ADK `ChatModelAgent` and graphs; `go list` resolves `supervisor`, `planexecute` and `multiagent/host` with the current `go.sum`, so nothing refuses them today. Not added (not in the design).

## 6. Risks

- Until E1 imports Eino, `go mod tidy` drops the pin (finding 6).
- The sonic line will be one non-JSON stderr line per process start once the worker links Eino; a log pipeline that parses every line as JSON sees it.
- `einoClosure` and `einoBrings` are exact: an Eino package outside the seven, or an Eino or sonic upgrade, can bring a module or an I/O edge that the rules refuse; that is a reviewed change to rules.go and ADR-031, and `TestEinoAgreedPlacementsConform` shows it.
- `scripts/check-architecture`'s test time grows (about 45 overlay loads and one matrix load more); the known overlay flake of its probes (checks/H-045_H-048_LIVE_VALIDATION.md §2) applies to the new probes too.
- H-061 may move the investigation loop; the fence then changes with it.
