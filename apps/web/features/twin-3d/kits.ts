// The equipment of the 3D stage, built from primitives in code (H-072;
// docs/design/H-072_TRACE_ANIMATION.md §3): what each kind of station looks
// like, where its boards wait, sit in service and leave, and where its
// lights are. Pure: no PlayCanvas, no model, no time. scene.ts places each
// kit where the 2D canvas puts its step and turns it to its flow.
//
// Every kit is in its own frame: x runs with the flow (in at -x, out at +x),
// z across it (the units of a bank side by side, toward the viewer at +z
// before the stage turns it), y up, the floor at 0. Sizes are world units:
// one 2D layer (240 px) is 4, one row (120 px) is 3 (scene.ts), so a kit
// stays inside about 3.7 by 2.4 and the conveyors between steps show.
//
// The looks follow the brief: neutral housings, no textures or logos, a
// station's kind readable from its silhouette:
//   smt      a long low machine, its conveyor through it, four bays
//            (printer, SPI, placement, reflow) and a window between SPI and
//            placement where the board in service shows
//   aoi      a cabinet with a camera gantry over its conveyor
//   tester   one test cabinet per unit of capacity, side by side, each with
//            its instrument tower and light
//   oven     burn-in: a chamber with a door, open above so its batches show,
//            one cell per chamber (a batch loads into one cell)
//   bench    a bench with a seat and a light per operator
//   table    a packing table, a light per station
//   machine  the generic process: a cabinet per unit (one block past 6)
//   buffer   a short conveyor where boards stack in a grid, front first
//   dock, shipping, conveyor, junction, reject, return: the other steps
import type { EquipmentKind } from "./equipment";

export type Vec3 = [number, number, number];
export type Shape = "box" | "cylinder" | "sphere";
/**
 * A part's material, from the tokens (engine.ts): body Rule Strong (a
 * selected step's Graphite Ink; with results, a process from Control Edge
 * to Amber Edge), trim Control Edge, belt Rule, detail Ink 2, queue between
 * Rule and Control Edge (the T-033 mean-length column), ring the sheet
 * (unlit: the socket a light sits in).
 */
export type Tone = "body" | "trim" | "belt" | "detail" | "queue" | "ring";

export type LocalPart = { readonly shape: Shape; readonly tone: Tone; readonly at: Vec3; readonly size: Vec3 };

/**
 * Where a stack of boards goes: slot 0's centre, the way the slots run
 * (along, a column at a time, `rows` boards across each), then layer on
 * layer up to `layers` (more are not drawn). Directions are in the kit's
 * frame (x, z).
 */
export type LocalGrid = {
  readonly at: Vec3;
  readonly along: readonly [number, number];
  readonly cols: number;
  readonly rows: number;
  readonly layers: number;
};

export type Kit = {
  readonly parts: readonly LocalPart[];
  /** Half the footprint along x and z, and the height with its lights and its highest stack. */
  readonly half: readonly [number, number];
  readonly height: number;
  /** Queued (a queue's, or a process entered directly) and on a delay. */
  readonly wait: LocalGrid;
  /** Between two events of one tick (arrived, done), held at an oven, sliding into a sink. */
  readonly port: LocalGrid;
  /** In service, changing over and held (blocked): one per unit drawn; unit u goes to units[u % length], stacked. */
  readonly units: readonly LocalGrid[];
  /** An oven's chambers: a batch's members stack in one. */
  readonly cells: readonly LocalGrid[];
  /** The lights: one per unit drawn (the first units fill them), or one for the station (`shared`). */
  readonly lamps: readonly Vec3[];
  /** One light shows the station's state (an oven, a bank too large to draw unit by unit). */
  readonly shared: boolean;
};

/** A board: a small tile, along × thickness × across. */
export const BOARD: Vec3 = [0.3, 0.045, 0.22];
/** The distance between the centres of two boards side by side in a stack (along, across), and between two layers. */
export const PITCH: readonly [number, number] = [0.34, 0.26];
export const LAYER = 0.06;
/** The top of a station's own belt; a flow's conveyor runs just under it (scene.ts). */
export const BELT = 0.14;
/** Units drawn one by one; a larger bank is one block with one light. */
export const MAX_UNITS = 6;
/** A light: its radius, and the socket under it. */
export const LAMP = 0.06;
const RING = 0.025;

