---
name: Geminus
description: Digital-twin modelling, simulation and decision support, drawn as a Marey graphic timetable.
colors:
  ground: "#EDEDEA"
  sheet: "#FFFFFF"
  panel: "#FFFFFF"
  raised: "#FFFFFF"
  well: "#F3F3F1"
  hover: "#F0F0ED"
  press: "#E5E5E1"
  time-grid: "#EDEDEA"
  time-grid-major: "#D9D9D4"
  rule: "#DADAD6"
  edge: "#808387"
  ink: "#1B1D1F"
  ink-2: "#4D5155"
  ink-3: "#62666A"
  ink-4: "#9A9DA0"
  on-ink: "#FFFFFF"
  baseline: "#3F4347"
  scenario: "#0B6B4B"
  scenario-wash: "#E8F2ED"
  on-scenario: "#FFFFFF"
  warning: "#8A4F00"
  warning-mark: "#B26B00"
  warning-wash: "#FDF3DD"
  warning-edge: "#D29A3A"
  error: "#B3261E"
  error-wash: "#FCEEEC"
  error-edge: "#D9837D"
  selection-tint: "#EFEFEC"
  text-selection: "#D6D9DB"
  avatar: "#3F4347"
  on-avatar: "#FFFFFF"
  veil: "rgba(27,29,31,.36)"
  ground-dark: "#111111"
  sheet-dark: "#171717"
  panel-dark: "#1B1B1B"
  raised-dark: "#232323"
  well-dark: "#0D0D0D"
  hover-dark: "#2A2A2A"
  press-dark: "#323232"
  time-grid-dark: "#1F1F1F"
  time-grid-major-dark: "#2E2E2E"
  rule-dark: "#2E2E2E"
  edge-dark: "#707070"
  ink-dark: "#EBEBEB"
  ink-2-dark: "#B5B5B5"
  ink-3-dark: "#949494"
  ink-4-dark: "#5E5E5E"
  on-ink-dark: "#111111"
  baseline-dark: "#C9C9C9"
  scenario-dark: "#5CC79E"
  scenario-wash-dark: "#242424"
  on-scenario-dark: "#0F0F0F"
  warning-dark: "#F0B44C"
  warning-mark-dark: "#F0B44C"
  warning-wash-dark: "#242424"
  warning-edge-dark: "#B07A1E"
  error-dark: "#F07A70"
  error-wash-dark: "#242424"
  error-edge-dark: "#B5534B"
  selection-tint-dark: "#262626"
  text-selection-dark: "#3B3B3B"
  avatar-dark: "#3A3A3A"
  on-avatar-dark: "#FFFFFF"
  veil-dark: "rgba(0,0,0,.62)"
typography:
  display:
    fontFamily: 'Archivo, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "22px"
    fontWeight: 600
    lineHeight: "28px"
    letterSpacing: "normal"
    fontFeature: '"tnum" 1, "lnum" 1'
  headline:
    fontFamily: 'Archivo, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "18px"
    fontWeight: 600
    lineHeight: "24px"
    letterSpacing: "normal"
    fontFeature: '"tnum" 1, "lnum" 1'
  title:
    fontFamily: 'Archivo, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "15px"
    fontWeight: 600
    lineHeight: "20px"
    letterSpacing: "normal"
    fontFeature: '"tnum" 1, "lnum" 1'
  figure:
    fontFamily: 'Archivo, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "15px"
    fontWeight: 400
    lineHeight: "20px"
    letterSpacing: "normal"
    fontFeature: '"tnum" 1, "lnum" 1'
  body:
    fontFamily: 'Archivo, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "13.5px"
    fontWeight: 400
    lineHeight: 1.4
    letterSpacing: "normal"
    fontFeature: '"tnum" 1, "lnum" 1'
  control:
    fontFamily: 'Archivo, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "13.5px"
    fontWeight: 500
    lineHeight: 1.4
    letterSpacing: "normal"
    fontFeature: '"tnum" 1, "lnum" 1'
  label:
    fontFamily: 'Archivo, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.35
    letterSpacing: "normal"
    fontFeature: '"tnum" 1, "lnum" 1'
  axis:
    fontFamily: 'Archivo, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.35
    letterSpacing: "normal"
    fontFeature: '"tnum" 1, "lnum" 1'
    fontVariation: '"wdth" 87.5'
rounded:
  sm: "4px"
  md: "6px"
  lg: "8px"
spacing:
  hair: "2px"
  xs: "4px"
  sm: "6px"
  md: "8px"
  cell: "10px"
  lg: "12px"
  group: "14px"
  gutter: "16px"
  section: "20px"
  xl: "24px"
  2xl: "32px"
  split: "40px"
