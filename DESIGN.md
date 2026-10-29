---
name: Geminus
description: Digital-twin modelling, simulation and decision support, in a developer-tool look (H-041) with the Marey timetable's data grammar.
colors:
  ground: "#FAFAFA"
  sheet: "#FFFFFF"
  panel: "#FFFFFF"
  raised: "#FFFFFF"
  well: "#F4F4F4"
  hover: "#F2F2F2"
  press: "#E8E8E8"
  time-grid: "#F0F0F0"
  time-grid-major: "#E3E3E3"
  rule: "#EBEBEB"
  rule-strong: "#E0E0E0"
  edge: "#8A8A8A"
  ink: "#0A0A0A"
  ink-2: "#525252"
  ink-3: "#6B6B6B"
  ink-4: "#A3A3A3"
  on-ink: "#FFFFFF"
  baseline: "#6B6B6B"
  scenario: "#08784E"
  scenario-wash: "#E8F5EE"
  on-scenario: "#FFFFFF"
  accent: "#6D4AFF"
  accent-fill: "#6D4AFF"
  accent-wash: "#F4F1FF"
  on-accent: "#FFFFFF"
  warning: "#955400"
  warning-mark: "#B26B00"
  warning-wash: "#FDF3E1"
  warning-edge: "#D29A3A"
  error: "#C42430"
  error-wash: "#FDEBEC"
  error-edge: "#F0A6AB"
  selection-tint: "#F0F0F0"
  text-selection: "#DCDCDC"
  avatar: "#0A0A0A"
  on-avatar: "#FFFFFF"
  veil: "rgba(0,0,0,.4)"
  ground-dark: "#0A0A0A"
  sheet-dark: "#111111"
  panel-dark: "#111111"
  raised-dark: "#171717"
  well-dark: "#161616"
  hover-dark: "#1F1F1F"
  press-dark: "#262626"
  time-grid-dark: "#1A1A1A"
  time-grid-major-dark: "#262626"
  rule-dark: "#232323"
  rule-strong-dark: "#2E2E2E"
  edge-dark: "#6E6E6E"
  ink-dark: "#EDEDED"
  ink-2-dark: "#A1A1A1"
  ink-3-dark: "#8A8A8A"
  ink-4-dark: "#525252"
  on-ink-dark: "#0A0A0A"
  baseline-dark: "#A1A1A1"
  scenario-dark: "#3DD68C"
  scenario-wash-dark: "#0D281B"
  on-scenario-dark: "#0A0A0A"
  accent-dark: "#9D8FFF"
  accent-fill-dark: "#6D4AFF"
  accent-wash-dark: "#1B1735"
  on-accent-dark: "#FFFFFF"
  warning-dark: "#F5B14C"
  warning-mark-dark: "#F5B14C"
  warning-wash-dark: "#2A2010"
  warning-edge-dark: "#B07A1E"
  error-dark: "#FF6B6B"
  error-wash-dark: "#2C1416"
  error-edge-dark: "#7D3438"
  selection-tint-dark: "#1C1C1C"
  text-selection-dark: "#333333"
  avatar-dark: "#EDEDED"
  on-avatar-dark: "#0A0A0A"
  veil-dark: "rgba(0,0,0,.7)"
typography:
  display:
    fontFamily: 'Geist, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "26px"
    fontWeight: 600
    lineHeight: "32px"
    letterSpacing: "-0.025em"
    fontFeature: '"tnum" 1, "lnum" 1'
  headline:
    fontFamily: 'Geist, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "20px"
    fontWeight: 600
    lineHeight: "28px"
    letterSpacing: "normal"
    fontFeature: '"tnum" 1, "lnum" 1'
  title:
    fontFamily: 'Geist, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "15px"
    fontWeight: 600
    lineHeight: "22px"
    letterSpacing: "normal"
    fontFeature: '"tnum" 1, "lnum" 1'
  figure:
    fontFamily: 'Geist, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "15px"
    fontWeight: 400
    lineHeight: "22px"
    letterSpacing: "normal"
    fontFeature: '"tnum" 1, "lnum" 1'
  body:
    fontFamily: 'Geist, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.5
    letterSpacing: "normal"
    fontFeature: '"tnum" 1, "lnum" 1'
  control:
    fontFamily: 'Geist, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "13px"
    fontWeight: 500
    lineHeight: 1.5
    letterSpacing: "normal"
    fontFeature: '"tnum" 1, "lnum" 1'
  label:
    fontFamily: 'Geist, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.4
    letterSpacing: "normal"
    fontFeature: '"tnum" 1, "lnum" 1'
  axis:
    fontFamily: 'Geist, "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif'
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.4
    letterSpacing: "normal"
    fontFeature: '"tnum" 1, "lnum" 1'
rounded:
  sm: "6px"
  md: "8px"
  lg: "12px"
  full: "999px"
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
    backgroundColor: "transparent"
    textColor: "{colors.ink-2}"
    typography: "{typography.label}"
    padding: "0 10px"
    height: "26px"
  segmented-item-mode:
    padding: "0 16px"
    height: "26px"
  segmented-item-hover:
    textColor: "{colors.ink}"
  segmented-item-selected:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
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
    backgroundColor: "{colors.ground}"
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
    backgroundColor: "{colors.sheet}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "{rounded.lg}"
    width: "168px"
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
    width: "360px"
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

