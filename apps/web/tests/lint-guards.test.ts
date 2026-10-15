import { fileURLToPath } from "node:url";
import { ESLint } from "eslint";
import { describe, expect, it } from "vitest";

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
