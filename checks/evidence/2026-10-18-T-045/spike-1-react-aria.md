# Spike 1: React Aria Components 1.21.1 + CSS Modules + CSS custom-property tokens

Report of the spike agent (2026-10-18), saved by the main session because the agent could not write files. Spike folder (scratch, not in the repo): `scratchpad/t045/spike-1-react-aria/` with `shots/` (29 files), `e2e/` scripts, `results/`, `logs/`, `packages-added.md`. Specification: `spike-spec.md` in this folder. `/home/user/Geminus` was not modified. Machine load average 3–8 during all timings.

Bottom line (agent): React Aria passed every accessibility check (0 axe violations, 66/66 keyboard steps). The costs are size (`/spike` adds 170.2 kB gzip over `/`) and a slow server render of large tables, worked around by rendering long tables after hydration.

## Versions

| Package | Version | Kind | Published | Licence | Install scripts | Note |
|---|---|---|---|---|---|---|
| react-aria-components | 1.21.1 | dep | 2026-09-04 | Apache-2.0 | none | newest eligible (1.22.1 is 2026-10-09) |
| @internationalized/date | 3.12.4 | dep | 2026-09-01 | Apache-2.0 | none | RAC dependency, not imported |
| @xyflow/react | 12.12.0 | dep | 2026-09-24 | MIT | none | |
| axe-core | 4.13.0 | dev | 2026-08-05 | **MPL-2.0** | none | measurement tool |
| @axe-core/playwright | 4.13.0 | dev | 2026-08-11 | **MPL-2.0** | none | measurement tool |
| playwright-core | 1.56.1 | dev | 2025-10-17 | Apache-2.0 | none | pinned to the global Playwright (otherwise 1.64.1, 2026-10-14) |

