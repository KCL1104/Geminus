# T-045 survey: UI library, design system and styling approach

Status: research input for ADR-021 (to be written as PROPOSED; owner decides). Not a decision.
Prepared: 2026-10-18 (all sources accessed 2026-10-18). Scope: planning/tasks.json T-045, REQ-022, REQ-023, ADR-007/008/016/020.
Nothing was installed and the repository was not modified. Package facts come from the public npm registry metadata, from downloaded and statically inspected package tarballs (grep only, nothing executed), from official docs (including the docs bundled in the installed `next@16.4.1`), and from GitHub raw files.

## 0. How to read this

- **[F]** = verified fact, with the source in brackets (the S-numbers are listed in section 9). "Tarball" means I downloaded that exact npm version and inspected the files.
- **[J]** = my judgement.
- **UNVERIFIED** = I could not confirm it from a primary source. Do not rely on it without checking.
- **Two-week rule** (AGENTS.md, ADR-017/018 practice): a version is eligible only if it was published on or before **2026-10-04**. Section 1 lists the newest eligible stable version as the "pin candidate".
- **Bundle numbers** are bundlephobia min+gzip for importing the whole package (S90). This overstates tree-shaken use. Where bundlephobia gave per-export numbers, I quote them; each per-export number includes that export's dependencies. Real numbers have to come from `next build` in the spike.

### Project constraints that drive the evaluation (from the repo)

- `apps/web`: Next.js 16.4.1, React 19.3.1, TypeScript 6.0.3, ESLint 10, vitest 5, happy-dom [F: apps/web/package.json]. Next.js 16 uses Turbopack by default for `dev` and `build` [F: S80 turbopack.md]. `next.config.ts` is empty [F].
- The root layout is a **Server Component**. It reads the `geminus_locale` cookie, sets `<html lang>` to `en` or `zh-Hant`, and mounts the self-written `I18nProvider`. Every route is dynamically rendered [F: ADR-020, app/layout.tsx].
- Every user-visible string must come from the typed catalog. ESLint `react/jsx-no-literals` and `no-restricted-syntax` block literal strings in JSX text and in `aria-label`/`title`/`placeholder`/`label`... attributes [F: ADR-020]. **Consequence [J]:** every library default English string that reaches the DOM (aria-labels, "Close", "Select all", date-segment names) either has to be overridden from the catalog or explicitly allowed in ADR-021.
- WCAG 2.2 AA on every P0 screen. TEST-033: axe-core (vitest + Playwright), keyboard-only E2E, and screenshots for en/zh-Hant at 390px and 1280px [F: PRD REQ-023, TEST_PLAN].
- 2D authoring uses React Flow (ADR-007). 3D is a read-only PlayCanvas view (ADR-008). ADR-016 adds no state library, and the API and draft rules live in `lib/`.
- No visual preference yet: the default is neutral, information-dense and tool-like (H-007).

---

## 1. Fact table: versions, licence, maintenance

Ages are counted to 2026-10-18. "Rel. 90d / 365d" is the number of stable (non-prerelease) versions published in the last 90 and 365 days [F: S1 npm registry].

| Option | npm package | Latest stable (published, age) | Pin candidate (≥14 days) | Rel. 90d / 365d | Licence (npm) | Licence / usage flags |
|---|---|---|---|---|---|---|
| React Aria Components | `react-aria-components` | 1.22.1 (2026-10-09, 8 d) | **1.21.1** (2026-09-04) | 5 / 12 | Apache-2.0 | None found. `UNSTABLE_Toast` is still unstable [F: S10] |
| Radix Primitives | `radix-ui` | 1.8.1 (2026-10-12, 5 d) | **1.6.7** (2026-07-24) | 8 / 12 | MIT | None. The docs changelog only goes up to 2026-07-20; 1.7.0 and 1.8.x have no notes there yet [F: S20] |
| Base UI | `@base-ui/react` | 1.9.0 (2026-10-09, 8 d) | **1.8.0** (2026-09-04) | 3 / 11 | MIT | None. The old name `@base-ui-components/react` is deprecated [F: S1] |
| Ark UI | `@ark-ui/react` | 5.39.3 (2026-10-05, 12 d) | **5.39.2** (2026-09-13) | 7 / 26 | MIT | None |
| Headless UI | `@headlessui/react` | 2.2.10 (2026-04-07, 193 d) | 2.2.10 | 0 / 1 | MIT | None, but activity is low |
| Mantine | `@mantine/core` (+ dates, notifications, spotlight, charts) | 9.7.3 (2026-10-17, 0 d) | **9.6.3** (2026-09-26) | 12 / 38 | MIT | Requires React ^19.2. One npm publisher account [F: S1] |
| MUI Material | `@mui/material` | 9.5.0 (2026-10-09, 8 d) | **9.4.0** (2026-08-27) | 4 / 17 | MIT | MUI X is open-core: Data Grid column resizing and multi-sort, date-range pickers and Tree View DnD need **commercial** Pro [F: S32]. `@mui/x-data-grid`'s npm `latest` tag points at the v8 backport 8.29.5, not v9 [F: S1] |
| MUI Joy UI | `@mui/joy` | none stable (5.0.0-beta.52, 2025-03-18) | none | 0 / 0 | MIT | "Joy UI is currently on hold" [F: S33] |
| Ant Design | `antd` | 6.6.6 (2026-10-12, 5 d) | **6.6.5** (2026-09-20) | 10 / 44 | MIT | None |
| Chakra UI v3 | `@chakra-ui/react` | 3.37.0 (2026-08-28, 50 d) | 3.37.0 | 2 / 13 | MIT | Depends on Emotion, which has had no release in 365 days (see the Emotion row) |
| IBM Carbon | `@carbon/react` | 1.118.0 (2026-10-07, 10 d) | **1.117.0** (2026-09-23) | 6 / 30 | Apache-2.0 | **Install-time telemetry**: the `postinstall` script runs `ibmtelemetry`, which is on by default in CI and containers; opt out with `IBM_TELEMETRY_DISABLED='true'` [F: tarball, S41]. IBM Plex fonts are OFL-1.1 [F: S1]. A v12 alpha (`2.0.0-alpha.3`) is in progress [F: S1] |
| Fluent UI React v9 | `@fluentui/react-components` | 9.74.9 (2026-09-23, 24 d) | 9.74.9 | 5 / 28 | MIT | The README puts Fluent fonts and icons under the Microsoft Fabric Assets License. That licence allows Segoe and Office/Fabric icons only in apps that "provide access or interact with a Microsoft service" [F: S50, S51]. `@fluentui/react-icons` itself is MIT on npm [F: S1] |
| React Spectrum S2 | `@react-spectrum/s2` | 1.8.2 (2026-10-15, 2 d) | **1.7.1** (2026-09-04) | 6 / 14 | Apache-2.0 | The Provider loads the Adobe Clean font from `use.typekit.net` [F: tarball, S13]. The licence for third-party use of that font is **UNVERIFIED** |
| GitHub Primer React | `@primer/react` | 38.42.0 (2026-10-16, 1 d) | **38.40.1** (2026-10-01) | 12 / 50 | MIT | Described as "The Design System for GitHub" [F: S60]. The tarball contains no LICENSE file; package.json says MIT [F: tarball] |
| Atlassian Design System | `@atlaskit/*` (e.g. `button`) | button 25.4.8 (2026-10-15) | button 25.4.4 (2026-10-01) | 27 / 76 (button) | Apache-2.0 on npm and in the tarball LICENSE | **Conflict.** The ADS License Agreement (updated 2025-03-13) covers "code packages". It grants use only for Add-Ons that "interoperate with or integrate into Atlassian's software", and it forbids modification, derivative works and distribution [F: S70]. **This is a legal blocker** |
| shadcn/ui | `shadcn` (CLI; components are copied into the repo) | 4.22.0 (2026-10-16) | 4.21.1 (2026-10-01) | 19 / 63 | MIT | None. Base UI has been the default primitive layer since July 2026 [F: S80b] |
| Tailwind CSS v4 | `tailwindcss`, `@tailwindcss/postcss` | 4.3.3 (2026-07-16, 93 d) | 4.3.3 | 0 / 14 | MIT | None |
| CSS Modules + CSS custom properties | built into Next.js | n/a | n/a | n/a | (Next.js is MIT) | No new dependency |
| vanilla-extract | `@vanilla-extract/css`, `@vanilla-extract/next-plugin` | css 1.21.3 (2026-10-08); next-plugin 2.5.2 (2026-04-12) | css **1.21.2** (2026-07-27) | 2 / 10 | MIT | Turbopack support is "experimental" [F: S83] |
| Panda CSS | `@pandacss/dev` | 2.2.0 (2026-10-09) | 2.1.1 (2026-10-02) | 9 / 29 | MIT | **2.0.0 was published 2026-09-29**, a new major only 19 days old [F: S1] |
| StyleX | `@stylexjs/stylex` | 0.19.1 (2026-09-15) | 0.19.1 | 1 / 13 | MIT | Still pre-1.0 |
| Emotion (runtime CSS-in-JS) | `@emotion/react`, `@emotion/styled` | react 11.14.0 (2024-12-09); styled 11.14.1 (2025-06-26) | same | 0 / 0 | MIT | No release in 365 days. Next.js 16.4.1 docs still list Emotion under "currently working on support" [F: S81] |

