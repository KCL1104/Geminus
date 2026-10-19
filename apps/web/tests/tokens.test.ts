// T-045 / REQ-023: the design tokens (ui/tokens.css) meet WCAG 2.2 AA
// contrast for every pair components use, the theme is light only with
// semantic tokens a dark theme can redefine (H-008 item 2), fonts are system
// fonts only (H-008 item 3), and compact density keeps 24 px targets.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const root = process.cwd();
const css = readFileSync(join(root, "ui/tokens.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

/** The declarations of the first block whose selector is exactly sel. */
function block(sel: string): Map<string, string> {
  const at = css.indexOf(`${sel} {`);
  if (at < 0) throw new Error(`no ${sel} block`);
  const body = css.slice(css.indexOf("{", at) + 1, css.indexOf("}", at));
  const out = new Map<string, string>();
  for (const decl of body.split(";")) {
    const i = decl.indexOf(":");
    if (i > 0) out.set(decl.slice(0, i).trim(), decl.slice(i + 1).trim());
  }
  return out;
}

const tokens = block(":root");

function resolve(name: string, seen: string[] = []): string {
  const v = tokens.get(name);
  if (v === undefined) throw new Error(`token ${name} is not defined`);
  if (seen.includes(name)) throw new Error(`token cycle at ${name}`);
  return v.replace(/var\((--[\w-]+)\)/g, (_, ref: string) => resolve(ref, [...seen, name]));
}

function luminance(hex: string): number {
  const m = /^#([0-9a-f]{6})$/i.exec(hex);
  if (!m) throw new Error(`not a #rrggbb colour: ${hex}`);
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(m[1]!.slice(i, i + 2), 16) / 255).map((c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!;
}

function contrast(fg: string, bg: string): number {
  const [a, b] = [luminance(resolve(fg)), luminance(resolve(bg))].sort((x, y) => y - x);
  return (a! + 0.05) / (b! + 0.05);
}

// Text (WCAG 1.4.3, 4.5:1) and the backgrounds each sits on in components.
const textPairs: [string, string][] = [
  ["--gm-color-text", "--gm-color-bg"],
  ["--gm-color-text", "--gm-color-surface"],
  ["--gm-color-text", "--gm-color-surface-hover"],
  ["--gm-color-text", "--gm-color-selected"],
  ["--gm-color-text-muted", "--gm-color-bg"],
  ["--gm-color-text-muted", "--gm-color-surface"],
  ["--gm-color-accent", "--gm-color-bg"],
  ["--gm-color-accent", "--gm-color-surface"],
  ["--gm-color-accent-hover", "--gm-color-bg"],
  ["--gm-color-on-accent", "--gm-color-accent"],
  ["--gm-color-on-accent", "--gm-color-accent-hover"],
  ["--gm-color-on-danger", "--gm-color-danger"],
  ["--gm-color-danger", "--gm-color-bg"],
  ["--gm-color-danger", "--gm-color-danger-bg"],
  ["--gm-color-warning", "--gm-color-warning-bg"],
  ["--gm-color-success", "--gm-color-success-bg"],
  ["--gm-color-info", "--gm-color-info-bg"],
];
// Control borders and the focus ring (WCAG 1.4.11, 3:1).
const nonTextPairs: [string, string][] = [
  ["--gm-color-border-strong", "--gm-color-bg"],
  ["--gm-color-border-strong", "--gm-color-surface"],
  ["--gm-color-focus", "--gm-color-bg"],
  ["--gm-color-focus", "--gm-color-surface"],
  ["--gm-color-focus", "--gm-color-selected"],
  ["--gm-color-accent", "--gm-color-surface-hover"],
];

function cssFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) return name === "node_modules" || name.startsWith(".") ? [] : cssFiles(p);
    return name.endsWith(".css") ? [p] : [];
  });
}
const ownCss = [...cssFiles(join(root, "ui")), ...cssFiles(join(root, "app"))];

describe("design tokens", () => {
  it.each(textPairs)("%s on %s is at least 4.5:1", (fg, bg) => {
    expect(contrast(fg, bg)).toBeGreaterThanOrEqual(4.5);
  });

  it.each(nonTextPairs)("%s against %s is at least 3:1", (fg, bg) => {
    expect(contrast(fg, bg)).toBeGreaterThanOrEqual(3);
  });

  it("is a light theme only, with semantic tokens over a palette", () => {
    expect(tokens.get("color-scheme")).toBe("light");
    expect(css).not.toMatch(/prefers-color-scheme|data-theme/);
    for (const [name, value] of tokens) {
      if (name.startsWith("--gm-color-")) expect(value, name).toMatch(/^var\(--gm-palette-[\w-]+\)$/);
    }
  });

  it("is used through semantic tokens: only tokens.css names the palette", () => {
    const offenders = ownCss.filter((f) => !f.endsWith("ui/tokens.css") && /--gm-palette-/.test(readFileSync(f, "utf8")));
    expect(offenders).toEqual([]);
  });

  it("uses system fonts only, with Traditional Chinese fallbacks", () => {
    for (const f of ownCss) expect(readFileSync(f, "utf8"), f).not.toMatch(/@font-face|@import\s+url|fonts\.googleapis/);
    expect(resolve("--gm-font-sans")).toMatch(/^system-ui,/);
    expect(resolve("--gm-font-sans")).toMatch(/"PingFang TC".*"Microsoft JhengHei"/);
  });

  it("keeps targets at least 24 px in compact density, and density applies from <html>", () => {
    const compact = block(':root[data-density="compact"]');
    for (const name of ["--gm-control-height", "--gm-row-height"]) {
      expect(parseInt(resolve(name), 10), name).toBeGreaterThanOrEqual(32);
      expect(parseInt(compact.get(name) ?? "", 10), `compact ${name}`).toBeGreaterThanOrEqual(24);
    }
  });

  it("has a visible focus ring", () => {
    expect(parseInt(resolve("--gm-focus-width"), 10)).toBeGreaterThanOrEqual(2);
    expect(resolve("--gm-focus-ring")).toMatch(/solid #/);
  });
});
