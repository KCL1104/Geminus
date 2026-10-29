# H-042 and H-043 validation: the rail and account menu, and the Meta models made visible (hackathon slice)

**Date: 2026-10-29 | Scope: H-038's UI polish window, plus the "Use of Meta AI Tooling" criterion | Basis: owner decisions H-042 (impeccable shape round), H-043 (Meta AI tooling) and H-044 (documents in English), DESIGN.md (H-041 world, H-027 simplicity) | Requirements: REQ-022, REQ-023**

## 1. What the owner asked and chose

- **H-042.** After H-041 the owner found the language and theme controls at the top right "AI slop", the left side empty, the personal information lacking, and the gallery's underlined state links too many. In the shape round they chose a model-workspace rail (Canvas, Scenarios, AI proposals, Runs), the language, theme and account in an avatar menu, and the person's name and email from the sign-in. They then confirmed the brief: 「好照做」 ("OK, go ahead"), deploying with this round. Asked about the gallery's fifteen state buttons, they chose one grouped dropdown.
- **H-043.** Asked how to raise the "Use of Meta AI Tooling" (25%) score, they chose: (A) show which Meta model wrote each proposal and explanation, (B) turn the explanation's suggested next experiment into a scenario proposal with Muse Spark, (D) say which Meta model does each step in the README, the submission text and the video. They did not choose (C), SAM's boxes over the photo.

## 2. Changes

| Area | Change | Files |
|---|---|---|
| Sign-in | The default OIDC scopes add `profile email`. The callback keeps a cleaned profile in the sealed session cookie: the name or nickname, with characters that show nothing or reorder text removed and spaces collapsed (at most 128 characters), and one printable email address (at most 254). No other claim is kept. Sessions sealed before H-042 (five fields) stay valid. The layout reads the profile only when there is a session and passes it to the rail. The type lives in `lib/auth/profile.ts`, so no client module reaches the session code. | `lib/session/{session,oidc,bff,server,config}.ts`, `lib/auth/profile.ts`, `app/layout.tsx` |
| Shell | The top strip is gone. A 56 px rail holds the brand (the way to the projects), a model's four sections (with `aria-current` on the page's own; a scenario, results or proposal page names its model with `RailModel`), and at its foot the account or the settings. On a phone the rail is a top bar. | `app/_components/rail.tsx`, `ui/shell.module.css`, `app/layout.tsx` |
| Account menu | A React Aria dialog in a popover. Signed in: the name, email, organization and role; the organization switcher (several organizations only); language and theme as segmented radio groups; Sign out. Signed out: a Settings button with the language and theme only. | `app/_components/account.tsx`, `locale-switcher.tsx`, `theme-switcher.tsx`, `ui/segmented.tsx` |
| New pages | `/models/<id>/scenarios`, `/models/<id>/proposals` (new client method `listProposals` on the existing `GET /models/{id}/ai-proposals`) and `/models/<id>/runs` (`listSimulations`). Each has an empty state, short state pills and an honest "more pages" note. The scenario list moved off the model page. | `features/models/model-lists.tsx`, `app/models/[modelId]/*/page.tsx`, `lib/api/client.ts`, `ui/list.module.css` |
| Back links | The proposal, scenario and results pages no longer have a "Back to the model" link, because the rail's Canvas is that way and H-027 allows no two links to the same place. The results page keeps its scenario as the trail. | `proposal-view.tsx`, `simulate-view.tsx`, `run-results.tsx` |
| Less clutter | The draft pill shows a dot, "Saved" and "rev 3"; the sentence stays in the status region and Save draft's description. The AI form's help text is shorter (the synthetic-data notice stays, T-057). The results' run settings are a light block, not a card. The canvas is as tall as the window allows (480 to 900 px). | `model-editor.tsx`, `editor.module.css`, `messages/*`, `results.module.css`, `canvas.module.css` |
| Gallery | The fifteen sample states are one dropdown in three groups (ready to review, not reviewable yet, decided and edge cases); the editor's four states use the same menu. Review surface only: production never serves the gallery. | `app/ui-gallery/state-menu.tsx`, `*/preview.tsx` |
| H-043 A (contract first) | New `GeneratedBy` in `schemas/common.schema.json`; `generated_by` on the proposal resource and on `AnalysisRecord`. `proposals.FromMetadata` takes only the provider, the model and the segmenter's provider, model and object count (at most 64) from the stored metadata, each checked against the contract's patterns. Credential labels, costs, reservations, request IDs, hashes and latency never leave the server. It is absent while generating and for a failure. TS types regenerated (`scripts/gen_ts_contracts.py`). | `schemas/*.json`, `internal/proposals/public.go`, `internal/analyses/analyses.go`, `apps/web/lib/contracts/*` |
| H-043 A (UI) | The proposal header has "Written by Muse Spark 1.3 (Meta)" and, for a photo, "Photo read by SAM 3.1 · 12 objects". The explanation's note says "Written by Muse Spark 1.3 (Meta) from these results." A model the catalog does not know is shown by its own ID. | `features/ai-proposals/generated-by.tsx`, `proposal-view.tsx`, `features/results/analysis.tsx` |
| H-043 B | Beside each suggested next experiment, an editor has "Propose as a scenario" (accessible name "Propose as a scenario: <name>"). It reads the model's latest version, asks for a scenario proposal with the suggestion as the intent, and opens the proposal page. A retry reuses its idempotency key. Viewers do not see it. | `features/results/analysis.tsx` |
| Docs (H-044) | Three sessions translated the core documents and rewrote the README (with a section on how Geminus uses Meta AI); merged. DESIGN.md, PRODUCT.md and IMPLEMENTATION_PLAN updated. | see H-044 |