Companion packages (pin candidates in bold, all MIT unless noted) [F: S1]:
- `@xyflow/react` **12.12.0** (2026-09-24)
- `playcanvas` **2.23.0** (2026-10-01; latest 2.23.3 is 2 days old)
- `@playcanvas/react` **0.11.7** (pre-1.0)
- `@tanstack/react-table` **9.2.4** (2026-08-28). v9.0.0 is a new major from 2026-08-04; the last v8 was 8.21.3 on 2025-04-14.
- `@tanstack/react-virtual` **3.14.13** (2026-09-14)
- `recharts` **3.10.1** (2026-07-25)
- `echarts` **6.1.0** (2026-05-19, Apache-2.0)
- `cmdk` 1.1.1 (2025-03-14; no release since)
- `sonner` 2.0.8 (2026-08-09)
- `lucide-react` **1.51.0** (ISC)
- `axe-core` / `@axe-core/playwright` **4.13.0** (MPL-2.0; axe-core 4.14.0 is 13 days old and not yet eligible)
- `@playwright/test` **1.63.0** (Apache-2.0)
- `@ibm/plex-sans-tc` 1.1.1 (OFL-1.1)
- `@fontsource/noto-sans-tc` (OFL-1.1, 69 MB unpacked)
- `@react-aria/optimize-locales-plugin` **2.0.2** (2026-09-01; no Turbopack code). 2.1.0 (2026-10-08) adds Turbopack; Apache-2.0 LICENSE file, no npm `license` field.

---

## 2. Evaluation table (one row per option)

Abbreviations: RSC = React Server Components; SR = screen reader; "gz" = bundlephobia min+gzip for the whole package unless a component is named.

| Option | Next 16 / RSC | Accessibility evidence | i18n fit for en + zh-Hant with our catalog | Data-dense coverage | Theming / neutral look | Size (gz) | Verdict |
|---|---|---|---|---|---|---|---|
| **React Aria Components** | No `"use client"`. Component modules `import "client-only"`, so they must be imported from our own `'use client'` wrappers [F: tarball] | Strongest. Follows APG and documents an SR matrix (VoiceOver macOS/iOS, JAWS, NVDA, TalkBack) [F: S11] | Built-in strings for 30+ locales incl. `zh-TW`, plus `@internationalized/date` and number parsing. **`zh-Hant` resolves to the zh-CN (Simplified) strings unless we pass `zh-TW`** [F: tarball] | Table (sort, resize, keyboard grid, DnD, load-more, virtualizer), Tree, DatePicker, NumberField, Autocomplete, Toast (unstable) | Unstyled. We own the tokens; CSS Modules or Tailwind both documented | 274 kB full; Table 46, DatePicker 34, Select 30, Modal 17 + Dialog 5 | **Spike #1 (recommended)** |
| Radix Primitives | `"use client"` shipped [F: tarball] | APG; "tested in ... commonly used assistive technologies" [F: S21] | No locale-aware date or number components | No table, tree, combobox, date or number primitives [F: S1 deps list] | Unstyled | Dialog ≈ 12.6 kB | Set aside: Base UI covers the same ground and more |
| **Base UI** | `"use client"` in 776 of 1,592 JS files [F: tarball] | APG; focus management; "tested on a broad spectrum of ... screen readers" (no matrix) [F: S23] | NumberField. A few English defaults ("Notifications", "Increase"/"Decrease", "Close", "Dismiss") [F: tarball] | Dialog, Toast, Combobox, Autocomplete, Menu filtering (preview), NumberField, Field/Form. **No table, tree or date picker** [F: S22] | Unstyled | 147 kB full | **Spike #2** (with Tailwind + TanStack Table) |
| Ark UI | `"use client"` shipped; recent RSC fixes [F: tarball, S25] | README: "WCAG compliant components tested with real assistive technologies" (no matrix) [F: S24] | `LocaleProvider`; English defaults overridable with a `translations` prop. Good catalog fit | DatePicker, NumberInput, TreeView, Combobox, Toast, Splitter. **No table** [F: S25] | Unstyled | 290 kB full; DatePicker 43, Dialog 19 | Reserve (fallback if RAC fails) |
| Headless UI | `"use client"` in some files [F: tarball] | "fully accessible" (no detail) [F: S26] | none | 16 components; no table, tree, date or toast [F: S26] | Unstyled | 63 kB | Set aside: too small, 1 release per year |
| **Mantine 9** | Docs: "all Mantine components are client components" and ship `'use client'` [F: S30] | WAI-ARIA, jest-axe and keyboard unit tests, manual VoiceOver [F: S31] | dayjs required for dates; English aria-label defaults [F: S30b, tarball] | Table (basic, TanStack Virtual example), Tree, dates, Spotlight, notifications, form, charts (Recharts) [F: S30] | CSS Modules-based; CSS variables; `styles.layer.css` [F: tarball]. Default look is fairly neutral [J] | 163 kB core; Select 38, Modal 22, Table 13 | **Spike #3** (batteries-included contrast) |
| MUI Material 9 | Emotion runtime plus `AppRouterCacheProvider` registry [F: S34] | **Draft WCAG 2.2 VPAT for 12 components**, self-assessed, "no assistive-technology testing ... yet"; the default focus indicator has a gap [F: S35] | `zhTW` locale object [F: S36] | Material set; X Data Grid / pickers / tree are open-core | Material Design look; hard to neutralise [J] | 155 kB + Emotion | Set aside |
| Ant Design 6 | Runtime CSS-in-JS + `@ant-design/nextjs-registry`. App Router "currently not support using sub-components via `.`" [F: S37] | No conformance statement; many a11y fixes in the changelog [F: S38] | Built-in `zh_TW` [F: tarball] | Richest set (Table with virtual, Tree, Form, pickers) | Distinct Ant look; tokens and algorithms | 456 kB; Table 184, DatePicker 109 | Set aside |
| Chakra UI v3 | Emotion runtime + provider; `suppressHydrationWarning` [F: S39] | "accessible component system" (inherits Ark) | via Ark | Table, TreeView, DatePicker, Toast, Charts (Recharts) | Tokens and recipes | 314 kB | Set aside: same behaviour as Ark, plus Emotion |
| IBM Carbon | `"use client"` only at the package index; **Sass required** (peer `sass`) [F: tarball] | IBM Accessibility Checklist "based on WCAG AA, Section 508, and European standards"; no SR-testing claim [F: S40] | `translateWithId` per component (good catalog fit) [F: tarball] | DataTable (no virtualization found), TreeView, DatePicker (flatpickr), UIShell [F: tarball] | IBM look; themes | 230 kB; DataTable 42, Modal 29 | Set aside (telemetry, Sass, brand); best fallback if the owner wants a finished enterprise look |
| Fluent UI v9 | Griffel runtime; client registry with `useServerInsertedHTML`. Docs warn of duplicated styles on streaming. Fluent docs still point to a third-party SWC plugin [F: S52, S53] | Aims at WCAG 2.1 + ARIA; zoom tested to 400%; tabster [F: S54] | "Internationalization ... deliberately not part of Fluent UI" [F: S54] | DataGrid (virtualization via a contrib package), Tree, Toast, DatePicker (compat) [F: S55] | Microsoft 365 look; Segoe licence | 320 kB | Set aside |
| React Spectrum S2 | Style macros; **"Turbopack is not supported"**, so Next 16 needs `--webpack` [F: S12] | Same React Aria behaviour | Same as RAC | TableView, TreeView, pickers | "colors and internal padding cannot be customized" [F: S14] | not measured | Set aside |
| Primer React | `"use client"` at entry points [F: tarball] | No WCAG level stated [F: S61] | **Hard-coded English aria-labels** (e.g. "Close" ×4, "Clear selection") [F: tarball] | DataTable only in `experimental` [F: tarball] | GitHub look | UNVERIFIED (bundlephobia ratio implausible) | Set aside |
| Atlassian DS | No `"use client"` (0 of 159 files in button) [F: tarball] | No WCAG level stated [F: S71] | not examined (UNVERIFIED) | dynamic-table, modal-dialog, … | Atlassian look | not measured | **Excluded (licence)** |
| shadcn/ui | Copied source, so we control directives; Base UI or Radix + Tailwind v4 [F: S80b] | Inherits from Base UI / Radix | Our strings | Data Table = TanStack Table; Command = cmdk; Toast = Sonner; Chart = Recharts [F: S80a] | Tailwind tokens | n/a | Not adopted as such (H-007). Spike #2 builds the equivalent stack by hand |

