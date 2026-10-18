# Spike 2: Base UI 1.8.0 + Tailwind CSS 4.3.3 + TanStack Table 9.2.4 + TanStack Virtual 3.14.13

Report of the spike agent (2026-10-18), saved by the main session because the agent could not write files. Spike folder (scratch, not in the repo): `scratchpad/t045/spike-2-base-ui-tailwind/` with `shots/`, `logs/`, `scripts/` and the raw JSON results. Specification: `spike-spec.md` in this folder. `/home/user/Geminus` was not modified.

Built: route `/spike` (Server Component page with four client islands); `'use client'` wrappers in `apps/web/ui/`; tokens in `ui/tokens.css`, read by Tailwind's `@theme inline` and by React Flow's `--xy-*`; 79 `spike.*` catalog keys per locale; `tests/a11y/spike-axe.test.tsx`. kB = 1,024 bytes.

## Versions

All survey pins were eligible (published on or before 2026-10-04): @base-ui/react 1.9.0 (2026-10-09) and axe 4.14.0 are too new; @tanstack/react-table 9.2.6 was eligible but 9.2.4 was kept.

| Package | Version | Published | Licence | pre/install/postinstall | Kind |
|---|---|---|---|---|---|
| @base-ui/react | 1.8.0 | 2026-09-04 | MIT | none | dependency |
| @tanstack/react-table | 9.2.4 | 2026-08-28 | MIT | none | dependency |
| @tanstack/react-virtual | 3.14.13 | 2026-09-14 | MIT | none | dependency |
| @xyflow/react | 12.12.0 | 2026-09-24 | MIT | none | dependency |
| tailwindcss | 4.3.3 | 2026-07-16 | MIT | none | dev |
| @tailwindcss/postcss | 4.3.3 | 2026-07-16 | MIT | none | dev |
| axe-core | 4.13.0 | 2026-08-05 | **MPL-2.0** | none | dev (test only) |
| @axe-core/playwright | 4.13.0 | 2026-08-11 | **MPL-2.0** | `prepare` only | dev (test only) |
| playwright-core | 1.56.1 | 2025-10-17 | Apache-2.0 | none | dev, pinned to the global Playwright |
| @babel/runtime (override) | 7.29.7 | 2026-05-25 | MIT | none | Base UI's range resolved 7.29.10 (2026-10-07) |

- 72 lock entries added or changed (53 installed, 19 optional platform binaries): MIT 47, MPL-2.0 14, ISC 9, BSD-3-Clause 1, Apache-2.0 1. No new pre/install/postinstall script (the only one in the tree is the existing `unrs-resolver`). No telemetry at install; `NEXT_TELEMETRY_DISABLED=1` for Next.js commands.
- **MPL-2.0 (owner decision):** axe-core and @axe-core/playwright (dev, measurement tools) and **lightningcss with 12 platform binaries, a build-time dependency of `@tailwindcss/node` that Tailwind 4 cannot avoid**. None reaches the browser bundle; Next.js's SWC binary already contains lightningcss.
- The repo's own `next` 16.4.1 and `react` 19.3.1 were published 2026-10-14 (existing pins, not additions); `npm install --before` therefore cannot resolve peers.

## Commands

| Command | Exit | Result |
|---|---|---|
| `npm ci --no-audit` | 1 | EBADENGINE (Node 22.22.0; engines ≥24.21; `engine-strict=true`) |
| `npm ci --no-audit --engine-strict=false` | 0 | 371 packages |
| `npm install --save-exact --before=…` (runtime) | 1 | ERESOLVE (react 19.3.1 hidden) |
| `npm install --save-exact` runtime / dev / override | 0 | |
| `npm run lint` | 0 | 0 problems |
| `npm run typecheck` | 0 | |
| `npx vitest run` | 0 | 169/169 (162 existing + 7 axe tests) |
| `npx next build` (Turbopack) | 0 | compiled in 22.1 s |
| scripts: axe, keyboard (200 and 5,000 rows), shots, states, leaks, targets, contrast, perf, bundle, attribution | 0 | |

