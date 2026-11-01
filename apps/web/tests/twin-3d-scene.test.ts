// H-072: the 3D stage's equipment (features/twin-3d/scene.ts, kits.ts).
// Every step is drawn as its equipment where the 2D canvas puts it, turned
// to its flow, every part of it inside the box picking and the first view
// use; the electronics line's stations look like what they are (SMT's four
// bays, AOI's gantry, a test cabinet and a light per tester, burn-in's door
// and a cell per chamber, a seat per operator, a packing table); every flow
// is a conveyor with an arrow that points downstream, clear of other steps;
// other packs get the generic shapes; a selected step is framed on the floor.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import type { ModelIR } from "../lib/contracts";
import { canvasLayout } from "../features/model-editor/layout";
import { equipmentOf } from "../features/twin-3d/equipment";
import { LAMP, MAX_UNITS } from "../features/twin-3d/kits";
import { arrowAt, buildScene, covers, type Part, type Scene, type SceneNode, selectionFrame } from "../features/twin-3d/scene";
import { readElectronicsLine } from "./fixtures/electronics-line";

const { model } = readElectronicsLine();
const withFct = (ir: ModelIR, capacity: number): ModelIR => ({ ...ir, resources: ir.resources.map((r) => (r.id === "fct_tester" ? { ...r, capacity } : r)) });
const template = (path: string) => JSON.parse(readFileSync(join(process.cwd(), "../..", path), "utf8")) as ModelIR;

/** A part's corners on the floor plan and its top, turned by its yaw. */
function extent(p: Part): { xs: number[]; zs: number[]; bottom: number; top: number } {
  const a = (p.yaw * Math.PI) / 180;
  const xs: number[] = [];
  const zs: number[] = [];
  const [hx, hz] = p.shape === "box" ? [p.size[0] / 2, p.size[2] / 2] : [p.size[0] / 2, p.size[0] / 2];
  for (const sx of [-1, 1])
    for (const sz of [-1, 1]) {
      xs.push(p.position[0] + sx * hx * Math.cos(a) + sz * hz * Math.sin(a));
      zs.push(p.position[2] - sx * hx * Math.sin(a) + sz * hz * Math.cos(a));
    }
  return { xs, zs, bottom: p.position[1] - p.size[1] / 2, top: p.position[1] + p.size[1] / 2 };
}

function inBox(n: SceneNode, x: number, z: number, y: number, eps = 1e-6) {
  const r = n.rotationY === 0 ? null : Math.hypot(n.size[0], n.size[2]) / 2;
  const hx = r ?? n.size[0] / 2;
  const hz = r ?? n.size[2] / 2;
  return Math.abs(x - n.position[0]) <= hx + eps && Math.abs(z - n.position[2]) <= hz + eps && y >= -eps && y <= n.size[1] + eps;
}

const partsOf = (scene: Scene, id: string) => scene.parts.filter((p) => p.node === id);
const node = (scene: Scene, id: string) => scene.nodes.find((n) => n.id === id)!;

