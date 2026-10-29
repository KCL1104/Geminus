# H-048 validation: a landing page for judges, with real product screens

**Date: 2026-10-30 | Status: delivered; independent review round 1 (no High, 3 Medium, 10 Low) fixed or answered in §8 | Basis: owner decision H-048, DESIGN.md (H-041 world, H-027 simplicity, H-042 rail, H-043 Meta made visible), PRODUCT.md, README.md "How Geminus uses Meta AI" | Requirements: REQ-022 (catalog text, en and zh-Hant), REQ-023 (WCAG 2.2 AA, 390 px)**

## 1. What the owner asked and chose

The owner asked 「現在有 landing page 嗎？是不是要有個？」 ("is there a landing page now? should there be one?") and chose 「要，現在平行做 (Recommended)」 and 「真實產品畫面 (Recommended)」: a one-page landing at `/`, English first, for the hackathon's judges, whose main visuals are real screens of the product, with a sign-in call to action, a signed-in reader offered their projects, and no credentials on the page.

## 2. What changed

The state after review round 1 (§8 lists what round 1 changed).

| Area | Change | Files |
|---|---|---|
| The page | `/` was a title and "Go to projects". It is now the landing: the product's mark and name, the promise as the h1 and one paragraph of how, the one primary action, the results' key metrics; the loop as five numbered steps, each naming who acts (pills: the person, a Meta model, the Geminus server, the engine) beside the screen it happens on; which Meta model does what (Muse Spark 1.3, SAM 3.1, and Meta's web search as a tool of the Meta Model API that Muse Spark uses); the industries the templates cover; why it is safe to try; a closing line that it is a hackathon build on synthetic data. `generateMetadata` adds the page's description. | `apps/web/app/page.tsx`, `apps/web/features/landing/landing.tsx`, `landing.module.css` |
| The action | A client component reads the auth context the signed-in bootstrap provides from the server's session hint (`app/layout.tsx` `hasSession`, the BFF): no session, "Sign in to try it" to `/auth/login?return_to=%2Fprojects` and the line "Judges: the demo account is in the hackathon submission."; with a session (loading or ready), "Open your projects" to `/projects`; a session that ended (`/me` 401) is signed out again. No second call for the session is made, so the layout still asks the BFF once per request (`layout-session.test.tsx` unchanged). | `apps/web/features/landing/cta.tsx` |
| The screens | Five pictures. From the gallery's review pages, in both themes: the results' Key metrics card (first view), the model page with a request to the AI written in, and the Marey chart. From the live demo's screenshots of 2026-10-29 (synthetic data, light theme only, dimmed in the dark theme): Muse Spark's own scenario proposal ("Written by Muse Spark 1.3 (Meta)", Geminus's risk check, the decision; step 3) and Muse Spark's explanation (step 5). Each is framed as a screenshot: a window on the Overlay shadow whose title bar is the caption. WebP at 1x and 2x, 14 files, 0.76 MB in all; a 1x visitor loads about 215 KB, a 2x one about 350 KB. Both themes' pictures are in the markup and CSS shows the one the tokens show, so a theme switched in the menu applies at once; all are `loading="lazy"` with width and height, so the hidden theme's are never fetched (checked in the browser). Served from `public/` (CSP `img-src 'self'`). | `apps/web/features/landing/shot.tsx`, `shots.ts` (generated), `apps/web/public/landing/*.webp`, `checks/evidence/2026-10-30-h048-landing/live/*.png` |
| Re-shooting | `apps/web-e2e/scripts/landing-shots.mjs` makes every picture again: it opens the gallery's editor and results pages (synthetic sample line, no AI called), pans the canvas to AOI, its touch-up loop and ICT, types the request and chooses "A scenario to simulate", crops the results at 1040 px (where the run settings panel moves below the card) from the Key metrics heading to the row before "Lead time, mean of replication means", crops each to the product's own view at 2x in light and dark, and encodes WebP in the browser (no image tool needed); it crops the two live screens from the committed source PNGs, replaces `public/landing/` and writes `features/landing/shots.ts`. `--start` starts the built app itself on :3210; `--base URL` uses a running one. | `apps/web-e2e/scripts/landing-shots.mjs`, `apps/web-e2e/README.md` |
| Image | The standalone server does not copy `public/`; the web image now does, or the pictures would 404 in the deployment. | `apps/web/Dockerfile` |
| Text | 60 new `landing.*` keys in both catalogs (no existing key changed or removed; `home.title` and `home.toProjects` stay, now unused). The Meta model names reuse `ai.model.*` and `ai.provider.meta`; the brand reuses `app.name`. | `apps/web/messages/en.ts`, `zh-Hant.ts` |
| Design record | DESIGN.md's "Simplifications in the app" says how the landing uses the system (no new token, size or shadow); the impeccable surface brief records the page's audience, proof and open items. | `DESIGN.md`, `.impeccable/surfaces/apps-web-app-page-tsx.md` |

The rail stays on `/` (H-042, `layout.test.tsx` and `shell-h042.test.tsx` unchanged): signed out its foot is the settings, which a first-time reader needs for the language and the theme, and its brand is the way to the projects.

## 3. Requirement mapping

| H-048 / brief item | Where | Checked by |
|---|---|---|
| Hero: one sentence of value, one line of how, "Sign in to try it"; signed in "Open your projects" | `landing.tsx` hero, `cta.tsx` | `tests/landing.test.tsx` (signed out, no bootstrap, loading, ready, the server's route), `landing.spec.ts` (signed out; signed in through the BFF, clicking through to the projects) |
| The loop on real screens: describe or photograph → Muse Spark (SAM 3.1 for a photo) proposes → the server validates and computes risk → a person approves → simulate and compare (Marey chart) → the AI explains and suggests the next experiment, which it can propose as a scenario; web research when asked | Five steps, four screens; research in the Meta section (no screen, the feature is built in parallel) | `landing.test.tsx` (steps in order, who acts in each, step 3's picture is Muse Spark's own proposal), `landing.spec.ts` (pictures loaded, theme, origin) |
| Which Meta model does each step, exactly | Actor pills per step; the list of the two models and the one tool, and step 3's live proposal "Written by Muse Spark 1.3 (Meta)" | `landing.test.tsx`; copy checked against README.md "How Geminus uses Meta AI", H-043 and H-046, and the research feature's own wording (§8 M1) |
| Industries the templates cover | "Start from a template": the electronics assembly and test line, and the four of H-045 under the names the parallel work uses now (sneaker factory, bread bakery, corrugated box plant, distribution center) | `landing.test.tsx` |
| Why it is safe: the AI only proposes; the server validates and computes risk; a person approves; versions immutable; demo data synthetic | "Why it is safe to try" | `landing.test.tsx` |
| No credentials anywhere | No account, address, password or key in the text, the alt texts or the catalog's landing messages | `landing.test.tsx`, `landing.spec.ts` (both languages, signed out and in) |
| English first, zh-Hant translation; only add keys | 60 keys added to each catalog | `i18n.test.ts` (same keys, placeholders, everything translated but names), `layout.test.tsx`, `landing.test.tsx` (zh-Hant), `strings.spec.ts` (every string on `/` is the catalog's) |
| Accessible: landmarks, headings, alt text, focus, contrast | Sections labelled by their headings; h1, then h2 per section and h3 per step; every picture's alt says what the screen shows; the action is the first stop after the rail | `landing.test.tsx` (axe in happy-dom), `a11y.spec.ts` (axe with contrast, both themes, both languages, 390 and 1280), `keyboard.spec.ts` |
| 390 px, no horizontal overflow | One column under 720 px | `a11y.spec.ts` (page-level scroll ≤ width on `/`); measured 0 px at 390 and 1280 in both themes |
| Fast: sized, lazy below the fold | `width`/`height`, `srcset` 1x/2x, `sizes`, `loading="lazy"`, the hidden theme never fetched; the first view's picture is lazy too (§8 L5) | `landing.test.tsx` (files match the sizes the page states, nothing stray in `public/landing/`, no preload in the markup), `landing.spec.ts` |
| One primary action, no two controls for one action (H-027) | Signed out nothing repeats (the H-028 pair is gone there); signed in "Open your projects" goes where the rail's brand goes, the pair H-028 kept | `simplicity.test.tsx` (signed out and signed in), `simplicity.spec.ts` (`askedAbout` for `/` is now empty), `landing.spec.ts` |
| Design system unchanged | Tokens only, five sizes, three shadows, no gradient or animation | `design-rules.test.ts`, `tokens.test.ts`; impeccable detector: no finding |

## 4. Tests changed or added

- New `apps/web/tests/landing.test.tsx` (11 tests): the action in each auth state and on the server's route (en and zh-Hant); the content in order in both languages with axe (the brand above the h1, the Meta list's two models and one tool, step 3's picture and caption); no credentials; the pictures (each WebP's real size read from its header against `shots.ts`, 1x and 2x per theme, nothing else in `public/landing/`; same-origin `src` and `srcset`, alt, lazy, sizes, the caption bar first in each frame, the light-only live screens marked); no preload of a picture in the markup.
- `apps/web/tests/layout.test.tsx`: the shell's check of the start page now looks for the landing's title and sign-in instead of "Go to projects" (the rail checks are unchanged).
- `apps/web/tests/simplicity.test.tsx`: `/` signed out repeats nothing now; a new test per language checks `/` with a session keeps only the pair H-028 kept (`link to /projects`), one primary, nothing disabled.
- New `apps/web-e2e/tests/landing.spec.ts` (9 tests): pictures per theme and language, the theme switch, a remembered dark theme over a light system (no light picture fetched, the live screens dimmed, the others not), signed out, signed in (through the BFF and the test provider), no credentials.
- `apps/web-e2e/tests/keyboard.spec.ts`: after the rail's settings the next stop is "Sign in to try it"; after switching to zh-Hant the link "登入試用" is on screen.
- `apps/web-e2e/tests/simplicity.spec.ts`: `askedAbout` no longer lists `/` (signed out, it repeats nothing).
- `apps/web-e2e/tests/strings.spec.ts`: a new test per language: every string on `/`, the settings menu open too, is the catalog's (or an accepted React Aria string).
- `apps/web-e2e/tests/screenshots.spec.ts` and `helpers.ts`: on `/` every visible picture is loaded and decoded before the full-page shot (`loadPictures`), so the baselines do not depend on lazy loading. The baselines themselves are not touched.

No assertion was weakened and no test skipped.

## 5. Commands and results

Run in this worktree with Node 24.21.0 (`apps/web/.nvmrc`).

Round 0 (commit `670d999`):

| Command | Result |
|---|---|
| `npm ci` in `apps/web` and `apps/web-e2e` | exit 0 |
| `make frontend-test` (lint, typecheck, 54 files / 1154 unit tests) | exit 0. The first run failed (exit 2) on `results-preview.test.tsx`: no app module may import JSON, and the picture list was `shots.json`; it is now the generated module `shots.ts`. |
| `node /home/kuoba123/.claude/skills/impeccable/scripts/detect.mjs --json` on the four landing files | `[]` (no finding) |
| `node apps/web-e2e/scripts/landing-shots.mjs --start` | exit 0; 18 WebP files and `shots.ts` written |
| `make e2e-test` (ports 3100–3103 checked free first) | exit 0: 562 passed, 72 skipped (all 72 are the screenshot comparisons, NOT RUN without `E2E_SCREENSHOTS=1`), 17.5 min. Includes the 8 tests of `landing.spec.ts` and `/` in a11y, keyboard, simplicity, security headers and theme. |
| `npx playwright test tests/strings.spec.ts tests/landing.spec.ts tests/keyboard.spec.ts` (in `apps/web-e2e`, a fresh build) | exit 0: 24 passed (the landing's new strings test and a zh-Hant wording fix, after the full run) |
| `PATH=/home/kuoba123/Geminus/.venv/bin:$PATH make spec-check` | exit 0 (`docs-check.json` rewritten: one more Markdown file) |

Round 1 (review fixes):

| Command | Result |
|---|---|
| `node apps/web-e2e/scripts/landing-shots.mjs --start` | exit 0; 14 WebP files and `shots.ts` written (the results recropped; the gallery's proposal no longer made) |
| `make frontend-test` | exit 0, 54 files / 1155 tests. Two runs before it failed: the first (exit 2) because a figure attribute named `data-themes` made `layout.test.tsx`'s "no data-theme on the system's theme" checks match, and an eager first picture made React put a preload link ahead of the CSP nonce meta that `layout.test.tsx` pins (renamed to `data-live`; the eager picture dropped, §8 L5); the second (exit 2) on a lint warning in the new test (`<head>` element), rewritten. |
| `npx playwright test` on `landing`, `keyboard`, `strings`, `theme`, `security-headers`, and `/` in `a11y` and `simplicity` (ports 3100–3103 checked free first; a fresh build) | exit 0: 51 passed (a11y on `/` in both themes, languages and widths; simplicity on `/` in both languages and widths) |
| `PATH=/home/kuoba123/Geminus/.venv/bin:$PATH make spec-check` | exit 0 |

The full `make e2e-test` was not run again in round 1; the specs above are the ones that open `/`. The logs are in `checks/evidence/2026-10-30-h048-landing/` (`frontend-test.log`, `e2e-test.log`, `e2e-targeted.log` and `spec-check.log` from round 0; `frontend-test-r1.log`, `e2e-r1.log` and `spec-check-r1.log` from round 1).

## 6. NOT RUN

- Screenshot comparisons (`E2E_SCREENSHOTS=1`, CI's pinned container only). The `home-*` baselines change: `home-{en,zh-Hant}-{390,1280}.png` and their `-dark` versions (8 files). The coordinator re-shoots them; this slice did not touch `apps/web-e2e/tests/__screenshots__/`.
- `make backend-test`, `make backend-race`, `make integration-test`, `make ai-eval`: no Go, SQL, contract or prompt change.
- `make build-containers`: no Docker daemon here; the Dockerfile's new `COPY --from=build /app/public ./public` was not built. `make spec-check`'s deploy check reads the Dockerfile statically.
- Live: nothing deployed, no live AI call. The two live crops come from screenshots taken on 2026-10-29 (`checks/evidence/2026-10-30-h048-landing/live/`).
- The full `make e2e-test` after round 1 (see §5).

## 7. Limits and open questions

- **Live screens only in light.** Muse Spark's proposal and explanation exist only on the live demo, which needs the judge account; this slice used the screenshots already taken on 2026-10-29 and did not sign in. In the dark theme they are dimmed light pictures. Re-shooting them in dark needs a live sign-in by the coordinator.
- **Dated live screens.** The live proposal (FCT testers 3 → 4) and explanation (a third ICT tester) are from before H-047's what-ifs; they are dated in their captions. The step 1 example ("a third ICT tester") stays, because the gallery's sample line, which the canvas picture shows, has no FCT step.
- **Screens stay English in zh-Hant.** The pictures were captured in English; the zh-Hant page translates the text, captions and alt texts. Capturing zh-Hant pictures too would double the files.
- **Built in parallel.** The web-search row (H-046 (2)) and the four new industries (H-045) describe work other sessions are doing; the industry names follow the parallel work's current names and are to be reconciled with its catalog at merge. If either does not ship before the submission, its row or names must be removed (`landing.meta.search`, `landing.meta.searchName`, `landing.meta.tool`, `landing.industry.*`).
- **The H-028 pair with a session.** Signed in, "Open your projects" goes where the rail's brand goes, as "Go to projects" did. Signed out the pair is gone.
- **Phone.** At 390 px the wide screens are miniatures (the alt text and captions carry them); the product is desktop first (PRODUCT.md).
- **Unused keys.** `home.title` and `home.toProjects` are no longer used; the brief allowed only adding keys, so they stay.

## 8. Review round 1

Independent review of `670d999`: no High, 3 Medium, 10 Low.

| Finding | Outcome |
|---|---|
| M1: the research row overclaimed ("searches the web and reads pages… the sources it opened") and listed web search as a Meta model | Fixed. `landing.meta.search` is the feature's own claim: "When you turn research on, Muse Spark uses Meta's web search tool before it proposes; the proposal lists the pages it asked to open. Only your words are searched, never a photo." Its term is "Web search", labelled "Meta Model API tool" (`landing.meta.searchName`, `landing.meta.tool`), not "Meta"; the section's intro says two Meta models and one of the API's tools. Unit test checks the three terms. |
| M2: step 3 showed the gallery's hand-written sample proposal right after "Muse Spark 1.3 writes a proposal" | Fixed with the genuine one: step 3's picture is Muse Spark's own scenario proposal from the live demo ("Written by Muse Spark 1.3 (Meta)", Geminus's low-risk check beside the AI's own claim, Approve and save the scenario), captioned as captured on the live demo. The gallery's sample is no longer made or shown; the Meta section no longer repeats the picture. Unit test checks step 3's picture and caption. |
| M3: the first view never named the product, and the hero picture opened with a warning line, run IDs and "Version 3" with its caption below the fold | Fixed. The brand (the rail's mark and "Geminus", `app.name`) stands above the h1. The results picture is recropped to the Key metrics card from its heading to "Lead time, mean" (captured at 1040 px, where the run settings move below the card): no note, run IDs or version. Its caption is the frame's title bar above it. At 1280×900 the whole picture and its caption are in the first view. |
| L1: industry names | Changed to the parallel H-045 work's current names: Sneaker factory, Bread bakery, Corrugated box plant, Distribution center (zh-Hant 球鞋工廠, 麵包烘焙廠, 瓦楞紙箱工廠, 物流配送中心); to be reconciled at merge. |
| L2: "whatever the AI claims" contradicted "the AI's own estimate can raise it" | Fixed: "The server validates each change and computes the risk itself; the AI's claim can only raise it." |
| L3: "Each has one job" | Fixed: the intro no longer says it (two models and one tool; none can change a model, approve or start a run). |
| L4: step 1's example is a third ICT tester; the demo's what-ifs now centre on FCT | Left as it is: the canvas picture is the gallery's sample line, which has no FCT step, so an FCT question would not match the screen. Noted in §7. |
| L5: the first view's picture was lazy, with no fetch priority | Tried and left lazy. A picture that is not lazy (with or without `fetchPriority`) makes React put `<link rel="preload" as="image">` in the head ahead of the CSP nonce meta, which `layout.test.tsx` pins as the head's first element; changing that assertion was not this slice's call. The visible picture still loads at once in practice (it is in the first view); the hidden theme's is never fetched. A unit test now checks that the page puts no preload in the markup. |
| L6: the light-only live screens glare in the dark theme | Fixed: in the dark theme (system or chosen) they are drawn at 82% brightness (`filter`, no colour); browser test checks the live ones are dimmed and the others are not. |
| L7: dead code | Removed: the search branch of the actor pill, `.primary svg`, `.facts dd p`, the unused `landing.actor.search` and the sample proposal's two keys. |
| L8: about 600 px empty under step 5's text at 1280 | Fixed: the step's text is sticky beside the tall explanation, so it stays level with the screen while the page scrolls past it (static on a phone). |
| L9: screens at 1:1 with real buttons look clickable | Fixed: each picture is framed as a screenshot, a window on the Overlay shadow whose title bar (Well, a Rule line under it, Ink 2 at Label) is the caption, so the buttons sit inside a picture. |
| L10: 390 px miniatures | Left, as asked; noted in §7. |