const lift = (y: number) => y + BOARD[1] / 2;

const box = (tone: Tone, x: number, y0: number, z: number, sx: number, sy: number, sz: number): LocalPart => ({ shape: "box", tone, at: [x, y0 + sy / 2, z], size: [sx, sy, sz] });
const cyl = (tone: Tone, x: number, y0: number, z: number, d: number, h: number): LocalPart => ({ shape: "cylinder", tone, at: [x, y0 + h / 2, z], size: [d, h, d] });
const grid = (x: number, y: number, z: number, cols = 1, rows = 1, layers = 6, along: readonly [number, number] = [-1, 0]): LocalGrid => ({ at: [x, lift(y), z], along, cols, rows, layers });

/** A light's socket on a pole from `base` up to `top` (none when top is base); returns where the light sits. */
function pole(parts: LocalPart[], x: number, z: number, base: number, top: number): Vec3 {
  if (top > base) parts.push(cyl("trim", x, base, z, 0.05, top - base));
  parts.push(cyl("ring", x, top, z, 0.2, RING));
  return [x, top + RING + LAMP, z];
}

/** The centres across z of n units side by side, at most `pitch` apart within `span`. */
function spread(n: number, pitch: number, span: number): { z: number[]; pitch: number } {
  const p = Math.min(pitch, span / n);
  return { z: Array.from({ length: n }, (_, i) => (i - (n - 1) / 2) * p), pitch: p };
}

const top = (lamps: readonly Vec3[], base: number) => Math.max(base, ...lamps.map((l) => l[1] + LAMP));
const halfZ = (zs: number[], d: number, min: number) => Math.max(min, ...zs.map((z) => Math.abs(z) + d / 2));

/** A conveyor section: its frame, its belt and, optionally, rails. */
function lane(parts: LocalPart[], length: number, width: number, rails: number) {
  parts.push(box("trim", 0, 0, 0, length, BELT - 0.04, width));
  parts.push(box("belt", 0, BELT - 0.04, 0, length - 0.06, 0.04, width - 0.1));
  if (rails > 0) for (const s of [-1, 1]) parts.push(box("trim", 0, BELT, s * (width / 2 - 0.02), length, rails, 0.03));
}

function buffer(): Kit {
  const parts: LocalPart[] = [];
  lane(parts, 1.7, 1, 0.07);
  // Four boards along by three across a layer, the front (slot 0) at the downstream end; twelve layers.
  const wait = grid(0.66, BELT, 0, 4, 3, 12);
  return { parts, half: [0.85, 0.5], height: lift(BELT) + 12 * LAYER, wait, port: wait, units: [], cells: [], lamps: [], shared: false };
}

function conveyor(): Kit {
  const parts: LocalPart[] = [];
  lane(parts, 2.8, 0.56, 0.07);
  const wait = grid(1.2, BELT, 0, 7, 1, 6);
  return { parts, half: [1.4, 0.28], height: lift(BELT) + 6 * LAYER, wait, port: wait, units: [], cells: [], lamps: [], shared: false };
}

function returnLane(): Kit {
  const parts: LocalPart[] = [];
  lane(parts, 1.5, 0.54, 0.12);
  parts.push(box("trim", -0.735, BELT, 0, 0.03, 0.12, 0.54)); // the lane's closed end: a U, back into the line
  const wait = grid(0.5, BELT, 0, 4, 1, 4);
  return { parts, half: [0.75, 0.27], height: 0.5, wait, port: wait, units: [], cells: [], lamps: [], shared: false };
}

function dock(out: boolean): Kit {
  // A platform, and the portal boards come in through (a source, at -x) or leave through (a sink, at +x).
  const parts: LocalPart[] = [box("body", 0, 0, 0, 1.2, 0.12, 1.2)];
  const x = out ? 0.56 : -0.56;
  for (const s of [-1, 1]) parts.push(box("trim", x, 0.12, s * 0.52, 0.07, 0.86, 0.07));
  parts.push(box("trim", x, 0.98, 0, 0.07, 0.07, 1.11));
  const port = grid(out ? -0.1 : 0.36, 0.12, 0, 2, 3, 6, out ? [1, 0] : [-1, 0]);
  return { parts, half: [0.6, 0.6], height: 1.05, wait: port, port, units: [], cells: [], lamps: [], shared: false };
}

