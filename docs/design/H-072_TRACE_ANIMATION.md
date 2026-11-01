# H-072: the line in motion (trace-driven animation in 3D and 2D)

**Status: brief for the owner's confirmation (impeccable shape), 2026-11-01. Decisions: H-072 (scope, look, domains), the owner's answers of 2026-11-01 (autoplay with a pause button; colours by the design system). Web only: no API, schema, engine or migration change.**

## 1. Job and audience

- **Who.** A line engineer or manager deciding a what-if, and a hackathon judge watching a 3-minute video or trying the live demo. Operate mode: the motion serves reading the run, not persuasion.
- **Need.** To see the line work: boards moving station to station, queues building where the line is short of capacity, a tester down, the burn-in oven loading a batch. Today the model is a diagram, and playback is a column per step.
- **Uniquely true here.** Every board that moves is a board the simulation actually moved: its stations, waits, blocks and breakdowns come from the run's sampled trace (`geminus.trace.v1`, replication 0, 1 board in 8), never from a made-up loop. That is the product's own rule (results are reproducible, nothing is fabricated) shown as motion.

## 2. Outcome and proof

- **Primary moment.** On the results page, the baseline and the scenario play side by side on one clock. With a fourth FCT tester, the pile of boards before FCT on the scenario side drains while it stays on the baseline side. This is the video's centrepiece.
- **Success.**
  - An engineer reads where the line waits without opening a table.
  - A judge understands "baseline vs scenario" in five seconds with the sound off.
  - Nothing on screen claims more than the trace holds.
- **Proof on screen.** "Sampled: 1 board in 8, replication 0" sits by the controls, and the run's time sits on the playhead. A truncated trace says where it stops.

## 3. Selected direction

- **Visual authority.** DESIGN.md (H-041), unchanged in palette and type. The world is "a twin you operate like infrastructure": a precise, quiet developer-tool look. The equipment is drawn the way Figma or Linear would draw hardware: clean primitives, neutral materials, no textures, logos or realism.
- **Thesis: a factory you can read.**
  - Each station is shaped by what it is. The kind comes from the resource it requires, through the DomainPack, so the kernel stays neutral.
  - Each board is a small tile in its run's ink: baseline gray (outlined, the dash's 3D twin) and scenario green (solid). Green still means the scenario.
  - Machine state is a status light: Ink while busy, Ink 4 while idle, warning amber while blocked or in setup, error red while failed. Violet stays for the AI and selection, as now.
- **Electronics line, in detail** (resource → equipment):
  - `smt_line`: a long low machine with a conveyor through it and four bays (printer, SPI, placement, reflow), as its label says.
  - `aoi_machine`: a cabinet with a camera gantry over its conveyor.
  - `ict_tester`, `fct_tester`: test cabinets, one per unit of capacity, side by side, so a third ICT tester appears as a third cabinet.
  - `burn_in_chamber`: an oven with a door. A batch (`batch_id`) loads in together and leaves together.
  - `debug_operator`: a bench with a seat per operator on shift.
  - `pack_station`: a table.
  - Queues (`q_*`): a short conveyor before the station where boards stack in a grid.
  - Decisions: a junction.
  - Sources: a dock that boards enter from.
  - Failures and rework: a reject bin and a return lane.
- **Other packs, generic.** By node type and resource: process → machine block sized by capacity; queue → conveyor buffer; source → dock; sink → shipping; delay → long conveyor; warehouse resources → racks. Same lights, same boards.
- **Layout.** 3D keeps the 2D canvas's positions (no second topology, AGENTS.md §2). Conveyors follow the IR's flows.

## 4. Scope and boundaries

**In scope:**
- `apps/web/features/twin-3d/`: the equipment, the boards, the lights, the batches.
- A pure motion model shared by 3D and 2D: from the trace, where each sampled board is, and in what state, at time t.
- `apps/web/features/model-editor`: the 2D canvas overlay and the 3D view.
- `apps/web/features/results`: two stages side by side on one clock.
- DESIGN.md: one new named rule (below), with tests and screenshots.