## 3. Tests

Unit (`apps/web/tests`, happy-dom where a component mounts):

- `session.test.ts`: the profile keeps the name (or nickname) and email, cleaned, and no other claim; hidden and reordering characters are dropped and the length is capped; an email is one printable address; a session reads back with a profile, with `null`, and without one (sealed before H-042), and a malformed profile is refused. Review M1: a name already clean cleans to itself, a cut that ends on a space included, and the session sealed with it reads back.
- `bff.test.ts`: the default scopes include `profile email`; the callback seals the cleaned profile and nothing else from the ID token (fake OIDC `extra` claims); `profile()` gives only the two strings.
- `layout.test.tsx`, `layout-session.test.tsx`: one `nav` (the rail), one `main`, no `header` or `aside`; the brand is the way to the projects; signed out the rail's foot is Settings; signed in the profile is read only with a session.
- `shell-h042.test.tsx` (new, 12 tests): the rail's sections and `aria-current`, `RailModel` for a page outside `/models`, no section outside a model; the avatar's initials (whole characters, review L2) and the menu's name, email, organization and role; the signed-out Settings menu; the three list pages (state pills, empty states, the model named and not linked); review M3, a summary's hidden or reordering characters shown as ⟨U+XXXX⟩ inside a `<bdi>`, and the "more pages" notices for proposals and runs (review M2).
- `theme.test.tsx`, `i18n-dom.test.tsx`: language and theme are radio groups; arrow keys move, only a press changes the page.
- `proposal-view.test.tsx`, `proposal-names.test.tsx`, `proposal-simplicity.test.tsx`, `results-simplicity.test.tsx`, `results-view.test.tsx`: no back link (zero links in the band, stricter than one); every hostile-name check kept; "Written by" and "Photo read by" from `generated_by`; "Propose as a scenario" for an editor (the request's base, mode, intent and locale; a retry reuses the key) and not for a viewer.
- `api.test.ts`: `listProposals`. `i18n.test.ts`: both catalogs have the same keys. `proposal-preview.test.tsx`: the gallery's state menu.

Go: `internal/proposals/generated_by_test.go` (new): `FromMetadata` takes only the provider, model and segmenter fields, each checked against the contract's patterns, and ignores everything else (credential labels, costs, reservations, request IDs, hashes, latency); malformed values give no field. `proposals_test.go`: `generated_by` on asked, reviewed and invalid proposals, not on generating or failed ones.

e2e (`apps/web-e2e`): `helpers.ts` `openAccount`; `simplicity.spec.ts` `railShape` (56 px wide; on a phone one row of at most 56 px); `bootstrap.spec.ts`, `keyboard.spec.ts`, `theme.spec.ts`, `editor.spec.ts`, `a11y.spec.ts` reach the language, theme, organization and Sign out through the menu; `proposal.spec.ts` uses the rail's Canvas and asserts the band has no links; the fake API serves the version, scenario, proposal and run lists in the server's order.

## 4. Independent review (H-029, once)

The review read every changed file and the code they depend on. Its tools had no shell, so it ran nothing.

| # | Finding | Handling |
|---|---|---|
| M1 | `cleanName` cut a name to 128 characters without trimming again. A cut that ended on a space gave a profile that failed the session's own check, so the next request found no session: a silent sign-in loop. | **Fixed**: trimmed after the cut; a test that a clean name cleans to itself (a cut on a space included) and that its session reads back. |
| M2 | The proposals notice said "newest", but the API lists proposals oldest first (and the fake reversed them, so the tests agreed with each other, not with the server). The runs page had no "more pages" notice. | **Fixed**: the notice says "first", like the other lists; a "runs" notice; the client comment and the fake follow the server's order; a test for both notices. |
| M3 | The proposals list showed the AI's summary raw, though the proposal page shows hidden and reordering characters as ⟨U+XXXX⟩. | **Fixed**: `visible(summary, "prose")` in a `<bdi>`, with a hostile-summary test. "Propose as a scenario"'s accessible name uses the same. |
| M4 | ADR-018, both runbooks and `oidc.ts`'s header still said the ID token is discarded with nothing kept and the scopes are `openid offline_access`. | **Fixed**: ADR-018 records the profile (H-042), the runbooks the new default scopes, `oidc.ts` its header. H-042 and H-043 were already in the register. |
| L1 | During a rolling deploy or after a rollback, the previous build refuses a six-field session cookie: someone who signed in on the new build is signed out. | **Not changed**: a one-time sign-in again, recorded here. |
| L2 | The avatar took a word's first UTF-16 unit, which breaks an emoji. | **Fixed**, with a test. |
| L3 | The rail's current section differs from the others by less than 3:1 (white tile on near-white); at 720 px and below the icons have no visible names. | **Not changed**: `aria-current` and the accessible names carry it; recorded for the owner's screenshot review. |
| L4 | The rail adds exits from the canvas with no guard for unsaved edits. | **Not changed**: the draft is saved on the server (Save draft); recorded. |
| L5 | No test for `generated_by` on analyses; none that a refreshed session keeps its profile; a stale test pointer; `ProposalHrefs.model` and `Select variant="strip"` unused; the gallery's rail links to real routes. | Test pointer **fixed**. The rest **not changed**: the analysis rule is three lines beside the proposals rule that is tested; refresh spreads the session. The unused code is listed for the owner (H-027). |
| L6 | "Propose as a scenario" pulled the person to the new proposal even after they had left the results. | **Fixed**: nothing happens after the panel is gone. |

The e2e run before the review fixes also found two problems of its own, fixed in the same batch:

- **The menu's focus.** React Aria's dialog took the focus itself on opening, so the first stop after Enter had no visible ring (TEST-033). The menu now opens on the language's chosen item (`Segmented autoFocus`).
- **The avatar's initials.** For an organization without a profile they are "LW"; `strings.spec.ts` now lists them with the tenant data they are made from.

## 5. Deliberately not changed, and limits

- **Screenshot baselines.** The 32 baselines (`/`, `/projects`, `/no-such-page`, `/ui-gallery`) are stale since H-041 and change again with the rail. CI's `update_screenshots` re-shot them (run 40895058314 on 8abef1f, every job green); the owner looked at them and approved them on 2026-10-29, and they are committed.
- **List order.** The lists show the server's order (oldest first, like the project and model lists); paging beyond 100 is still not built, and the page says so.
- **Who read the photo.** "Photo read by SAM 3.1" shows only when the proposal's stored metadata records the segmenter; otherwise only the writer shows.
- **Live AI.** Live NOT RUN until the deploy below: which provider the deployment uses, and whether "Propose as a scenario" answers, are checked there.
- **Unused code.** `ProposalHrefs.model` and `Select variant="strip"` are unused; listed for the owner (H-027), not deleted.

## 6. Commands and results (2026-10-29, local WSL2; Node 24.21.0; Go 1.27.3; Chromium, Playwright 1.56.1)

| Command | Result |
|---|---|
| `make frontend-test` (before the review fixes) | exit 0 |
| `make e2e-test` (before the review fixes) | 550 passed, **4 failed**, 32 skipped: `keyboard.spec.ts` ×2 (the dialog took the focus, see §4), `strings.spec.ts` ×2 (the avatar's "LW") |
| `make frontend-test` (after the fixes) | exit 0: lint, typecheck, **1142 unit tests passed** (53 files) |
| `make e2e-test` (after the fixes) | exit 0: **554 passed**, 32 skipped (screenshots, compared only in CI's pinned image), 17.1 min |
| `make backend-test` | exit 0 (85 packages ok) |
| `scripts/gen_ts_contracts.py --check` | exit 0 (the TS contracts match the schemas) |
| `make spec-check` | exit 0 |

NOT RUN locally: `make backend-race`, `make integration-test` (left to CI); the screenshot comparison (baselines stale, §5); live (the deploy follows this commit).

## 7. Live (after the 5b0d354 deploy, 2026-10-29, as the judge)

The deploy finished with exit 0: migrations (18 recorded), api, worker and web SUCCESS; `/healthz` and `/readyz` 200; `/auth/login` 303 to Auth0. Script: `check-h042.cjs` (Playwright; the password only in an environment variable).

| Check | Result |
|---|---|
| Sign-in with the new `profile email` scopes | Signed in with no consent screen. The avatar shows initials. The menu shows the name and the email, two radio groups (language, theme) and Sign out. |
| Rail on the model page | Projects, Canvas (current), Scenarios, AI proposals, Runs. No link back to the model in `main`. No horizontal overflow at 1280. |
| List pages | Scenarios: 7 rows, all 5 what-ifs present. AI proposals: 9 rows with their states. Runs: 16 rows. On each page the rail marks its own section. |
| `generated_by` from the live API | Every approved, needs-input and invalid proposal is labelled `meta/muse-spark-1.3`; the photo proposal is labelled `+sam-3.1 (10 objects)`. Failed proposals have no label. |
| Proposal page | "Written by: Muse Spark 1.3 (Meta)"; no links in `main`. |
| Explanation (kept analysis) | "Written by Muse Spark 1.3 (Meta) from these results." Three "Propose as a scenario: …" buttons. |
| Phone (390 px) | The rail is a 390 × 49 px bar across the top; no overflow; no page errors. |
| "Propose as a scenario" (one live request: "Test FCT follow up capacity") | The proposal was created (`aip_01M63PMA6595KPWG9XWMJX8MEX`). The worker's Meta call returned `provider_status=404`, `not_found_error` (`AI_REQUEST_REJECTED`) after 1.25 s, so the proposal **failed** and nothing was approved. This is the egress problem recorded in T-057: Meta answers 404 to some Railway instances after a deploy. The fix recorded there is to redeploy the worker; that needs the owner's go-ahead. The H-043 B loop is **not yet confirmed live**. |

**Worker redeploys (owner's go-ahead, 2026-10-29).** Two `railway redeploy --service worker` (deployments `77fa0984`, then `6a28453c`); both SUCCESS, `/readyz` 200, the region still `asia-southeast1-eqsg3a` from `deploy/railway/worker.railway.json`. After each, one more "Propose as a scenario" (`aip_01M63YPHDDHEWVTQ4VRZ9NKQFC`, `aip_01M63YV4D1BD9SXTHSNP01YY7A`): the new instance's Meta call again returned `provider_status=404`, `not_found_error`, cost 0, and the proposal failed. From this machine the same key's `GET /v1/models` returns 200 and lists `muse-spark-1.3` (the key was read into the shell from `.env` and sent from stdin, never printed). The provider code is unchanged since the last deploy whose proposals succeeded (f06ca92..5b0d354 touches only `internal/ai/provider/segment.go`). So the key and the model are good, and Meta refuses this Railway egress; a redeploy no longer clears it. The H-043 B loop stays **not confirmed live**; the three failed proposals are in the live list. The options (owner's call) are in T-057's findings: a static egress IP, or a network or region restriction on the key in Meta's dashboard.

**US East, then the key (owner's go-ahead, 2026-10-29).** 94538b6 moved the worker to `us-east4-eqdc4a`; `deploy.sh --apply` exit 0 (18 migrations recorded; api, worker, web SUCCESS; `/healthz`, `/readyz` 200), and the deployment's manifest reads `{"us-east4-eqdc4a": {"numReplicas": 1}}`. The next proposal (`aip_01M6410BMTTF5FJHM654BCRAAG`) still got 404 `not_found_error`, cost 0: three instances in two regions, all refused. The worker's `GEMINUS_MUSE_API_KEY` is the owner's own Railway variable (not set from `.env`) and is sealed, so it could not be compared with the local key. With the owner's go-ahead it was replaced by the `.env` key (sent on stdin, `railway variable set --stdin`; never printed or in argv), which triggered a worker deploy (`c8283beb`, SUCCESS). Then:

| "Propose as a scenario" | Result |
|---|---|
| "Repeat with more replications" (`aip_01M641JDQ7TENY14SHJDT1RDXV`) | **Needs input**, written by Muse Spark 1.3 (Meta): the replication count is a run setting, not a scenario change, so it proposes no patch and asks three questions. Cost USD 0.027. |
| "Test FCT follow up capacity" (`aip_01M641KT478Z20QRR1HMV4D6FA`) | **Ready for review**, written by Muse Spark 1.3 (Meta): FCT testers 3 → 4, buffer before FCT 10 → 20; Geminus: low risk, every check passes; the AI's confidence 72%. Not approved (a person's step). Cost USD 0.042. |

So the 404 was the key Railway held, not the egress: the T-057 notes that blamed the region or the instance were wrong (the redeploys that "fixed" it before are unexplained). The H-043 B loop is **confirmed live**. The worker stays in US East. Five failed proposals from today are in the live list.