function junction(): Kit {
  const parts = [cyl("trim", 0, 0, 0, 0.74, 0.11), cyl("belt", 0, 0.11, 0, 0.62, 0.035), cyl("body", 0, 0.145, 0, 0.16, 0.04)];
  const port = grid(0.18, 0.145, 0, 1, 1, 4);
  return { parts, half: [0.37, 0.37], height: 0.3, wait: port, port, units: [], cells: [], lamps: [], shared: false };
}

function reject(): Kit {
  // An open bin with a dark floor.
  const parts = [box("body", 0, 0, 0, 0.8, 0.04, 0.7), box("detail", 0, 0.04, 0, 0.72, 0.01, 0.62)];
  for (const s of [-1, 1]) parts.push(box("body", 0, 0.04, s * 0.33, 0.8, 0.4, 0.04), box("body", s * 0.38, 0.04, 0, 0.04, 0.4, 0.62));
  const port = grid(0, 0.05, 0, 2, 2, 4);
  return { parts, half: [0.4, 0.35], height: 0.5, wait: port, port, units: [], cells: [], lamps: [], shared: false };
}

/** A station's own queue (a process entered directly) on the floor before it, and its out side at belt height. */
const before = (x: number) => grid(x, 0, 0, 2, 3, 8);
const after = (x: number) => grid(x, BELT, 0, 1, 3, 6, [1, 0]);

function machine(n: number, kind: "machine" | "tester"): Kit {
  const parts: LocalPart[] = [];
  const lamps: Vec3[] = [];
  const units: LocalGrid[] = [];
  if (n > MAX_UNITS) {
    // One block for the bank, sized by its capacity, its boards in a grid on top.
    const span = Math.min(2.4, 0.6 + 0.06 * n);
    parts.push(box("body", 0, 0, 0, 0.9, 0.6, span), box("belt", -0.08, 0.6, 0, 0.6, 0.02, span - 0.12));
    lamps.push(pole(parts, 0.33, -span / 2 + 0.12, 0.6, 0.92));
    units.push(grid(0.04, 0.62, 0, 2, Math.max(1, Math.floor((span - 0.12) / PITCH[1])), 4));
    return { parts, half: [0.48, span / 2], height: top(lamps, 0.62 + 4 * LAYER), wait: before(-0.8), port: after(0.62), units, cells: [], lamps, shared: true };
  }
  const { z, pitch } = spread(n, kind === "tester" ? 0.56 : 0.62, 2.3);
  const d = pitch - 0.08;
  for (const zi of z) {
    if (kind === "tester") {
      // A test cabinet: its fixture where the board sits, its instrument tower behind it with the light on top.
      parts.push(box("body", 0, 0, zi, 0.9, 0.5, d), box("belt", -0.12, 0.5, zi, 0.5, 0.025, d - 0.12));
      parts.push(box("body", 0.33, 0.5, zi, 0.2, 0.42, d - 0.04), box("detail", 0.33, 0.92, zi, 0.21, 0.04, d - 0.03));
      lamps.push(pole(parts, 0.33, zi, 0.96, 0.96));
      units.push(grid(-0.12, 0.525, zi, 1, 1, 4));
    } else {
      parts.push(box("body", 0, 0, zi, 0.8, 0.6, d), box("belt", -0.08, 0.6, zi, 0.46, 0.02, d - 0.12));
      lamps.push(pole(parts, 0.3, zi, 0.6, 0.9));
      units.push(grid(-0.08, 0.62, zi, 1, 1, 4));
    }
  }
  return { parts, half: [kind === "tester" ? 0.45 : 0.4, halfZ(z, d, 0.3)], height: top(lamps, 0.62), wait: before(-0.8), port: after(0.6), units, cells: [], lamps, shared: false };
}

