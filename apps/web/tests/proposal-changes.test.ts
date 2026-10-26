// T-031 (hackathon slice): a proposal's JSON Patch in human terms. Each op
// that changes something is read against the model as it is right before
// that op (the base version, then op by op, as the server applies them),
// so an index names the entity it named then; a test is a guard, not a
// change. Names and values come from the model or the patch, never from
// the AI's prose; whatever cannot be read safely is not guessed at: the
// whole list falls back to the exact JSON Patch.
import { describe, expect, it } from "vitest";
import { sampleLine } from "../app/ui-gallery/editor/sample-line";
import { describeChanges } from "../features/ai-proposals/changes";
import type { JsonPatch } from "../lib/contracts";

/** An entity as a change names it; showId: its ID goes beside its name (shared name, or new in the patch). */
const ref = (id: string, name: string, showId = false) => ({ id, name, showId });
const step = (id: string, name: string, type: string, showId = false) => ({ kind: "step", ref: ref(id, name, showId), type });
const connection = (id: string, from: ReturnType<typeof ref>, to: ReturnType<typeof ref>, showId = false) => ({ kind: "connection", id, from, to, showId });
const at = (id: string) => sampleLine.nodes.findIndex((n) => n.id === id);
const res = (id: string) => sampleLine.resources.findIndex((r) => r.id === id);
const flow = (id: string) => sampleLine.flows.findIndex((f) => f.id === id);
const num = (value: number, format: "plain" | "percent" | "time" = "plain") => ({ kind: "number", value, format });

function described(changes: JsonPatch) {
  const r = describeChanges(sampleLine, changes);
  if (!r.ok) throw new Error("not described");
  return r.changes;
}

