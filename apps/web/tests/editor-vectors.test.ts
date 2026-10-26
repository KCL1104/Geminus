// T-015: the editor's patches, written out for the server to check. Each
// vector names a base IR, the ops one editor action (or a save of several)
// produces, and the model hash (sha256 of the RFC 8785 form, D-006) of the
// IR the editor then shows. internal/model/patch's TestEditorVectors
// applies the ops with the server's manual-draft policy and requires a
// schema-valid IR with that hash: the browser and the API agree on what an
// edit does. This test fails when the editor's ops change and the file was
// not regenerated (GEMINUS_WRITE_EDITOR_VECTORS=1 npx vitest run
// tests/editor-vectors.test.ts), so the Go test always checks today's ops.
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { sampleLine } from "../app/ui-gallery/editor/sample-line";
import type { ModelIR } from "../lib/contracts";
import { addStep, connectSteps, deleteStep, disconnect, type EditResult, moveStep, renameStep, setBatch, setProbability, setQueueCapacity, setTiming } from "../features/model-editor/edits";
import { layeredLayout } from "../features/model-editor/layout";
import { applyPatch, type Op } from "../features/model-editor/patch";

const file = join(process.cwd(), "../../testdata/editor/patch-vectors.json");
const fixture = (name: string): ModelIR => JSON.parse(readFileSync(join(process.cwd(), "../../testdata/contracts", name), "utf8")) as ModelIR;
const clone = (ir: ModelIR): ModelIR => JSON.parse(JSON.stringify(ir)) as ModelIR;
const placed = (ir: ModelIR): ModelIR => {
  const c = clone(ir);
  c.views["2d"].positions = layeredLayout(c);
  return c;
};

type Base = "sample" | "sample_placed" | "minimal" | "line_flow_placed";
type Vector = { name: string; base: Base; ops: Op[]; expected_model_hash: string };

/** RFC 8785 for the values an IR holds (no -0, no exotic numbers): sorted members, ES number and string forms. */
function jcs(v: unknown): string {
  if (Array.isArray(v)) return `[${v.map(jcs).join(",")}]`;
  if (v !== null && typeof v === "object") {
    const o = v as Record<string, unknown>;
    return `{${Object.keys(o).sort().map((k) => `${JSON.stringify(k)}:${jcs(o[k])}`).join(",")}}`;
  }
  return JSON.stringify(v);
}
const modelHash = (ir: ModelIR) => `sha256:${createHash("sha256").update(jcs(ir)).digest("hex")}`;

function apply(ir: ModelIR, ops: Op[]): ModelIR {
  const r = applyPatch(ir, ops);
  if (!r.ok) throw new Error(`op ${r.index}: ${r.reason}`);
  return r.doc;
}

/** Runs edits one after another, each on the IR the previous one left, as the editor does; the save sends all their ops. */
function save(name: string, base: Base, ...edits: ((ir: ModelIR) => EditResult)[]): Vector {
  let ir = bases()[base];
  const ops: Op[] = [];
  for (const edit of edits) {
    const r = edit(ir);
    if (!r.ok) throw new Error(`${name}: refused ${r.refusal}`);
    ops.push(...r.ops);
    ir = apply(ir, r.ops);
  }
  expect(ops.length, name).toBeGreaterThan(0);
  return { name, base, ops, expected_model_hash: modelHash(ir) };
}

function bases(): Record<Base, ModelIR> {
  return { sample: clone(sampleLine), sample_placed: placed(sampleLine), minimal: fixture("valid-minimal-ir.json"), line_flow_placed: placed(fixture("valid-line-flow-ir.json")) };
}

