# T-045 spike specification (shared by the three spikes)

Source of truth for the task: `planning/tasks.json` T-045, `docs/PRD.md` REQ-022/REQ-023, `docs/TEST_PLAN.md` TEST-033, `docs/adr/ADR-020-ui-localization.md`, `docs/adr/ADR-016-frontend-server-state-boundary.md`, `checks/evidence/2026-10-18-T-045/survey.md` §6, and the owner's H-007/H-008 decisions in `docs/DECISIONS_AND_TBD.md`.

Every spike builds the SAME page with a different stack, measures the SAME 9 things, and reports in the SAME format. The point is a fair comparison for the owner, not a finished product.

## Ground rules

- Work ONLY in your own directory `SPIKE_DIR` (given in your prompt). Never modify `/home/user/Geminus`, never commit or push, never touch git there. Copy the app with:
  `mkdir -p $SPIKE_DIR && rsync -a --exclude node_modules --exclude .next --exclude tsconfig.tsbuildinfo /home/user/Geminus/apps/web/ $SPIKE_DIR/web/`
  then `cd $SPIKE_DIR/web && npm ci --no-audit` (the lockfile is the repo's), then add your stack's packages.
- Pin exact versions (`npm install --save-exact pkg@x.y.z`). No `latest`, no ranges. Every version you add must have been published on or before 2026-10-04 (two weeks before today, 2026-10-18): check with `npm view pkg time --json` and record the publish date of the exact version. If a pinned version in the survey is too new, use the newest eligible one and say so.
- Licences: record each added package's licence (`npm view pkg@ver license`). Copyleft or unclear licences → stop and report; do not use.
- H-008: no packages that send telemetry at install (check `scripts.postinstall`/`install`/`preinstall` of each added package and of new transitive packages: `npm query ':attr(scripts, [postinstall])'` or inspect `node_modules/*/package.json`); no commercial licences; system fonts only (no web fonts); light theme only, tokens leave room for dark.
- No network calls other than the npm registry. No secrets. No live AI.
- Node in this container is 22 (the repo's `engines` says 24; ignore engine warnings, note them).
- Playwright: the global install `PW=$(npm root -g)/playwright` (1.56.1) with Chromium at `/opt/pw-browsers` (PLAYWRIGHT_BROWSERS_PATH is set). Do NOT run `playwright install`. If you need `@axe-core/playwright`, install it in your spike copy; require playwright from the global path (`require(process.env.PW)`), or use `executablePath: '/opt/pw-browsers/chromium'` if versions clash.
- When you start `next start`, use a free port in the range given in your prompt and stop the server by its PID when done (do not use pkill patterns that could match other shells).
- All user-visible text comes from the catalogs (`messages/en.ts`, `messages/zh-Hant.ts`): add a `spike` section to both (zh-Hant type follows en). The repo's lint rule against hard-coded text must stay on and pass (`npm run lint`); every lint exception you need is a measurement (#6), list each.
- Library-internal strings you cannot override by a prop (H-008 item 1) are allowed but must be listed (measurement #4). React Aria: map the app's `zh-Hant` to `zh-TW` at the provider (React Aria gives Simplified for `zh-Hant`).

## The page: route `/spike` (no auth, fake data)

Use the existing app shell (`app/layout.tsx`: header, nav, LocaleMenu) and add `app/spike/page.tsx` (+ client components). The page has:

1. **Form** (left column on ≥1024 px, stacked below on narrow):
   - text field "Model name" (required), select "Queue discipline" (FIFO / LIFO / priority), number field "Capacity" (locale-formatted, min 1, max 1000), checkbox "Use calendar".
   - Submit validates and shows inline messages from a `ValidationIssue`-shaped object `{ code, path, message }` where `path` is a JSON Pointer (`/name`, `/resources/0/capacity`); the message text comes from the catalog by `code` (use codes `REQUIRED`, `OUT_OF_RANGE`).
   - A second submit path ("Save" when the fake server says the revision is stale) shows the 409 conflict state with ADR-016 copy from the catalog (reuse the existing catalog text for MODEL_VERSION_CONFLICT if present) and a "Reload latest" action.
2. **Table** of import row errors (fields: row number, column, code, message, severity):
   - 200 rows by default and a 5,000-row variant (`/spike?rows=5000`), deterministic fake data generated in code.
   - Sortable columns, row selection (checkbox per row + select all), sticky header, keyboard navigation (arrow keys move between cells or rows per the library's grid pattern).
   - Empty state (`/spike?rows=0`) and error state (`/spike?state=error`), text from the catalog.
3. **Dialog**: button "Approve proposal…" opens a confirm dialog with title + description wired (`aria-labelledby`/`aria-describedby`), focus trapped, Escape closes, focus returns to the trigger; "Approve" closes it and shows a success toast or `role="status"` message.
4. **React Flow canvas**: a small `@xyflow/react` 12.12.0 (or newest eligible 12.x) canvas (3 nodes, 2 edges) beside the form, to check focus order, Escape handling and CSS conflicts (cascade layers / globals). PlayCanvas is not needed.

Design tokens: CSS custom properties for color (neutral, accent, danger, warning, success, focus), font stack (system fonts with Traditional Chinese fallbacks: e.g. `system-ui, -apple-system, "Segoe UI", Roboto, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", sans-serif`), spacing scale, radius, density (compact control height ≥ 24 px target size; default 32 px), focus ring (2 px, contrast ≥ 3:1). Feed the same tokens to React Flow's `--xy-*` variables. Keep tokens in one file so the three spikes can be compared.

## The 9 measurements (record exact commands and outputs; anything not run is NOT RUN)

1. **axe-core** (4.13.0 or newest eligible 4.x): vitest DOM (happy-dom; axe may need jsdom — if happy-dom fails, say so and use Playwright only) and `@axe-core/playwright` with tags `wcag2a, wcag2aa, wcag21a, wcag21aa, wcag22aa`: violation count per state (default, form errors, 409, dialog open, empty table, error table, zh-Hant default). List each violation id and node count.
2. **Keyboard-only Playwright script** of the whole flow (Tab into form, fill, submit with errors, fix, submit; move into table, sort with keyboard, select rows, navigate cells; open dialog with keyboard, Escape, reopen, approve; tab into and out of the React Flow canvas). At every step assert that `document.activeElement` is visible and has a visible focus indicator (computed outline/box-shadow not none). Report pass/fail per step.
3. **Screenshots** en / zh-Hant × 390 px / 1280 px wide (full page), saved as PNG in `SPIKE_DIR/shots/`. Look at them yourself (Read the PNG) and note wrapping, overflow, truncation, horizontal scroll at 390 px.
4. **Leaked English** on the zh-Hant render: list every Latin-script text node and accessible name (aria-label, title, alt, placeholder, live-region text, visually-hidden text) that does not come from our catalog — use Playwright to collect them; include library-internal strings (e.g. "Select all", "Sort ascending", "Close", "Press Delete to remove"). For each say whether a prop can override it.
5. **Bundle**: `next build` (Turbopack, the default) — First Load JS for `/spike` and for `/` (baseline), and total CSS bytes; give the numbers from the build output and from `.next` (gzipped size of the route's JS chunks if the build output does not show it; say how you measured). Also the 5,000-row variant's interaction time: time to sort once (Playwright `performance.now()` around the click until the first row changes) and whether rows are virtualized.
6. **Wrapper code**: lines of code you wrote in `apps/web/ui/` (wrappers + CSS) vs page code; number of lint exceptions (eslint-disable) needed and why.
7. **Contrast** of each token foreground/background pair (compute WCAG ratios in a script) and target sizes (measure bounding boxes of all interactive elements at compact density; list any < 24×24 px).
8. **Screen reader smoke test**: NOT RUN (needs a human with VoiceOver/NVDA). Say what a human should check.
9. **Effort and friction**: approximate time/turns, notable problems (RSC boundaries, `'use client'` needs, hydration warnings, console errors, SSR of the library, CSS ordering with React Flow, i18n mapping).

Also always run in your copy: `npm run lint`, `npm run typecheck`, `npx vitest run` (the existing 162 tests must still pass), `npx next build`. Report exit codes.

## Report format (your final message, and the same text in `SPIKE_DIR/REPORT.md`)

```
# Spike <N>: <stack>
Versions (exact, publish date, licence, install scripts): table
Commands run with exit codes: table
Measurements 1–9: one subsection each, numbers first
Screenshots: paths
What a human must still check
Verdict for the comparison (your judgement, marked as judgement): strengths, weaknesses, risks
```
