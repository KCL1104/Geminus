# Electronics Assembly/Test Line Domain Spec (proposal)

Status: **APPROVED** (2026-10-20, owner H-018: 「全都照建議」 ("follow all the recommendations")). Proposed on 2026-10-19 by T-046 phase 1. Basis: H-016, [ADR-025](../adr/ADR-025-p0-domain-electronics-line.md), [PRD](../PRD.md) REQ-003/004/013/015/024/025, [TEST_PLAN](../TEST_PLAN.md) TEST-034–036. The engine's new primitives are in [ADR-026](../adr/ADR-026-line-flow-primitives.md) (D-047–D-057); this document's own decisions are D-058–D-060 (§10). The status of every decision is as recorded in [DECISIONS_AND_TBD](../DECISIONS_AND_TBD.md) and `planning/decisions.json`.

**Numbers and sources.** The template is **synthetic**: it holds no real company's data, and no value in it was measured in a factory. Since H-046 (2026-10-29) its **magnitudes follow the sources cited in §4.5**: each parameter there has its value, its unit and either a source (URL and retrieval date) or the word "estimate" with the reasoning. §4.5 also says what the template simplifies compared with a real line. Simulation outputs of the template are outputs of this synthetic model, not predictions for any real line. Elsewhere in this document, statements about factory data and about what engineers expect come from the agent's earlier research draft (not committed, not verified); they are input only, not fact.

## 1. Scope

- **Unit of flow**: one board (entity type `board`); in the rack variant, one rack (`rack`, §4.3). P0 does not do panelization/depanelization (merge and split, an item ADR-026 defers); one entity is one board.
- **Stations**: SMT → AOI → ICT → FCT → burn-in → packing and shipping, including test fail → debug → retest, and scrap when a board cannot be repaired.
- **Representative question** (PRD §2): today there are 2 ICT testers, 3 FCT testers and a 10-board buffer before FCT. If we add 1 ICT tester or enlarge the buffer, how do UPH, the bottleneck station, WIP, utilization, blocking time and the retest ratio change?
- **P0 includes** (approved in H-018): finite buffers and blocking; machine breakdowns (accumulated only on shift when there is a calendar); calendars (including per-shift staffing and exception days); time-varying release (including whole-batch release tables); batching; setups (changeovers) and multiple products (one source per product, D-055); lognormal and Weibull (D-056); and the simulated pass ratio, retest ratio and scrap rate (D-058). The owner's direction is to keep as much functionality as possible (H-017; it listed setups and lognormal/Weibull as P0 proposals, left the other originally deferred items for the owner to decide, and approved no decision).
- **P0 excludes**: priority and dispatching rules, which the PRD already lists as P1 (including setup sequencing grouped by product); service interruption (preemption); advanced routing; and items the agent deferred to keep the scope converging (§9). Transport is approximated with delay nodes. Operator skills are not modeled. For holding a resource across steps, see §4.3.

## 2. Stations and typical structure

This is how P0 models the line, not a description of every factory. Real lines differ a lot in structure; T-027's inference and the user's confirmation decide the actual model.

| Station | What it does (overview) | IR representation (proposal) | Primitives used |
|---|---|---|---|
| SMT | A line made of solder paste printing, component placement, reflow and so on | One process, resource `smt_line` (the whole line is treated as one server; the balance between machines inside the line is not modeled) | Breakdowns, calendar; setups with multiple products (§4.4) |
| AOI | Automated optical inspection | process (`aoi`) + decision (pass/fail); fail goes through a failure node to manual touch-up, then into ICT | Finite buffer, breakdowns, calendar |
| ICT | In-circuit test | The first-test process and the retest process share `ict_tester`; fail → failure node → debug → rework node → retest | Finite buffer, breakdowns, calendar |
| FCT | Functional test | Same structure as ICT, resource `fct_tester` | Same as above |
| Burn-in | Powered on for a long time in a chamber | Batch process (`burn_in_chamber`, min/max/max_wait) | Batching, breakdowns; usually not limited by a calendar |
| Debug/touch-up | Manual diagnosis and repair | process, resource `debug_operator`; a decision decides repaired or scrapped | Calendar |
| Packing and shipping | Packing, shipping | process (`pack_station`) → completed sink | Finite buffer, calendar |
| Buffer | WIP racks or conveyors between stations | Explicit queue node; finite `capacity` on the main line; unlimited before debug and retest (D-050) | Finite buffer |

**First test and retest are separate.** IR routing has no memory, so one process cannot tell a first test from a retest. The template therefore builds the retest as another process that shares the tester with the first test (both queue under resource-wise FIFO). This lets the first-pass yield be read directly from the first-test decision (§5), and it allows retest time and yield to differ from the first test.

**Debug per station.** After debug a board must return to its "original" test station, and probability routing cannot split by origin. So ICT and FCT each have their own debug process, and they share `debug_operator`.

## 3. Catalog

### 3.1 Node kinds (pack catalog → generic type)

| Kind | Generic type | Description |
|---|---|---|
| `board_release` | source | Release; fixed distribution or release schedule (D-053) |
| `line_buffer` | queue | Buffer before a station; may have `capacity` (D-049) |
| `smt_station`, `aoi_station`, `ict_station`, `fct_station`, `pack_station` | process | Main-line stations |
| `retest_station` | process | Retest; shares the tester with the first test |
| `debug_station` | process | Debug/touch-up; needs `debug_operator` |
| `burn_in_station` | process (batch) | Batch burn-in (D-054) |
| `test_result` | decision | Test result; exactly one flow to a failure node (fail), the rest are pass |
| `debug_result` | decision | Repaired or scrapped |
| `defect_observation` | failure | Records a test defect (a generic failure, not a machine breakdown) |
| `rework_observation` | rework | Records re-entry after repair |
| `transport` | delay | Transport or waiting; holds no resource |
| `shipped` | sink (completed) | Shipping |
| `scrap` | sink (abandoned) | Scrap; not counted in output |

### 3.2 Resource roles

| Role | Type | Description |
|---|---|---|
| `smt_line` | machine | One SMT line (treated as one server) |
| `aoi` | machine | AOI machine |
| `ict_tester` | machine | ICT tester |
| `fct_tester` | machine | FCT tester or test fixture |
| `burn_in_chamber` | machine | Burn-in chamber; counted per batch (one batch holds one unit) |
| `debug_operator` | human | Debug/touch-up staff |
| `pack_station` | human | Packing station |
| `test_slot` | machine | Test slot in the rack variant (§4.3) |
| `assembler` | human | Assembler in the rack variant |
| `debug_engineer` | human | Debug engineer in the rack variant |
| `generic` | generic | Other constrained capacity, with no domain semantics |

The kernel does not decide behavior from role strings (TWIN_IR_SPEC §2); roles are used only by the pack's validation, metrics and UI.

### 3.3 `domain_attributes` conventions (proposal)

- Processes of main-line stations and retest stations: `station` (station ID, such as `fct`), `pass` (`first` or `retest`).
- `test_result` decision: `result_of` (the ID of the process under test).
- These are pack conventions and are untrusted data (UNTRUSTED_DATA); the generic kernel does not read them.

### 3.4 Pack validation rules (proposal, T-051)

1. Each main-line station process has exactly one primary resource whose role matches the station (operators may be added).
2. Each `test_result` decision has exactly one flow to a failure node; if not, that station's FPY is unavailable and a warning is returned.
3. The first-test and retest stations share the same primary resource.
4. All parameters of the demo template are marked `synthetic: true` in `metadata` (the UI shows a synthetic marker).
5. The generic validator's rules (including `BLOCKING_CYCLE`) are not repeated in the pack.

## 4. Full template (proposal, synthetic)

### 4.1 Topology

```mermaid
flowchart LR
  REL[board_release] --> QS[q_smt] --> SMT[smt]
  SMT --> QA[q_aoi 20] --> AOI[aoi] --> AR{aoi_result}
  AR -- 0.97 --> QI[q_ict 10]
  AR -- 0.03 --> AD[aoi_defect] --> QT[q_touchup] --> AT[aoi_touchup] --> AW[aoi_rework] --> QI
  QI --> ICT[ict] --> IR{ict_result}
  IR -- 0.95 --> QF[q_fct 10]
  IR -- 0.05 --> IF[ict_fail] --> QID[q_ict_debug] --> ID[ict_debug] --> IDR{ict_debug_result}
  IDR -- 0.97 --> IW[ict_rework] --> QIR[q_ict_retest] --> IRT[ict_retest] --> IRR{ict_retest_result}
  IDR -- 0.03 --> SCRAP[(scrap)]
  IRR -- 0.90 --> QF
  IRR -- 0.10 --> IF
  QF --> FCT[fct] --> FR{fct_result}
  FR -- 0.93 --> QB[q_burnin 120]
  FR -- 0.07 --> FF[fct_fail] --> QFD[q_fct_debug] --> FD[fct_debug] --> FDR{fct_debug_result}
  FDR -- 0.95 --> FW[fct_rework] --> QFR[q_fct_retest] --> FRT[fct_retest] --> FRR{fct_retest_result}
  FDR -- 0.05 --> SCRAP
  FRR -- 0.90 --> QB
  FRR -- 0.10 --> FF
  QB --> BI[burn_in batch] --> BR{burnin_result}
  BR -- 0.995 --> QP[q_pack 500] --> PACK[pack_ship] --> SHIP[(shipped)]
  BR -- 0.005 --> BF[burnin_fail] --> QFD
```