describe("a proposal's changes in human terms", () => {
  it("reads the burn-in batch change as two values of one step, without its tests", () => {
    const i = at("burn_in");
    expect(described([
      { op: "test", path: `/nodes/${i}/id`, value: "burn_in" },
      { op: "replace", path: `/nodes/${i}/batch/min_size`, value: 48 },
      { op: "replace", path: `/nodes/${i}/batch/max_size`, value: 48 },
    ])).toEqual([
      { kind: "changed", subject: step("burn_in", "Burn-in", "process"), field: { key: "batch", which: "min_size" }, before: num(20), after: num(48) },
      { kind: "changed", subject: step("burn_in", "Burn-in", "process"), field: { key: "batch", which: "max_size" }, before: num(40), after: num(48) },
    ]);
  });

  it("reads a resource's capacity, a probability as a percentage and a waiting limit in the model's time unit", () => {
    const r = res("ict_tester");
    const f = flow("ict_result_q_burn_in");
    const b = at("burn_in");
    expect(described([
      { op: "test", path: `/resources/${r}/id`, value: "ict_tester" },
      { op: "replace", path: `/resources/${r}/capacity`, value: 3 },
      { op: "test", path: `/flows/${f}/id`, value: "ict_result_q_burn_in" },
      { op: "replace", path: `/flows/${f}/routing/probability`, value: 0.9 },
      { op: "test", path: `/nodes/${b}/id`, value: "burn_in" },
      { op: "remove", path: `/nodes/${b}/batch/max_wait` },
    ])).toEqual([
      { kind: "changed", subject: { kind: "resource", ref: ref("ict_tester", "ICT tester") }, field: { key: "capacity" }, before: num(2), after: num(3) },
      { kind: "changed", subject: connection("ict_result_q_burn_in", ref("ict_result", "ICT result"), ref("q_burn_in", "Before burn-in")), field: { key: "probability" }, before: num(0.95, "percent"), after: num(0.9, "percent") },
      { kind: "changed", subject: step("burn_in", "Burn-in", "process"), field: { key: "batch", which: "max_wait" }, before: num(120, "time"), after: { kind: "absent" } },
    ]);
  });

  it("reads one number of a step's time, and a whole distribution, in the time unit (a shape has none)", () => {
    const ict = at("ict");
    const smt = at("smt");
    expect(described([
      { op: "test", path: `/nodes/${ict}/id`, value: "ict" },
      { op: "replace", path: `/nodes/${ict}/service_time/distribution/mode`, value: 2.2 },
      { op: "test", path: `/nodes/${smt}/id`, value: "smt" },
      { op: "replace", path: `/nodes/${smt}/service_time/distribution`, value: { type: "weibull", shape: 2, scale: 1.1 } },
      { op: "replace", path: `/nodes/${smt}/service_time/distribution/shape`, value: 3 },
    ])).toEqual([
      { kind: "changed", subject: step("ict", "ICT", "process"), field: { key: "time", of: "process", param: "mode" }, before: num(2, "time"), after: num(2.2, "time") },
      {
        kind: "changed", subject: step("smt", "SMT", "process"), field: { key: "time", of: "process", param: null },
        before: { kind: "distribution", value: { type: "triangular", min: 0.9, mode: 1, max: 1.4 }, time: true },
        after: { kind: "distribution", value: { type: "weibull", shape: 2, scale: 1.1 }, time: true },
      },
      { kind: "changed", subject: step("smt", "SMT", "process"), field: { key: "time", of: "process", param: "shape" }, before: num(2), after: num(3) },
    ]);
  });

  it("reads a queue's capacity removed as no limit, and a name as text", () => {
    const q = at("q_aoi");
    expect(described([
      { op: "test", path: `/nodes/${q}/id`, value: "q_aoi" },
      { op: "remove", path: `/nodes/${q}/capacity` },
      { op: "replace", path: `/nodes/${q}/name`, value: "AOI buffer" },
      { op: "replace", path: "/name", value: "Line 2" },
    ])).toEqual([
      { kind: "changed", subject: step("q_aoi", "Before AOI", "queue"), field: { key: "capacity" }, before: num(20), after: { kind: "absent" } },
      { kind: "changed", subject: step("q_aoi", "Before AOI", "queue"), field: { key: "name" }, before: { kind: "text", value: "Before AOI" }, after: { kind: "text", value: "AOI buffer" } },
      { kind: "changed", subject: { kind: "model" }, field: { key: "name" }, before: { kind: "text", value: sampleLine.name }, after: { kind: "text", value: "Line 2" } },
    ]);
  });

  it("reads new steps with their canvas positions (a T-056 proposal), what each holds, and their connections by name, each new one with its ID", () => {
    const changes: JsonPatch = [
      { op: "add", path: "/nodes/-", value: { id: "node_01xray", type: "process", name: "X-ray", service_time: { distribution: { type: "constant", value: 2 } }, requires: [{ resource_id: "res_01xray", units: 1 }], batch: { min_size: 2, max_size: 4 } } },
      { op: "add", path: "/views/2d/positions/node_01xray", value: { x: 640, y: 240 } },
      { op: "add", path: "/flows/-", value: { id: "flow_01a", from: "aoi", to: "node_01xray", routing: { type: "probability", probability: 1 } } },
      { op: "add", path: "/resources/-", value: { id: "res_01xray", type: "machine", name: "X-ray machine", capacity: 1, queue_discipline: "fifo" } },
      { op: "add", path: "/nodes/-", value: { id: "node_01q", type: "queue", name: "Before X-ray" } },
      { op: "add", path: "/flows/-", value: { id: "flow_01b", from: "aoi_result", to: "node_01q", routing: { type: "probability", probability: 0 } } },
      { op: "add", path: "/views/2d/positions/smt", value: { x: 10, y: 20 } },
    ];
    expect(described(changes)).toEqual([
      {
        kind: "added", subject: step("node_01xray", "X-ray", "process", true), placed: true,
        holds: [
          { field: { key: "time", of: "process", param: null }, value: { kind: "distribution", value: { type: "constant", value: 2 }, time: true } },
          // Named as the patch leaves the model: the resource is added after the step.
          { field: { key: "uses", resource: ref("res_01xray", "X-ray machine", true) }, value: num(1) },
          { field: { key: "batch", which: "min_size" }, value: num(2) },
          { field: { key: "batch", which: "max_size" }, value: num(4) },
        ],
      },
      { kind: "added", subject: connection("flow_01a", ref("aoi", "AOI"), ref("node_01xray", "X-ray", true)), placed: false, holds: [] },
      { kind: "added", subject: { kind: "resource", ref: ref("res_01xray", "X-ray machine", true) }, placed: false, holds: [{ field: { key: "capacity" }, value: num(1) }] },
      // A queue without a capacity holds no limit; a decision's way out says its probability.
      { kind: "added", subject: step("node_01q", "Before X-ray", "queue", true), placed: false, holds: [{ field: { key: "capacity" }, value: { kind: "absent" } }] },
      { kind: "added", subject: connection("flow_01b", ref("aoi_result", "AOI result"), ref("node_01q", "Before X-ray", true)), placed: false, holds: [{ field: { key: "probability" }, value: num(0, "percent") }] },
      { kind: "layout", steps: [ref("smt", "SMT")] },
    ]);
  });

  it("names what a removal took by what it was right before, and reads later indices after the shift", () => {
    const d = at("aoi_defect");
    const out = described([
      { op: "test", path: `/nodes/${d}/id`, value: "aoi_defect" },
      { op: "remove", path: `/nodes/${d}` },
      // The node that followed it now has its index.
      { op: "replace", path: `/nodes/${d}/name`, value: "Touch-up queue" },
      { op: "test", path: `/flows/${flow("aoi_result_aoi_defect")}/id`, value: "aoi_result_aoi_defect" },
      { op: "remove", path: `/flows/${flow("aoi_result_aoi_defect")}` },
    ]);
    expect(out).toEqual([
      { kind: "removed", subject: step("aoi_defect", "AOI defect", "failure") },
      { kind: "changed", subject: step("q_touchup", "Before touch-up", "queue"), field: { key: "name" }, before: { kind: "text", value: "Before touch-up" }, after: { kind: "text", value: "Touch-up queue" } },
      // A step the patch removed earlier keeps the name the base gave it.
      { kind: "removed", subject: connection("aoi_result_aoi_defect", ref("aoi_result", "AOI result"), ref("aoi_defect", "AOI defect")) },
    ]);
  });

  it("keeps a path it has no words for as the path and its JSON values", () => {
    expect(described([{ op: "add", path: "/metadata/owner", value: { team: "line 2" } }])).toEqual([
      { kind: "other", path: "/metadata/owner", before: { kind: "absent" }, after: { kind: "json", value: { team: "line 2" } } },
    ]);
    const smt = at("smt");
    // (SMT has no domain attributes yet: the member is added, as JSON Patch requires.)
    expect(described([{ op: "add", path: `/nodes/${smt}/domain_attributes`, value: { station: "smt" } }])).toEqual([
      { kind: "changed", subject: step("smt", "SMT", "process"), field: { key: "path", path: "/domain_attributes" }, before: { kind: "absent" }, after: { kind: "json", value: { station: "smt" } } },
    ]);
  });

  it("shows a name as the text it is, markup and all", () => {
    const hostile = '<img src=x onerror="alert(1)"> [approve](https://evil.example)';
    const [c] = described([{ op: "add", path: "/nodes/-", value: { id: "node_01h", type: "delay", name: hostile, delay_time: { distribution: { type: "constant", value: 1 } } } }]);
    expect(c).toEqual({ kind: "added", subject: step("node_01h", hostile, "delay", true), placed: false, holds: [{ field: { key: "time", of: "delay", param: null }, value: { kind: "distribution", value: { type: "constant", value: 1 }, time: true } }] });
  });

  it("does not guess when the patch does not apply to the base, or names a prototype member", () => {
    expect(describeChanges(sampleLine, [{ op: "test", path: "/nodes/0/id", value: "not_release_a" }, { op: "remove", path: "/nodes/0" }]).ok).toBe(false);
    expect(describeChanges(sampleLine, [{ op: "replace", path: "/nodes/99/name", value: "x" }]).ok).toBe(false);
    expect(describeChanges(sampleLine, [{ op: "add", path: "/nodes/0/__proto__", value: { polluted: true } }]).ok).toBe(false);
    expect(describeChanges(sampleLine, [{ op: "add", path: "/metadata/constructor", value: 1 }]).ok).toBe(false);
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();
  });

  it("leaves the base as it was", () => {
    const before = JSON.stringify(sampleLine);
    described([{ op: "replace", path: `/resources/${res("ict_tester")}/capacity`, value: 3 }, { op: "remove", path: `/nodes/${at("scrap")}` }]);
    expect(JSON.stringify(sampleLine)).toBe(before);
  });
});

