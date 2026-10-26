// T-045 / H-027 ("簡潔"): the simplicity rules of DESIGN.md that a source
// scan can check. Every stylesheet but ui/tokens.css takes its colours,
// type sizes, spacing, corners, shadows and motion from the tokens; nothing
// is decorated (no gradient, no keyframe animation, no shadow on anything
// that does not float, no border thicker than a hairline, so no side
// stripe); and no component writes a colour inline. The rules that need a
// rendered page (one primary action per view, no duplicate controls,
// visible focus) are checked in tests/simplicity.test.tsx and in the
// browser (apps/web-e2e/tests/simplicity.spec.ts).
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { describe, expect, it } from "vitest";

const root = process.cwd();

function files(dir: string, ext: RegExp): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) return name === "node_modules" || name.startsWith(".") ? [] : files(p, ext);
    return ext.test(name) ? [p] : [];
  });
}

const tokensFile = join(root, "ui/tokens.css");
const sheets = [...files(join(root, "ui"), /\.css$/), ...files(join(root, "app"), /\.css$/)].filter((f) => f !== tokensFile);
const sources = [...files(join(root, "ui"), /\.tsx?$/), ...files(join(root, "app"), /\.tsx?$/)];
const defined = new Set([...readFileSync(tokensFile, "utf8").matchAll(/(--gm-[\w-]+)\s*:/g)].map((m) => m[1]!));

