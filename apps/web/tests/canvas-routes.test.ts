// The owner's canvas-layout decision of 2026-11-02: on the 2D canvas of a model
// made from any template, no link runs behind a card, and no decision's
// probability label sits on a card, a handle or another label.
//
// Each link's route is the one the canvas draws: React Flow's smoothstep
// path (getSmoothStepPath, as FlowEdge in features/model-editor/
// trace-overlay.tsx calls it), from the source handle on a card's right to
// the target handle on the next card's left, flattened as the trace overlay
// flattens it (pathPoints), at the positions the canvas uses (canvasLayout).
// The card and its handles are measured from the canvas's own stylesheet
// and tokens (canvas.module.css, ui/tokens.css), so a change to the card's
// size changes this test's geometry, or fails it where it cannot follow.
//
// A label is drawn above every link (React Flow's edge-label layer) on a
// backing in the canvas's ground, so a link under a label never crosses its
// text; what this test refuses is a label on a card, a handle or another
// label. Its box is the text's width at an upper bound of Geist's advances
// at 12 px (a tabular digit is 7 px, "." 2 px and "%" 10 px in the browser;
// apps/web-e2e/tests/canvas-labels.spec.ts checks the real boxes stay
// inside this bound), plus its padding, at the label's line height.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { getSmoothStepPath, Position } from "@xyflow/react";
import { describe, expect, it } from "vitest";
import { canvasLayout } from "../features/model-editor/layout";
import { pathPoints, type Point } from "../features/model-editor/trace-geometry";
import type { ModelIR } from "../lib/contracts";

const web = process.cwd();
const packs = join(web, "../../internal/domainpack");

// --- The card, its handles and a label, from the stylesheet --------------

