// T-045 / REQ-023 / H-027 / H-041: the design tokens (ui/tokens.css) are
// DESIGN.md's, value for value, in the light and the near-black dark theme;
// every colour pair components use meets WCAG 2.2 AA in both; the palette
// is the neutrals, the accent (the AI, selection and focus), the scenario
// green and the warning and error signals only; type, spacing and corners
// are DESIGN.md's scales; the fonts are self-hosted OFL faces with real
// fallbacks; compact density keeps 24 px.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const root = process.cwd();
const uncomment = (s: string) => s.replace(/\/\*[\s\S]*?\*\//g, "");
const css = uncomment(readFileSync(join(root, "ui/tokens.css"), "utf8"));

/** The declarations of the block whose selector is exactly sel. */
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

const light = block(":root");
const darkBySystem = block(':root:not([data-theme="light"])');
const darkByChoice = block(':root[data-theme="dark"]');
const themes = { light, dark: new Map([...light, ...darkByChoice]) };
type ThemeName = keyof typeof themes;

function resolve(name: string, theme: ThemeName = "light", seen: string[] = []): string {
  const v = themes[theme].get(name);
  if (v === undefined) throw new Error(`token ${name} is not defined`);
  if (seen.includes(name)) throw new Error(`token cycle at ${name}`);
  return v.replace(/var\((--[\w-]+)\)/g, (_, ref: string) => resolve(ref, theme, [...seen, name]));
}

// DESIGN.md's front matter: the source of truth for the values.
const design = readFileSync(join(root, "../../DESIGN.md"), "utf8").split("\n---\n", 1)[0]!;
function section(name: string): string {
  const m = new RegExp(`\\n${name}:\\n((?:  .*\\n)+)`).exec(design + "\n");
  if (!m) throw new Error(`DESIGN.md has no ${name}`);
  return m[1]!;
}
const designColours = new Map([...section("colors").matchAll(/^ {2}([\w-]+): "([^"]+)"$/gm)].map((m) => [m[1]!, m[2]!]));
const designSpacing = new Map([...section("spacing").matchAll(/^ {2}([\w-]+): "([^"]+)"$/gm)].map((m) => [m[1]!, m[2]!]));
const designRadii = new Map([...section("rounded").matchAll(/^ {2}([\w-]+): "([^"]+)"$/gm)].map((m) => [m[1]!, m[2]!]));
const designSizes = new Set([...section("typography").matchAll(/fontSize: "([\d.]+)px"/g)].map((m) => Number(m[1])));
const colourKeys = [...designColours.keys()].filter((k) => !k.endsWith("-dark"));
const norm = (v: string) => v.toLowerCase().replace(/\s+/g, "");

/** [r, g, b, alpha] of #rrggbb or rgba(). */
function rgba(value: string): [number, number, number, number] {
  const hex = /^#([0-9a-f]{6})$/i.exec(value);
  if (hex) return [0, 2, 4].map((i) => parseInt(hex[1]!.slice(i, i + 2), 16)).concat(1) as [number, number, number, number];
  const fn = /^rgba?\(([\d.]+),([\d.]+),([\d.]+)(?:,([\d.]+))?\)$/.exec(norm(value));
  if (fn) return [Number(fn[1]), Number(fn[2]), Number(fn[3]), fn[4] === undefined ? 1 : Number(fn[4])];
  throw new Error(`not a colour: ${value}`);
}

const linear = (c: number) => (c / 255 <= 0.04045 ? c / 255 / 12.92 : ((c / 255 + 0.055) / 1.055) ** 2.4);

function luminance(value: string): number {
  const [r, g, b, a] = rgba(value);
  if (a !== 1) throw new Error(`contrast of a translucent colour: ${value}`);
  return 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
}

function contrast(fg: string, bg: string, theme: ThemeName): number {
  const [a, b] = [luminance(resolve(fg, theme)), luminance(resolve(bg, theme))].sort((x, y) => y - x);
  return (a! + 0.05) / (b! + 0.05);
}

/** OKLCH chroma (Björn Ottosson's OKLab), to tell a neutral from a hue. */
function chroma(value: string): number {
  const [r, g, b] = rgba(value).slice(0, 3).map(linear) as [number, number, number];
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  const A = 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s;
  const B = 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s;
  return Math.hypot(A, B);
}

// The only hues (H-027 rule 3, H-041; DESIGN.md Colors): the accent (the AI,
// selection, focus), the scenario green (also a passed check) and the
// warning and error signals. Everything else is a neutral.
const hueFamilies = /^--gm-color-(accent|scenario|warning|error)(-|$)/;
const colourTokens = (m: Map<string, string>) => [...m].filter(([n]) => n.startsWith("--gm-color-"));

// Text (WCAG 1.4.3, 4.5:1) and the surfaces each sits on in components.
const textPairs: [string, string][] = [
  ...["sheet", "ground", "raised", "well", "hover", "press", "selection-tint", "text-selection", "scenario-wash", "warning-wash", "error-wash"].map(
    (bg): [string, string] => ["--gm-color-ink", `--gm-color-${bg}`],
  ),
  ...["sheet", "ground", "raised", "well", "hover", "error-wash"].map((bg): [string, string] => ["--gm-color-ink-2", `--gm-color-${bg}`]),
  ["--gm-color-ink-3", "--gm-color-ground"],
  ["--gm-color-ink-3", "--gm-color-sheet"],
  ["--gm-color-on-ink", "--gm-color-ink"],
  ["--gm-color-on-ink", "--gm-color-ink-2"],
  ["--gm-color-error", "--gm-color-raised"],
  ["--gm-color-error", "--gm-color-hover"],
  ["--gm-color-error", "--gm-color-sheet"],
  ["--gm-color-error", "--gm-color-error-wash"],
  ["--gm-color-warning", "--gm-color-warning-wash"],
  ["--gm-color-scenario", "--gm-color-scenario-wash"],
  ["--gm-color-scenario", "--gm-color-sheet"],
  // T-032: the run button's label, an interval in the scenario column, the chart's labels.
  ["--gm-color-on-scenario", "--gm-color-scenario"],
  ["--gm-color-ink-2", "--gm-color-scenario-wash"],
  ["--gm-color-ink-3", "--gm-color-scenario-wash"],
  // H-041: the accent as text and its wash, the AI button's label, the tertiary ink on raised cards.
  ["--gm-color-accent", "--gm-color-sheet"],
  ["--gm-color-accent", "--gm-color-ground"],
  ["--gm-color-accent", "--gm-color-accent-wash"],
  ["--gm-color-on-accent", "--gm-color-accent-fill"],
  ["--gm-color-ink", "--gm-color-accent-wash"],
  ["--gm-color-ink-3", "--gm-color-raised"],
  // H-045: a chosen template's one-line description on Selected Row Tint.
  ["--gm-color-ink-2", "--gm-color-selection-tint"],
];
// Control boundaries against the surfaces controls sit on, the focus ring
// and the checked box (WCAG 1.4.11, 3:1). A hovered control's own fill is
// not a surface it sits on, and rows have no hover fill (ui/table.module.css).
// A selected row's checkbox sits on Selected Row Tint (T-045 review 1 L4:
// 3.31:1 light, 3.06:1 dark, so a small tint change could break it).
const nonTextPairs: [string, string][] = [
  ...["sheet", "ground", "raised", "panel", "selection-tint"].map((bg): [string, string] => ["--gm-color-edge", `--gm-color-${bg}`]),
  ...["sheet", "ground", "raised", "hover", "selection-tint", "well"].map((bg): [string, string] => ["--gm-color-ink", `--gm-color-${bg}`]),
  ["--gm-color-error", "--gm-color-raised"],
  // T-032: the run button's edge, and the two data inks of the Marey chart and its keys on the sheet and in a table's scenario column.
  ["--gm-color-scenario", "--gm-color-sheet"],
  ["--gm-color-baseline", "--gm-color-sheet"],
  ["--gm-color-scenario", "--gm-color-scenario-wash"],
  // H-041: the focus ring and a selected step's border are the accent.
  ["--gm-color-accent", "--gm-color-sheet"],
  ["--gm-color-accent", "--gm-color-raised"],
  ["--gm-color-accent", "--gm-color-ground"],
];

function cssFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) return name === "node_modules" || name.startsWith(".") ? [] : cssFiles(p);
    return name.endsWith(".css") ? [p] : [];
  });
}
const ownCss = [...cssFiles(join(root, "ui")), ...cssFiles(join(root, "app")), ...cssFiles(join(root, "features"))];

