// T-032: the run button is Deep Scenario Green with an On Scenario label
// (DESIGN.md Buttons, Run), and green means scenario (The Green Means
// Scenario Rule): no other button variant uses the scenario ink. The pair's
// contrast is in tokens.test.ts.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const css = readFileSync(join(process.cwd(), "ui/button.module.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map((m) => ({ selector: m[1]!.trim(), body: m[2]! }));
const decl = (body: string, prop: string) => new RegExp(`(^|;|\\s)${prop}\\s*:\\s*([^;]+)`).exec(body)?.[2]?.trim();

describe("the run button", () => {
  it("fills Deep Scenario Green with an On Scenario label", () => {
    const run = rules.find((r) => r.selector === '.button[data-variant="run"]');
    expect(run).toBeDefined();
    expect([decl(run!.body, "background"), decl(run!.body, "color"), decl(run!.body, "border-color")]).toEqual(["var(--gm-color-scenario)", "var(--gm-color-on-scenario)", "var(--gm-color-scenario)"]);
  });

  it("is the only button that uses the scenario ink", () => {
    const green = rules.filter((r) => /--gm-color-(on-)?scenario/.test(r.body)).map((r) => r.selector);
    expect(green).toEqual(['.button[data-variant="run"]']);
  });
});
