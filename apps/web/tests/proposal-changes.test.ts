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

const step = (id: string, name: string, type: string) => ({ kind: "step", id, name, type });
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
      { kind: "changed", subject: { kind: "resource", id: "ict_tester", name: "ICT tester" }, field: { key: "capacity" }, before: num(2), after: num(3) },
      { kind: "changed", subject: { kind: "connection", id: "ict_result_q_burn_in", from: "ICT result", to: "Before burn-in" }, field: { key: "probability" }, before: num(0.95, "percent"), after: num(0.9, "percent") },
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

  it("reads new steps with their canvas positions (a T-056 proposal) and their connections by name", () => {
    const changes: JsonPatch = [
      { op: "add", path: "/nodes/-", value: { id: "node_01xray", type: "process", name: "X-ray", service_time: { distribution: { type: "constant", value: 2 } }, requires: [] } },
      { op: "add", path: "/views/2d/positions/node_01xray", value: { x: 640, y: 240 } },
      { op: "add", path: "/flows/-", value: { id: "flow_01a", from: "aoi", to: "node_01xray", routing: { type: "probability", probability: 1 } } },
      { op: "add", path: "/resources/-", value: { id: "res_01xray", type: "machine", name: "X-ray machine", capacity: 1, queue_discipline: "fifo" } },
      { op: "add", path: "/views/2d/positions/smt", value: { x: 10, y: 20 } },
    ];
    expect(described(changes)).toEqual([
      { kind: "added", subject: step("node_01xray", "X-ray", "process"), placed: true },
      { kind: "added", subject: { kind: "connection", id: "flow_01a", from: "AOI", to: "X-ray" }, placed: false },
      { kind: "added", subject: { kind: "resource", id: "res_01xray", name: "X-ray machine" }, placed: false },
      { kind: "layout", steps: ["SMT"] },
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
      { kind: "removed", subject: { kind: "connection", id: "aoi_result_aoi_defect", from: "AOI result", to: "AOI defect" } },
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
    expect(c).toEqual({ kind: "added", subject: step("node_01h", hostile, "delay"), placed: false });
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