components:
  button:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.control}"
    rounded: "{rounded.md}"
    padding: "0 12px"
    height: "32px"
  button-hover:
    backgroundColor: "{colors.hover}"
  button-active:
    backgroundColor: "{colors.press}"
  button-primary:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.on-ink}"
    typography: "{typography.control}"
    rounded: "{rounded.md}"
    padding: "0 12px"
    height: "32px"
  button-primary-hover:
    backgroundColor: "{colors.ink-2}"
  button-run:
    backgroundColor: "{colors.scenario}"
    textColor: "{colors.on-scenario}"
    typography: "{typography.control}"
    rounded: "{rounded.md}"
    padding: "0 12px"
    height: "32px"
  button-run-more:
    backgroundColor: "{colors.scenario}"
    textColor: "{colors.on-scenario}"
    width: "30px"
    height: "32px"
  button-quiet:
    backgroundColor: "transparent"
    textColor: "{colors.ink}"
    rounded: "{rounded.md}"
    height: "32px"
  button-quiet-hover:
    backgroundColor: "{colors.hover}"
  button-danger:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.error}"
    typography: "{typography.control}"
    rounded: "{rounded.md}"
    height: "32px"
  button-disabled:
    backgroundColor: "{colors.well}"
    textColor: "{colors.ink-4}"
  button-toggled:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.on-ink}"
  button-icon:
    size: "32px"
    rounded: "{rounded.md}"
  link:
    textColor: "{colors.ink}"
    typography: "{typography.control}"
  segmented-item:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    padding: "0 10px"
    height: "30px"
  segmented-item-mode:
    padding: "0 16px"
    height: "30px"
  segmented-item-hover:
    backgroundColor: "{colors.hover}"
  segmented-item-selected:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.on-ink}"
  input:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.figure}"
    rounded: "{rounded.md}"
    padding: "0 10px"
    height: "34px"
  stepper:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.figure}"
    rounded: "{rounded.md}"
    height: "34px"
  tag-scenario:
    textColor: "{colors.scenario}"
    typography: "{typography.label}"
    rounded: "10px"
    padding: "0 6px"
    height: "20px"
  tag-added:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.on-ink}"
    typography: "{typography.label}"
    rounded: "10px"
    padding: "0 6px"
    height: "20px"
  tag-removed:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.error}"
    typography: "{typography.label}"
    rounded: "{rounded.sm}"
    padding: "0 8px"
    height: "24px"
  tag-neutral:
    textColor: "{colors.ink-2}"
    typography: "{typography.label}"
    rounded: "{rounded.sm}"
    padding: "0 6px"
    height: "20px"
  chip-warning:
    backgroundColor: "{colors.warning-wash}"
    textColor: "{colors.warning}"
    typography: "{typography.label}"
    rounded: "{rounded.sm}"
    padding: "0 7px"
    height: "22px"
  chip-error:
    backgroundColor: "{colors.error-wash}"
    textColor: "{colors.error}"
    typography: "{typography.label}"
    rounded: "{rounded.sm}"
    padding: "0 7px"
    height: "22px"
  kbd:
    backgroundColor: "{colors.well}"
    textColor: "{colors.ink-2}"
    typography: "{typography.label}"
    rounded: "{rounded.sm}"
    padding: "3px 5px"
  app-strip:
    backgroundColor: "{colors.ground}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    padding: "0 16px"
    height: "48px"
  provenance-row:
    backgroundColor: "{colors.ground}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    padding: "0 16px"
    height: "44px"
  canvas:
    backgroundColor: "{colors.sheet}"
  canvas-caption:
    backgroundColor: "{colors.sheet}"
    textColor: "{colors.ink-2}"
    typography: "{typography.label}"
    rounded: "{rounded.md}"
    padding: "6px 10px"
  canvas-toolbar:
    backgroundColor: "{colors.sheet}"
    textColor: "{colors.ink}"
    typography: "{typography.control}"
    rounded: "{rounded.md}"
    padding: "3px"
  node:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "{rounded.sm}"
    width: "136px"
  node-header:
    padding: "0 12px"
    height: "39px"
  pool:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "22px"
    padding: "0 16px"
    height: "44px"
  issues-pill:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.control}"
    rounded: "16px"
    padding: "0 12px"
    height: "32px"
  callout-warning:
    backgroundColor: "{colors.warning-wash}"
    textColor: "{colors.warning}"
    typography: "{typography.label}"
    rounded: "{rounded.md}"
    padding: "0 10px"
    height: "32px"
  marey-strip:
    backgroundColor: "{colors.sheet}"
    textColor: "{colors.ink-2}"
    height: "220px"
  marey-header:
    typography: "{typography.body}"
    padding: "0 16px"
    height: "40px"
  marey-row-selected:
    backgroundColor: "{colors.selection-tint}"
    textColor: "{colors.ink}"
  side-panel:
    backgroundColor: "{colors.panel}"
    textColor: "{colors.ink}"
    padding: "20px"
    width: "400px"
  compare-scenario-cell:
    backgroundColor: "{colors.scenario-wash}"
    textColor: "{colors.scenario}"
    typography: "{typography.figure}"
  compare-row-selected:
    backgroundColor: "{colors.selection-tint}"
  note:
    backgroundColor: "{colors.well}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "{rounded.md}"
    padding: "10px 12px"
  note-pass:
    backgroundColor: "{colors.scenario-wash}"
  note-warning:
    backgroundColor: "{colors.warning-wash}"
  note-error:
    backgroundColor: "{colors.error-wash}"
  popover:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    rounded: "{rounded.md}"
    padding: "12px"
    width: "264px"
  menu:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    rounded: "{rounded.md}"
    padding: "4px"
    width: "340px"
  dialog:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    rounded: "{rounded.lg}"
    width: "560px"
  palette:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    rounded: "{rounded.lg}"
    width: "600px"
  palette-option-selected:
    backgroundColor: "{colors.selection-tint}"
    textColor: "{colors.ink}"
  toast:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.on-ink}"
    typography: "{typography.body}"
    rounded: "{rounded.md}"
    padding: "8px 8px 8px 14px"
  avatar:
    backgroundColor: "{colors.avatar}"
    textColor: "{colors.on-avatar}"
    typography: "{typography.label}"
    rounded: "50%"
    size: "32px"
---

# Design System: Geminus

## Overview

**Creative North Star: "The Graphic Timetable"**

Geminus is drawn like Marey's graphic train timetable: time runs left to right, the steps of a process stand top to bottom, and every item moving through the process is a line through them. A line going flat is an item waiting in a queue; a steep line is an item moving. The interface is one white timetable sheet laid on a pale neutral shell and inked in graphite, with exactly two data inks: dashed graphite for the baseline and solid deep green for the scenario. Amber warnings, red errors and gray structure stay out of their way.

The density is that of a working instrument for people who model all day and decide in 30 to 60 minute sittings: 13.5 px body text, 32 px controls, hairline rules, small corners, no decoration. Depth belongs only to what floats over the work (popovers, menus, drawers, dialogs, the palette, toasts); the sheet, the canvas objects and the panel are flat and separated by line. Provenance is part of the furniture: the version, draft, scenario and runs on screen are always named, and the canvas says whether it shows the editable draft or a read-only version.

Dark is the same timetable on a gray-black slate with chalk ink. Its neutrals have zero chroma, the scenario green lightens to a chalk green, and the line grammar does not change. The theme follows the system by default; the person can switch it and the choice is remembered. The world refuses the category default for modelling tools: a dot-grid canvas of floating soft-shadow cards with one blue accent.

The system is domain-neutral. The prototype's example data is the Software Delivery example pack (steps such as Code review, CI, QA test and Deploy; time in eight-hour working days; lead time and throughput). The P0 domain is now the electronics assembly/test line (H-016, ADR-025), and its screens are not designed yet. Examples quoted below come from the prototype and are marked as such. Step and resource names, the time unit, metric names and domain icons come from the DomainPack; no component may assume one domain.

This record is taken from the shipped workspace prototype (`checks/evidence/2026-10-19-T-045/prototypes/workspace.html`, finish review disposition ship, 2026-10-19). **Status: the owner adopted this system as the design basis, on the condition that the interface stays simple (H-027, 2026-10-26: "UI 都照建議，但我希望介面要是簡潔的"; "只保留所有應該要包含的按鈕功能等"). The owner confirmed the 32 re-skinned screenshot baselines, light and dark (H-028, TEST-033).** The Simplicity section below says how that is applied and checked; where this record and simplicity disagreed, the app takes the simpler choice, listed under Simplifications in the app. Token keys are the system's own names. Each color has a `-dark` sibling; components reference the light key, which resolves to its sibling in dark. The app's tokens (`apps/web/ui/tokens.css`) follow this record value for value under ADR-021's `--gm-*` custom properties (see Tokens in the app); `apps/web/tests/tokens.test.ts` fails when the two drift apart.