Styling approaches are compared in section 5.

---

## 3. Per-option notes

### 3.1 React Aria Components (Adobe)

- **Version and activity [F: S1, S10].** 1.21.1 was published 2026-09-04. 1.22.0/1.22.1 (2026-10-08/09) are too new. Releases come roughly monthly:
  - 1.18 (May): TableFooter, multi-date Calendar.
  - 1.19 (Jun): Tree/GridList keyboardNavigationBehavior, Autocomplete inline completion.
  - 1.20 (Jul): interactive content in Table rows, TokenField alpha.
  - 1.21 (Sep): NavigationTree.
  - 1.22 (Oct): Sheet, Turbopack support for the locale plugin.
  - Licence is Apache-2.0. The first 1.0 was published 2023-12-20, with no new major since.
- **RSC [F: tarball, S15].**
  - 1.21.1's `dist/exports/*.mjs` contain no `"use client"`, and 70 component modules `import "client-only"`. Importing RAC directly into a Server Component file therefore fails.
  - The documented Next.js App Router setup puts `I18nProvider` in a `"use client"` provider file and sets `lang`/`dir` on `<html>` in the server layout.
  - `@react-aria/optimize-locales-plugin` strips unused locale strings, and the docs describe Turbopack use on Next.js ≥ 15.4. **Turbopack support exists only in plugin 2.1.0** (published 2026-10-08, so not eligible until 2026-10-22). The eligible 2.0.2 (2026-09-01) contains no Turbopack code [F: tarballs]. The plugin's package.json has no `license` field, but the tarball ships an Apache-2.0 LICENSE [F: tarball].
  - [J] This fits `apps/web/ui/`: each styled wrapper is a `'use client'` module anyway, and pages stay Server Components.
- **Accessibility [F: S11].** Follows WAI-ARIA and the APG. "React Aria components are tested across a wide variety of devices, browsers, and screen readers":
  - VoiceOver on macOS (Safari, Chrome);
  - JAWS and NVDA on Windows (Firefox, Chrome);
  - VoiceOver on iOS;
  - TalkBack on Android.
  Known axe false positives are tracked in a wiki. This is the most concrete public testing statement of any option surveyed.
- **i18n [F: S11, tarball].**
  - Supports 30+ locales, including `zh-CN` and `zh-TW`, plus 13 calendar systems and locale number parsing.
  - **Pitfall (verified in `@internationalized/string` 3.2.10 and `react-aria` 3.52.1):** lookup is exact match, then language+script, then language, then the *first* `zh-*` key. The bundled dictionaries contain only `zh-CN` and `zh-TW`, in that order (all 19 string modules checked). So `I18nProvider locale="zh-Hant"` gives **Simplified** labels such as date-segment names.
  - The wrapper must map `zh-Hant` to `zh-TW` for RAC while `<html lang="zh-Hant">` stays as is. With the optimize-locales plugin, list `en-US` and `zh-TW`.
  - [J] RAC's internal strings (e.g. "Select all", calendar labels) do not come from our catalog. ADR-021 must either allow them (they are translated by Adobe) or override them per component.
- **Data-dense [F: S10, S16, S17].**
  - Table: `allowsSorting`, single and multiple selection, `ResizableTableContainer`/`ColumnResizer`, arrow-key cell navigation, DnD with a keyboard alternative, and `TableLoadMoreItem`.
  - `Virtualizer` with `TableLayout` (also ListBox and GridList).
  - Tree (sections are "alpha"), GridList, Tabs, Dialog/Modal, Form/FieldError, DatePicker/DateRangePicker/TimeField, NumberField, ComboBox/Select, Autocomplete (the command-palette pattern), Toolbar, Menu.
  - Toast only as `UNSTABLE_*` exports.
  - No charts.
- **Theming.** Unstyled. States are exposed as `data-*` attributes and render props; Tailwind v4 has a plugin; vanilla-CSS and Tailwind starter kits exist [F: S18].
- **Size [F: S90].** 274 kB gz for the whole package. Per export: Table 46, Tree 43, DatePicker 34, Select 30, NumberField 20, Modal 17, Button 12.5, TextField 9, Virtualizer 8, Dialog 5. [J] These per-export numbers include all locale dictionaries, so the plugin should cut them.
- **Risks.** Toast is unstable. The API surface is large (collections, render props). Few npm publishers (2) [F: S1], but it is Adobe-backed and S2 is built on it [J].

### 3.2 Radix Primitives (WorkOS)

- `radix-ui` 1.6.7 was published 2026-07-24; 1.7.0 (10-05) and 1.8.x (10-12) are too new. The docs changelog shows large 2026 releases (June 6: composition APIs; June 30 / July 6: React 19 re-render fixes; July 20: subpath imports and tree-shaking), but nothing after July 20 [F: S20]. MIT, © WorkOS [F: S20a].
- `"use client"` is shipped [F: tarball]. Accessibility: APG, "tested in a wide selection of modern browsers and commonly used assistive technologies"; no WCAG statement [F: S21].
- Primitives: dialog, alert-dialog, tabs, toast, select, menu, dropdown-menu, context-menu, popover, form, toolbar, scroll-area, slider, … **No combobox, number field, date picker, tree or table** [F: S1 dependency list].
- [J] Set aside. Base UI is built by much of the same team ("From the creators of Radix, Floating UI, and Material UI" [F: S22a]), has the missing Combobox/Autocomplete/NumberField, and is now shadcn's default.

### 3.3 Base UI (MUI team)

- v1.0.0 was released 2025-12-11 and v1.9.0 on 2026-10-09; releases come monthly [F: S22]. Pin 1.8.0 (2026-09-04). MIT. The team listed includes Colm Tuite and Jenna Smith [F: S22a]. MUI's 2026 post calls Base UI "the long-term direction for foundational, unstyled components" [F: S33].
- `"use client"` is shipped [F: tarball]. It has a CSP provider for inline style nonces [F: S22].
- Accessibility: APG keyboard support; focus management (`initialFocus`/`finalFocus`); the docs say visible focus and contrast are *the app's* responsibility and recommend APCA. Testing claim: "a broad spectrum of browsers, devices, platforms, screen readers" [F: S23].
- Components: Accordion, Alert Dialog, Autocomplete, Checkbox, Combobox, Context Menu, Dialog, Drawer, Field/Fieldset/Form, Menu, Menubar, Navigation Menu, Number Field, Popover, Select, Tabs, **Toast**, Toolbar, Tooltip, … [F: S22]. **No Table, Tree or date picker** in the public exports of 1.8.0. Internal temporal adapters exist, and `date-fns` is an optional peer [F: tarball].
- i18n: strings are mostly caller-provided. A few English defaults exist in dist ("Notifications" toast region, "Increase"/"Decrease", "Close", "Dismiss") [F: tarball]. Whether they can all be overridden by props is UNVERIFIED.
- Size: 147 kB gz for the whole package [F: S90]; per-export numbers are not available.
- [J] Strength: smaller, modern API, MIT, broad team. Weakness for Geminus: the table (import row errors, results) and the tree would be our own a11y work, on TanStack Table v9 (a new major).

