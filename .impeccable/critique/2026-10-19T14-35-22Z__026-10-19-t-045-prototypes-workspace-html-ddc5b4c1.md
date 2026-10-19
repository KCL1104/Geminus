---
target: workspace prototype v2
total_score: 23
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 4
target_identity: "file:/home/user/Geminus/checks/evidence/2026-10-19-T-045/prototypes/workspace.html"
target_fingerprint: "sha256:594ba7b55da4f3c7e18eaa92431ec2d17c8e78ce07d0fdbbc25323666cf580e7"
target_path: /home/user/Geminus/checks/evidence/2026-10-19-T-045/prototypes/workspace.html
timestamp: 2026-10-19T14-35-22Z
slug: 026-10-19-t-045-prototypes-workspace-html-ddc5b4c1
---
Method: dual-agent (A: design-review agent · B: detector + browser agent; isolated, run in parallel)
Run note: B finished before A, so detector findings reached the parent before A's; A ran isolated and never saw B. The scores are A's, unchanged.

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 2 | After "Publish v4" the strip still reads "Version 3 · all changes saved"; runs have no queued/running/cancel states; nothing is labelled synthetic or sampled |
| 2 | Match System / Real World | 3 | Plain field language (Shortest / Most common / Longest); "N unsaved changes on version 3" implies an immutable version is edited in place; "cap" unexplained |
| 3 | User Control and Freedom | 2 | No undo/redo, no discard draft; Esc closes only the inspector |
| 4 | Consistency and Standards | 2 | Design notes say middots and mono were removed, the build still has them; "Show: Version 3 / Scenario" changes the canvas but not the charts under it; zh keeps English aria-labels |
| 5 | Error Prevention | 2 | Publishing/approving a new immutable version has no confirm or diff; "Run again" enabled on an invalid draft; phone allows topology edits |
| 6 | Recognition Rather Than Recall | 2 | Six icon-only dock tools, names and shortcuts on hover only; theme and language toggles expose no state |
| 7 | Flexibility and Efficiency | 3 | ⌘K, V/Q/P/D/R/C, arrow keys designed; 14 Tabs from a node to its inspector; ⌘K finds no steps by name |
| 8 | Aesthetic and Minimalist Design | 3 | Calm canvas-first layout; the what-if answer is 12.5px mono, the least prominent thing on screen |
| 9 | Error Recovery | 2 | Field errors name problem and fix; 409/stale, run failed and cancelled are undesigned |
| 10 | Help and Documentation | 2 | Only a hint that vanishes after 4.5 s, with mouse wording on phone; no shortcut reference |
| **Total** | | **23/40** | **Acceptable** |

## Design Specificity Verdict

LLM assessment: half-authored. The sheet contents are this product's (live triangular-distribution preview; 30-run distributions with run, seed, engine and dataset; Review sets "Checked by Geminus: High" beside "The AI said: Low"; JSON Pointer issues that jump to the step; the scenario change drawn on the canvas). The shell is any canvas tool's (grid paper, seven floating surfaces with one radius and two soft shadows, centred segmented mode switch, "Find anything ⌘K", pill issue chip). Templated cues: navy blueprint dark with a periwinkle accent; Instrument Sans + IBM Plex Mono with mono on whole phrases; about ten middot-joined meta strings. Missed character: provenance (draft → immutable version → scenario patch → seeded run) is a 3.41:1 grey status string; it is the obvious signature move. Owner's binding constraint (dark = gray-black, not blue): FAIL (--paper #0e1c2c 212° 52% 11%, --surface #152739, --well #10213a; blue-tinted grid and rules).

Deterministic scan: source fragment exit 2, 1 finding, low-contrast #000 on #0e1c2c (false positive: no document shell, so the static pass lost body colour). Wrapped document exit 2, 1 finding, overused-font Instrument Sans (true: 88–92% of rendered text; on impeccable's training-default list with IBM Plex; agrees with A). Browser overlay over 5 states: ink-3 low-contrast 3.3–3.8:1 on the status strip, ⌘K label, sheet subtitle and the selected palette row (true; A measured the same independently); text-occlusion ×3 when a node is selected at 1440 wide (false against the rule's intent, but the geometry is real: the sheet covers Deploy and Released because selection pans only the selected node); #hint occlusion (timing false positive). Detector-blind issues A found: results contradict their own histograms, provenance blur, invisible node focus, unlabelled stepper input, 28% phone fit with ~4–5px node text.

Visual overlays: injected in a headless browser; no user-visible [Human] tab exists. Overlay screenshots are in the scratchpad critique-b folder.

## Overall Impression

What sits inside the sheets already reads as Geminus; the shell and visual language are a generic canvas tool in AI-default colour and type. Biggest opportunity: give provenance a visible form as the signature move while replacing the navy dark theme and the default faces.

## What's Working