function aoi(n: number): Kit {
  // A cabinet with its conveyor on top and a camera gantry over it; one lane per unit (two at most, then one for all).
  const parts: LocalPart[] = [];
  const lamps: Vec3[] = [];
  const units: LocalGrid[] = [];
  const lanes = n <= 2 ? n : 1;
  const { z } = spread(lanes, 1, 2);
  for (const zi of z) {
    parts.push(box("body", 0, 0, zi, 1.4, 0.48, 0.86), box("belt", 0, 0.48, zi, 1.62, 0.03, 0.4));
    for (const s of [-1, 1]) parts.push(box("trim", 0.12, 0.48, zi + s * 0.38, 0.06, 0.56, 0.06));
    parts.push(box("trim", 0.12, 1.04, zi, 0.08, 0.07, 0.82), box("detail", 0.12, 0.86, zi, 0.17, 0.18, 0.17));
    lamps.push(pole(parts, 0.6, zi - 0.3, 0.48, 1));
    units.push(grid(-0.38, 0.51, zi, 1, 1, 4));
  }
  return { parts, half: [0.81, halfZ(z, 0.86, 0.43)], height: top(lamps, 1.11), wait: before(-1.05), port: after(0.95), units, cells: [], lamps, shared: lanes < n };
}

function smt(n: number): Kit {
  // Its belt runs the machine's length at belt height; the four bays stand over it.
  const parts: LocalPart[] = [box("trim", 0, 0, 0, 4, BELT - 0.04, 0.5), box("belt", 0, BELT - 0.04, 0, 4, 0.04, 0.4)];
  const bays: [number, number, number][] = [
    [-1.51, 0.6, 0.6], // printer: centre x, length, height
    [-0.835, 0.45, 0.46], // SPI
    [0.235, 0.85, 0.74], // placement
    [1.31, 1, 0.52], // reflow
  ];
  for (const [x, len, h] of bays) parts.push(box("body", x, 0, 0, len, h, 0.9));
  parts.push(box("detail", -1.51, 0.6, 0, 0.36, 0.02, 0.6)); // the printer's stencil hatch
  parts.push(box("detail", -0.835, 0.46, 0, 0.14, 0.08, 0.14)); // SPI's camera
  parts.push(box("trim", 0.235, 0.74, 0, 0.1, 0.06, 0.94), box("detail", 0.235, 0.66, 0.16, 0.16, 0.08, 0.16)); // placement's gantry and head
  for (const k of [-1, 0, 1]) parts.push(box("trim", 1.31 + k * 0.3, 0.52, 0, 0.05, 0.02, 0.8)); // reflow's zones
  // The window between SPI and placement, where the board in service shows; a light per unit behind it.
  const shown = Math.min(n, MAX_UNITS);
  const lamps = Array.from({ length: shown }, (_, i) => pole(parts, -0.4 + (i - (shown - 1) / 2) * 0.22, -0.36, BELT, 0.95));
  return {
    parts, half: [2, 0.47], height: top(lamps, 0.8), wait: before(-2.2), port: after(1.92),
    units: [grid(-0.4, BELT, 0, 1, 1, 6)], cells: [], lamps, shared: false,
  };
}

function oven(n: number): Kit {
  // A chamber open above, so its batches show: a tall back wall, a door on its front, one cell per chamber.
  const parts: LocalPart[] = [box("belt", 0, 0, 0, 2.9, 0.08, 1.9)];
  parts.push(box("body", 0, 0, -0.97, 3, 1.05, 0.06));
  for (const s of [-1, 1]) parts.push(box("body", s * 1.47, 0, 0, 0.06, 0.7, 2));
  parts.push(box("body", 0, 0, 0.97, 3, 0.7, 0.06));
  parts.push(box("detail", -0.55, 0.08, 1.005, 1.3, 0.54, 0.02), box("trim", -0.55, 0.42, 1.03, 0.6, 0.04, 0.04)); // the door and its handle
  // The cells fill the back of the chamber: the front wall hides what is right behind it from above.
  const count = Math.max(1, Math.min(n, 12));
  const rows = count <= 3 ? 1 : count <= 8 ? 2 : 3;
  const cols = Math.ceil(count / rows);
  const [x0, x1, z0, z1] = [-1.4, 1.4, -0.92, 0.36];
  const w = (x1 - x0) / cols;
  const d = (z1 - z0) / rows;
  for (let c = 1; c < cols; c++) parts.push(box("trim", x0 + c * w, 0.08, (z0 + z1) / 2, 0.02, 0.05, z1 - z0));
  for (let r = 1; r < rows; r++) parts.push(box("trim", 0, 0.08, z0 + r * d, x1 - x0, 0.05, 0.02));
  const across = d >= 0.5 ? 2 : 1;
  const along = w >= 0.72 ? 2 : 1;
  const cells: LocalGrid[] = [];
  for (let k = 0; k < count; k++) {
    // Column by column from the door (-x), back to front.
    const cx = x0 + (Math.floor(k / rows) + 0.5) * w;
    const cz = z0 + ((k % rows) + 0.5) * d;
    cells.push(grid(cx - ((along - 1) / 2) * PITCH[0], 0.08, cz, along, across, 8, [1, 0]));
  }
  const lamps = [pole(parts, 0, -0.94, 1.05, 1.18)];
  return { parts, half: [1.5, 1.06], height: top(lamps, 1.1), wait: before(-1.85), port: after(1.75), units: [], cells, lamps, shared: true };
}

