---
version: 1
slug: "026-10-19-t-045-prototypes-workspace-html-ddc5b4c1"
primary_target: "checks/evidence/2026-10-19-T-045/prototypes/workspace.html"
related_targets: []
---

# Surface brief: model workspace

## Scope and mode
The model workspace: one canvas with Edit, Simulate and Review modes, plus a Compare view that can take the full stage for projection. Visitor mode: Operate. Prototype: `checks/evidence/2026-10-19-T-045/prototypes/workspace.html` (static, synthetic data); the app build (T-015 and later) follows it.

## Audience, job, task
Process owners modelling all day; managers making 30–60 minute decisions at a desk; teams reading Compare on a meeting-room projector (PRODUCT.md). Tasks: edit the draft and publish an immutable version; run a scenario patch against an immutable version and compare it with the baseline; review an AI proposal whose risk the server computed, then approve, edit or reject it.

## Content and proof
Synthetic example data, labelled as such on screen. Every number agrees with the graphic that shows it. Provenance is always visible: published version, draft revision and its base, scenario patch and its base, the runs compared, seed, engine, dataset revision.

## Constraints
PRODUCT.md. Dark theme gray-black, never blue (H-012); default theme follows the system, user can switch and it is remembered (H-013). English default plus zh-Hant. Desktop first; on a phone only results and proposal review (approve or reject), no topology editing. WCAG 2.2 AA, keyboard-complete. Standard controls only (React Aria Components in the app, ADR-021).

## Critique backlog (2026-10-19, 23/40)
P1 dark theme navy; P1 draft, version and scenario blurred, outcomes undesigned; P1 results unfit for decisions and projection, numbers contradicting their graphics; P1 keyboard and screen-reader path broken at the canvas; P2 grey text below AA and a fragmented type scale. Owner scope: all five, phone read-only, Compare able to take the full stage.

## Chosen direction and memorable moment
Marey graphic timetable (H-013). The memorable moment: the playback strip is a string diagram in which a queue shows as a line going flat, tied to the canvas selection.

## Unresolved
3D view and the Import / ask-AI entry points appear as entries only in the prototype. Exact app token names follow ADR-021's `--gm-*` scheme when the system lands in `apps/web`.

## Direction contract
THESIS: Time runs left to right, the steps stand top to bottom, and every work item is a line through them; a queue is a line going flat. It refuses the category default: a dot-grid canvas of floating soft-shadow cards with one blue accent.
OWN-WORLD: A white timetable sheet on a pale neutral shell; graphite ink; a fine grey time grid; baseline drawn as dashed graphite, scenario as solid deep-green ink; amber warnings and red errors kept apart from the scenario ink; Archivo with true tabular figures, one family; small corners and hairline rules, shadows only on overlays. Dark is a gray-black slate sheet with chalk ink and the same line grammar.
STORY: The visitor always knows which version, draft, scenario and run they are looking at; edits the draft; runs a scenario on an immutable version; reads where work waits (flat lines) and what changed (baseline, scenario, Δ, Δ%, interval); approves or rejects a proposal against server-computed risk.
FIRST VIEWPORT: At 1440×900 in Simulate: a 48 px strip (product, breadcrumb, Edit/Simulate/Review, Find ⌘K, language, account, primary "Run S-2 again"); a provenance row of label/value groups (Version v3 published; Draft rev 12 on v3; Scenario S-2, patch on v3; Runs R-7 against R-6); the canvas on the left with six steps and two pools; a 220 px Marey strip along the canvas bottom with the playhead at day 34; a 400 px Compare panel on the right with the metric table and a per-run rug, and a Full screen control that gives Compare the stage.
FORM: Marey graphic timetable, position 1 of 7 on the ordered list (the IMPECCABLE'S PICK card, chosen by the owner); seed:6aeec0acf3a8777b
FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

Signature interaction: scrubbing or playing the Marey strip moves one playhead that the canvas follows (queue counts for that day); selecting a step on the canvas lights its row in the strip, and selecting a row selects the step. Motion grammar: state changes 150–200 ms ease-out; the playhead moves linearly while playing; with reduced motion the playhead jumps and nothing else animates; no decorative motion.
