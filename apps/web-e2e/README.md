# Browser checks of apps/web (TEST-033)

A separate npm project, so Playwright and axe never reach a production
install of `apps/web` (Next.js declares an optional peer dependency on
`@playwright/test`, which would otherwise be installed with `--omit=dev`).

```sh
npm ci --prefix apps/web && npm ci --prefix apps/web-e2e
npm --prefix apps/web-e2e test            # builds and starts apps/web on :3100 with the gallery on
```

- `tests/a11y.spec.ts`: axe-core (WCAG 2.0–2.2 A/AA, contrast and target size included) on every route and on the gallery's states, en and zh-Hant, 390 and 1280 px; no page-level horizontal scroll at 390 px.
- `tests/keyboard.spec.ts`: the shell, the language change, and the gallery's flows with the keyboard only; a visible focus at every stop.
- `tests/strings.spec.ts`: every string a person sees or hears is from the catalog, data, or one of the React Aria strings the owner accepted (H-008, H-009); prints the non-catalog strings it found.
- `tests/session.spec.ts` (TEST-032, T-043): sign-in through the BFF against the test-only provider and API stand-in in `fake/servers.mjs` (started by `playwright.config.ts` on :3101 and :3102, never a real provider): the session cookie's attributes, no token a script can read, the API proxy without cookies, CSRF refusals, a tampered or replayed callback, refresh with rotation, logout with revocation, and an unknown identity's 403.
- `tests/screenshots.spec.ts`: full-page screenshots, en and zh-Hant at 390 and 1280 px, compared with the baselines in `tests/__screenshots__/`.

## Screenshot baselines

Rendering depends on the installed fonts, so baselines are made and
compared in one environment only: CI's e2e job, in the pinned Playwright
container. Elsewhere the screenshot tests report NOT RUN (they run only
with `E2E_SCREENSHOTS=1`).

A baseline never changes by itself (`updateSnapshots: "none"`). To change
one, run the CI workflow `ci` by hand with `update_screenshots` on. The job
uploads the new images as the artifact `screenshots`. A person looks at them
and commits them into `tests/__screenshots__/`.