function bench(n: number): Kit {
  // A bench, a seat behind it per operator, a light at each place.
  const shown = Math.min(n, MAX_UNITS);
  const { z, pitch } = spread(shown, 0.5, 2.2);
  const length = shown * pitch + 0.1;
  const parts: LocalPart[] = [box("body", -0.05, 0.56, 0, 0.62, 0.05, length)];
  for (const sx of [-1, 1]) for (const sz of [-1, 1]) parts.push(box("trim", -0.05 + sx * 0.27, 0, sz * (length / 2 - 0.04), 0.04, 0.56, 0.04));
  const lamps: Vec3[] = [];
  const units: LocalGrid[] = [];
  for (const zi of z) {
    parts.push(cyl("trim", 0.5, 0, zi, 0.05, 0.34), cyl("detail", 0.5, 0.34, zi, 0.24, 0.05));
    lamps.push(pole(parts, 0.19, zi, 0.61, 0.9));
    units.push(grid(-0.12, 0.61, zi, 1, 1, 4));
  }
  return { parts, half: [0.65, length / 2], height: top(lamps, 0.85), wait: before(-0.75), port: after(0.75), units, cells: [], lamps, shared: false };
}

function table(n: number): Kit {
  const shown = Math.min(n, MAX_UNITS);
  const { z, pitch } = spread(shown, 0.6, 2.2);
  const length = shown * pitch + 0.1;
  const parts: LocalPart[] = [box("body", 0, 0.66, 0, 0.86, 0.05, length), box("trim", 0, 0.18, 0, 0.78, 0.03, length - 0.1)];
  for (const sx of [-1, 1]) for (const sz of [-1, 1]) parts.push(box("trim", sx * 0.39, 0, sz * (length / 2 - 0.04), 0.04, 0.66, 0.04));
  const lamps: Vec3[] = [];
  const units: LocalGrid[] = [];
  for (const zi of z) {
    lamps.push(pole(parts, 0.34, zi, 0.71, 0.98));
    units.push(grid(-0.08, 0.71, zi, 1, 1, 4));
  }
  return { parts, half: [0.45, length / 2], height: top(lamps, 0.95), wait: before(-0.7), port: after(0.6), units, cells: [], lamps, shared: false };
}

/** The kit of an equipment kind with `units` units of capacity (a queue's capacity, or null, is not drawn). */
export function kit(kind: EquipmentKind, units: number | null): Kit {
  const n = Math.max(1, Math.floor(units ?? 1));
  switch (kind) {
    case "smt":
      return smt(n);
    case "aoi":
      return aoi(n);
    case "tester":
      return machine(n, "tester");
    case "machine":
      return machine(n, "machine");
    case "oven":
      return oven(n);
    case "bench":
      return bench(n);
    case "table":
      return table(n);
    case "buffer":
      return buffer();
    case "conveyor":
      return conveyor();
    case "return":
      return returnLane();
    case "dock":
      return dock(false);
    case "shipping":
      return dock(true);
    case "junction":
      return junction();
    case "reject":
      return reject();
  }
}
