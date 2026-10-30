# Production and Logistics Domain Spec

Status: implemented per **H-045** (owner, 2026-10-29: 「4 個產業 + 建立畫面 (Recommended)」 ("4 industries + a creation screen")), with the templates researched and retuned per **H-046** (「先研究實際工廠, 倉庫的樣子，我希望範本要是符合真實性的」 ("research what real factories and warehouses look like first; the templates must be realistic")). The pack ID is `production_logistics` (Go package `internal/domainpack/productionlogistics`). The catalog, the four templates and the scaling are the agent's choices within H-045 and H-046, reviewed in their independent review; they add no engine primitive, no contract member beyond the four template names, and no migration. Validation: [checks/H-045_PRODUCTION_LOGISTICS_VALIDATION.md](../../checks/H-045_PRODUCTION_LOGISTICS_VALIDATION.md).

**Numbers.** The templates are **synthetic**: realistic structure and magnitudes taken from the public sources in §3, scaled to keep a run small, and not any company's data. Each value in §3 says where it comes from: a source (read on 2026-10-30) or "estimate" with the reasoning. Each template's `metadata` says `synthetic: true`, and a model created from it keeps that labelling.

## 1. Scope

- **What it is**: traditional factories and a distribution center expressed **only with the generic Twin IR primitives** ([TWIN_IR_SPEC](../TWIN_IR_SPEC.md) §2, §2.1): sources with release tables, finite buffers and blocking, single-resource steps, batch steps, shift calendars with per-shift crews, breakdowns, setups (changeovers), probabilistic yields with repair or scrap, and delays that hold no resource.
- **What the pack adds**: the domain ID and its fail-closed rule (`DOMAIN_MISMATCH`), the catalog below for the palette and the AI prompt context, and the templates. It has no domain rule beyond the domain check, no domain metric (a run shows the engine's generic metrics: lead time, throughput, resource utilization, queue waits, failure and rework counts), no event inference (`InferFromEvents` is unavailable) and no photo vocabulary (the photo-to-line path stays electronics only, H-045).
- **Time**: the minute; t=0 is Monday 00:00 model time; calendars have no time zone (D-052).

## 2. Catalog

### 2.1 Node kinds

| Kind | Generic type | Description |
|---|---|---|
| `order_release` | source | Orders, lots or inbound trucks; a release table (D-053) or an interarrival distribution |
| `buffer` | queue | WIP, a staging area or storage before one step; `capacity` makes it finite (D-049) |
| `work_step` | process | A manual or machine step on one resource |
| `batch_step` | process (batch) | An autoclave, a mixer, a pallet trip or a trailer load (D-054) |
| `inspection_step` | process | A quality check, followed by a quality result |
| `repair_step` | process | Repair or correction by people |
| `quality_result` | decision | Pass, repair or scrap; the probabilities are model inputs (yields) |
| `defect_observation` | failure | Counts a defect, reject or pick error (not a machine breakdown) |
| `rework_observation` | rework | Counts a repaired or corrected item |
| `wait_step` | delay | Floor time, proofing, cooling or transport: time without a resource |
| `completed` | sink (completed) | Packed, palletized or shipped: the output |
| `scrap` | sink (abandoned) | Scrapped or wasted; not output |

### 2.2 Resource roles

| Role | Type | Description |
|---|---|---|
| `machine` | machine | An autoclave, mixer, oven, corrugator or folder-gluer |
| `operator` | human | Cutters, stitchers, receivers, pickers, packers |
| `inspector` | human | People who check quality or correct errors |
| `inspection_equipment` | machine | A metal detector, checkweigher or scanner |
| `material_handling` | machine | Reach trucks, strappers, palletizers, conveyors |
| `dock` | machine | Dock doors |
| `generic` | generic | Any other constrained capacity |

The kernel never reads a role (TWIN_IR_SPEC §2); every template resource takes its role from this table with the matching type (a test keeps them so).

## 3. Templates

All four share these conventions: breakdowns count on-shift time only (D-051); shift ends and changeovers are finish-first (D-052, D-055); buffers on the main line are finite, and **buffers before repair or correction are unlimited** so that no full buffer blocks in a loop (D-050 R1); each step needs one resource (so R3 and R4 hold by construction). Routing is by probability, never by product; where families must take different routes (the box plant), each family has its own step on the shared resource. Each template has its own 2D positions; 3D derives from 2D. Breakdown parameters (time to failure, time to repair) are **estimates** in every template: no public source gives them for these machines.

The bottleneck is the resource with the largest on-shift work share in the template's own run (`TestTemplatesThroughTheEngine`) and in the UI's default run (one week, 5 replications, seed 1; `TestDefaultRunShowsTheBottleneck`).

| Template | Item | Bottleneck | Daily load |
|---|---|---|---|
| `shoe_factory` | a pair of canvas sneakers | stitching (72 stitchers) | 600 pairs, Monday to Friday |
| `food_plant` | a lot of 40 loaves | tunnel oven (18 lot positions) | 960 lots (38,400 loaves), Monday to Saturday |
| `carton_plant` | a load of 1,000 sheets or boxes | flexo folder-gluer (makeready at each order change) | 148 loads (148,000 boxes), Monday to Friday |
| `warehouse` | a case | case picking (4 pickers early, 3 late) | 6,000 cases, Monday to Friday |

### 3.1 `shoe_factory`: a vulcanized canvas sneaker factory

**Real-world flow.** Vulcanized shoes are lasted (toe and heel lasting), get their rubber outsole and foxing tape attached with adhesive and pressure, and are loaded on racks into an autoclave, a steam-heated pressure vessel that cures the rubber and bonds it to the upper [S2, S3]. Cutting and stitching come before, and de-lasting and finishing after, as on any athletic shoe line; a complete line with its cutting, sewing, lasting and finishing machines makes about 500–700 pairs in an 8–10 hour day [S1].

**Template.** Cutting orders (high-tops and low-tops alternating) → cutting → cut-kit buffer → stitching (closing) → lasting → sole attaching and foxing → racks waiting for the autoclave → vulcanizing (batch) → cooling racks → de-lasting and finishing → final inspection (repair or scrap) → packing.

| Parameter | Value | Unit | Source |
|---|---|---|---|
| Line output | 600 | pairs a day | [S1] "about 500-700 pairs per 8-10 hours/day"; midpoint of the range |
| Shift | 08:00–12:00, 13:00–17:00, Monday to Friday | | estimate: one 8-hour day shift with a lunch hour, within [S1]'s 8–10 hours |
| Cutters | 12 | people | [S1] "10 cutters" for 500 pairs, scaled to 600 |
| Cutting time | tri(5, 7, 10) | min a pair | estimate: below the 9.6 cutter-minutes a pair that [S1]'s crew implies, so cutting keeps ahead of stitching |
| Stitchers | 72 | people | [S1] "50 to 80 stitchers" for 500 pairs; the midpoint, 65, scaled to 600 pairs is 78; 72 (an estimate) keeps stitching about 90% busy |
| Stitching time | tri(42, 50, 67) | min a pair | estimate: [S1]'s crew implies 48–77 stitcher-minutes a pair (50–80 stitchers × 480 min ÷ 500 pairs); a simple canvas upper at the low end |
| Cut-kit buffer | 150 | pairs | estimate: two cutting orders, so cutting blocks behind stitching |
| Lasting machines | 4 | machines | estimate; [S1] lists toe and heel lasting machines on one assembly line |
| Lasting time | tri(1.4, 1.8, 2.6) | min a pair | estimate: machine cycle at the line's pace |
| Sole and foxing operators | 34 | people | estimate: the largest share of [S1]'s 50 assembly workers for 500 pairs, scaled to 600 |
| Sole and foxing time | tri(17, 21, 28) | min a pair | estimate: a mean of 22, a little under half of the 48 assembly-minutes a pair that [S1]'s crew implies |
| Autoclave load | 150–300 | pairs | [S2] "500 to 1000 pairs may be heated at one time"; scaled to a single line's output; at most 30 min wait once 150 are waiting |
| Vulcanizing time | 90 | min a load | [S2] 75 min at 110 °C for textile shoes; [S3] at least 70–80 min at about 120 °C; plus 15 min loading and unloading (estimate) |
| Finishing operators, time | 14; tri(6, 8, 12) | people; min a pair | estimate (de-lasting, cleaning, lacing) |
| Inspectors, time | 4; tri(1.5, 2, 3) | people; min a pair | estimate |
| Defects found at inspection | 6% | of pairs | estimate: 5% repaired (estimate) and 1% scrapped, the finished-shoe reject rate of [S4] ("around 1% of production") |
| Repair | 2 operators; tri(8, 12, 20) | min a pair | estimate |
| Packers, time | 3; tri(1.2, 1.5, 2.2) | people; min a pair | estimate |

**Simplified or left out.** Skiving, printing, embroidery, insole and sockliner making, and outsole production are not separate steps; the lasts travel with each pair, so the two families need no changeover; cut parts are not inspected separately ([S4] rejects about 5% of cut parts, which this template folds into the cutting time); the autoclave is one vessel sized for one line.

### 3.2 `food_plant`: a pan bread bakery

**Real-world flow.** Commercial white pan bread is made with a straight (no-time) dough: mixing, a short floor time, makeup (dividing, rounding, intermediate proof, sheeting and moulding, panning), final proof, baking, cooling, slicing and packaging [F1]; cooled bread is sliced and bagged at once [F2]. A real plant of this kind runs horizontal mixers into makeup lines, a proofer and a tunnel oven, cools on overhead conveyors, slices and bags, and passes every bag through a metal detector; it runs six days a week on two 10-hour shifts with 4 hours of maintenance and sanitation a day, and moved its sesame products to Fridays to contain the allergen [F4].

**Template.** Dough orders (white bread; sesame bread on Friday's second shift) → mixing (batch) → floor time → makeup → final proof → oven queue → baking (tunnel oven) → depanning and cooling → slicing and bagging → metal detection (reject) → to dispatch.

| Parameter | Value | Unit | Source |
|---|---|---|---|
| Schedule | 04:00–14:00 and 14:00–24:00, Monday to Saturday; 00:00–04:00 sanitation | | [F4] "six days a week on two, 10-hour shifts with 4 hours of maintenance and sanitation each day" |
| Sesame products | Friday's second shift only | | [F4] moved sesame-seeded bread to Fridays |
| Item | a lot of 40 loaves | | estimate (scaling): keeps a day at about 1,000 items instead of 38,400 loaves |
| Line rate | 40 | loaves a minute (one lot a minute while dough is released, 04:00–19:30) | estimate: below two slicer-baggers of [F5] (up to 33 loaves a minute each) |
| Dough batch | 30 lots (1,200 loaves, about 900 kg) | | [F3] industrial mixers of 2,000-lb (about 900 kg) batches |
| Mixing cycle | tri(22, 24, 27) on 2 mixers | min a batch | [F3] "a 10- to 15-minute mix" and a worked example of one batch every 24 minutes; loading and discharge make up the rest (estimate) |
| Floor time | tri(12, 15, 18) | min | [F1] "let dough rest for about 15 minutes" |
| Makeup | tri(0.6, 0.7, 0.9) | min a lot | estimate: at the line rate |
| Topping changeover (white → sesame) | tri(15, 20, 30) | min | estimate; the allergen wet clean back to white falls in the nightly sanitation window [F4], so sesame → white is tri(10, 15, 25) (estimate) |
| Final proof | tri(50, 57, 65) | min | [F1] "proof times are typically 50-65 minutes" |
| Baking | tri(18, 20, 22) | min | [F1] "usually takes 18-22 minutes" at 204–216 °C |
| Tunnel oven | 18 | lot positions (720 loaves) | estimate: sized just below the line rate so the oven paces the line, as a real line is balanced to its oven |
| Cooling | tri(60, 75, 90) | min | estimate: [F2] cools from 93–97 °C to 32–43 °C, with time set by loaf size and conveyor speed |
| Slicer-baggers | 2; tri(1.2, 1.4, 1.8) | machines; min a lot | [F5] slicer up to 33 loaves a minute (40 loaves in about 1.2 min) |
| Metal detector reject | 0.3% | of lots | estimate |

**Simplified or left out.** Pan handling and depanning, bulk ingredient systems, shelf life, temperature, humidity and over-proofing (a full oven queue holds proofed dough without penalty), and the sanitation crew's own work (the sanitation window is a calendar gap).

### 3.3 `carton_plant`: a corrugated box plant

**Real-world flow.** A corrugator makes board and cuts it into sheets; converting machines then make boxes: a flexo folder-gluer prints, slots, folds and glues regular slotted cases in one pass, and a rotary die-cutter cuts trays and displays; loads are bundled and strapped, palletized and shipped [C1–C3]. Order changes cost converting time: a 1997 survey found a flexo folder-gluer or rotary die-cutter producing for only about four hours of an eight-hour shift, with setup the significant part of the loss [C4]; newer machines claim settings of 1–2 min [C1].

**Template.** Four order families: plain and printed shipping cases go through their own corrugating step to the flexo folder-gluer; die-cut trays and displays go through theirs to the rotary die-cutter; both share the corrugator → quality check (reject) → bundling and strapping → palletizing and wrapping → ready to ship.

| Parameter | Value | Unit | Source |
|---|---|---|---|
| Shifts | 06:00–14:00, 14:00–22:00, Monday to Friday | | estimate |
| Item | a load of 1,000 sheets or boxes | | estimate (scaling) |
| Orders | 14 case orders of 8 loads, 6 die-cut orders of 6 loads | a day | estimate: short runs, so changeovers matter |
| Corrugator | tri(2, 2.5, 3.5) | min a load | [C2] up to 400 m/min; at an estimated 300 m/min running speed and two 1.5 m sheets across, about 24,000 sheets an hour |
| Corrugator order change | tri(1, 2, 4) | min | estimate: modern corrugators change orders on the run |
| Flexo folder-gluer | tri(4, 5, 7) | min a load | [C1] rated up to 22,000 boxes an hour; about 12,000 an hour as running speed (estimate) |
| Folder-gluer makeready | tri(10, 15, 25) | min an order change | estimate between [C1]'s 1–2 min claim for its newest machine and [C4]'s finding that setup takes much of a shift on conventional machines |
| Rotary die-cutter | tri(6, 7.5, 10) | min a load | [C3] up to 12,000 sheets an hour; about 8,000 as running speed (estimate) |
| Die change | tri(15, 20, 30) | min | estimate |
| Rejected loads | 1.5% | of loads | estimate; trim and slot waste inside a load (2.5–5% at the corrugator, 5–6% in converting, 3–5% more when die-cut [C5]) is material, not loads, and is not modelled |
| Strapping, palletizing | tri(2.5, 3, 4); tri(2, 2.5, 3.5) | min a load | estimate |

**Simplified or left out.** Paper roll stock, board grades and flutes, the corrugator's trim optimization, warp, glue curing, and finished-goods storage; each family is one design, so a changeover happens only when the family changes.

### 3.4 `warehouse`: a distribution center

**Real-world flow.** Trailers are unloaded and received, cases are palletized and put away to reserve storage, cases are picked to orders, packed and labeled, checked, staged and loaded onto outbound trailers. In picker-to-parts picking about half of the picker's time is travel [W2].

**Template.** Inbound trailers → unloading and receiving → inbound staging → put-away (pallet batch) → reserve storage → case picking → labeling and packing → scan and weight check (correction) → outbound staging → loading outbound trailers (batch) → shipped.

| Parameter | Value | Unit | Source |
|---|---|---|---|
| Shifts | 06:00–14:00, 14:00–22:00, Monday to Friday | | estimate |
| Inbound | 5 trailers of 1,200 cases | a day | estimate: a floor-loaded trailer's case count |
| Unloading | tri(0.45, 0.6, 0.8) | receiver-min a case | [W3] "3 dock workers 4 hours to unload a trailer" (12 person-hours) over 1,200 cases (estimate) |
| Receivers | 9 early, 3 late | people | estimate: three trailer crews of three [W3] on the early shift |
| Put-away | pallets of 24–48 cases; tri(3, 4, 6) min a pallet; 3 reach trucks | | estimate, in line with [W4]'s 18–30 pallets an hour for a two-person unloading team with a forklift |
| Reserve storage | 6,000 | cases | estimate: one day of inbound |
| Case picking | tri(0.35, 0.45, 0.75) | min a case (about 116 an hour) | [W1] "cases picked per hour" went from 110 to 120 (2008); travel included [W2] |
| Pickers | 4 early, 3 late | people | estimate, so picking is about 90% busy |
| Pick errors | 0.3% | of cases | estimate |
| Packing | tri(0.2, 0.25, 0.35) | min a case | estimate |
| Outbound trailers | 600–1,200 cases; tri(30, 45, 60) min a trailer; 2 doors | | estimate |

**Simplified or left out.** Pick paths, slotting and replenishment from reserve to forward pick locations (picking works straight from storage); orders and order lines (every stored case is assumed to have an order, so picking is limited by pickers, not by orders); inbound inspection and cross-docking.

### 3.5 Sources (read 2026-10-30)

- [S1] Shoemakers Academy, "How do I build a shoe factory?" (Elitech Footwear Technology factory design), https://shoemakersacademy.com/how-to-build-a-shoe-factory/
- [S2] Shoemakers Academy glossary, "Vulcanizing Oven", https://shoemakersacademy.com/glossary/vulcanizing-oven/
- [S3] US Patent 10,307,981 B2, "Methods for making a footwear article", background on conventional vulcanization, https://patents.google.com/patent/US10307981B2/en
- [S4] CORDIS, EU project BRST965086 (leather cut-part inspection), https://cordis.europa.eu/project/id/BRST965086/it
- [F1] BAKERpedia, "White Pan Bread", https://bakerpedia.com/processes/white-pan-bread/
- [F2] BAKERpedia, "Bread Cooling", https://bakerpedia.com/processes/bread-cooling/
- [F3] Baking Business, "Choosing bakery mixers for higher-volume production" (2026-10-13), https://www.bakingbusiness.com/articles/67033-choosing-bakery-mixers-for-higher-volume-production
- [F4] Baking Business, "Piantedosi Baking differentiates itself in a growing market" (2023-06-26), https://bakingbusiness.com/articles/59376-piantedosi-baking-differentiates-itself-in-a-growing-market
- [F5] Exapro used-equipment listing of bakery lines (a sliced-bread line with a HOBA Holly slicer, "up to 33 loaves per minute"), https://www.exapro.com/benier-kaak-peerless-winkler-tin-bread-toast-line-p251113110/
- [C1] BOBST, "FFG 8.20 EXPERT" flexo folder-gluer, https://www.bobst.com/cn/en/products/flexo-printing-folding-gluing/ffg-8-20-expert
- [C2] BHS Corrugated, "WIDTH Line" corrugator, https://www.bhs-world.com/en/corrugators/corrugator-lines/width-line
- [C3] Sun Automation, "Kolbus RDS Graphics Series 115" rotary die cutter, https://sunautomation.com/corrugated-converting/kolbus/kolbus-rds-graphics-series-115
- [C4] TAPPI, "New Approach to Increasing Machine Utilization", 1997 Corrugated Containers Conference proceedings, https://imisrise.tappi.org/TAPPI/Products/COR/COR9755.aspx
- [C5] PrintWeek India, "Wastage in brown box manufacturing transpires due to multiple factors", https://www.printweek.in/features/wastage-in-brown-box-manufacturing-transpires-due-to-multiple-factors-55472
- [W1] Supply Chain Digest, on WERC's DC Measures 2008, https://www.scdigest.com/assets/On_Target/08-06-03-1.php?cid=1713
- [W2] arXiv 2401.04563, "Joint Order Selection, Allocation, Batching and Picking for Large Scale Warehouses", citing Tompkins, White, Bozer and Tanchoco, Facilities Planning (Wiley, 2010), https://arxiv.org/html/2401.04563
- [W3] Gorbel, "Return on Investment" (manual trailer unloading), https://gorbel.com/education/warehouse-solutions/return-on-investment
- [W4] Racklify, "How warehouses calculate container unloading costs and labour", https://racklify.com/encyclopedia/how-warehouses-calculate-container-unloading-costs-and-labour/

## 4. Not modelled (approximations)

- **Shelf life, temperature and over-proofing** (bakery): floor time, proof and cooling are fixed-time delays; product that waits does not spoil.
- **Recipes, lot genealogy, merge and split**: one item is one pair, lot, load or case; ADR-026 defers merging and splitting, so a mixer batch of 30 lots stands for one dough.
- **Pick paths, slotting and replenishment** (distribution center): travel is inside the pick time; storage is a count of cases, not a layout.
- **Routing by product**: a family cannot pick its route at a decision; where routes differ by family, each family has its own step on the shared resource (the box plant's corrugating steps).
- **Cleaning** is a calendar gap (scheduled downtime) or a changeover (setup), not a task with its own crew.
- **Breakdowns** use estimated parameters in every template.

## 5. Tests

`internal/domainpack/productionlogistics`: each template in both locales is valid without issues, belongs to the pack's domain, uses only catalog roles, keeps the buffer after every failure unlimited, has its documented release volume and shifts, has a complete zh-Hant names file, and runs deterministically (repeat equality, another seed differs, a regression lock of the result digests in `testdata/template-run-lock.json`, not an independent oracle); the bottleneck above does the most work on shift and is in use at least 80% of it, both in the lock's run and in the UI's default run; the pack passes the DomainPack conformance suite (T-042); the UI's catalogs label every kind in both languages. `internal/packs` checks that the create-model contract names exactly the templates the served packs build.