### 3.4 Ark UI (Chakra team, Zag.js state machines)

- 5.39.2 was published 2026-09-13; changelog dates run to 5.39.3 (2026-10-05) [F: S25]. Majors were frequent in 2023–2025 (v1 2023-11 to v5 2025-03), with none since [F: S1]. MIT.
- RSC: `"use client"` is shipped. The changelog fixes `asChild` across the RSC boundary and Next 15 `Activity` import issues [F: S25].
- Accessibility claim (README): "WCAG compliant components tested with real assistive technologies" [F: S24]; no test matrix. Recent fixes include `Field.ErrorText` now using `aria-describedby` for VoiceOver/Narrator [F: S25].
- i18n: `LocaleProvider`, Intl formatting, and a `translations` prop (Partial) on many components for built-in strings [F: S25]. [J] This is the best fit with a catalog-only rule.
- Components: DatePicker/Date Input, Number Input, Tree View, Combobox, Listbox, Toast, Dialog, Splitter, Pagination, Tags Input, JSON Tree View, Tour, … **No table** [F: S25].
- Size: 290 kB gz full; DatePicker 43 kB, Combobox 33 kB, Dialog 19 kB [F: S90].
- [J] A credible alternative to RAC with less SR-testing evidence and no table. Kept as the fallback.

### 3.5 Headless UI (Tailwind Labs)

- 2.2.10 (2026-04-07) is the only release in the last 365 days [F: S1, S27].
- 16 components (Button, Checkbox, Combobox, Dialog, Disclosure, Fieldset, Input, Listbox, Menu, Popover, Radio Group, Select, Switch, Tabs, Textarea, Transition). No table, date, tree or toast [F: S26]. "fully accessible", with no detail [F: S26].
- [J] Set aside: coverage and activity are too low for a data-dense app.

### 3.6 Mantine 9

- 9.0.0 was released 2026-03-31. Minors come roughly monthly; 9.7.0 was 2026-10-06 [F: S30a]. Majors: v7 2023-09, v8 2025-05, v9 2026-03 [F: S1]. Pin 9.6.3 (2026-09-26). MIT. One npm publisher account (`rtivital`) [F: S1].
- Requires React 19.2+ [F: S30a]. All components are client components with `'use client'` [F: S30].
- App Router setup:
  - `ColorSchemeScript` plus `mantineHtmlProps` on `<html>`.
  - PostCSS with `postcss-preset-mantine` [F: S30c].
  - CSS-variables theme; a `styles.layer.css` variant puts Mantine in a cascade layer [F: tarball].
- Accessibility: follows WAI-ARIA; axe (jest-axe) and keyboard unit tests; "manually tested with screen readers (VoiceOver)" [F: S31].
- Components:
  - Table: styled HTML table only. Sorting is your own; the docs show virtualization with `@tanstack/react-virtual` [F: S30d].
  - Tree and TreeSelect.
  - `@mantine/dates` (**dayjs is required**, `DatesProvider` locale) [F: S30b].
  - `@mantine/notifications`, `@mantine/spotlight` ("Command center"), `@mantine/form`, `@mantine/charts` (peer `recharts >=3.2.1`) [F: S1, S30].
- i18n: English aria-label defaults in dist (e.g. "Toggle password visibility", "Scroll left") [F: tarball]. Date names come from dayjs locales, a second date library next to our `Intl` formatting [J].
- Default font stack is system fonts without CJK families [F: tarball].
- Size: 163 kB gz core; Select 38, Modal 22, NumberInput 20, Table 13, Tree 11 [F: S90]. Each package also needs its CSS file.
- [J] The fastest route to a complete and fairly neutral app. Costs: a single maintainer, yearly majors, dayjs, and English defaults to override.

### 3.7 MUI Material 9 (and Joy UI)

- `@mui/material` 9.0.0 was released 2026-04-07; the name jumped from v7 straight to v9 [F: S1]. Pin 9.4.0.
- The styling engine is Emotion (`@mui/styled-engine` depends on `@emotion/cache`/`serialize`) [F: S1]. Pigment CSS "remains in alpha phase and is currently on hold" [F: S33].
- App Router needs `@mui/material-nextjs` `AppRouterCacheProvider`, an Emotion registry that collects CSS while streaming [F: S34]. Next.js 16.4.1 docs list `@mui/material` as supported in Client Components only [F: S81].
- Accessibility: the best *documentation* of any option. A VPAT-style WCAG 2.2 A/AA report for v9.5.0, but marked "Draft — partial coverage": 12 components, self-assessment, "no assistive-technology testing has been performed yet". Known gaps include a default focus indicator with untested contrast, palette colours below contrast minimums, and status messages not announced [F: S35].
- i18n: locale objects including `zhTW` [F: S36, tarball].
- Data: MUI X Community (MIT) Data Grid, pickers, charts and tree. **Column resizing, multi-sort, column pinning, date-range pickers and Tree DnD are Pro (commercial)** [F: S32]. Version gotcha: the `@mui/x-data-grid` `latest` dist-tag is 8.29.5 while v9 is 9.15.0; v9.14.0 is the eligible v9 pin [F: S1].
- Joy UI: no stable release; "on hold" [F: S33].
- [J] Set aside: the Material look, runtime CSS-in-JS on a stalled engine, and open-core grid features.

### 3.8 Ant Design 6

- 6.0.0 was released 2025-11-21; pin 6.6.5 (2026-09-20) [F: S1]. v6 requires React ≥ 18, no longer needs the React 19 patch, uses CSS variables by default, and supports modern browsers only [F: S37a].
- App Router: runtime CSS-in-JS (`@ant-design/cssinjs`) with `@ant-design/nextjs-registry` for first-paint styles, or a whole-CSS export. "Next.js App Router currently not support using sub-components via `.` like `<Select.Option />`" [F: S37].
- Accessibility: no conformance or testing statement in the docs index; the changelog shows steady a11y fixes [F: S38]. Built-in `zh_TW`, `zh_HK` and `zh_CN` locales [F: tarball].
- Size: 456 kB gz full; Table 184, DatePicker 109, Modal 80, Form 75 [F: S90]. 48 runtime dependencies [F: S1].
- [J] Set aside: heaviest option, strong Ant brand, weakest a11y evidence. Its Chinese-locale strength does not outweigh these.

### 3.9 Chakra UI v3

- 3.0.0 was released 2024-10-22; 3.37.0 on 2026-08-28 [F: S1]. Built on Ark UI with Emotion as a peer [F: S1]. Works with Next.js 15 and 16. The App Router setup needs a client `Provider` (ChakraProvider + next-themes) and `suppressHydrationWarning` on `<html>` [F: S39]. Charts use Recharts [F: S39].
- [J] Set aside: it adds a runtime CSS-in-JS engine on top of Ark, which we can use directly.

### 3.10 IBM Carbon

- `@carbon/react` 1.117.0 (2026-09-23) pin, Apache-2.0. **`postinstall: ibmtelemetry --config=telemetry.yml`.** IBM Telemetry runs in CI and containers on any install unless `IBM_TELEMETRY_DISABLED='true'` is set; "By installing this package as a dependency you are agreeing to telemetry collection" [F: tarball, S41]. `@carbon/styles` also depends on `@ibm/telemetry-js` [F: S1].
- Peer `sass` (Sass build required) [F: S1]. `"use client"` only on the package index [F: tarball].
- Accessibility: follows the IBM Accessibility Checklist "based on WCAG AA, Section 508, and European standards"; no SR-testing or audit claim [F: S40].
- i18n: `translateWithId` props (DataTable has `defaultTranslations`) [F: tarball]. [J] A good fit with a catalog.
- Components: DataTable (sorting, batch actions, expansion; no virtualization code found), TreeView, DatePicker (`flatpickr`), Notification, ComboBox, UIShell, … [F: tarball].
- IBM Plex is OFL-1.1, and `@ibm/plex-sans-tc` exists [F: S1].
- Size: 230 kB gz; DataTable 42 kB, Modal 29 kB [F: S90].
- [J] Set aside for P0: default-on telemetry in our CI/containers, a Sass pipeline, the IBM visual identity, and an upcoming v12. It is the strongest *finished enterprise look* if the owner prefers one after the prototype.