## 1. axe-core 4.13.0

| State | Playwright (production build) | vitest + happy-dom |
|---|---|---|
| default, form errors, 409, empty, error, zh-Hant default | **0** | 0 |
| dialog open | **0** | **1** `aria-hidden-focus` (7 nodes) |
| extra: toast, select open, all selected, 5,000 rows, compact, zh-Hant dialog | **0 each** | not run |

Base UI hides content outside a modal with `aria-hidden` plus focus guards, not `inert`; Chrome's axe reports those nodes as incomplete. Incomplete in the browser: color-contrast (cells under the sticky header or scrolled away), aria-prohibited-attr (React Flow `<Controls>`), aria-hidden-focus (Base UI focus guards; the toast close button is `aria-hidden` until expanded or focused), aria-valid-attr-value (select's `aria-controls` into a portal).

## 2. Keyboard-only: 53/53 steps with 200 rows, 53/53 with 5,000 (virtualized)

Every stop is in the viewport, uncovered at its centre (`elementFromPoint`) and has a visible 2 px outline. Form (focus to the first invalid field, `aria-describedby`, `role=alert` summary), select, number field, checkbox, 409 + reload, canvas, grid (sort with `aria-sort`, Space selects, PageDown, Ctrl+End to row 5,001 of 5,001, one tab stop, Shift+Tab returns to the last cell), alert dialog (trap, Escape, focus return) and toast all pass.

Findings: React Flow puts edges before nodes in the Tab order; React Flow 12.12.0 blurs a node on Escape (focus to `<body>`) — worked around in `ui/flow-canvas.tsx`; an SVG edge's focus is a thicker stroke plus Chrome's outline.

## 3. Screenshots

390 px en/zh-Hant: no page-level horizontal scroll (`scrollWidth` 390); the grid (min 52 rem) scrolls inside its box; the canvas's `fitView` makes labels about 7 px. 1280 px: nothing overflows. zh-Hant: no truncation; the 「（必填）」 marker leaves a slightly wide gap; CJK headings look lighter in the system font.

## 4. English left over (zh-Hant, 8 states)

Only React Flow's attribution ("React Flow", aria-label "React Flow attribution"); removable only with `proOptions.hideAttribution` (MIT allows it; xyflow asks non-subscribers to keep it) — owner decision. Library English overridden with catalog text: Base UI NumberField `aria-roledescription="Number field"`, Increment/Decrement labels, Toast.Viewport "Notifications"; React Flow's 11 `ariaLabelConfig` strings (its keys are named the wrong way round: `default` is used when keyboard a11y is disabled), node/edge roledescriptions, edge names; browser bubbles avoided with `noValidate`. TanStack emits no strings. Remaining Latin text is catalog text (FIFO, LIFO, UI, Enter, Esc, English) and 11 data strings marked `lang="en"`.

## 5. Bundle and 5,000 rows

Next.js 16.4.1 no longer prints First Load JS; measured from manifests and from Chromium's downloads (gzip `zlib -9`).

| | JS raw | JS gzip | CSS raw | CSS gzip |
|---|---|---|---|---|
| `/` baseline (manifest / browser) | 475.8 / 477.3 kB | 142.8 / 143.6 kB | 33.7 kB | 6.8 kB |
| **`/spike`** (manifest / browser) | **912.3 / 913.8 kB** | **286.2 / 287.0 kB** | 33.7 kB | 6.8 kB |
| page chunk only | 436.5 kB | 143.4 kB | — | — |

One shared CSS file, 6.8 kB gzip, about half React Flow's `style.css`. Page chunk by package (≈ gzip pro rata): Base UI ≈ 57 kB (with floating-ui), React Flow ≈ 55 kB (with d3), TanStack ≈ 21 kB, our code ≈ 8 kB.

| Variant | Sort, median | min–max | Select all |
|---|---|---|---|
| 5,000 rows, virtualized (26 `<tr>`, `aria-rowcount` 5001) | **46.9 ms** | 23.1–105.7 ms | 33.7 ms |
| 200 rows, not virtualized | 86.8 ms | 71.4–115.7 ms | 127.2 ms |

