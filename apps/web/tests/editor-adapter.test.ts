// T-015 (TEST-025 for 2D, REQ-004): the IR ↔ React Flow adapter. React Flow
// gets a projection of the IR: one node per IR node and one edge per IR
// flow, with the IR's own IDs; nothing else is kept (ADR-007, no second
// topology). Every edit is JSON Patch ops on the IR, written the way the
// server's patch policy wants them (D-020: an element is tested by its
// identity right before it is changed or removed), and applying them gives
// the IR the edit means. internal/model/patch's editor vectors test applies
// the same ops with the server's own code (testdata/editor/patch-vectors.json).
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { sampleLine } from "../app/ui-gallery/editor/sample-line";
import type { ModelIR } from "../lib/contracts";
import {
  addStep,
  connectSteps,
  deleteStep,
  disconnect,
  moveStep,
  renameStep,
  setBatch,
  setProbability,
  setQueueCapacity,
  setTiming,
  type EditResult,
} from "../features/model-editor/edits";
import { toFlow } from "../features/model-editor/flow";
import { claim, locate, locateIssues, stepOf } from "../features/model-editor/issues";
import { canvasLayout, LAYER_SPACING, layeredLayout, ROW_SPACING } from "../features/model-editor/layout";
import { applyPatch, type Op, parsePointer, reapplyPatch } from "../features/model-editor/patch";

const fixture = (name: string): ModelIR => JSON.parse(readFileSync(join(process.cwd(), "../../testdata/contracts", name), "utf8")) as ModelIR;
const minimal = () => fixture("valid-minimal-ir.json");
const lineFlow = () => fixture("valid-line-flow-ir.json");
const template = (): ModelIR => JSON.parse(readFileSync(join(process.cwd(), "../../internal/domainpack/electronicsline/templates/electronics_assembly_test_line.json"), "utf8")) as ModelIR;
const bareLine = (): ModelIR => JSON.parse(readFileSync(join(process.cwd(), "../../testdata/photos/bare-line.json"), "utf8")) as ModelIR;
const sample = (): ModelIR => JSON.parse(JSON.stringify(sampleLine)) as ModelIR;
/** The sample with every node placed, as after its first edit. */
const placed = (): ModelIR => {
  const ir = sample();
  ir.views["2d"].positions = layeredLayout(ir);
  return ir;
};

function ops(r: EditResult): Op[] {
  if (!r.ok) throw new Error(`refused: ${r.refusal}`);
  return r.ops;
}
function after(ir: ModelIR, r: EditResult): ModelIR {
  const a = applyPatch(ir, ops(r));
  if (!a.ok) throw new Error(`op ${a.index}: ${a.reason}`);
  return a.doc;
}

const catalogs: Record<string, string> = { nodes: "id", flows: "id", resources: "id", entity_types: "id", metrics: "id", constraints: "node_id", calendars: "id" };

/**
 * The server's static rule (internal/model/patch check, D-020), restated: a
 * change to an existing element of a catalog needs a test of its identity
 * since the last op that shifted that catalog's indices; the whole catalog
 * is never replaced while it has elements.
 */
function testBeforeModify(patch: Op[]): string[] {
  const problems: string[] = [];
  const tested = new Map<string, Set<string>>();
  patch.forEach((op, i) => {
    const t = parsePointer(op.path)!;
    const catalog = t[0]!;
    if (!(catalog in catalogs)) return;
    if (op.op === "test") {
      if (t.length === 3 && t[2] === catalogs[catalog]) tested.set(catalog, (tested.get(catalog) ?? new Set()).add(t[1]!));
      return;
    }
    if (t.length === 1) problems.push(`${i}: whole ${catalog}`);
    else if (op.op === "add" && t.length === 2) {
      if (t[1] !== "-") tested.delete(catalog);
    } else if (!tested.get(catalog)?.has(t[1]!)) problems.push(`${i}: ${op.op} ${op.path} without a test`);
    else if (op.op === "remove" && t.length === 2) tested.delete(catalog);
  });
  return problems;
}

