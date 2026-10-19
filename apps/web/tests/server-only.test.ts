// lib/session holds the BFF's keys, tokens and provider client (ADR-018,
// T-043): nothing a "use client" module reaches may import it, directly or
// through other modules, or it would be bundled for the browser.
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("..", import.meta.url));
const SESSION = join(root, "lib", "session");

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) return name === "node_modules" || name.startsWith(".") ? [] : files(p);
    return /\.(ts|tsx)$/.test(name) && !name.endsWith(".d.ts") ? [p] : [];
  });
}

const sources = ["app", "lib", "ui", "messages"].flatMap((d) => files(join(root, d)));

/** The repository modules file imports (relative specifiers only). */
function imports(file: string): string[] {
  const text = readFileSync(file, "utf8");
  const specs = [...text.matchAll(/(?:import|export)\s[^"']*?from\s*["']([^"']+)["']|import\s*\(\s*["']([^"']+)["']\s*\)|import\s*["']([^"']+)["']/g)].map((m) => m[1] ?? m[2] ?? m[3] ?? "");
  return specs
    .filter((s) => s.startsWith("."))
    .map((s) => resolve(dirname(file), s))
    .flatMap((base) => [base, `${base}.ts`, `${base}.tsx`, join(base, "index.ts"), join(base, "index.tsx")].filter((p) => existsSync(p) && statSync(p).isFile()).slice(0, 1));
}

const isClient = (file: string) => /^\s*(?:\/\/[^\n]*\n\s*)*["']use client["']/.test(readFileSync(file, "utf8"));

describe("server-only session code", () => {
  it("has client modules to check", () => {
    expect(sources.filter(isClient).length).toBeGreaterThan(5);
  });

  it("is not reachable from any client module", () => {
    const reached = new Map<string, string>();
    const queue = sources.filter(isClient).map((f) => [f, relative(root, f)] as const);
    while (queue.length > 0) {
      const [file, via] = queue.shift() as readonly [string, string];
      if (reached.has(file)) continue;
      reached.set(file, via);
      for (const next of imports(file)) queue.push([next, `${via} → ${relative(root, next)}`]);
    }
    const leaks = [...reached].filter(([f]) => f.startsWith(SESSION)).map(([, via]) => via);
    expect(leaks).toEqual([]);
  });

  it("can rely on relative imports: no path alias could reach it another way", () => {
    // A tsconfig paths or baseUrl entry would let "@/lib/session/…" or
    // "lib/session/…" in, which the scan above does not follow.
    const tsconfig = readFileSync(join(root, "tsconfig.json"), "utf8");
    expect(tsconfig).not.toMatch(/"(paths|baseUrl)"\s*:/);
  });

  it("is itself never a client module", () => {
    expect(files(SESSION).filter(isClient)).toEqual([]);
  });

  it("would be caught: a client module importing it is found", () => {
    // The scan follows the same import forms the code uses.
    const probe = join(root, "lib", "api", "provider.tsx");
    expect(isClient(probe)).toBe(true);
    expect(imports(probe).some((p) => p.endsWith(join("lib", "api", "client.ts")))).toBe(true);
  });
});