### 3.11 Microsoft Fluent UI React v9

- 9.74.9 (2026-09-23) pin; MIT code.
- **Assets.** The README says "Usage of the fonts and icons referenced in Fluent UI React is subject to the terms of the assets license agreement" [F: S50]. That licence (Nov 2019) grants Segoe, Office icons and Fabric icons "exclusively" for apps that use a Microsoft API to "provide access or interact with a Microsoft service", and it is revocable [F: S51]. `@fluentui/react-icons` is MIT on npm [F: S1]. [J] Legal review would be needed; Geminus is not a Microsoft-integrated app.
- Styling: Griffel CSS-in-JS. App Router needs a client renderer with `useServerInsertedHTML`; Griffel docs warn of duplicate styles per streaming flush [F: S52]. Fluent's appDir guide still refers to the third-party `fluentui-next-appdir-directive` SWC plugin [F: S53], although `@fluentui/react-dialog` 9.18.5 now ships `"use client"` in component files [F: tarball].
- Accessibility: "aim to fully respect both WCAG and ARIA"; tabster focus; zoom tested to 400%. "Internationalization, globalization ... deliberately not part of Fluent UI" [F: S54].
- DataGrid virtualization comes from the `fluentui-contrib` package; DatePicker is under "Compat" [F: S55].
- [J] Set aside.

### 3.12 Adobe React Spectrum S2

- 1.7.1 (2026-09-04) pin; 1.0.0 was released 2025-12-16 [F: S1]. Same React Aria behaviour, so the a11y evidence of 3.1 applies.
- **Blockers:**
  - "`unplugin-parcel-macros` currently only works with webpack, not Turbopack. When using Next.js 16 or later, make sure to start your app with the `--webpack` flag" [F: S12].
  - "Other styles such as colors and internal padding cannot be customized within Spectrum components" [F: S14].
  - The Provider loads Adobe Clean from Typekit [F: tarball, S13].
- [J] Set aside. It cannot look neutral, it forces webpack, and it makes a third-party font request.

### 3.13 GitHub Primer React

- 38.40.1 (2026-10-01) pin; majors are yearly (v35 2022 to v38 2025-10-27) [F: S1]. Uses CSS Modules (`*.module.css.js` in dist) and ships `"use client"` [F: tarball].
- The docs make no WCAG statement [F: S61]. DataTable is exported only from `@primer/react/experimental` [F: tarball]. Hard-coded English aria-labels appear in dist [F: tarball]. Its identity is "The Design System for GitHub" [F: S60].
- [J] Set aside.

### 3.14 Atlassian Design System

- npm and the tarball LICENSE say Apache-2.0 [F: tarball]. The ADS License Agreement on atlassian.design covers ADS including "code packages". It limits use to Add-Ons that interoperate with Atlassian products, and prohibits modification, derivative works and distribution to third parties [F: S70].
- No `"use client"` [F: tarball]. Very high release churn: 27 releases of `@atlaskit/button` in 90 days, and 5 majors of `modal-dialog` since 2025-02 [F: S1].
- [J] **Excluded until legal clarifies.** Do not spike.

### 3.15 shadcn/ui (for comparison)

- The CLI (MIT) copies component source into the repo. The changelog shows Tailwind v4 (Feb 2025), unified `radix-ui` (Feb 2026), CLI v4 (Mar 2026), and **"Base UI as the Default" (July 2026)** [F: S80b].
- Its data-dense pieces are other libraries: Data Table = TanStack Table; Command = cmdk (last release 2025-03-14); Toast = Sonner; Chart = Recharts; Calendar/Date Picker [F: S80a, S1].
- [J] The "own the code in `apps/web/ui/`" model is worth copying whatever we choose. H-007 rules out adopting shadcn by default, so spike #2 rebuilds its stack (Base UI + Tailwind v4 + TanStack) by hand and evaluates it on equal terms.

---

## 4. Cross-cutting findings

### 4.1 Locale and strings (REQ-022 / ADR-020)

- **No library reads our catalog.** Each one either has built-in English (Base UI, Mantine, Primer, Ark defaults), its own translated dictionaries (React Aria / S2: `zh-TW`; MUI: `zhTW`; antd: `zh_TW`), or per-component translation props (Ark `translations`, Carbon `translateWithId`) [F: tarballs]. ADR-021 should state the rule [J]. The proposal is:
  - every library string that can be set through a prop is fed from the catalog;
  - React Aria's own zh-TW/en strings are accepted only for AT-only text that cannot be overridden, and that list is recorded;
  - the spike greps the rendered zh-Hant DOM for leaked English.
- **Locale tag mapping [F: tarball].** The app uses `zh-Hant` (ADR-020). React Aria needs `zh-TW` to get Traditional strings. MUI uses `zhTW`, antd `zh_TW`, dayjs `zh-tw`. Keep the mapping in one function in `lib/i18n/`, with a unit test [J].
- **Number and date input.** P0 needs locale-aware numbers (model parameters, durations) more than date pickers [J]. RAC NumberField, Base UI NumberField and Ark NumberInput use `Intl`. `@mantine/core` depends on `react-number-format` [F: S1]; that NumberInput uses it is my inference [J].

### 4.2 CJK typography (zh-Hant) and text length

- [F: S100] `word-break: keep-all` stops breaks inside CJK text, so do not apply it globally. `break-all` does not affect CJK. `auto-phrase` is experimental.
- [F: S101] `text-autospace` (spacing between CJK and Latin text) is "Baseline 2025, newly available". Use it only as progressive enhancement.
- [J] Font stack: system fonts first; no CJK web font in P0. A full Noto Sans TC package is about 69 MB unpacked [F: S1], and none of the libraries ships CJK fonts.
  - Proposed token: `--font-sans: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, "PingFang TC", "Noto Sans TC", "Microsoft JhengHei", sans-serif;`
  - Under `:lang(zh-Hant)`, put the CJK families first so Latin digits stay consistent but Han glyphs come from a Traditional font.
  - `lang="zh-Hant"` on `<html>` already lets browsers pick Traditional glyph forms.
  - Naming a locally installed font like Segoe UI in a stack is not using Microsoft's assets CDN. Even so, Fluent's assets licence is a reason not to download Segoe [J].
- [J] Table cells hold long unbroken Latin tokens (IDs, JSON Pointers, metric paths). Use `overflow-wrap: anywhere` and `font-variant-numeric: tabular-nums` for numbers. Use `line-height` ≥ 1.5 for body text; WCAG 1.4.12 Text Spacing is AA, so test it.
- [J] Length difference: zh-Hant labels are usually shorter in characters but each glyph is about 1em wide, and English is the longest at 390 px. TEST-033's en/zh-Hant × 390/1280 screenshots cover this. Tokens should allow wrapping in buttons and table headers instead of fixed widths.

### 4.3 WCAG 2.2-specific requirements that land on the design system [F: S102, S103]

- **2.5.8 Target Size (Minimum, AA):** pointer targets at least 24×24 CSS px, or spaced so that 24 px circles do not overlap. Dense tables and toolbars need a minimum "compact" density that still passes [J].
- **2.4.11 Focus Not Obscured (AA):** sticky headers, toasts and React Flow panels must not cover the focused element [J].
- **2.5.7 Dragging Movements (AA):** "For any action that involves dragging, provide a simple pointer alternative." This hits the React Flow editor (moving nodes, drawing edges) and any column resize or row reorder. RAC's DnD has keyboard support [F: S16]. React Flow offers keyboard moves and `ariaLabelConfig` [F: S110], but a single-pointer alternative still needs UI such as a property-panel position or a connect menu [J]. This belongs to T-015; flag it in ADR-021.
- 4.1.1 Parsing was removed in 2.2 [F: S102].

### 4.4 Coexistence with React Flow and PlayCanvas