describe("layered default layout (the same rule as internal/domainpack.LayeredLayout)", () => {
  it("places nodes by breadth-first layer from the sources", () => {
    expect(layeredLayout(minimal())).toEqual({ start: { x: 0, y: 0 }, work: { x: 240, y: 0 }, end: { x: 480, y: 0 } });
    expect([LAYER_SPACING, ROW_SPACING]).toEqual([240, 120]);
  });

  it("keeps existing positions, sends unreachable nodes past the deepest layer and terminates on loops", () => {
    // TestLayeredLayout's second case in internal/domainpack, value for value.
    const ir = minimal();
    ir.views["2d"].positions = { work: { x: 7, y: 9 } };
    ir.nodes.push({ id: "orphan", type: "sink" }, { id: "check", type: "decision" });
    ir.flows.push({ id: "loop", from: "end", to: "start", routing: { type: "probability", probability: 1 } }, { id: "wire", from: "work", to: "check", routing: { type: "probability", probability: 1 } });
    const got = layeredLayout(ir);
    expect(got.work).toEqual({ x: 7, y: 9 });
    expect(got.orphan).toEqual({ x: 3 * LAYER_SPACING, y: 0 });
    expect(got.check).toEqual({ x: 2 * LAYER_SPACING, y: ROW_SPACING });
    expect(Object.keys(got).sort()).toEqual(["check", "end", "orphan", "start", "work"]);
    expect(layeredLayout(ir)).toEqual(got);
  });

  it("rounds a kept position to its cell half away from zero, as Go's math.Round does", () => {
    // x -120 is layer -0.5: Go takes it to -1, so layer 0 row 0 stays free for start.
    const ir = minimal();
    ir.views["2d"].positions = { end: { x: -120, y: 0 } };
    expect(layeredLayout(ir).start).toEqual({ x: 0, y: 0 });
    ir.views["2d"].positions = { end: { x: 120, y: 0 } };
    expect(layeredLayout(ir).work).toEqual({ x: 240, y: 120 });
  });
});

/** minimal() laid out, then an AI edit (which cannot write the view, D-022) puts a step "check" between work and end. */
const aiInserted = (): ModelIR => {
  const ir = minimal();
  ir.views["2d"].positions = layeredLayout(ir);
  ir.nodes.splice(2, 0, { id: "check", type: "delay", delay_time: { distribution: { type: "constant", value: 1 } } });
  ir.flows = [ir.flows[0]!, { id: "work_check", from: "work", to: "check", routing: { type: "probability", probability: 1 } }, { id: "check_end", from: "check", to: "end", routing: { type: "probability", probability: 1 } }];
  return ir;
};

describe("the canvas's layout", () => {
  it("lays the whole line out again when an AI edit added steps to a layout no one placed by hand", () => {
    // The layered layout alone keeps end where it was, in front of the step the AI put before it.
    expect(layeredLayout(aiInserted()).end).toEqual({ x: 480, y: 0 });
    expect(canvasLayout(aiInserted())).toEqual({ start: { x: 0, y: 0 }, work: { x: 240, y: 0 }, check: { x: 480, y: 0 }, end: { x: 720, y: 0 } });
    const words = { step: (id: string) => id, edge: (from: string, to: string) => `${from}>${to}`, roleStep: "step", roleEdge: "connection", probability: (p: number) => String(p) };
    const { nodes } = toFlow(aiInserted(), { selected: null, severity: () => null, editable: true, describe: words });
    expect(nodes.map((n) => [n.id, n.position.x])).toEqual([["start", 0], ["work", 240], ["check", 480], ["end", 720]]);
  });

  it("keeps positions someone placed: every step placed, a step keyed onto a grid point, a template's own layout", () => {
    const all = aiInserted();
    all.views["2d"].positions!.check = { x: 240, y: 240 }; // on the grid, not the layered default
    expect(canvasLayout(all)).toEqual(all.views["2d"].positions);
    // Six Shift+arrow presses (20 px each) move work one row down, exactly onto the grid (review r1 M2).
    const keyed = aiInserted();
    keyed.views["2d"].positions!.work = { x: 240, y: 120 };
    expect(canvasLayout(keyed)).toEqual(layeredLayout(keyed));
    expect(canvasLayout(keyed).end).toEqual({ x: 480, y: 0 });
    // The shipped template's positions are on the grid but hand-placed (its rework loops): kept,
    // with or without a step an AI put between AOI and its result (review r1 H1).
    const tpl = template();
    const authored = { ...tpl.views["2d"].positions! };
    expect(canvasLayout(tpl)).toEqual(authored);
    const at = tpl.flows.findIndex((f) => f.from === "aoi" && f.to === "aoi_result");
    tpl.flows.splice(at, 1, { ...tpl.flows[at]!, id: "aoi_xray", to: "xray" }, { ...tpl.flows[at]!, id: "xray_aoi_result", from: "xray" });
    tpl.nodes.push({ id: "xray", type: "delay", delay_time: { distribution: { type: "constant", value: 1 } } });
    const got = canvasLayout(tpl);
    for (const [id, p] of Object.entries(authored)) expect(got[id], id).toEqual(p);
  });

  it("puts Shipped last again after the photo proposal adds the line's stations to the bare line (T-057 live)", () => {
    const ir = bareLine();
    ir.flows = ir.flows.filter((f) => f.from !== "smt");
    const steps = ["q_aoi", "aoi", "q_ict", "ict"];
    ir.nodes.push(...steps.map((id) => ({ id, type: "delay" as const, delay_time: { distribution: { type: "constant" as const, value: 1 } } })));
    ["smt", ...steps, "shipped"].slice(1).forEach((to, k, list) => {
      const from = k === 0 ? "smt" : list[k - 1]!;
      ir.flows.push({ id: `${from}_${to}`, from, to, routing: { type: "probability", probability: 1 } });
    });
    const got = canvasLayout(ir);
    expect(Math.max(...Object.values(got).map((p) => p.x))).toBe(got.shipped!.x);
    expect(got.shipped).toEqual({ x: 7 * LAYER_SPACING, y: 0 });
    // A step put in the middle of the line, before SMT: the line before it is release, q_smt, smt, shipped.
    const mid = bareLine();
    mid.flows = mid.flows.filter((f) => f.to !== "smt");
    mid.nodes.push({ id: "bake", type: "delay", delay_time: { distribution: { type: "constant", value: 1 } } });
    mid.flows.push({ id: "q_smt_bake", from: "q_smt", to: "bake", routing: { type: "probability", probability: 1 } },
      { id: "bake_smt", from: "bake", to: "smt", routing: { type: "probability", probability: 1 } });
    expect(canvasLayout(mid)).toEqual({ release: { x: 0, y: 0 }, q_smt: { x: 240, y: 0 }, bake: { x: 480, y: 0 }, smt: { x: 720, y: 0 }, shipped: { x: 960, y: 0 } });
  });

  it("is what the first edit writes down, each kept position it changes after a test of its old value", () => {
    const ir = aiInserted();
    const first = ops(moveStep(ir, "start", { x: -10, y: 0 }));
    expect(first).toEqual([
      { op: "add", path: "/views/2d/positions/check", value: { x: 480, y: 0 } },
      { op: "test", path: "/views/2d/positions/end", value: { x: 480, y: 0 } },
      { op: "replace", path: "/views/2d/positions/end", value: { x: 720, y: 0 } },
      { op: "test", path: "/views/2d/positions/start", value: { x: 0, y: 0 } },
      { op: "replace", path: "/views/2d/positions/start", value: { x: -10, y: 0 } },
    ]);
    expect(after(ir, moveStep(ir, "start", { x: -10, y: 0 })).views["2d"].positions).toEqual({ ...canvasLayout(ir), start: { x: -10, y: 0 } });
  });
});