**Creative North Star: "A twin you operate like infrastructure" (H-041)**

The owner found the earlier Graphic Timetable world too plain ("太素了感覺根本沒有設計過的痕跡") and named the products whose craft Geminus should sit beside: Railway, Vercel / Geist and Figma / Framer (H-041, 2026-10-29). The app now looks like a developer tool of that family: a near-white page (near-black in dark) with white cards on it, hairline borders and a hair of shadow, Geist type with tight display tracking, status as pills, and one accent, a violet, that marks the AI, selection and focus. The 2D model is a design-tool canvas: a dot grid, steps as raised cards, the tools floating over it, an inspector at the right.

What did not change is the data grammar, which comes from Marey's graphic timetable: time runs left to right, the steps of a process stand top to bottom, and every item is a line through them, flat while it waits and steep while it moves. Exactly two data inks carry every comparison: dashed gray for the baseline and solid green for the scenario. Green means scenario (or a passed check) and nothing else; the accent never stands for a run.

The density is still that of a working instrument for people who model all day and decide in 30 to 60 minute sittings: 13 px body text, 32 px controls, 12 px card corners. Provenance is part of the furniture: the version, draft, scenario and runs on screen are always named. Dark is the same system on #0A0A0A with zero-chroma neutrals; the hues' washes are tinted. The theme follows the system by default (the owner's choice in H-041); the person can switch it and the choice is remembered.

The system is domain-neutral. Step and resource names, the time unit, metric names and domain icons come from the DomainPack; no component may assume one domain. The approved direction is recorded as three mocks in `checks/evidence/2026-10-29-h041-visual/`. **Status: adopted by the owner (H-041), on the standing condition that the interface keeps only what each view needs (H-027).** The Simplicity section below says how that is applied and checked. Token keys are the system's own names. Each color has a `-dark` sibling; components reference the light key, which resolves to its sibling in dark. The app's tokens (`apps/web/ui/tokens.css`) follow this record value for value under ADR-021's `--gm-*` custom properties; `apps/web/tests/tokens.test.ts` fails when the two drift apart. The sections from Compare Table onward describe the screens of the T-045 prototype and keep its structure; their colours, type and corners follow the tokens above. Names from the earlier world that remain in this record map to today's tokens: Graphite Ink and Chalk Ink → Ink; Secondary Graphite → Ink 2; Label Graphite → Ink 3; Disabled Graphite → Ink 4; Reverse Ink → On Ink; Control Edge → Edge; Hairline Rule → Rule; Timetable White and Slate → Sheet; Pale Shell → Ground; Inset Well → Well; Deep Scenario Green and Chalk Green → Scenario; Baseline Graphite → Baseline; Amber and Error names → Warning and Error. Selection and focus, which that world drew in Graphite Ink, are the Accent.

**Key Characteristics:**
- Two data inks with a fixed grammar: baseline dashed gray, scenario solid green. The dash carries the difference; the hue confirms it.
- Geist for everything, Geist Mono for IDs, hashes and code, Noto Sans TC for Chinese; tabular figures; five sizes.
- Cards on a page: 12 px corners, a hairline ring and a hair of shadow; pills for status and provenance.
- One accent (violet) for the AI, selection and focus; green only for the scenario; amber and red only for signals.
- Provenance always on screen; the page's primary action sits at the right of its header, level with the provenance.
- A design-tool canvas: dot grid, card steps, floating tools, an inspector.

## Simplicity (H-027)

The owner adopted this system on one condition: the interface is simple and clean (簡潔), and it keeps only the buttons and controls each view needs. Five working principles make that checkable; the tests that enforce them are named after each.