1. Review keeps AI and server judgement visibly apart, with the AI's assumptions in plain language and base freshness: "AI suggests, humans approve" given form.
2. Uncertainty is honest: distributions over 30 runs with run facts, and a live triangular curve that turns red when invalid, not KPI tiles.
3. One topology, three overlays: Edit, Simulate and Review share the canvas; queue tokens and utilisation sit on the real steps; the proposal diff is drawn in place; issues jump to the step; reduced motion is respected.

## Priority Issues

- **[P1] Dark theme is navy blueprint, against the owner's gray-black rule.** Why: blocks sign-off; the main AI-default cue the owner named. Fix: near-zero-chroma gray-black neutrals (paper ~#141414–#17181a, surface ~#1e1f22, wells one step lighter, ink ~#e8e8e5, neutral white-alpha rules/grid); one chromatic accent; retune flow/queue/fault for dark; stronger palette veil; rewrite the notes. Command: /impeccable colorize
- **[P1] Draft, immutable version and scenario are blurred; publish, approve and run outcomes are undesigned.** Why: PRODUCT.md requires telling "save as version" from "what-if"; immutability is P0. Simulate draws run data over the invalid draft; Review draws the proposal over the dirty draft while claiming it passes. Fix: persistent object chip (Draft · based on v3 · rev · saved), Save draft separate from Publish v4…, publish confirm with diff, Simulate renders the run's immutable version read-only with the scenario as a named patch, Review renders base v3, post-publish/approve/run states. Command: /impeccable clarify
- **[P1] Results unfit for the decision and projector scenes.** Why: a stated median that contradicts its bars destroys trust in a meeting (verified in source: throughput baseline bins peak at 18–24, label says 42; also lead time and WIP). Baseline 35%-opacity bars are 1.49:1, told apart by fill only; no Δ/Δ%, intervals, queue p95, synthetic or sampled labels. Fix: Compare that can take the full stage; per-metric row baseline | scenario | Δ | Δ% | interval; outline vs fill histograms with median ticks; ticks ≥12px at 4.5:1; one data source for bars and labels; synthetic and sampled labels. Command: /impeccable layout
- **[P1] Keyboard and screen-reader path breaks at the canvas (REQ-023).** Why: all-day modellers and WCAG 2.2 AA. Inline stroke overrides the node focus ring; 14 Tabs to the inspector; Esc strands focus; palette has no trap or combobox semantics or empty state; #f-cap unlabelled; charts unnamed. Fix: focus stroke from a class; move focus into the sheet and back on close; combobox palette with trap and empty state; labels and chart titles; visible publish-block reason. Command: /impeccable harden
- **[P2] Whisper-grey hierarchy fails AA; fragmented type scale on default faces.** Why: AA failures in light; fatigue over a full day. ink-3 3.41/3.83:1 on paper/surface; inputs 1.09:1 against surface with no border; 11 font sizes 10.5–17px; Instrument Sans + Plex Mono flagged. Fix: ink-3 ≥4.5:1, lightest grey for disabled only; 1px input borders ≥3:1; five-step scale with result numerals ≥15px; faces chosen for the world. Command: /impeccable typeset

## Persona Red Flags

**Alex (power user):** ⌘K returns nothing for "CI" or "Deploy"; "Add a process step" closes silently; tool keys only toggle the pressed state; no ⌘Z or multi-select; resource pools cannot be focused or edited although the headline scenario changes a pool; 14 Tabs to the inspector.

**Sam (accessibility):** invisible node focus; popover strands focus; palette has no trap; unlabelled #f-cap; unnamed charts; error vs warning by red vs orange only; baseline vs scenario by colour only; English aria-labels in zh; theme and language toggles expose no state.

**Engineering manager presenting on a meeting-room projector (PRODUCT scene 3):** the comparison fills a quarter of the screen; 10.5px grey ticks and 1.49:1 baseline bars vanish on a projector; no Δ or Δ% to say out loud; "median 42" against bars near 20 invites a challenge; nothing says synthetic or conditional estimate.

## Minor Observations

- "Fix 1 errors to publish" (plural) and it disagrees with "2 issues".
- Hard-coded "cap", "h", "d" bypass the catalog; the prototype defaults to zh while PRODUCT says en.
- Long step names overflow the node box; long project names wrap the strip.
- Simulate: node subtitles collide with the utilisation bar; queue tokens sit on arrowheads.
- Review: the removed edge is a 55%-opacity dotted stroke, nearly invisible.
- No 3D entry point, no Describe/Import or ask-AI entry, no AI explain on results.
- Phone: editing offered though phones are view/review only; canvas fits at 28% and cannot be zoomed; the strip takes 126px in 3 rows.

## Questions to Consider

- Should a persistent provenance ribbon (v3 → draft → v4, scenarios and runs as branches) be the signature, since no other canvas tool has it?
- Should Simulate be a read-only view of an immutable run that can never show draft edits?
- In the meeting, is the canvas or the comparison the star?
- Is a vertical list of steps with deltas the truer phone view of the same IR?