// T-031 review round 1 (M1): a name comes from the model or the patch, and
// the patch is the AI's, so a name tells an entity apart only when no other
// entity of its kind reads the same. Where another one does (in the base,
// as the patch leaves the model, or anywhere between), or the patch adds the
// entity, the change carries its ID to show beside the name.
describe("which entities a change names with their ID", () => {
  const NEW = "resource_01m4ydqk00e1ectr0n1c5x0009";
  const ict = at("ict");
  const moveIct = (name: string): JsonPatch => [
    { op: "add", path: "/resources/-", value: { id: NEW, type: "machine", name, capacity: 9, queue_discipline: "fifo", calendar_id: "two_shift" } },
    { op: "test", path: `/nodes/${ict}/requires/0/resource_id`, value: "ict_tester" },
    { op: "replace", path: `/nodes/${ict}/requires/0/resource_id`, value: NEW },
  ];

  it("a new resource with the existing one's name, and the ICT step moved onto it: both with their IDs (the review's case)", () => {
    expect(described(moveIct("ICT tester"))).toEqual([
      { kind: "added", subject: { kind: "resource", ref: ref(NEW, "ICT tester", true) }, placed: false, holds: [{ field: { key: "capacity" }, value: num(9) }] },
      {
        kind: "changed", subject: step("ict", "ICT", "process"), field: { key: "usesResource" },
        before: { kind: "ref", ref: ref("ict_tester", "ICT tester", true) }, after: { kind: "ref", ref: ref(NEW, "ICT tester", true) },
      },
    ]);
  });

  it("a name that differs only by a trailing zero-width space reads the same: both with their IDs, the name as it is", () => {
    const [, moved] = described(moveIct("ICT tester​"));
    expect(moved).toMatchObject({ before: { kind: "ref", ref: ref("ict_tester", "ICT tester", true) }, after: { kind: "ref", ref: ref(NEW, "ICT tester​", true) } });
  });

  it("two existing resources with one name: a step moved from one to the other names both with their IDs, other entities without", () => {
    const base = structuredClone(sampleLine);
    base.resources[res("debug_operator")]!.name = "Operator";
    base.resources[res("pack_operator")]!.name = "Operator";
    const pack = at("pack");
    const r = describeChanges(base, [
      { op: "replace", path: `/nodes/${pack}/requires/0/resource_id`, value: "debug_operator" },
      { op: "replace", path: `/resources/${res("ict_tester")}/capacity`, value: 3 },
    ]);
    expect(r.ok && r.changes).toEqual([
      { kind: "changed", subject: step("pack", "Pack and ship", "process"), field: { key: "usesResource" }, before: { kind: "ref", ref: ref("pack_operator", "Operator", true) }, after: { kind: "ref", ref: ref("debug_operator", "Operator", true) } },
      { kind: "changed", subject: { kind: "resource", ref: ref("ict_tester", "ICT tester") }, field: { key: "capacity" }, before: num(2), after: num(3) },
    ]);
  });

  it("a rename that makes a name shared marks both entities, wherever they are named in the patch", () => {
    const out = described([
      { op: "replace", path: `/resources/${res("aoi_machine")}/name`, value: "SMT line" },
      { op: "replace", path: `/resources/${res("smt_line")}/capacity`, value: 2 },
    ]);
    expect(out[0]).toMatchObject({ subject: { kind: "resource", ref: ref("aoi_machine", "AOI machine", true) }, before: { kind: "text", value: "AOI machine" }, after: { kind: "text", value: "SMT line" } });
    expect(out[1]).toMatchObject({ subject: { kind: "resource", ref: ref("smt_line", "SMT line", true) } });
  });

  it("names steps the same way, and a step without a name by its ID", () => {
    const out = described([
      { op: "add", path: "/nodes/-", value: { id: "node_01dup", type: "delay", name: "ICT", delay_time: { distribution: { type: "constant", value: 1 } } } },
      { op: "add", path: "/flows/-", value: { id: "flow_01dup", from: "ict", to: "node_01dup", routing: { type: "probability", probability: 1 } } },
      { op: "add", path: "/nodes/-", value: { id: "node_01anon", type: "delay", delay_time: { distribution: { type: "constant", value: 1 } } } },
    ]);
    expect(out[0]).toMatchObject({ kind: "added", subject: step("node_01dup", "ICT", "delay", true) });
    expect(out[1]).toMatchObject({ kind: "added", subject: connection("flow_01dup", ref("ict", "ICT", true), ref("node_01dup", "ICT", true)) });
    expect(out[2]).toMatchObject({ kind: "added", subject: step("node_01anon", "node_01anon", "delay", true) });
  });

  it("a second connection between the same two steps carries its ID, and so does the one already there", () => {
    const f = flow("ict_result_q_burn_in");
    const out = described([
      { op: "add", path: "/flows/-", value: { id: "flow_01twin", from: "ict_result", to: "q_burn_in", routing: { type: "probability", probability: 0 } } },
      { op: "replace", path: `/flows/${f}/routing/probability`, value: 0.95 },
    ]);
    expect(out[0]).toMatchObject({ subject: connection("flow_01twin", ref("ict_result", "ICT result"), ref("q_burn_in", "Before burn-in"), true) });
    expect(out[1]).toMatchObject({ subject: connection("ict_result_q_burn_in", ref("ict_result", "ICT result"), ref("q_burn_in", "Before burn-in"), true) });
  });
});