1. **One primary action per view.** At most one primary (Ink) button on any screen; every other action is a default, quiet or danger button, or sits in a menu. A what-if run and a version action never share a slot (The Action Beside Its Object Rule). Checked: `apps/web/tests/simplicity.test.tsx` (every draft state, the gallery, failures, and the signed-in project list, project and model pages) and `apps/web-e2e/tests/simplicity.spec.ts` (every route as it renders signed out, both languages, 390 and 1280 px).
2. **Only the controls a view needs.** Every visible button, link or field does something that is built and tested today, or is a step of a flow that has shipped. No placeholder, "coming soon" or disabled-forever control; no two controls for the same action on one view. A control a spec or a test requires (skip link, language, theme, retry, sign-in recovery) stays; one that looks redundant but is required is kept and asked about, not removed. Checked in the browser (`apps/web-e2e/tests/simplicity.spec.ts`, both languages, 390 and 1280 px) on every route as it renders signed out: no disabled control outside the gallery, no two visible buttons with the same name and no two visible links to the same place. Checked in unit tests (`apps/web/tests/simplicity.test.tsx`, both languages, each view together with the app shell's controls): no two buttons or links with the same name and no two links to the same place on the start, not-found and sign-in error pages, in every draft state (also a conflict whose reload failed), in a failed load, and on the signed-in project list, project and model pages, loaded and failed, with a fake API and session; there also nothing disabled outright and at most one primary, and a control blocked for now (the editor's Save draft and Publish on the model page) names visible, non-empty text as its reason by `aria-describedby` (the rule bars placeholder and disabled-forever controls, not an action waiting on its condition); the other signed-in pages block nothing. Since the app mounts the signed-in bootstrap (T-043 follow-up), the browser also checks the signed-in views, signed in through the BFF and the test provider: the project list, a project, the model with the editor (as editor and as viewer; the editor's blocked actions must show their reason), the choice of organization, the project list with the organization switcher, and no access; the unit tests add the signed-in strip and every state of the bootstrap. The one pair kept for the owner's decision (H-028 kept it) is listed in both tests.
3. **Progressive disclosure.** What the current step does not need is collapsed or one click away: technical details and request IDs, legends, keyboard hints, provenance details. The app strip holds the product, the navigation, the language and the theme; once signed in also the account: Sign out, and the organization switcher for a user with several organizations (T-043 follow-up). Nothing else until a view has the function (no Find or mode switch yet). The sign-in is on the page that needs it (the sign-in-required notice), not in the strip. Checked by review; the problem view's technical details sit behind a "Details" disclosure (`app/_components/problem-view.tsx`).
4. **No decoration.** No gradient, ornamental icon, extra border, heavy shadow or animation beyond this system's: borders are 1 px hairlines (no side stripes), shadows only from the three shadow tokens (card, control, overlay) plus the invalid-field and selected-step rings, motion only as 180 ms state-colour transitions. The palette is the zero-chroma neutrals, the accent (the AI, selection, focus), the scenario green (also a passed check) and the warning and error signals; nothing else (H-041). Checked: `apps/web/tests/design-rules.test.ts` (no colour, size, spacing, corner, shadow or transition outside the tokens; no gradient but the canvas's dot grid, no keyframes; no border over 1 px) and `tokens.test.ts` (no hue outside the four families; dark neutrals have no chroma); in the browser, `simplicity.spec.ts` (every transition is 180 ms on that curve, and none is longer than 0.01 ms under reduced motion).
5. **Calm layout, plain words.** At most one secondary panel beside the main content; generous whitespace on one spacing scale; short, plain copy in English and Traditional Chinese, with no marketing language and no task IDs. Checked: the shell has one main region and no side panel (`layout.test.tsx`, `simplicity.spec.ts`); spacing only from the scale (`design-rules.test.ts`); copy by review (catalog changes are listed in `checks/T-045_VALIDATION.md`).

### Simplifications in the app

Where a component of this record was busier than the principles allow, the app is simpler, and the record keeps its original wording above for the screens still to be built:

