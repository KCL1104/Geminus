---
target: model page, proposal review page and results page
total_score: 24
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 3
timestamp: 2026-10-28T16-51-01Z
slug: apps-web-app-ui-gallery
---
Method: dual-agent (A: design review sub-agent · B: detector + browser sub-agent)

Targets: model page (/ui-gallery/editor), proposal review (/ui-gallery/proposal), results (/ui-gallery/results); live evidence checks/evidence/2026-10-29-live-t030-t034/. Build includes the uncommitted 3D framing change.

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|---|---|---|
| 1 | Visibility of System Status | 3 | Stale proposal's status line still says "Ready for your review."; the out-of-date note is ~1,100 px lower |
| 2 | Match System / Real World | 2 | Internal identifiers show: "domain electronics_line", JSON Pointer paths in Problems |
| 3 | User Control and Freedom | 2 | Editor has no undo/redo; needs_input proposal offers only Reject |
| 4 | Consistency and Standards | 3 | Δ uses hyphen-minus; utilization Δ given in pp and % |
| 5 | Error Prevention | 3 | Blocked actions say why; stale cannot be approved; confirm keeps label/position (Enter-Enter) |
| 6 | Recognition Rather Than Recall | 2 | 3D boxes unnamed; needs_input questions must be carried to another page |
| 7 | Flexibility and Efficiency | 1 | Editor: no Ctrl+S, no jump-to-step; Tab walks every step |
| 8 | Aesthetic and Minimalist Design | 2 | Results repeat the same numbers in lead, table, AI paragraphs and What changed |
| 9 | Error Recovery | 3 | Problems list good; failed-run heading framed as "request not accepted" |
| 10 | Help and Documentation | 3 | Honest inline help; 3D colour scale explained by one sentence |
| **Total** | | **24/40** | **Acceptable** |

## Design Specificity Verdict

LLM: the visual grammar is product-specific (Marey strip, dashed/solid key spelled in words, scenario wash column, server-vs-AI risk cells); the page structures are generic stacked h2 + details documents. Proposal review is the most grounded.

Deterministic scan: CLI detect.mjs found 0 findings on 9 targets (verified working on a planted file). Browser overlay: editor 7, proposal ready 8, needs_input 3, results 2, results failed 3. Real: line-length on proposal meta/words/hint (~140–160 chars) and runs.noComparison (~166). False positives: low-contrast on aria-disabled buttons, cramped-padding on DESIGN's 32 px buttons, flat-type-hierarchy (DESIGN Five Sizes Rule), React Flow clip, patch hash and gallery-only intro line length.

## Priority Issues

1. [P1] Results: the answer has no hierarchy and no uncertainty; provenance comes last (features/results/results-view.tsx, results.module.css .summary). Fix: value pair per DESIGN, the server's interval beside it, one-line provenance under the heading. clarify, layout.
2. [P1] Results repeat themselves (features/results/analysis.tsx, features/simulations/run-status.tsx): AI summary paragraphs and What changed restate the table; "Both runs completed." box duplicates the per-run labels. Fix: disclose the restating parts; removal needs owner (H-027). distill.
3. [P1] Model page: the Ask AI form (textarea, select, 2 buttons, 3 help lines) pushes the canvas to y≈650 at 1280×800; ~9 actions in the top band (features/model-editor/model-view.tsx, features/ai-proposals/ask-form.tsx). layout, distill.
4. [P2] Proposal: stale status line contradicts the decision area (features/ai-proposals/proposal-view.tsx). The server gives no reason for its risk level (contract has none). clarify.
5. [P2] 3D stage 520 px with the line in a thin band; steps unnamed (twin-3d/engine.ts, twin-3d.module.css, viewer.tsx). layout.
6. [P2] Phone Compare: at 390 px Δ is off-screen; zh-Hant names wrap 3–4 lines (features/results/metrics-table.tsx). adapt.
7. [P2] Prose lines run 140–166 characters on proposal and run-status paragraphs (detector). typeset.

## Persona Red Flags

Alex: no undo/redo or Ctrl+S; Tab reaches Burn-in after ~26 stops; Enter-Enter on Approve commits.
Sam: canvas first view ~0.72 zoom (below READABLE_ZOOM 0.75), names ~10 px; JSON Pointer paths read aloud.
IE presenting: thin dashes blur on screen share; tan vs grey utilization low contrast in 3D; Δ sign has no better/worse cue; ~4 screens of scrolling to the chart.

## Minor Observations

- Change row "Burn-in Smallest batch" has no separator (change-list.tsx).
- Precision "0.000309 min", "+0.00956 min" (format.ts).
- Hyphen-minus in Δ; "No percentage: the baseline is 0." repeated per row.
- Editor 3D hint touches the frame's left edge.
- Live D1: Marey label column ~1/3 width; hours 0–6 empty.

## Questions to Consider

- Is the model page two products stacked (AI request over an editor)?
- Is the results page for the presenter or the room? If the room: first screen = question, answer with interval, key, provenance.
- What does an unlabeled 3D view tell that the Marey strip does not?
