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

// Go's math.Round rounds half away from zero; Math.round rounds half up.
function roundHalfAway(v: number): number {
  return Math.sign(v) * Math.round(Math.abs(v));
}
