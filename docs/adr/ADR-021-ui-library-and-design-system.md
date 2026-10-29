# ADR-021 — UI library and design system (T-045)

Status: ACCEPTED (proposed by the T-045 agent from a survey and three spikes; on 2026-10-18 the owner adopted all the recommendations, H-009). Date: 2026-10-18. Basis: REQ-022, REQ-023, TEST-033, H-007, H-008, ADR-016, ADR-020; `survey.md`, `spike-spec.md`, `spike-1-react-aria.md`, `spike-2-base-ui-tailwind.md`, `spike-3-mantine.md` in `checks/evidence/2026-10-18-T-045/`.

## Context

H-007 requires a comparison of several UI libraries rather than adopting Tailwind + shadcn/ui directly. The survey compared headless primitives, styled component libraries, enterprise design systems and styling approaches, and picked three sets for spikes. All three built the same content on the same page `/spike`: a form (ValidationIssue errors, a 409 conflict), a 200/5,000-row error table (sorting, selection, keyboard, empty and error states), a confirmation dialog and a success message, and a small React Flow canvas; they measured the same 9 items (`spike-spec.md`). All three follow H-008: system fonts only, a light theme, no paid licence, no install-time scripts or telemetry, and every version released before 2026-10-04.

## Comparison (measured with a machine load of 3–14; times can only be compared relatively)

| Item | 1. React Aria Components 1.21.1 + CSS Modules | 2. Base UI 1.8.0 + Tailwind 4.3.3 + TanStack Table 9.2.4/Virtual 3.14.13 | 3. Mantine 9.6.3 |
|---|---|---|---|
| axe (WCAG 2.0–2.2 A/AA), 13 states in the browser | **0 violations** | **0 violations** (1 `aria-hidden-focus` on the dialog in happy-dom; incomplete in Chrome) | **0 violations** |
| Keyboard-only flow | **66/66** + 7/7 with 5,000 rows | **53/53** (200 and 5,000 rows) | 29/32 (React Flow Escape loses focus; a notification hides the focus, WCAG 2.4.11; Tab lands on a notification that is sliding out) |
| Table accessibility | **Built into the library**: full grid keyboard model, sorting, selection, virtualization | **Hand-written**: about 110 lines, covering only the basic APG grid (no range selection, type-ahead, editing, column widths) | **Hand-written**: 256 lines, plus TanStack Virtual |
| `/spike` First Load JS (gzip, KiB) | 314.0 | **287.0** | ≈289.4 (plus ≈28 KiB more JS per route) |
| JS beyond `/` (gzip) | 170.2 KiB (React Flow ≈55.5, the rest React Aria + our code; another ≈14.6 KiB of unused locales could be removed) | ≈143.4 KiB (Base UI ≈57, React Flow ≈55, TanStack ≈21) | ≈146.7 KiB |
| `/spike` CSS (gzip) | **5.6 KiB** | 6.8 KiB | ≈38 KiB (≈17 when each component is imported separately) |
| Median time to sort 5,000 rows (virtualized) | 257 ms | **46.9 ms** | ≈70 ms |
| Sorting 200 rows (not virtualized) | 825 ms | **86.8 ms** | 175–230 ms |
| Code we wrote (`ui/`) | 553 lines TS + 673 lines CSS | 686 lines TSX + 174 lines CSS (+ 61 lines globals) | 401 lines TS + 236 lines CSS |
| Lint exceptions | **0** | 1 (`useVirtualizer`) | 1 (`useVirtualizer`) |
| Library strings that cannot be overridden by a prop (zh-Hant) | 5 React Aria Traditional Chinese strings (see below; need an H-008 exception list) | 0 | 0 (but the close button has no name by default; forget to pass one and it has none) |
| Effect on the root layout | One client provider (zh-Hant→zh-TW mapping, tested) | None | `ColorSchemeScript` inline script (a later CSP needs a nonce), controls lose their styles without JS, `suppressHydrationWarning` |
| Licence | Apache-2.0 | MIT; Tailwind 4 needs lightningcss at build time (MPL-2.0, not in the bundle) | MIT |
| Main risks | 1.21.1 builds large collections on the server in O(n²) (1 min 51 s for 5,000 rows; long tables now render only after hydration); `UNSTABLE_Toast` not used | TanStack Table v9 is very new (most examples are still v8); Base UI releases monthly; grid accessibility is maintained entirely by us and not verified with a screen reader | A single maintainer, one major release per year; our density/focus overrides depend on internal variable names; a later date component would need dayjs |

