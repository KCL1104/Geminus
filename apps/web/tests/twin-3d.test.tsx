// @vitest-environment happy-dom
// T-033: the read-only 3D projection (ADR-008, TEST-025's 3D side). The
// scene is computed from the IR alone: the same step IDs as the 2D canvas,
// placed where the 2D canvas places them, flows between the same steps,
// nothing added or dropped; results only colour and raise; picking finds
// the step under a ray. Without WebGL the view says so and nothing else
// breaks (jsdom has no WebGL: the real failure path).
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import type { ModelIR, Result } from "../lib/contracts";
import { canvasLayout } from "../features/model-editor/layout";
import { buildScene, pick } from "../features/twin-3d/scene";
import { en } from "../messages/en";
import { mount, settle } from "./ui-harness";

const template = (): ModelIR =>
  JSON.parse(readFileSync(join(process.cwd(), "../../internal/domainpack/electronicsline/templates/electronics_assembly_test_line.json"), "utf8")) as ModelIR;
const scenarioResult = (): Result => JSON.parse(readFileSync(join(process.cwd(), "features/results/fixtures/board-line-scenario-result.json"), "utf8")) as Result;
const boardModel = (): ModelIR => JSON.parse(readFileSync(join(process.cwd(), "features/results/fixtures/board-line-model.json"), "utf8")) as ModelIR;

describe("the 3D scene (T-033)", () => {
  it("has one object per step with the step's IR ID, where the 2D canvas puts it, and one line per flow between the same steps", () => {
    const ir = template();
    const scene = buildScene(ir);
    expect(scene.nodes.map((n) => n.id)).toEqual(ir.nodes.map((n) => n.id));
    const layout = canvasLayout(ir);
    for (const n of scene.nodes) {
      expect([n.position[0], n.position[2]], n.id).toEqual([layout[n.id]!.x / 60, layout[n.id]!.y / 40]);
      expect(n.position[1], n.id).toBeCloseTo(n.size[1] / 2);
    }
    expect(scene.edges.map((e) => [e.id, e.from, e.to])).toEqual(ir.flows.map((f) => [f.id, f.from, f.to]));
    expect(scene.nodes.every((n) => n.utilization === null && n.queueLength === null)).toBe(true);
    // The camera's target is inside the line.
    const xs = scene.nodes.map((n) => n.position[0]);
    expect(scene.center[0]).toBeGreaterThanOrEqual(Math.min(...xs));
    expect(scene.center[0]).toBeLessThanOrEqual(Math.max(...xs));
  });

  it("colours each process by its run's utilization and raises each queue by its mean length; nothing else changes", () => {
    const ir = boardModel();
    const result = scenarioResult();
    const plain = buildScene(ir);
    const scene = buildScene(ir, result);
    expect(scene.nodes.map((n) => [n.id, n.position[0], n.position[2]])).toEqual(plain.nodes.map((n) => [n.id, n.position[0], n.position[2]]));
    for (const n of scene.nodes) {
      const node = ir.nodes.find((x) => x.id === n.id)!;
      if (node.type === "process") {
        const us = node.requires.map((r) => result.metrics.resources[r.resource_id]?.utilization_mean).filter((u): u is number => typeof u === "number");
        expect(n.utilization, n.id).toBe(us.length > 0 ? Math.max(...us) : null);
      } else expect(n.utilization, n.id).toBeNull();
      const q = result.metrics.queues[n.id];
      if (node.type === "queue" && q?.status === "available") {
        expect(n.queueLength).toBe(q.queue_length_mean);
        expect(n.size[1]).toBeGreaterThanOrEqual(plain.nodes.find((x) => x.id === n.id)!.size[1]);
      }
    }
    expect(scene.nodes.some((n) => n.utilization !== null)).toBe(true);
  });

  it("finds the step a ray hits first, and nothing where there is no step", () => {
    const scene = buildScene(template());
    const target = scene.nodes.find((n) => n.type === "process")!;
    const [x, , z] = target.position;
    // Straight down onto the step, and from the side through it.
    expect(pick(scene, [x, 50, z], [0, -1, 0])).toBe(target.id);
    const before = scene.nodes.filter((n) => n.position[2] === z && n.position[0] < x).map((n) => n.id);
    const hit = pick(scene, [x - 1000, target.position[1], z], [1, 0, 0]);
    expect(hit === target.id || before.includes(hit!)).toBe(true);
    expect(pick(scene, [x, 50, z + 1000], [0, -1, 0])).toBeNull();
    expect(pick(scene, [x, 50, z], [0, 1, 0])).toBeNull(); // pointing away
  });
});

describe("the 3D view without WebGL", () => {
  it("says it cannot run, with nothing else on the page affected", async () => {
    const { Twin3D } = await import("../features/twin-3d/viewer");
    const el = await mount(<Twin3D ir={template()} selected={null} onSelect={() => {}} />, "en");
    const until = Date.now() + 15_000;
    while (!(el.textContent ?? "").includes(en["twin3d.unavailable"]) && Date.now() < until) await settle();
    expect(el.textContent).toContain(en["twin3d.unavailable"]);
    expect(el.querySelector("canvas")).toBeNull();
  }, 20_000);
});
