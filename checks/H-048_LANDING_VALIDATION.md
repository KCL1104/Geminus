# H-048 validation: a landing page for judges, with real product screens

**Date: 2026-10-30 | Status: delivered by the slice's agent, not reviewed yet (H-048 asks for one independent review) | Basis: owner decision H-048, DESIGN.md (H-041 world, H-027 simplicity, H-042 rail, H-043 Meta made visible), PRODUCT.md, README.md "How Geminus uses Meta AI" | Requirements: REQ-022 (catalog text, en and zh-Hant), REQ-023 (WCAG 2.2 AA, 390 px)**

## 1. What the owner asked and chose

The owner asked 「現在有 landing page 嗎？是不是要有個？」 ("is there a landing page now? should there be one?") and chose 「要，現在平行做 (Recommended)」 and 「真實產品畫面 (Recommended)」: a one-page landing at `/`, English first, for the hackathon's judges, whose main visuals are real screens of the product, with a sign-in call to action, a signed-in reader offered their projects, and no credentials on the page.

## 2. What changed

| Area | Change | Files |
|---|---|---|
| The page | `/` was a title and "Go to projects". It is now the landing: the promise and one paragraph of how, the one primary action, the results page itself; the loop as five numbered steps, each naming who acts (pills: the person, a Meta model, the Geminus server, the engine) beside the screen it happens on; which Meta model does what; the industries the templates cover; why it is safe to try; a closing line that it is a hackathon build on synthetic data. `generateMetadata` adds the page's description. | `apps/web/app/page.tsx`, `apps/web/features/landing/landing.tsx`, `landing.module.css` |
| The action | A client component reads the auth context the signed-in bootstrap provides from the server's session hint (`app/layout.tsx` `hasSession`, the BFF): no session, "Sign in to try it" to `/auth/login?return_to=%2Fprojects` and the line "Judges: the demo account is in the hackathon submission."; with a session (loading or ready), "Open your projects" to `/projects`; a session that ended (`/me` 401) is signed out again. No second call for the session is made, so the layout still asks the BFF once per request (`layout-session.test.tsx` unchanged). | `apps/web/features/landing/cta.tsx` |
| The screens | Six pictures: the results page (hero), the model page with a request to the AI written in, a scenario proposal with Geminus's risk check and the decision, and the Marey chart, from the gallery's review pages in both themes; Muse Spark's explanation and the scenario it proposed from its own suggested next experiment, cropped from the live demo's screenshots of 2026-10-29 (synthetic data, light theme only). WebP at 1x and 2x, 1.26 MB in all; a 1x visitor loads about 285 KB, a 2x one about 640 KB. Both themes' pictures are in the markup and CSS shows the one the tokens show, so a theme switched in the menu applies at once; all are `loading="lazy"` with width and height, so the hidden theme's are never fetched (checked in the browser). Served from `public/` (CSP `img-src 'self'`). | `apps/web/features/landing/shot.tsx`, `shots.ts` (generated), `apps/web/public/landing/*.webp`, `checks/evidence/2026-10-30-h048-landing/live/*.png` |
| Re-shooting | `apps/web-e2e/scripts/landing-shots.mjs` makes every picture again: it opens the gallery's editor, proposal and results pages (synthetic sample line, no AI called), pans the canvas to AOI, its touch-up loop and ICT, types the request and chooses "A scenario to simulate", crops each to the product's own view at 2x in light and dark, and encodes WebP in the browser (no image tool needed); it crops the two live screens from the committed source PNGs, replaces `public/landing/` and writes `features/landing/shots.ts`. `--start` starts the built app itself on :3210; `--base URL` uses a running one. | `apps/web-e2e/scripts/landing-shots.mjs`, `apps/web-e2e/README.md` |
| Image | The standalone server does not copy `public/`; the web image now does, or the pictures would 404 in the deployment. | `apps/web/Dockerfile` |
| Text | 61 new `landing.*` keys in both catalogs (no key changed or removed; `home.title` and `home.toProjects` stay, now unused). The Meta model names reuse `ai.model.*` and `ai.provider.meta`. | `apps/web/messages/en.ts`, `zh-Hant.ts` |
| Design record | DESIGN.md's "Simplifications in the app" says how the landing uses the system (no new token, size or shadow); the impeccable surface brief records the page's audience, proof and open items. | `DESIGN.md`, `.impeccable/surfaces/apps-web-app-page-tsx.md` |

The rail stays on `/` (H-042, `layout.test.tsx` and `shell-h042.test.tsx` unchanged): signed out its foot is the settings, which a first-time reader needs for the language and the theme, and its brand is the way to the projects.

## 3. Requirement mapping