Findings common to all three (independent of the choice):

- React Flow 12.12.0: Escape makes the node lose focus (all three fixed it in a wrapper); edges come before nodes in the Tab order; the `aria-label` of `<Controls>` has no role; connection handles are 6–8 px (below 24 px); the help text is English before hydration; **the attribution link "React Flow" is the only string that cannot be translated** (it can only be hidden with `proOptions.hideAttribution`, which MIT allows, but the vendor asks those without a Pro subscription to keep it).
- At 390 px the table scrolls horizontally inside its own box and the canvas text is too small: a narrow-screen design for tables and the canvas is needed (T-015).
- Density set on a page wrapper does not reach portals (dialogs, dropdowns, notifications); it has to be set on `<html>`.
- axe-core and @axe-core/playwright are MPL-2.0 (weak copyleft, used only in development and tests, not shipped with the product); TEST-033's automated checks need them.
- `next dev` of Next.js 16.4.1 writes an `AGENTS.md` into `apps/web` unless `agentRules: false` is set: it would be confused with the repository's AGENTS.md, so it must be set whichever set is chosen.
- Screen readers (VoiceOver + Safari, NVDA + Firefox): NOT RUN for all three; a person needs to run them.

## Recommendation (the agent's judgement)

**Choose 1: React Aria Components 1.21.1 + CSS Modules + CSS custom-property tokens.**

- The rule the survey set in advance: "choose 1 unless 1 fails axe/keyboard where the others do not, or its route JS exceeds the best by about 1.5 times without an accessibility benefit". 1 has no axe or keyboard failure attributable to React Aria; its JS is 1.09 times the best (314.0/287.0).
- P0's screens are mostly data-dense components (the import error table, proposal review, run comparison, the editor's property panel), and table and grid accessibility is exactly where mistakes are easiest. With 1, the library provides the full grid keyboard model and virtualization; with 2 and 3 we would have to write and maintain them ourselves for the long term, without screen-reader verification.
- The costs of 1 and the countermeasures:
  - all the CSS is ours to write (our tokens need a custom look anyway);
  - sorting a non-virtualized table is slow → virtualize every table over about 50 rows;
  - server rendering of large collections is slow → long tables render only on the client;
  - unused locales ≈14.6 KiB → add `@react-aria/optimize-locales-plugin` 2.1.0 (meets the two-week rule from 2026-10-22) once Turbopack support is confirmed;
  - 5 library Traditional Chinese strings → list them under H-008, review them, and list them in a Playwright test.
- If the owner values the Tailwind/shadcn ecosystem and token enforcement more and is willing to maintain grid accessibility, choose 2; if the owner values "a complete look right away" more and accepts the larger CSS and the root layout's inline script, choose 3.

## The owner's decision (H-009)

1. **Choose 1: React Aria Components 1.21.1 + CSS Modules + CSS custom-property tokens.**
2. The following React Aria Traditional Chinese strings use the library's translation under H-008 (listed, reviewed, and listed in a Playwright test): 可排序的欄 (sortable column), 已依據「…」欄遞增／遞減排序 (sorted by the "…" column in ascending/descending order), 已選取「…」。 (selected "…"), 關閉 (close; the hidden close button of Select), 數字欄位 (number field; not overridden, keeping React Aria's handling on iOS).
3. axe-core and @axe-core/playwright (MPL-2.0) are only test and development tools and are not shipped with the product.
4. Keep the React Flow attribution (do not set `proOptions.hideAttribution`).
5. Success feedback uses an in-page `role="status"` message, not a toast.

## Implementation after the choice (the rest of the T-045 scope)