describe("IR to React Flow", () => {
  const describe_ = { step: (id: string) => id, edge: (from: string, to: string) => `${from}>${to}`, roleStep: "step", roleEdge: "connection", probability: (p: number) => String(p) };

  it("has one node per IR node and one edge per flow, with the IR's IDs and endpoints only", () => {
    for (const ir of [minimal(), lineFlow(), sample()]) {
      const { nodes, edges } = toFlow(ir, { selected: null, severity: () => null, editable: true, describe: describe_ });
      expect(nodes.map((n) => n.id).sort()).toEqual(ir.nodes.map((n) => n.id).sort());
      expect(edges.map((e) => [e.id, e.source, e.target])).toEqual(ir.flows.map((f) => [f.id, f.from, f.to]));
      // The projection carries no copy of the model: just what React Flow draws.
      for (const n of nodes) expect(Object.keys(n.data).sort()).toEqual(["kind", "label", "severity"]);
    }
  });

  it("puts nodes where the IR says, else where the layered layout does, in reading order", () => {
    const ir = minimal();
    ir.views["2d"].positions = { end: { x: -50, y: 10 } };
    const { nodes } = toFlow(ir, { selected: null, severity: () => null, editable: true, describe: describe_ });
    // end's kept position rounds to cell (0, 0), so start takes the next row there.
    expect(nodes.map((n) => [n.id, n.position])).toEqual([
      ["end", { x: -50, y: 10 }],
      ["start", { x: 0, y: 120 }],
      ["work", { x: 240, y: 0 }],
    ]);
  });

  it("marks the selected node, the severity of its issues and whether it can be edited", () => {
    const { nodes, edges } = toFlow(sample(), { selected: "smt", severity: (id) => (id === "q_ict" ? "error" : null), editable: false, describe: describe_ });
    const smt = nodes.find((n) => n.id === "smt")!;
    expect([smt.selected, smt.draggable, smt.connectable, smt.deletable]).toEqual([true, false, false, false]);
    expect(nodes.find((n) => n.id === "q_ict")!.data.severity).toBe("error");
    expect(nodes.filter((n) => n.selected).map((n) => n.id)).toEqual(["smt"]);
    // Connections are changed from the steps' properties, never as canvas objects.
    expect(edges.every((e) => e.selectable === false && e.focusable === false && e.deletable === false)).toBe(true);
    expect(edges.find((e) => e.id === "aoi_result_aoi_defect")!.label).toBe("0.03");
    expect(edges.find((e) => e.id === "smt_q_aoi")!.label).toBeUndefined();
    // React Flow's own words ("node", "edge", "Edge from a to b") are replaced by the caller's.
    expect(edges.find((e) => e.id === "smt_q_aoi")).toMatchObject({ ariaLabel: "SMT>Before AOI", domAttributes: { "aria-roledescription": "connection" } });
    expect(smt).toMatchObject({ ariaLabel: "smt", domAttributes: { "aria-roledescription": "step" } });
  });
});