| H-048 / brief item | Where | Checked by |
|---|---|---|
| Hero: one sentence of value, one line of how, "Sign in to try it"; signed in "Open your projects" | `landing.tsx` hero, `cta.tsx` | `tests/landing.test.tsx` (signed out, no bootstrap, loading, ready, the server's route), `landing.spec.ts` (signed out; signed in through the BFF, clicking through to the projects) |
| The loop on real screens: describe or photograph → Muse Spark (SAM 3.1 for a photo) proposes → the server validates and computes risk → a person approves → simulate and compare (Marey chart) → the AI explains and suggests the next experiment, which it can propose as a scenario; web research when asked | Five steps, four screens; research in the Meta section (no screen, the feature is built in parallel) | `landing.test.tsx` (steps in order, who acts in each), `landing.spec.ts` (pictures loaded, theme, origin) |
| Which Meta model does each step, exactly | Actor pills per step; the three-row list (Muse Spark 1.3, SAM 3.1, Meta's web search) and the live proposal "Written by Muse Spark 1.3 (Meta)" | `landing.test.tsx`; copy checked against README.md "How Geminus uses Meta AI" and H-043, H-046 |
| Industries: electronics assembly and test line, shoe factory, food plant, corrugated carton plant, warehouse | "Start from a template" | `landing.test.tsx` |
| Why it is safe: the AI only proposes; the server validates and computes risk; a person approves; versions immutable; demo data synthetic | "Why it is safe to try" | `landing.test.tsx` |
| No credentials anywhere | No account, address, password or key in the text, the alt texts or the catalog's landing messages | `landing.test.tsx`, `landing.spec.ts` (both languages, signed out and in) |
| English first, zh-Hant translation; only add keys | 61 keys added to each catalog | `i18n.test.ts` (same keys, placeholders, everything translated but names), `layout.test.tsx`, `landing.test.tsx` (zh-Hant) |
| Accessible: landmarks, headings, alt text, focus, contrast | Sections labelled by their headings; h1, then h2 per section and h3 per step; every picture's alt says what the screen shows; the action is the first stop after the rail | `landing.test.tsx` (axe in happy-dom), `a11y.spec.ts` (axe with contrast, both themes, both languages, 390 and 1280), `keyboard.spec.ts` |
| 390 px, no horizontal overflow | One column under 720 px | `a11y.spec.ts` (page-level scroll ≤ width on `/`); measured 0 px at 390 and 1280 in both themes |
| Fast: sized, lazy below the fold | `width`/`height`, `srcset` 1x/2x, `sizes`, `loading="lazy"`, the hidden theme never fetched | `landing.test.tsx` (files match the sizes the page states, nothing stray in `public/landing/`), `landing.spec.ts` |
| One primary action, no two controls for one action (H-027) | Signed out nothing repeats (the H-028 pair is gone there); signed in "Open your projects" goes where the rail's brand goes, the pair H-028 kept | `simplicity.test.tsx` (signed out and signed in), `simplicity.spec.ts` (`askedAbout` for `/` is now empty), `landing.spec.ts` |
| Design system unchanged | Tokens only, five sizes, three shadows, no gradient or animation | `design-rules.test.ts`, `tokens.test.ts`; impeccable detector: no finding |

## 4. Tests changed or added

- New `apps/web/tests/landing.test.tsx` (10 tests): the action in each auth state and on the server's route (en and zh-Hant), the content in order in both languages with axe, no credentials, the pictures (each WebP's real size read from its header against `shots.ts`, 1x and 2x per theme, nothing else in `public/landing/`; same-origin `src` and `srcset`, alt, lazy, sizes).
- `apps/web/tests/layout.test.tsx`: the shell's check of the start page now looks for the landing's title and sign-in instead of "Go to projects" (the rail checks are unchanged).
- `apps/web/tests/simplicity.test.tsx`: `/` signed out repeats nothing now; a new test per language checks `/` with a session keeps only the pair H-028 kept (`link to /projects`), one primary, nothing disabled.
- New `apps/web-e2e/tests/landing.spec.ts` (8 tests): pictures per theme and language, the theme switch, signed out, signed in (through the BFF and the test provider), no credentials.
- `apps/web-e2e/tests/keyboard.spec.ts`: after the rail's settings the next stop is "Sign in to try it"; after switching to zh-Hant the link "登入試用" is on screen.
- `apps/web-e2e/tests/simplicity.spec.ts`: `askedAbout` no longer lists `/` (signed out, it repeats nothing).
- `apps/web-e2e/tests/strings.spec.ts`: a new test per language: every string on `/`, the settings menu open too, is the catalog's (or an accepted React Aria string).
- `apps/web-e2e/tests/screenshots.spec.ts` and `helpers.ts`: on `/` every visible picture is loaded and decoded before the full-page shot (`loadPictures`), so the baselines do not depend on lazy loading. The baselines themselves are not touched.

No assertion was weakened and no test skipped.

## 5. Commands and results

Run in this worktree with Node 24.21.0 (`apps/web/.nvmrc`):

| Command | Result |
|---|---|
| `npm ci` in `apps/web` and `apps/web-e2e` | exit 0 |
| `make frontend-test` (lint, typecheck, 54 files / 1154 unit tests) | exit 0. The first run failed (exit 2) on `results-preview.test.tsx`: no app module may import JSON, and the picture list was `shots.json`; it is now the generated module `shots.ts`. |
| `node /home/kuoba123/.claude/skills/impeccable/scripts/detect.mjs --json` on the four landing files | `[]` (no finding) |
| `node apps/web-e2e/scripts/landing-shots.mjs --start` | exit 0; 18 WebP files and `shots.ts` written |
| `make e2e-test` (ports 3100–3103 checked free first) | exit 0: 562 passed, 72 skipped (all 72 are the screenshot comparisons, NOT RUN without `E2E_SCREENSHOTS=1`), 17.5 min. Includes the 8 tests of `landing.spec.ts` and `/` in a11y, keyboard, simplicity, security headers and theme. |
| `npx playwright test tests/strings.spec.ts tests/landing.spec.ts tests/keyboard.spec.ts` (in `apps/web-e2e`, a fresh build) | exit 0: 24 passed. Run after the full run for the landing's new strings test (added to `strings.spec.ts` once the full run had started) and a zh-Hant wording fix. |
| `make frontend-test`, again after the last change | exit 0, 1154 tests |
| `PATH=/home/kuoba123/Geminus/.venv/bin:$PATH make spec-check` | exit 0 (`docs-check.json` rewritten: one more Markdown file) |

The logs are in `checks/evidence/2026-10-30-h048-landing/` (`frontend-test.log`, `e2e-test.log`, `e2e-targeted.log`, `spec-check.log`).

## 6. NOT RUN

- Screenshot comparisons (`E2E_SCREENSHOTS=1`, CI's pinned container only). The `home-*` baselines change: `home-{en,zh-Hant}-{390,1280}.png` and their `-dark` versions (8 files). The coordinator re-shoots them; this slice did not touch `apps/web-e2e/tests/__screenshots__/`.
- `make backend-test`, `make backend-race`, `make integration-test`, `make ai-eval`: no Go, SQL, contract or prompt change.
- `make build-containers`: no Docker daemon here; the Dockerfile's new `COPY --from=build /app/public ./public` was not built. `make spec-check`'s deploy check reads the Dockerfile statically.
- Live: nothing deployed, no live AI call. The two live crops come from screenshots taken on 2026-10-29 (`checks/evidence/2026-10-30-h048-landing/live/`).
- The impeccable finish review by a separate reviewer agent: not available in this harness; H-048's independent review is the coordinator's.

## 7. Limits and open questions

- **Live screens only in light.** Muse Spark's explanation and the scenario it proposed exist only on the live demo, which needs the judge account; this slice used the screenshots already taken on 2026-10-29 and did not sign in. In the dark theme they show as light pictures. Re-shooting them in dark needs a live sign-in by the coordinator.
- **The gallery's proposal is a sample.** The proposal screen in step 3 comes from the gallery's sample (written for review, not by a model), so it has no "Written by" pill; the live proposal in the Meta section is Muse Spark's own and shows it. The page says every screen shows synthetic data; no screen claims a result the product did not compute (the results and the Marey chart are the engine's output on the synthetic sample line, `features/results/fixtures`).
- **Screens stay English in zh-Hant.** The pictures were captured in English; the zh-Hant page translates the text, captions and alt texts. Capturing zh-Hant pictures too would double the files.
- **Built in parallel.** The web-search row ("When you ask Muse Spark to research first…", H-046 (2)) and the four new industries (H-045) describe work other sessions are doing. If either does not ship before the submission, its row or names must be removed from the catalogs (`landing.meta.search`, `landing.actor.search`, `landing.industry.*`).
- **The H-028 pair with a session.** Signed in, "Open your projects" goes where the rail's brand goes, as "Go to projects" did. Signed out the pair is gone.
- **Phone.** At 390 px the wide screens are miniatures (the alt text and captions carry them); the product is desktop first (PRODUCT.md).
- **Unused keys.** `home.title` and `home.toProjects` are no longer used; the brief allowed only adding keys, so they stay.