- [F: S111] React Flow needs its `base.css` (or `style.css`). It themes through `--xy-*` CSS variables and a `colorMode` prop, and the docs recommend importing its stylesheet into a lower cascade layer (`layer(base)`) so app styles win. `ariaLabelConfig` lets us feed React Flow's built-in a11y strings from the catalog [F: S110]. React Flow states it "provides features that can help you meet key WCAG 2.1 AA criteria when properly implemented" [F: S110].
- [J] Token plan: map our tokens onto `--xy-*` (node background, border, selection, handle) so 2D, panels and dialogs share one palette. Mantine (`styles.layer.css`) and S2 also use layers; CSS Modules and Tailwind v4 both work with `@layer`.
- [J] Overlays: library modals render in portals and apply focus traps and scroll locks. The spike must check that Escape in a dialog does not also deselect in React Flow, and that focus returns to the node or panel. PlayCanvas (637 kB gz for the full engine [F: S90]) should be loaded with a client-only dynamic import so its size and failures stay out of the 2D path (PRD: "3D 載入失敗不可阻擋 2D").
- `@playcanvas/react` 0.11.7 is pre-1.0, with peer React ^18.3.1 || ^19.1.0 [F: S1].

### 4.5 Runtime CSS-in-JS and RSC

- [F: S81] Next.js 16.4.1 docs: "Using CSS-in-JS with newer React features like Server Components and Streaming requires library authors to support the latest version of React". Supported libraries work "in Client Components", through a style registry plus `useServerInsertedHTML`. Emotion is listed as "currently working on support".
- [J] MUI, Chakra, antd and Fluent all need a registry or cache provider in the root layout, and they all inject styles at runtime. Our pages are dynamically rendered on every request (ADR-020), so per-request style serialization is a real recurring cost. Zero-runtime CSS (CSS Modules, Tailwind, vanilla-extract, StyleX, Panda) avoids it.

### 4.6 Charts (results dashboards) — not decided here

- Recharts 3 has `accessibilityLayer` "on ... by default" (Tab into the chart, arrow keys) since 3.0.0 (2025-06-23) [F: S120, S1]. It uses SVG and costs about 62 kB gz per chart type [F: S90]. Mantine, Chakra and shadcn already pair with it.
- ECharts 6.1.0 (Apache-2.0) is canvas-based, with aria labels and decal patterns that are **off by default**; the docs mention no keyboard support. It costs about 365 kB gz for `init` [F: S121, S90].
- [J] Defer the choice to the results-comparison task. It is independent of the primitives choice.

---

## 5. Styling approaches

