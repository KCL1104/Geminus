# impeccable audit: apps/web (2026-10-19)

Technical audit (impeccable 4.6.0 `audit`, engine 0.1.15), run by an agent on a production build of `apps/web` (Next 16.4.1) against the test-only OIDC provider and fake API from `apps/web-e2e`. Report only; nothing was fixed in this run. Scratch evidence (screenshots, axe JSON, detector JSON) stayed in the session scratchpad and is not committed.

## Score

| # | Dimension | Score | Key finding |
|---|---|---|---|
| 1 | Accessibility | 3 | axe 0 A/AA violations in 28 page states; virtualized row focus invisible; virtualized table clips at 200% text; the language select changes the page on an arrow key and drops focus |
| 2 | Performance | 3 | CLS 0; LCP 68–112 ms locally; 147–248 KiB gzipped JS per page; React Aria ships every locale (plugin planned, ADR-021) |
| 3 | Theming | 3 | Semantic tokens used throughout and tested (`tests/tokens.test.ts`); no dark theme yet (H-011, H-012); a few rgb() literals |
| 4 | Responsive | 3 | No page-level horizontal scroll at 320/390/640 px at 100% text; 32 px controls on phones; unbreakable `<code>` overflows at 390 px with 200% text; no scroll cue on tables |
| 5 | Implementation integrity | 2 | Signed-in users are told the session is missing; no sign-in action in the UI; side-tab callouts |
| **Total** | | **14/20** | **Good** |

## Findings

- **P0, signed-in path is a dead end.** `app/layout.tsx` mounts no session provider; `RequireSession` (`app/_components/states.tsx`) falls back to `ProblemView`, so after a real BFF sign-in `/projects` still says "Sign in required. Your session is missing or has expired."; no page links to `/auth/login` except `/auth/error`; the signed-out notice is a danger `role="alert"`. Belongs to T-043's UI (D-045 `/v1/me`, approved under H-010).
- **P1, virtualized row focus invisible** (`ui/table.tsx` Virtualizer, `ui/table.module.css`): the focused row box is 0 px tall, so its ring does not show (WCAG 2.4.7).
- **P1, virtualized table clips at 200% text** (`rowHeight = 32` in px, cells `overflow:hidden`; WCAG 1.4.4).
- **P1, language select** (`app/_components/locale-switcher.tsx`): `key={locale}` remount plus `router.refresh()` on change; ArrowDown switches the language and focus lands on `<body>` (WCAG 3.2.2, 2.4.3).
- **P2:** every page titled "Geminus"; the model route has no h1 outside its loaded state; unbreakable `<code>` overflows on phones; 32 px touch targets without `(pointer: coarse)` sizing; narrow tables cut columns with no scroll cue; no dark theme; 4 px side-tab borders on `Callout` and `StatusRegion` (detector `side-tab`, true positive: `ui/callout.module.css:5`, `ui/status.module.css:4`).
- **P3:** `CrashNotice` uses a raw `<button>`; `global-error.tsx` loads no tokens; native `<select>` beside the React Aria `Select`; double focus ring on the checkbox; sortable column headers without `onSortChange`; global 0.01 ms reduced-motion rule; React Aria strings for all locales; task IDs in UI copy; no `aria-current` in the nav.

## Detector

- Source scan of `apps/web/app` and `apps/web/ui`: `side-tab` ×2.
- URL scan at 1280×800 and 390×844 (en): `side-tab` ×4, all on `/ui-gallery`; other routes 0. The URL scan needed a Chromium wrapper with `--no-sandbox` because the container runs as root.

## Positive

axe-core 4.13 (WCAG 2.0–2.2 A/AA): 0 violations in 28 states, also under forced colours and a dark preference; skip link, dialog focus trap and return, `Select` and table keyboard paths work; every token text pair passes AA and is tested; all copy comes from the catalog.

## Not checked

Signed-in pages with data (the fake API returns no projects and the UI mounts no session), `error.tsx` and `global-error.tsx` rendered (code reading only), screen readers, physical devices, Firefox and WebKit, projector legibility.