- **Language and theme are in the settings menu (H-042).** The owner found labelled Selects in the chrome too much like a form (「很有 ai slop 的味道」). Both are segmented choices (radio groups) in the menu at the rail's foot: English and 繁體中文, each named in itself and marked with its `lang`; System, Light and Dark. The arrow keys move between the items and only a press changes the page (WCAG 3.2.2). Signed out, the foot is a Settings button with these two only.
- **The account (T-043 follow-up, H-042).** Signed in, the rail's foot is an avatar (the initials of the person's name, else of their email, else of the organization, on the Accent Fill) that opens the account menu: who is signed in (name and email from the sign-in's profile and email claims, cleaned by the BFF and shown only to that person), the organization and role, the organization switcher with several organizations (a Select; a long name ends in an ellipsis), the language and the theme, and Sign out. Of several organizations, the page asks once, as a list of buttons with the role beside each; until then the menu has no switcher. The choice is remembered for the browser session.
- **No entrance motion.** Overlays appear without the 4 px rise and fade; motion is only the 180 ms colour change of a control's state. The motion token is kept for the screens that need it (the playhead).
- **No icons in notes yet.** A note's tone is its edge and wash, with Graphite Ink text and a heading; icons come with the authored icon set.
- **No row hover in tables.** Rows show selection (Selected Row Tint) and focus (the ring), not the pointer; in dark, Control Edge on the Hover fill would fall below 3:1 for a checkbox in the row.
- **Page headings are Display, Title and Body** (H-041). A page's h1 is 26 px (tracked -0.025em), h2 15 px, h3 13 px at 600.
- **An action's outcome is a framed status.** The in-page status message (ADR-021: no toast) is the Outcome frame: 1 px Graphite Ink, ink text, no colour.
- **The 2D editor (T-015 hackathon slice, H-027 Q2 flow).** The header band's provenance row (H-040) holds the draft's status and revision and ends in the actions (Discard my changes only while there are unsaved edits, Save draft, Publish version, the one primary); the latest version and the domain are groups of the same row, and the Scenario and Runs groups come with Simulate. A blocked Save draft or Publish stays focusable as `aria-disabled` with its reason beside it (Buttons, Disabled). The step panel is an Add step menu in the canvas tool bar (no key caps, pools, undo or redo yet); the canvas has no caption box, issues pill or zoom group (the 2D / 3D segment came with T-033, below): the status row says what the canvas shows, a Problems list under the canvas replaces the drawer, scroll or pinch zooms, Show all steps fits the line, and Tab reaches every step. A selected step is a Graphite Ink border on Selected Row Tint without the ring shadow (principle 4), and an issue's border colour stays on it. The side panel is the view's one secondary panel (principle 5), 360 px, as tall as the canvas column and scrolling inside itself: the AI request above the step's properties (H-040); the page with the editor takes the window's width, since a line runs wide. Connections are changed in a step's properties (and by dragging between handles), not as canvas objects, so Tab never stops on an edge.
- **The 3D view (T-033 hackathon slice).** A 2D / 3D segment ends the editor's tool bar (the Segmented Controls' 2D / 3D size: 30 px items, 8 px sides, 12 px type; the focus ring drawn inside the item, Reverse Ink on the selected one). 3D replaces the canvas with a read-only stage on Timetable White under a Control Edge top rule, as wide as its column and as tall as the line's shape needs (within 240–520 px; H-039), the first view (52° pitch) holding every step in perspective and, on the results page, the tallest column playback can raise: steps as procedural boxes and cylinders where the 2D canvas puts them (no other layout), flows as Ink Graphite lines, steps in Control Edge, queues between Rule and Control Edge, sources and sinks in Baseline Graphite, the selected step in Graphite Ink; with a run's results (the results page's closed "See the line in 3D"), a process goes from Control Edge to Amber Edge with its resources' utilization and a queue rises with its mean length. No step is added, moved or fitted there; Add step and Show all steps leave the tool bar, and the Properties panel stays. A hover shows the step's name in a Raised chip; under the stage, one Label line names the selection and the controls (no legend panel, no buttons: drag, Shift+drag, the wheel; the arrows, + and −, 0, Escape). The palette is read from the tokens when the view opens and again when the theme changes. Without WebGL the stage is an info Callout and nothing else changes.
- **One way to run, so no "More ways to run" segment** (T-032, hackathon slice). The run button runs the scenario beside its baseline with the settings above it; its hover darkens the edge to Graphite Ink instead of brightening the fill 8%, which would need a colour outside the tokens.
- **Results stand on their own page until the canvas has a Simulate mode** (T-032). The runs' state, the Compare table and the Marey chart are one page (`/scenarios/<id>/results`). The chart shows the whole run: no playhead, scrubber, play button or row buttons, because no canvas follows them yet; its numbers are a table behind a disclosure. The Compare table has no rugs, metric buttons or full screen, and under a mean it gives that run's 95% interval when the server has one (the API defines only that interval, for replication means), not a 90% interval under a median. Under the header band (H-040), whose provenance row, once both runs have completed, gives each run (the line key, Baseline or Scenario, Completed and its replications), the Compare table, the 3D view and the Marey chart take the main column and the AI explanation and the run settings a side panel (400 px, divided by Control Edge, the run IDs one per line); below 1100 px the panel follows the main column. The Runs section (progress, a failure, Cancel) shows only until both runs have completed.
- **Each step's measures are one click away** (2026-10-28, the live demo). The key metrics table holds the whole line's labelled measures, the resources and the waits; the measures a domain pack reports for each step (their names are the server's keys, such as `blocking_ratio.ict`, a few hundred on the electronics line) follow it in a closed disclosure, "Each step's measures (N)", in the same table form, those with a value in either run first. Without it the Marey chart sat below several hundred rows.
- **The results say each thing once** (H-039, the UI review of 2026-10-29). The median lead time sentence sets the scenario's value in green at 600 and a 12 px caption under it says how many replications the figures come from and that the table gives the 95% intervals where the server computed them. When both runs complete, the outcome is announced but not framed on screen, since each run's line already says Completed; a run that stops shows its outcome in the Outcome frame. The live region itself is never shown, so its look does not change as it speaks. In the AI's explanation, its summaries of the two runs and its "What changed" list repeat the table, so they sit in one closed disclosure; why, the assumptions, what is uncertain and what to try next stay open. Negative figures carry a minus sign (U+2212), not a hyphen.
- **On a phone the Compare table stacks each row** (H-039). At 720 px and below each metric's name takes its own line, its two runs the next and Δ and Δ % the one after, under heads laid out the same way (Metric, then two by two), so Δ is on screen without scrolling sideways and no figure is cut; the caption moves above the table, which keeps its table roles.
- **Asking the AI is one form on the model page; the review stands on its own page until the canvas has a Review mode** (T-031, hackathon slice). At the top of the editor's side panel (H-040; on a phone, under the note that editing needs a larger screen): the request in the person's words (a text area), what the AI proposes (a Select: a change to the model or a scenario to simulate, the proposal's mode) and Send to the AI, a default button, so Publish stays the page's one primary; while the editor has unsaved edits, Send waits with its reason beside it, since the proposal opens on its own page. The request's help line goes on, in the same 12 px Secondary Graphite with no icon or colour, to say that what is typed or uploaded goes to an AI service, so only synthetic data belongs there (T-057, H-035); the text area and the photo button name that sentence as their description. The form stands at the top of the editor's side panel (H-040; before, H-039 kept it above the editor on one row): the text area three lines tall, what the AI proposes with its hint under it, then Send and the photo button. The review (`/proposals/<id>`) has no side panel and draws no canvas: its state, the AI's summary, the two risk cells (Geminus's check and validation in a Graphite Ink frame, the AI's claim and confidence in a hairline frame, the counted risk under them), the changes as hairline rows with Added (a Graphite Ink tag) and Removed (an Error Red tag) at 4 px corners (the scale has no capsule radius), what a new step holds under its row, the exact JSON Patch and its hash behind a disclosure, the AI's assumptions and expectations, then the decision under a Control Edge rule, confirmed in place. Approve and Reject are the only actions; there is no "Edit first" and no "Redo on vN" (asking again is the model page's one request). A proposal whose base is no longer the latest can never be approved, so Approve is not offered there and a warning note says why (no disabled-forever control); a 409 at approval keeps the page and puts a warning note, focused, where the decision was. An approval the server refuses on its own recheck (its checks fail again, or its risk is not the one reviewed; D-069 (b)) is the same: the proposal stays ready, Approve is not offered again, and a focused warning note says why and that the way on is Reject and asking again. In the changes, every name and value from the model or the patch is plain text in its own `<bdi>`, with characters that show nothing or reorder text written as ⟨U+XXXX⟩; an entity whose name another of its kind shares, or that the proposal adds, carries its ID as "{name} ({id})" (the organization picker's rule); a change whose two sides would still read the same says so (the same value, or the exact values). The outcome is the Outcome frame without a check icon (no icons yet); after an approved scenario its next step is a link to the scenario. A proposal whose base is no longer the latest says so in its state line too ("Out of date"), not "Ready for your review"; the confirmation's button is worded apart from Approve ("Yes, create version N"), so a second Enter is not taken for the first (H-039).
- **Which AI wrote it is shown (H-043).** A proposal's header has a pill naming the model that wrote it ("Written by Muse Spark 1.3 (Meta)") and, for a photo, what read the photo and how many objects it found ("SAM 3.1 · 12 objects"); the AI explanation's note names the model the same way. The names come from the server's `generated_by` (the stored provider metadata's provider, model and segmenter only); a model the catalog does not know is shown by its own ID. Beside each of the explanation's suggested next experiments an editor has "Propose as a scenario" (the AI button): Muse Spark writes the suggestion as a scenario proposal on the model's latest version, which opens on its own page for review, approval and a run; nothing runs or is saved until then.
- **The start page is a landing in the same system (H-048).** `/` is the one page written to persuade, for the hackathon's judges first, and it adds nothing to the system: the five sizes (the page's h1 at Display, sections at Headline, steps at Title), the tokens, hairlines and the three shadows. Under the rail it reads as the record of one what-if: the promise and one paragraph of how, the one primary action (Sign in to try it, back to the projects; with a session, Open your projects, the pair with the rail's brand that H-028 kept), then the results page itself; the loop as a numbered list whose steps name who acts in pills like the header's provenance (a Meta model's pill has the Accent edge and "Meta" in Ink 3, the Geminus server's the Ink edge of "Checked by Geminus", a person's and the engine's Rule Strong), each beside the product screen it happens on; which Meta model does what and the industries the templates cover as hairline-divided lists; why it is safe as terms and their meaning, like the run settings. The screens are pictures of the real views, made by `apps/web-e2e/scripts/landing-shots.mjs` from the gallery's review pages (both themes, WebP at 1x and 2x) and, for the two that exist only live, cropped from the demo's screenshots (light only); each floats on the Overlay shadow so it does not read as part of the page, and CSS shows the picture of the theme on screen, so the other is never fetched. No marketing figure, logo wall or second call to action (H-027).

### Tokens in the app

ADR-021 names every token `--gm-*` in `apps/web/ui/tokens.css`:

| DESIGN.md | App token |
|---|---|
| `colors.<key>` and `<key>-dark` | `--gm-color-<key>`; dark values under `prefers-color-scheme: dark` (unless `data-theme="light"`) and under `:root[data-theme="dark"]` |
| typography sizes 12, 13, 15, 20, 26 | `--gm-font-size-label`, `-body`, `-title`, `-headline`, `-display`, with `--gm-line-height-*` |
| the families and figures | `--gm-font-sans` (`"Geist Variable"`, `"Noto Sans TC Variable"`, then PingFang TC, Microsoft JhengHei, system-ui), `--gm-font-mono` (`"Geist Mono Variable"`), `--gm-font-features` |
| `spacing.<key>` | `--gm-space-<key>` |
| `rounded.<key>` | `--gm-radius-<key>` (`full` for pills) |
| Card, control and overlay shadows (light, dark) | `--gm-shadow-card`, `--gm-shadow-control`, `--gm-shadow-overlay` |
| focus | `--gm-focus-ring` (2 px accent) |
| 180 ms `cubic-bezier(.2,.8,.2,1)` | `--gm-motion` |

The theme follows the system unless the reader chose one; the choice is the cookie `geminus_theme` (`light` or `dark`; none means the system's), which the root layout renders as `<html data-theme>` so the first paint is already right, with no script and nothing for a CSP to allow. The fonts are self-hosted from `@fontsource-variable/geist`, `@fontsource-variable/geist-mono` and `@fontsource-variable/noto-sans-tc` (OFL-1.1, `docs/THIRD_PARTY_NOTICES.md`).

## Colors

Neutrals with no chroma, one accent, one data green, and an amber and red signal pair. Every text pair components use is at least 4.5:1 and every control boundary, data ink and the focus ring at least 3:1 against the surfaces they sit on, in both themes (`apps/web/tests/tokens.test.ts`).

### Neutrals
- **Ground** (#FAFAFA; dark #0A0A0A): the page, the strip and the canvas's dot grid.
- **Sheet** and **Panel** (#FFFFFF; dark #111111): cards, the workspace and the inspector.
- **Raised** (#FFFFFF; dark #171717): buttons, inputs, the chosen segment, floating tools, overlays.
- **Well**, **Hover**, **Press** (#F4F4F4, #F2F2F2, #E8E8E8; dark #161616, #1F1F1F, #262626): the segmented control's track, disabled keys, pointer feedback.
- **Rule** and **Rule Strong** (#EBEBEB and #E0E0E0; dark #232323 and #2E2E2E): card rings and dividers; button and pill borders.
- **Edge** (#8A8A8A; dark #6E6E6E): a boundary that identifies a control: inputs, checkboxes, the scrollbar.
- **Ink** (#0A0A0A; dark #EDEDED), **Ink 2** (#525252; dark #A1A1A1), **Ink 3** (#6B6B6B; dark #8A8A8A), **Ink 4** (#A3A3A3; dark #525252): text from primary to disabled. **On Ink** (#FFFFFF; dark #0A0A0A) labels the primary button.
- **Baseline** (#6B6B6B; dark #A1A1A1): the baseline run, always dashed.

### Accent
- **Accent** (#6D4AFF; dark #9D8FFF), **Accent Fill** (#6D4AFF), **Accent Wash** (#F4F1FF; dark #1B1735), **On Accent** (#FFFFFF): the AI's actions (Send to the AI, Explain these results), a selected step's border and its wash, the focus ring, the brand mark.

### Scenario and signals
- **Scenario** (#08784E; dark #3DD68C), **Scenario Wash** (#E8F5EE; dark #0D281B), **On Scenario** (#FFFFFF; dark #0A0A0A): the scenario run and a passed check, the run button.
- **Warning** (#955400; dark #F5B14C) with its mark, wash and edge; **Error** (#C42430; dark #FF6B6B) with its wash and edge: signals only.

### Named Rules
**The Two Inks Rule.** Baseline is dashed gray and scenario is solid green, in every chart, bar, key and legend. The dash carries the difference; hue is the second cue, never the only one.

**The Green Means Scenario Rule.** Green marks the scenario run or a passed check and nothing else: never brand, selection, links, focus or decoration.

**The Accent Means AI and Attention Rule.** The violet marks what the AI does, what is selected and where focus is. It is never a run and never a status.

## Typography

**Font:** Geist (variable weight), with Noto Sans TC, PingFang TC, Microsoft JhengHei, system-ui
**Mono:** Geist Mono, for IDs, hashes, seeds, engine versions and the JSON Patch

**Character:** A precise neo-grotesque from the developer-tool world the owner named, with tabular figures (`"tnum" 1, "lnum" 1`, set on the body) so every column of numbers lines up. Page titles are tightly tracked (-0.025em). All three faces are OFL.

### Hierarchy
- **Display** (600, 26px, 32px, -0.025em): a page's title (h1).
- **Headline** (500–600, 20px, 28px): the median lead time sentence on the results page, dialog titles, claim values in a proposal.
- **Title** (600, 15px, 22px): card and section headings (h2), the brand.
- **Body** and **Control** (400 and 500, 13px, 1.5): text, buttons, links, fields.
- **Label** (400–500, 12px, 1.4): pills, captions, help, table headers, segment labels.

### Named Rules
**The Five Sizes Rule.** Five sizes only: 12, 13, 15, 20 and 26 px. Hierarchy comes from weight and from Ink against Ink 2 and Ink 3, never from a sixth size.

## Layout

The frame is a 48 px app strip over a 44 px provenance row over the stage. Both rows sit on Pale Shell; the strip ends in a hairline and the provenance row ends in Control Edge, marking where the sheet begins. The strip holds the product and breadcrumb on the left, the Edit / Simulate / Review switch in the centre, and Find (with its key hint), language, theme and account on the right. The provenance row holds label and value groups divided by hairline verticals with 14 px either side (Version, Draft, Scenario, Runs) and ends at the right edge in the mode's actions: in Edit, Save draft and then Publish; in Simulate, the split run button. A blocked action's 12 px reason sits to its left. Review has no row action; its decision stays in the panel.

In the app every signed-in page opens with its header (H-040, restyled in H-041): on the page's ground, the way back (the parents as links in Ink 2, divided by a drawn "/"), the page's title at Display, then its provenance as pills (a 12 px label in Ink 3, the value in Ink), and at the right, level with the pills, the page's actions with a blocked action's reason to their left. The model page's pills are the latest version, the domain and the draft (status and revision), its actions Save draft and Publish; a proposal's pill is its status; the results' are the two runs once both have completed. A parent's link never repeats the rail's: the model page's parent is its project, the results page's its scenario, and the list pages name their model as a pill instead. The content under it is cards: 12 px corners, Sheet, a Rule ring and a hair of shadow, 20 px inside and 20 px apart. The frame is a rail (H-042, the owner's choice from the shape round: Railway's layout): 56 px at the left on Ground, a Rule hairline at its right, full height and in view while the page scrolls. At its top the brand mark (two offset squares, the twin: one Accent Fill, one outlined in Ink) is the way to the projects; inside a model, four 40 px icon links follow: Canvas (the model page), Scenarios, AI proposals and Runs (each its own page on an API that already existed), named by `aria-label`, with the name in an Ink chip beside the icon on hover and keyboard focus, and the page's own raised like a chosen segment (`aria-current="page"`); a scenario's, its results' and a proposal's page belong to Scenarios and AI proposals. At its foot is the account (signed in) or the settings (signed out). A page inside a model links back to nothing the rail already holds: the proposal, scenario and results pages have no "Back to the model" link. On a phone the rail is a 52 px bar across the top: the brand, the sections in a row, the account at the end.

The stage is the canvas column beside a side panel 400 px wide (360 px at 1366 px and below), divided by Control Edge. The canvas column is the model canvas on Timetable White above the Marey strip (a 40 px header plus a 180 px timetable, 220 px in all, under a Control Edge top rule). Canvas chrome floats 16 px in from the canvas edges: top right, the 2D / 3D and zoom groups and the fit button. In Simulate and Review the caption takes the top left (at most 460 px). In Edit the tool bar takes the top left and the caption moves to the bottom left, after the issues pill. The panel pads 20 px, opens with its heading block 16 px above the first section, spaces sections 20 px apart and places a section's header 10 px above its content.

Full-screen Compare removes the canvas column and gives the panel the stage at 24 px top and bottom and 32 px at the sides, in two columns (1.1fr and 1fr, 40 px gap) with the rugs in a two-column grid. Every role steps up for the projector: labels and captions 12 to 15 px, metric and run names 13.5 to 18 px, figures and Δ 15 to 22 px.

Spacing runs tight inside a group and opens between groups: 6 px from icon to label, 8 px between controls, 10 to 12 px inside rows, nodes and notes, 14 px around provenance groups, outcomes and toast text, 16 px for gutters and the canvas inset, 20 px for the panel and its sections, 24 to 40 px in full-screen Compare. Every outer gutter is `max(16px, env(safe-area-inset-*))`.

Breakpoints: at 1366 px and below the panel narrows to 360 px, the scrubber to 120 px and Compare tightens its cell padding to 3 px. At 1100 px and below the strip's key drops its flat-and-steep reading hint and lower-priority provenance groups leave. At 720 px and below is the phone layout: the page scrolls; the strip becomes two rows (product and compact tools, then a full-width Simulate / Review switch, with no Edit); provenance becomes a two-column grid under the model title; the row actions leave; the panel comes first, then the Marey strip (160 px timetable, its key wrapping and the reading hint back), then a list of the steps at the playhead's time in place of the canvas. The phone shows results and proposal review only; in Edit it says editing needs a larger screen and offers Simulate and Review.

### Named Rules
**The Action Beside Its Object Rule.** The mode's primary action ends the provenance row, next to the version, draft or scenario it acts on ("Publish v4…" in Edit, "Run S-2 again" in Simulate). A what-if run and a version action never share a slot.

**The Provenance Always Rule.** On every desktop mode the provenance row names the version, draft, scenario and runs, and the canvas caption says what the canvas shows (the editable draft and its base, or a read-only version with its patch), plus seed, engine, dataset and whether the data is synthetic.

## Elevation & Depth

Cards lift a hair off the page; what floats over the work lifts more. Three shadows, all tokens:

- **Card** (`0 0 0 1px <Rule>, 0 1px 2px rgba(0,0,0,.04)`; dark the ring only): cards, the workspace, lists.
- **Control** (`0 1px 2px rgba(0,0,0,.05)`; dark `rgba(0,0,0,.4)`): buttons, the Select's button, the chosen segment, steps on the canvas.
- **Overlay** (`0 0 0 1px rgba(0,0,0,.06), 0 8px 30px rgba(0,0,0,.12)`; dark `0 0 0 1px #2E2E2E, 0 12px 32px rgba(0,0,0,.6)`): popovers, menus, dialogs and the floating canvas tools.

Two rings are states, not depth: a selected step's (its Accent border inside a 3 px Accent Wash) and an invalid field's (an inset 1 px Error).

## Shapes

Corners: 6 px for tags and small parts and the segments inside a segmented control, 8 px for controls (buttons, inputs, the segmented control, notes), 12 px for cards, steps on the canvas, the workspace, dialogs and the floating tools, and full for pills (status, provenance, counts). Borders are 1 px. Dashes carry meaning and nothing else: the baseline, and removed edges in a proposal diff.

### Named Rules
**The Steps Are Cards Rule.** On the canvas a step is a 168 px card with its name at 600 and its kind in Ink 3; selection is the accent, an issue colours its border.

## Components

### Buttons
A raised key (H-041): 32 px, 8 px corners, 12 px sides, a 1 px Rule Strong border, Raised fill and the Control shadow; Control type. The page's actions carry a 16 px line icon from the authored set 6 px before the label (Add step, Show all steps, Send to the AI, From a photo, Save draft, Publish version, Discard, Approve, Reject, Run, Cancel, Explain); a choice, a retry or a confirmation's Cancel does not.
- **Primary:** the ink inverted: Ink fill, On Ink label; hover to Ink 2. One per view (Publish, Approve, the post-outcome next step).
- **AI:** Accent Fill with an On Accent label: the actions that ask the AI (Send to the AI, Explain these results).
- **Run:** Scenario fill with an On Scenario label; used only to run a scenario.
- **Hover / Focus:** the Hover wash (Press on press), 180 ms; focus is a 2 px Accent outline 2 px out.
- **Quiet:** no border, fill or shadow until hover: tools inside the floating canvas pill, Sign out.
- **Danger:** an Error label on the default key (Reject, Discard, Delete).
- **Disabled:** Well fill, Rule border, Ink 4 label, no shadow. A blocked action stays focusable as `aria-disabled` and its reason is on screen beside it and linked as its description.
- **Link:** Ink with a 1 px underline in Edge 3 px below; Ink on hover.

### Segmented Controls
One form for every one-of-n choice (2D / 3D and field choices): a Well track with a Rule ring and 8 px corners, 2 px inside; 26 px items at 12 px; the chosen item raised on it (Raised fill, Rule Strong border, the Control shadow, Ink at 600), the others in Ink 2.

### Chips
- **Scenario tag:** 20 px capsule, 1 px Deep Scenario Green border, 12 px 600 green label ("S-2"), on steps the scenario changes.
- **Added tag:** a Graphite Ink capsule ("Added", "New in v4") flagged across the node's top edge.
- **Removed tag:** 24 px, 4 px corners, 1 px Error Red border on Raised, a 12 px 600 red label with an x icon, placed over a removed edge.
- **Status chips:** 22 px, 4 px corners, wash fill, edge border, 12 px 600 text with a 13 px icon; warning in amber, error in red.
- **Neutral tag:** 20 px, 4 px corners, Hairline border, Secondary Graphite at 500.
- **Review step-up:** Review draws the canvas at about 82%, so its tags take 13.5 px type (22 px tall; the removed tag 28 px) to stay legible.

### Cards / Containers
The model canvas has no cards in the usual sense; its containers are steps and pools.
- **Step node:** 12 px corners, Sheet fill, a 1 px Rule Strong border with the Control shadow (H-041; the app's steps are 168 px cards with the name at 600 over the kind in Ink 3). The prototype's step: a 39 px header (12 px inset, name at 600) above a hairline; the body is label (12 px) and value (15 px) pairs; utilisation is two square-ended strokes on 1 px Hairline tracks, dashed graphite baseline (2 px) above solid green scenario (2.5 px). A process step is 136 px wide (152 px while editing); sources and sinks run 116 to 140 px.
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
- **Key caps:** Geist Mono 12 px 500 in Ink 2 on Well with a Rule border and 6 px corners.
- **Icons:** one authored SVG set, 16 px (13 px small, 12 px in tags), 1.5 px stroke, round caps and joins, drawn in currentColor.

## Do's and Don'ts

### Do:
- **Do** draw every baseline as dashed Baseline Graphite and every scenario as solid Deep Scenario Green, in charts, node bars, keys and legends alike (The Two Inks Rule).
- **Do** write a change as baseline → scenario with the scenario value in green at 600, and give Δ, Δ% and the 90% interval beside the median.
- **Do** keep the provenance row and the canvas caption on screen in every desktop mode, and label synthetic and sampled data where it is shown.
- **Do** end the page's header with its primary action: Publish in Ink, a scenario run in Scenario green, an AI request in the accent.
- **Do** draw an input's boundary in Edge (at least 3:1); a labelled button, a segment or a step may use Rule Strong with its shadow (H-041); keep Rule for dividing content.
- **Do** keep a blocked action focusable as `aria-disabled` with its reason written beside it.
- **Do** let a table wrap a name before reducing a size, and step every role up one size for full-screen Compare.
- **Do** animate state changes only, at 180 ms with `cubic-bezier(.2,.8,.2,1)`; under reduced motion, switch transitions off and let the playhead jump one time unit at a time.
- **Do** take step names, resource names, the time unit, metric names and domain icons from the DomainPack.

### Don't:
- **Don't** tell baseline and scenario apart by hue alone; the two inks are barely 1.5:1 apart.
- **Don't** use green for brand, selection, links, focus or decoration; it means scenario or passed.
- **Don't** let a tinted neutral into either theme; neutrals stay zero-chroma. Only the hues' washes carry colour.
- **Don't** put a grid anywhere but the model canvas's dots (H-041) and the Marey time grid.
- **Don't** use Disabled Graphite for anything but disabled text.
- **Don't** add a sixth type size or a third family; Geist Mono is only for IDs, hashes and code.
- **Don't** mark an active option or row with a colored stripe; use Selected Row Tint and weight.
- **Don't** offer topology editing on a phone; the phone shows results and proposal review only.
- **Don't** build one domain's vocabulary, units or icons into a component; the prototype's Software Delivery names are example data, not the system.
