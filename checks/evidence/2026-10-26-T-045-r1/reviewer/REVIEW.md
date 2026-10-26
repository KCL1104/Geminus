# T-045 independent review, round 1 (H-003): Marey re-skin and the earlier T-045 work

Reviewer: independent agent, who did not write this code. Date: 2026-10-26.
Tree reviewed: `df7c091` (branch `claude/lucid-meitner-vq1awo`), which contains the re-skin commits f522328, 9f52384, 420e36f, 180c86f and 245a375 and the earlier T-045 work (2026-10-18/19). No product code was changed. Every mutation was reverted, and `git status` was clean afterwards.
Environment: Node 22.22.0 and npm 10.9.4. The repository asks for Node 24, so `npm ci` ran with `--engine-strict=false`. The local Chromium is build 1194, which matches Playwright 1.56.1.

## Verdict: CHANGES REQUESTED

There are no High findings: no attribute or script injection, no WCAG AA failure in the token pairs actually used, and no false PASS. There is one Medium finding (M1): the owner's H-027 rule "no two controls for the same action" is claimed as checked, but it is not tested for any view that has product controls. The other findings are Low or Informational.

## Findings

### M1 (Medium): H-027 rule 2 ("no two buttons with the same name, no two links to one place") is untested wherever product controls exist, and DESIGN.md claims a check that does not exist

- `DESIGN.md:434` says: "Checked: no disabled control on a product route, no two visible links to the same place and no two buttons with the same name (`simplicity.spec.ts`, `simplicity.test.tsx`; …)". The report's Simplicity table (`checks/T-045_VALIDATION.md:110`) says the same.
- `apps/web/tests/simplicity.test.tsx:47-104` checks three things: at most one primary (draft states, gallery, failures), no disabled control (draft states), and shared hrefs. The shared-href check covers only `/`, not-found and the sign-in error page. **No unit test checks for duplicate button names.** `apps/web-e2e/tests/simplicity.spec.ts:43` does check for them, but only on routes rendered signed out (no AuthProvider). On the project and model routes it therefore never sees the model list, `DraftStatus` (Reload / Discard), "Back to project" or the Retry button.
- Evidence (`mutations.log`):
  - **M2.** `DraftStatus` renders "Discard my changes" twice. Result: `npx vitest run`: 547 passed, so the mutation **survived**. No browser test can see this state.
  - **M4.** Each model row in the signed-in list gets a second link to the same model. Result: 547 passed, so the mutation **survived**.
  - **M3.** The removed "All projects" link is added back outside `RequireSession`. The unit tests pass, so it survives there. The browser test `simplicity.spec.ts` kills it with 4 failures (`mutation-3-e2e-simplicity.log`). The browser rule works only for what is rendered signed out.
- Why it matters: H-027 is the owner's explicit condition for adopting the design ("只保留所有應該要包含的按鈕功能等"). The only views that have product buttons or links today are exactly the views the checks do not cover.
- Suggested fix:
  1. In `simplicity.test.tsx`, add a "no two buttons with the same name / no two links to the same href" assertion. Run it over each `DraftStatus` state, a `RemoteView` failure, and the signed-in project and model pages rendered with a fake `ApiProvider` and `AuthProvider` (as `dom.test.tsx` and `ui.test.tsx` already do).
  2. Or narrow DESIGN.md:434 and the report to what is actually checked, and list the uncovered views as NOT RUN.

### L1 (Low): the browser suite's `reducedMotion: "reduce"` is ignored by Playwright, and the reduced-motion rule has no test