| Approach | RSC | Runtime cost | Tokens | Fit for Geminus | Notes |
|---|---|---|---|---|---|
| **CSS Modules + CSS custom properties** | Works in Server Components (Next docs example is a server `page.tsx`) [F: S82] | none | `:root` custom properties; themes and density via `[data-theme]`/`[data-density]` [J] | **Recommended** | Built in, no dependency, no new licence. CSS order follows import order [F: S82]. More hand-written CSS [J] |
| Tailwind CSS v4 | Zero runtime; one global CSS entry via `@tailwindcss/postcss` [F: S82] | none | `@theme` variables become real CSS variables (`--color-*`) [F: S84] | Strong alternative (spike #2) | Needs Chrome 111 / Safari 16.4 / Firefox 128. "not designed to be used with ... Sass". "we don't recommend using CSS modules and Tailwind together" [F: S85]. Long class lists in JSX; `jsx-no-literals` ignores props [J] |
| vanilla-extract | via next-plugin | none | typed `createTheme` | Possible | Turbopack support is "experimental. Its API is unstable" [F: S83]. next-plugin is 6 months old |
| Panda CSS | PostCSS + codegen | none | tokens/recipes | Not now | v2.0.0 was 19 days old on 2026-10-18 [F: S1]; v2 migration notes not found (UNVERIFIED) |
| StyleX | Babel plugin + PostCSS plugin; "Since Next.js 16.0.3 ... Webpack and Turbopack" [F: S86] | none | `defineVars` | Not now | Pre-1.0 (0.19.1). A Babel config disables SWC under webpack; Turbopack keeps SWC for internals [F: S80] |
| Emotion (runtime) | Client Components only + registry; Next lists it as "currently working on support" [F: S81] | yes | theme object | No | No release in 365 days [F: S1] |
| Sass (Carbon) | supported by Next | none | Sass variables | Only if Carbon | Conflicts with Tailwind v4 [F: S85] |

[J] Recommended token set for `apps/web/ui/tokens.css`, used by any spike:
- colour: neutral grey ramp, one accent, and status success/warning/danger/info, each with a contrast-checked text pair;
- typography: `--font-sans` with a CJK fallback, `--font-mono`, a size scale, `line-height` ≥ 1.5 for body text;
- space and size: a 4 px scale; `--target-min: 24px`; `--control-h-compact` and `--control-h-default`;
- radius and elevation;
- focus: `--focus-ring` with 2 px width and a ≥ 3:1 contrast colour, plus an offset;
- z-index layers: base, React Flow, sticky, popover, modal, toast;
- motion, honouring `prefers-reduced-motion`.

Dark mode: define the variables so a `[data-theme="dark"]` set can be added later. ADR-021 decides whether P0 ships it.

---

## 6. Recommendation

### One recommendation

**React Aria Components 1.21.1 + CSS Modules + CSS custom-property design tokens.** Our own styled, `'use client'` wrappers live in `apps/web/ui/`. Pages and layouts stay Server Components. There is no runtime CSS-in-JS and no Tailwind.

Why [J, based on the facts above]:
1. **Accessibility evidence is the strongest available.** It is the only option with a published SR/browser test matrix, and its behaviour covers the hard data-dense widgets we would otherwise build ourselves: Table with keyboard grid navigation, resizing and virtualization; Tree; DnD with keyboard; DatePicker; NumberField. These are exactly where WCAG 2.2 AA failures happen in tool UIs.
2. **Coverage of P0 screens with one library.**
   - Editor property panels: Form, Field, NumberField, Select.
   - Import row-error tables: Table + Virtualizer.
   - Proposal review: Dialog, Tabs, GridList.
   - Run monitoring and result comparison: Table, Meter/ProgressBar.
   - Command palette: Autocomplete.
   - No second headless library or table engine is needed.
3. **Neutral look is ours by construction.** There is no brand to fight, unlike Material, Ant, Carbon, Fluent, Spectrum or Primer. The tokens feed React Flow's `--xy-*` variables too.
4. **Server Components stay clean.** CSS Modules are zero-runtime and work in Server Components. Only interactive leaves are client components, and no style registry sits in the root layout.
5. **Licence and stability.** Apache-2.0, no new major since 2023-12, monthly minors, Adobe-backed.

Accepted trade-offs and mitigations:
- **zh-Hant mapping:** map to `zh-TW` at the provider, with a test that asserts Traditional output. Otherwise React Aria serves Simplified strings.
- **Library-internal strings:** ADR-021 must record which RAC AT strings are accepted versus overridden. The owner has to approve this exception to "all strings from the catalog".
- **`UNSTABLE_Toast`:** the spike decides between using it behind our wrapper or a small own `role="status"` region. The toast API is tiny either way.
- **Size:** 46 kB gz for Table and 34 kB for DatePicker per bundlephobia, before locale stripping. Measure the spike route's First Load JS with and without locale stripping. Turbopack support in `@react-aria/optimize-locales-plugin` arrives only in 2.1.0, which becomes eligible on 2026-10-22. Before then, the spike either measures without stripping or records a one-off `--webpack` measurement as a comparison only.
- **More CSS to write than with a styled kit.** Use React Aria's vanilla-CSS starter as a reference. The vanilla-CSS starter kit and the shadcn registry path should be checked for licence before copying (UNVERIFIED).

### The 2–3 combinations to spike (same form + table + dialog)

| # | Combination (pin) | What it represents | Main question the spike answers |
|---|---|---|---|
| 1 | **React Aria Components 1.21.1 + CSS Modules + tokens** (+ `@internationalized/date` 3.12.4) | Headless with the strongest a11y; zero-runtime CSS | Can we reach a neutral, dense look and 0 axe violations with acceptable JS size and wrapper effort? Do zh-TW mapping and RAC's internal strings satisfy REQ-022? |
| 2 | **Base UI 1.8.0 + Tailwind CSS 4.3.3 + TanStack Table 9.2.4 + TanStack Virtual 3.14.13** | The shadcn-era mainstream stack, built by hand (H-007) | How much a11y work does a self-built table/grid need? Is utility CSS easier to keep consistent than CSS Modules? |
| 3 | **Mantine 9.6.3** (+ `@mantine/dates` 9.6.3 + dayjs; tokens mapped through `createTheme`/CSS variables) | A batteries-included styled library on CSS Modules | What does a finished kit save, and what does making it look like our tokens, overriding its English defaults and adding dayjs cost? |

**Identical spike content (one route, behind no auth, using fake data under `testdata/` style fixtures):**
- App shell (header, nav, LocaleMenu) and a **form**:
  - text field, select, number field (locale formatted), checkbox;
  - inline validation messages from a `ValidationIssue`-shaped object with a JSON Pointer;
  - submit producing a 409 "conflict" state (ADR-016 copy from the catalog).
- **Table:**
  - 200 rows (and a 5,000-row variant) of import row errors;
  - sortable columns, row selection, sticky header, keyboard navigation;
  - an empty state and an error state.
- **Dialog:** "Approve proposal?" confirm with title and description wiring, focus trap, Escape, focus return, and a success toast/status message.
- A small React Flow 12.12.0 canvas beside the form, to check focus, Escape and cascade-layer conflicts. PlayCanvas is optional, as a dynamic-import placeholder only.

**Same measurements for each (record commands and output; anything not run is NOT RUN):**
1. axe-core 4.13.0 via vitest DOM and `@axe-core/playwright` 4.13.0 with WCAG 2.0/2.1/2.2 A and AA tags: violation count per state.
2. Keyboard-only Playwright script of the whole flow; focus visible at every step.
3. Screenshots for en/zh-Hant × 390/1280; manual review of wrapping and overflow.
4. Leaked-English check: on the zh-Hant render, list every Latin-script text node and accessible name that does not come from the catalog.
5. `next build` (Turbopack) First Load JS and CSS bytes for the spike route.
6. Wrapper code size in `apps/web/ui/` and the number of lint exceptions needed.
7. Contrast check of the token pairs; target size ≥ 24 px at compact density.
8. Manual SR smoke test (VoiceOver + Safari, NVDA + Firefox). Only a human can mark this PASS.
9. Time spent and notable friction.

Decision rule [J]: choose #1 unless it fails axe/keyboard in a way #2 or #3 does not, or its route JS is more than ~1.5× the best alternative without an a11y gain. Choose #3 only if the owner, after seeing the three prototypes, values speed and a finished look above the extra dependency (dayjs) and the single-maintainer risk.

### Why the others were set aside (one line each)

- **Radix Primitives:** Base UI covers the same primitives from much of the same team, plus Combobox/Autocomplete/NumberField. The Radix docs changelog lags npm.
- **Ark UI:** good, with the best `translations` story, but no table and less SR-test evidence than RAC. It is the fallback if spike #1 fails.
- **Headless UI:** 16 components, 1 release in 12 months.
- **MUI Material:** Material look, Emotion runtime plus registry, and grid features behind a commercial licence. Its VPAT is only a draft with no AT testing.
- **MUI Joy:** on hold, never stable.
- **Ant Design:** heaviest (456 kB gz), strong brand, no a11y conformance statement, runtime CSS-in-JS, and App Router dot-subcomponent caveat.
- **Chakra v3:** Ark behaviour plus Emotion runtime.
- **IBM Carbon:** default-on install telemetry in CI/containers, a Sass pipeline, IBM look, and an upcoming v12. It is the best fallback if the owner wants an off-the-shelf enterprise look.
- **Fluent UI v9:** restrictive Segoe/icon assets licence, Griffel runtime with streaming caveats, i18n out of scope, Microsoft look.
- **React Spectrum S2:** colours cannot be customised, Turbopack is not supported, and Typekit font loading. Its behaviour is available through RAC anyway.
- **Primer React:** GitHub identity, hard-coded English aria-labels, DataTable still experimental.
- **Atlassian DS:** the licence agreement conflicts with Apache-2.0 on npm and restricts use to Atlassian add-ons. Excluded.
- **shadcn/ui as-is:** H-007 says not to adopt it directly. Its stack is evaluated as spike #2.
- **Styling — Panda v2, StyleX, vanilla-extract:** too new (Panda 2.0 is 19 days old), pre-1.0 (StyleX), or experimental Turbopack support (vanilla-extract). They give no advantage over CSS Modules for our size.
- **Styling — Emotion and runtime CSS-in-JS:** no release in a year, and Next.js still lists it as not fully supported.

### Questions for the owner (to resolve in ADR-021)

1. May library-provided, already-translated accessibility strings (React Aria's `zh-TW` dictionary) be used where a prop override is impossible, or must every string come from our catalog? (REQ-022 wording.)
2. Dark mode in P0: tokens ready but light only, or both themes?
3. Fonts: system fonts only (proposed), or a web font for Latin and/or Traditional Chinese (e.g. IBM Plex Sans TC or Noto Sans TC, both OFL) at a significant size cost?
4. Is a commercial component licence (e.g. MUI X Pro) or install-time vendor telemetry (Carbon) acceptable in principle? The current recommendation needs neither.

---

## 7. Items I could not verify (UNVERIFIED)

- Font licence terms for third-party use of Adobe Clean (S2) via Typekit.
- Whether Base UI's built-in English labels ("Notifications", "Increase", …) can all be overridden by props.
- Licence of React Aria's starter-kit code and the shadcn-registry install path for RAC examples.
- Panda CSS v2 breaking changes (the docs migration page returned 404; the main-branch CHANGELOG shows only 1.x).
- Atlassian components' i18n approach (not examined, since they are excluded on licence).
- Primer bundle size (bundlephobia gave an implausible min/gzip ratio) and whether Primer has any locale provider (only grep evidence).
- Carbon theme and token mechanics beyond the package contents (the Themes page was not fetched).
- Radix release notes for 1.7.0 and 1.8.x (not yet on the docs changelog).
- All bundle sizes are bundlephobia estimates; real numbers need `next build` in the spike.
- Maintainer and bus-factor judgements use npm publisher counts only, not commit statistics.

---

## 8. Method notes

- Versions, dates, licences, peer dependencies, dist-tags and publishers come from `https://registry.npmjs.org/<package>` (full documents, `time` field), fetched 2026-10-18.
- Tarball inspection: `npm` was not used. The tarball URL from registry metadata was downloaded with curl, extracted to a scratch directory, and read with grep and sed only. I checked `"use client"` presence, `client-only` imports, `postinstall` scripts, LICENSE files, locale dictionaries and hard-coded aria-labels.
- The Next.js facts come from the docs bundled in the already-installed `apps/web/node_modules/next@16.4.1/dist/docs` (read-only), which match the version in use.

---

## 9. Sources (all accessed 2026-10-18)

**Registries and measurements**
- S1. npm registry metadata, `https://registry.npmjs.org/<pkg>` (and `/<pkg>/<version>`) for every package named in sections 1–3. Tarballs from the `dist.tarball` URLs for: react-aria-components 1.21.1, react-aria 3.52.1, @internationalized/string 3.2.7 and 3.2.10, @radix-ui/react-dialog 1.1.23, @base-ui/react 1.8.0, @ark-ui/react 5.39.2, @headlessui/react 2.2.10, @mantine/core 9.6.3, @mui/material 9.4.0, antd 6.6.5, @chakra-ui/react 3.37.0, @carbon/react 1.117.0, @fluentui/react-dialog 9.18.5, @primer/react 38.40.1, @atlaskit/button 25.4.4, @react-spectrum/s2 1.7.1.
- S90. Bundlephobia API, `https://bundlephobia.com/api/size?package=<pkg@ver>` and `/api/exports-sizes?package=<pkg@ver>`.

**React Aria / React Spectrum**
- S10. React Spectrum releases (GitHub): https://github.com/adobe/react-spectrum/releases ; React Aria Toast: https://react-aria.adobe.com/Toast.md
- S11. React Aria "Quality" (accessibility, screen readers, locales): https://react-aria.adobe.com/quality.md
- S12. React Spectrum S2 getting started (Next.js tab, Turbopack notice): https://react-spectrum.adobe.com/getting-started.md
- S13. S2 Provider: https://react-spectrum.adobe.com/Provider.md
- S14. S2 Styling: https://react-spectrum.adobe.com/styling.md
- S15. React Aria framework setup (Next.js App Router, optimize-locales plugin): https://react-aria.adobe.com/frameworks.md (redirect target of https://react-spectrum.adobe.com/react-aria/ssr.html)
- S16. React Aria Table: https://react-aria.adobe.com/Table.md
- S17. React Aria Virtualizer: https://react-aria.adobe.com/Virtualizer.md ; Tree: https://react-aria.adobe.com/Tree.md ; Autocomplete: https://react-aria.adobe.com/Autocomplete.md
- S18. React Aria getting started and styling: https://react-aria.adobe.com/getting-started.md , https://react-aria.adobe.com/styling.md ; index: https://react-aria.adobe.com/llms.txt

**Radix / Base UI / Ark / Headless UI**
- S20. Radix Primitives releases: https://www.radix-ui.com/primitives/docs/overview/releases ; S20a. README: https://raw.githubusercontent.com/radix-ui/primitives/main/README.md
- S21. Radix accessibility: https://www.radix-ui.com/primitives/docs/overview/accessibility
- S22. Base UI docs index and release notes (v1.0.0 to v1.9.0): https://base-ui.com/llms.txt , https://base-ui.com/react/overview/releases/v1-9-0.md ; S22a. README: https://raw.githubusercontent.com/mui/base-ui/master/README.md
- S23. Base UI accessibility: https://base-ui.com/react/overview/accessibility.md
- S24. Ark UI README: https://raw.githubusercontent.com/chakra-ui/ark/main/README.md
- S25. Ark UI React docs and changelog: https://ark-ui.com/llms.txt , https://ark-ui.com/llms-react.txt
- S26. Headless UI home: https://headlessui.com/
- S27. Headless UI releases: https://github.com/tailwindlabs/headlessui/releases

**Mantine / MUI / antd / Chakra**
- S30. Mantine server components FAQ: https://mantine.dev/llms/q-server-components.md ; index: https://mantine.dev/llms.txt
- S30a. Mantine changelog v9.0.0 and all releases: https://mantine.dev/llms/changelog-9-0-0.md , https://mantine.dev/llms/changelog-all-releases.md
- S30b. Mantine dates getting started: https://mantine.dev/llms/dates-getting-started.md
- S30c. Mantine Next.js guide: https://mantine.dev/llms/guides-next.md
- S30d. Mantine Table: https://mantine.dev/llms/core-table.md ; Spotlight: https://mantine.dev/llms/x-spotlight.md ; color schemes: https://mantine.dev/llms/theming-color-schemes.md
- S31. Mantine accessibility FAQ: https://mantine.dev/llms/q-are-mantine-components-accessible.md
- S32. MUI X licensing: https://mui.com/x/introduction/licensing/
- S33. MUI blog "2026 and beyond" (2026-01-01): https://mui.com/blog/2026-and-beyond/
- S34. Material UI Next.js integration: https://mui.com/material-ui/integrations/nextjs.md
- S35. Material UI accessibility conformance report: https://mui.com/material-ui/getting-started/accessibility.md
- S36. Material UI localization: https://mui.com/material-ui/guides/localization.md
- S37. Ant Design use with Next.js: https://ant.design/docs/react/use-with-next.md ; SSR: https://ant.design/docs/react/server-side-rendering.md ; S37a. migration v6: https://ant.design/docs/react/migration-v6.md
- S38. Ant Design docs index and full docs/changelog: https://ant.design/llms.txt , https://ant.design/llms-full.txt
- S39. Chakra UI full docs (Next.js App guide, charts): https://chakra-ui.com/llms-full.txt

**Enterprise design systems**
- S40. Carbon accessibility overview: https://carbondesignsystem.com/guidelines/accessibility/overview/ ; React framework page: https://carbondesignsystem.com/developing/frameworks/react/
- S41. IBM Telemetry README (collection and opt-out): https://raw.githubusercontent.com/ibm-telemetry/telemetry-js/main/README.md
- S50. Fluent UI repo README (licences): https://raw.githubusercontent.com/microsoft/fluentui/master/README.md
- S51. Microsoft Fabric Assets License Agreement (Nov 2019), via https://aka.ms/fluentui-assets-license , resolving to https://static2.sharepointonline.com/files/fabric/assets/microsoft_fabric_assets_license_agreement_nov_2019.pdf
- S52. Griffel SSR guide: https://griffel.js.org/react/guides/ssr-usage/
- S53. Fluent Next.js appDir setup: https://storybooks.fluentui.dev/react//llms/concepts-developer-server-side-rendering-next-js-appdir-setup.txt ; build-time styles: https://storybooks.fluentui.dev/react//llms/concepts-developer-build-time-styles.txt
- S54. Fluent components accessibility overview: https://storybooks.fluentui.dev/react//llms/concepts-developer-accessibility-components-overview.txt
- S55. Fluent docs index and DataGrid: https://storybooks.fluentui.dev/react/llms.txt , https://storybooks.fluentui.dev/react//llms/components-datagrid.txt
- S60. Primer site and getting started: https://primer.style/ , https://primer.style/product/getting-started/react/ ; README: https://raw.githubusercontent.com/primer/react/main/README.md
- S61. Primer accessibility: https://primer.style/accessibility
- S70. Atlassian Design System license agreement: https://atlassian.design/license
- S71. Atlassian accessibility foundations: https://atlassian.design/foundations/accessibility

**Styling, Next.js, shadcn**
- S80. Next.js 16.4.1 bundled docs, Turbopack reference (Babel row, v16 default bundler): `apps/web/node_modules/next/dist/docs/01-app/03-api-reference/08-turbopack.md`
- S80a. shadcn/ui docs index: https://ui.shadcn.com/llms.txt ; S80b. shadcn/ui changelog: https://ui.shadcn.com/docs/changelog
- S81. Next.js 16.4.1 bundled docs, CSS-in-JS guide: `apps/web/node_modules/next/dist/docs/01-app/02-guides/css-in-js.md`
- S82. Next.js 16.4.1 bundled docs, CSS getting started (Tailwind, CSS Modules, ordering): `apps/web/node_modules/next/dist/docs/01-app/01-getting-started/11-css.md`
- S83. vanilla-extract Next.js integration: https://vanilla-extract.style/documentation/integrations/next/
- S84. Tailwind CSS theme variables: https://tailwindcss.com/docs/theme
- S85. Tailwind CSS compatibility: https://tailwindcss.com/docs/compatibility
- S86. StyleX Next.js installation: https://stylexjs.com/docs/learn/installation/nextjs/ ; general: https://stylexjs.com/docs/learn/installation/
- S87. Panda CSS Next.js install page: https://panda-css.com/docs/installation/nextjs ; changelog: https://raw.githubusercontent.com/chakra-ui/panda/main/CHANGELOG.md

**CSS, WCAG, companion libraries**
- S100. MDN `word-break`: https://developer.mozilla.org/en-US/docs/Web/CSS/word-break
- S101. MDN `text-autospace`: https://developer.mozilla.org/en-US/docs/Web/CSS/text-autospace
- S102. W3C "What's New in WCAG 2.2": https://www.w3.org/WAI/standards-guidelines/wcag/new-in-22/
- S103. W3C Understanding SC 2.5.8 Target Size (Minimum): https://www.w3.org/WAI/WCAG22/Understanding/target-size-minimum.html
- S110. React Flow accessibility: https://reactflow.dev/learn/advanced-use/accessibility ; index: https://reactflow.dev/llms.txt
- S111. React Flow theming: https://reactflow.dev/learn/customization/theming
- S112. PlayCanvas React user manual: https://developer.playcanvas.com/user-manual/react/
- S120. Recharts v3.0.0 release notes: https://github.com/recharts/recharts/releases/tag/v3.0.0
- S121. ECharts ARIA handbook: https://echarts.apache.org/handbook/en/best-practices/aria/

**Repository files read (read-only)**
- /home/user/Geminus/AGENTS.md; /home/user/Geminus/planning/tasks.json (T-045); /home/user/Geminus/docs/PRD.md (REQ-022, REQ-023); /home/user/Geminus/docs/TEST_PLAN.md (TEST-033); /home/user/Geminus/docs/DECISIONS_AND_TBD.md (H-007); /home/user/Geminus/docs/adr/ADR-007, ADR-008, ADR-016, ADR-017, ADR-018, ADR-020; /home/user/Geminus/apps/web/package.json, next.config.ts, app/layout.tsx.