describe("the electronics line's stations", () => {
  const scene = buildScene(model);

  it("draws every step as its equipment, where the 2D canvas puts it", () => {
    const kinds = equipmentOf(model);
    const layout = canvasLayout(model);
    expect(scene.nodes.map((n) => [n.id, n.kind])).toEqual(model.nodes.map((n) => [n.id, kinds.get(n.id)!.kind]));
    for (const n of model.nodes) {
      const st = scene.stations[n.id]!;
      expect(st.kind, n.id).toBe(kinds.get(n.id)!.kind);
      expect([st.hub[0], st.hub[2]], n.id).toEqual([layout[n.id]!.x / 60, layout[n.id]!.y / 40]);
      expect(partsOf(scene, n.id).length, n.id).toBeGreaterThan(0);
    }
  });

  it("keeps every part and light of a station inside its box, so picking and the first view hold all of it", () => {
    for (const n of scene.nodes) {
      for (const p of partsOf(scene, n.id)) {
        const e = extent(p);
        for (let i = 0; i < 4; i++) expect(inBox(n, e.xs[i]!, e.zs[i]!, e.top), `${n.id} ${p.tone} ${p.shape} at ${p.position.join(",")}`).toBe(true);
        expect(e.bottom, n.id).toBeGreaterThanOrEqual(-1e-9);
      }
      for (const l of scene.stations[n.id]!.lamps) expect(inBox(n, l[0], l[2], l[1] + LAMP), `${n.id} light`).toBe(true);
    }
  });

  it("draws a test cabinet and a light per tester, so a fourth FCT tester is a fourth cabinet", () => {
    const cabinets = (s: Scene, id: string) => partsOf(s, id).filter((p) => p.tone === "detail").length; // each cabinet's instrument tower cap
    expect([scene.stations.ict!.units.length, scene.stations.ict!.lamps.length, cabinets(scene, "ict")]).toEqual([2, 2, 2]);
    expect([scene.stations.fct!.units.length, scene.stations.fct!.lamps.length, cabinets(scene, "fct")]).toEqual([3, 3, 3]);
    const four = buildScene(withFct(model, 4));
    expect([four.stations.fct!.units.length, four.stations.fct!.lamps.length, cabinets(four, "fct")]).toEqual([4, 4, 4]);
    expect(four.stations.fct_retest!.units.length).toBe(4);
    // Side by side: one cabinet per unit, each its own place, none on another.
    const spots = four.stations.fct!.units.map((g) => g.at.join(","));
    expect(new Set(spots).size).toBe(4);
  });

  it("gives SMT four bays and a window, AOI a camera gantry, burn-in a door and a cell per chamber, debug a seat per operator, packing a table", () => {
    const smt = partsOf(scene, "smt");
    expect(smt.filter((p) => p.tone === "body")).toHaveLength(4); // printer, SPI, placement, reflow
    const bays = smt.filter((p) => p.tone === "body").map((p) => p.position[0]).sort((a, b) => a - b);
    const window = scene.stations.smt!.units[0]!.at[0];
    expect(window).toBeGreaterThan(bays[1]!); // the board in service shows between SPI and placement
    expect(window).toBeLessThan(bays[2]!);
    expect(scene.stations.smt!.lamps).toHaveLength(1);

    const aoi = partsOf(scene, "aoi");
    const belt = aoi.find((p) => p.tone === "belt")!;
    const camera = aoi.find((p) => p.tone === "detail")!;
    expect(camera.position[1]).toBeGreaterThan(belt.position[1] + 0.2); // above its conveyor
    expect(aoi.filter((p) => p.tone === "trim" && p.shape === "box").length).toBeGreaterThanOrEqual(3); // two posts and a beam

    const oven = scene.stations.burn_in!;
    expect([oven.cells.length, oven.units.length, oven.lamps.length, oven.shared]).toEqual([9, 0, 1, true]);
    const door = partsOf(scene, "burn_in").find((p) => p.tone === "detail")!;
    expect(door.position[2]).toBeGreaterThan(node(scene, "burn_in").position[2]); // on its front, toward the viewer
    expect(new Set(oven.cells.map((c) => c.at.join(","))).size).toBe(9);

    for (const id of ["aoi_touchup", "ict_debug", "fct_debug"]) {
      const seats = partsOf(scene, id).filter((p) => p.tone === "detail" && p.shape === "cylinder");
      expect([seats.length, scene.stations[id]!.units.length, scene.stations[id]!.lamps.length], id).toEqual([4, 4, 4]);
    }
    expect([scene.stations.pack_ship!.kind, scene.stations.pack_ship!.units.length, scene.stations.pack_ship!.lamps.length]).toEqual(["table", 2, 2]);
    // Only stations have lights.
    for (const n of model.nodes) if (n.type !== "process") expect(scene.stations[n.id]!.lamps, n.id).toEqual([]);
  });

  it("stacks a queue's boards in a grid on its conveyor, the front at the end its station takes them from", () => {
    for (const q of model.nodes.filter((n) => n.type === "queue")) {
      const st = scene.stations[q.id]!;
      const next = scene.stations[scene.links[q.id]![0]!]!;
      const toward = [next.hub[0] - st.hub[0], next.hub[2] - st.hub[2]];
      const length = Math.hypot(toward[0]!, toward[1]!);
      // Slot 0 is nearer the station than the queue's centre, and the slots run away from it.
      const front = (st.wait.at[0] - st.hub[0]) * toward[0]! + (st.wait.at[2] - st.hub[2]) * toward[1]!;
      expect(front / length, q.id).toBeGreaterThan(0.5);
      expect((st.wait.along[0] * toward[0]! + st.wait.along[1] * toward[1]!) / length, q.id).toBeCloseTo(-1);
      expect([st.wait.cols, st.wait.rows], q.id).toEqual([4, 3]);
    }
  });

  it("turns a queue along its flow out and a station square to its flow in", () => {
    const yaw = (id: string) => scene.stations[id]!.yaw;
    expect([yaw("q_fct"), yaw("fct"), yaw("smt")]).toEqual([0, 0, 0]); // the main line runs left to right
    expect(yaw("q_ict_debug")).toBe(-90); // down to its bench (+z)
    expect(yaw("ict_debug")).toBe(-90); // fed from above
    expect(yaw("q_ict_retest")).toBe(90); // up to its tester
    expect(yaw("ict_retest")).toBe(90);
    // A quarter turn swaps the box's sides rather than turning it.
    expect(node(scene, "ict_debug").rotationY).toBe(0);
    expect(node(scene, "ict_debug").size[0]).toBeGreaterThan(node(scene, "ict_debug").size[2]);
  });
});