The number after a queue name is its synthetic `capacity`. A queue without a number is unlimited (`q_smt` is unlimited too, so the source's release backlog does not appear in the template).

**Deadlock check (D-050).** The shared components of holders: {ict, ict_retest} (`ict_tester`), {fct, fct_retest} (`fct_tester`), {aoi_touchup, ict_debug, fct_debug} (`debug_operator`); every other holder is its own component. Blocking edges: smt→aoi, aoi→ICT, debug→ICT (through `aoi_rework` into `q_ict`), ICT→FCT, FCT→burn-in, burn-in→packing. The buffers before debug are unlimited, so no edge points into the debug component; the graph has no cycle and passes. If `q_ict_debug` is made finite, ICT failures can be blocked by the debug area (ICT→debug), and debug can already be blocked by `q_ict` (debug→ICT); that forms a cycle and is rejected. The same holds when `q_fct_debug` is made finite (FCT→debug→ICT→FCT). Making only `q_ict_retest` finite does not: it only adds to the existing "debug→ICT" edge, and no edge enters the debug component. The other three D-050 rules: each bundle has only one resource, so R2 (calendar intersection) and R3 (units = 1 for a resource with breakdowns) hold trivially, and R4 (on-shift time of the resource with breakdowns and of its partners) also holds.

### 4.2 Parameters (synthetic; magnitudes and sources in §4.5)

The time unit is minute. Triangular distributions are written tri(min, mode, max).

| Resource | Type | Capacity | Calendar | Breakdown TTF | Breakdown TTR |
|---|---|---|---|---|---|
| `smt_line` | machine | 1 | `two_shift_weekdays` | exponential(mean 600) | tri(10, 20, 90) |
| `aoi` | machine | 1 | `two_shift_weekdays` | exponential(mean 1200) | tri(5, 10, 30) |
| `ict_tester` | machine | 2 | `two_shift_weekdays` | exponential(mean 900) | tri(10, 20, 60) |
| `fct_tester` | machine | 3 | `two_shift_weekdays` | exponential(mean 900) | tri(10, 30, 90) |
| `burn_in_chamber` | machine | 9 | none (24/7) | exponential(mean 10080) | tri(60, 120, 480) |
| `debug_operator` | human | 4 | `two_shift_weekdays` | — | — |
| `pack_station` | human | 2 | `two_shift_weekdays` | — | — |

TTF is counted in on-shift time (the D-051 clock advances only on shift), so mean 600 means about one breakdown per "600 on-shift minutes". Burn-in has no calendar, so its TTF is wall-clock time.

| Process | Needs | Service time |
|---|---|---|
| `smt` | smt_line ×1 | tri(0.75, 0.85, 1.15) |
| `aoi` | aoi ×1 | tri(0.2, 0.25, 0.35) |
| `aoi_touchup` | debug_operator ×1 | tri(3, 5, 10) |
| `ict`, `ict_retest` | ict_tester ×1 | tri(1.4, 1.7, 2.3) |
| `ict_debug` | debug_operator ×1 | tri(5, 10, 30) |
| `fct`, `fct_retest` | fct_tester ×1 | tri(2.8, 3.2, 4.0) |
| `fct_debug` | debug_operator ×1 | tri(10, 20, 60) |
| `burn_in` | burn_in_chamber ×1 (per batch) | constant 480 (per batch); batch min 20, max 40, max_wait 120 |
| `pack_ship` | pack_station ×1 (2 units) | tri(0.8, 1.0, 1.5) |

- **Calendar** `two_shift_weekdays`: period 10080 (one week); the shifts are [360, 1320) + d·1440 for Monday to Friday (06:00–22:00, two back-to-back 8-hour shifts with no break); t=0 is Monday 00:00 (model local time, D-052).
- **Release**: `board_release` uses a release schedule (D-053): deterministic, period 10080, rate 0.75/minute (45/hour) in the on-shift periods above, and 0 otherwise.
- **Routing probabilities**: see the diagram in §4.1.
- **Rough capacity estimate (review L7; checked with the engine in §11)**: estimated from on-shift averages. λ = arrival rate (units/minute), w = mean time each unit holds the resource (including retests, minutes), m = number of units, A ≈ MTTF/(MTTF + MTTR) (time availability in on-shift time), ρ = λ·w/(m·A). Retest counts come from the geometric series of the routing probabilities: 0.054 per unit at ICT and 0.079 per unit at FCT (including burn-in failures that return to FCT).

| Station | λ | w | m | A | ρ | Note |
|---|---|---|---|---|---|---|
| SMT | 0.750 | 0.917 | 1 | 0.938 | 0.73 | About 0.81 with the changeovers of §4.4 |
| AOI | 0.750 | 0.267 | 1 | 0.988 | 0.20 | Inline AOI is much faster than the line's pace (§4.5) |
| ICT | 0.750 | 1.897 | 2 | 0.968 | 0.74 | About 0.49 after adding 1 tester |
| **FCT** | 0.749 | 3.596 | 3 | 0.954 | **0.94** | Highest; expected bottleneck |
| Burn-in | 0.749 (including boards burned again after a burn-in failure is repaired) | 12.0 (per chamber per unit; a full batch is 40 units/480 minutes) | 9 | 0.979 | 1.02 (on-shift periods) | Runs 24/7; the whole-week average is about 0.49; it carries the daytime backlog (in `q_burnin`, at most 120) into the evening |
| Debug | 0.750 | 3.50 (touch-up, ICT debug and FCT debug combined) | 4 | — | 0.66 | |
| Packing | 0.746 | 1.100 | 2 | — | 0.41 | At shift start it must first clear the boards that burn-in finished overnight |

Burn-in keeps running at night and on weekends. The finished boards are at most about 120 (`q_burnin`) + 9×40 (in the chambers) = 480, which fits in `q_pack`'s 500, so the chambers are not blocked all night while packing is off shift. That is why the original 4 chambers, `q_burnin` 80, `q_pack` 30 and 1 packing station were changed (under the original settings, burn-in and packing were the constraint), and why H-046, which made burn-in 8 hours instead of 4 (§4.5), raised the chambers from 6 to 9 and `q_pack` from 400 to 500. FCT can do about 3,820 boards per week against a weekly release of 3,600 boards, a margin of about 6%: FCT is the one bottleneck. This is only a rough steady-state average; it does not account for blocking, shift boundaries or variability. The engine runs of §11 confirm the order (FCT's on-shift work ratio is the highest). They also show that a bigger buffer before FCT and a third ICT tester change lead time and output by less than 1%: FCT is starved only 2.7% of the time (its `starved_ratio`, the same with buffers of 30), so a bigger buffer has little to protect, and ICT is not the bottleneck ([H-046 report](../../checks/H-046_ELECTRONICS_REALISM_VALIDATION.md)). CB-24 once found the original template's reviewer overloaded; every adjustment stays synthetic.
- Names are provided in two languages, en and zh-Hant (the TemplateProvider of ARCHITECTURE §7); IDs do not change.

### 4.3 Rack integration test variant (AI servers)

| Station | IR representation (proposal) | Parameters (synthetic) |
|---|---|---|
| Assembly | process, `assembler` ×1 (capacity 4) | tri(120, 180, 300) |
| System test | process, `test_slot` ×1 (capacity 12) | tri(240, 360, 720) |
| Test result | decision: pass 0.85/fail 0.15 → debug | — |
| Debug | process, `debug_engineer` ×1 (capacity 3); with the approximation below, also `test_slot` ×1 | tri(60, 180, 600) |
| Retest | process, shares `test_slot` with system test | Same as system test |
| Packing and shipping | process → shipped | tri(60, 90, 120) |

Limitation: in practice a rack is often debugged inside its test slot (the slot stays occupied during debug). In P0 a resource is released at the end of each process and cannot be held across steps, so the slot is released during debug. This overestimates slot availability. The P0 approximation builds debug as a process that needs both `test_slot` and `debug_engineer` (it reacquires a slot, so waits may come out too long), and the result marks this approximation. Holding a resource across steps is not scheduled for P0 for now (§9). Long tests also make the underestimate from finish-first (D-051, D-052) more visible.

### 4.4 Multi-product and setup variant (synthetic)