describe("edits are JSON Patch ops on the IR", () => {
  it("move: a test of the old position and one replace for a placed node; an unplaced layout is written down first so nothing else jumps", () => {
    const p = placed();
    const move = moveStep(p, "smt", { x: 10.4, y: -3.6 });
    expect(ops(move)).toEqual([{ op: "test", path: "/views/2d/positions/smt", value: p.views["2d"].positions!.smt }, { op: "replace", path: "/views/2d/positions/smt", value: { x: 10, y: -4 } }]);
    const m = minimal();
    const first = ops(moveStep(m, "work", { x: 5, y: 5 }));
    expect(first).toEqual([
      { op: "add", path: "/views/2d/positions", value: { start: { x: 0, y: 0 }, end: { x: 480, y: 0 } } },
      { op: "add", path: "/views/2d/positions/work", value: { x: 5, y: 5 } },
    ]);
    const partly = minimal();
    partly.views["2d"].positions = { start: { x: 0, y: 0 } };
    expect(ops(moveStep(partly, "end", { x: 1, y: 2 }))).toEqual([
      { op: "add", path: "/views/2d/positions/work", value: { x: 240, y: 0 } },
      { op: "add", path: "/views/2d/positions/end", value: { x: 1, y: 2 } },
    ]);
    expect(moveStep(p, "nope", { x: 0, y: 0 })).toEqual({ ok: false, refusal: "unknown_step" });
  });

  it("rename: test the step and its old name, then replace or add; an empty name removes it; the same name is no change", () => {
    const ir = sample();
    expect(ops(renameStep(ir, "smt", "  SMT line 1 "))).toEqual([{ op: "test", path: "/nodes/3/id", value: "smt" }, { op: "test", path: "/nodes/3/name", value: "SMT" }, { op: "replace", path: "/nodes/3/name", value: "SMT line 1" }]);
    expect(ops(renameStep(ir, "smt", "   "))).toEqual([{ op: "test", path: "/nodes/3/id", value: "smt" }, { op: "test", path: "/nodes/3/name", value: "SMT" }, { op: "remove", path: "/nodes/3/name" }]);
    expect(ops(renameStep(ir, "smt", "SMT"))).toEqual([]);
    expect(ops(renameStep(minimal(), "work", "Work"))).toEqual([{ op: "test", path: "/nodes/1/id", value: "work" }, { op: "add", path: "/nodes/1/name", value: "Work" }]);
    expect(ops(renameStep(minimal(), "work", ""))).toEqual([]);
  });

  it("add: a new node of each type with a fresh ID, placed in the first free cell past the deepest layer", () => {
    const ir = placed();
    const r = addStep(ir, "process");
    expect(r.ok && r.id).toBe("process_1");
    const next = after(ir, r);
    expect(next.nodes.at(-1)).toEqual({ id: "process_1", type: "process", service_time: { distribution: { type: "constant", value: 1 } }, requires: [] });
    expect(next.views["2d"].positions!.process_1).toEqual(layeredLayout({ ...next, views: { ...next.views, "2d": { positions: ir.views["2d"].positions } } }).process_1);
    expect(testBeforeModify(ops(r))).toEqual([]);
    const shapes = Object.fromEntries((["source", "queue", "decision", "delay", "sink", "failure", "rework"] as const).map((type) => {
      const a = addStep(ir, type);
      return [type, after(ir, a).nodes.at(-1)];
    }));
    expect(shapes).toEqual({
      source: { id: "source_1", type: "source", entity_type: "board_a", arrival: { distribution: { type: "exponential", mean: 1 } } },
      queue: { id: "queue_1", type: "queue" },
      decision: { id: "decision_1", type: "decision" },
      delay: { id: "delay_1", type: "delay", delay_time: { distribution: { type: "constant", value: 1 } } },
      sink: { id: "sink_1", type: "sink", outcome: "completed" },
      failure: { id: "failure_1", type: "failure" },
      rework: { id: "rework_1", type: "rework" },
    });
    // An ID is never one a node, resource or flow already has.
    const taken = placed();
    taken.resources.push({ id: "queue_1", type: "generic", name: "x", capacity: 1, queue_discipline: "fifo" });
    taken.flows.push({ id: "queue_2", from: "smt", to: "q_aoi", routing: { type: "probability", probability: 1 } });
    expect(addStep(taken, "queue")).toMatchObject({ ok: true, id: "queue_3" });
    const noTypes = placed();
    noTypes.entity_types = [];
    expect(addStep(noTypes, "source")).toEqual({ ok: false, refusal: "no_entity_type" });
  });

  it("delete: the node, its flows, the constraints and metrics that name them and its position, each removal tested", () => {
    const ir = placed();
    const r = deleteStep(ir, "q_ict");
    const patch = ops(r);
    expect(testBeforeModify(patch)).toEqual([]);
    const next = after(ir, r);
    expect(next.nodes.some((n) => n.id === "q_ict")).toBe(false);
    expect(next.flows.filter((f) => f.from === "q_ict" || f.to === "q_ict")).toEqual([]);
    expect(next.flows.length).toBe(ir.flows.length - 4);
    expect(next.metrics.map((m) => m.id)).toEqual(["lead_time", "throughput", "ict_utilization"]);
    expect(next.views["2d"].positions!.q_ict).toBeUndefined();
    expect(Object.keys(next.views["2d"].positions!).length).toBe(ir.nodes.length - 1);
    // Every removal sits right after the test of the element it removes, and that after a test of its whole old value.
    let doc: unknown = ir;
    patch.forEach((op, i) => {
      const t = parsePointer(op.path)!;
      if (op.op === "remove" && t.length === 2) {
        expect(patch[i - 1]).toMatchObject({ op: "test", path: `/${t[0]}/${t[1]}/${catalogs[t[0]!]}` });
        expect(patch[i - 2]).toEqual({ op: "test", path: op.path, value: (doc as Record<string, unknown[]>)[t[0]!]![Number(t[1])] });
      }
      const a = applyPatch(doc, [op]);
      if (!a.ok) throw new Error(`op ${i}: ${a.reason}`);
      doc = a.doc;
    });
    expect(patch.slice(-2)).toEqual([{ op: "test", path: "/views/2d/positions/q_ict", value: ir.views["2d"].positions!.q_ict }, { op: "remove", path: "/views/2d/positions/q_ict" }]);
    const decision = deleteStep(ir, "ict_result");
    expect(after(ir, decision).constraints.map((c) => c.node_id)).toEqual(["aoi_result", "debug_result"]);
    const counted = lineFlow();
    counted.views["2d"].positions = layeredLayout(counted);
    const noTest = after(counted, deleteStep(counted, "test"));
    expect(noTest.metrics.map((m) => m.id)).toEqual(["line_wip", "scrapped", "daily_output"]);
    expect(after(counted, deleteStep(counted, "scrap")).metrics.map((m) => m.id)).toEqual(["line_wip", "first_pass", "first_fail", "daily_output"]);
    expect(deleteStep(ir, "nope")).toEqual({ ok: false, refusal: "unknown_step" });
  });

  it("connect: a new flow with a fresh ID; probability 1, or 0 for a decision's second way out", () => {
    const ir = placed();
    const r = connectSteps(ir, "q_pack", "scrap");
    expect(r).toMatchObject({ ok: true, id: "q_pack_scrap" });
    expect(ops(r)).toEqual([{ op: "add", path: "/flows/-", value: { id: "q_pack_scrap", from: "q_pack", to: "scrap", routing: { type: "probability", probability: 1 } } }]);
    expect(after(ir, connectSteps(ir, "aoi_result", "scrap")).flows.at(-1)!.routing.probability).toBe(0);
    const fresh = after(ir, addStep(ir, "decision"));
    expect(after(fresh, connectSteps(fresh, "decision_1", "scrap")).flows.at(-1)!.routing.probability).toBe(1);
    // A flow ID is never a node's (one selection store holds both kinds).
    const clash = placed();
    clash.nodes.push({ id: "q_pack_scrap", type: "queue" });
    expect(connectSteps(clash, "q_pack", "scrap")).toMatchObject({ ok: true, id: "q_pack_scrap_2" });
    expect(connectSteps(ir, "smt", "smt")).toEqual({ ok: false, refusal: "self_connection" });
    expect(connectSteps(ir, "shipped", "q_pack")).toEqual({ ok: false, refusal: "from_sink" });
    expect(connectSteps(ir, "smt", "release_a")).toEqual({ ok: false, refusal: "to_source" });
    expect(connectSteps(ir, "smt", "q_aoi")).toEqual({ ok: false, refusal: "duplicate_connection" });
    expect(connectSteps(ir, "smt", "nope")).toEqual({ ok: false, refusal: "unknown_step" });
  });

  it("disconnect: the flow and the flow counts that name it, each tested", () => {
    const counted = lineFlow();
    counted.views["2d"].positions = layeredLayout(counted);
    const r = disconnect(counted, "test_pass");
    expect(testBeforeModify(ops(r))).toEqual([]);
    const next = after(counted, r);
    expect(next.flows.some((f) => f.id === "test_pass")).toBe(false);
    expect(next.metrics.map((m) => m.id)).toEqual(["line_wip", "first_fail", "scrapped", "daily_output"]);
    expect(disconnect(counted, "nope")).toEqual({ ok: false, refusal: "unknown_connection" });
  });

  it("properties: probability, queue capacity, the step's own time and batch sizes, each after a test of the element and of the value it replaces or removes", () => {
    const ir = placed();
    const at = (id: string) => ir.nodes.find((n) => n.id === id) as Record<string, unknown> & ModelIR["nodes"][number];
    const old = {
      probability: ir.flows[6]!.routing.probability,
      smtMax: (at("smt") as unknown as { service_time: { distribution: { max: number } } }).service_time.distribution.max,
      releaseMean: (at("release_a") as unknown as { arrival: { distribution: { mean: number } } }).arrival.distribution.mean,
      batch: (at("burn_in") as unknown as { batch: { max_size: number; max_wait: number } }).batch,
    };
    expect(ops(setProbability(ir, "aoi_result_q_ict", 0.9))).toEqual([{ op: "test", path: "/flows/6/id", value: "aoi_result_q_ict" }, { op: "test", path: "/flows/6/routing/probability", value: old.probability }, { op: "replace", path: "/flows/6/routing/probability", value: 0.9 }]);
    expect(ops(setQueueCapacity(ir, "q_aoi", 25))).toEqual([{ op: "test", path: "/nodes/4/id", value: "q_aoi" }, { op: "test", path: "/nodes/4/capacity", value: 20 }, { op: "replace", path: "/nodes/4/capacity", value: 25 }]);
    expect(ops(setQueueCapacity(ir, "q_aoi", null))).toEqual([{ op: "test", path: "/nodes/4/id", value: "q_aoi" }, { op: "test", path: "/nodes/4/capacity", value: 20 }, { op: "remove", path: "/nodes/4/capacity" }]);
    // A member that is not there has no value to test; reapplying checks that it is still absent (reapplyPatch).
    expect(ops(setQueueCapacity(ir, "q_smt", 5))).toEqual([{ op: "test", path: "/nodes/2/id", value: "q_smt" }, { op: "add", path: "/nodes/2/capacity", value: 5 }]);
    expect(ops(setQueueCapacity(ir, "q_smt", null))).toEqual([]);
    expect(ops(setTiming(ir, "smt", "max", 1.6))).toEqual([{ op: "test", path: "/nodes/3/id", value: "smt" }, { op: "test", path: "/nodes/3/service_time/distribution/max", value: old.smtMax }, { op: "replace", path: "/nodes/3/service_time/distribution/max", value: 1.6 }]);
    expect(ops(setTiming(ir, "release_a", "mean", 2.5))).toEqual([{ op: "test", path: "/nodes/0/id", value: "release_a" }, { op: "test", path: "/nodes/0/arrival/distribution/mean", value: old.releaseMean }, { op: "replace", path: "/nodes/0/arrival/distribution/mean", value: 2.5 }]);
    expect(setTiming(ir, "smt", "mean", 1)).toEqual({ ok: false, refusal: "unknown_field" });
    expect(setTiming(ir, "q_smt", "value", 1)).toEqual({ ok: false, refusal: "unknown_field" });
    expect(ops(setBatch(ir, "burn_in", "max_size", 48))).toEqual([{ op: "test", path: "/nodes/20/id", value: "burn_in" }, { op: "test", path: "/nodes/20/batch/max_size", value: old.batch.max_size }, { op: "replace", path: "/nodes/20/batch/max_size", value: 48 }]);
    expect(ops(setBatch(ir, "burn_in", "max_wait", null))).toEqual([{ op: "test", path: "/nodes/20/id", value: "burn_in" }, { op: "test", path: "/nodes/20/batch/max_wait", value: old.batch.max_wait }, { op: "remove", path: "/nodes/20/batch/max_wait" }]);
    expect(setBatch(ir, "smt", "min_size", 2)).toEqual({ ok: false, refusal: "unknown_field" });
  });

  it("every replace and remove follows a test of the value it replaces or removes (T-015 review r1 M1)", () => {
    const ir = placed();
    const counted = lineFlow();
    counted.views["2d"].positions = layeredLayout(counted);
    const edits: [ModelIR, EditResult][] = [
      [ir, moveStep(ir, "aoi", { x: 1, y: 2 })],
      [ir, renameStep(ir, "smt", "X")],
      [ir, renameStep(ir, "smt", "")],
      [ir, deleteStep(ir, "q_ict")],
      [ir, deleteStep(ir, "ict_result")],
      [counted, deleteStep(counted, "test")],
      [counted, disconnect(counted, "test_pass")],
      [ir, setProbability(ir, "aoi_result_q_ict", 0.5)],
      [ir, setQueueCapacity(ir, "q_aoi", 3)],
      [ir, setQueueCapacity(ir, "q_aoi", null)],
      [ir, setTiming(ir, "smt", "max", 2)],
      [ir, setBatch(ir, "burn_in", "min_size", 2)],
      [ir, setBatch(ir, "burn_in", "max_wait", null)],
    ];
    for (const [base, r] of edits) {
      const patch = ops(r);
      let doc: unknown = base;
      patch.forEach((op, i) => {
        if (op.op === "replace" || op.op === "remove") {
          // Among the tests right before it is one of this path; it passed on the document as it was then.
          let j = i - 1;
          while (j >= 0 && patch[j]!.op === "test" && patch[j]!.path !== op.path) j--;
          expect(patch[j], `${op.op} ${op.path}`).toMatchObject({ op: "test", path: op.path });
        }
        const a = applyPatch(doc, [op]);
        if (!a.ok) throw new Error(`op ${i}: ${a.reason}`);
        doc = a.doc;
      });
    }
  });

  // What reapplying after a 409 does with each edit when someone else's
  // newer revision changed the same thing (it fails: only discarding is
  // left, REQ-006) or something else (it applies).
  it("written on an older draft, fail on a newer one where someone changed what they change, and apply where they did not", () => {
    const ir = placed();
    const unplaced = sample();
    const cases: { name: string; base: ModelIR; mine: (x: ModelIR) => EditResult; same: (x: ModelIR) => EditResult; other: (x: ModelIR) => EditResult }[] = [
      { name: "rename", base: ir, mine: (x) => renameStep(x, "smt", "Mine"), same: (x) => renameStep(x, "smt", "Theirs"), other: (x) => renameStep(x, "aoi", "Theirs") },
      { name: "remove a name", base: ir, mine: (x) => renameStep(x, "smt", ""), same: (x) => renameStep(x, "smt", "Theirs"), other: (x) => setTiming(x, "smt", "max", 2) },
      { name: "name an unnamed step", base: minimal(), mine: (x) => renameStep(x, "work", "Mine"), same: (x) => renameStep(x, "work", "Theirs"), other: (x) => renameStep(x, "start", "Theirs") },
      { name: "capacity", base: ir, mine: (x) => setQueueCapacity(x, "q_aoi", 25), same: (x) => setQueueCapacity(x, "q_aoi", 12), other: (x) => renameStep(x, "q_aoi", "Theirs") },
      { name: "remove a capacity", base: ir, mine: (x) => setQueueCapacity(x, "q_aoi", null), same: (x) => setQueueCapacity(x, "q_aoi", 12), other: (x) => setQueueCapacity(x, "q_ict", 3) },
      { name: "set a capacity", base: ir, mine: (x) => setQueueCapacity(x, "q_smt", 50), same: (x) => setQueueCapacity(x, "q_smt", 40), other: (x) => setQueueCapacity(x, "q_aoi", 12) },
      { name: "time", base: ir, mine: (x) => setTiming(x, "smt", "max", 1.6), same: (x) => setTiming(x, "smt", "max", 1.5), other: (x) => setTiming(x, "smt", "min", 0.8) },
      { name: "batch", base: ir, mine: (x) => setBatch(x, "burn_in", "max_size", 48), same: (x) => setBatch(x, "burn_in", "max_size", 30), other: (x) => setBatch(x, "burn_in", "min_size", 10) },
      { name: "remove a batch limit", base: ir, mine: (x) => setBatch(x, "burn_in", "max_wait", null), same: (x) => setBatch(x, "burn_in", "max_wait", 60), other: (x) => setBatch(x, "burn_in", "min_size", 10) },
      { name: "probability", base: ir, mine: (x) => setProbability(x, "aoi_result_q_ict", 0.9), same: (x) => setProbability(x, "aoi_result_q_ict", 0.8), other: (x) => setProbability(x, "aoi_result_aoi_defect", 0.1) },
      { name: "move", base: ir, mine: (x) => moveStep(x, "aoi", { x: 1, y: 2 }), same: (x) => moveStep(x, "aoi", { x: 5, y: 5 }), other: (x) => moveStep(x, "smt", { x: 5, y: 5 }) },
      { name: "the first move writes the layout down", base: unplaced, mine: (x) => moveStep(x, "aoi", { x: 1, y: 2 }), same: (x) => moveStep(x, "smt", { x: 5, y: 5 }), other: (x) => renameStep(x, "aoi", "Theirs") },
      { name: "delete a step", base: ir, mine: (x) => deleteStep(x, "q_ict"), same: (x) => setQueueCapacity(x, "q_ict", 3), other: (x) => setQueueCapacity(x, "q_aoi", 3) },
      { name: "delete a step whose flow changed", base: ir, mine: (x) => deleteStep(x, "aoi_result"), same: (x) => setProbability(x, "aoi_result_q_ict", 0.5), other: (x) => setQueueCapacity(x, "q_aoi", 3) },
      { name: "delete a moved step", base: ir, mine: (x) => deleteStep(x, "q_ict"), same: (x) => moveStep(x, "q_ict", { x: 5, y: 5 }), other: (x) => moveStep(x, "smt", { x: 5, y: 5 }) },
      { name: "disconnect", base: ir, mine: (x) => disconnect(x, "aoi_result_q_ict"), same: (x) => setProbability(x, "aoi_result_q_ict", 0.5), other: (x) => setProbability(x, "aoi_result_aoi_defect", 0.5) },
      { name: "add a step", base: ir, mine: (x) => addStep(x, "process"), same: (x) => addStep(x, "process"), other: (x) => setQueueCapacity(x, "q_aoi", 3) },
      { name: "connect", base: ir, mine: (x) => connectSteps(x, "q_pack", "scrap"), same: (x) => connectSteps(x, "q_pack", "scrap"), other: (x) => setQueueCapacity(x, "q_aoi", 3) },
    ];
    for (const c of cases) {
      const mine = ops(c.mine(c.base));
      expect(applyPatch(c.base, mine).ok, `${c.name}: on its own draft`).toBe(true);
      expect(reapplyPatch(c.base, mine).ok, `${c.name}: reapplied on its own draft`).toBe(true);
      expect(reapplyPatch(after(c.base, c.same(c.base)), mine).ok, `${c.name}: someone changed the same`).toBe(false);
      expect(reapplyPatch(after(c.base, c.other(c.base)), mine).ok, `${c.name}: someone changed something else`).toBe(true);
    }
  });

  it("round trip: an edit and its opposite give the IR back; the projection follows the IR", () => {
    const ir = placed();
    const added = addStep(ir, "queue");
    const withQueue = after(ir, added);
    expect(after(withQueue, deleteStep(withQueue, "queue_1"))).toEqual(ir);
    const linked = connectSteps(ir, "q_pack", "scrap");
    const withFlow = after(ir, linked);
    expect(after(withFlow, disconnect(withFlow, "q_pack_scrap"))).toEqual(ir);
    const moved = after(ir, moveStep(ir, "aoi", { x: 1, y: 2 }));
    const back = ir.views["2d"].positions!.aoi!;
    expect(after(moved, moveStep(moved, "aoi", back))).toEqual(ir);
    const d = { step: (id: string) => id, edge: () => "", roleStep: "", roleEdge: "", probability: String };
    expect(toFlow(moved, { selected: null, severity: () => null, editable: true, describe: d }).nodes.find((n) => n.id === "aoi")!.position).toEqual({ x: 1, y: 2 });
    expect(toFlow(withFlow, { selected: null, severity: () => null, editable: true, describe: d }).edges.at(-1)).toMatchObject({ id: "q_pack_scrap", source: "q_pack", target: "scrap" });
  });
});

