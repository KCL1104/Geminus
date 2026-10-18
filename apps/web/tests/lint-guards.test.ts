import { fileURLToPath } from "node:url";
import { ESLint } from "eslint";
import { describe, expect, it, vi } from "vitest";

// The first lint loads every plugin, which can take longer than the default 5 s.
vi.setConfig({ testTimeout: 30_000 });

const eslint = new ESLint({ cwd: fileURLToPath(new URL("..", import.meta.url)) });

async function ruleIds(code: string): Promise<(string | null)[]> {
  const [result] = await eslint.lintText(code, { filePath: "app/probe.tsx" });
  if (!result) throw new Error("no lint result");
  const fatal = result.messages.filter((m) => m.fatal);
  if (fatal.length > 0) throw new Error(`parse failure: ${fatal[0]?.message}`);
  return result.messages.map((m) => m.ruleId);
}

describe("forbidden imports", () => {
  it.each([
    'import Anthropic from "@anthropic-ai/sdk";',
    'import OpenAI from "openai";',
    'import { anthropic } from "@ai-sdk/anthropic";',
    'import { Pool } from "pg";',
    'import { drizzle } from "drizzle-orm/node-postgres";',
  ])("rejects %s", async (line) => {
    expect(await ruleIds(`${line}\nexport const used = [${line.match(/import \{? ?(\w+)/)?.[1]}];\n`))
      .toContain("no-restricted-imports");
  });

  it("allows ordinary UI imports", async () => {
    expect(await ruleIds('import { useState } from "react";\nexport const used = useState;\n'))
      .not.toContain("no-restricted-imports");
  });
});

// eslint-plugin-react, -import and -jsx-a11y do not yet declare ESLint 10 in
// their peer ranges (docs/adr/ADR-011). Prove their rules still execute so an
// incompatibility fails here instead of silently disabling the rules.
describe("eslint-config-next plugins run under the pinned ESLint", () => {
  it.each([
    ["jsx-a11y/alt-text", 'export const A = () => <img src="/a.png" />;'],
    ["react/jsx-key", "export const L = () => <ul>{[1].map((n) => <li>{n}</li>)}</ul>;"],
    ["import/no-anonymous-default-export", "export default { a: 1 };"],
    ["react-hooks/rules-of-hooks", 'import { useState } from "react";\nexport function f() { if (Math.random()) { useState(0); } }'],
    ["@next/next/no-html-link-for-pages", 'export const N = () => <a href="/">home</a>;'],
  ])("%s fires", async (rule, code) => {
    expect(await ruleIds(code)).toContain(rule);
  });
});

// REQ-022: user-visible text comes from the message catalog.
describe("hard-coded UI text", () => {
  it.each([
    ["JSX text", "export const P = () => <p>Hello</p>;"],
    ["a string in JSX", 'export const P = () => <p>{"Hello"}</p>;'],
    ["a template literal in JSX", "export const P = ({ n }: { n: number }) => <p>{`${n} items`}</p>;"],
    ["an aria-label", 'export const N = () => <nav aria-label="Main" />;'],
    ["a title", 'export const S = () => <span title="Help" />;'],
    ["a placeholder", 'export const I = () => <input placeholder="Name" />;'],
    ["an alt text", 'export const A = () => <img src="/a.png" alt="Logo" />;'],
    ["an aria-label expression", 'export const N = () => <nav aria-label={"Main"} />;'],
    ["a string after &&", 'export const P = ({ a }: { a: boolean }) => <p>{a && " (out of date)"}</p>;'],
    ["a string in a conditional", 'export const P = ({ a }: { a: boolean }) => <p>{a ? "Yes" : "No"}</p>;'],
    ["Chinese text", "export const P = () => <p>專案</p>;"],
    ["a string in a nested conditional", 'export const P = ({ a, b }: { a: boolean; b: boolean }) => <p>{a && (b ? "Yes" : null)}</p>;'],
    ["a string in a fragment's conditional", 'export const P = ({ a }: { a: boolean }) => <>{a && "Yes"}</>;'],
    ["Chinese text in a conditional", 'export const P = ({ a }: { a: boolean }) => <p>{a && "專案"}</p>;'],
  ])("rejects %s", async (_, code) => {
    const ids = await ruleIds(code);
    expect(ids.some((id) => id === "react/jsx-no-literals" || id === "no-restricted-syntax"), ids.join(",")).toBe(true);
  });

  it.each([
    ["catalog text", 'declare const t: (k: string) => string;\nexport const P = () => <p>{t("x")}</p>;'],
    ["punctuation", 'declare const a: string;\nexport const P = () => <p>{a}{" "}{a}{"/"}</p>;'],
    ["non-text attributes", 'export const B = () => <button type="button" className="x" role="status" data-x="y" />;'],
    ["data", "export const P = ({ name }: { name: string }) => <p>{name}</p>;"],
    ["a conditional between data", "export const P = ({ a, b }: { a: string; b: string }) => <p>{a || b}{a ? \"/\" : null}</p>;"],
    ["a catalog key chosen by a condition", 'declare const t: (k: string) => string;\nexport const P = ({ a }: { a: boolean }) => <p>{t(a ? "x.y" : "x.z")}</p>;'],
    ["a conditional that maps data", 'export const P = ({ a }: { a: boolean }) => <p>{a && [1].map((n) => String(n)).join("x")}</p>;'],
  ])("allows %s", async (_, code) => {
    const ids = await ruleIds(code);
    expect(ids).not.toContain("react/jsx-no-literals");
    expect(ids).not.toContain("no-restricted-syntax");
  });
});