- Two product families, `board_a` and `board_b`, each with its own source, released in whole batches with table-type release (D-053): `board_a` releases 360 boards at 06:00 each weekday (offset 360 + d·1440), and `board_b` releases 360 boards at 14:00 each weekday (840 + d·1440). The daily total is the same as in §4.2 (720 boards). The rest of the topology is the same as §4.1 (stations do not differ by product).
- `setups` of `smt_line`: A→B and B→A are both tri(20, 35, 60) minute (H-046, §4.5; before it tri(15, 25, 45)), and `initial` is `board_a`; other resources have no setups. Whole-batch release plus FIFO gives about 2 setups per day (about 77 minutes), which raises SMT's ρ from 0.73 to about 0.81, still below FCT's 0.94. If the two products were released interleaved, almost every board would need a setup under FIFO and SMT would be overloaded. Dispatching grouped by product is PRD P1.
- Breakdown repair times can use lognormal instead (for example, TTR of `smt_line` as lognormal(mean 25, cv 0.8)) to demonstrate D-056; these values are synthetic as well.
- This variant also makes "halve the SMT setup time" a comparable scenario (D-055 distributions can be changed under D-057).

### 4.5 Sources and realism of the template (H-046)

The owner asked that the template be realistic (H-046: 「我希望範本要是符合真實性的」, "the templates must be realistic"). The template stays **synthetic**: it is no real company's line and holds no real company's data. Its magnitudes follow the sources below, which the agent read on 2026-10-29. A value that no source gives is marked **estimate**, with the reasoning. The sources are mostly equipment makers, EMS and test vendors, trade magazines and one journal paper; vendor figures are often best cases. None of them describes this line, so a value "follows" a source when it is inside or next to the range the source gives, for the kind of board assumed here: a mid-volume, two-family line of mid-complexity boards (several hundred placements each) built on one SMT line in two shifts.

**Scale.** One SMT line, 720 boards a day, 3,600 a week. That is a realistic mid-volume output for one line (about 45 boards an hour on shift), so nothing is scaled down: a 28-day run with 5 replications takes well under a second on the development machine (the lock run of §11). The one value at the edge of its sources' range is burn-in: 8 hours is the low end of the range given for consumer products (see the table); a longer burn-in would need proportionally more chambers.

