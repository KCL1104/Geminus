# Browser checks of apps/web (TEST-033)

A separate npm project, so Playwright and axe never reach a production
install of `apps/web` (Next.js declares an optional peer dependency on
`@playwright/test`, which would otherwise be installed with `--omit=dev`).

```sh
npm ci --prefix apps/web && npm ci --prefix apps/web-e2e
npm --prefix apps/web-e2e test            # builds and starts apps/web on :3100 with the gallery on
```

- `tests/a11y.spec.ts`: axe-core (WCAG 2.0–2.2 A/AA, contrast and target size included) on every route and on the gallery's states, en and zh-Hant, 390 and 1280 px, in the light and the dark theme (the system's), and on every route with the reader's own choice over the opposite system theme; the signed-in views (`helpers.ts` `signedInViews`: the project list, a project, the model with the editor as editor and viewer, the choice of organization, the switcher, no access) in both themes, languages and widths; no page-level horizontal scroll at 390 px.
- `tests/keyboard.spec.ts`: the shell, the language and theme changes, and the gallery's flows with the keyboard only; a visible focus at every stop.
- `tests/theme.spec.ts` (H-013): the theme follows the system unless chosen; a choice applies at once, is remembered (cookie `geminus_theme`) and is in the server's HTML, so the first paint has it; the fonts load from the app's own origin.
- `tests/simplicity.spec.ts` (H-027): on every route, at most one primary action, no disabled control outside the gallery, no two links to the same place or buttons with the same name (one pair kept for the owner's decision), no side panel, and a one-row app strip. The signed-in views are checked too, signed in through the BFF (the model page's blocked editor actions must show their reason; on a 390 px phone the account takes a row of its own under the strip's unchanged first row); `apps/web/tests/simplicity.test.tsx` adds every state of the bootstrap. Motion: every transition is 180 ms, and none lasts longer than 0.01 ms when the test emulates reduced motion. The suite runs with motion on, as the screenshot baselines were made.
- `tests/strings.spec.ts`: every string a person sees or hears is from the catalog, data, or one of the React Aria strings the owner accepted (H-008, H-009); prints the non-catalog strings it found. The signed-in views' data is the fake API's (organization, project and model names).
- `tests/session.spec.ts` (TEST-032, T-043): sign-in through the BFF against the test-only provider and API stand-in in `fake/servers.mjs` (started by `playwright.config.ts` on :3101 and :3102, never a real provider): the session cookie's attributes, no token a script can read, the API proxy without cookies, CSRF refusals, a tampered or replayed callback, refresh with rotation, logout with revocation, and an unknown identity's 403.
- `tests/bootstrap.spec.ts` (T-043 follow-up, D-045): the signed-in UI against the same fakes: the sign-in from the signed-out page and its return path (never to another site), GET /v1/me without an organization, the only organization chosen by itself and sent as `X-Organization-ID`, the projects, a project and the model in the 2D editor, a viewer's read-only model, several organizations (a choice, remembered for the browser session, switched from the strip), sign-out, a session the provider ended (the sign-in again), no membership and an unknown identity (the permission state), and the flow with the keyboard only. The fake API's tenant data: a subject per kind of user (`helpers.ts` `tenant`), per organization its projects and, in one, a model with the draft fixture `testdata/contracts/valid-draft-with-issues.json`.
- `tests/editor.spec.ts` (T-015): the 2D editor on its review preview (`/ui-gallery/editor`, the real model page against an in-page stand-in for the model API): the main flow with the keyboard only, dragging and connecting with the mouse, the 409 path, publish, the language switch keeping unsaved edits, and axe in each state (en and zh-Hant, light and dark). The preview is not in the screenshot spec.
- `tests/proposal.spec.ts` (T-031): the AI proposal review on its review page (`/ui-gallery/proposal`, the real page and client against an in-page stand-in for the proposal API; no AI is called): approve with the keyboard only (confirmed in place, the outcome focused), approve a scenario, reject with a reason (en and zh-Hant), a 409 at approval keeping the page, a proposal being written becoming ready, the model page's request landing on its proposal, hostile AI text and hostile names in the patch staying text (each name in a `<bdi>`, no raw format or control character), the JSON Patch disclosure at 390 px, and every string the catalog's or data. The samples whose look differs are also in `helpers.ts`'s routes (axe, simplicity). And the signed-in demo path (review round 1): signed in through the BFF as an editor, the model page's request, the proposal read until ready, the edit approved with the keyboard and version 2 on the model page (en and zh-Hant, axe at each step), and a rejection with a reason, against the fake API's proposal routes: `fake/servers.mjs` answers createProposal (202 generating), getProposal (generating on its first read, then ready with the fixed answer in `fake/proposal-answer.json`, in the request's language; written for the test, not an AI's and not the server's review), approveProposal (an edit makes the next version and moves the clean draft; a scenario gets a scenario ID) and rejectProposal, per organization, with idempotency keys; `POST /__reset` on :3101 puts the tenant data back, before and after each of these tests. Not in the screenshot spec.
- `tests/screenshots.spec.ts`: full-page screenshots, en and zh-Hant at 390 and 1280 px, light and dark (names ending in `-dark`), compared with the baselines in `tests/__screenshots__/`.

## Screenshot baselines

Rendering depends on the browser build and the fonts (the app serves
Archivo and Noto Sans TC itself; system fallbacks still differ), so
baselines are made and compared in one environment only: CI's e2e job, in the pinned Playwright
container. Elsewhere the screenshot tests report NOT RUN (they run only
with `E2E_SCREENSHOTS=1`).

A baseline never changes by itself (`updateSnapshots: "none"`). To change
one, run the CI workflow `ci` by hand with `update_screenshots` on. The job
uploads the new images as the artifact `screenshots`. A person looks at them
and commits them into `tests/__screenshots__/`.
