# Workspace v3: impeccable finish review (2026-10-19)

The workspace prototype (`prototypes/workspace.html`) rebuilt in the Marey timetable direction (H-013), code-led, reviewed by impeccable's finish reviewer: a fresh agent with no build history, judging the screenshots against the surface brief's direction contract (`.impeccable/surfaces/026-10-19-t-045-prototypes-workspace-html-ddc5b4c1.md`), the craft floor and the chosen decision sketch (`.impeccable/mocks/decision/model-pick.html`) as the critique reference. The detector ran once on the finished build: 4 low-contrast warnings on the raw fragment (false positives: without a document shell the static pass loses the body colour) and 1 advisory `codex-grid-background` on the canvas, which the reviewer judged true and the build removed.

## Round 1 (d040abd): fix

Kept as they were: the Marey strip tied to canvas selection; the dashed-graphite baseline vs solid deep-green scenario grammar; the 0%-saturation gray-black dark theme; the Compare rows with the spoken headline.

| # | Material fix | Round 2 (abf9cdf) | Round 3 (914f358) |
|---|---|---|---|
| 1 | Provenance (seed, engine, dataset, synthetic label) visible in every mode; Run facts in the first viewport | resolved | — |
| 2 | Every number agrees with its graphic: one data source for Compare, rugs, node counts and the strip | partial: one source, but the unit (working days) and unequal arrivals between runs were not visible | 2a working days on screen: resolved; 2b common random numbers for arrivals: resolved |
| 3 | One queue rule on every strip row; releases on one station line | resolved | — |
| 4 | No graph-paper canvas; no stripe on the palette's active option | resolved | — |
| 5 | Phone 390: no clipped labels or orphaned provenance tokens | resolved | — |
| 6 | Full-screen Compare fits 1280×800 | resolved | — |
| 7 | Simulate canvas at 1280 readable (≥11 px node text) | resolved | — |
| 8 | Record the primary action's place (brief amended, 2e8fb65); re-run axe and contrast on the final build | resolved | — |
| R1 | (round 2 regression) lead-time rug cut by the fold at 1440×900 | — | resolved |
| R2 | (round 2 regression) Source lines too small in Review | — | resolved |
| R3 | (round 2 regression) seeds in the chip without their runs | — | resolved |

The owner chose one more round after round 2 (two rounds is the unattended budget). The original target figures in the agent's sketch brief were not jointly possible (Little's law), so the screen shows the simulation's own values (lead time 3.9 → 2.9 working days, throughput 21.4 → 23.7 a week, QA queue p95 10 → 1, WIP 44 → 17); the reviewer judged the result truthful.

## Disposition: ship (round 3)

The ship covers the scored fixes, not the whole surface. Final-build scans (build log `r4`): axe (wcag2a/aa, 21a/aa, 22aa) 0 violations in 72 states; computed contrast 0 failures in 87 states (lowest 4.93:1 light, 5.18:1 dark); no console errors, overflow or overlap. Open, not scored: a compact Marey strip in full-screen Compare (a ceiling idea); the reviewer suggests writing the arrival rate as "23 items/week on average".

Evidence of the rounds (screenshots, axe and contrast reports) stayed in the session scratchpad; the review captures are `.impeccable/review/desktop.png` and `mobile.png` (gitignored).
