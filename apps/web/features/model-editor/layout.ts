// The layered default view (TWIN_IR_SPEC §7: "positions 缺省時
// DomainPack.DefaultViews 生成"), the same rule as the server's
// internal/domainpack.LayeredLayout, so a node without a position appears
// where the server would put it: kept positions stay; any other node goes
// to its breadth-first layer from the sources (x) and the first row of that
// layer nothing occupies (y); nodes no source reaches go one layer past the
// deepest. Order is document order (no map order), so it is deterministic.
import type { ModelIR } from "../../lib/contracts";

export const LAYER_SPACING = 240;
export const ROW_SPACING = 120;

export type Position = { x: number; y: number };

/** A position for every node of ir. */
export function layeredLayout(ir: ModelIR): Record<string, Position> {
  const index = new Map<string, number>();
  ir.nodes.forEach((n, i) => {
    if (!index.has(n.id)) index.set(n.id, i);
  });
  const out: number[][] = ir.nodes.map(() => []);
  for (const f of ir.flows) {
    const from = index.get(f.from);
    const to = index.get(f.to);
    if (from !== undefined && to !== undefined) out[from]!.push(to);
  }
  const layer = ir.nodes.map(() => -1);
  const queue: number[] = [];
  ir.nodes.forEach((n, i) => {
    if (n.type === "source") {
      layer[i] = 0;
      queue.push(i);
    }
  });
  let deepest = 0;
  for (let head = 0; head < queue.length; head++) {
    const i = queue[head]!;
    for (const j of out[i]!) {
      if (layer[j]! < 0) {
        layer[j] = layer[i]! + 1;
        deepest = Math.max(deepest, layer[j]!);
        queue.push(j);
      }
    }
  }
  const cell = (l: number, row: number) => `${l}:${row}`;
  const taken = new Set<string>();
  const positions: Record<string, Position> = {};
  const existing = ir.views["2d"].positions ?? {};
  ir.nodes.forEach((n, i) => {
    const p = Object.hasOwn(existing, n.id) ? existing[n.id] : undefined;
    if (p !== undefined && index.get(n.id) === i) {
      positions[n.id] = p;
      if (Number.isFinite(p.x) && Number.isFinite(p.y)) taken.add(cell(roundHalfAway(p.x / LAYER_SPACING), roundHalfAway(p.y / ROW_SPACING)));
    }
  });
  ir.nodes.forEach((n, i) => {
    if (Object.hasOwn(positions, n.id) || index.get(n.id) !== i) return;
    const l = layer[i]! < 0 ? deepest + 1 : layer[i]!;
    let row = 0;
    while (taken.has(cell(l, row))) row++;
    taken.add(cell(l, row));
    positions[n.id] = { x: l * LAYER_SPACING, y: row * ROW_SPACING };
  });
  return positions;
}

/**
 * The canvas's positions (the 2D editor, and the 3D view that derives from
 * 2D): the layered layout of ir, except when the IR places some steps and
 * not others and the placed ones are exactly the layered default of the
 * line before those steps were added. Steps without a position come from an
 * AI edit (D-022: it cannot write the view); positions that are the default
 * were placed by no one, so the whole line is laid out again from its flows,
 * and the first edit writes that down (edits.ts materialize). Positions
 * someone placed (a template's, a drag's, a key's) are always kept.
 */
export function canvasLayout(ir: ModelIR): Record<string, Position> {
  const saved = ir.views["2d"].positions;
  if (saved === undefined || ir.nodes.every((n) => Object.hasOwn(saved, n.id)) || !defaultBefore(ir, saved)) return layeredLayout(ir);
  return layeredLayout({ ...ir, views: { ...ir.views, "2d": { ...ir.views["2d"], positions: {} } } });
}

/**
 * Whether saved is the layered default of the line before its unplaced
 * steps: the placed steps alone, each joined to the placed steps it reaches
 * directly or through unplaced ones only.
 */
function defaultBefore(ir: ModelIR, saved: Record<string, Position>): boolean {
  const placed = new Set(ir.nodes.filter((n) => Object.hasOwn(saved, n.id)).map((n) => n.id));
  const next = new Map<string, string[]>();
  for (const f of ir.flows) next.set(f.from, [...(next.get(f.from) ?? []), f.to]);
  const flows: ModelIR["flows"] = [];
  for (const from of placed) {
    const seen = new Set<string>();
    const stack = [...(next.get(from) ?? [])];
    while (stack.length > 0) {
      const id = stack.pop()!;
      if (seen.has(id)) continue;
      seen.add(id);
      if (placed.has(id)) flows.push({ id: `${from}>${id}`, from, to: id } as ModelIR["flows"][number]); // layeredLayout reads only from and to
      else stack.push(...(next.get(id) ?? []));
    }
  }
  const before = layeredLayout({ ...ir, nodes: ir.nodes.filter((n) => placed.has(n.id)), flows, views: { ...ir.views, "2d": { ...ir.views["2d"], positions: {} } } });
  return [...placed].every((id) => before[id]!.x === saved[id]!.x && before[id]!.y === saved[id]!.y);
}

// Go's math.Round rounds half away from zero; Math.round rounds half up.
function roundHalfAway(v: number): number {
  return Math.sign(v) * Math.round(Math.abs(v));
}