describe("issues by JSON Pointer", () => {
  it("finds the entity a path is under and what is left of the path", () => {
    const ir = sample();
    expect(locate(ir, "/nodes/4/capacity")).toEqual({ target: { kind: "node", id: "q_aoi" }, suffix: "/capacity" });
    expect(locate(ir, "/nodes/20/batch")).toEqual({ target: { kind: "node", id: "burn_in" }, suffix: "/batch" });
    expect(locate(ir, "/flows/6/routing/probability")).toEqual({ target: { kind: "flow", id: "aoi_result_q_ict" }, suffix: "/routing/probability" });
    expect(locate(ir, "/resources/0/setups/transitions/1/distribution")).toEqual({ target: { kind: "resource", id: "smt_line" }, suffix: "/setups/transitions/1/distribution" });
    expect(locate(ir, "/constraints/1")).toEqual({ target: { kind: "node", id: "ict_result" }, suffix: "" });
    expect(locate(ir, "/metrics/3/node_id")).toEqual({ target: { kind: "node", id: "q_ict" }, suffix: "" });
    expect(locate(ir, "/views/2d/positions/smt")).toEqual({ target: { kind: "node", id: "smt" }, suffix: "" });
    for (const p of ["/nodes", "", "/calendars/0/shifts/1", "/nodes/99/name", "/views/2d/positions/ghost", "/nodes/x"]) expect(locate(ir, p).target, p).toBeNull();
  });

  it("goes to the step: the node, a flow's start, the first step that needs a resource", () => {
    const ir = sample();
    expect(stepOf(ir, { kind: "node", id: "smt" })).toBe("smt");
    expect(stepOf(ir, { kind: "flow", id: "aoi_result_q_ict" })).toBe("aoi_result");
    expect(stepOf(ir, { kind: "resource", id: "debug_operator" })).toBe("touchup");
    const idle = sample();
    idle.resources.push({ id: "spare", type: "generic", name: "Spare", capacity: 1, queue_discipline: "fifo" });
    expect(stepOf(idle, { kind: "resource", id: "spare" })).toBeNull();
    expect(stepOf(ir, null)).toBeNull();
  });

  it("locates a draft's issues against the IR they were found in, and gives each field the most specific ones", () => {
    const ir = sample();
    const located = locateIssues(ir, [
      { code: "BATCH_INVALID", path: "/nodes/20/batch/max_size", message: "m", severity: "error" },
      { code: "UNREACHABLE_NODE", path: "/nodes/20", message: "m", severity: "warning" },
      { code: "DISTRIBUTION_INVALID", path: "/nodes/20/service_time/distribution", message: "m", severity: "error" },
      { code: "NO_REACHABLE_SINK", path: "/nodes", message: "m", severity: "error" },
    ]);
    expect(located.map((i) => [i.step, i.suffix])).toEqual([["burn_in", "/batch/max_size"], ["burn_in", ""], ["burn_in", "/service_time/distribution"], [null, "/nodes"]]);
    const { byField, rest } = claim(located.filter((i) => i.step === "burn_in"), ["/name", "/batch", "/batch/max_size", "/service_time/distribution"]);
    expect([...byField.entries()].map(([f, is]) => [f, is.map((i) => i.code)])).toEqual([["/batch/max_size", ["BATCH_INVALID"]], ["/service_time/distribution", ["DISTRIBUTION_INVALID"]]]);
    expect(rest.map((i) => i.code)).toEqual(["UNREACHABLE_NODE"]);
  });
});