type Decl = { file: string; prop: string; value: string };
/** Every declaration in a stylesheet, comments removed. */
function declarations(file: string): Decl[] {
  const text = readFileSync(file, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
  const out: Decl[] = [];
  for (const body of text.matchAll(/\{([^{}]*)\}/g)) {
    for (const d of body[1]!.split(";")) {
      const i = d.indexOf(":");
      if (i > 0) out.push({ file: relative(root, file), prop: d.slice(0, i).trim(), value: d.slice(i + 1).trim() });
    }
  }
  return out;
}
const all = sheets.flatMap(declarations);
const where = (d: Decl) => `${d.file}: ${d.prop}: ${d.value}`;

/** What is left of a value once the allowed forms are taken out. */
function rest(value: string, allowed: RegExp[]): string {
  let v = value;
  for (const re of allowed) v = v.replace(re, " ");
  return v.replace(/[\s,()]+/g, " ").trim();
}

const tokenRef = (prefix: string) => new RegExp(`var\\(--gm-${prefix}[\\w-]*\\)`, "g");

describe("simplicity rules (H-027) in the stylesheets", () => {
  it("has stylesheets to check", () => {
    expect(sheets.length).toBeGreaterThanOrEqual(10);
    expect(all.length).toBeGreaterThan(150);
  });

  it("uses only defined tokens", () => {
    const used = [...sheets, ...sources].flatMap((f) => [...readFileSync(f, "utf8").matchAll(/var\((--gm-[\w-]+)/g)].map((m) => `${relative(root, f)}: ${m[1]}`));
    expect(used.length).toBeGreaterThan(100);
    expect(used.filter((u) => !defined.has(u.slice(u.indexOf(": ") + 2)))).toEqual([]);
  });

  it("names no colour outside the tokens (no hex, colour function or colour keyword)", () => {
    const literal = /#[0-9a-f]{3,8}\b|\b(rgba?|hsla?|hwb|lab|lch|oklab|oklch|color|color-mix)\(/i;
    expect(all.filter((d) => literal.test(d.value)).map(where)).toEqual([]);
    // In properties that take a colour, every word must be a token or a non-colour keyword.
    const colourProps = /^(color|background(-color)?|border(-(top|right|bottom|left|inline|block)(-(start|end))?)?(-color)?|outline(-color)?|fill|stroke|caret-color|accent-color|scrollbar-color|text-decoration(-color)?|box-shadow)$/;
    const keywords = /\b(transparent|currentcolor|inherit|none|solid|inset|underline|auto|0)\b/gi;
    const offenders = all
      .filter((d) => colourProps.test(d.prop))
      .filter((d) => rest(d.value, [/var\(--gm-[\w-]+\)/g, /calc\([^)]*\)/g, /-?[\d.]+(px|em|%)?/g, keywords]) !== "");
    expect(offenders.map(where)).toEqual([]);
  });

  it("has no gradient and no animation; transitions are the motion token", () => {
    // (The reduced-motion rule in ui/base.css may shorten durations; it starts nothing.)
    expect(all.filter((d) => /gradient\(/.test(d.value) || /^animation(-name)?$/.test(d.prop)).map(where)).toEqual([]);
    for (const f of sheets) expect(readFileSync(f, "utf8"), f).not.toMatch(/@keyframes/);
    const transitions = all.filter((d) => d.prop === "transition");
    expect(transitions.length).toBeGreaterThan(0);
    for (const t of transitions) expect(t.value.split(",").every((p) => /^\s*[a-z-]+ var\(--gm-motion\)\s*$/.test(p)), where(t)).toBe(true);
  });

  it("casts a shadow only from what floats over the work, besides the invalid-field ring", () => {
    const shadows = all.filter((d) => d.prop === "box-shadow");
    expect(shadows.map((d) => `${d.file}: ${d.value}`).sort()).toEqual([
      "ui/dialog.module.css: var(--gm-shadow-overlay)",
      "ui/field.module.css: inset 0 0 0 1px var(--gm-color-error)",
      "ui/select.module.css: var(--gm-shadow-overlay)",
    ]);
  });

  it("draws borders as 1 px hairlines: no thicker border, so no side stripe", () => {
    const borders = all.filter((d) => /^border(-(top|right|bottom|left|inline|block)(-(start|end))?)?(-width)?$/.test(d.prop));
    expect(borders.length).toBeGreaterThan(15);
    const thick = borders.filter((d) => [...d.value.matchAll(/([\d.]+)px/g)].some((m) => Number(m[1]) > 1));
    expect(thick.map(where)).toEqual([]);
  });

  it("takes type sizes from the five tokens, and corners from the scale", () => {
    const sizes = all.filter((d) => d.prop === "font-size" || d.prop === "font");
    expect(sizes.filter((d) => !/^(var\(--gm-font-size-[\w-]+\)|inherit)$/.test(d.value)).map(where)).toEqual([]);
    const radii = all.filter((d) => /radius/.test(d.prop));
    expect(radii.filter((d) => !/^(var\(--gm-radius-(sm|md|lg)\)|0)$/.test(d.value)).map(where)).toEqual([]);
  });

  it("takes every margin, padding, gap and offset from the spacing scale", () => {
    const spacing = all.filter((d) => /^(margin|padding)(-(top|right|bottom|left|inline|block)(-(start|end))?)?$|^(row-|column-)?gap$|^(top|right|bottom|left|inset)$/.test(d.prop));
    expect(spacing.length).toBeGreaterThan(40);
    const allowed = [
      /max\(var\(--gm-space-[\w-]+\), env\(safe-area-inset-[a-z]+, 0px\)\)/g,
      /calc\(-1 \* var\(--gm-[\w-]+\)\)/g,
      tokenRef("space-"),
      /var\(--gm-control-padding-x\)/g,
      /\b(0|auto|12vh)\b/g,
    ];
    expect(spacing.filter((d) => rest(d.value, allowed) !== "").map(where)).toEqual([]);
  });

  it("writes no colour in a component's inline style or markup", () => {
    const literal = /#[0-9a-f]{6}\b|#[0-9a-f]{3}\b|\brgba?\(|\bhsla?\(/i;
    expect(sources.filter((f) => literal.test(readFileSync(f, "utf8").replace(/\/\/.*$/gm, ""))).map((f) => relative(root, f))).toEqual([]);
  });
});