**Untouched:**
- The IR, the API, the engine, the trace format, the Marey chart, the Compare table, the editor's authoring behaviour, the AI flows.

**Anti-goals:**
- No motion without a trace: a model with no run shows only the direction of flow, as chevrons on its links in 2D and arrows on its conveyors in 3D, with nothing moving.
- No interpolated claims: between two trace events a board stands where its last event put it. Only the hop from one station to the next is drawn as a short slide along its conveyor, 300 ms of playback. A label says the motion is sampled.
- No physics, no 3D authoring, no camera fly-throughs or auto-orbit, no outside 3D assets, no PCB green, no new colours.

## 5. States and ranges

| State | What shows |
|---|---|
| No completed run (model page) | Flow direction only; a quiet caption: "Run a simulation to see the line in motion." |
| A run without a trace (trace off, failed to load) | The static scene; the reason in the caption; no playback controls. |
| A truncated trace | Plays to its last event, then holds; "The trace stops at <time>" under the slider. |
| The run is still running or failed | The model page uses the latest completed run of the version shown; the results page follows the runs' state as now. |
| Ranges | Up to 100k events per trace (the engine's cap); a typical demo run (7 days, 1 in 8 boards) has a few thousand boards. The renderer draws at most what is at stations at time t: instanced tiles, a cap of 2,000 drawn per stage, with "+N not drawn" if ever exceeded. |
| Reduced motion | Starts paused. Play jumps one time unit every 650 ms (DESIGN.md's rule) and hops are drawn without the slide. |
| WebGL unavailable | 3D says so (as now); 2D still plays. |
| Phone (390 px) | The results page stacks baseline above scenario, one clock; the model page shows 2D motion (the editor is read-only there). |
| Dark | Same tokens' dark values. |

## 6. Interaction and layout

- **Autoplay with a pause button** (the owner's answer). Both pages start playing once the trace has loaded, and loop.
  - Pause/Play is a 32 px icon button with a text label for screen readers, first in the playback bar. It satisfies WCAG 2.2.2.
  - The time slider follows. The run's time is shown as "Day 2, 14:30" in the run's unit.
  - The choice to pause holds for the page visit.
- **Results page.**
  - Two stages under one playback bar: Baseline on the left, Scenario on the right, each titled with its run pill.
  - Selecting a step in either stage selects it in both (shared entity IDs).
  - Under 1100 px they stack.
- **Model page.**
  - 2D: an overlay on the React Flow canvas. Dots in the run's ink travel along the links, each step card shows a count badge of the boards waiting there, and a 2 px state ring shows the machine's state. Editing is unaffected: dragging or editing pauses playback, and it resumes on Play.
  - 3D: the same equipment scene as the results page, one stage.
  - The caption names the run it plays (scenario or baseline, its time and seed).
- **Speed.** A whole run plays in 30 s by default (as now). A 1× / 2× / 4× segment sits in the bar for long runs.

## 7. Constraints and open decisions

- **Design system amendment (to record in DESIGN.md with this slice): "The Trace Motion Rule".** Motion beyond the 180 ms state transitions is allowed only for a run's trace playback, driven by its events, with a pause control, and paused under reduced motion. `design-rules.test.ts` keeps rejecting any other animation.
- **Where the equipment map lives.** The kind per resource is presentation, keyed by the pack's ID and the resource's ID, in the web's domain registry beside the existing pack labels; a pack without an entry gets the generic shapes. *Open:* move it into the DomainPack's API later (not in this slice).
- **Performance.** PlayCanvas 2.23.2 instancing for the boards; one draw per material for equipment; at most one redraw per animation frame while playing; nothing re-renders React per frame.
- **Tests.**
  - Unit: the motion model, with deterministic fixtures from a real trace.
  - Browser: autoplay, pause, reduced motion starting paused, keyboard, axe, 390 and 1280 px.
  - Screenshots: the scene paused at a fixed time, for deterministic baselines.
  - Live: the demo model's comparison on the site.
- **i18n.** Every new string in `en` and `zh-Hant`.
- **Not invented by the builder.** No new colour, size, radius or shadow. No claim beyond the trace. No change to what a run computes.