**Key Characteristics:**
- Two data inks with a fixed grammar: baseline dashed graphite, scenario solid deep green. The dash carries the difference; the hue confirms it.
- One family, Archivo, with tabular lining figures everywhere, five sizes, and narrower widths before smaller sizes.
- A flat white sheet, hairline rules, 4 to 8 px corners with capsules for resources and counts; shadows only on overlays.
- Provenance always on screen; the mode's primary action sits beside the object it acts on.
- A gray-black dark theme with zero-chroma neutrals; the default follows the system.
- One palette and one line grammar across every projection of the model: the 2D canvas, the Marey strip, Compare, and the read-only 3D view when it lands.
- Domain-neutral furniture: vocabulary, time unit, metrics and domain icons come from the DomainPack.

## Simplicity (H-027)

The owner adopted this system on one condition: the interface is simple and clean (簡潔), and it keeps only the buttons and controls each view needs. Five working principles make that checkable; the tests that enforce them are named after each.

1. **One primary action per view.** At most one Graphite Ink button on any screen; every other action is a default, quiet or danger button, or sits in a menu. A what-if run and a version action never share a slot (The Action Beside Its Object Rule). Checked: `apps/web/tests/simplicity.test.tsx` (every draft state, the gallery, failures, and the signed-in project list, project and model pages) and `apps/web-e2e/tests/simplicity.spec.ts` (every route as it renders signed out, both languages, 390 and 1280 px).
2. **Only the controls a view needs.** Every visible button, link or field does something that is built and tested today, or is a step of a flow that has shipped. No placeholder, "coming soon" or disabled-forever control; no two controls for the same action on one view. A control a spec or a test requires (skip link, language, theme, retry, sign-in recovery) stays; one that looks redundant but is required is kept and asked about, not removed. Checked in the browser (`apps/web-e2e/tests/simplicity.spec.ts`, both languages, 390 and 1280 px) on every route as it renders signed out: no disabled control outside the gallery, no two visible buttons with the same name and no two visible links to the same place. Checked in unit tests (`apps/web/tests/simplicity.test.tsx`, both languages, each view together with the app shell's controls): no two buttons or links with the same name and no two links to the same place on the start, not-found and sign-in error pages, in every draft state (also a conflict whose reload failed), in a failed load, and on the signed-in project list, project and model pages, loaded and failed, with a fake API and session; there also nothing disabled outright and at most one primary, and a control blocked for now (the editor's Save draft and Publish on the model page) names visible, non-empty text as its reason by `aria-describedby` (the rule bars placeholder and disabled-forever controls, not an action waiting on its condition); the other signed-in pages block nothing. Since the app mounts the signed-in bootstrap (T-043 follow-up), the browser also checks the signed-in views, signed in through the BFF and the test provider: the project list, a project, the model with the editor (as editor and as viewer; the editor's blocked actions must show their reason), the choice of organization, the project list with the organization switcher, and no access; the unit tests add the signed-in strip and every state of the bootstrap. The one pair kept for the owner's decision (H-028 kept it) is listed in both tests.
3. **Progressive disclosure.** What the current step does not need is collapsed or one click away: technical details and request IDs, legends, keyboard hints, provenance details. The app strip holds the product, the navigation, the language and the theme; once signed in also the account: Sign out, and the organization switcher for a user with several organizations (T-043 follow-up). Nothing else until a view has the function (no Find or mode switch yet). The sign-in is on the page that needs it (the sign-in-required notice), not in the strip. Checked by review; the problem view's technical details sit behind a "Details" disclosure (`app/_components/problem-view.tsx`).
4. **No decoration.** No gradient, ornamental icon, extra border, heavy shadow or animation beyond this system's: borders are 1 px hairlines (no side stripes), shadows only on what floats (popover, dialog) plus the invalid-field ring, motion only as 180 ms state-colour transitions. The palette is the graphite and paper neutrals, the scenario green (also a passed check) and the warning and error signals; nothing else. Checked: `apps/web/tests/design-rules.test.ts` (no colour, size, spacing, corner, shadow or transition outside the tokens; no gradient or keyframes; no border over 1 px) and `tokens.test.ts` (no hue outside the three families; dark neutrals have no chroma); in the browser, `simplicity.spec.ts` (every transition is 180 ms on that curve, and none is longer than 0.01 ms under reduced motion).
5. **Calm layout, plain words.** At most one secondary panel beside the main content; generous whitespace on one spacing scale; short, plain copy in English and Traditional Chinese, with no marketing language and no task IDs. Checked: the shell has one main region and no side panel (`layout.test.tsx`, `simplicity.spec.ts`); spacing only from the scale (`design-rules.test.ts`); copy by review (catalog changes are listed in `checks/T-045_VALIDATION.md`).

### Simplifications in the app

Where a component of this record was busier than the principles allow, the app is simpler, and the record keeps its original wording above for the screens still to be built:

- **Language and theme are one form.** The strip uses the design system's Select for both, each labelled (12 px Label Graphite, hidden visually on a phone and still the control's name), instead of a language segment and a cycling theme button. One form for both choices; every option is named; the arrow keys open the list and only a choice changes the page (WCAG 3.2.2); the strip stays one 48 px row on a 390 px phone in both languages. Segmented controls remain the form for the mode switch and 2D / 3D when T-015 builds them.
- **The account (T-043 follow-up).** Signed in, the strip ends in the account: Sign out (a quiet button) and, for a user with several organizations, the organization switcher (the strip's Select; a long name ends in an ellipsis). It shows nothing while signed out or while the session's organizations load. Of several organizations, the page asks once, as a list of buttons with the role beside each (no second way to choose while it asks); the choice is remembered for the browser session. On a phone there is no room left in the strip's row (8 to 14 px), so signed in the account takes one row of its own under it, ending at the same edge; the first row stays as it is signed out. Between 721 px and about 800 px in English the account can also wrap under the row, still ending at the right edge.
- **No entrance motion.** Overlays appear without the 4 px rise and fade; motion is only the 180 ms colour change of a control's state. The motion token is kept for the screens that need it (the playhead).
- **No icons in notes yet.** A note's tone is its edge and wash, with Graphite Ink text and a heading; icons come with the authored icon set.
- **No row hover in tables.** Rows show selection (Selected Row Tint) and focus (the ring), not the pointer; in dark, Control Edge on the Hover fill would fall below 3:1 for a checkbox in the row.
- **Page headings are Headline, Title and Body.** A page's h1 is 18 px, h2 15 px, h3 13.5 px at 600; Display stays for full-screen Compare.
- **An action's outcome is a framed status.** The in-page status message (ADR-021: no toast) is the Outcome frame: 1 px Graphite Ink, ink text, no colour.
- **The 2D editor (T-015 hackathon slice, H-027 Q2 flow).** The row above the stage holds the draft's status and revision and ends in the actions (Discard my changes only while there are unsaved edits, Save draft, Publish version, the one primary); the version and domain stay in the line under the model's name, and the Scenario and Runs groups come with Simulate. A blocked Save draft or Publish stays focusable as `aria-disabled` with its reason beside it (Buttons, Disabled). The step panel is an Add step menu in the canvas tool bar (no key caps, pools, undo or redo yet); the canvas has no caption box, issues pill, zoom group or 2D / 3D segment: the status row says what the canvas shows, a Problems list under the canvas replaces the drawer, scroll or pinch zooms, Show all steps fits the line, and Tab reaches every step. A selected step is a Graphite Ink border on Selected Row Tint without the ring shadow (principle 4), and an issue's border colour stays on it. The properties panel is the view's one secondary panel (principle 5), 360 px; the page with the editor takes the window's width, since a line runs wide. Connections are changed in a step's properties (and by dragging between handles), not as canvas objects, so Tab never stops on an edge.
- **One way to run, so no "More ways to run" segment** (T-032, hackathon slice). The run button runs the scenario beside its baseline with the settings above it; its hover darkens the edge to Graphite Ink instead of brightening the fill 8%, which would need a colour outside the tokens.
- **Results stand on their own page until the canvas has a Simulate mode** (T-032). The runs' state, the Compare table and the Marey chart are one page (`/scenarios/<id>/results`) with no side panel. The chart shows the whole run: no playhead, scrubber, play button or row buttons, because no canvas follows them yet; its numbers are a table behind a disclosure. The Compare table has no rugs, metric buttons or full screen, and under a mean it gives that run's 95% interval when the server has one (the API defines only that interval, for replication means), not a 90% interval under a median.
- **Asking the AI is one form on the model page; the review stands on its own page until the canvas has a Review mode** (T-031, hackathon slice). Above the editor: the request in the person's words (a text area), what the AI proposes (a Select: a change to the model or a scenario to simulate, the proposal's mode) and Send to the AI, a default button, so Publish stays the page's one primary; while the editor has unsaved edits, Send waits with its reason beside it, since the proposal opens on its own page. The review (`/proposals/<id>`) has no side panel and draws no canvas: its state, the AI's summary, the two risk cells (Geminus's check and validation in a Graphite Ink frame, the AI's claim and confidence in a hairline frame, the counted risk under them), the changes as hairline rows with Added (a Graphite Ink tag) and Removed (an Error Red tag) at 4 px corners (the scale has no capsule radius), what a new step holds under its row, the exact JSON Patch and its hash behind a disclosure, the AI's assumptions and expectations, then the decision under a Control Edge rule, confirmed in place. Approve and Reject are the only actions; there is no "Edit first" and no "Redo on vN" (asking again is the model page's one request). A proposal whose base is no longer the latest can never be approved, so Approve is not offered there and a warning note says why (no disabled-forever control); a 409 at approval keeps the page and puts a warning note, focused, where the decision was. The outcome is the Outcome frame without a check icon (no icons yet); after an approved scenario its next step is a link to the scenario.

