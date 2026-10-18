import { fileURLToPath } from "node:url";
import { ESLint } from "eslint";
import { describe, expect, it, vi } from "vitest";

// The first lint loads every plugin, which can take longer than the default 5 s.
vi.setConfig({ testTimeout: 30_000 });

const eslint = new ESLint({ cwd: fileURLToPath(new URL("..", import.meta.url)) });

async function ruleIds(code: string, filePath = "app/probe.tsx"): Promise<(string | null)[]> {
  const [result] = await eslint.lintText(code, { filePath });
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
// The rules that keep user-visible text in the catalog.
const textRules = ["react/jsx-no-literals", "no-restricted-syntax", "no-restricted-globals", "no-restricted-properties"];

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
    ["an aria-label template literal", "export const N = ({ n }: { n: number }) => <nav aria-label={`Main ${n}`} />;"],
    ["a string after &&", 'export const P = ({ a }: { a: boolean }) => <p>{a && " (out of date)"}</p>;'],
    ["a string in a conditional", 'export const P = ({ a }: { a: boolean }) => <p>{a ? "Yes" : "No"}</p>;'],
    ["Chinese text", "export const P = () => <p>專案</p>;"],
    ["a string in a nested conditional", 'export const P = ({ a, b }: { a: boolean; b: boolean }) => <p>{a && (b ? "Yes" : null)}</p>;'],
    ["a string in a fragment's conditional", 'export const P = ({ a }: { a: boolean }) => <>{a && "Yes"}</>;'],
    ["Chinese text in a conditional", 'export const P = ({ a }: { a: boolean }) => <p>{a && "專案"}</p>;'],
    ["a template literal in a conditional", "export const P = ({ a, n }: { a: boolean; n: number }) => <p>{a && `${n} items`}</p>;"],
    // T-044 review M3
    ["a conditional aria-label", 'export const B = ({ open }: { open: boolean }) => <button aria-label={open ? "Close menu" : "Open menu"} />;'],
    ["an alt fallback", 'export const I = ({ alt }: { alt?: string }) => <img src="/a.png" alt={alt ?? "Avatar"} />;'],
    ["a concatenated title", 'export const S = ({ n }: { n: string }) => <span title={"Delete " + n} />;'],
    ["a cast aria-label", 'export const N = () => <nav aria-label={"Main" as string} />;'],
    ["a text prop on a component", 'declare const Notice: (p: { text: string }) => null;\nexport const P = () => <Notice text="Saved" />;'],
    ["a message prop on a component", 'declare const Notice: (p: { message: string }) => null;\nexport const P = () => <Notice message={"Saved"} />;'],
    ["a submit button value", 'export const F = () => <input type="submit" value="Save" />;'],
    ["text in an object prop", 'declare const V: (p: { problem: { message: string } }) => null;\nexport const P = () => <V problem={{ message: "Something failed" }} />;'],
    ["a string in an array", 'export const P = () => <p>{["Hello"]}</p>;'],
    ["a concatenated child", 'export const P = ({ n }: { n: string }) => <p>{"Delete " + n}</p>;'],
    ["a string a map returns", 'export const P = ({ xs }: { xs: number[] }) => <p>{xs.map(() => "item")}</p>;'],
    ["a conditional a map returns", 'export const P = ({ xs }: { xs: boolean[] }) => <p>{xs.map((x) => (x ? "Yes" : "No"))}</p>;'],
    ["accented text in a conditional", 'export const P = ({ a }: { a: boolean }) => <p>{a && "Été"}</p>;'],
    ["a rare CJK character in a conditional", 'export const P = ({ a }: { a: boolean }) => <p>{a && "\u{2000B}"}</p>;'],
    ["alert", 'export const B = () => <button onClick={() => alert("Saved")} />;'],
    ["window.confirm", 'export const B = () => <button onClick={() => window.confirm("Sure?")} />;'],
    ["document.title", 'export const P = () => { document.title = "Home"; return null; };'],
    // T-044 review 2 N3
    ["globalThis.alert", 'export const B = () => <button onClick={() => globalThis.alert("Saved")} />;'],
    ["self.confirm", 'export const B = () => <button onClick={() => self.confirm("Sure?")} />;'],
    ["window.document.title", 'export const P = () => { window.document.title = "Home"; return null; };'],
    ["a submit button value expression", 'export const F = () => <input type="submit" value={"Save"} />;'],
  ])("rejects %s", async (_, code) => {
    const ids = await ruleIds(code);
    expect(ids.some((id) => id !== null && textRules.includes(id)), ids.join(",")).toBe(true);
  });

  // A hook or helper in a .ts module can open a browser dialog or set the
  // title as well as a component can (T-044 review 2 N3).
  it.each([
    ["window.confirm", 'export const useDiscard = () => () => window.confirm("Discard changes?");'],
    ["alert", 'export function notify(): void { alert("Saved"); }'],
    ["prompt", 'export const ask = () => prompt("Name?");'],
    ["document.title", 'export function retitle(): void { document.title = "Home"; }'],
  ])("rejects %s in a .ts module", async (_, code) => {
    const ids = await ruleIds(code, "app/use-probe.ts");
    expect(ids.some((id) => id !== null && textRules.includes(id)), ids.join(",")).toBe(true);
  });

  it("allows strings in a .ts module", async () => {
    const ids = await ruleIds('export const mode = (edit: boolean) => (edit ? "edit" : "view");', "lib/probe.ts");
    for (const rule of textRules) expect(ids).not.toContain(rule);
  });

  it.each([
    ["catalog text", 'declare const t: (k: string) => string;\nexport const P = () => <p>{t("x")}</p>;'],
    ["punctuation", 'declare const a: string;\nexport const P = () => <p>{a}{" "}{a}{"/"}</p>;'],
    ["non-text attributes", 'export const B = () => <button type="button" className="x" role="status" data-x="y" />;'],
    ["data", "export const P = ({ name }: { name: string }) => <p>{name}</p>;"],
    ["a conditional between data", "export const P = ({ a, b }: { a: string; b: string }) => <p>{a || b}{a ? \"/\" : null}</p>;"],
    ["a catalog key chosen by a condition", 'declare const t: (k: string) => string;\nexport const P = ({ a }: { a: boolean }) => <p>{t(a ? "x.y" : "x.z")}</p>;'],
    ["a conditional that maps data", 'export const P = ({ a }: { a: boolean }) => <p>{a && [1].map((n) => String(n)).join("x")}</p>;'],
    ["a decorative image", 'export const I = () => <img src="/a.png" alt="" />;'],
    ["an option value", 'export const O = () => <select><option value="en">{"/"}</option></select>;'],
    ["a hidden input value", 'export const F = () => <input type="hidden" name="m" value="edit" />;'],
    ["a handler argument", 'declare const set: (m: string) => void;\nexport const B = () => <button type="button" onClick={() => set("edit")} />;'],
    ["a conditional class name", 'export const P = ({ a }: { a: boolean }) => <p className={a ? "on" : "off"} />;'],
    ["a built href", 'export const L = ({ id }: { id: string }) => <a href={"/projects/" + id} />;'],
    ["a comparison in a condition", 'declare const X: () => null;\nexport const P = ({ m }: { m: string }) => <div>{m === "edit" && <X />}</div>;'],
    ["a catalog key in a cast", 'declare const t: (k: string) => string;\nexport const N = ({ open }: { open: boolean }) => <nav aria-label={t(open ? "a.open" : "a.closed")} />;'],
    ["a list of elements", 'export const P = ({ xs }: { xs: string[] }) => <ul>{xs.map((x) => <li key={x}>{x}</li>)}</ul>;'],
    ["an empty message", 'declare const V: (p: { problem: { message: string } }) => null;\nexport const P = () => <V problem={{ message: "" }} />;'],
  ])("allows %s", async (_, code) => {
    const ids = await ruleIds(code);
    for (const rule of textRules) expect(ids).not.toContain(rule);
  });
});