- `apps/web-e2e/playwright.config.ts:49` puts `reducedMotion` at the top level of `use`. In Playwright Test 1.56 this is not a fixture option; it belongs under `contextOptions` (see `apps/web-e2e/node_modules/playwright/types/test.d.ts:7398-7402`, the `contextOptions` example).
- Probe P9c (`motion2.probe.ts.txt`, `reviewer-probe-run2.log`) runs under the same kind of config and gets `matchMedia("(prefers-reduced-motion: reduce)").matches === false` and transition-duration `0.18s`. So all 116 browser checks, and the CI screenshots, run with motion on, which the config did not intend.
- The implementation is correct. With `page.emulateMedia({ reducedMotion: "reduce" })`, the 180 ms transitions become `1e-05s` (P9, P9b). However, no test asserts this. `design-rules.test.ts` only mentions it in a comment, and DESIGN.md "Do's" requires it.
- The dead option dates from dd9dc34 (2026-10-19). It matters more now that the re-skin added 180 ms colour transitions: an axe contrast sample taken right after a hover or focus can land mid-transition.
- Suggested fix:
  - Change the config to `contextOptions: { reducedMotion: "reduce" }`. Running a TypeScript check over `apps/web-e2e` would have caught this.
  - Add one browser assertion: under reduce, a Button's `transitionDuration` is at most 0.01 ms.

### L2 (Low): status text that is stale after H-028, and an unrecorded post-baseline CI result

- `DESIGN.md:418`: "The re-skinned screenshot baselines still need the owner's confirmation (TEST-033)". H-028 confirmed them.
- `checks/T-045_VALIDATION.md`:
  - line 3 says "prototype 與 baseline 待 owner 確認";
  - line 5 and line 23 say "16 張截圖 baseline";
  - line 98 says "截圖 baseline 尚未重新產生…CI 的截圖比對會失敗";
  - line 254 says "提交後的 CI 結果記在下一次更新", but there is no such update.
- `docs/DECISIONS_AND_TBD.md:464` (the H-027 "pending" column) says "換裝後的 **16** 張截圖 baseline". There were 32: 16 light and 16 dark.
- `.impeccable/design.json:1196`: the overview text still says "Status: awaiting the owner's review (H-013); this system is not approved … The app's current tokens … do not follow it yet". This contradicts its own line 5.
- What I found for the missing CI record:
  - `gh run view 40309207437` (push of 245a375): all 5 jobs passed, and browser checks reported **148 passed, 0 skipped**, with the 32 `screenshots.spec.ts` cases compared (`ci-40309207437-browser-checks-test-step.log`).
  - The `update_screenshots` run 40307255504 is listed with conclusion **cancelled**: its backend job was cancelled by the workflow's concurrency group. Its browser-checks job succeeded and uploaded the artifact. The report does not mention the cancelled conclusion.
- Suggested fix: update these texts and record run 40309207437 in the report.

### L3 (Low): some of the re-skin agent's own results have no committed log

`checks/T-045_VALIDATION.md:180-185` reports four results that have no log under `checks/evidence/2026-10-26-T-045-reskin/`, which holds only coordinator logs and the local preview shots:

- the strip-guard mutation ("4 個 case 失敗");
- the first `npm --prefix apps/web-e2e test` run (114 passed, 2 failed);
- the local preview-shot run;
- the first-load size table.

AGENTS §6 asks for the commands and their results to be shown. I reproduced the two results that matter:

- **Mutation M1** (`mutations.log`) fails exactly 4 tests in `ui-components.test.tsx`.
- **First-load sizes** (`first-load-probe.log`, gzip level 9): `/` JS 246.3 / CSS 37.8 KiB; `/projects` 251.6 / 37.8; `/ui-gallery` 291.8 / 37.8. The report gives 247.7/38.7, 253.0/38.7 and 293.4/38.7, which is within about 1.6 KiB. The difference is consistent with a different gzip level, so the claims are plausible.

Next 16 `next build` prints no per-route sizes (`frontend-build.log`), so `make frontend-build` alone cannot confirm them.

Suggested fix: commit the measuring script and its output, or mark these rows as "log not kept".

### L4 (Low): one used non-text pair is not in the contrast test, and a DESIGN.md contrast statement is slightly off