describe("the conveyors", () => {
  const scene = buildScene(model);
  const arrows = scene.parts.filter((p) => p.node === null && p.size[1] === 0.012);

  it("lays a conveyor on every flow, from step to step", () => {
    expect(scene.edges.map((e) => [e.id, e.from, e.to])).toEqual(model.flows.map((f) => [f.id, f.from, f.to]));
    const belts = scene.parts.filter((p) => p.node === null && p.tone === "belt");
    expect(belts).toHaveLength(scene.edges.length);
    scene.edges.forEach((e, i) => {
      const b = belts[i]!;
      expect(b.size[0]).toBeCloseTo(Math.hypot(e.end[0] - e.start[0], e.end[2] - e.start[2]));
      expect([b.position[0], b.position[2]]).toEqual([(e.start[0] + e.end[0]) / 2, (e.start[2] + e.end[2]) / 2]);
    });
  });

  it("puts an arrow on each, pointing downstream, where no other step covers it", () => {
    expect(arrows).toHaveLength(2 * scene.edges.length);
    scene.edges.forEach((e, i) => {
      const tip = arrowAt(e, scene.nodes)!;
      const d = [e.end[0] - e.start[0], e.end[2] - e.start[2]];
      // Both arms lie behind the tip: the chevron points from `from` to `to`.
      for (const arm of arrows.slice(2 * i, 2 * i + 2)) expect((arm.position[0] - tip[0]) * d[0]! + (arm.position[2] - tip[2]) * d[1]!, e.id).toBeLessThan(0);
      const others = scene.nodes.filter((n) => n.id !== e.from && n.id !== e.to);
      expect(others.filter((n) => covers(n, tip[0], tip[2])).map((n) => n.id), e.id).toEqual([]);
    });
  });

  it("are the same with or without a run: a line with no run shows its direction and nothing else", () => {
    expect(buildScene(model).parts).toEqual(scene.parts);
  });
});

describe("the generic shapes for other packs", () => {
  it("draws a software delivery pipeline as machines, a cabinet and a light per unit", () => {
    const ir = template("internal/domainpack/softwaredelivery/templates/default_delivery_pipeline.json");
    const scene = buildScene(ir);
    for (const n of ir.nodes.filter((x) => x.type === "process")) {
      const st = scene.stations[n.id]!;
      const units = ir.resources.find((r) => r.id === n.requires[0]?.resource_id)?.capacity ?? 1;
      expect(st.kind, n.id).toBe("machine");
      if (units <= MAX_UNITS) expect([st.units.length, st.lamps.length, st.shared], n.id).toEqual([units, units, false]);
      else expect([st.units.length, st.lamps.length, st.shared], n.id).toEqual([1, 1, true]);
    }
  });

  it("draws a bank too large to show unit by unit as one block with one light", () => {
    const ir = template("internal/domainpack/softwaredelivery/templates/default_delivery_pipeline.json");
    const first = ir.nodes.find((n) => n.type === "process")!;
    if (first.type !== "process") throw new Error("no process");
    const big = { ...ir, resources: ir.resources.map((r) => (r.id === first.requires[0]!.resource_id ? { ...r, capacity: 20 } : r)) };
    const st = buildScene(big).stations[first.id]!;
    expect([st.units.length, st.lamps.length, st.shared]).toEqual([1, 1, true]);
    expect(st.units[0]!.cols * st.units[0]!.rows).toBeGreaterThan(1); // its boards in service side by side on top
  });
});

describe("the selected step", () => {
  it("is framed on the floor just outside its box, nothing of it hidden", () => {
    const scene = buildScene(withFct(model, 4));
    const fct = node(scene, "fct");
    const bars = selectionFrame(fct);
    expect(bars).toHaveLength(4);
    for (const b of bars) {
      expect(b.position[1] + b.size[1] / 2).toBeLessThan(0.05); // on the floor
      const e = extent(b);
      // Every bar lies outside the footprint, around it.
      const inside = e.xs.every((x, i) => Math.abs(x - fct.position[0]) < fct.size[0] / 2 && Math.abs(e.zs[i]! - fct.position[2]) < fct.size[2] / 2);
      expect(inside).toBe(false);
    }
    const xs = bars.flatMap((b) => extent(b).xs);
    const zs = bars.flatMap((b) => extent(b).zs);
    expect(Math.min(...xs)).toBeLessThan(fct.position[0] - fct.size[0] / 2);
    expect(Math.max(...xs)).toBeGreaterThan(fct.position[0] + fct.size[0] / 2);
    expect(Math.min(...zs)).toBeLessThan(fct.position[2] - fct.size[2] / 2);
    expect(Math.max(...zs)).toBeGreaterThan(fct.position[2] + fct.size[2] / 2);
  });
});
