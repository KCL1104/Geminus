# ADR-031 — Eino for the AI roles: version, dependencies and placement (H-058, H-059; slice E0)

Status: **PROPOSED** (2026-10-31, written by the E0 agent; the agent approved nothing). The decision to build the AI agent behaviour on Eino is the owner's: H-058 (APPROVED: 「我希望你能使用 https://github.com/cloudwego/eino 做而不是手搓出來」) and H-059 (APPROVED: every AI role refactored onto Eino graphs, behaviour unchanged, no second pipeline). The version, the placement rules and the JSON path below come from the synthesized design ([H-056_INVESTIGATIONS_ON_EINO.md](../design/H-056_INVESTIGATIONS_ON_EINO.md) §6.1, §6.2), whose sections 6.1–6.4 stand after H-060, as narrowed by the architecture delta H-061 (`docs/design/H-061_BACKGROUND_INVESTIGATOR.md`, approved by the owner in H-063: the investigation is an ADK agent in the worker, the API links no Eino, and "eino-fenced becomes ./internal/aigraph/... only"; its slice A1's rule changes fold into this slice while it is unmerged). The choice of sonic's compat path is the coordinator's (the design's owner question 6 had two options); the stderr exception it brings is recorded here for review. Awaiting independent review. Basis: ADR-011 (two-week rule, license column, no floating versions), ADR-027 and ADR-030 (the metered provider client and its adapters), D-093 (a) and D-094 (structured logs and their filter), AGENTS.md §2, §3 and §7.

## Context

H-058 and H-059 ask for the AI roles (proposal generation in edit, scenario, research and photo modes; the analyst; H-057's repair attempt; H-056's investigations, which H-060 and H-061 make an Eino ADK agent in the worker) to run on Eino, while every reviewed safety step stays as it is: the context and redaction, spend metering, the Standard-mode check, strict decoding, the server's review, the photo rules, the grounding checks and no-store. So Eino must reach a model only through the app's own metered client (`provider.LLMProvider`, the `*provider.Client` of ADR-027), never through a provider SDK of its own, and the engine and the domain packs must never depend on it. This slice (E0) pins the module and adds the rules; no package imports Eino yet, and no product behaviour changes.

## Decision

### Version

`github.com/cloudwego/eino` **v0.9.21**, exactly, in `go.mod` and `go.sum` (no floating version):

| | Value |
|---|---|
| Published | 2026-09-23T08:56:40Z (proxy.golang.org `.info`), 38 days before 2026-10-31: over two weeks (ADR-011) |
| Commit | `ba04fde8641057055c358d7ab5d3015a9ba825e1` (tag `v0.9.21`) |
| go.sum | `h1:kBAyAjsnPq5NDWjt0VdmP9HY/XVUHs05LoeqybudPRQ=`, `/go.mod h1:OBD1mrkfkt/pJa4rkg1P0VnaMeOVl7l8IAdEqY//3IQ=` (the design's values; `go mod verify`: all modules verified) |
| License | Apache-2.0 (`LICENSE-APACHE`; no NOTICE file) |
| Its `go.mod` | `go 1.18`; requires sonic v1.15.0, eino-contrib/jsonschema v1.0.3, gonja v1.5.3, uuid v1.6.0, wk8/go-ordered-map/v2 v2.1.8, yaml.v3 v3.0.1 and others below |

v0.9.22 (published 2026-10-19T10:03:00Z, 12 days before) fails the two-week rule until 2026-11-02, and the design needs nothing it adds. No newer patch is required: no advisory for v0.9.21 is known to this slice (govulncheck is NOT RUN, see Consequences). The pin was added with `go get github.com/cloudwego/eino@v0.9.21` and then `go get` of the seven packages the design uses at the same version, so that `go.sum` holds their whole closure: `compose`, `schema`, `components/model`, `components/tool`, `components/tool/utils`, `callbacks`, `adk`. No existing module's version changed (`go.mod` only gains `// indirect` lines).

### Modules it brings, and their licenses

Each license was read from the module's own files in the module cache (the zip `go.sum` pins); release times from proxy.golang.org's `.info`. Every one is over two weeks old. No module is copyleft.

Linked by the seven packages on some platform of the architecture matrix (`go list -deps` for the 16 configurations of `scripts/check-architecture`):

| Module | Version (published) | License | Why |
|---|---|---|---|
| `github.com/cloudwego/eino` | v0.9.21 (2026-09-23) | Apache-2.0 | the framework |
| `github.com/bytedance/sonic` | v1.15.0 (2026-01-22) | Apache-2.0; its `licenses/` directory carries the notices of code it derives from: Boost-1.0 (Drachennest, eisel_lemire), BSD-3-Clause (Go, golang-asm), Apache-2.0 (simdjson), MIT (yyjson) | Eino's JSON (messages, tool arguments); the compat path only, see below |
| `github.com/bytedance/gopkg` | v0.1.3 (2025-08-13) | Apache-2.0 | sonic (`lang/dirtmake`) |
| `github.com/eino-contrib/jsonschema` | v1.0.3 (2025-11-27) | MIT, Copyright (C) 2014 Alec Thomas (`COPYING`) | tool parameter schemas |
| `github.com/nikolalohinski/gonja` | v1.5.3 (2023-07-16) | MIT, Copyright (c) 2013-2014 Florian Schlachter | `schema`'s Jinja2 message templates (the design uses no ChatTemplate) |
| `github.com/dustin/go-humanize` | v1.0.1 (2023-01-10) | MIT, Copyright (c) 2005-2008 Dustin Sallings | gonja's built-in filters |
| `github.com/goph/emperror` | v0.17.2 (2019-07-02) | MIT, Copyright (c) 2017 Márk Sági-Kazár | gonja |
| `github.com/json-iterator/go` | v1.1.12 (2021-09-11) | MIT, Copyright (c) 2016 json-iterator | gonja's built-in filters |
| `github.com/modern-go/concurrent` | v0.0.0-20180306012644-bacd9c7ef1dd (2018-03-06) | Apache-2.0 | json-iterator |
| `github.com/modern-go/reflect2` | v1.0.2 (2021-09-11) | Apache-2.0 | json-iterator |
| `github.com/mailru/easyjson` | v0.7.7 (2021-02-06) | MIT, Copyright (c) 2016 Mail.Ru Group | ordered maps (`buffer`, `jwriter`) |
| `github.com/pelletier/go-toml/v2` | v2.0.9 (2023-07-12) | MIT, Copyright (c) 2013 - 2022 Thomas Pelletier, Eric Anderton | gonja's built-in filters |
| `github.com/pkg/errors` | v0.9.1 (2020-01-14) | BSD-2-Clause, Copyright (c) 2015, Dave Cheney | gonja, emperror |
| `github.com/sirupsen/logrus` | v1.9.3 (2023-05-21) | MIT, Copyright (c) 2014 Simon Eskildsen | gonja |
| `github.com/slongfield/pyfmt` | v0.0.0-20220222012616-ea85ff4c361f (2022-02-22) | BSD-3-Clause, Copyright (c) 2018, Stephen Longfield, Jr. | `schema`'s Python-style message templates |
| `github.com/wk8/go-ordered-map/v2` | v2.1.8 (2023-06-27) | Apache-2.0 | `schema` and jsonschema (ordered maps) |
| `github.com/yargevad/filepathx` | v1.0.0 (2021-05-01) | MIT, Copyright 2016 The filepathx Authors | gonja's built-in filters |
| `golang.org/x/exp` | v0.0.0-20230713183714-613f0c0eb8a1 (2023-07-13) | BSD-3-Clause, Copyright (c) 2009 The Go Authors | gonja's built-in filters (`constraints`, `slices`) |
| `gopkg.in/yaml.v3` | v3.0.1 (2022-05-27) | MIT and Apache-2.0 (its `NOTICE`: Copyright 2011-2016 Canonical Ltd.) | ordered maps, gonja's built-in filters |
| `github.com/bahlo/generic-list-go` | v0.2.0 (already required) | BSD-3-Clause | ordered maps |
| `github.com/buger/jsonparser` | v1.1.2 (already required) | MIT | ordered maps |
| `github.com/google/uuid` | v1.6.0 (already required) | BSD-3-Clause | Eino's IDs |
| `golang.org/x/sys` | v0.48.0 (already required) | BSD-3-Clause | logrus's terminal check (`unix`, `windows`) |

In `go.mod` and `go.sum` but linked on no platform of the matrix (sonic's native path, which go1.27 excludes): `github.com/bytedance/sonic/loader` v0.5.0 (2026-01-22, Apache-2.0), `github.com/cloudwego/base64x` v0.1.6 (2025-08-04, Apache-2.0), `github.com/klauspost/cpuid/v2` v2.2.9 (2024-10-26, MIT), `github.com/twitchyliquid64/golang-asm` v0.15.1 (2021-01-12, BSD-3-Clause), `golang.org/x/arch` v0.11.0 (2024-09-10, BSD-3-Clause). `go mod download` (the Dockerfiles' build stage) fetches them; no binary links them (`json-without-jit`).

### Why not eino-ext

`github.com/cloudwego/eino-ext` holds Eino's provider components (OpenAI, Claude, Gemini, Ark and others), retrievers, callbacks to hosted tracers and tools. Its model components call a provider with their own SDKs and HTTP clients, so a call through one would bypass the metered client: no spend reservation or settlement, no Standard-mode check, no approved-model check, no strict decoding of the answer, no output size limit and no no-store guarantee, and it would need a provider key outside the worker's configuration. The design (§6.3) instead builds one ChatModel, `internal/aigraph/einomodel.Metered`, over `provider.LLMProvider`. So eino-ext is not added, and the rules `no-eino-ext` and `no-eino-ext-in-tests` refuse it in production code and in tests (a test import would put it in `go.mod`). `github.com/eino-contrib/jsonschema`, which Eino itself links, is a schema library, not part of eino-ext.

### What may import Eino (`scripts/check-architecture`, design §6.2 and H-061)

The paths are the designs'; none of these packages exists yet, so the rules that apply only to them are marked `future`, and the probes create them through a go build overlay. H-056's investigation loop (`internal/investigations/loop`, an advance graph in the API) is removed by H-061 (§2: "Eino in the API: none; eino-fenced becomes ./internal/aigraph/... only"), so no package outside `internal/aigraph` imports Eino.

| Rule | What it says |
|---|---|
| `eino-fenced` | Only `internal/aigraph/...` imports `github.com/cloudwego/eino/...`: the metered ChatModel adapters (`einomodel`: `Metered`, and H-061's tool-calling `Planner`), the role graphs (`proposal`, `photo`, `analysis`) and the investigator agent (`investigator`, the ADK `ChatModelAgent`). Every other package reaches Eino only through `internal/aigraph` (the rule's one gateway). |
| `no-eino-in-the-api` | The API binary (`cmd/api`), its route assembly (`internal/api`) and the investigation packages only the API links (`internal/investigations`, its background executor `internal/investigations/executor`) never link Eino or eino-ext, directly or through any package: no gateway, not even `internal/aigraph` or the photo pipeline (H-061 §1 and §15.1: "cmd/api links neither Eino nor a live adapter"; the executor "holds no Eino and no provider"). `aigraph-only-in-the-worker` already keeps the role graphs out of the API; this rule says the same of Eino itself and holds even if that rule gains an exception. |
| `no-eino-in-the-api-tests` | The API binary's own tests never link Eino either. It does not apply to `internal/api` or the investigation packages: their closed-loop tests run the worker's handlers in process (`internal/api`'s tests already link the proposal generator, the analyzer and the runner), and those handlers link Eino once H-059 moves them onto the role graphs. `TestEinoAgreedPlacementsConform` shows those tests stay allowed. |
| `no-eino-in-kernel-packs-transport` (and `-tests`) | The generic kernel (`internal/model`, `internal/simulation`, `internal/results`, the `internal/domainpack` interface, `schemas`), every domain pack and the HTTP transport (`internal/httpapi`) never link Eino or eino-ext, directly or through any package, tests included. The kernel and the packs already refused it through their third-party allowlists; this says so with no gateway, and covers the transport. |
| `no-eino-ext`, `no-eino-ext-in-tests` | eino-ext nowhere. |
| `aigraph-has-no-storage` (future) | The role graphs, the agent and the adapters: their module closure is the kernel, `internal/ai/...` (the pure AI packages), `internal/ids`, `prompts` and `internal/aigraph/...`; no storage, tenant scope, write service, live adapter (`provider/anthropic`, `provider/meta`), config, observability, HTTP, process, file or SQL access; third parties only the kernel's and Eino's closure as listed above (sonic package by package, its compat path only); their own standard-library imports only the kernel's computation list (`kernelStd`); Go files only. Eino's closure links `os` underneath (gonja's template loaders, logrus, uuid, filepathx, sonic's warning) and `syscall` through logrus's `golang.org/x/sys`, so the standard-library allowlist applies to the packages' own imports, as for the kernel. The I/O list (network, `database/sql`, process, plugin, TLS) applies to the whole closure, except four imports that open nothing and are exempt only through the package that has them (`bring`): google/uuid's `database/sql/driver` (as in `db-only-in-storage`) and `net` (`net.Interfaces`, for version 1 node IDs), eino-contrib/jsonschema's `net` (the type `net.IP`), easyjson `buffer`'s `net` (`net.Buffers`, written to the writer it is given), and logrus's `golang.org/x/sys` (its terminal check). |
| `aigraph-only-in-the-worker` | The role graphs and the agent call models, so only the worker's side links them: `cmd/worker`, `cmd/ai-eval-live`, `internal/proposals/generator`, `internal/analyses/analyzer`, `internal/investigator` (H-061's investigation job handler), `internal/aievals` and `internal/ai/photopipeline` (the photo facade of §6.4). Never `cmd/api`, `internal/api`, the investigation packages or a service the API shares. This is the same boundary as `live-ai-only-in-the-worker`. |
| `photopipeline-is-pure-but-its-graph` | The design's one reviewed gateway: `internal/ai/photopipeline` leaves `ai-is-pure-but-the-adapter` for a rule with the very same lists (`TestPhotopipelineRuleKeepsTheAIPackagesLists`) plus one gateway, `internal/aigraph/photo`. Its own imports stay those of every pure AI package (a probe importing `net` or `os` still fails); no other AI package may front a role graph (the context builder linking the photo graph fails). |
| `ai-suggests-only` | Gains one `bring`: google/uuid's `database/sql/driver`, because the photo pipeline will reach Eino, which links uuid, through the photo graph; database/sql or a driver through uuid, and the driver through any other package, are still violations. |
| `json-without-jit` | See below. |

`internal/investigations/...` (the API's side) and `internal/investigator/...` (the worker's handler) are in `appLayers`, so the kernel, the packs, the AI packages and the role graphs can never link them. H-061's slice A1 lists four more rules, which do not concern Eino and are added with these, all over packages that do not exist yet: `investigations-only-in-the-api` (only `cmd/api` and `internal/api` link `internal/investigations/...`), `acting-for-only-in-the-executor` (only `internal/investigations/executor` and `cmd/api` link `internal/tenant/actingfor`), `proposal-effects-only-in-the-api` (only `cmd/api`, `internal/api`, `internal/proposals/approval` and `internal/investigations/...` link `internal/proposals/effect`), and `internal/investigator` joins `job-handlers-only-in-the-worker`. They are H-061's decisions, recorded in its design (and, for the acting-for scope, in ADR-032, which A1 writes), not in this ADR; `TestInvestigationPlacement` probes each of them, and `TestEinoAgreedPlacementsConform` shows H-061's planned packages break none.

### JSON: sonic's compat path

Eino uses `github.com/bytedance/sonic` for its JSON. sonic v1.15.0's native path (a JIT that writes generated machine code into executable memory through `sonic/loader`, plus SIMD assembly) is built only for `(amd64 && go1.17 && !go1.27) || (arm64 && go1.20 && !go1.27)`; with `go 1.27.0` in `go.mod` and toolchain go1.27.3, every platform builds its compat path instead, which is `encoding/json` (`sonic.APIKind == sonic.UseStdJSON`), with no JIT and no executable memory. The coordinator chose this path. The alternative, a sonic release that supports go1.27 natively (the design names v1.15.4), was not taken: it would map executable memory in the worker (and, under H-056's first placement, the API).

- **Enforced.** `json-without-jit` forbids, in every package, sonic's loader, JIT, JIT decoder, x86 encoder and native assembly, golang-asm and base64x. Minimal version selection would move sonic to a native release if any module required one, and no import would change; this rule (and `aigraph-has-no-storage`'s package-by-package sonic list) then fails. A sonic upgrade is a reviewed change to this ADR.
- **The stderr line (a reviewed exception to D-093 (a) and D-094).** On the compat path an `init` function of `github.com/bytedance/sonic/ast` writes one line to the process's stderr, before `main` runs:

  ```text
  WARNING: sonic/ast only supports (go1.17~1.26 and amd64 CPU) or (go1.20~1.26 and arm64 CPU), but your environment is not suitable and will fallback to encoding/json
  ```

  It is not a JSON line (D-093 (a)) and does not pass `internal/observability`'s filter (D-094), because it is printed with `fmt.Fprintf(os.Stderr, …)` by a dependency. It is constant text and carries no data, no ID, no prompt and no answer. It will appear once at the start of every process that links Eino: the worker (`cmd/worker`) from the first H-059 slice on, and the live eval (`cmd/ai-eval-live`). It never appears in the API process: H-061 (approved in H-063) places no Eino there, and `no-eino-in-the-api` refuses any path by which `cmd/api` could link it. This ADR proposes it as the one exception; `TestSonicRunsItsCompatPath` builds a program that links the seven Eino packages and checks that stdout reports the compat path and that stderr is exactly this line, so a second line (sonic's `decoder` or `encoder` packages each print their own when linked), a changed one or none (the native path) fails. The `cmd` tests capture the writer `run` is given, not `os.Stderr`, so they are unchanged. Log collectors that parse every stderr line as JSON will see one unparsable line per process start.