- A selection checkbox in a selected table row (gallery table 1, `selectionMode="multiple"`) puts the Control Edge box border on Selected Row Tint. `tests/tokens.test.ts:115-119` does not list `edge` against `selection-tint`.
- The value is **3.31:1 in light and 3.06:1 in dark** (`contrast-probe.log`). It passes 3:1 today with little margin, and a token tweak could break it unnoticed. The axe browser check would catch a failure only if axe evaluated non-text contrast, and it does not (axe's color-contrast rule covers text only).
- `DESIGN.md:471` says "Control Edge stays at least 3.17:1 against every surface a control sits on". In the app the dark value is 3.06.
- The other pairs the CSS produces are fine (light / dark):
  - danger label on Press: 5.18 / 4.72;
  - edge on Well: 3.43 / 3.92;
  - edge on Error Wash: 3.37 / 3.13.
- Edge on Hover would be 2.90 in dark, but it is not used, because rows have no hover fill, as DESIGN.md says.
- Suggested fix: add `["--gm-color-edge", "--gm-color-selection-tint"]` to `nonTextPairs`, and change 3.17 to 3.06 in DESIGN.md, or say the 3.17 figure is the prototype's.

### L5 (Low; judgement): on a phone the theme control's visible text is only its value

- At ≤720 px, `apps/web/ui/select.module.css:24-33` hides the strip labels visually. The theme control then reads only "System" (or "Light"/"Dark", 「跟隨系統」/「淺色」/「深色」) with a chevron; see baseline `home-en-390.png`. Its accessible name keeps the label ("System Theme"), so WCAG 2.5.3 holds (probe P10), and axe passes.
- A sighted reader is not told that the control is a theme choice. WCAG 3.3.2 (labels or instructions) is arguably met only through the option list, and only after the control is opened. The language control does not have this problem, because "English" or 「繁體中文」 is self-explanatory.
- The owner accepted these screenshots (H-028), so this is a note, not a blocker.
- Suggested fix: keep a short visible label on phones, or add a theme icon (DESIGN.md: icons "come with the authored icon set").

### I1: on the closed strip Select, printable keys are dropped entirely

React Aria applies ArrowLeft, ArrowRight and type-ahead to a closed Select immediately. The strip guard (`apps/web/ui/select.tsx:51`, `onlyByChoice` at 54-65) stops those keys. I verified in the browser:

- Inside the **open** listbox, type-ahead still works, because the guard checks only the trigger's `aria-haspopup` (probe P1).
- On the closed trigger, Home, End, PageUp and PageDown change nothing, and Space opens the list (P2).
- On the closed trigger, a printable key neither selects nor opens the list (P3).

The APG select-only combobox would open the list and move to the matching option on a printable key. This is a usability deviation, not a WCAG failure. The report's description ("ArrowUp, ArrowDown, Enter and Space still open the list") is accurate.

Optional fix: on a printable key, open the list instead of dropping the key.

### I2 (outside T-045; found through a route T-045 tests): `/auth/error?reason=toString` returns 500

- `apps/web/app/auth/error/page.tsx:17` uses `reason in reasons`. That expression is true for `Object.prototype` keys, so `t()` receives a function and throws "needs a numeric count".
- `cookie-probe.log` shows: `toString`, `constructor`, `__proto__`, `hasOwnProperty` and `valueOf` all return **500**; `expired` and `nonsense` return 200. Probe P11 shows the same.
- The code came from T-043 (74e327a). The page is otherwise unchanged by T-045, but T-045's a11y and simplicity checks use it.
- Suggested fix: `Object.hasOwn(reasons, reason)`, plus a negative test. Route to the T-043 follow-up.

### I3: the server and the client decode the cookies differently

- Next's `cookies().get` percent-decodes values: `geminus_theme=%64ark` renders `data-theme="dark"` (`cookie-probe.log`).
- `cookieTheme` and `cookieLocale` (`apps/web/lib/theme/theme.ts:22-29`, `lib/i18n/locale.ts`) do not decode, so the same cookie reads as "system" in the browser.
- This affects only `global-error`, and only values that decode to exactly `light`, `dark` or a locale. It is harmless, but the comment "as in Next.js cookies().get" is only half true.
- Optional: decode before `parseTheme`/`parseLocale`, or note the difference.

### I4: wording in ADR-011

`docs/adr/ADR-011-toolchain-bootstrap.md:23` says "5.3.1 為 2026-10-11 發布". That is true only for `@fontsource-variable/noto-sans-tc`. For `@fontsource-variable/archivo`, `npm view … time` shows the latest is 5.3.0.

### I5: smaller notes

- **Explicit-theme axe coverage.** `a11y.spec.ts` checks dark only through the system preference. The reader's explicit choice (`data-theme="dark"` on a light system, `data-theme="light"` on a dark system) is not axe-checked. It passes in probe P4, and `tokens.test.ts` keeps the two dark blocks equal (mutation M7 killed).
- **Future CSP.** The statement "nothing for a CSP to allow" is true for the theme and the fonts. Separately, React Aria and the virtualized table emit `style="…"` attributes in the SSR HTML, so a strict CSP will need `style-src-attr` handling. This was already true before the re-skin, and the report does not discuss it.
- **Reflow at 320 px.** WCAG 1.4.10 is defined at 320 CSS px. No route scrolls sideways at 320 px in either language; the strip wraps to two rows of 81 px (probe P12, `reviewer-probe-reflow.log`). The 48 px one-row claim is made for 390 px only, which is correct.

## What I verified as correct

### Cookie security
- **Server side.** `<html data-theme>` takes only the value `parseTheme` returns, and `parseTheme` matches exactly `light` or `dark`; React also escapes attributes.
  - Injection attempts (`dark" onload="…`, `"><script>…`, `DARK`, a value with a leading space) render no `data-theme` (`cookie-probe.log`).
  - A malformed locale cookie renders `lang="en"`.
  - With two cookies of the same name, the last one wins on both the server and the client.
- **Client side.** The client writes only `themeCookieValue(parseTheme(id))`.
- **Cookie attributes.** `Path=/; Max-Age=31536000; SameSite=Lax`, plus `Secure` over https; "System" sets `Max-Age=0`.
  - The cookie is not HttpOnly, which is acceptable: it holds a colour choice that the client must write, and it carries no identity or authorization.
  - The browser test checks SameSite, HttpOnly and Path, and the unit test checks Secure.
- **No inline script.** The served HTML has 0 inline scripts in `<head>`, and none that touches the theme. The only inline scripts are Next's RSC payload.

### Accessibility
- **Contrast.** Every pair in `tokens.test.ts` is computed from DESIGN.md's values. I added the pairs the CSS produces but the test omits; all meet AA (L4 lists the slim one).
- **Explicit themes.** axe shows 0 violations with an explicit theme over the opposite system theme (P4).
- **Problem disclosure.** It is a native `<details>`/`<summary>`. Focus is visible (`solid 2px`), Enter opens it, and opened it has 0 axe violations in both themes (P5).
- **Strip listboxes.** Open at 390 and 1280 px, in both themes, they have 0 violations (P6).
- **Theme and language together.**
  - A theme chosen in the browser survives a language change (`router.refresh` re-renders `<html>`; P7, P8).
  - Going back to "System" leaves no stale attribute.
- **React Aria Chinese.** `zh-Hant` maps to React Aria's `zh-TW` (`ui-provider.test.tsx`).
- **Library strings.** The only non-catalog strings found are the accepted React Aria ones (`e2e-test.log`, strings spec).

### Tests
- `tokens.test.ts` reads DESIGN.md's front matter itself. Mutation M6 (a changed DESIGN.md value) is killed.
- `design-rules.test.ts` really scans every stylesheet.
- The strip guard is tested (M1 killed).
- Comparing the pre- and post-re-skin tests (`git diff a0fb365 245a375 -- apps/web/tests apps/web-e2e/tests`), the removed assertions fall into two groups:
  - **Relaxed under H-011/H-013, as the report says:** "light only", "system fonts only" and "palette layer".
  - **Replaced by equal or stronger checks:** the native `<select>` assertions, now Select name / value / option `lang`; the focus ring, now an exact value in both themes; contrast, now 28+11 pairs per theme instead of 17+6 in light only, with every old pair mapped.
  - I found no unjustified weakening.

### Dependencies
`@fontsource-variable/archivo` 5.3.0 and `@fontsource-variable/noto-sans-tc` 5.3.0:
- published 2026-07-19 (`npm view`);
- license OFL-1.1, recorded in THIRD_PARTY_NOTICES with copyright, and in ADR-011;
- no scripts and no dependencies;
- lockfile integrity equals the registry's `dist.integrity`;
- `npm audit --omit=dev`: 0 vulnerabilities.

The full audit finds 1 high: happy-dom, a dev dependency that was already there before the re-skin (`npm-audit-all.log`). The build serves 105 Noto Sans TC files and 3 Archivo files, 4.5 MB in all, as the report says.

### H-028 record
- I downloaded artifact 12643138154 from run 40307255504. The zip's sha256 is `e4829e4cb845fc56c985ce064e09071bd800f40c85a3d95e9357a2ecc4d8502f`, as reported.
- Its 32 PNGs are byte-identical to the 32 committed files (`baselines-*.sha256`, `diff` empty).
- `screenshots.spec.ts` enumerates exactly 4 routes × 2 locales × 2 widths × 2 schemes = 32 names, with the `-dark` suffix.
- CI after the commit compared all 32 and passed (L2).

### Report numbers
The report's numbers reproduce:
- 21 files / 547 vitest tests;
- e2e 116 passed and 32 skipped, made up of a11y 64, keyboard 8, simplicity 28, theme 5, strings 2 and session 9.

## Commands run (exit codes in `exit-codes.txt`; logs in this directory)

| Command | Result |
|---|---|
| `npm ci --prefix apps/web --engine-strict=false` | exit 0 (EBADENGINE warnings: Node 22) |
| `npm ci --prefix apps/web-e2e --engine-strict=false` | exit 0 |
| `make frontend-test` | exit 0: lint, typecheck, vitest 21 files / 547 tests |
| `make frontend-build` | exit 0 |
| `npm audit --prefix apps/web --omit=dev` | exit 0, 0 vulnerabilities |
| `npm audit --prefix apps/web` | exit 1: 1 high (happy-dom, dev, pre-existing) |
| `make e2e-test` | exit 0: 116 passed, 32 skipped (screenshots NOT RUN locally by design) |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | exit 0. It rewrote `checks/docs-check.json` (86 to 87 links): the committed file regressed in 1ad02c4 (T-053), not T-045. Restored. |
| Reviewer Playwright probes against `next start` :3200 (`reskin/motion/motion2.probe.ts.txt`) | 16 passed, 1 failed (P11, the I2 defect); `reviewer-probe-run2.log` |
| Reflow probe at 320 px (`reflow.probe.ts.txt`) | 2 passed |
| `contrast-probe.py.txt`, `cookie-probe.py.txt`, `first-load-probe.py.txt` | outputs in the matching `.log` files |
| Mutations M1 to M7 (`mutations.log`) | M1, M6, M7 killed; M3 killed only by the browser test; M2, M4, M5 survived |
| `gh run view` 40309207437, 40307255504; artifact 12643138154 download | see the H-028 record above and L2 |

## NOT RUN

| Item | Reason |
|---|---|
| Local screenshot comparison | Only in CI's pinned container (README). I relied on CI run 40309207437 instead. |
| Node 24 | Not installed; Node 22.22.0 was used with `--engine-strict=false` |
| Firefox, Safari, screen readers (VoiceOver, NVDA) | P0 runs Chromium only; screen readers need a person |
| Signed-in views in a browser (project list, model page with a 409) | The app mounts no AuthProvider, and the fake API returns no data. This is the gap behind M1. |
| `global-error` in a browser | No way to make the root layout fail without changing code |
| A real CSP header | The app sends none; I5 is reasoning from the served HTML |
| Pushing, CI dispatch, baseline changes | Not allowed in this review |
