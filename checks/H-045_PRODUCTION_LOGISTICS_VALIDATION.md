# H-045 validation report: the production and logistics pack and a new model from a template (hackathon slice)

**Date: 2026-10-30 | Basis: owner decisions H-045 (「4 個產業 + 建立畫面 (Recommended)」) and H-046 (research real plants first; the templates must be realistic), [docs/domains/PRODUCTION_LOGISTICS.md](../docs/domains/PRODUCTION_LOGISTICS.md), AGENTS.md | Requirements: REQ-003, REQ-004, REQ-022, REQ-023 | Status: agent self-check, not acceptance; the one independent review of H-045 and H-046 is still to come.**

## 1. What was built

| Part | What | Files |
|---|---|---|
| Domain pack | `production_logistics` (Go package `productionlogistics`): ID, name, a 12-kind node catalog and a 7-role resource catalog, the fail-closed domain rule, no inference, no domain metrics, no photo vocabulary, prompt context that says what the templates approximate | `internal/domainpack/productionlogistics/pack.go` |
| Templates | `shoe_factory` (a vulcanized sneaker factory), `food_plant` (a pan bread bakery), `carton_plant` (a corrugated box plant), `warehouse` (a distribution center) as IR JSON, each with a zh-Hant names file; loader with typed errors and strict names files (the same shape as the electronics pack's) | `internal/domainpack/productionlogistics/template.go`, `templates/*.json` |
| Pack tests | catalog equals the doc's tables, copies, fail-closed, no inference/metrics/photo, prompt rules, positions, UI labels in both catalogs; each template valid without issues in both locales, its primitives, main line, release volume per day, shifts, roles, an unlimited buffer after every failure, synthetic metadata, locales identical but names, typed errors, strict names files; engine runs (schema-valid Result, bottleneck busiest, repeat equality, another seed differs, digest lock); the UI's default run (one week, H-047) shows the same bottleneck; locales run alike; conformance suite (4 templates × 2 locales); registration | `pack_test.go`, `template_test.go`, `engine_test.go`, `conformance_test.go`, `registry_test.go`, `export_test.go`, `testdata/template-run-lock.json` |
| Registry | the pack is served by the API and the worker | `internal/packs/packs.go`, `packs_test.go` (domains; the contract names exactly the served templates, `const` or `enum`; each template schema-valid under its own domain in each locale and refused at `/template` under every other) |
| Contract | `CreateModel` gets a `production_logistics` alternative whose `template` is an `enum` of the four; OpenAPI request example; TS contracts regenerated; four fixtures (two valid, two invalid at `/template`) | `schemas/http.schema.json`, `openapi/openapi.yaml`, `apps/web/lib/contracts/http.ts`, `testdata/contracts/*create-model-production-logistics*.json`, `testdata/contracts/cases.json`, `checks/contracts-check.json` |
| API | no handler change: `models.Service.Create` already builds any registered pack's template; a DB-gated closed-loop test creates all four (en, zh-Hant), checks the replay, a viewer's 403, wrong-domain and unknown-template 422, and runs one through the job loop | `internal/api/production_logistics_test.go` |
| AI context | the proposal context accepts every template in both locales and both modes within the default budget, nothing redacted; the photo path stays electronics only (`PhotoVocabulary` not implemented; the UI's `PHOTO_DOMAINS` unchanged) | `internal/ai/aicontext/production_logistics_test.go` |
| UI | project page: a "New model" action for editors and owners (not viewers); it opens an inline form above the list (no modal, no new route): the templates grouped under their domain's name with a one-line description, a name that follows the template until typed, Create model (the one primary) and Cancel (focus back to the action); `createModel` with an Idempotency-Key reused on retry and the reader's locale; refusals through `ProblemView`; then the new model's canvas. English first, zh-Hant the translation | `apps/web/app/projects/[projectId]/page.tsx`, `apps/web/features/models/new-model.tsx`, `new-model.module.css`, `templates.ts` |
| Catalogs | `domain.production_logistics.*` (name, 12 node kinds, 7 roles), `newModel.*`, `template.<id>.name/description` for all six templates, en and zh-Hant | `apps/web/messages/en.ts`, `zh-Hant.ts` |
| Web tests | picker: list equals the contract's, strings in both languages, roles, grouping, focus, prefill, request body and key, trimming and empty name, refusal/retry/new key, arrows, Cancel; generic views: the model page names the domain and offers no photo, the canvas and the 3D scene follow the template positions, Marey rows in flow order; contrast pair ink-2 on Selected Row Tint recorded | `apps/web/tests/new-model.test.tsx`, `production-logistics.test.tsx`, `tokens.test.ts` |
| Fake API and e2e | createModel in Line Works' project (role check, key, replay, 409 for a key reused with another request, 422 for a pair outside the contract), serving the pack's template file and the created model's draft, version and empty lists; keyboard-only creation in en and zh-Hant with axe, Cancel, 390 px in both languages, viewer | `apps/web-e2e/fake/servers.mjs`, `apps/web-e2e/tests/new-model.spec.ts` |
| Docs | domain spec with the research tables; README's domain list; TWIN_IR_SPEC §2 link to the new catalog; this report and its index line | `docs/domains/PRODUCTION_LOGISTICS.md`, `README.md`, `docs/TWIN_IR_SPEC.md`, `checks/H-045_PRODUCTION_LOGISTICS_VALIDATION.md`, `checks/README.md` |

No migration (the domain is IR data), no engine change, no change to another pack's behaviour, no AI approval or live setting touched.

## 2. Requirement mapping

| Asked | Where |
|---|---|
| H-045: a new pack `production_logistics`, only generic IR primitives | `pack.go` (no rule but the domain check, no metrics); `TestTemplates` (one resource per step, primitives listed); conformance suite |
| H-045: four synthetic templates (shoe, food, carton, warehouse) | `templates/*.json`; `TestTemplatesList`, `TestTemplateMetadata` (synthetic labelling kept in a created model) |
| H-045: zh-Hant names for each | `*.zh-Hant.names.json`; `TestTemplateLocales`, `TestNamesFileStrict`; calendar names and failure reasons are left out of the templates so nothing untranslated shows |
| H-045: a "New model from a template" action on the project page for every template the packs offer | `NewModelForm`; `new-model.test.tsx` (the list equals the contract's six); `new-model.spec.ts` |
| H-045: not the photo path; shelf life, temperature, pick paths approximated and said so | `TestNoInferenceNoDomainMetrics` (no `PhotoVocabulary`); `production-logistics.test.tsx` (no photo button); PRODUCTION_LOGISTICS §4 and the prompt rules |
| H-045: one model of each on the live demo organization | **NOT RUN** here: a deployment step for the coordinator (this agent does not deploy) |
| H-046: research real plants and warehouses; realistic templates; a source or "estimate" per value | PRODUCTION_LOGISTICS §3 (per template: the real-world flow, a parameter table, what is simplified) and §3.5 (sources); §4 below |
| Owner reminder: English first | template names and steps, the picker's copy and the default model name are English first, zh-Hant the translation; docs and this report in English |

## 3. The templates (synthetic, researched; PRODUCTION_LOGISTICS §3)

| Template | Steps | Bottleneck | Key parameters |
|---|---|---|---|
| `shoe_factory` | cutting → cut-kit buffer (150) → stitching (closing) → lasting → sole attaching and foxing → autoclave queue → vulcanizing (batch 150–300 pairs, 90 min) → cooling racks → de-lasting and finishing → final inspection (repair or scrap) → packing | stitching: 72 stitchers, tri(42, 50, 67) min a pair | 600 pairs a day in eight cutting orders of 75 (high-tops and low-tops alternating), one 8-hour day shift, Monday to Friday; 12 cutters, 4 lasting machines, 34 sole and foxing operators, 14 finishers; 6% defects at inspection (5% repaired, 1% scrapped) |
| `food_plant` | dough orders → mixing (batch of 1,200 loaves) → floor time → makeup → final proof → oven queue (30) → tunnel oven → depanning and cooling → slicing and bagging → metal detection → to dispatch | tunnel oven: 18 lot positions (720 loaves), tri(18, 20, 22) min a bake | one lot = 40 loaves; 38,400 loaves a day, Monday to Saturday, two 10-hour shifts and a 4-hour sanitation window; sesame bread on Friday's second shift with a topping changeover on the makeup line; final proof tri(50, 57, 65), cooling tri(60, 75, 90) min |
| `carton_plant` | plain and printed case orders → corrugating (case sheets) → flexo folder-gluer; die-cut tray and display orders → corrugating (die-cut sheets) → rotary die-cutter; both → quality check (reject) → strapping → palletizing | flexo folder-gluer: tri(4, 5, 7) min a load of 1,000 boxes plus a makeready of tri(10, 15, 25) min at each order change | 148 loads a day in 20 short orders, two shifts, Monday to Friday; the corrugator is shared by two corrugating steps so each family keeps its own route |
| `warehouse` | inbound trailers → unloading and receiving → staging (600) → put-away (pallets of 24–48) → reserve storage (6,000) → case picking → labeling and packing → scan check (correction) → outbound staging → trailer loading (batch 600–1,200) | case picking: 4 pickers early, 3 late, tri(0.35, 0.45, 0.75) min a case | 6,000 cases a day on five trailers of 1,200, two shifts, Monday to Friday; 9 receivers early and 3 late; 3 reach trucks |

In the UI's default run (H-047: one week from Monday 00:00, 5 replications, seed 1), during tuning the bottleneck was in use about 0.84 (stitchers), 0.89 (tunnel oven), 0.86 (flexo folder-gluer, much of it makeready) and 0.92 (pickers) of its shift time, each the largest work share of its template; `TestDefaultRunShowsTheBottleneck` asserts "largest work share" and "in use ≥ 0.8", not these figures. Mean throughput over the week was about 2,625 pairs, 5,679 lots (227,000 loaves), 728 loads and 30,000 cases; mean lead time about 794, 271, 76 and 324 min (exploratory runs during tuning, not asserted). These are engine outputs on synthetic inputs, not predictions for any plant.

## 4. Realism (H-046)

The four templates were first written with plausible minutes (2026-10-29) and then, per H-046, rebuilt on public sources read on 2026-10-30 (the full tables and links are PRODUCTION_LOGISTICS §3 and §3.5). What changed and why:

- **Shoe factory → a vulcanized canvas sneaker factory.** The batch "curing oven" became an autoclave, as vulcanized shoes are cured in a steam-heated pressure vessel loaded on racks, 75 min at 110 °C for textile shoes [S2] or at least 70–80 min at about 120 °C [S3]; the load is scaled from 500–1,000 pairs [S2] to 150–300 for one line. Crew sizes follow a factory design for 500–700 pairs a day: 10 cutters and 50–80 stitchers per 500 pairs, 50 assembly workers per 500 pairs [S1]; so stitching, not the lasting machine, is the bottleneck. The finished-shoe reject rate of about 1% [S4] sets scrap; the repair share is an estimate. The lasting machines' changeover was removed (the lasts travel with each pair).
- **Food plant → a pan bread bakery.** Steps and times from BAKERpedia: floor time about 15 min, final proof 50–65 min, bake 18–22 min, cooling from 93–97 °C to 32–43 °C [F1, F2]; mixer batches and cycle from Baking Business (2,000-lb batches; one batch every 24 min in its worked example) [F3]; the schedule (six days, two 10-hour shifts, 4 hours of maintenance and sanitation a day) and sesame on Fridays from a real bakery's profile [F4]; slicer speed from a machine listing [F5]. The bottleneck moved from batch rack ovens to a tunnel oven that paces the line.
- **Carton plant → a corrugated box plant with two converting routes.** Rated speeds from makers' pages (corrugator up to 400 m/min [C2], flexo folder-gluer up to 22,000 boxes an hour [C1], rotary die-cutter up to 12,000 sheets an hour [C3]); running speeds are estimates at about half to two thirds of rated. Order changes dominate: a 1997 TAPPI survey found converting machines producing about four hours of an eight-hour shift, with setup the significant loss [C4], against a newest-machine claim of 1–2 min [C1]; the template takes 10–25 min (estimate). Waste figures [C5] are material inside a load and are not modelled. Families now take their real routes (cases to the folder-gluer, die-cut work to the die-cutter) through their own corrugating steps on the shared corrugator.
- **Warehouse → a distribution center.** Picking at about 116 cases an hour from WERC's 2008 figure of 120 cases picked per hour [W1], travel included (about half of picker time [W2]); unloading about 12 person-hours a trailer [W3]; put-away in line with 18–30 pallets an hour [W4]. Volumes and crews rescaled (6,000 cases a day; 4 + 3 pickers).
- **Estimates.** Every breakdown parameter, every buffer size, the defect and reject rates except shoe scrap, the line rate of the bakery, and the trailer case count are estimates, each with its reasoning in the tables. Nothing cites a source that was not read; sources that could not be read (PDF theses and papers, pages that refused access) are not cited.
- **Scaling.** One item is a pair, a lot of 40 loaves, a load of 1,000 boxes or a case, chosen so that a week's run stays within about 30,000 items; crew sizes and machine counts are scaled to the chosen daily volume, not copied from one plant.
- **What cannot be expressed** with the generic primitives is approximated and listed in PRODUCTION_LOGISTICS §3 (per template) and §4: shelf life and over-proofing, merge and split, routing by product at a decision, pick paths and replenishment, cleaning crews; the engine is unchanged.

## 5. Commands and results

Run by the coordinator on the uncommitted tree, 2026-10-30 (Go 1.27.3, Node 24.21.0):

| Command | Result |
|---|---|
| `make backend-test` (`go vet` + `GEMINUS_FULL_DIFFERENTIAL=1 go test ./cmd/... ./internal/... ./schemas/... ./scripts/... ./prompts/...`) | exit 0; 86 packages ok, none failed; `internal/domainpack/productionlogistics` ok in 10.7 s |
| `make frontend-test` (eslint `--max-warnings=0`, typecheck, vitest) | exit 0; 55 files, 1170 tests passed |
| `make e2e-test` (Playwright 1.56.1, fake provider) | exit 0; 560 passed, 72 skipped (the screenshot tests, which run only with `E2E_SCREENSHOTS=1`), 0 failed, 18.9 min |
| `make spec-check` | exit 0; `PASS_LIMITED_SPEC_CHECKS` |
| `scripts/gen_ts_contracts.py --check` | exit 0; `PASS_TS_CONTRACTS_FRESH` (15 files, 110 types) |

The default-run test (`TestDefaultRunShowsTheBottleneck`, the UI's 7-day run of H-047) ran each template in 15–372 ms (`go test -run TestDefaultRunShowsTheBottleneck -v -count=1`: shoe 87 ms, bakery 129 ms, box plant 15 ms, distribution center 372 ms), within D-007's 300 s wall limit and its event and entity budgets.

## 6. NOT RUN

- `make backend-race`: not run locally (CI runs it).
- `make integration-test` and the DB-gated Go tests, among them `internal/api.TestProductionLogisticsTemplatesClosedLoop`: skipped locally without `GEMINUS_TEST_DATABASE_URL` (CI runs them on a disposable PostgreSQL).
- Screenshot baselines (`E2E_SCREENSHOTS=1`, CI's pinned container): not run; see §7.
- Creating one model of each template on the live demo organization (H-045): a deployment step for the coordinator.
- No live AI call was made (none needed).

## 7. Screenshots this change alters

The accepted baselines (`apps/web-e2e/tests/__screenshots__/screenshots.spec.ts/`) cover `/`, `/projects` (signed out and signed in), `/no-such-page`, the gallery, the canvas and the scenarios list. None shows the project page (`/projects/<id>`), the only page this change alters visually (the "New model" action in its header, and the form when opened). The canvas baseline is of the Line Works fixture model (software delivery), whose page is unchanged. So no existing baseline should change; the coordinator's CI screenshot run confirms it.

## 8. Limits and open items

- **PROPOSED (agent's choices within H-045 and H-046, for the review)**: the catalog's kinds and roles; the choice of a vulcanized sneaker factory, a pan bread bakery, a corrugated box plant and a case-picking distribution center as the four real-world references; every estimate in the tables; the scaling; one `enum` alternative in `CreateModel` rather than four `const` ones; the inline form rather than a modal or a `/projects/<id>/new` page; the software delivery example listed in the picker (it is in the contract today; remove it from the UI list or the contract if the demo should not offer it); the picker starting on the first template (the electronics line).
- The pack has no domain metrics: a run shows the engine's generic metrics only.
- The fake API serves the pack's template file as it is (no server build, validation or metadata rewrite).
- Redundant code noticed, not changed: the template loader (`applyNames`, `templateFrom`, `InvalidError`, `userMetadata`) is now repeated in three packs; a shared helper in `internal/domainpack` would remove it. `MANIFEST.sha256` is stale (README says no script checks it). Stale worktree copies sit under `.claude/worktrees/`.

## 9. Independent review (round 1, H-029)

An independent reviewer read the slice (contract, pack, API, fake API, picker, docs, tests), ran the pack's Go tests, the two picker test files and `go list -deps` on the generic packages, and spot-checked cited sources. No High.

- **M1, fixed.** The shoe template's metadata said "60 to 300 pairs a load (at most 45 min wait once 60 are waiting)" while its IR and this report say 150–300 and 30 min; the text is copied into every model made from the template and into the AI's context. It now matches the IR. The template's run digest changes with its text, so `testdata/template-run-lock.json` was updated for `shoe_factory` only (sha256:3dd55dde…); no parameter changed.
- **L1, fixed.** PRODUCTION_LOGISTICS §3.1 arithmetic: 50–80 stitchers × 480 min ÷ 500 pairs is 48–77 stitcher-minutes, not 48–96; the stitcher count's reasoning is spelled out; sole and foxing is a mean of 22 min, a little under half of 48, not "about 60%".
- **L2, fixed.** [W1] does not call 120 cases picked per hour a median; the doc and §4 now keep the source's wording.
- **L3, fixed.** Cancel is blocked (`aria-disabled`, focus kept) while a create is in flight, so a form opened again cannot send the request under a new key. New test: `new-model.test.tsx` "does not close while a create is in flight, so it cannot be sent twice"; it fails without the fix and passes with it.
- **L4, fixed.** zh-Hant: 碼頭門 → 月台, 堆棧 → 堆疊棧板 / 棧板堆疊 (messages and the template name files).

After the fixes: `go test -count=1 ./internal/domainpack/... ./internal/packs/... ./internal/ai/... ./internal/api/...` exit 0 (the DB-gated API tests skip locally); `npx vitest run tests/new-model.test.tsx tests/production-logistics.test.tsx` 27 passed; web lint and typecheck exit 0.