| Parameter | Value in the template | Unit | Source (retrieved 2026-10-29) or estimate |
|---|---|---|---|
| SMT line pace (`smt` service) | tri(0.75, 0.85, 1.15), mean 0.92 (about 55 s a board) | minute per board | The line runs at the pace of its slowest machine, usually placement. Estimate from: a 60,000 CPH (components per hour) line in a contract manufacturer ([I-Connect007, Downing and Owen](https://iconnect007.com/article/51662/implementing-aoi-in-a-cm-environment/51665/smt)); real placement rates up to 40% below the rated speed ([CircuitNet, Bliss, 2007](https://www.circuitnet.com/experts/40004.html)) or about 60% of the IPC speed as a rule of thumb ([I-Connect007, Klaver, 2014](https://iconnect007.com/article/80564/smt-trends-and-technologies-what-is-your-real-output/80567/smt)); about 600 placements a board at about 40,000 CPH real is about 54 s. Worked examples elsewhere use a 40 s line cycle ([Indium](https://www.indium.com/blog/selecting-a-reflow-oven-length/); [I-Connect007, van Gastel](https://iconnect007.com/article/44038/placement-quality-affects-costs/44041/smt)). The printer is not the limit: core cycle 6.5–8 s ([ASMPT DEK NeoHorizon](https://smt.asmpt.com/en/products/printing-solutions/dek-neohorizon)). |
| Changeover between families (`smt_line` setups) | tri(20, 35, 60), mean 38 | minute | Feeder replacement "may take longer than 30 minutes" and a line "must stop for more than 30 minutes" when printer and mounter changeovers follow each other; a case study went from about 20 to 5 minutes with automated setup ([Yamaha](https://global.yamaha-motor.com/business/smt/concept/recipe/)); a TV-board SMT line went from over 80 to 40 minutes ([de Jesus et al., ITEGAM-JETIA 2018](https://sumarios.org/node/124707)). The range is a line with offline feeder preparation but no automated changeover. |
| Inline AOI (`aoi` service) | tri(0.2, 0.25, 0.35), mean 0.27 (16 s) | minute per board | 10 s for a 260 × 200 mm board including load and unload ([D-TEK Xceed 3D AOI](https://dtek.com.tw/en/product/smt/aoi/xceed---new-generation-3d-aoi)); the range adds board transfer and slower programs (estimate). |
| Boards needing touch-up after AOI (`aoi_result`) | 0.03 (FPY 0.97) | ratio | A worked example for a 1,000-component board gives a yield of 97.26% ([I-Connect007, van Gastel](https://iconnect007.com/article/44038/placement-quality-affects-costs/44041/smt)). False calls are taken to be cleared at the AOI review within its cycle (estimate); a CM measured them below 0.5% ([Downing and Owen](https://iconnect007.com/article/51662/implementing-aoi-in-a-cm-environment/51665/smt)). |
| Touch-up (`aoi_touchup`) | tri(3, 5, 10) | minute | Estimate: a hand-soldering repair of one or two joints and a look under the microscope. |
| ICT test (`ict`, `ict_retest`) | tri(1.4, 1.7, 2.3), mean 1.8 | minute per board | ICT "typically ~30 s to 2 min per board" ([NextPCB, 2025](https://www.nextpcb.com/blog/ict-vs-fct-pcba-test-strategy)); about 15–30 s more to load and unload a manual fixture (estimate). |
| ICT first-pass yield (`ict_result`) | 0.95 | ratio | More than 95% FPY with an inline ICT ([Keysight case study 5992-2445](https://www.keysight.com.cn/cn/zh/assets/7018-05838/case-studies/5992-2445.pdf)); a CM went from 75% to more than 93% at ICT after adding AOI ([Downing and Owen](https://iconnect007.com/article/51662/implementing-aoi-in-a-cm-environment/51665/smt)); FPY "as low as 76%" with a fixture contact problem ([Keysight case study 5992-3792](https://www.keysight.com/hk/en/assets/7018-06552/case-studies/5992-3792.pdf.html)). |
| FCT test (`fct`, `fct_retest`) | tri(2.8, 3.2, 4.0), mean 3.3 | minute per board | FCT "typically about 1 to 10 min per board for production tests" ([NextPCB, 2025](https://www.nextpcb.com/blog/ict-vs-fct-pcba-test-strategy)); unchanged by H-046. |
| FCT first-pass yield (`fct_result`) | 0.93 | ratio | Estimate: "for modern day top tier manufacturing yields are well above 90%" ([Mini-Circuits, 2025](https://blog.minicircuits.com/rf-measurement-devices-in-modern-production-test-lines/)); 0.93 keeps a visible debug loop. No source with an FCT figure was found. |
| ICT and FCT debug (`ict_debug`, `fct_debug`) | tri(5, 10, 30); tri(10, 20, 60) | minute | Estimate. For orientation: diagnosing a board "ROUGHLY 15 minutes", 10 minutes to replace a chip, about 20–25 minutes a board in all, for detector boards repaired at a university ([Penn SNO](https://www.hep.upenn.edu/SNO/daq/testing/ASICS-DBs-Part-2.html)); ICT names the failing part, FCT does not, so FCT debug is longer. |
| Retest pass, repaired or scrapped | retest 0.90; repaired 0.97 (ICT), 0.95 (FCT) | ratio | Estimate. |
| Burn-in time (`burn_in`) | constant 480 (8 h) | minute per batch | Consumer electronics 8–24 hours, industrial 24–72 hours ([Best Technology, 2026](https://www.bestpcbs.com/blog/2026/02/burn-in-test-a-complete-guide-for-electronics-pcb-reliability/)); commercial and industrial "24-72 hours at 85°C" ([Anzer, 2026](https://www.anzer-usa.com/resources/?p=3334)). 8 h is the low end (see Scale). |
| Burn-in batch and chambers | batch 20–40 boards, max wait 120 min; 9 chambers | boards; minute; units | Estimate: a chamber (or rack) of 40 boards; 9 chambers are what 720 boards a day need at 8 h (§4.2). |
| Burn-in yield (`burnin_result`) | 0.995 | ratio | An aerospace case: a burn-in failure rate of 4% on the first run, brought "to 0.5%" after process changes ([Anzer, 2026](https://www.anzer-usa.com/resources/?p=3334)); estimate for this line. |
| Breakdowns (all machines) | TTF and TTR of §4.2 | minute | Estimate. No source with MTBF or MTTR of SMT machines or testers was found. The values give each machine 94–99% time availability; electronics OEE benchmarks are "roughly in the 75–88% range", with minor stops weighing more than breakdowns on SMT lines ([TeepTrak, 2026](https://teeptrak.com/en/us-electronics-semiconductor-oee-case-study-2026/)); the template does not model minor stops. |
| Shifts | two 8-hour shifts, 06:00–14:00 and 14:00–22:00, Monday to Friday | — | Two shifts Monday to Friday, 7:00–15:30 and 15:00–23:15, at an electronics assembler ([job posting, Burnaby](https://www.simplyhired.ca/job/8wKd1WwYxvEG3YcuCyCuJNaYDEgcOB5AZWCcYa3TaqfX1iJriacSBQ)); breaks are not modelled (estimate). |
| Release | 360 boards of each family at its shift's start, every weekday | boards | Estimate: one shift's work order released as a whole. |
| Packing (`pack_ship`) | tri(0.8, 1.0, 1.5); 2 stations | minute per board | Estimate: bag, label and box one board. |
| Buffers | `q_aoi` 20, `q_ict` 10, `q_fct` 10, `q_burnin` 120, `q_pack` 500 | boards | Estimate: conveyor and rack space. `q_pack` holds what burn-in finishes overnight (§4.2). |
| Debug crew, testers | 4 debug and repair operators a shift; 2 ICT, 3 FCT testers | units | Estimate, sized so that FCT is the bottleneck at about 0.94 (§4.2). |

**What H-046 changed** (old → new): SMT pace tri(0.9, 1.0, 1.4) → tri(0.75, 0.85, 1.15); changeover tri(15, 25, 45) → tri(20, 35, 60); AOI tri(0.8, 0.9, 1.1) → tri(0.2, 0.25, 0.35); ICT tri(1.8, 2.0, 2.6) → tri(1.4, 1.7, 2.3); burn-in 240 → 480 min; burn-in chambers 6 → 9; `q_pack` 400 → 500. The other values were checked against the sources (or kept as estimates) and did not change. The values the demo's what-ifs replace (`deploy/demo/whatifs.json`: ICT testers 2, debug crew 4, burn-in batch 20–40, `q_ict` and `q_fct` 10, 360 boards a release) are unchanged, so the what-ifs still apply to models created from the template.

**What the template simplifies compared with a real line**:

- The SMT line (printer, SPI, placement, reflow) is one server at the pace of its slowest machine (§2). The reflow oven's transit time (about 4.5 minutes in the heated zones in [Indium's example](https://www.indium.com/blog/selecting-a-reflow-oven-length/)) and the balance between the machines are not modelled; the oven holds several boards at once, so it does not limit throughput, but every board's lead time is a few minutes too short.
- Panels: one entity is one board; real lines often build and test several boards per panel, then depanelize (§1, §9).
- AOI false calls and their review are not separate steps: only boards with a confirmed defect go to touch-up.
- Breakdowns stop a machine for minutes to hours; minor stops (feeder reloads, vision rejects) and speed losses are not modelled, nor are planned maintenance and breaks.
- Test results are fixed probabilities (probability routing, §5): a real FPY drifts with the process, the fixture and the board revision.
- Operators have no skills or names; a debug operator can do every repair, and the debug crew is the same on both shifts.
- Releases are whole work orders at shift start with first-in, first-out at every station; real lines dispatch by priority and group work by family (PRD P1).
- The rack variant of §4.3 is not part of the template and was not researched.

## 5. Domain metrics (REQ-015)

**Notation.** For each replication r, the measurement window is [W, H], and T = H−W (ticks). For a resource ρ: capacity C; calendar capacity S(t) (S≡C without a calendar); number of down units D(t) (0 without breakdowns); number of units in use U(t) (current `in_use`); of these, K(t) are held by blocked entities (0 without finite buffers) and Z(t) are in setup (0 without setups); processing P(t) = U(t)−K(t)−Z(t); available scheduled capacity A(t) = min(S(t), C−D(t)). All integrals are over [W, H]. Unless noted otherwise, each metric has one value per replication and is aggregated across replications with the current MetricSummary (population = replication, Student-t CI, SIMULATION_SPEC §5).

**Primary resource of a station**: the resource of §3.4 rule 1; metrics use it to represent the station.

| Metric | Formula | Semantic data needed | Unavailable (provisional reason) |
|---|---|---|---|
| UPH (wall-clock hours) | UPHᵣ = Nᵣ / (T/3,600,000), Nᵣ = number of units entering a completed sink in the window (current throughput) | completed sink (`shipped`) | No completed sink: `NO_COMPLETED_SINK` |
| UPH (scheduled hours) | Nᵣ / (L/3,600,000), L = on-shift time of the line calendar within [W, H] (not multiplied by capacity) | Line calendar = the one calendar shared by the main-line stations that have a calendar; stations without a calendar (such as 24/7 burn-in) do not affect it | No main-line station has a calendar, or two or more different calendars are used: `NO_COMMON_LINE_CALENDAR`; L = 0: `NO_SCHEDULED_TIME_IN_WINDOW` |
| First-pass yield FPY (station) | Model input: sum of the pass-flow probabilities of the first-test `test_result` (= 1 − fail-flow probability); for the observed value see §7 | Stations with `pass: first` and their `test_result` (§3.3) | First-test decision or fail flow not found: `TEST_RESULT_NOT_IDENTIFIED` |
| Rolled throughput yield | Product of the FPYs of the main-line test stations (AOI, ICT, FCT, burn-in) (model input) | Same as above; every test station identifiable | Any station unavailable: same as above |
| Simulated first-test pass ratio (station) | passᵣ / (passᵣ + failᵣ), the number of passes through the pass and fail flows of the first-test `test_result` in the window | IR `metrics[]` declares `flow_count` for these flows (D-058) | Not declared: `FLOW_COUNT_NOT_DECLARED`; no first test in the window: `NO_TEST_ATTEMPT_IN_WINDOW` |
| Retest ratio (station) | Count of the flow into the retest buffer / number of first tests | Same as above, plus a declaration for the flow into the retest buffer | Same as above |
| Scrap rate | Nscrapᵣ / (Nᵣ + Nscrapᵣ), Nscrap = number of units entering the abandoned sink | `sink_count` declared for the `scrap` sink (D-058) | Not declared: `SINK_COUNT_NOT_DECLARED`; denominator 0: `NO_EXIT_IN_WINDOW` |
| Line WIP | ∫Nsys(t)dt / T, Nsys = number of entities created and not yet departed (including release backlog, waiting, in service, blocked, delay) | IR `metrics[]` declares `system_wip` (D-058) | Not declared: `WIP_NOT_DECLARED` |
| Buffer WIP | Current `queue_length_mean` | — | As currently defined |
| Blocking ratio (station) | ∫K(t)dt / (C·T) (primary resource) | The model has at least one finite buffer (D-049) | No finite buffer: `NO_FINITE_BUFFERS` (the model assumes unlimited buffers; blocking is not modeled) |
| Mean blocking time (station) | Mean time (minute) of complete blocks (start ≥ W, end ≤ H) | Same as above | Same as above; no complete block: `NO_COMPLETED_BLOCK_IN_WINDOW` |
| Down ratio (station) | ∫D(t)dt / (C·T) | The resource has `breakdowns` (D-051) | `NO_BREAKDOWN_MODEL` |
| Setup ratio (station) | ∫Z(t)dt / (C·T) | The resource has `setups` (D-055) | `NO_SETUP_MODEL` |
| Setup count (station) | Number of setups started in the window (one value per replication) | Same as above | Same as above |
| Availability (station) | ∫A(t)dt / ∫S(t)dt (time availability) | Same as above | `NO_BREAKDOWN_MODEL`; ∫S = 0: `NO_SCHEDULED_TIME_IN_WINDOW` |
| Starved ratio (station) | ∫max(0, A(t)−U(t))dt / (C·T) | The primary resource is used only by this station (first test and retest); these processes need only that one resource, with units = 1, and are not batch processes | Shared resource or multiple resources: `STARVATION_UNDEFINED_SHARED_RESOURCE`; units > 1: `STARVATION_UNDEFINED_MULTI_UNIT`; batch process: `STARVATION_UNDEFINED_BATCH`; in all three cases the same formula is shown as "idle ratio" instead |
| Off-shift ratio (station) | 1 − (processing + setup + blocking + down + idle), processing = ∫P/(C·T) | — | — |
| Scheduled utilization (station) | ∫min(U, S)dt / ∫S dt (D-052) | The resource has a calendar | Without a calendar it equals `utilization_mean` and is not listed separately; ∫S = 0: `NO_SCHEDULED_TIME_IN_WINDOW` |
| Overtime (station) | ∫max(U−S, 0)dt / 60,000 (minute) | The resource has a calendar | Same as above |
| Line balance | LBᵣ = mean_s(w_s) / max_s(w_s), w_s = ∫min(P, S)dt / ∫S dt (the primary resource of each main-line station, each resource counted once) | At least 2 main-line stations identifiable | Fewer than 2 stations: `TOO_FEW_STATIONS`; max w = 0: `NO_PROCESSING_IN_WINDOW`; station role missing: `STATION_ROLE_MISSING` |

Notes:

- **The six state ratios sum to 1**: processing P, setup Z, blocking K, down D, idle max(0, A−U), and off-shift (the rest). Finish-first lets U temporarily exceed A (overtime, or a breakdown whose unit has not gone down yet); then idle is 0 and the off-shift ratio shrinks accordingly. Because U + D ≤ C always holds, the off-shift ratio never goes below 0.
- **Idle is called starvation only under specific conditions (review L6)**: at a station with a dedicated single resource, units = 1 and no batch process, if an available unit is idle while the buffer has units, dispatch grants it in the same tick, so at event boundaries idle necessarily means the buffer is empty. In other cases idle is not starvation: a shared resource or a multi-resource bundle may be waiting for another resource (D-038 (m)); a request with units > 1 may be waiting to gather enough units; a batch process's chamber may be waiting to fill a batch (the buffer has units, but they are not ready).
- **Data sources (D-058)**: the integral of U is the current utilization. The integrals of K, Z, D, S, A and of min(U,S), min(P,S), max(0, A−U), max(U−S, 0) are all Result members per replication and per resource (listed in D-058), present only when the model uses the corresponding feature. The Collector integrates them directly from the step functions of the observations (integer ticks); they cannot be derived afterwards from the per-feature aggregates (for example, ∫min(P,S) and ∫max(0, A−U) both need the quantities at the same time). The line calendar's on-shift time L is not in the Result: the pack computes it with integer arithmetic from the IR's calendar and `measurement_window`.
- **Candidate bottleneck**: the station with the largest w_s (on ties, the one with the smaller node ID), shown in the UI as "the station with the highest work ratio". This is a common reading, not a proof.
- **Not called OEE**: P0 does not compute OEE. Utilization is not OEE; availability is only time availability, without the performance (ideal cycle time) and quality factors. Neither the UI nor the AI analyst may label any metric as OEE.
- **FPY is an input**: under probability routing, the simulated first-test pass ratio is only the input probability plus sampling error, not a prediction. P0 shows it as a model input and compares it with observed values in §7. After `flow_count` is declared, the "simulated first-test pass ratio" and the "retest ratio" are used only to check that the model behaves consistently with its inputs, and the UI must label them so.
- **Missing features are not filled with 0**: when the model has no breakdowns, finite buffers, setups or calendars, the corresponding metric returns unavailable and states the model's assumption, rather than showing 100% availability or 0 blocking (REQ-015: no fabrication).
- Lead time, queue wait, utilization and failure/rework count keep their current definitions; lead time starts at release (arrival) and includes the release backlog.

## 6. Canonical fields of the station event log (import mapping)

There are two kinds of records. Among the corresponding canonical-event fields, `end_timestamp`, `shift` and `product` are optional fields added by D-059 (T-046 phase 2 added them to `canonical-event.schema.json`; fixtures `valid-canonical-event-station.json` and `valid-canonical-event-downtime.json`; T-017 added them to the mapping's target fields (D-070 (a), PROPOSED), see TWIN_IR_SPEC §7).

**Operation record** (one operation or test of one board at one station):

| Logical field | Required | canonical-event | Description |
|---|---|---|---|
| serial | Yes | `case_id` | Serial number of the board or rack |
| station | Yes | `stage` | Station; during mapping the user maps it to the IR's station |
| record type | Yes | `event_type` | Pack vocabulary: `operation`. When the CSV has no such column, a constant transform is needed (CB-28): T-017 did not implement it, and the owner decided in H-021 to open D-073 for it (APPROVED, H-024, before T-052; D-070 (b), CB-43 (e)) |
| equipment | Recommended | `resource_id` | ID of the machine, tester or chamber; used to infer capacity and to match downtime |
| start | Yes | `timestamp` | RFC 3339 with offset; a time without a time zone is always rejected (TWIN_IR_SPEC §7) |
| end | Yes (needed for inference) | `end_timestamp` (new) | ≥ start |
| result | Required at test stations | `outcome` | `pass`/`fail`; other values are a data-quality issue |
| shift | Optional | `shift` (new) | Shift label, used only for grouping in §7 |
| duration | Optional | `duration_ms` | A data-quality issue when inconsistent with end−start (TWIN_IR_SPEC §7) |
| product/family | Required with multiple products | `product` (new, D-059) | Maps to the IR's entity type; inferring setups and per-product cycle times needs it (D-055) |
| operator ID | **Not imported** | — | Personal data. It stays unmapped during mapping and never enters a dataset, prompt or telemetry (the vocabulary has no field for it; if put in metadata, T-017 cannot recognize it, CB-43 (f)) |

**Downtime record** (one downtime period of one piece of equipment): `case_id` = equipment ID (canonical-event requires case_id), `event_type` = `downtime`, `resource_id` = equipment ID, `stage` = station, `timestamp` = downtime start, `end_timestamp` = recovery, `outcome` = reason code (optional). Planned downtime (breaks, maintenance), if a reason code identifies it, is excluded from breakdowns during inference.

**Data-quality checks (proposal; T-052 implemented the vocabulary and required-field rules as import validation warnings, see §12 and D-118)**: end < start; overlapping operation times for the same serial; more operations on one piece of equipment than its inferred capacity; a serial's station order inconsistent with the topology; downtime and operations overlapping on the same equipment (operations during downtime); data gaps within the observation period. The research draft says that micro-stops are often not recorded and that aggregate OEE is not enough to model breakdowns (not verified). So P0 estimates breakdown parameters only from per-event downtime records; without them, `breakdowns` is entered by the user or needs_input is returned, and it is never back-derived from OEE.

**Derivable quantities (T-027's scope, for reference only here)**: service-time samples per station = end−start (empirical dataset); first test and retest are told apart by serial + station, sorted by start; routing probabilities and FPY are counted from the results; TTR = recovery − downtime start (wall-clock time); TTF = on-shift time between the previous recovery and the next downtime start for equipment with a calendar (matching D-051's clock that advances only on shift; estimating from wall-clock intervals counts nights and weekends and underestimates the on-shift breakdown rate, for example to only about 48% under `two_shift_weekdays`), and wall-clock time for equipment without a calendar (equipment that runs only while the line is on shift must be given the line's calendar even without an operator partner: otherwise R4 does not warn, and a TTF estimated from wall-clock intervals underestimates the on-shift breakdown rate; review round 4 L1); the release rate is counted from the first station's start per time period; setup time is the interval from the previous unit's end to the next unit's start when two adjacent units on the same equipment have different products (it may include idle time, so it is only an upper bound and needs the user's confirmation); equipment concurrency estimates capacity from simultaneous operations. Inference results always go through a proposal and human approval (D-009); they never change the Twin directly.

**Time mapping.** Model time has no time zone. For comparison and inference, the user specifies an anchor (for example, model t=0 = a specific RFC 3339 instant with offset, which is also the start of the calendar period). In P0, a window that crosses a DST change returns a data-quality issue.

## 7. Comparing simulation with observation (REQ-025)

This is done only when the baseline run has a linked observed dataset. The following is the D-060 proposal.

| Quantity | Observed | Simulated | Class |
|---|---|---|---|
| Line output per period (day, week, or shift) | Number of serials that completed an operation at the last station (packing and shipping) within the window ÷ number of periods | Each replication's throughput ÷ number of periods | **Output comparison** |
| Station queue wait (mean) | This station's start − that serial's end at the previous station (includes transport and upstream blocking, so it is not exactly the simulation's definition; marked "approximate") | Current queue wait + upstream blocking (when there are finite buffers) | Output comparison (approximate) |
| Lead time (mean) | Time from release (the first station's start, or a release event in the log) to shipping | lead time (counted from arrival, includes the wait before SMT; marked "approximate" when the start points differ) | Output comparison (approximate) |
| Station cycle time (mean/p50/p95) | end − start | The model's service-time distribution | **Input consistency** |
| FPY (station) | Serials whose first operation passed ÷ serials with an operation | Model input (§5) | Input consistency |
| Availability, MTBF, MTTR (station) | Downtime records (within scheduled time) | The model's TTF/TTR and the simulated availability | Input consistency |

- **"Input consistency" is not validation**: it checks whether the model's parameters match the data. Only "output comparison" can show that model behavior is close to observation. The UI shows the two in separate sections.
- **Presenting the error**: observed value o; simulated value per replication xᵣ. Show the simulated mean x̄, the simulated range (the Type 7 2.5% and 97.5% quantiles when R ≥ 20, otherwise min–max, stating which one is used), the absolute error x̄−o, the relative error (x̄−o)/o (null when o = 0, reason `OBSERVED_ZERO`), and whether o falls within the simulated range. When the model declares `period_throughput` in IR `metrics[]` (with the same period as the compared period, D-058), a per-period comparison is also done: each period's observed value against that period's simulated replication range, shown as a chart. Without the declaration there is only the per-period average.
- **In-sample and hold-out**: if the model's parameters (empirical datasets or inference sources) come from data that covers the observed window, mark the comparison **in-sample** and state that it is not validation. The recommendation is to infer from the first few weeks and compare against the last week (T-052's synthetic data should provide such a split).
- **Unavailable (provisional reasons)**: no linked observed dataset: `NO_OBSERVED_DATA`; observed window shorter than one period: `OBSERVED_WINDOW_TOO_SHORT`; a needed field is not mapped (for example, no result): `OBSERVED_FIELD_MISSING`; the two windows are not whole periods or cannot be aligned with the anchor: `OBSERVED_WINDOW_NOT_ALIGNED`; the simulated metric is unavailable: `SIMULATED_METRIC_UNAVAILABLE`.
- **No claim of calibration**: no "pass/fail" verdict is given, and model parameters are not adjusted automatically. The AI analyst may only cite these numbers and suggest changes through a proposal (REQ-016, D-016). Demo data is always marked synthetic.

## 8. Demo flow (proposal from the research draft, used as input)

1. Upload one week of synthetic station log (T-052 generates it with a fixed seed from a known ground-truth model).
2. Mapping and validation → freeze the dataset → the AI proposes stations, cycle-time distributions, yield and retest loops, and buffers → an engineer reviews and approves (D-009).
3. Baseline: UPH, candidate bottleneck, WIP, utilization, blocking and starved ratios, and the weekly output comparison between simulation and observation (§7).
4. Scenarios: "ICT +1 tester" and "larger buffer before FCT", compared and explained by the analyst. The second scenario needs D-057 (buffer capacity can be changed in a scenario); if D-057 is not approved, it must publish a new version, which is not comparable with the baseline (SIMULATION_SPEC §7).

## 9. Deferred items and limitations

- **Listed as P1 in the PRD, stays P1**: service interruption (preemption for breakdowns, shift change and setups); priority and dispatching rules (including setup sequencing grouped by product); advanced routing; connectors such as MES/CFX.
- **The agent recommends including these in P0, following H-017's direction (pending the owner's decision)**: different staffing per shift and exception days (D-052), release tables (D-053), and the simulated first-test pass ratio, retest ratio, scrap rate and per-period comparison (D-058's declarative generic metrics).
- **Recommended as separate tasks after the core is done**: buffers with capacity 0; panelization/depanelization; holding a resource across steps (a rack debugged in its slot); a breakdown clock that runs on busy time; not starting work that cannot be finished; AGVs and transport systems (P0 uses delay). The cost and rationale of each are in the table under ADR-026 Consequences.
- **Not modeled**: operator skills; the detailed balance of equipment inside the SMT line.

## 10. Decisions proposed by this document

### D-058 Data sources and Result location of the electronics line metrics (gate: T-051)

**Question.** The pack's `ComputeMetrics` sees only the Result, not station roles (domain_attributes, resource role), calendars or routing probabilities. Where in the Result do the electronics line metrics go, and which generic statistics are needed?

**Options.**
- A: Add an optional interface (for example `ModelMetricsProvider`: `ComputeModelMetrics(ir, result)`, where ir is the effective IR the engine actually ran), modeled on `TemplateProvider`, so the DomainPack method set does not change. The pack's metrics go into a new optional `metrics.domain` map in the Result (omitted when empty); `metrics.dora` is untouched (Software Delivery results and digests do not change). The Result also gains the following generic members, present only when the model uses the corresponding feature (D-047); the pack computes the §5 metrics only from them and the IR; FPY is a model input, not a run result. Consequences: results of existing models do not change; the metrics are in result_digest and the analyst can cite them; the generic kernel still does not read domain attributes.

  **Per replication, per resource** (review M3; `replication_summaries[i].resource_states[<resource ID>]`; the names were finalized in T-046 phase 2 (`simulation.schema.json#/$defs/ResourceState`; each group of members appears together through `dependentRequired`); values are integer-tick integrals or counts within [W, H]):

  | Member | Content | Present when |
  |---|---|---|
  | `in_use_ticks` | ∫U | Always, when the object exists |
  | `blocked_ticks` | ∫K | The model has finite buffers |
  | `setup_ticks`, `setups_started`, `setups_completed`, `completed_setup_ticks` | ∫Z, number of setups, number and total time of complete setups | The resource has setups |
  | `down_ticks`, `breakdowns_started`, `repairs_completed`, `completed_repair_ticks` | ∫D, number of downtimes, number and total time of complete repairs | The resource has breakdowns |
  | `scheduled_ticks`, `scheduled_in_use_ticks`, `overtime_ticks` | ∫S, ∫min(U,S), ∫max(U−S, 0) | The resource has a calendar |
  | `available_ticks`, `idle_ticks` | ∫A, ∫max(0, A−U) | The resource has breakdowns or a calendar |
  | `scheduled_processing_ticks` | ∫min(P,S) | Any feature above applies to the resource |

  A resource with none of these features does not appear in `resource_states`: its processing = utilization and idle = 1−utilization, which the pack derives from the current `resource_utilization`.

  **Per replication, per node**: `replication_summaries[i].blocking[<node ID>]` (when the model has finite buffers, for nodes that can be blocked): number of blocks, number and total ticks of complete blocks, and ∫number of blocked entities (`blocks_started`, `blocks_completed`, `completed_block_ticks`, `blocked_entity_ticks`); `replication_summaries[i].batches[<process ID>]` (batch processes): number of batches started and total members (`batches_started`, `members_started`). T-046 phase 2 also added, per the D-049 statistics list, `replication_summaries[i].buffers[<queue ID>].full_ticks` (the number of ticks a finite buffer is full, D-063 (b)).

  **Declarative generic metrics** (computed only when declared in IR `metrics[]`; `metrics.additional_metrics[<metric id>]` is the cross-replication summary and `replication_summaries[i].declared[<metric id>]` is the per-replication value): `system_wip` (∫Nsys/T), `flow_count` (number of passes through the given flow in the window), `sink_count` (number of units entering the given sink, including abandoned), `period_throughput` (array of completed units per period for the given period; the window must be whole periods). These three counts are suggestions the agent added following H-017's direction (originally deferred, pending the owner's decision). Details (review N5): `flow_count` counts at routing resolution, at the same time as the routing draw and the failure/rework observation (D-049); a later block of that entity does not affect the count. `period_throughput` periods start at W; when H−W is not a whole multiple of the period, it is unavailable (provisional reason `WINDOW_NOT_WHOLE_PERIODS`). A declared metric ID must not equal a built-in `additional_metrics` key (for example `replication_mean_lead_time_minutes`); the validator returns `DUPLICATE_ID` (path `/metrics/<i>/id`). Mapping the other existing IR `metrics[]` types to the Result still belongs to T-029 (CB-22).

  The line calendar's on-shift time L is not put into the Result; the pack computes it from the IR's calendar and `measurement_window` (integer ticks).
- B: Keep `ComputeMetrics(result)` and have the engine write station roles into the Result. Consequence: the generic kernel would read domain_attributes, which violates ADR-010; not adopted.
- C: Compute the metrics only on the UI/analyst side from IR + Result, not in the Result. Consequences: they are not in the digest, the analyst's evidence cannot cite them (REQ-016), and the two implementations easily diverge.

**Recommendation: A.**

### D-059 Canonical fields of the station event log (gate: before the electronics line's T-017 mapping, T-027 inference and T-052)

**Question.** The current canonical-event has only one time per record, with no end time and no shift, and downtime records have no serial. How do these map?

**Options.**
- A: Add optional `end_timestamp` (RFC 3339, ≥ timestamp, must agree with duration_ms when both are present), `shift` (string ≤ 64) and `product` (string ≤ 128, mapped to an entity type during mapping; required with multiple products, D-055) to canonical-event. The pack defines the vocabularies of `event_type` (`operation`, `downtime`) and `outcome` (`pass`, `fail`, reason codes). Downtime records use the equipment ID as `case_id`. Operator ID is not imported (§6). Consequences: most exports with one operation per row can be mapped directly; the schema change is additive.
- B: Do not change the schema: split each operation into two events, start and end, and put the shift in `metadata`. Consequences: users must convert their files first; `metadata` has no transform (CB-28).
- C: Keep `duration_ms` and add a "subtract one column from another" transform. Consequences: the transform needs parameters (CB-28 does not support them yet), and the original end instant is lost.

**Recommendation: A.** The constant transform (when the CSV has no event_type column) belongs to CB-28 and is decided by T-017.

### D-060 Metrics and error for comparing simulation with observation (gate: T-052)

**Question.** Which quantities does REQ-025 compare, how is the error presented, and when is it unavailable?

**Options.**
- A: The §7 approach: compare the two classes of quantities, "output comparison" and "input consistency", using per-period averages; show the simulated mean, the replication range, the absolute and relative error, and whether the value falls within the range; mark in-sample/hold-out; give no pass verdict and do no automatic calibration. Consequences: it honestly shows the gap between model and data without creating an impression of "validated"; per-period series are deferred.
- B: Only show the observed and simulated numbers side by side, without computing an error. Consequences: simplest; users must interpret it themselves, and REQ-025's "error" is not delivered.
- C: Statistical tests (t-test, KS, etc.) giving pass/fail. Consequences: gives a false sense of "validated"; one week of observation is only one sample, so the tests are meaningless.

**Recommendation: A.**

## 11. T-051 implementation (hackathon slice, 2026-10-26, H-029)

This section records what the T-051 hackathon slice has implemented. Details the approval text does not cover are D-117 (**PROPOSED**); validation is in [checks/T-051_VALIDATION.md](../../checks/T-051_VALIDATION.md). The rest of T-051's scope (declarative metrics and so on) is not implemented yet; see that report.

**Pack.** `internal/domainpack/electronicsline`, domain `electronics_line`, registered by `internal/packs` together with the software delivery example pack. The catalogs are §3.1 and §3.2 (tests compare against this document's two tables). The rules are §3.4: rule 1 `STATION_RESOURCE_INVALID` (error), rule 3 `RETEST_RESOURCE_MISMATCH` (error), rule 2 `TEST_RESULT_NOT_IDENTIFIED` (warning); an unusable `station`/`pass`/`result_of` is `DOMAIN_ATTRIBUTES_INVALID` (warning); rule 4 is confirmed by the template's tests (`metadata.synthetic: true`, `source: "synthetic, for demonstration"`); rule 5: the generic validator's rules are not repeated. `InferFromEvents` returns `ErrUnavailable` until T-027.

**Template `electronics_assembly_test_line`.** The §4.1 topology (41 nodes, 47 flows) and the §4.2 parameters, plus the two product families and the SMT setups of §4.4. All are synthetic (not measured values from any factory); since H-046 their magnitudes follow the sources of §4.5:

| Item | Value in the template | Source |
|---|---|---|
| Release | `release_a` (`board_a`) at 06:00 on weekdays and `release_b` (`board_b`) at 14:00 on weekdays, 360 boards each, table, period 10080 | §4.4 |
| Calendar `two_shift_weekdays` | Monday to Friday 06:00–14:00 `early`, 14:00–22:00 `late` (the [360, 1320) + d·1440 of §4.2 split into two labeled shifts; S(t) unchanged) | §4.2; the labels are D-117 (g) |
| SMT line `smt_line` | 1 line, calendar, TTF exp(600), TTR tri(10, 20, 90); setup A↔B tri(20, 35, 60), initial `board_a`; service tri(0.75, 0.85, 1.15); printing, SPI, placement and reflow treated as one server (§2) | §4.2, §4.4, §4.5 |
| AOI `aoi_machine` (role `aoi`) | 1 machine, TTF exp(1200), TTR tri(5, 10, 30), service tri(0.2, 0.25, 0.35); yield 0.97, defects go through touch-up (`debug_operator`, tri(3, 5, 10)) into ICT | §4.1, §4.2 |
| ICT `ict_tester` | 2 testers, TTF exp(900), TTR tri(10, 20, 60); first test and retest tri(1.4, 1.7, 2.3); yield 0.95, retest 0.90; debug tri(5, 10, 30), repaired 0.97, the rest scrapped | Same as above |
| FCT `fct_tester` | 3 testers, TTF exp(900), TTR tri(10, 30, 90); first test and retest tri(2.8, 3.2, 4.0); yield 0.93, retest 0.90; debug tri(10, 20, 60), repaired 0.95 | Same as above |
| Burn-in `burn_in_chamber` | 9 chambers, 24/7, TTF exp(10080), TTR tri(60, 120, 480); batch min 20, max 40, max_wait 120, constant 480 per batch; yield 0.995, defects return to FCT debug | Same as above |
| Debug `debug_operator`, packing `pack_station` | 4 people, 2 stations, calendar; `shift_capacity` equals capacity in both shifts (numbers unchanged; there for D-057 per-shift staffing scenarios) | §4.2; D-117 (g) |
| Buffers | `q_aoi` 20, `q_ict` 10, `q_fct` 10, `q_burnin` 120, `q_pack` 500; unlimited before SMT, touch-up, debug and retest | §4.1 |
| 2D positions | The main line is in the first row (x from left to right); each station's touch-up, debug and retest loops are below it; processes sorted by x, then by y, give the row order of the Marey chart (SMT, AOI, touch-up, ICT, ICT debug, ICT retest, FCT, FCT debug, FCT retest, burn-in, packing) | D-117 (g) |

Names come in two languages, en and zh-Hant (`templates/*.zh-Hant.names.json`); IDs and all other content are the same in both languages.

**Confirming the §4.2 estimate with the engine.** The template runs on des-go-0.5.0 (horizon 28 days, warmup 7 days, 5 replications, seed 2026; the result's digest is locked in `internal/domainpack/electronicsline/testdata/template-run-lock.json`, a regression lock on engine output, not an independent oracle). Over the measured 21 days, an average of 10,733 boards shipped (about 3,578 per week against a release of 3,600; WIP did not keep accumulating). The on-shift work ratio (w_s of §5) is highest at FCT, 0.897, followed by ICT 0.710, SMT 0.687, burn-in 0.486 (24/7), packing 0.410 and AOI 0.200, consistent with §4.2's "FCT is the expected bottleneck". SMT setups are about 2 per day (30 in 21 days). The mean burn-in batch size is 39.2 boards. These are the numbers of H-046's template; T-051's template gave 10,756 boards and work ratios FCT 0.898, ICT 0.843, SMT 0.825, AOI 0.701, packing 0.411, burn-in 0.365, and the lock was re-taken for H-046 ([H-046 report](../../checks/H-046_ELECTRONICS_REALISM_VALIDATION.md)). These are simulation outputs of a synthetic model, not factory numbers; H-046 tuned the parameters to the sources of §4.5, not to these outputs.

**Metrics (`metrics.domain`).** Line metric keys are the metric name; station metric keys are `<name>.<first-test node ID>`. Each has one value per replication and is aggregated across replications the same way as the Result's other statistics (population replication, mean, Type 7, Student-t CI). This build:

| Metric | Scope | Unit | This build |
|---|---|---|---|
| `uph`, `uph_scheduled` | Line | entity_per_hour | Available (unavailable when there is no completed sink, no shared line calendar, or no on-shift time in the window) |
| `rolled_throughput_yield_input`, `fpy_input` | Line, test station | ratio | Available (model input; `TEST_RESULT_NOT_IDENTIFIED` when a test result cannot be found). The rolled throughput yield multiplies one FPY per test station kind, in line order (AOI, ICT, FCT, burn-in). Several first tests of the same station kind are parallel cells and count once: when their FPYs are equal within the routing tolerance 1e-9, the value of the first first-test in node order is used; when they differ, `PARALLEL_TEST_YIELDS_DIFFER` (D-117 (c), PROPOSED; review r1 M1). `fpy_input` has one value per first test |
| `line_balance`, `work_ratio` | Line, station | ratio | Available; line balance counts each primary resource once (parallel cells that share a tester count once) |
| `processing_ratio`, `setup_ratio`, `blocking_ratio`, `down_ratio`, `idle_ratio`, `off_shift_ratio` | Station | ratio | Available; when the model has no setups, finite buffers or breakdowns, respectively `NO_SETUP_MODEL`, `NO_FINITE_BUFFERS`, `NO_BREAKDOWN_MODEL` |
| `starved_ratio` | Station | ratio | Available for stations with a dedicated single resource, units 1 and no batching; otherwise §5's `STARVATION_UNDEFINED_*` (the same formula is shown as `idle_ratio`) |
| `availability`, `setups_started`, `mean_blocking_minutes` | Station | ratio, count, minute | Available (same conditions as above; `NO_COMPLETED_BLOCK_IN_WINDOW` when there is no complete block) |
| `scheduled_utilization`, `overtime_minutes` | Stations with a calendar | ratio, minute | Available; stations without a calendar are not listed (§5) |
| `batches_started`, `mean_batch_size`, `batch_fill_ratio` | Station | count, entity, ratio | Available for batch stations; other stations `NO_BATCH_MODEL` |
| `line_wip`, `scrap_rate`, `simulated_first_pass_ratio`, `retest_ratio` | Line, test station | entity, ratio | **unavailable** (`WIP_NOT_DECLARED`, `SINK_COUNT_NOT_DECLARED`, `FLOW_COUNT_NOT_DECLARED`): they need D-058's declarative metrics, whose declarations this build still rejects with `PRIMITIVE_NOT_SUPPORTED` |

The UI labels (kinds and metric names, en and zh-Hant) are in `domain.electronics_line.*` in `apps/web/messages`. P0 does not compute OEE, and no metric is called OEE.

**Review r1 fix (M1, 2026-10-26).** The original rolled throughput yield multiplied one FPY for each test process with `pass: first`. When a parallel FCT cell was added to the template (both cells with FPY 0.93), it gave 0.97·0.95·0.93·0.93·0.995 = 0.79302032325 instead of §5's series value 0.97·0.95·0.93·0.995 = 0.852710025, and the pack's rules raised no issue. Now it is once per station kind, by the rule in the table above. When parallel cells have different FPYs, that station kind has no single FPY (it would need weighting by routing share, which the pack does not derive), and the rolled throughput yield is unavailable. The pack does not analyze routing: two first tests of the same station kind that are actually in series (not the §2 structure) are also treated as parallel cells. The template has only one first test per test station kind, and the node order of the first tests is the line order, so the product is bit-for-bit identical to before the fix and the digest in `testdata/template-run-lock.json` is unchanged (H-046 later changed the template's parameters and so the digest, §4.5). Validation: "Review round 1 fixes" in [checks/T-051_VALIDATION.md](../../checks/T-051_VALIDATION.md).

## 12. T-052 implementation (hackathon slice, 2026-10-26, H-029)

This section records what the T-052 hackathon slice has implemented. Details the approval text does not cover are D-118 (**PROPOSED**); validation is in [checks/T-052_VALIDATION.md](../../checks/T-052_VALIDATION.md). The rest of T-052's scope (D-073's transform, linking datasets and runs after commit, and so on) is in that report.

**Synthetic data (step 1 of §8).** [`testdata/datasets/electronics-line`](../../testdata/datasets/electronics-line/README.md): one week of station log (22,569 rows, 2,606,756 bytes, every row `synthetic=true`; regenerated when H-046 changed the template, before it 22,739 rows), the ground-truth model (this pack's template, §11) and how the data was generated, and 7 negative fixtures. `scripts/gen-electronics-line-data` records the template's replication 0 (seed 52) with the engine's Observer, without writing a separate simulation. The same seed gives the same bytes (SHA-256 pinned in a test), and the engine's own counts for that replication in the same window (shipped, test failures, repairs, downtime per machine, SMT setups) equal the log's. Time uses 2026-09-28T00:00+08:00 as model time 0 (the anchor of §6).

**The data-quality rules of §6.** Import validation uses the base version the import is bound to as its vocabulary and checks the D-059 rules on every valid row. The results are warnings (D-072 (b)), and the import stays VALIDATED: `EVENT_TYPE_UNKNOWN`, `EVENT_STAGE_MISSING`, `EVENT_STAGE_UNKNOWN`, `EVENT_END_MISSING`, `EVENT_OUTCOME_MISSING` (test stations), `EVENT_OUTCOME_UNKNOWN`, `EVENT_PRODUCT_MISSING` (multiple products), `EVENT_PRODUCT_UNKNOWN` (D-118 (b)). An end earlier than start is still a D-072 (b) invalid row. The cross-row checks listed in §6 (overlapping operations of a serial, exceeding capacity, station order, operations during downtime, data gaps) are not implemented yet.

**The comparison of §7.** `electronicsline.CompareObserved` (computed with `internal/results/observed`): the line's weekly and daily output (output comparison), FPY per test station and mean cycle time per station (input consistency); D-060's simulated mean, replication range, absolute and relative error, and whether the value is within range; when data is insufficient, the §7 reason (D-118 (d)). For the synthetic data (marked `model_generated`: the data was produced by the same model it is compared with, so it is neither in-sample nor hold-out, and this only checks the pipeline; see review r1 below):

| Quantity | Observed (log) | Simulated (seed 2026, 5 replications, horizon 14 days, warmup 7 days) | Note |
|---|---|---|---|
| `line_output_per_week` | 3,563 | Mean 3,567.4, range (min–max) 3,510–3,593, absolute error 4.4, relative 0.12%, within range | With the generator's seed (52) and 1 replication, the simulation is the log and the error is 0 |
| `first_pass_yield.fct` | 0.9396 | Model input 0.93 | Input consistency |
| `mean_cycle_minutes.fct` | 3.329 | Mean 3.333 of the model's tri(2.8, 3.2, 4.0) | Input consistency; log times are truncated to whole seconds |

The table holds the numbers of the data regenerated for H-046's template (§4.5). With T-052's template they were 3,588 observed against a simulated mean of 3,613 (range 3,555–3,677, absolute error 25, relative 0.70%), FPY 0.9399 and mean FCT cycle 3.328.

These are numbers from a synthetic model and from data it produced itself, not factory numbers. No pass verdict is given, and the model is not adjusted (D-060).

**Review r1 fixes (2026-10-26; D-118 (d) is still PROPOSED).**

- **No observed data in the window (M1)**: originally the output comparison returned `NO_OBSERVED_DATA` only when the whole event list was empty. For a window the log did not cover, the observed "number of operations ending in the window" became 0 or half, yet the result was available (the week after the log: observed 0, simulated 3,613; two weeks with the log covering only the second: observed 1,794, relative error +101.4%). That violated D-060's "unavailable when there is no observed data" and AGENTS.md §2. Now `line_output_per_week` and `line_output_per_day` require at least one log event to **start** (any record type, operation or downtime) in every unit of the window [From, To) (unit = lcm(period, model cycle); one week for this template, so daily output is also checked in weekly units); otherwise the result is `NO_OBSERVED_DATA`. Partial coverage is also `NO_OBSERVED_DATA`, not `OBSERVED_WINDOW_TOO_SHORT`: §7's TOO_SHORT means the window itself is shorter than one period, while here the window is long enough and the data for one of its units is missing. The check runs after alignment (units are defined only for an aligned window) and before `NO_COMPLETED_SINK` and `OBSERVED_FIELD_MISSING`. Input consistency (FPY, cycle) is a ratio and mean over the events in the window, not a per-period count, so this rule does not apply to it; when the window has no events for the quantity, it is `NO_OBSERVED_DATA` (as before; now with a test). Limitation: one event is enough to cover a unit. A more complete approach is for the caller to pass the dataset's observation period and require [From, To) to lie within it; that needs the dataset metadata after T-018 (the rest of T-052's scope).
- **Counting by end (L1)**: a test with a handwritten event makes a packing operation span both the window's start and its end, and shows that the shipment is recorded in the week of its end and each board counts only once.
- **Labeling (the coordinator's decision on question 4 and I2)**: data produced by the same model it is compared with must not be marked hold-out. `observed.Sample` gains `model_generated` (`observed.ModelGenerated`), which the pack's tests and the API test use (the comparison has no API or UI yet; when the comparison of this data is shown later, it is marked as well). `Sample` is an internal Go value with no schema or OpenAPI contract; the comparison numbers do not change (table above).

## 13. T-056: photo concepts (2026-10-26, H-033; D-131 PROPOSED)

The pack implements `domainpack.PhotoVocabulary`: the segmenter looks for 8 concepts, in order, in a line photo or floor plan (T-055's `provider.SegmentRequest`: at most 8, lowercase English noun phrases, one call per concept, one SAM image): `smt line`, `aoi machine`, `ict tester`, `fct tester`, `packing station` (the stations of §2, in line order), `conveyor belt` (the conveyor between stations, i.e. the line buffer of §3.1), and the shapes of a drawn floor plan, `rectangle` (a workstation drawn as a box) and `arrow` (flow direction). This is the pack's static data, not the tenant's. The vision model proposes workstations and flows from the image and these objects (which station a floor-plan box is gets decided from its label in the image; the label is data). New steps may not outnumber the boxes, numbers may only reuse values the base already has (`review.Photo`), and the editor arranges the 2D positions. The demo image is a synthetic floor plan drawn by `scripts/gen-photo-demo` (`testdata/photos/`), not a photo of any factory.

**Adjustment after the first live run (2026-10-27; D-131 (h), PROPOSED).** The original 8 concepts were `smt line`, `aoi machine`, `ict tester`, `fct tester`, `burn-in chamber`, `packing station`, `conveyor belt`, `workstation`. In the first live photo smoke (run `01M5XDJ3T0TTGE55K3A3JVK29N`), SAM 3.1 answered all 8 concepts on the demo floor plan but with 0 boxes; the server returned needs_input, and the judge did not run. SAM looks for the things a short noun phrase refers to (Meta's documentation gives the example of finding 6 with `square` in an image of six squares), and fine-grained domain nouns are a known weakness, so the floor-plan shapes `rectangle` and `arrow` were added. To stay at 8 (one image per concept, so SAM spend is still at most USD 0.02), `burn-in chamber` and `workstation`, which the demo does not have, were removed (on the floor plan they are also labeled boxes, found by `rectangle` and identified by their label). The photo prompt template changed to `photo-0.2.0`; the photo rules did not change. The wording still needs live confirmation: `make ai-smoke-photo-live` can ask about other concepts with `AI_SMOKE_PHOTO_CONCEPTS` (or `-concepts`), only in the smoke; the worker never reads it.
