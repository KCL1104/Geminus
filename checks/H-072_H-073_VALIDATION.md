# H-072 + H-073 validation report: the line in motion, and the pre-freeze audit's five slices, merged

**Date: 2026-11-02 | Status: built by workflow agents, each slice reviewed and its confirmed findings fixed; the merge reviewed again (no High/Medium); local gates below; screenshot baselines re-taken in CI and accepted by the owner (H-076); not yet deployed, no live call | Decisions: H-072, H-073, H-074, H-075 (and H-027, H-039, H-055 kept)**

Branch `h07x-int`: local `main` (H-073's decision commit 0280b3d and H-074's 0c69eaa) + `h073-int` (the five H-073 slices and their integration) + `h072` (the line in motion), merged in 285c169 and 333c527.

## 1. What changed

| Decision | What a person sees | Main places |
|---|---|---|
| H-072 | A run's sampled trace drives the line: boards hop between stations (300 ms slides), stations wear their state, queues show counts; nothing moves that the trace lacks, and the playback says it is sampled. The results page plays the baseline and the scenario on one clock; the model page plays its version's latest run in 2D and 3D. 3D equipment is built in code from primitives (electronics: SMT line, AOI, ICT and FCT testers by capacity, burn-in oven, pack; other packs generic). | `apps/web/features/playback/`, `features/twin-3d/` (equipment, kits, boards, engine), `features/model-editor/trace-*`, `docs/design/H-072_TRACE_ANIMATION.md`, DESIGN.md The Trace Motion Rule |
| H-075 | The line in motion is a card of the page's whole width under the comparison and its side panel, Baseline above Scenario (about 1,064 × 239 px each at 1280 px; it was 312 × 239 beside the side panel). | `features/results/results-view.tsx`, `results.module.css` |
| H-073 (1) | A finished comparison shows its saved Muse Spark explanation, in the reader's language when one exists (`lang` on another language's text), without asking the AI again. New read `GET /v1/simulations/{id}/analyses?baseline_run_id=` (listAnalyses: completed only, newest first, keyset cursor, tenant-scoped, owner/editor/viewer); items carry `output_locale`. | `schemas/http.schema.json` `Analyses`, `openapi/openapi.yaml`, `internal/analyses/list.go`, `internal/storage/analyses`, `features/results/saved-explanation.tsx` |
| H-073 (2) | The Scenarios page ranks each what-if by its latest comparison's p50 lead time, with the server's warnings, one click to its results. | `features/simulations/ranking.ts`, `scenario-list.tsx` |
| H-073 (3) | A line-constraint card from the server's rows (the largest work ratio, its rule stated, no cause claimed); the utilization label says it counts blocked and changeover time. | `features/results/constraint.ts`, `line-constraint.tsx` |
| H-073 (4) | Each run's reproducibility fingerprints and how many of the model's other completed runs with the same inputs gave the same result digest (capped reads that say what they left out); the explanation's evidence links to the rows it was checked against. | `features/results/proof.tsx`, `reruns.ts`, `evidence-link.tsx` |
| H-073 (5) | After a photo proposal is approved, a prefilled research request for the new stations' typical times; the person still sends it and approves its result. | `features/ai-proposals/station-research*.ts(x)` |
| H-073 quick fixes | README status, honest SAM copy, no signed zero, hours beside long minutes (under them in a narrow cell), a replaced connection read by name. | `README.md`, `features/results/format.ts`, `figure.tsx`, `features/ai-proposals/changes.ts` |

## 2. Reviews

| Round | Findings | Confirmed (High/Medium) | Fixed in |
|---|---|---|---|
| H-073 slices, one reviewer each, each finding verified | 11 | 3 (s2 warnings dropped from the ranking; s3 Escape on a hovered fingerprint; s3 the relabel's screenshots, recorded) | 20cc9e9, c8d7f77 |
| H-073 integration, three lenses, each finding verified | 5 | 3 (hours overflow at 390 px in the ranking and the Compare Table; fingerprint chip not hoverable) | 48877d7, 8833437 |
| H-072, three lenses, each finding verified | 13 | 7 (cut-short trace freezes; 2D rings without key or sampled caveat; stages 312 × 239; wheel and touch trapped; theme change destroyed shared meshes; quadratic batch cells) | b730ac7 |
| The merge (h07x-int), three lenses, two skeptics per finding | 5 | 0 (all Low) | — |

Found by the merged branch's own gates and fixed:
- `TestCataloguesHaveUILabels` (Go, electronics line pack) refused `domain.electronics_line.playback.sampled`: a pack's own playback words now live under `pack.<pack>.*` (1bdf846). H-072's agents had not run Go because they changed no Go file; the Go pack reads the web catalogs.
- motion.spec's strings check read the hours of a long time (H-073's Figure) apart from their figure (64aecf8).
- motion.spec's theme check: 3 of 13 runs failed in full suites. Measured on h072 and on the merged branch alike: about 12 frames a second on software WebGL, every scenario picture different from the last after the theme change, no page error, one stage screenshot 1.7-3.7 s. Its own 5 s poll now uses the project's 15 s, and it checks for page errors before it waits (ce15ad9).

## 3. Commands and results (h07x-int)

| Command | Result |
|---|---|
| `make frontend-test` (eslint, tsc, vitest), after 1bdf846 | exit 0; 168 files, 3252 tests |
| `PATH=$PWD/.venv/bin:$PATH make spec-check`, after 1bdf846 | exit 0, errors [] |
| `.venv/bin/python scripts/gen_ts_contracts.py --check` | PASS_TS_CONTRACTS_FRESH (17 files, 166 types) |
| `make backend-test` (gofmt, go vet, go test), after 1bdf846 | exit 0; 104 packages ok, 11 without tests, 0 FAIL (DB-backed tests skip without a DB URL) |
| DB-backed tests of the touched packages (`internal/analyses`, `internal/storage/analyses`, `internal/api`) with `-race` on a throwaway PostgreSQL 16, and `make db-test` (migrations 0001-0019, RLS) | exit 0 (run on h073-int, whose Go and SQL the merged branch carries unchanged) |
| `make e2e-test`, final (after ce15ad9) | exit 0. With Muse Wonder: 856 tests, 784 passed, 72 skipped (the screenshot comparisons), 27.4 min. Without Muse Wonder: 784 passed, 27.1 min. The two runs before it failed only motion.spec (the strings check, then the theme check), fixed in 64aecf8 and ce15ad9. |

## 4. Not run

- Screenshot comparisons (`E2E_SCREENSHOTS=1`): only in CI's pinned container, so not locally. CI run 41374165022 (update_screenshots, on tmp/h07x-screenshots at db3a28e; all six jobs green) re-took the 72: 64 the same, 8 changed (canvas at 1280 px, the landing at 390 px), 8 new (`ui-gallery-results-state-still-*`) in place of the 8 the spec no longer takes. The owner accepted them side by side (H-076); every committed baseline equals that run's artifact byte for byte.
- Screenshots of the build without Muse Wonder: no baselines (H-069).
- `make integration-test` and `make db-go-test` over every package: NOT RUN; only the touched packages ran against the throwaway DB.
- `make backend-race` over every package: NOT RUN; the touched packages ran with `-race`.
- `make ai-eval`, and any live AI call: NOT RUN (no prompt change).
- On the live site after a deploy: the five demo what-ifs' saved explanations (H-073 (1)), the 'Start here' project, and whether stations from an approved photo proposal get their kits in 3D (needs a run of the new version). NOT DONE.
- The touch behaviour of the stages on a real device; a screen reader on the slider's throttled value.
