# H-046 (1): a realistic electronics assembly and test line template (2026-10-29/30)

**Request.** H-046 (1), the owner: 「在做這些情境時我希望你可以先研究實際工廠, 倉庫的樣子，我希望範本要是符合真實性的」 ("research what real factories and warehouses look like first; the templates must be realistic"), answered 「要，現在平行做 (Recommended)」 ("yes, in parallel now") for the electronics line template: its parameters are researched and tuned, with a source or "estimate" per value; the software delivery example pack does not change. The owner also asked for English first (「記得要以英文為主，因為黑客松是英文的」, "remember English first, because the hackathon is in English"): this report, the doc changes and the template's English names are in English, and the sources are English-language pages.

**Status.** Delivered on branch `worktree-agent-a519875b6ec9c21ee`, not reviewed. No push, no deploy, no live AI call. Requirement IDs: REQ-004 (templates), REQ-015 (no fabricated numbers), REQ-024/REQ-025 (electronics line, observed comparison). Decisions: H-046 (1); D-117 (g) (the template's settings) is touched by the changed values, see "Open questions".

## What changed

| File | Change |
|---|---|
| `internal/domainpack/electronicsline/templates/electronics_assembly_test_line.json` | 9 parameter values (below) and `metadata.origin`/`assumptions`. Topology, IDs, names, routing, calendar, releases, the pack rules and every value the demo's what-ifs replace are unchanged. |
| `internal/domainpack/electronicsline/templates/electronics_assembly_test_line.zh-Hant.names.json` | Not changed: no name changed. The English names were reviewed and are standard industry terms (SMT line, AOI, ICT, FCT, burn-in, touch-up, debug, retest, scrap). |
| `internal/domainpack/electronicsline/template_test.go` | `TestTemplateParameters` pins the new values. |
| `internal/domainpack/electronicsline/testdata/template-run-lock.json` | Result digest re-taken (the template changed); note says so. |
| `internal/domainpack/electronicsline/observed_test.go` | The regenerated log's counts (3,563 shipped) and the new model cycle means. |
| `testdata/datasets/electronics-line/*` (CSV, ground truth, 7 negative fixtures, README) | Regenerated with `go run ./scripts/gen-electronics-line-data` (seed 52, generator `electronics-line-synthetic-1`, unchanged): the data set is the template's own engine run, so it changes with the template. README: sizes, rows, hash, the model's description. |
| `scripts/gen-electronics-line-data/generate_test.go` | The pinned CSV SHA-256; 9 burn-in chambers; the shortest changeover gap 15 → 20 min (tighter, matching the new minimum). |
| `internal/api/synthetic_line_test.go` | A comment's row count (22,739 → 22,569). No assertion changed (its numbers come from the files). |
| `docs/domains/ELECTRONICS_LINE.md` | The "synthetic illustrative" statement replaced; §4.2 tables and capacity estimate; §4.4 changeover; new §4.5 (sources table, what changed, simplifications); §11 and §12 numbers. |
| `README.md` | One sentence: the template's magnitudes follow cited sources (§4.5). |
| `checks/README.md` | Index row for this report. |

No schema, migration, OpenAPI, AI code, UI code or other pack changed. `apps/web` tests that read the template (`twin-3d.test.tsx`, `editor-adapter.test.ts`) depend on its topology and 2D positions only, which did not change; no contract fixture holds template values.

## Research summary

The agent searched and read equipment makers' pages (ASMPT, Yamaha, D-TEK, Keysight), EMS and test vendors (NextPCB, Best Technology, Anzer, Mini-Circuits), trade magazines (I-Connect007, CircuitNet), a journal paper (ITEGAM-JETIA) and a job posting (shift pattern), all retrieved 2026-10-29. Every cited number was read on the page itself; the full table with links is ELECTRONICS_LINE §4.5. What the sources say, in short:

- **SMT pace.** Placement sets the line's pace. A contract manufacturer's fastest line placed 60,000 CPH; real rates run up to 40% below the rated speed (CircuitNet, Bliss) or about 60% of the IPC speed (I-Connect007, Klaver). About 600 placements a board at about 40,000 CPH real is about 54 s. Worked examples use 40 s line cycles (Indium; van Gastel). Printer core cycle 6.5–8 s (ASMPT) is not the limit.
- **Changeover.** Feeder replacement can take over 30 minutes, a line can stop more than 30 minutes when printer and mounter changeovers follow each other, and automation took one case from about 20 to 5 minutes (Yamaha); a TV-board line went from over 80 to 40 minutes (ITEGAM-JETIA 2018).
- **AOI.** 10 s for a 260 × 200 mm board including load and unload (D-TEK). Yield 97.26% in a worked example for a 1,000-component board (van Gastel); false calls below 0.5% at a CM (Downing and Owen).
- **ICT and FCT.** ICT about 30 s to 2 min per board, FCT about 1 to 10 min (NextPCB). ICT FPY above 95% (Keysight), 75% → above 93% after AOI (Downing and Owen), as low as 76% with a fixture contact problem (Keysight). Yields "well above 90%" for top-tier manufacturing (Mini-Circuits); no FCT-specific yield was found.
- **Burn-in.** Consumer 8–24 h, industrial 24–72 h (Best Technology); commercial and industrial 24–72 h at 85 °C (Anzer); an aerospace burn-in failure rate brought from 4% to 0.5% (Anzer).
- **Breakdowns.** No source with MTBF/MTTR of SMT machines or testers was found; the values stay estimates. Electronics OEE benchmarks are about 75–88%, and on SMT lines minor stops and speed dips usually outweigh breakdowns (TeepTrak); the template models neither.
- **Shifts.** Two shifts Monday to Friday at an electronics assembler (job posting).

Not found, so marked estimate: MTBF/MTTR, debug and touch-up times (orientation only: about 20–25 min a board at a university repair of detector boards), retest and repair ratios, FCT yield, packing time, buffer sizes, crew sizes, burn-in batch size and chamber count. Search results that came only as summaries and could not be read (two PDFs that would not decode, a 404, two pages that refused the request) are not cited.

## Parameters: old → new

| Parameter | Old (T-051) | New (H-046) | Why |
|---|---|---|---|
| SMT line pace `smt` | tri(0.9, 1.0, 1.4), mean 66 s | tri(0.75, 0.85, 1.15), mean 55 s | Derated placement rate for a mid-complexity board (§4.5) |
| SMT changeover A↔B | tri(15, 25, 45) | tri(20, 35, 60) | Feeder and printer changeovers of 30+ min without automation (Yamaha; ITEGAM-JETIA) |
| Inline AOI `aoi` | tri(0.8, 0.9, 1.1), mean 56 s | tri(0.2, 0.25, 0.35), mean 16 s | 10 s a board including handling (D-TEK); the old value was slower than the line |
| ICT `ict`, `ict_retest` | tri(1.8, 2.0, 2.6), mean 2.13 min | tri(1.4, 1.7, 2.3), mean 1.8 min | 30 s – 2 min test (NextPCB) plus manual fixture handling (estimate) |
| Burn-in `burn_in` | constant 240 | constant 480 | 8 h, the low end of 8–24 h for consumer products (Best Technology) |
| Burn-in chambers | 6 | 9 | What 720 boards a day need at 8 h with 40 a chamber (§4.2) |
| `q_pack` | 400 | 500 | Holds what 9 chambers and `q_burnin` finish overnight (120 + 9×40 = 480) |
| Unchanged, now sourced or marked estimate | FCT tri(2.8, 3.2, 4.0); yields AOI 0.97, ICT 0.95, FCT 0.93, burn-in 0.995; retest and repair ratios; touch-up and debug times; all breakdowns; calendar; releases; packing; other buffers; crews | — | Inside the sources' ranges, or no source (estimate) |

The values the demo's what-ifs test and replace (`deploy/demo/whatifs.json`: `ict_tester` 2, `debug_operator` 4, burn-in batch 20–40, `q_ict`/`q_fct` 10, 360 a release) are unchanged on purpose, so the what-ifs still apply to a model created from the new template.

**The bottleneck.** FCT, clearly. Rough estimate (§4.2) ρ: FCT 0.94, burn-in 1.02 on shift but 0.49 over the week (it runs 24/7), ICT 0.74, SMT 0.73 (0.81 with changeovers), debug 0.66, packing 0.41, AOI 0.20. Engine (28 days, 7-day warm-up, 5 replications, seed 2026), on-shift work ratio: FCT 0.897, ICT 0.710, SMT 0.687, burn-in 0.486, packing 0.410, AOI 0.200 (old template: FCT 0.898, ICT 0.843, SMT 0.825, AOI 0.701, packing 0.411, burn-in 0.365). 10,733 boards shipped in the 21 measured days (old 10,756) against 10,800 released. Blocking now shows the bottleneck's queue backing up: AOI blocked 41.8% of the time, ICT 26.4%, SMT 12.7%; FCT starved only 2.7%.

## The five demo what-ifs under the new parameters

Engine runs with a throwaway Go test in the pack (not committed; it built each what-if as the IR change `deploy/demo/whatifs.json` describes and ran it through `des.Engine` with the pack's metrics). Median lead time is `lead_time_minutes.p50`; output is the mean number of boards shipped in the window. Under the old template the same harness reproduced the recorded live numbers of [DEMO_WHATIFS_VALIDATION.md](DEMO_WHATIFS_VALIDATION.md) exactly (469.51 → 461.11 min and so on), so the demo's settings are one day, no warm-up, 5 replications, seed 1 (the web form's default).

**The demo's default settings (1 day, no warm-up, 5 replications, seed 1).** Baseline: median lead time 732.69 min, 318.2 boards shipped (old template: 469.51 min, 463.4).

| What-if | Median lead time | Output | Old template, same settings |
|---|---|---|---|
| Third ICT tester | 730.01 (−0.4%) | 318.2 (0) | 469.51 → 461.11 (−1.8%) |
| One more debug operator (as `whatifs.json`: capacity 4 → 5) | 732.69 (no change) | 318.2 (0) | no change |
| One more debug operator, with both shifts' crew 4 → 5 | 733.54 (+0.1%) | 318.2 (0) | 469.60 (+0.02%) |
| Burn-in batches of 48 | 751.23 (+2.5%) | 332.8 (+4.6%) | 469.51 → 475.37 (+1.2%) |
| Bigger buffers before ICT and FCT | 732.76 (+0.01%) | 318.2 (0) | 469.42 (−0.02%) |
| 20% more boards per release | 748.47 (+2.2%) | 344.0 (+8.1%) | 526.88 (+12.2%), 506.6 |

**Steady state (28 days, 7-day warm-up, 5 replications, seed 2026).** Baseline: median lead time 1,028.79 min (about 17 h), mean 1,242.88, p95 3,977.45 (boards that finish burn-in after Friday's last shift wait for Monday's packing), 10,733.2 boards shipped.

| What-if | Median lead time | Output |
|---|---|---|
| Third ICT tester | 1,025.62 (−0.3%) | 10,744.8 (+0.1%) |
| One more debug operator (as `whatifs.json`) | 1,028.79 (no change) | 10,733.2 (0) |
| One more debug operator, with both shifts' crew | 1,028.22 (−0.06%) | 10,724.8 (−0.1%, noise) |
| Burn-in batches of 48 | 1,022.97 (−0.6%) | 10,744.6 (+0.1%) |
| Bigger buffers before ICT and FCT | 1,023.81 (−0.5%) | 10,752.6 (+0.2%) |
| 20% more boards per release | 4,015.70 (+290%) | 11,160.0 (+4.0%) |

**One week (7 days, no warm-up, 5 replications, seed 1).** Baseline 1,010.96 min, 3,151.2 boards. Third ICT tester 1,012.50 (+0.2%); debug operator no change (as `whatifs.json`) or 1,010.76 (with crews); batches of 48 997.68 (−1.3%); bigger buffers 1,007.15 (−0.4%); 20% more boards 1,498.96 (+48%), 3,266.0 boards (+3.6%).

**Reading.** Four of the five what-ifs change something that is not the bottleneck, so they move lead time and output by about 1% or less; the fifth (+20% boards) loads FCT past its capacity (4,320 boards a week against about 3,820), so the release backlog grows week after week while output rises only 4%. That is the realistic lesson of the line, but none of the five shows an improvement at the bottleneck. For comparison (not one of the five, not added to the demo): a **fourth FCT tester** gives 978.28 min (−4.9%; mean −8.2%) in steady state and 916.38 min (−9.4%) over one week, and with 20% more boards it lets output grow 13% (12,125.2 boards).

## Commands and results

Go `go1.27.3` (toolchain path as in AGENTS.md's setup), Node v24.21.0, Python of the main checkout's `.venv` (this worktree has none). Run on 2026-10-29/30 in the worktree.

| Command | Exit | Result |
|---|---|---|
| `go test ./internal/domainpack/electronicsline/` with the new template, before updating any pin | 1 | Failed as expected, only on pinned values: the lock digest (`TestTemplateThroughTheEngine`; every other check of that test passed), `TestTemplateParameters`, and the old log's counts and cycle means (`TestCompareObservedOnTheSyntheticLog`) |
| `go run ./scripts/gen-electronics-line-data` | 0 | Wrote the 10 files; CSV 2,606,756 bytes, `sha256:42eb1055…62e1bc` |
| `go run ./scripts/gen-electronics-line-data -check` | 0 | The committed files are the generator's output |
| `go run ./scripts/gen-electronics-line-data -weeks 3 -out <scratch>` | 0 | 7,814,632-byte CSV (README's "about 7.8 MB" still holds); not committed |
| `make backend-test` (gofmt, `go vet`, `GEMINUS_FULL_DIFFERENTIAL=1 go test` of all Go packages) | 0 | 86 packages ok, none failed. Some packages came from Go's test cache of identical earlier runs in this session; the database tests in `internal/api` skip without `GEMINUS_TEST_DATABASE_URL` (see NOT RUN) |
| `go test -count=1 ./internal/domainpack/... ./internal/ai/aicontext/ ./internal/ai/review/ ./scripts/gen-electronics-line-data/ ./internal/import/validator/` | 0 | The packages that read the template, uncached |
| `npm ci` in `apps/web` (lockfile) | 0 | Dependencies installed in this worktree |
| `make frontend-test` (eslint, `tsc --noEmit`, vitest) | 0 | 53 test files, 1,142 tests passed |
| `make spec-check PYTHON=/home/kuoba123/Geminus/.venv/bin/python` | 0 | Contracts, planning, docs links (152 Markdown files, 289 relative links), SQL inventory, reference math, decisions, TS contracts, hashes, DB test render, deploy. A first run before this report existed failed (exit 2) on the link to it. `checks/docs-check.json` is rewritten by it and committed |

`make spec-check` does not run SQL and is not the full OpenAPI validator (AGENTS.md §4).

## NOT RUN

- `make e2e-test`: NOT RUN (another agent uses ports 3100–3103, as instructed). The browser screenshots of a model created from the template may show the changed values (for example 9 burn-in chambers); their baselines were not checked.
- `make integration-test` (disposable PostgreSQL): NOT RUN, no test database in this session. Its electronics tests (`TestSyntheticStationLogImport`, `TestSyntheticStationLogRules`, `TestSyntheticStationLogIsTheTemplatesRun`, the electronics run test) read the regenerated files and the template; they compare against those files, not against pinned numbers, but they did not run.
- `make backend-race`, `make ai-eval`: NOT RUN (not asked; no AI code changed).
- Live: NOT RUN. Nothing was deployed. The live demo model v1 was created from the old template and is immutable; the new values reach only models created from the template from now on.

## Open questions for the coordinator and the owner

1. **"One more debug operator" is a no-op.** `debug_operator` lists `shift_capacity` {early: 4, late: 4} (D-117 (g)), and the calendar uses a shift's units in place of `capacity` (`internal/model/calendar.FromModel`). The what-if changes only `capacity` 4 → 5, so nobody is added on shift: lead time and output are bit-for-bit the baseline's, under the old template too, and the utilization drop recorded live (40.6% → 32.5% = 4/5 of it) is only the larger denominator. A fix is to add `shift_capacity/early` and `shift_capacity/late` 4 → 5 to that scenario in `deploy/demo/whatifs.json`; not done here (outside this slice). With the fix the effect is still about zero, because debug is not a bottleneck (utilization about 0.32).
2. **The demo's one-day horizon.** With an 8-hour burn-in, a one-day run from an empty line ships only the boards that finish burn-in before packing closes at 22:00 (318 of 720), and its lead times are those of the boards that finished. A 7-day horizon gives a fairer comparison (numbers above). Changing the web form's default is a UI decision, not made here.
3. **A what-if at the bottleneck.** None of the five touches FCT; "Fourth FCT tester" (above) would show the bottleneck effect. Not added.
4. **Live model.** Whether to create a new live model from the new template (H-045 already plans models of each template on the live organization).
5. **D-117 (g)** (PROPOSED) still describes the SMT changeover as tri(15, 25, 45); H-046 changed it to tri(20, 35, 60). The register was not edited.
6. **Burn-in length.** 8 h is the low end of the cited consumer range; an industrial product would burn in 24–72 h and need about 3–9 times the chambers. The owner may prefer another product class.

## Not deleted, possibly redundant

- `internal/results/observed/observed_test.go` uses 3588 (the old log's weekly output) as an arbitrary input of a pure-function test; it is not tied to the template, so it was left as is.
- Historical reports (`checks/T-051_*.md`, `checks/T-052_*.md`, `checks/DEMO_WHATIFS_VALIDATION.md`) keep the old template's values as recorded.