### Tokens in the app

ADR-021 names every token `--gm-*` in `apps/web/ui/tokens.css`:

| DESIGN.md | App token |
|---|---|
| `colors.<key>` and `<key>-dark` | `--gm-color-<key>`; dark values under `prefers-color-scheme: dark` (unless `data-theme="light"`) and under `:root[data-theme="dark"]` |
| typography sizes 12, 13.5, 15, 18, 22 | `--gm-font-size-label`, `-body`, `-title`, `-headline`, `-display`, with `--gm-line-height-*` |
| the family and figures | `--gm-font-sans` (`"Archivo Variable"`, `"Noto Sans TC Variable"`, then PingFang TC, Microsoft JhengHei, system-ui), `--gm-font-features` |
| `spacing.<key>` | `--gm-space-<key>` |
| `rounded.<key>` | `--gm-radius-<key>` |
| Overlay shadow (light, dark) | `--gm-shadow-overlay` |
| focus | `--gm-focus-ring` (2 px Graphite Ink) |
| 180 ms `cubic-bezier(.2,.8,.2,1)` | `--gm-motion` |

The theme follows the system unless the reader chose one; the choice is the cookie `geminus_theme` (`light` or `dark`; none means the system's), which the root layout renders as `<html data-theme>` so the first paint is already right, with no script and nothing for a CSP to allow. The fonts are self-hosted from `@fontsource-variable/archivo` and `@fontsource-variable/noto-sans-tc` (OFL-1.1, `docs/THIRD_PARTY_NOTICES.md`).

## Colors

Near-neutral paper and graphite, one data green, and a separate amber and red signal pair; dark is pure gray-black with chalk inks.

Light neutrals lean faintly warm (OKLCH hue about 106°, chroma at most 0.007) and the inks faintly cool (hue about 248°, chroma at most 0.009). Dark neutrals have no chroma at all. On the build, text contrast measured at least 4.93:1 in light and 5.18:1 in dark across 87 states (finish review), and Control Edge stayed at least 3.17:1 against every surface a control sat on there, in both themes. In the app the lowest is 3.06:1, in dark, for a selected table row's checkbox on Selected Row Tint (3.31:1 in light); `apps/web/tests/tokens.test.ts` holds Control Edge at 3:1 or more against Sheet, Ground, Raised, Panel and Selected Row Tint in both themes.

### Primary
- **Graphite Ink** (#1B1D1F; dark Chalk Ink #EBEBEB): text; the primary button fill; toggled segments and toggle buttons; selection and focus rings; the playhead; skip links; the caret and native control accents. The interface's one action color. The prototype names focus and selection as their own properties; both resolve to Graphite Ink in both themes.
- **Baseline Graphite** (#3F4347; dark #C9C9C9): the baseline run wherever it is drawn: dashed lines in the Marey strip, dashed utilisation bars on nodes, the dashed key in Compare headers, the strip and the provenance row, baseline ticks and intervals in the rugs.
- **Reverse Ink** (#FFFFFF; dark #111111): text and icons on Graphite Ink: primary buttons, toggled segments, toasts, skip links, the playhead's time flag.

### Secondary
- **Deep Scenario Green** (#0B6B4B; dark Chalk Green #5CC79E): the scenario run: solid lines in the strip, solid bars on nodes, scenario values (600) after the arrow, the scenario column header and figures in Compare, the run button, the scenario tag on changed steps, the branch icon in the provenance row. It also marks a passed check: the all-checks-pass note, the no-issues pill, the check in an outcome.
- **Scenario Wash** (#E8F2ED; dark #242424): the scenario column in Compare and the passed-check note. In dark it is a neutral gray, not a green tint.
- **On Scenario** (#FFFFFF; dark #0F0F0F): the label on the run button.

### Tertiary
- **Warning Amber** (#8A4F00; dark #F0B44C): warning text in chips, callouts, field warnings and the provenance row (a newer version exists: "v4 is newer" in the prototype).
- **Amber Mark** (#B26B00; dark #F0B44C): warning icons. Non-text only (4.20:1 on white).
- **Amber Wash** and **Amber Edge** (#FDF3DD and #D29A3A; dark #242424 and #B07A1E): fill and border of warning chips, callouts and notes; the leader line from a step to its callout.
- **Error Red** (#B3261E; dark #F07A70): error text and icons, invalid field borders, danger button labels, failed-run messages, removed edges and the removed tag in a proposal diff.
- **Error Wash** and **Error Edge** (#FCEEEC and #D9837D; dark #242424 and #B5534B): fill and border of error chips and notes.

### Neutral
- **Pale Shell** (#EDEDEA; dark #111111): the app strip and provenance row around the sheet.
- **Timetable White** (#FFFFFF; dark Slate #171717): the canvas, the Marey strip and the canvas chrome boxes.
- **Panel** (#FFFFFF; dark #1B1B1B): the side panel.
- **Raised** (#FFFFFF; dark #232323): controls, nodes, pools and overlays.
- **Inset Well** (#F3F3F1; dark #0D0D0D): disabled buttons, key caps, neutral notes, the run progress track and the distribution preview's fill.
- **Hover** and **Press** (#F0F0ED and #E5E5E1; dark #2A2A2A and #323232): pointer feedback on controls, rows and options.
- **Minor Time Grid** and **Major Time Grid** (#EDEDEA and #D9D9D4; dark #1F1F1F and #2E2E2E): the Marey time grid, a minor line every quarter of the time unit and a major line every unit, and the station line on the sink row. No other surface carries a grid.
- **Hairline Rule** (#DADAD6; dark #2E2E2E): dividers between content: table rows, sections, overlay borders, provenance separators, the canvas chrome boxes, utilisation-bar tracks.
- **Control Edge** (#808387; dark #707070): every boundary that identifies a control or a model object: buttons, inputs, segments, nodes, pools; also the panel's left edge, the provenance row's bottom edge, the strip's top edge, the Compare header rule, the rug axis and the scrollbar thumb.
- **Secondary Graphite** (#4D5155; dark #B5B5B5): secondary text, units, captions, the change arrow, icons beside labels.
- **Label Graphite** (#62666A; dark #949494): field and provenance labels, axis ticks, issue paths, palette group names, pool links.
- **Disabled Graphite** (#9A9DA0; dark #5E5E5E): disabled text only.
- **Selected Row Tint** (#EFEFEC; dark #262626): the selected row band in the Marey strip, the selected Compare row, the active palette option, the selected step on phone.
- **Text Selection** (#D6D9DB; dark #3B3B3B): the `::selection` background under ink text.
- **Avatar Graphite** and **On Avatar** (#3F4347 and #FFFFFF; dark #3A3A3A and #FFFFFF): the account avatar's fill and initials.
- **Modal Veil** (rgba(27,29,31,.36); dark rgba(0,0,0,.62)): behind dialogs and the palette.

### Named Rules
**The Two Inks Rule.** Baseline is dashed graphite and scenario is solid deep green, in every chart, bar, key and legend. The two inks are only 1.5:1 apart in light and 1.25:1 in dark, so the dash carries the difference and hue is the second cue, never the only one.

**The Green Means Scenario Rule.** Green marks the scenario run or a passed check and nothing else: never brand, selection, links, focus or decoration. Selection and focus are Graphite Ink.

**The Gray-Black Rule.** Every dark neutral, from the inset well (#0D0D0D) to Chalk Ink (#EBEBEB), has zero chroma: no blue, no warm cast. In dark every wash collapses to one neutral gray (#242424) and hue lives only in ink and edge.

## Typography

**Display Font:** Archivo (variable: width 75–100, weight 400–700), with Noto Sans TC, PingFang TC, Microsoft JhengHei, system-ui
**Body Font:** Archivo, the same family
**Label/Mono Font:** none; key caps and figures are Archivo too

**Character:** A plain grotesque with the sobriety of an engineering drawing and a real width axis, so a timetable can narrow its labels instead of shrinking them. Tabular lining figures (`"tnum" 1, "lnum" 1`, set on the body) make every column of numbers line up. Noto Sans TC is the CJK face for zh-Hant only; there is no second Latin family. Both faces are OFL.

### Hierarchy
- **Display** (600, 22px, 28px): full-screen Compare only: the panel title, median figures, Δ and the rug median values.
- **Headline** (600, 18px, 24px): panel and dialog titles, claim values in a proposal, the 3D placeholder title; the brand at 700 with -0.01em tracking; in full-screen Compare, metric, row and run names.
- **Title** (600, 15px, 20px): popover and outcome headings, the phone's step-list heading and model title, rug headings in full screen.
- **Figure** (400, 15px, 20px, tabular): every result number: Compare medians, node and step values, pool counts (at 600), inputs and steppers. The 90% interval under a median is set at 87.5% width in Secondary Graphite. The spoken summary above the Compare table shares this size and weight (line-height 1.35) and steps up to 18 px in full screen.
- **Body** (400, 13.5px, 1.4): default text, node text (line-height 1.3), row labels, segmented items, notes.
- **Control** (500, 13.5px, 1.4): buttons and links. Primary buttons and toggled segments use 600.
- **Label** (400, 12px, 1.35): field and provenance labels, captions, units, help text. Chips, tags and table headers use 600; key caps and neutral tags 500.
- **Axis** (400, 12px, 87.5% width): time numbers and labels on the Marey time axis.

### Named Rules
**The Five Sizes Rule.** Five sizes only: 12, 13.5, 15, 18 and 22 px. Hierarchy comes from weight (400, 500, 600) and from Graphite Ink against Secondary Graphite, never from a sixth size.

**The Fifteen-Pixel Figure Rule.** A result number is never smaller than 15 px. Twelve pixels is for what the number is, not for the number.

**The Narrow Before Small Rule.** When room runs out, Archivo narrows (87.5% for Compare row names at 1366 px and below and for the phone's timetable labels, 75% for intervals at 1366 px and below) before any size drops.

## Layout

The frame is a 48 px app strip over a 44 px provenance row over the stage. Both rows sit on Pale Shell; the strip ends in a hairline and the provenance row ends in Control Edge, marking where the sheet begins. The strip holds the product and breadcrumb on the left, the Edit / Simulate / Review switch in the centre, and Find (with its key hint), language, theme and account on the right. The provenance row holds label and value groups divided by hairline verticals with 14 px either side (Version, Draft, Scenario, Runs) and ends at the right edge in the mode's actions: in Edit, Save draft and then Publish; in Simulate, the split run button. A blocked action's 12 px reason sits to its left. Review has no row action; its decision stays in the panel.

The stage is the canvas column beside a side panel 400 px wide (360 px at 1366 px and below), divided by Control Edge. The canvas column is the model canvas on Timetable White above the Marey strip (a 40 px header plus a 180 px timetable, 220 px in all, under a Control Edge top rule). Canvas chrome floats 16 px in from the canvas edges: top right, the 2D / 3D and zoom groups and the fit button. In Simulate and Review the caption takes the top left (at most 460 px). In Edit the tool bar takes the top left and the caption moves to the bottom left, after the issues pill. The panel pads 20 px, opens with its heading block 16 px above the first section, spaces sections 20 px apart and places a section's header 10 px above its content.

Full-screen Compare removes the canvas column and gives the panel the stage at 24 px top and bottom and 32 px at the sides, in two columns (1.1fr and 1fr, 40 px gap) with the rugs in a two-column grid. Every role steps up for the projector: labels and captions 12 to 15 px, metric and run names 13.5 to 18 px, figures and Δ 15 to 22 px.

Spacing runs tight inside a group and opens between groups: 6 px from icon to label, 8 px between controls, 10 to 12 px inside rows, nodes and notes, 14 px around provenance groups, outcomes and toast text, 16 px for gutters and the canvas inset, 20 px for the panel and its sections, 24 to 40 px in full-screen Compare. Every outer gutter is `max(16px, env(safe-area-inset-*))`.

Breakpoints: at 1366 px and below the panel narrows to 360 px, the scrubber to 120 px and Compare tightens its cell padding to 3 px. At 1100 px and below the strip's key drops its flat-and-steep reading hint and lower-priority provenance groups leave. At 720 px and below is the phone layout: the page scrolls; the strip becomes two rows (product and compact tools, then a full-width Simulate / Review switch, with no Edit); provenance becomes a two-column grid under the model title; the row actions leave; the panel comes first, then the Marey strip (160 px timetable, its key wrapping and the reading hint back), then a list of the steps at the playhead's time in place of the canvas. The phone shows results and proposal review only; in Edit it says editing needs a larger screen and offers Simulate and Review.

### Named Rules
**The Action Beside Its Object Rule.** The mode's primary action ends the provenance row, next to the version, draft or scenario it acts on ("Publish v4…" in Edit, "Run S-2 again" in Simulate). A what-if run and a version action never share a slot.

**The Provenance Always Rule.** On every desktop mode the provenance row names the version, draft, scenario and runs, and the canvas caption says what the canvas shows (the editable draft and its base, or a read-only version with its patch), plus seed, engine, dataset and whether the data is synthetic.

## Elevation & Depth

The system is flat. The sheet, nodes, pools, the panel and notes sit on one plane and are separated by Hairline Rule and Control Edge, never by shadow. Only surfaces that float over the work carry a shadow, and they enter by fading in and rising 4 px over 180 ms. Two state rings are drawn with `box-shadow` but are not depth: the selection ring and the invalid-field ring.

### Shadow Vocabulary
- **Overlay** (`box-shadow: 0 1px 2px rgba(27,29,31,.10), 0 10px 28px rgba(27,29,31,.16)`): popovers, menus, the issues drawer, dialogs, the palette and toasts.
- **Overlay, dark** (`box-shadow: 0 0 0 1px #3A3A3A, 0 12px 32px rgba(0,0,0,.6)`): the same surfaces in dark; the 1px ring gives an overlay the edge a shadow alone cannot on gray-black.
- **Selection ring** (`box-shadow: 0 0 0 3px <Timetable White>, 0 0 0 5px <Graphite Ink>`): a selected node or pool, with its border in Graphite Ink. A state, not a depth.
- **Invalid field** (`box-shadow: inset 0 0 0 1px <Error Red>`): doubles the red border on an invalid input.

### Named Rules
**The Overlays Only Rule.** Only what floats over the work casts a shadow. Nodes are never lifted; a node on a soft shadow is the category default this world refuses.

## Shapes

Small corners and hairlines. Objects inside the sheet take 4 px (nodes, chips, neutral and removed tags, key caps, list rows and menu items); controls and containers take 6 px (buttons, inputs, segmented controls, notes, popovers, menus, toasts, the canvas caption and tool bar); the two modal surfaces, dialog and palette, take 8 px. Capsules, with the radius at half the height, mark counts and resources rather than steps: pools (44 px tall, 22 px radius), the issues pill (32, 16), scenario and added tags (20, 10), the mode count (18, 9), the progress bar (6, 3) and the round avatar.

Borders are 1 px. Two pixels means "added" in a proposal diff (node, pool and edge in Graphite Ink). Dashes carry meaning and nothing else: the baseline (4 on, 2.5 off for short marks and bars; 5 on, 3 off for the long timetable lines) and removed edges (6 on, 4 off, in Error Red with a red cross). Flow arrows end in open chevrons.

### Named Rules
**The Square Steps, Round Resources Rule.** Steps are 4 px rectangles; resource pools are capsules. Shape alone tells a step from a pool on the canvas.

## Components

### Buttons
Squared, quiet and exact: a button looks like a control, not a call to action.
- **Shape:** gently squared corners (6px), 32 px tall, 12 px side padding, 1 px Control Edge border, Raised fill, Control type (13.5 px, 500).
- **Primary:** Graphite Ink fill with a Reverse Ink 600 label; hover to Secondary Graphite. Publish, Approve, Save and the post-outcome next step.
- **Run:** Deep Scenario Green fill with an On Scenario label, joined to a 30 px chevron segment ("More ways to run") by a hairline of On Scenario at 40%; hover brightens 8%. Used only to run a scenario.
- **Hover / Focus:** Hover and Press washes, 180 ms ease-out; focus is a 2 px Graphite Ink outline 2 px out.
- **Quiet:** no border or fill until hover: dialog close, tool bar tools, toast close.
- **Danger:** Error Red label on the default chrome (Reject proposal, Discard, Delete).
- **Disabled:** Inset Well fill, Hairline border, Disabled Graphite label. A blocked action stays focusable as `aria-disabled` and its reason is on screen beside it and linked as its description.
- **Toggled:** a pressed toggle fills Graphite Ink, like a selected segment.
- **Link:** Graphite Ink with a 1 px underline 3 px below; 2 px on hover.

### Segmented Controls
One form for every one-of-n choice: the mode switch, language (EN / 中文), 2D / 3D, and field choices. A Control Edge outline with 6 px corners, Raised fill, 30 px items divided by Control Edge, 10 px item sides (16 px in the mode switch, 8 px and 12 px type in the 2D / 3D group); the selected item fills Graphite Ink with a 600 Reverse Ink label. The mode switch carries a capsule count of waiting proposals.

### Chips
- **Scenario tag:** 20 px capsule, 1 px Deep Scenario Green border, 12 px 600 green label ("S-2"), on steps the scenario changes.
- **Added tag:** a Graphite Ink capsule ("Added", "New in v4") flagged across the node's top edge.
- **Removed tag:** 24 px, 4 px corners, 1 px Error Red border on Raised, a 12 px 600 red label with an x icon, placed over a removed edge.
- **Status chips:** 22 px, 4 px corners, wash fill, edge border, 12 px 600 text with a 13 px icon; warning in amber, error in red.
- **Neutral tag:** 20 px, 4 px corners, Hairline border, Secondary Graphite at 500.
- **Review step-up:** Review draws the canvas at about 82%, so its tags take 13.5 px type (22 px tall; the removed tag 28 px) to stay legible.

### Cards / Containers
The model canvas has no cards in the usual sense; its containers are steps and pools.
- **Step node:** 4px corners, Raised fill, 1 px Control Edge, flat (see Elevation & Depth). A 39 px header (12 px inset, name at 600) above a hairline; the body is label (12 px) and value (15 px) pairs; utilisation is two square-ended strokes on 1 px Hairline tracks, dashed graphite baseline (2 px) above solid green scenario (2.5 px). A process step is 136 px wide (152 px while editing); sources and sinks run 116 to 140 px.
- **States:** hover darkens the edge to Secondary Graphite; selected adds the selection ring and a Graphite Ink border; focus is a 2 px outline 4 px out (9 px when selected, clearing the ring); error turns the border Error Red; added is a 2 px Graphite Ink border.
- **Resource pool:** a 44 px capsule; icon and name in Secondary Graphite, count at 15 px 600.
- **Links:** flow arrows 1.5 px Secondary Graphite with open chevrons (Graphite Ink and 2 px when added); pool links 1 px Label Graphite with hollow end dots of 2.5 px radius; a warning leader in 1 px Amber Edge to a 32 px amber callout.

### Canvas Chrome
- **Caption:** a 6 px Timetable White box with a Hairline border saying what the canvas shows ("Read-only: v3 with patch S-2", or the draft and its base in Edit) at 13.5 px 600 after a lock or pencil icon, then runs, seed, engine, dataset and the synthetic-data label at 12 px; in Review it adds the key for added and removed.
- **Tool bar (Edit):** a 6 px Timetable White box with a Hairline border and 3 px inset holding quiet 30 px tools with their key caps (add step, add pool), undo and redo, divided by 20 px hairline verticals.
- **View and zoom:** the 2D / 3D segment, a zoom group (minus, percentage, plus) and a fit icon button, top right.
- **Issues pill (Edit):** a 32 px capsule with the worst issue's icon (Error Red, Amber Mark, or a green check when clean) and a count summary; it opens the issues drawer.

### Inputs / Fields
- **Style:** 34 px, 6 px corners, 1 px Control Edge, Raised fill, Figure type (15 px), 10 px inset. Label at 13.5 px 600 above; help in 12 px Secondary Graphite below.
- **Focus:** a 2 px Graphite Ink outline 1 px out; hover darkens the edge to Secondary Graphite.
- **Error / Disabled:** an invalid field turns its border Error Red, doubled by the inset ring, with a 12 px red message and 13 px icon below that names the problem and the allowed range. A warning uses Warning Amber text with an Amber Mark icon.
- **Stepper:** minus, value and plus as one 34 px control: 34 px square buttons around a 64 px centred value, divided by Control Edge; invalid turns the frame and the value red.
- **Distribution inputs:** three equal columns with a small curve preview below, drawn in Graphite Ink on Inset Well, or Error Red on Error Wash when invalid.

### Navigation
- **App strip:** 48 px on Pale Shell. The brand at 18 px 700, then a hairline divider; the breadcrumb (project in Secondary Graphite, model at 600); the mode switch centred; Find with its key hint, the language segment, the theme button (System, Light, Dark) and a 32 px round avatar on the right.
- **Provenance row:** 12 px Label Graphite labels before icon and value groups with values at 600: Version (versioned check), Draft (pencil, revision, base, a hollow dot with the unsaved count), Scenario (green branch, "patch on v3"), Runs (a solid green key for the scenario run, a dashed graphite key for the baseline).
- **Mobile:** see Layout. The switch spans the width and offers Simulate and Review only.

### Compare Table and Rugs
The decision instrument: built to be read across a meeting room.
- **Table:** metric names are underlined 13.5 px 600 buttons that select the metric's rug (Control Edge underline; Graphite Ink and 2 px when selected); columns run baseline, scenario, Δ, Δ%. Headers carry the line key and the run's short name at 12 px 600 over its run id at 12 px 400 in Secondary Graphite, above a Control Edge rule; rows divide with Hairline Rule.
- **Scenario column:** Scenario Wash fill, green header, green figures at 600.
- **Figures:** the median in Figure type, the 90% interval beneath it at 87.5% width; Δ and Δ% at 15 px 600. A selected row takes Selected Row Tint while the scenario cell keeps its wash.
- **Words around it:** a spoken summary leads, naming the metric, the baseline and scenario medians with their unit, and the relative change (in the prototype: "Median lead time 3.9 → 2.9 working days: 26% shorter."); a 12 px caption says what the figures are. Value pairs are written baseline, arrow (Secondary Graphite, read aloud as "to"), scenario, with the scenario value in green at 600.
- **Rugs:** one tick per run (baseline 1 px graphite, scenario 1.25 px green), stacked dots where runs tie (hollow for baseline, filled green for scenario); an interval bar with end caps (dashed for baseline, solid for scenario); a tall 2.5 px median mark topped by a dot, with its value at 15 px 600 haloed in the panel color; and a filled triangle under the sampled run. The axis is Control Edge.

### Marey Strip
The signature: a string diagram in which a queue shows as a line going flat.
- **Header (40 px):** a 32 px play / pause icon button, the playhead's position at 13.5 px 600 (in the prototype: "Working day 34 of 90"), a 200 px scrubber, the sampling note, and the line key at 12 px (baseline, scenario, and the flat-and-steep reading hint).
- **Timetable (180 px):** a label column of row buttons (selected row at 600 Graphite Ink with a Selected Row Tint band across the whole strip), time numbers on an 18 px axis band, about 112 px per time unit, the minor and major time grid, hairline row rules and one station line on the sink row where every item's release lands.
- **Lines:** baseline 1.4 px dashed, stacking down from each row's top; scenario 1.75 px solid, stacking up from its bottom; lanes 2.5 px apart and 3 px in from the row edges; release dots of 2.2 px radius, hollow for baseline and filled green for scenario. Only a labelled sample of items is drawn (in the prototype: "every 3rd work item").
- **Playhead:** a 1.5 px Graphite Ink line under a Graphite Ink time flag (12 px 600 Reverse Ink). Scrubbing or playing moves one playhead that the canvas follows; selecting a row selects its step, and selecting a step lights its row. Playback advances 1.5 time units a second and moves linearly; with reduced motion it jumps one unit every 650 ms.

### Overlays
- **Popover, menu, drawer:** 264 px, 340 px and up to 560 px; Raised fill, Hairline border, 6 px corners, the Overlay shadow; they fade in and rise 4 px.
- **Run menu:** each option is a 600 title over a 12 px line saying exactly what will run (version, patch, seeds, arrivals). A blocked option greys its title to Disabled Graphite and says why ("Drafts don't run. Publish the draft as a version first.").
- **Issues drawer:** rows of icon, message and a 12 px path; red icon for errors, Amber Mark for warnings.
- **Toast:** Graphite Ink fill, Reverse Ink text, 6 px corners, bottom centre, at most two at once, with an outlined action; gone after 7 s, or 12 s with an action.

### Dialogs, Confirmations and the Palette
- **Publish dialog:** up to 560 px, 8 px corners, header, body and footer split by hairlines, under the Modal Veil at 12vh. The title (18 px 600) names the version to be made; the body states what will happen, lists the changes since the base (added in Graphite Ink, removed in Error Red and struck through) and the checks as notes; the footer gives the reason, then Cancel, then the primary action, which stays `aria-disabled` while blocked. Focus moves in and returns to the opener.
- **In-place confirmation:** approving or rejecting a proposal confirms in the panel's footer, not in a modal: the question at 600, then the committing action (Approve in Graphite Ink, Reject in Error Red) and Cancel.
- **Command palette:** ⌘K or Ctrl K; up to 600 px, 8 px corners; a 50 px search field whose focus is a 2 px Graphite Ink underline; 12 px 600 Label Graphite group names; 36 px option rows. The active option takes Selected Row Tint and 600 weight, never a stripe; disabled options say why; an empty state says what was searched.

### Notes and Outcomes
- **Note:** 10 px 12 px inset, 6 px corners, icon and text in Graphite Ink: neutral (Hairline and Inset Well), passed (Deep Scenario Green edge on Scenario Wash), warning (Amber Edge on Amber Wash), error (Error Edge on Error Wash).
- **Outcome:** after publish, approve or run, a 1 px Graphite Ink frame with 6 px corners and 14 px inset: a heading with a green check, what happened, and the next action.
- **Proposal risk:** two cells side by side: Geminus's own check in a Graphite Ink frame (the authority) and the AI's claim in a Hairline frame, values at 18 px 600.

### Browser Surfaces and Icons
- **Focus and selection:** a 2 px Graphite Ink outline, 2 px out (4 px on canvas objects, inset on the panel); the caret and native accents in Graphite Ink; text selection on Text Selection; thin scrollbars with a Control Edge thumb.
- **Skip links:** hidden until focused, then a 6 px Graphite Ink button with a 600 Reverse Ink label at the top left.
- **Key caps:** Archivo 12 px 500 in Secondary Graphite on Inset Well with a Hairline border and 4 px corners; never monospace.
- **Icons:** one authored SVG set, 16 px (13 px small, 12 px in tags), 1.5 px stroke, round caps and joins, drawn in currentColor.

## Do's and Don'ts

### Do:
- **Do** draw every baseline as dashed Baseline Graphite and every scenario as solid Deep Scenario Green, in charts, node bars, keys and legends alike (The Two Inks Rule).
- **Do** write a change as baseline → scenario with the scenario value in green at 600, and give Δ, Δ% and the 90% interval beside the median.
- **Do** keep the provenance row and the canvas caption on screen in every desktop mode, and label synthetic and sampled data where it is shown.
- **Do** end the provenance row with the mode's primary action: Publish in Graphite Ink, a scenario run in Deep Scenario Green.
- **Do** draw any boundary that identifies a control or a model object in Control Edge (at least 3:1); keep Hairline Rule for dividing content.
- **Do** keep a blocked action focusable as `aria-disabled` with its reason written beside it.
- **Do** narrow Archivo to 87.5% or 75% before reducing a size, and step every role up one size for full-screen Compare.
- **Do** animate state changes only, at 180 ms with `cubic-bezier(.2,.8,.2,1)`; under reduced motion, switch transitions off and let the playhead jump one time unit at a time.
- **Do** take step names, resource names, the time unit, metric names and domain icons from the DomainPack.

### Don't:
- **Don't** tell baseline and scenario apart by hue alone; the two inks are barely 1.5:1 apart.
- **Don't** use green for brand, selection, links, focus or decoration; it means scenario or passed.
- **Don't** let any blue or tinted neutral into the dark theme; its neutrals stay zero-chroma gray-black.
- **Don't** put a dot grid or graph paper behind the model canvas, and don't float nodes on soft shadows; the only grid in the world is the Marey time grid.
- **Don't** use Disabled Graphite for anything but disabled text.
- **Don't** add a sixth type size, a second Latin family or a monospace face.
- **Don't** mark an active option or row with a colored stripe; use Selected Row Tint and weight.
- **Don't** offer topology editing on a phone; the phone shows results and proposal review only.
- **Don't** build one domain's vocabulary, units or icons into a component; the prototype's Software Delivery names are example data, not the system.