Design tokens (colours, system fonts with a Traditional Chinese fallback, spacing, density, focus ring; dark reserved), the app shell and basic components (button, field, select, dialog, status, table, and the loading/empty/error/insufficient-permission/409 states) applied to the screens of T-014/T-044; `agentRules: false`; axe in vitest and Playwright, keyboard-only E2E, en/zh-Hant × 390/1280 screenshot comparison (a baseline change needs human confirmation); low-fi prototypes of T-015 and T-031 handed to the owner for review. The spike code stays only in scratch and is not moved into the repository directly; it is rewritten for the chosen set, tests first.

## Implementation record (T-045)

- Installed in `apps/web` on 2026-10-19 (`package-lock.json`):
  - `react-aria-components` 1.21.1 (Apache-2.0, 2026-09-04) is the only new production dependency, together with `react-aria` 3.52.1, `react-stately` 3.50.0, `@internationalized/*`, `aria-hidden`, `clsx` and `use-sync-external-store`: 10 lock entries in all, none with an install script.
  - Overrides: `@internationalized/number` 3.6.8, `@react-types/shared` 3.36.1. React Aria's ranges would resolve to 3.6.9/3.37.0, released on 2026-10-08, less than two weeks old.
  - `axe-core` 4.13.0 (MPL-2.0, dev): only for vitest's accessibility checks; the existing 4.14.0 (brought in by eslint-plugin-jsx-a11y) is lowered to 4.13.0 as well.
  - `npm audit --omit=dev`: 0 vulnerabilities.