function vectors(): Vector[] {
  const [sample, line, minimal, lineFlow] = ["sample", "sample_placed", "minimal", "line_flow_placed"] as const;
  return [
    save("move a placed step", line, (ir) => moveStep(ir, "smt", { x: 410, y: 30 })),
    save("the first move writes the layout down", sample, (ir) => moveStep(ir, "aoi", { x: 700, y: -40 })),
    save("a move creates the positions map", minimal, (ir) => moveStep(ir, "work", { x: 250, y: 60 })),
    save("rename a step", line, (ir) => renameStep(ir, "smt", "SMT line 1")),
    save("name an unnamed step", minimal, (ir) => renameStep(ir, "work", "Work")),
    save("remove a name", line, (ir) => renameStep(ir, "q_smt", "")),
    save("add a process", line, (ir) => addStep(ir, "process")),
    save("add a source, a queue, a decision, a delay, a failure, a rework and a sink", line,
      (ir) => addStep(ir, "source"), (ir) => addStep(ir, "queue"), (ir) => addStep(ir, "decision"), (ir) => addStep(ir, "delay"),
      (ir) => addStep(ir, "failure"), (ir) => addStep(ir, "rework"), (ir) => addStep(ir, "sink")),
    save("delete a buffer with its flows and its queue metric", line, (ir) => deleteStep(ir, "q_ict")),
    save("delete a decision with its constraint", line, (ir) => deleteStep(ir, "ict_result")),
    save("delete a step of an unplaced layout", sample, (ir) => deleteStep(ir, "touchup_rework")),
    save("delete a decision whose flows are counted", lineFlow, (ir) => deleteStep(ir, "test")),
    save("delete a counted sink", lineFlow, (ir) => deleteStep(ir, "scrap")),
    save("connect two steps", line, (ir) => connectSteps(ir, "q_pack", "scrap")),
    save("a decision's next way out", line, (ir) => connectSteps(ir, "aoi_result", "scrap")),
    save("disconnect", line, (ir) => disconnect(ir, "aoi_result_aoi_defect")),
    save("disconnect a counted flow", lineFlow, (ir) => disconnect(ir, "test_pass")),
    save("decision probabilities", line, (ir) => setProbability(ir, "aoi_result_q_ict", 0.9), (ir) => setProbability(ir, "aoi_result_aoi_defect", 0.1)),
    save("queue capacity: change, set, remove", line, (ir) => setQueueCapacity(ir, "q_aoi", 25), (ir) => setQueueCapacity(ir, "q_smt", 50), (ir) => setQueueCapacity(ir, "q_burn_in", null)),
    save("a step's own time", line, (ir) => setTiming(ir, "smt", "max", 1.6), (ir) => setTiming(ir, "release_a", "mean", 2.5), (ir) => setTiming(ir, "burn_in", "value", 300)),
    save("batch sizes and no waiting limit", line, (ir) => setBatch(ir, "burn_in", "max_size", 48), (ir) => setBatch(ir, "burn_in", "min_size", 24), (ir) => setBatch(ir, "burn_in", "max_wait", null)),
    save("one save of several edits", sample,
      (ir) => addStep(ir, "process"),
      (ir) => renameStep(ir, "process_1", "X-ray"),
      (ir) => connectSteps(ir, "q_pack", "process_1"),
      (ir) => connectSteps(ir, "process_1", "shipped"),
      (ir) => moveStep(ir, "process_1", { x: 2600, y: 240 }),
      (ir) => deleteStep(ir, "touchup_rework"),
      (ir) => connectSteps(ir, "touchup", "q_ict"),
      (ir) => setQueueCapacity(ir, "q_debug", 30)),
  ];
}

describe("editor patch vectors for the server (testdata/editor/patch-vectors.json)", () => {
  it("hash like the server (RFC 8785, D-006): the shared hashing fixture", () => {
    const h = JSON.parse(readFileSync(join(process.cwd(), "../../testdata/hashing/expected-hashes.json"), "utf8")) as { model_hash?: Record<string, string> };
    const known = Object.entries(h.model_hash ?? {});
    expect(known.length).toBeGreaterThan(0);
    for (const [path, hash] of known) expect(modelHash(JSON.parse(readFileSync(join(process.cwd(), "../..", path), "utf8")) as ModelIR), path).toBe(hash);
  });

  it("are the ops the editor makes today", () => {
    const now = { comment: "Generated by apps/web/tests/editor-vectors.test.ts; checked by internal/model/patch TestEditorVectors.", bases: bases(), vectors: vectors() };
    const text = JSON.stringify(now, null, 2) + "\n";
    if (process.env.GEMINUS_WRITE_EDITOR_VECTORS === "1") writeFileSync(file, text);
    expect(readFileSync(file, "utf8")).toBe(text);
  });
});
