# Spike 3: Mantine 9.6.3 (+ @mantine/notifications 9.6.3, TanStack Virtual 3.14.13) with React Flow 12.12.0

Report of the spike agent (2026-10-18), saved by the main session because the agent could not write files. Spike folder (scratch, not in the repo): `scratchpad/t045/spike-3-mantine/` with `shots/`, `results/`, `scripts/`. Specification: `spike-spec.md` in this folder.

**Summary.** The page runs; lint, typecheck, vitest and build exit 0 (170/170 tests: the existing 162 plus 8 new). axe: 0 violations in all 13 Playwright states and all 8 happy-dom states. Keyboard script: 29 of 32 steps pass; the 3 failures come from React Flow and Mantine's toast. zh-Hant render: the only non-catalog English is React Flow's attribution; Mantine leaked nothing because every label was passed. `/spike` First Load JS 296.3 kB gzip; Mantine adds about 29 kB gzip JS and 36 kB gzip CSS to every route. Sorting 200 rows takes about 175–230 ms, 5,000 rows without virtualization about 10 s. The table is the weak point.

Disk-full incident at about 19:42 UTC: an extra baseline `npm ci` filled the disk; deleted within a minute.

Setup: Next.js 16.4.1 (Turbopack), React 19.3.1, Node 22.22.0 (`--engine-strict=false` needed for installs). `/home/user/Geminus` not modified; servers on 3961/3962 stopped by PID.

## Versions

Every pinned version was published on or before 2026-10-04.

| Package | Version | Published | Licence | Install scripts | Role |
|---|---|---|---|---|---|
| @mantine/core | 9.6.3 | 2026-09-26 | MIT | none | components, theme, Table |
| @mantine/hooks | 9.6.3 | 2026-09-26 | MIT | none | peer of core |
| @mantine/notifications | 9.6.3 | 2026-09-26 | MIT | none | success toast |
| @tanstack/react-virtual | 3.14.13 | 2026-09-14 | MIT | none | virtualizes rows above 500 (Mantine has no grid) |
| @xyflow/react | 12.12.0 | 2026-09-24 | MIT | none | React Flow canvas |
| axe-core (dev) | 4.13.0 | 2026-08-05 | **MPL-2.0** | none | measurement 1 |
| @axe-core/playwright (dev) | 4.13.0 | 2026-08-11 | **MPL-2.0** | `prepare` only | measurement 1 |

- 49 new transitive packages, each checked for date and licence: MIT (Mantine store, floating-ui, react-number-format, react-remove-scroll, clsx, zustand, tabbable, @xyflow/system, @tanstack/virtual-core, …), type-fest (MIT OR CC0-1.0), react-transition-group (BSD-3-Clause), d3-* (ISC; d3-ease BSD-3-Clause). playwright-core 1.63.0 (Apache-2.0) came as an unused peer.
- No new package has a preinstall, install or postinstall script; nothing sends telemetry at install (H-008).
- One transitive package was too new: @babel/runtime 7.29.10 (2026-10-07) via react-transition-group; pinned to 7.29.7 with `overrides`.
- Pinning axe-core 4.13.0 also moves eslint-plugin-jsx-a11y's copy back from 4.14.0 (published 2026-10-05, after the cutoff).
- **MPL-2.0 (owner decision):** axe-core and @axe-core/playwright are weak file-level copyleft; dev-only, not shipped, and axe-core is already in the lockfile through eslint-plugin-jsx-a11y.
- Not added: @mantine/dates + dayjs (no date field), postcss-preset-mantine, @mantine/form.
- npm 10.9.4 drops the npm 11 lockfile's `libc` fields; restored.

## Commands