const tokens = new Map([...readFileSync(join(web, "ui/tokens.css"), "utf8").split(/@media|:root\[/)[0]!.matchAll(/(--gm-[\w-]+)\s*:\s*([^;]+);/g)].map((m) => [m[1]!, m[2]!.trim()]));
const sheet = readFileSync(join(web, "features/model-editor/canvas.module.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
/** The declarations of a rule whose selector is exactly `selector`. */
function rule(selector: string): Record<string, string> {
  const m = sheet.match(new RegExp(`(?:^|\\})\\s*${selector.replace(/[.[\]()]/g, "\\$&")}\\s*\\{([^}]*)\\}`));
  if (!m) throw new Error(`canvas.module.css has no rule ${selector}`);
  return Object.fromEntries(m[1]!.split(";").map((d) => d.split(/:(.*)/s).map((s) => s.trim())).filter((d) => d[0] !== "").map(([k, v]) => [k, v]));
}
/** A length in px: "12px", or a token that is one. */
function px(value: string): number {
  const v = value.replace(/var\((--gm-[\w-]+)\)/, (_, t: string) => tokens.get(t) ?? "?");
  if (!/^-?[\d.]+px$/.test(v)) throw new Error(`not a length in px: ${value}`);
  return Number.parseFloat(v);
}
/** A "padding" with one to four values, as top, right, bottom, left. */
function padding(value: string): [number, number, number, number] {
  const p = value.split(/\s+/).map((v) => (v === "0" ? 0 : px(v)));
  const [t, r = t, b = t, l = r] = p as [number, number?, number?, number?];
  return [t, r, b, l];
}

const node = rule(".node");
const name = rule(".name");
const kind = rule(".kind");
const handle = rule(".handle");
const label = rule(".edgeLabel");

/** The card: its width, and its height from its two lines of text, their padding and its 1 px border. */
const CARD_W = px(node.width!);
const BORDER = px(node.border!.split(/\s+/)[0]!);
const CARD_H = Math.round(1000 * (
  2 * BORDER +
  padding(name.padding!)[0] + px(name["line-height"]!) + padding(name.padding!)[2] +
  padding(kind.padding!)[0] + px(kind["font-size"]!) * Number(tokens.get("--gm-line-height-label")) + padding(kind.padding!)[2])) / 1000;
/** A handle is a square centred on the card's edge (React Flow's base.css: translate(±50%, -50%)). */
const HANDLE = px(handle.width!);
/** A label: its line box and its padding. */
const LABEL_H = Math.round(1000 * px(label["font-size"]!) * Number(tokens.get("--gm-line-height-label"))) / 1000;
const LABEL_PAD = padding(label.padding!);
/** Upper bounds of Geist's advance at 12 px, per character of a percentage. */
const advance = (c: string) => (/\d/.test(c) ? 8 : c === "%" ? 12 : c === "." || c === "," ? 4 : 12);
/** Room every label keeps from a card, a handle and another label, in canvas units (DESIGN.md spacing, hair). */
const CLEAR = px("var(--gm-space-hair)");

type Box = { x0: number; y0: number; x1: number; y1: number };
const grow = (b: Box, m: number): Box => ({ x0: b.x0 - m, y0: b.y0 - m, x1: b.x1 + m, y1: b.y1 + m });
const overlaps = (a: Box, b: Box) => a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1;
const inside = (p: Point, b: Box) => p.x >= b.x0 - 1e-6 && p.x <= b.x1 + 1e-6 && p.y >= b.y0 - 1e-6 && p.y <= b.y1 + 1e-6;

/** The part of segment a–b inside box (Liang–Barsky), or null. */
function clip(a: Point, b: Point, box: Box): [Point, Point] | null {
  let t0 = 0;
  let t1 = 1;
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  for (const [p, q] of [[-dx, a.x - box.x0], [dx, box.x1 - a.x], [-dy, a.y - box.y0], [dy, box.y1 - a.y]] as const) {
    if (p === 0) {
      if (q < 0) return null;
      continue;
    }
    const t = q / p;
    if (p < 0) {
      if (t > t1) return null;
      t0 = Math.max(t0, t);
    } else {
      if (t < t0) return null;
      t1 = Math.min(t1, t);
    }
  }
  return [{ x: a.x + dx * t0, y: a.y + dy * t0 }, { x: a.x + dx * t1, y: a.y + dy * t1 }];
}

type Card = { id: string; body: Box; source: Box | null; target: Box | null };

/** What the canvas draws for ir: every card with its handles, every link's route, and each decision's label. */
function drawn(ir: ModelIR, format: (p: number) => string) {
  const positions = canvasLayout(ir);
  const cards = new Map<string, Card>();
  for (const n of ir.nodes) {
    const { x, y } = positions[n.id]!;
    const mid = y + CARD_H / 2;
    const square = (cx: number): Box => ({ x0: cx - HANDLE / 2, x1: cx + HANDLE / 2, y0: mid - HANDLE / 2, y1: mid + HANDLE / 2 });
    cards.set(n.id, {
      id: n.id,
      body: { x0: x, x1: x + CARD_W, y0: y, y1: y + CARD_H },
      // canvas.tsx StepNodeView: no target handle on a source, no source handle on a sink.
      target: n.type === "source" ? null : square(x),
      source: n.type === "sink" ? null : square(x + CARD_W),
    });
  }
  const types = new Map(ir.nodes.map((n) => [n.id, n.type]));
  const links = ir.flows.map((f) => {
    const from = cards.get(f.from)!.source!;
    const to = cards.get(f.to)!.target!;
    // React Flow's getHandlePosition: a right handle's right edge, a left handle's left edge, at its middle.
    const [path, labelX, labelY] = getSmoothStepPath({
      sourceX: from.x1,
      sourceY: (from.y0 + from.y1) / 2,
      sourcePosition: Position.Right,
      targetX: to.x0,
      targetY: (to.y0 + to.y1) / 2,
      targetPosition: Position.Left,
    });
    let box: Box | null = null;
    let text = "";
    if (types.get(f.from) === "decision") {
      text = format(f.routing.probability);
      const w = [...text].reduce((s, c) => s + advance(c), 0) + LABEL_PAD[1] + LABEL_PAD[3];
      const h = LABEL_H + LABEL_PAD[0] + LABEL_PAD[2];
      box = { x0: labelX - w / 2, x1: labelX + w / 2, y0: labelY - h / 2, y1: labelY + h / 2 };
    }
    return { flow: f, route: pathPoints(path), label: box, text };
  });
  return { cards: [...cards.values()], links };
}

/** Every way a drawing breaks the rule, in words. */
function faults(ir: ModelIR, format: (p: number) => string): string[] {
  const { cards, links } = drawn(ir, format);
  const out: string[] = [];
  for (const { flow, route } of links) {
    for (const c of cards) {
      // A card is its body and its handles; a link may touch only its own two handles.
      const footprint = grow({ x0: c.body.x0 - HANDLE / 2, x1: c.body.x1 + HANDLE / 2, y0: c.body.y0, y1: c.body.y1 }, CLEAR);
      const own = [c.id === flow.from ? c.source : null, c.id === flow.to ? c.target : null].filter((b): b is Box => b !== null).map((b) => grow(b, CLEAR));
      const hit = route.slice(1).some((b, i) => {
        const part = clip(route[i]!, b, footprint);
        return part !== null && !own.some((o) => inside(part[0], o) && inside(part[1], o));
      });
      if (hit) out.push(`link ${flow.id} (${flow.from} → ${flow.to}) runs behind ${c.id}`);
    }
  }
  const labelled = links.filter((l) => l.label !== null);
  for (const l of labelled) {
    const box = grow(l.label!, CLEAR);
    for (const c of cards) {
      if (overlaps(box, c.body)) out.push(`label ${l.text} of ${l.flow.id} is on ${c.id}`);
      for (const h of [c.target, c.source]) if (h !== null && overlaps(box, h)) out.push(`label ${l.text} of ${l.flow.id} is on a handle of ${c.id}`);
    }
    for (const o of labelled) if (o !== l && overlaps(box, o.label!)) out.push(`label ${l.text} of ${l.flow.id} is on the label of ${o.flow.id}`);
  }
  return out;
}

/** Every template a model can start from: each pack's templates/*.json but the names files. */
const templates = readdirSync(packs, { withFileTypes: true })
  .filter((d) => d.isDirectory())
  .flatMap((d) => {
    const dir = join(packs, d.name, "templates");
    let files: string[] = [];
    try {
      files = readdirSync(dir);
    } catch {
      return [];
    }
    return files.filter((f) => f.endsWith(".json") && !f.includes(".names.")).map((f) => ({ name: `${d.name}/${f}`, ir: JSON.parse(readFileSync(join(dir, f), "utf8")) as ModelIR }));
  });

/** The canvas writes a probability as canvas.tsx does (number, percent, at most two decimals), in both catalogs' languages. */
const formats = ["en", "zh-Hant"].map((locale) => {
  const f = new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 2 });
  return (p: number) => f.format(p);
});

describe("the canvas's geometry, as its stylesheet draws it", () => {
  it("is a 168 × 64.8 card with 8 px handles centred on its sides, and a 16.8 px label padded 4 px each side", () => {
    expect([CARD_W, CARD_H, HANDLE]).toEqual([168, 64.8, 8]);
    expect(handle.height).toBe(handle.width);
    expect([LABEL_H, ...LABEL_PAD]).toEqual([16.8, 0, 4, 0, 4]);
  });

  it("puts a link's ends where React Flow does: the source handle's right edge, the target handle's left edge", () => {
    const ir = { ...templates[0]!.ir };
    const { cards, links } = drawn(ir, formats[0]!);
    const l = links[0]!;
    const from = cards.find((c) => c.id === l.flow.from)!;
    const to = cards.find((c) => c.id === l.flow.to)!;
    expect(l.route[0]).toEqual({ x: from.body.x1 + HANDLE / 2, y: from.body.y0 + CARD_H / 2 });
    expect(l.route.at(-1)).toEqual({ x: to.body.x0 - HANDLE / 2, y: to.body.y0 + CARD_H / 2 });
  });

  it("finds a link back along its own row, a forward link behind a card and a label on a handle", () => {
    // Three steps on one row, 200 apart (the delivery template before 2026-11-02): every fault the rule names.
    const flow = (id: string, from: string, to: string, probability = 1) => ({ id, from, to, routing: { type: "probability" as const, probability } });
    const ir = {
      schema_version: "0.1",
      model_id: "mdl_01M4YDQK00TEST0000000000LT",
      domain: "software_delivery",
      name: "row",
      time_unit: "minute",
      entity_types: [],
      resources: [],
      nodes: [
        { id: "a", type: "decision" },
        { id: "b", type: "process" },
        { id: "c", type: "process" },
      ],
      flows: [flow("ab", "a", "b", 0.2), flow("ac", "a", "c", 0.8), flow("cb", "c", "b")],
      views: { "2d": { positions: { a: { x: 0, y: 0 }, b: { x: 200, y: 0 }, c: { x: 400, y: 0 } } }, "3d": { layout_mode: "derived_from_2d" } },
    } as unknown as ModelIR;
    const found = faults(ir, formats[0]!);
    expect(found).toEqual(expect.arrayContaining([
      "link ac (a → c) runs behind b",
      "link cb (c → b) runs behind c",
      "label 20% of ab is on a handle of a",
      "label 20% of ab is on a handle of b",
    ]));
    // Moved apart on two rows at the default spacing, the same three are clean.
    const apart = { ...ir, views: { ...ir.views, "2d": { positions: { a: { x: 0, y: 0 }, c: { x: 240, y: 0 }, b: { x: 240, y: 120 } } } } } as ModelIR;
    expect(faults(apart, formats[0]!)).toEqual([]);
  });
});

describe("every template's canvas (the canvas-layout decision of 2026-11-02)", () => {
  it("has templates to check", () => {
    expect(templates.map((t) => t.name).sort()).toEqual([
      "electronicsline/electronics_assembly_test_line.json",
      "electronicsline/photo_start.json",
      "productionlogistics/carton_plant.json",
      "productionlogistics/food_plant.json",
      "productionlogistics/shoe_factory.json",
      "productionlogistics/warehouse.json",
      "softwaredelivery/default_delivery_pipeline.json",
    ]);
  });

  for (const { name, ir } of templates) {
    it(`${name}: no link runs behind a card, and no label is on a card, a handle or another label`, () => {
      for (const format of formats) expect(faults(ir, format)).toEqual([]);
    });
  }
});