### The adapter contract (for E1; recorded, not built)

Per the design §6.3, with H-060's change: `einomodel.Metered` implements Eino's `model.ToolCallingChatModel` over `provider.LLMProvider` (one instance over the text client, one over the vision client). `Generate` takes exactly a system and a user message equal to the sealed prompt's texts (a tripwire refuses anything else), dispatches to `GenerateModelProposal` or `AnalyzeSimulation` so `Client.call` runs unchanged (verify, attribution, image and research gating, approved model, prompt size, per-organization slot, quote, reserve, settle, returned model, output size, strict decoding, output hash, no-store), returns the canonical answer with usage and the typed result, and unwraps errors to `*provider.Error`. Eino's retry and failover configurations, `effort` and thinking options and ChatTemplates are never used; callbacks carry no message content. Tool calling (H-060) is not `Metered`'s: H-061 keeps `Metered` refusing tools and adds `einomodel.Planner`, a tool-calling ChatModel over a separate investigator `provider.Client` on the same spend ledger, used only by the investigator agent in the worker (its slice A6).

## Consequences

- No binary links Eino after E0: `go.mod` lists it and its closure as `// indirect`, and **`go mod tidy` would remove them** until the first package imports Eino (E1). Do not run `go mod tidy` in between; if it is run, the Eino probes of `scripts/check-architecture` fail to load (they import Eino through an overlay), so the removal cannot pass unnoticed.
- `go mod download` in the Dockerfiles' build stage fetches the new modules (including the five that no platform links); the runtime images do not change.
- **NOT RUN**: `govulncheck` (v1.9.0, the version CI pins) on the repository with Eino added. CI runs it on every push; source-mode scanning reports only code that is reachable, and no package reaches Eino yet, so the first slice that links Eino (E1) must run it and record the result. The design reports that a scratch probe that linked Eino was scanned with 0 affecting vulnerabilities; this slice did not repeat that.
- Upgrading Eino, sonic or any module above follows ADR-011's two-week rule, updates this ADR, the lists in `scripts/check-architecture/rules.go` (`einoClosure`, `einoBrings`, `sonicJIT`) and [THIRD_PARTY_NOTICES](../THIRD_PARTY_NOTICES.md), and reruns `make architecture-check` (the matrix test `TestEinoAgreedPlacementsConform` shows a new module or a new I/O edge).
- AGENTS.md §7 keeps "an autonomous multi-agent framework" out of P0. The design uses compose graphs and one ADK `ChatModelAgent` (H-060), not Eino's multi-agent packages (`adk/prebuilt/deep`, `planexecute`, `supervisor`, `flow/agent/multiagent`). Nothing refuses them yet: `go list` resolves `supervisor`, `planexecute` and `multiagent/host` with the current `go.sum` (only `deep` needs a module it lacks, doublestar). A rule refusing them is PROPOSED, not added.
- If the owner withdraws H-058/H-059, remove the `go.mod` lines, these rules and this ADR; nothing else depends on them.

## Alternatives

- **eino-ext's model components** (rejected above): they bypass the metered client.
- **sonic's native path** (a release supporting go1.27): no stderr line, faster JSON, but executable memory written at run time in the worker; not taken (the coordinator's choice).
- **Wait for v0.9.22**: under two weeks old until 2026-11-02, nothing needed from it.
- **No Eino** (H-056's first answer): superseded by H-058.