- The browser E2E (`@playwright/test` 1.56.1, the same version as the machine's Chromium; `@axe-core/playwright` 4.13.0) lives in a separate npm project, not in `apps/web`. There are two reasons: Next.js 16.4.1 declares an optional peer dependency on `@playwright/test`, so the lockfile marks it as non-dev and `npm ci --omit=dev` would install it into production; and `@axe-core/playwright` would also bring in playwright-core 1.64.1, less than two weeks old.
- `next.config.ts` sets `agentRules: false`.
- Tokens are in `apps/web/ui/tokens.css`, element defaults in `ui/base.css` (`@layer base`). `tests/tokens.test.ts` checks the contrast of 17 text pairs and 6 non-text pairs, that only semantic tokens and only system fonts are used, and that compact density is still 24 px. `UiProvider` (`ui/provider.tsx`) maps `zh-Hant` to React Aria's `zh-TW` (`tests/ui-provider.test.tsx`).

## Applying the Marey design system (T-045 re-skin, H-013, H-027, 2026-10-26)

In H-027 the owner adopted DESIGN.md (the Marey graphical timetable) as the design basis, on the condition that the interface is simple and keeps only the controls each screen needs. This ADR's choice (React Aria Components + CSS Modules + custom-property tokens) is unchanged; what changes are the tokens and the components' look:

- **Tokens**: `ui/tokens.css` now uses DESIGN.md's values, one `--gm-color-<key>` per key (light and grey-black dark). There are only five font sizes (`--gm-font-size-label|body|title|headline|display`); spacing and radii follow DESIGN.md's scale. The former palette layer (`--gm-palette-*`) and the blue accent are removed: DESIGN.md's keys are semantic names already, and another wrapping layer brings no benefit. `tests/tokens.test.ts` compares every value with DESIGN.md, and `tests/design-rules.test.ts` blocks any colour, font size, spacing, radius, shadow or transition that is not a token.
- **Theme** (H-011 allows dark; H-013: follow the system by default, switchable and remembered): the choice is stored in the cookie `geminus_theme` (only `light` or `dark`; no cookie means follow the system; `Path=/; Max-Age=31536000; SameSite=Lax`, plus `Secure` under https, not HttpOnly, holds no identity and plays no part in authorization, the same pattern as ADR-020's language cookie). The root layout reads the cookie on the server and outputs `<html data-theme>`, so the first paint already has the right theme, with no inline script that must run before paint (a later strict CSP need not open a nonce or a hash for it). On a choice, the client changes `data-theme` directly, with no re-render. localStorage was not used: it would need an inline script before paint to avoid a flash.
- **Fonts** (H-011 allows OFL web fonts, replacing H-008 (3)): `@fontsource-variable/archivo` 5.3.0 and `@fontsource-variable/noto-sans-tc` 5.3.0 (versions and licences in ADR-011 and THIRD_PARTY_NOTICES). `ui/global-styles.ts` imports the packages' CSS; the build puts the font files in `/_next/static/media`, and they load from the app's own origin. Noto Sans TC is split into files by unicode-range, so a page downloads only the characters it uses. `next/font/google` was not used: it connects to Google Fonts at build time.
  - **2026-10-29 update (H-041)**: the owner replaced the whole visual system; Archivo was replaced by `@fontsource-variable/geist` 5.3.0 and `@fontsource-variable/geist-mono` 5.3.0 (OFL-1.1, self-hosted); Noto Sans TC is unchanged; `@fontsource-variable/archivo` has been removed.
- **The language and theme choices** now use this system's `Select` (the language was a native `<select>` before): the same component as single choices in forms. The arrow keys open the list, and the language changes only on selection (WCAG 3.2.2; P1 of the impeccable audit of 2026-10-19: the native select changed the language on an arrow key, and `key={locale}` rebuilt the component so the focus fell to body; now the focus stays on the control).
- **Components**: button (primary is Graphite Ink; danger is the default outline with red text; disabled is Inset Well), field (34 px, 15 px text, invalid is a red border with an inner ring), select, checkbox (the focus ring is drawn only once), dialog (560 px, 8 px radius, hairline dividers, 12vh), callout (edge + wash, ink-coloured text, the 4 px side bar removed), status (Outcome box), table (12 px 600 header, Control Edge header line, no row hover), shell (48 px app strip). The technical message and the request ID of a Problem are tucked into a "Details" disclosure (H-027 progressive disclosure). `CrashNotice` now uses `Button`; `global-error` loads the global styles and the remembered theme itself.
- **Known simplifications and things not done**: overlays have no entrance animation; notes have no icon yet; the app strip has no Find, account or mode switch (no feature yet). For the reasons and checks see the Simplicity section of DESIGN.md.

## Wrapping React Flow (T-015 hackathon slice, 2026-10-26)

ADR-007 names React Flow as the 2D authoring adapter; the `@xyflow/react` 12.12.0 used in this ADR's spike is the production version (version, date and licence in ADR-011 and THIRD_PARTY_NOTICES). The spikes' common findings are handled in `apps/web/features/model-editor/` as follows:

- **Styles**: only `base.css` (structure) is used, put into the vendor layer with `@import … layer(vendor)` (the built CSS was confirmed to be inside `@layer vendor`); the look is set by `canvas.module.css`, which sets React Flow's CSS variables and the node/edge/attribution styles from tokens. `<Controls>`, `<MiniMap>` and `<Background>` are not used (no dotted grid, DESIGN.md).
- **Strings**: `ariaLabelConfig` is taken entirely from the catalog (the node and edge descriptions, the aria-live while moving, the handles); React Flow's hard-coded English `aria-roledescription` "node" and "edge" and the edge's default name are replaced with catalog text through `domAttributes` and `ariaLabel` (fixed after the editor test in `strings.spec.ts` caught "edge"). **The attribution is kept** (H-009 (4)): the link text "React Flow" and its `aria-label` "React Flow attribution" are the only two strings on the canvas that cannot be translated; `strings.spec.ts` accepts only these two and lists them.
- **Keyboard**: Escape used to clear the selection and make the node lose focus → the editor intercepts it in the capture phase, only clears the selection, and the focus stays on the node. Delete is handled by the editor with a confirmation first (React Flow's `deleteKeyCode` is turned off: it listens on the whole document and would delete the selected node even when the focus is in the property panel). Edges are neither focusable nor selectable, so Tab does not stop on edges first; connections are made in the step's properties (a menu) or by dragging a handle. Nodes are ordered in reading order (left to right, top to bottom), so the Tab order makes sense.
- **Controlled mode**: nodes/edges are produced from the IR each time (`flow.ts`), and React Flow only reports changes; the node sizes it measures are kept in the canvas component and handed back, otherwise every render would measure again. A position during a drag exists only on screen; only a drop or an arrow key turns it into a JSON Patch.
- **Known limitation**: each step is one Tab stop (about 25 in the example line), so a keyboard user going from the canvas to the property panel passes through the remaining steps; a roving tabindex or a "skip to properties" link can be considered later (the full T-015 scope).