describe("design tokens", () => {
  for (const theme of ["light", "dark"] as const) {
    it.each(textPairs)(`${theme}: %s on %s is at least 4.5:1`, (fg, bg) => {
      expect(contrast(fg, bg, theme)).toBeGreaterThanOrEqual(4.5);
    });

    it.each(nonTextPairs)(`${theme}: %s against %s is at least 3:1`, (fg, bg) => {
      expect(contrast(fg, bg, theme)).toBeGreaterThanOrEqual(3);
    });
  }

  it("has every colour of DESIGN.md, value for value, and no other", () => {
    expect(colourKeys.length).toBeGreaterThan(20);
    for (const key of colourKeys) {
      expect(norm(light.get(`--gm-color-${key}`) ?? "missing"), key).toBe(norm(designColours.get(key)!));
      expect(norm(darkByChoice.get(`--gm-color-${key}`) ?? "missing"), `${key}-dark`).toBe(norm(designColours.get(`${key}-dark`)!));
    }
    const own = (m: Map<string, string>) => colourTokens(m).map(([n]) => n.slice("--gm-color-".length)).sort();
    expect(own(light)).toEqual([...colourKeys].sort());
    expect(own(darkByChoice)).toEqual([...colourKeys].sort());
  });

  it("is dark the same way whether the system or the reader chose it", () => {
    expect(css).toMatch(/@media \(prefers-color-scheme: dark\)\s*\{\s*:root:not\(\[data-theme="light"\]\)\s*\{/);
    expect([...darkBySystem]).toEqual([...darkByChoice]);
    expect(darkByChoice.get("color-scheme")).toBe("dark");
    expect(block(':root[data-theme="light"]').get("color-scheme")).toBe("light");
    expect(light.get("color-scheme")).toBe("light dark");
  });

  it("keeps the dark neutrals gray-black, with no chroma (H-012); the hues' washes are tinted (H-041)", () => {
    for (const [name, value] of [...colourTokens(darkByChoice), ["--gm-shadow-overlay", darkByChoice.get("--gm-shadow-overlay")!] as const]) {
      if (hueFamilies.test(name)) continue;
      for (const c of value.match(/#[0-9a-f]{6}|rgba?\([^)]*\)/gi) ?? []) {
        const [r, g, b] = rgba(c);
        expect([r, g], `${name}: ${c}`).toEqual([b, b]);
      }
    }
  });

  it("has no hue but the accent, the scenario green and the warning and error signals (H-027, H-041)", () => {
    for (const [theme, m] of [["light", light], ["dark", darkByChoice]] as const) {
      for (const [name, value] of colourTokens(m)) {
        if (hueFamilies.test(name)) continue;
        expect(chroma(value), `${theme} ${name} ${value} is a neutral`).toBeLessThanOrEqual(0.012);
      }
    }
    // The scenario green is the only green, and green is never the focus or a link colour: focus is the accent.
    expect(resolve("--gm-focus-ring")).toBe("2px solid #6d4aff");
  });

  it("has DESIGN.md's five type sizes, spacing scale and corners", () => {
    const sizes = [...light].filter(([n]) => n.startsWith("--gm-font-size-")).map(([, v]) => parseFloat(v));
    expect(new Set(sizes)).toEqual(new Set([12, 13, 15, 20, 26]));
    expect(designSizes).toEqual(new Set(sizes));
    for (const [key, value] of designSpacing) expect(light.get(`--gm-space-${key}`), `space ${key}`).toBe(value);
    expect([...light].filter(([n]) => n.startsWith("--gm-space-")).length).toBe(designSpacing.size);
    for (const [key, value] of designRadii) expect(light.get(`--gm-radius-${key}`), `radius ${key}`).toBe(value);
  });

  it("uses Geist, Geist Mono and Noto Sans TC with real fallbacks, and tabular figures (H-041)", () => {
    const stack = resolve("--gm-font-sans");
    expect(stack).toMatch(/^"Geist Variable", Geist, "Noto Sans TC Variable", "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", system-ui, sans-serif$/);
    expect(resolve("--gm-font-mono")).toMatch(/^"Geist Mono Variable", "Geist Mono", ui-monospace, .*monospace$/);
    expect(resolve("--gm-font-features")).toBe('"tnum" 1, "lnum" 1');
  });

  it("keeps targets at least 24 px in compact density, and density applies from <html>", () => {
    const compact = block(':root[data-density="compact"]');
    for (const name of ["--gm-control-height", "--gm-row-height"]) {
      expect(parseInt(resolve(name), 10), name).toBeGreaterThanOrEqual(32);
      expect(parseInt(compact.get(name) ?? "", 10), `compact ${name}`).toBeGreaterThanOrEqual(24);
    }
  });

  it("has a visible focus ring in the accent, which lightens in dark (H-041)", () => {
    expect(parseInt(resolve("--gm-focus-width"), 10)).toBeGreaterThanOrEqual(2);
    expect(resolve("--gm-focus-ring", "dark")).toBe("2px solid #9d8fff");
  });
});

describe("fonts", () => {
  const packages = ["@fontsource-variable/geist", "@fontsource-variable/geist-mono", "@fontsource-variable/noto-sans-tc"];
  const manifest = JSON.parse(readFileSync(join(root, "package.json"), "utf8")) as { dependencies: Record<string, string> };

  it("are self-hosted from three OFL packages at exact versions, recorded in the notices", () => {
    const notices = readFileSync(join(root, "../../docs/THIRD_PARTY_NOTICES.md"), "utf8");
    for (const p of packages) {
      expect(manifest.dependencies[p], p).toMatch(/^\d+\.\d+\.\d+$/);
      const pkg = JSON.parse(readFileSync(join(root, "node_modules", p, "package.json"), "utf8")) as { license: string; version: string; scripts?: unknown };
      expect([pkg.license, pkg.version, pkg.scripts]).toEqual(["OFL-1.1", manifest.dependencies[p], undefined]);
      expect(readFileSync(join(root, "node_modules", p, "LICENSE"), "utf8")).toContain("SIL OPEN FONT LICENSE Version 1.1");
      expect(notices).toContain(`${p} ${pkg.version}`);
    }
  });

  it("load only from the app's origin: the package CSS names local files, ours no font service", () => {
    const styles = readFileSync(join(root, "ui/global-styles.ts"), "utf8");
    const imported = [...styles.matchAll(/^import "(@fontsource-variable\/[^"]+)";$/gm)].map((m) => m[1]!);
    expect(imported).toEqual(["@fontsource-variable/geist/wght.css", "@fontsource-variable/geist-mono/wght.css", "@fontsource-variable/noto-sans-tc/wght.css"]);
    for (const spec of imported) {
      const text = readFileSync(join(root, "node_modules", spec), "utf8");
      const urls = [...text.matchAll(/url\(([^)]+)\)/g)].map((m) => m[1]!);
      expect(urls.length, spec).toBeGreaterThan(0);
      for (const u of urls) expect(u, spec).toMatch(/^\.\/files\/[\w-]+\.woff2$/);
      expect(text, spec).toMatch(/font-display: swap/);
    }
    // Our CSS imports only these package stylesheets (bundled from node_modules), and they load nothing either.
    const packageImports: Record<string, string[]> = {
      [join(root, "features/model-editor/vendor.css")]: ['@import "@xyflow/react/dist/base.css" layer(vendor);'],
    };
    for (const f of ownCss) {
      let text = readFileSync(f, "utf8");
      for (const line of packageImports[f] ?? []) {
        expect(text, f).toContain(line);
        text = text.replace(line, "");
        const spec = /^@import "([^"]+)"/.exec(line)![1]!;
        expect(readFileSync(join(root, "node_modules", spec), "utf8"), spec).not.toMatch(/@font-face|@import|url\(/);
      }
      expect(text, f).not.toMatch(/@font-face|@import|url\(|fonts\.googleapis|fonts\.gstatic/);
    }
  });
});