(Load average 6–8; rough.)

## 6. Wrapper code

`ui/` TSX 686 non-blank lines in 8 files (data-grid 285, field 177, flow-canvas 63, dialog 43, toast 39, checkbox 34, notice 25, button 20); CSS 174; `app/globals.css` 61; page code 435; test 114; 79 catalog keys per locale. About 110 lines of `data-grid.tsx` are keyboard, focus and ARIA work (roving tabindex, key map, focus restore with windowing, ARIA attributes, sticky-header scroll padding), about 25 virtualization glue. It covers the basic APG grid only: no range selection, type-ahead, cell editing, column resizing or screen-reader verification. Lint exceptions: 1 (`react-hooks/incompatible-library` on `useVirtualizer`). The repo's text rule flagged an object key `message: "auto"` (the known false positive, ADR-020) — avoided by leaving the key out.

## 7. Contrast and targets

22 text pairs ≥ 4.5:1 (lowest: danger on accent-subtle 5.73), 10 non-text pairs ≥ 3:1 (control border 3.44–3.72; focus ring 5.65–6.48); the 1.43:1 separator is decorative only. At compact density, 1,236 interactive elements measured; 5 under 24×24, all React Flow (4 handles 7.9 px, the attribution link). Our smallest targets are exactly 24 px. Density on the page wrapper does not reach Base UI portals; it belongs on `<html>`.

## 8. Screen reader: NOT RUN

A human with NVDA + Firefox and VoiceOver + Safari must check the grid (name, position while windowed via `aria-rowindex`, sort, selection, select-all mixed state), form errors and the 409 alert announced once, "required" from `aria-required`, the 「數字欄位」 roledescription, listbox navigation, the toast (polite; reachable by Tab or F6), the alert dialog, and the canvas's `role=application` and live region.

## 9. Effort and friction

About 75 minutes, 120 tool calls; the disk filled once and crashed Chromium (retry worked). Page and layout stay Server Components; no hydration warnings or console errors. Base UI `Form` runs native constraint validation and silently cancels submit when NumberField's hidden input fails, blocking the ValidationIssue flow — use a native `<form noValidate>` with Base UI `Field`. Base UI ships its docs in `node_modules`. Tailwind 4: `@theme` namespace resets make tokens the only palette (9 arbitrary values remain, all layout sizes); `outline-none` swallowed a later `outline-2` (only the focus assertion caught it); misspelled classes generate nothing silently; no `tailwind-merge`. TanStack Table v9 is a new API (most examples, shadcn included, are v8); numeric columns sort descending first unless `sortDescFirst: false`. React Flow's CSS has no layer (imported into `@layer xyflow`). `next dev` 16.4.1 writes `apps/web/AGENTS.md` on start unless `agentRules: false` — matters because the repo has its own AGENTS.md.

## Verdict (the agent's judgement)

Strengths: clean measurements (0 axe violations in 13 browser states, 53/53 keyboard steps at 200 and 5,000 rows, all contrast pairs, no console errors); Base UI covers this page with only 4 overridable internal English strings and no dictionary; Tailwind `@theme` resets enforce the tokens (6.8 kB gzip CSS); TanStack is headless, cheap (≈ 21 kB gzip) and fast virtualized.

Weaknesses: the grid's accessibility is entirely ours (≈ 110 lines for the basic APG subset, no screen-reader proof; every later grid feature adds more — the main argument for React Aria's Table); Base UI is the largest part of the page chunk (≈ 57 kB gzip for 7 components; route 287 kB gzip vs 143.6 baseline); utility classes have no type safety; Base UI `Form` fights a server-shaped validation flow; density must live on `<html>`.

Risks: TanStack Table v9 is young; Base UI releases monthly; MPL-2.0 tools (and lightningcss) need owner sign-off; the self-built grid is untested with screen readers. Choose it if the owner values the Tailwind/shadcn ecosystem and token enforcement over getting the grid's accessibility from the library.