| Command | Exit | Notes |
|---|---|---|
| `npm ci --no-audit` | 1 | EBADENGINE (Node 22, repo wants ≥24.21) |
| `npm ci --no-audit --engine-strict=false` | 0 | |
| `npm install --save-exact --before=…` | 1 | `--before` also hides react 19.3.1 |
| installs + override + libc restore + `npm ci` | 0 | |
| `npm run lint` | 0 | text rule on |
| `npm run typecheck` | 0 | |
| `npx vitest run` | 0 | 170/170 (162 + 8 axe tests), 80 s |
| `npx next build` | 0 | Turbopack |
| experiment: `<html {...mantineHtmlProps}>` | 1 | 9 of 10 existing layout tests fail on `/^<html lang="en">/`; reverted |
| `next dev` console capture en / zh-Hant | 0 | no warnings or errors |
| Playwright scripts against `next start` (axe, keyboard, leaks, shots, bundle, perf, targets) + contrast | 0 | |

## 1. axe-core 4.13.0 (wcag2a, wcag2aa, wcag21a, wcag21aa, wcag22aa)

- Playwright: 0 violations in every state (default, form errors, 409, dialog open, empty table, error table, zh-Hant default; plus toast, Select open, 5,000 rows, zh-Hant errors/409/dialog).
- Incomplete (not verified): color-contrast on 748 nodes (cells scrolled out of the table's container, overlapping React Flow nodes, the attribution link; token math in #7 covers the colours); aria-prohibited-attr on React Flow's `.react-flow__controls` (`<div aria-label>` without a role); aria-valid-attr-value on Mantine's `aria-controls` with Select open.
- happy-dom vitest (`tests/a11y/spike-axe.test.tsx`): 0 violations in 8 states (needed a `document.title` and a 120 s timeout; no contrast without layout).

## 2. Keyboard-only: 29 pass, 3 fail of 32 steps

All 48 Tab stops show a visible 2 px outline. Form, Select, Checkbox, 409 + reload, React Flow entry/exit, dialog focus trap / Escape / focus return, and the self-built grid (one tab stop, arrows, Home/End, PageUp/Down, Ctrl+Home/End, sort with `aria-sort`, Space selects) pass.

Failures:
- React Flow blurs a node on Escape; focus falls to `<body>`.
- The Mantine notification (bottom right, 6 s) fully covered the focused grid cell — fails WCAG 2.4.11.
- Tab out of the grid landed on the sliding-out toast's Dismiss button (timing-dependent).

## 3. Screenshots

en / zh-Hant × 390 / 1280 plus state shots and no-JS / per-component-CSS variants. No horizontal page scroll at 390 px; the table scrolls inside its container; React Flow's `fitView` makes text unreadable at 390 px; 409, dialog and empty state wrap cleanly in zh-Hant.

## 4. Leaked English (zh-Hant)

- Not from the catalog: React Flow's attribution ("React Flow", aria-label "React Flow attribution", a `data-message`). Only `proOptions.hideAttribution` removes it, which React Flow asks to use only with Pro.
- Mantine: none, because every label was supplied. Its Modal and Notification close buttons have **no default name** — without catalog labels they would be unnamed. Unused components carry overridable English defaults (PasswordInput, Scroller, ActionBar, FloatingWindow).
- React Flow strings fed from the catalog: 11 `ariaLabelConfig` keys, edge `ariaLabel`, `aria-roledescription`.
- `noValidate` keeps the browser's English validation bubbles away.

## 5. Bundle and 5,000 rows

Measured from `.next` manifests and from Chromium's cold-load downloads (agree within 1 kB; gzip = Node zlib default).

| Route | First Load JS raw / gzip | CSS raw / gzip |
|---|---|---|
| `/spike` | 960.4 / **296.3 kB** (page chunk 121.2 kB gzip) | 255.4 / **38.9 kB** |
| `/` with Mantine in the root layout | 575.9 / 175.1 kB | 238.9 / 36.0 kB |
| `/` baseline without Mantine | 486.0 / 146.1 kB | 0 |
| per-component CSS variant | — | `/` 14.8 kB; `/spike` 17.7 kB gzip |

Sort timing (load average 7.4–8.4 on 4 cores, 3 runs):

| Variant | Rows in DOM | Sort commit (ms) | DOMContentLoaded |
|---|---|---|---|
| virtualized | 27 | 67.6 / 70.1 / 72.4 | 166–250 ms |
| not virtualized, 5,000 | 5000 | 9,609 / 10,120 / 10,808 | 4.4–4.9 s |
| 200 rows, not virtualized | 200 | 175.5 / 224.5 / 230.1 | 0.6–1.1 s |

Isolation (reverted): plain `<tr>/<td>/<input>` rows sort 2.5–3.5× faster (indicative only, lower load).

## 6. Wrapper code

`apps/web/ui/`: 637 lines (401 TS/TSX: `data-grid.tsx` 256, `theme.ts` 62, `confirm-dialog.tsx` 38, `number-field.tsx` 21, `provider.tsx` 17, `notify.ts` 7; 236 CSS). Page code 510 lines; test 125; catalogs +70 each; root layout gets CSS/provider imports, `ColorSchemeScript`, `suppressHydrationWarning`. Lint exceptions: 1 (`react-hooks/incompatible-library` on `useVirtualizer`).

## 7. Contrast and targets

All 27 token pairs pass after `--g-color-success` moved to #11702f (fg/bg 16.56; muted 6.39; accent 6.67; danger/warning/success on 10% tints 5.58/5.48/5.37; control border 4.55; focus ring 6.67 / 5.79 in a selected row). Compact density reaches 24 px for Mantine controls; under 24 px: the shell's Projects link and React Flow's attribution (spacing exception) and **4 React Flow connection handles at 6.3 px (fail)**. Density does not reach Mantine's portals unless set on `<html>`/`<body>`.

## 8. Screen reader: NOT RUN

A human with VoiceOver + Safari and NVDA + Firefox must check grid position (`aria-rowindex` while virtualized), sort and selection announcements, Select in zh-Hant, NumberInput (a textbox, not a spinbutton), dialog title + description, the `role="status"` toast, and React Flow's `role="application"`, roledescriptions, live messages and unnamed-role Controls.

## 9. Effort and friction

About 70 minutes, 110 tool calls. Root layout: `mantineHtmlProps` breaks the layout tests; `ColorSchemeScript` needs a CSP nonce later; without JS Mantine controls lose borders and fills. The theme holds functions, so it lives in a client provider; dot-subcomponents only in client files. Density overrides depend on Mantine's internal variable names. Inputs have no focus outline by default; Modal hard-wires `aria-describedby`; NumberInput uses react-number-format (locale separators passed by a wrapper) and silently clamps unless `clampBehavior="none"`; Badge uppercases; notifications default to `role="alert"`. Cascade layers (`@layer mantine, xyflow, app`) coexist with React Flow without conflicts.

## Owner decisions raised

MPL-2.0 for the dev-only axe tools; React Flow attribution; toast vs inline status; full vs per-component Mantine CSS; density on `<html>`; CSP nonce for `ColorSchemeScript`. Not evaluated: dark mode, @mantine/dates + dayjs.

## Verdict (the agent's judgement)

Strengths: quickest finished, consistent look driven by our tokens; good accessibility out of the box (0 axe violations in 21 checks; Modal/Select/Checkbox keyboard behaviour correct); no English leaks once labels are passed; MIT, no install scripts, clean coexistence with React Flow.

Weaknesses: no data grid (256 lines of our own grid + TanStack Virtual + a lint exception); slow rows (2.5–3.5× plain rows); the largest JS of the three (296 kB gzip, plus a per-route tax); intrusive root-layout setup; missing labels instead of English ones (a review checklist item).

Risks: one npm publisher account and a yearly major; density/focus overrides on internal names; dayjs as a second date library later.

Against the survey's decision rule Mantine does not beat a headless option on accessibility or size; pick it only if the owner values a finished look over the table and size costs.
