// A small electronics assembly and test line (H-016) for the editor's review
// preview (/ui-gallery/editor) and its tests: SMT with changeovers between
// two boards and breakdowns, AOI, ICT with debug and rework, a burn-in
// batch, packing. Every number is synthetic (docs/domains/ELECTRONICS_LINE.md
// §4 shape, smaller): sample data for review, not a factory's and not the
// domain pack's template, which the API serves. Names are user data and
// stay English in both languages. internal/model/patch's editor vectors
// test checks that the server's validator finds no error in it.
import type { ModelIR } from "../../../lib/contracts";

const tri = (min: number, mode: number, max: number) => ({ type: "triangular" as const, min, mode, max });
const flow = (from: string, to: string, probability = 1) => ({ id: `${from}_${to}`, from, to, routing: { type: "probability" as const, probability } });

export const SAMPLE_MODEL_ID = "mdl_01M4YDQK00E1ECTR0N1C500000";

export const sampleLine: ModelIR = {
  schema_version: "0.1",
  model_id: SAMPLE_MODEL_ID,
  domain: "electronics_line",
  name: "Board assembly and test line (sample)",
  time_unit: "minute",
  entity_types: [
    { id: "board_a", name: "Board A" },
    { id: "board_b", name: "Board B" },
  ],
  calendars: [{ id: "two_shift", name: "Two shifts", period: 1440, shifts: [{ start: 360, end: 840, label: "day" }, { start: 840, end: 1320, label: "evening" }] }],
  resources: [
    {
      id: "smt_line", type: "machine", role: "smt_line", name: "SMT line", capacity: 1, queue_discipline: "fifo", calendar_id: "two_shift",
      breakdowns: { time_to_failure: { distribution: { type: "exponential", mean: 600 } }, time_to_repair: { distribution: tri(10, 20, 90) } },
      setups: { initial: "board_a", transitions: [{ from: "board_a", to: "board_b", distribution: tri(15, 25, 45) }, { from: "board_b", to: "board_a", distribution: tri(15, 25, 45) }] },
    },
    { id: "aoi_machine", type: "machine", role: "aoi", name: "AOI machine", capacity: 1, queue_discipline: "fifo", calendar_id: "two_shift" },
    {
      id: "ict_tester", type: "machine", role: "ict_tester", name: "ICT tester", capacity: 2, queue_discipline: "fifo", calendar_id: "two_shift",
      breakdowns: { time_to_failure: { distribution: { type: "exponential", mean: 900 } }, time_to_repair: { distribution: tri(10, 20, 60) } },
    },
    { id: "burn_in_chamber", type: "machine", role: "burn_in_chamber", name: "Burn-in chamber", capacity: 6, queue_discipline: "fifo" },
    { id: "debug_operator", type: "human", role: "debug_operator", name: "Debug operator", capacity: 2, queue_discipline: "fifo", calendar_id: "two_shift" },
    { id: "pack_operator", type: "human", role: "pack_station", name: "Pack operator", capacity: 2, queue_discipline: "fifo", calendar_id: "two_shift" },
  ],
  nodes: [
    { id: "release_a", type: "source", name: "Board A release", entity_type: "board_a", arrival: { distribution: { type: "exponential", mean: 3 } } },
    { id: "release_b", type: "source", name: "Board B release", entity_type: "board_b", arrival: { distribution: { type: "exponential", mean: 3 } } },
    { id: "q_smt", type: "queue", name: "Before SMT" },
    { id: "smt", type: "process", name: "SMT", service_time: { distribution: tri(0.9, 1, 1.4) }, requires: [{ resource_id: "smt_line", units: 1 }] },
    { id: "q_aoi", type: "queue", name: "Before AOI", capacity: 20 },
    { id: "aoi", type: "process", name: "AOI", service_time: { distribution: tri(0.8, 0.9, 1.1) }, requires: [{ resource_id: "aoi_machine", units: 1 }] },
    { id: "aoi_result", type: "decision", name: "AOI result" },
    { id: "aoi_defect", type: "failure", name: "AOI defect", reason: "AOI found a defect (synthetic)" },
    { id: "q_touchup", type: "queue", name: "Before touch-up" },
    { id: "touchup", type: "process", name: "Touch-up", service_time: { distribution: tri(3, 5, 10) }, requires: [{ resource_id: "debug_operator", units: 1 }] },
    { id: "touchup_rework", type: "rework", name: "Touched up" },
    { id: "q_ict", type: "queue", name: "Before ICT", capacity: 10 },
    { id: "ict", type: "process", name: "ICT", service_time: { distribution: tri(1.8, 2, 2.6) }, requires: [{ resource_id: "ict_tester", units: 1 }] },
    { id: "ict_result", type: "decision", name: "ICT result" },
    { id: "ict_fail", type: "failure", name: "ICT fail", reason: "ICT failed (synthetic)" },
    { id: "q_debug", type: "queue", name: "Before debug" },
    { id: "ict_debug", type: "process", name: "ICT debug", service_time: { distribution: tri(5, 10, 30) }, requires: [{ resource_id: "debug_operator", units: 1 }] },
    { id: "debug_result", type: "decision", name: "Debug result" },
    { id: "debug_rework", type: "rework", name: "Repaired" },
    { id: "q_burn_in", type: "queue", name: "Before burn-in", capacity: 120 },
    {
      id: "burn_in", type: "process", name: "Burn-in", service_time: { distribution: { type: "constant", value: 240 } },
      requires: [{ resource_id: "burn_in_chamber", units: 1 }], batch: { min_size: 20, max_size: 40, max_wait: 120 },
    },
    { id: "q_pack", type: "queue", name: "Before packing", capacity: 400 },
    { id: "pack", type: "process", name: "Pack and ship", service_time: { distribution: tri(0.8, 1, 1.5) }, requires: [{ resource_id: "pack_operator", units: 1 }] },
    { id: "shipped", type: "sink", name: "Shipped", outcome: "completed" },
    { id: "scrap", type: "sink", name: "Scrap", outcome: "abandoned" },
  ],
  flows: [
    flow("release_a", "q_smt"),
    flow("release_b", "q_smt"),
    flow("q_smt", "smt"),
    flow("smt", "q_aoi"),
    flow("q_aoi", "aoi"),
    flow("aoi", "aoi_result"),
    flow("aoi_result", "q_ict", 0.97),
    flow("aoi_result", "aoi_defect", 0.03),
    flow("aoi_defect", "q_touchup"),
    flow("q_touchup", "touchup"),
    flow("touchup", "touchup_rework"),
    flow("touchup_rework", "q_ict"),
    flow("q_ict", "ict"),
    flow("ict", "ict_result"),
    flow("ict_result", "q_burn_in", 0.95),
    flow("ict_result", "ict_fail", 0.05),
    flow("ict_fail", "q_debug"),
    flow("q_debug", "ict_debug"),
    flow("ict_debug", "debug_result"),
    flow("debug_result", "debug_rework", 0.97),
    flow("debug_result", "scrap", 0.03),
    flow("debug_rework", "q_ict"),
    flow("q_burn_in", "burn_in"),
    flow("burn_in", "q_pack"),
    flow("q_pack", "pack"),
    flow("pack", "shipped"),
  ],
  constraints: [
    { type: "outgoing_probability_sum", node_id: "aoi_result", equals: 1 },
    { type: "outgoing_probability_sum", node_id: "ict_result", equals: 1 },
    { type: "outgoing_probability_sum", node_id: "debug_result", equals: 1 },
  ],
  metrics: [
    { id: "lead_time", type: "entity_cycle_time" },
    { id: "throughput", type: "sink_throughput" },
    { id: "ict_utilization", type: "resource_utilization", resource_id: "ict_tester" },
    { id: "ict_wait", type: "queue_wait", node_id: "q_ict" },
  ],
  views: { "2d": { positions: {} }, "3d": { layout_mode: "derived_from_2d" } },
  metadata: { synthetic: true, note: "Sample data for the editor preview; not measured." },
};
