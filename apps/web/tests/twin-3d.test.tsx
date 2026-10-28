// @vitest-environment happy-dom
// T-033: the read-only 3D projection (ADR-008, TEST-025's 3D side). The
// scene is computed from the IR alone: the same step IDs as the 2D canvas,
// placed where the 2D canvas places them, flows between the same steps,
// nothing added or dropped; results only colour and raise; picking finds
// the step under a ray. Without WebGL the view says so and nothing else
// breaks (jsdom has no WebGL: the real failure path).
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { act } from "react";
import { describe, expect, it } from "vitest";
import type { ModelIR, Result } from "../lib/contracts";
import { canvasLayout } from "../features/model-editor/layout";
import { buildScene, pick } from "../features/twin-3d/scene";
import { en } from "../messages/en";
import { mount, settle, unmountAll } from "./ui-harness";
import type { View } from "../features/twin-3d/viewer";

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

/** A view that records what the component asks of it; stepAt always finds `hit`. */
function fakeView(hit: string | null = "burn_in") {
  const calls: string[] = [];
  const view: View = {
    setScene: (sc) => calls.push(`scene:${sc.nodes.length}`),
    setPalette: () => calls.push("palette"),
    setSelected: (id) => calls.push(`selected:${id}`),
    stepAt: (x, y) => (calls.push(`at:${x},${y}`), hit),
    orbit: (a, b) => calls.push(`orbit:${a},${b}`),
    zoom: (f) => calls.push(`zoom:${f.toFixed(2)}`),
    pan: (x, y) => calls.push(`pan:${x},${y}`),
    reset: () => calls.push("reset"),
    resize: () => {},
    destroy: () => calls.push("destroy"),
  };
  return { view, calls };
}

describe("the 3D view's pointer and keys (review r1)", () => {
  async function opened(hit: string | null = "burn_in") {
    const { Twin3D } = await import("../features/twin-3d/viewer");
    const { view, calls } = fakeView(hit);
    const picked: (string | null)[] = [];
    const el = await mount(<Twin3D ir={template()} selected="burn_in" onSelect={(id) => picked.push(id)} loadView={async () => view} />, "en");
    await settle();
    const canvas = el.querySelector("canvas")!;
    const pointer = async (type: string, x: number, y: number, extra: PointerEventInit = {}) =>
      act(async () => void canvas.dispatchEvent(new PointerEvent(type, { bubbles: true, clientX: x, clientY: y, button: 0, pointerId: 1, ...extra })));
    const key = async (k: string, extra: KeyboardEventInit = {}) => {
      const e = new KeyboardEvent("keydown", { bubbles: true, cancelable: true, key: k, ...extra });
      await act(async () => void canvas.dispatchEvent(e));
      return e.defaultPrevented;
    };
    return { el, canvas, calls, picked, pointer, key };
  }

  it("draws the scene and the selection once the view is made, and destroys it when it goes", async () => {
    const { calls } = await opened();
    expect(calls).toContain(`scene:${template().nodes.length}`);
    expect(calls).toContain("selected:burn_in");
    unmountAll();
    expect(calls.at(-1)).toBe("destroy");
  });

  it("selects the step under a click, and never after a drag, however slow", async () => {
    const { calls, picked, pointer } = await opened();
    await pointer("pointerdown", 100, 100);
    await pointer("pointerup", 100, 100);
    expect(picked).toEqual(["burn_in"]);
    // Twenty 1-pixel moves: each is small, the drag is not (review r1 M2).
    await pointer("pointerdown", 100, 100);
    for (let i = 1; i <= 20; i++) await pointer("pointermove", 100 + i, 100);
    await pointer("pointerup", 120, 100);
    expect(picked).toEqual(["burn_in"]);
    expect(calls.filter((c) => c.startsWith("orbit:")).length).toBe(20);
    // A cancelled pointer ends the drag: a later hover only looks.
    await pointer("pointerdown", 100, 100);
    await pointer("pointercancel", 100, 100);
    const orbits = calls.filter((c) => c.startsWith("orbit:")).length;
    await pointer("pointermove", 150, 100);
    expect(calls.filter((c) => c.startsWith("orbit:")).length).toBe(orbits);
  });

  it("turns, zooms, resets and clears from the keys, and leaves the browser's shortcuts alone", async () => {
    const { calls, picked, key } = await opened();
    expect(await key("ArrowLeft")).toBe(true);
    expect(await key("+")).toBe(true);
    expect(await key("0")).toBe(true);
    expect(await key("Escape")).toBe(true);
    expect(picked).toEqual([null]);
    const before = calls.length;
    // Ctrl/Cmd + and −, Alt+←: the page's zoom and Back (review r1 M1).
    expect(await key("+", { ctrlKey: true })).toBe(false);
    expect(await key("-", { metaKey: true })).toBe(false);
    expect(await key("ArrowLeft", { altKey: true })).toBe(false);
    expect(calls.length).toBe(before);
    expect(calls).toEqual(expect.arrayContaining(["orbit:-10,0", "zoom:0.80", "reset"]));
  });

  it("redraws in the page's new theme while open (review r1 M4)", async () => {
    const { calls } = await opened();
    await act(async () => {
      document.documentElement.setAttribute("data-theme", "dark");
      await new Promise((r) => setTimeout(r, 0));
    });
    expect(calls).toContain("palette");
    document.documentElement.removeAttribute("data-theme");
  });
});