- Overrides set before installing: `@internationalized/number` 3.6.8 and `@react-types/shared` 3.36.1 (RAC's ranges resolved 3.6.9 / 3.37.0, published 2026-10-08).
- 32 new lockfile packages, all eligible and permissive (Apache-2.0, MIT, ISC, BSD-3-Clause; MPL-2.0 only for the two axe packages), none with install scripts. One existing entry changed: top-level axe-core 4.14.0 → 4.13.0.
- **MPL-2.0 (owner decision):** axe-core and @axe-core/playwright, unmodified, dev-only, not shipped.
- Measurement-only and not kept: `@react-aria/optimize-locales-plugin` 2.0.2 + `unplugin` 2.3.11 (`--no-save`).

## Commands

| Command | Exit | Note |
|---|---|---|
| `npm ci --no-audit` | 1 | `engine-strict=true`, Node 22.22.0 vs `engines` ≥24.21.0 |
| `npm ci --no-audit --engine-strict=false` | 0 | |
| `npm install` with overrides | 0 | |
| baseline lint / typecheck / vitest | 0 / 0 / 0 | 162/162 |
| final `npm run lint` | 0 | `--max-warnings=0`, text rules on |
| final `npm run typecheck` | 0 | |
| final `npx vitest run` | 0 | 12 files, **172/172** (162 + 3 locale + 7 axe) |
| final clean `npx next build` | 0 | Turbopack, 43.1 s wall |
| 2 Turbopack variant builds, 2 `next build --webpack` | 0 | measurement only, reverted |
| `node e2e/*.cjs` against `next start` | 0 each | |

## 1. axe-core 4.13.0 (wcag2a, wcag2aa, wcag21a, wcag21aa, wcag22aa)

**0 violations in every state**, Playwright (13 states: default, form errors, 409, dialog open, approved, empty, error, zh-Hant default, zh-Hant form errors, 5,000 rows, RAC toast, compact density, 390 px) and vitest + happy-dom (7 tests). Incomplete: color-contrast on table cells inside the scroll box (covered by #7), th-has-data-cells on the empty table (expected). Found and fixed in React Flow: the attribution link at 2.84:1 (fixed with the muted token); `<Controls>` puts `aria-label` on a role-less div (replaced with our own `role="group"` panel and buttons). The axe test needs a 180 s timeout and a document title.

## 2. Keyboard-only

**66/66 steps PASS** (en, 1280 px), 0 console messages; plus **7/7** on the 5,000-row virtualized table (Ctrl+Home, ArrowDown ×40, PageDown ×3, Ctrl+End, ArrowUp ×5, Space; `aria-rowcount` 5001 and `aria-rowindex` correct; 16–22 rows in the DOM). Covered: invalid submit with focus to the first invalid field and a status summary, select, number field, 409 with focus on the alert, reload and save, canvas (2 edges, 3 nodes, 3 controls, attribution), table navigation, sort (`aria-sort`), selection and select-all, alert dialog (focus on Cancel, wrap both ways, Escape returns focus, approve). Dialog wiring checked (`role="alertdialog"`, labelled and described by id).

Issues: React Flow's Escape `blur()` drops focus to `<body>` — fixed in `ui/flow-canvas.tsx`; React Flow edges come before nodes in tab order (not fixed); Shift+Tab into the grid lands on the last row (React Aria behaviour). NOT RUN: a zh-Hant keyboard run, Firefox and Safari.

## 3. Screenshots

29 files. No horizontal page scroll; no truncated labels in either language. At 390 px the table (min 720 px) scrolls in its box and the canvas controls touch a node. Fixed during the spike: `overflow-wrap: anywhere` breaking codes mid-word; a 7,340 px tall page from React Aria's absolutely positioned hidden inputs escaping a non-positioned scroll box. Virtualized rows have a fixed height (long messages would be cut).

## 4. English and other non-catalog strings (zh-Hant, 12 states)

Latin-script strings not from the catalog: **2, both React Flow's attribution** ("React Flow", "React Flow attribution"); only `proOptions.hideAttribution` hides them (MIT allows it; the vendor asks that only Pro subscribers do). React Aria leaks none.

React Flow English fixed: help texts via `ariaLabelConfig` (applied in an effect after mount, so the **server HTML had English** — fixed by rendering the canvas after hydration), edge names, roledescriptions, controls.

React Aria internal strings, which come out in zh-TW (the H-008 list for review):

| Key | zh-TW text | Where | Prop override? |
|---|---|---|---|
| table `sortable` | 可排序的欄 | hidden description on sortable headers | No |
| table `ascendingSort` / `descendingSort` | 已依據「列」欄遞增排序 | sorted column description and live announcement | No |
| grid `selectedItem` (and siblings) | 已選取「26」。 | live announcement on selection | No |
| numberfield `numberField` | 數字欄位 | `aria-roledescription` on the number input | Yes, but left: React Aria omits it on iOS so VoiceOver still announces "required" |
| overlays `dismiss` | 關閉 | hidden dismiss button in the Select popover | No |

Overridden with catalog text: 全選, 選取, step buttons 增加／減少 (React Aria's zh-TW is 放大), Select placeholder, toast region and close. Bug found by the list and fixed: the sort description read 已依據「」欄 because a `Column` with render-function children had no `textValue`. `lib/i18n/aria-locale.ts` maps zh-Hant → zh-TW and en → en-US in `UiProvider`; `tests/spike-locale.test.tsx` asserts Traditional 全選 with the mapping and Simplified 全选 without it.

## 5. Bundle and interaction

Read from `.next/diagnostics/route-bundle-stats.json`; gzip -9 and brotli; kB = 1,024 bytes.

| Route | Raw | Gzip | Brotli |
|---|---|---|---|
| `/` baseline | 476.9 kB | 143.8 kB | 122.8 kB |
| **`/spike`** | **1,031.6 kB** | **314.0 kB** | **262.9 kB** |
| `/spike` beyond `/` | 554.7 kB | 170.2 kB | 140.1 kB |

CSS: `/` 1.2 kB gzip; `/spike` 5.6 kB gzip (React Flow ≈15 kB raw of it). Beyond `/`: React Flow ≈ 55.5 kB gzip, `UNSTABLE_Toast` 5.0 kB, React Aria + wrappers + page ≈ 114.7 kB. Locale stripping (webpack comparison; the Turbopack-capable plugin 2.1.0 is eligible from 2026-10-22): 312.3 → 297.7 kB gzip, so other locales cost ≈ 14.6 kB gzip.

| Table | Rows in DOM | Sort median | Range |
|---|---|---|---|
| 5,000, virtualized | 21 | **257 ms** | 164–352 ms |
| 200, not virtualized | 201 | **825 ms** | 685–908 ms |

**React Aria problem: O(n²) server render of large collections in 1.21.1** (`TableCollection.commit()` → `getRows()` per insert): 1,000 rows 0.6 s, 2,000 2.0 s, 4,000 8.6 s; 5,000 rows took 1 min 51 s of server time. Fix in `ui/table.tsx`: the virtualized table renders after hydration in a same-size placeholder (server response then 0.02–0.03 s).

## 6. Wrapper code

`ui/`: 553 TS/TSX lines (470 non-blank) and 673 CSS lines (628; 207 tokens and base) for 11 components (provider, button, text and number fields, select, checkbox, form, table, dialog, status, callout, toast, canvas) plus icons. Page code 425 TS + 55 CSS; `aria-locale.ts` 13; 75 catalog keys per language; 154 test lines. **Lint exceptions: 0.** Icons are SVG (the text rule rejects "▲").

## 7. Contrast and targets

27 of 27 token pairs pass (body 16.56; muted 6.76/6.30; links and primary 6.09; error 6.57/5.91; warning 5.90; success 6.37; selected-row text 14.43; control borders 4.78/4.46; focus ring 5.24–6.09). 227 targets at compact density: only the existing "Projects" link and React Flow's attribution are under 24 px, both within the spacing exception.

## 8. Screen reader: NOT RUN

A human with VoiceOver + Safari and NVDA + Firefox must check: Chinese voice for zh-TW strings; form labels, required, hints, errors, status and the 409 alert; Select; table (sortable column, sort and selection announcements, "row 42 of 5001"); dialog title and description; canvas names, roledescriptions (步驟／連線), help and move announcements; the status region versus `?toast=rac`.

## 9. Effort and friction

About 1 h 40 min and 110 tool calls. React Aria imports `client-only`, so all use sits in `'use client'` wrappers; pages and layout stay Server Components; no hydration warnings. Friction: the O(n²) server render; hidden inputs escaping non-positioned scroll boxes; `Column` needs `textValue`; NumberField clamps on blur (min/max left to the server's `OUT_OF_RANGE`); step-button labels get the field label appended; `validationBehavior="aria"` needed to avoid browser-language validation text; Form `validationErrors` takes `ValidationIssue`s directly when fields are named by JSON Pointer. CSS layering works in Turbopack (`@layer base,xyflow`, CSS Modules unlayered). Tooling: `next build` rewrites `tsconfig.json` when the build directory changes; **`next dev` 16.4.1 writes an `AGENTS.md` into `apps/web`** — set `agentRules: false`.

Toast decision: our own `role="status"` region for P0 (Toast is `UNSTABLE_`; React Aria renders toasts as focusable assertive alert dialogs in a region that appears with them; our region persists, no timing issue under WCAG 2.2.1; −5 kB). Revisit when Toast is stable.

## What a human must still check

Screen readers; React Aria's zh-TW wording and the H-008 exception (and whether to override 數字欄位); narrow-screen table design; canvas controls at 390 px and the attribution; focus rings in Firefox and Safari; the 314 kB gzip First Load JS or locale stripping; MPL-2.0 for the dev tools; table-cell contrast by eye; touch targets on a real device.

## Verdict (the agent's judgement)

Strengths: accessibility out of the box (0 axe violations, 66/66 + 7/7 keyboard, a full virtualized table keyboard model, correct dialog focus; every problem found was in the wrappers or React Flow); one library covers form, virtualized table, select, checkbox, dialog and toast; trivial, tested zh-TW mapping with only 5 kinds of library strings left, all Traditional; clean Server Components; no lint exceptions.

Weaknesses: we write all the CSS (628 lines) and need React Aria knowledge (`data-*` states, focus on the host, positioned scroll boxes, `textValue`); 170 kB gzip over `/` (≈115 kB React Aria + ours, ≈15 kB unused locales until stripping); unvirtualized tables re-sort slowly (200 rows ≈ 0.8 s).

Risks: the server render of large collections (mitigated client-only; 1.22 not checked); `UNSTABLE_Toast` avoided; a large API surface for reviewers.

If chosen: virtualize tables above about 50 rows; keep long collections client-side; check every `Column` has a `textValue`; keep the zh-TW string list as a Playwright test; set `agentRules: false`.

Against the survey's decision rule: no axe or keyboard failure is attributable to React Aria; the size test compares 314.0 kB gzip with the other spikes.
