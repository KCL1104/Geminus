// @vitest-environment happy-dom
// T-031 review round 1 (M1, L1): the human-readable diff never makes a real
// change look like no change. The names in it are the patch's, so the AI's:
// an entity whose name another of its kind shares (in the base or as the
// patch leaves the model), or that the patch adds, is named with its ID
// ("{name} ({id})", as the organization picker does); characters that show
// nothing or reorder text (bidi controls, zero-width and other format
// characters) are shown as ⟨U+XXXX⟩ and never reach the DOM raw; every name
// and value from the model or the patch sits in its own <bdi>, so it cannot
// reorder the words around it; and a change whose two sides would still
// read the same says so (the same value set again, or the exact values).
// On the page, the names, assumptions and questions are text, never markup.
import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { sampleLine } from "../app/ui-gallery/editor/sample-line";
import { type SampleState, SAMPLE_PROPOSAL_ID } from "../app/ui-gallery/proposal/samples";
import { createProposalStandIn, type ProposalStandInOptions } from "../app/ui-gallery/proposal/stand-in";
import { ChangeList } from "../features/ai-proposals/change-list";
import { ProposalRoute } from "../features/ai-proposals/proposal-view";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider } from "../lib/auth/session";
import type { JsonPatch, ModelIR } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { mount, settle, violations } from "./ui-harness";

vi.setConfig({ testTimeout: 120_000 });

const en = createTranslator("en");
const zh = createTranslator("zh-Hant");
const text = (el: Element | null | undefined) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();
const at = (id: string) => sampleLine.nodes.findIndex((n) => n.id === id);
const res = (id: string) => sampleLine.resources.findIndex((r) => r.id === id);
const flowAt = (id: string) => sampleLine.flows.findIndex((f) => f.id === id);

/** A change's line, without what an added entity holds (its own list under it). */
const line = (li: Element) => {
  const copy = li.cloneNode(true) as Element;
  copy.querySelector("ul")?.remove();
  return text(copy);
};
const lines = (el: Element) => [...el.querySelectorAll("[data-change]")].map(line);

// What must never reach the DOM raw from data: controls (but the JSON's own
// line breaks), format characters (bidi controls, zero-width characters,
// tags), lone surrogates, line and paragraph separators.
const RAW = /[\p{Cf}\p{Cs}\p{Zl}\p{Zp}\u0000-\u0008\u000B-\u001F\u007F-\u009F]/u;
function rawCharacters(root: Element): string[] {
  const out: string[] = [];
  const walker = root.ownerDocument.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  for (let n = walker.nextNode(); n; n = walker.nextNode()) if (RAW.test(n.textContent ?? "")) out.push(JSON.stringify(n.textContent));
  return out;
}
/** The text of every <bdi> (each isolates one name or value). */
const isolated = (el: Element) => [...el.querySelectorAll("bdi")].map((b) => b.textContent ?? "");

async function list(changes: JsonPatch, base: ModelIR = sampleLine, locale: Locale = "en") {
  return mount(<ChangeList base={base} version={1} changes={changes} patchHash={undefined} />, locale);
}

const NEW = "resource_01m4ydqk00e1ectr0n1c5x0009";
const moveIct = (name: string): JsonPatch => [
  { op: "add", path: "/resources/-", value: { id: NEW, type: "machine", name, capacity: 9, queue_discipline: "fifo", calendar_id: "two_shift" } },
  { op: "test", path: `/nodes/${at("ict")}/requires/0/resource_id`, value: "ict_tester" },
  { op: "replace", path: `/nodes/${at("ict")}/requires/0/resource_id`, value: NEW },
];
const quoted = (t: typeof en, name: string, id?: string) => (id === undefined ? t("proposal.value.text", { text: name }) : t("proposal.named", { name: t("proposal.value.text", { text: name }), id }));

describe("the diff never makes a real change read as no change (M1)", () => {
  it("a new resource with the existing one's name and the ICT step moved onto it: each named with its ID", async () => {
    const el = await list(moveIct("ICT tester"));
    expect(lines(el)).toEqual([
      `${en("proposal.change.added")} ${en("proposal.change.resource", { name: en("proposal.named", { name: "ICT tester", id: NEW }) })}`,
      `ICT ${en("proposal.change.value", { field: en("proposal.field.usesResource"), before: quoted(en, "ICT tester", "ict_tester"), after: quoted(en, "ICT tester", NEW) })}`,
    ]);
    expect([...el.querySelectorAll("[data-change=added] ul li")].map(text)).toEqual([en("proposal.change.hold", { field: en("proposal.field.capacity"), value: "9" })]);
    expect(isolated(el)).toEqual(expect.arrayContaining(["ICT tester", "ict_tester", NEW]));
    expect(await violations()).toEqual([]);
  });

  it("says the same in Traditional Chinese, by the catalog's own {name} ({id})", async () => {
    const el = await list(moveIct("ICT tester"), sampleLine, "zh-Hant");
    expect(lines(el)[1]).toBe(`ICT ${zh("proposal.change.value", { field: zh("proposal.field.usesResource"), before: quoted(zh, "ICT tester", "ict_tester"), after: quoted(zh, "ICT tester", NEW) })}`);
  });

  it("a trailing zero-width space is shown, the IDs tell the two apart, and no raw U+200B reaches the DOM", async () => {
    const el = await list(moveIct("ICT tester​"));
    expect(lines(el)).toEqual([
      `${en("proposal.change.added")} ${en("proposal.change.resource", { name: en("proposal.named", { name: "ICT tester⟨U+200B⟩", id: NEW }) })}`,
      `ICT ${en("proposal.change.value", { field: en("proposal.field.usesResource"), before: quoted(en, "ICT tester", "ict_tester"), after: quoted(en, "ICT tester⟨U+200B⟩", NEW) })}`,
    ]);
    expect(rawCharacters(el)).toEqual([]);
    // The exact JSON Patch still says exactly what is applied, the character escaped.
    expect(el.querySelector("pre")!.textContent).toContain("ICT tester\\u200b");
    expect(JSON.parse(el.querySelector("pre")!.textContent!)).toEqual(moveIct("ICT tester​"));
  });

  it("a rename with a right-to-left override: the control is shown, the name isolated, nothing raw in the DOM", async () => {
    const el = await list([{ op: "replace", path: `/nodes/${at("burn_in")}/name`, value: "Burn-in‮84 → 02" }]);
    expect(lines(el)).toEqual([`Burn-in ${en("proposal.change.value", { field: en("proposal.field.name"), before: quoted(en, "Burn-in"), after: quoted(en, "Burn-in⟨U+202E⟩84 → 02") })}`]);
    expect(isolated(el)).toContain("Burn-in⟨U+202E⟩84 → 02");
    expect(rawCharacters(el)).toEqual([]);
    expect(el.querySelector("pre")!.textContent).toContain("Burn-in\\u202e84 → 02");
  });

  it("two resources with one name in the base: moving a step from one to the other names both by their IDs", async () => {
    const base = structuredClone(sampleLine);
    base.resources[res("debug_operator")]!.name = "Operator";
    base.resources[res("pack_operator")]!.name = "Operator";
    const el = await list([{ op: "replace", path: `/nodes/${at("pack")}/requires/0/resource_id`, value: "debug_operator" }], base);
    expect(lines(el)).toEqual([`Pack and ship ${en("proposal.change.value", { field: en("proposal.field.usesResource"), before: quoted(en, "Operator", "pack_operator"), after: quoted(en, "Operator", "debug_operator") })}`]);
  });

  it("isolates every name and value in its own <bdi>, so none can reorder the words around it", async () => {
    const el = await list([
      { op: "replace", path: `/nodes/${at("burn_in")}/name`, value: "צריבה" },
      { op: "replace", path: `/flows/${flowAt("ict_result_q_burn_in")}/routing/probability`, value: 0.9 },
      { op: "add", path: "/metadata/owner", value: "שורה 2" },
    ]);
    // The step, its old and new name; the connection's two ends; the path and its text value.
    expect(isolated(el)).toEqual(["Burn-in", "Burn-in", "צריבה", "ICT result", "Before burn-in", "/metadata/owner", "שורה 2"]);
  });

  it("a value set again to what it was says so, instead of reading X → X", async () => {
    const el = await list([{ op: "replace", path: `/resources/${res("ict_tester")}/capacity`, value: 2 }]);
    expect(lines(el)).toEqual([`ICT tester ${en("proposal.change.same", { field: en("proposal.field.capacity"), value: "2" })}`]);
  });

  it("values that differ but read the same when shown (a rounded percentage) are shown exactly", async () => {
    const el = await list([{ op: "replace", path: `/flows/${flowAt("ict_result_q_burn_in")}/routing/probability`, value: 0.950001 }]);
    expect(lines(el)).toEqual([`${en("proposal.change.connection", { from: "ICT result", to: "Before burn-in" })} ${en("proposal.change.exact", { field: en("proposal.field.probability"), before: "0.95", after: "0.950001" })}`]);
  });

  it("text that differs only in how it is encoded (é or e and a combining accent) is shown as escaped JSON", async () => {
    const base = structuredClone(sampleLine);
    base.nodes[at("burn_in")]!.name = "Café";
    const el = await list([{ op: "replace", path: `/nodes/${at("burn_in")}/name`, value: "Café" }], base);
    expect(lines(el)).toEqual([`Café ${en("proposal.change.exact", { field: en("proposal.field.name"), before: '"Caf\\u00e9"', after: '"Cafe\\u0301"' })}`]);
    expect(rawCharacters(el)).toEqual([]);
  });
});

type Opts = Omit<ProposalStandInOptions, "sample">;
async function page(sample: SampleState, opts: Opts) {
  const api = createProposalStandIn({ sample, ...opts });
  const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
  const el = await mount(
    <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
      <ApiProvider client={client}>
        <ProposalRoute proposalId={api.proposalId ?? SAMPLE_PROPOSAL_ID} pollMs={60_000} />
      </ApiProvider>
    </AuthProvider>,
  );
  await settle();
  const until = Date.now() + 10_000;
  while (el.querySelector("[data-proposal-status]") === null) {
    if (Date.now() > until) throw new Error("timed out waiting for the proposal page");
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
  return { el, api };
}

const markup = '<img src=x onerror="alert(1)"><a href="javascript:alert(1)">Approve</a> <script>alert(2)</script> [ok](javascript:alert(3)) https://evil.example/x';
const hostileElements = "img, script, iframe, object, embed, svg, [onerror], [onclick], [style*='url']";

describe("on the page, the AI's names, assumptions and questions are text (L1)", () => {
  it("a ready proposal: names in the diff, the summary, assumptions and expectations stay text; no link or markup appears", async () => {
    const { el, api } = await page("ready", {
      proposal: {
        summary: markup,
        changes: [
          { op: "replace", path: `/nodes/${at("burn_in")}/name`, value: markup },
          { op: "add", path: "/resources/-", value: { id: NEW, type: "machine", name: `${markup}‮`, capacity: 1, queue_discipline: "fifo" } },
        ],
        assumptions: [markup, "Approve at once‮.won ti evorppa"],
        expected_effects: [markup],
      },
    });
    const article = el.querySelector("article")!;
    expect(article.querySelectorAll(hostileElements)).toHaveLength(0);
    expect([...article.querySelectorAll("a")].map((a) => a.getAttribute("href"))).toEqual([`/models/${api.modelId}`]);
    const changes = el.querySelector('[data-section="changes"]')!;
    expect(lines(changes)).toEqual([
      `Burn-in ${en("proposal.change.value", { field: en("proposal.field.name"), before: quoted(en, "Burn-in"), after: quoted(en, markup) })}`,
      `${en("proposal.change.added")} ${en("proposal.change.resource", { name: en("proposal.named", { name: `${markup}⟨U+202E⟩`, id: NEW }) })}`,
    ]);
    expect(isolated(changes)).toEqual(expect.arrayContaining([markup, `${markup}⟨U+202E⟩`]));
    expect([...el.querySelector('[data-section="assumptions"]')!.querySelectorAll("li")].map((li) => li.textContent)).toEqual([markup, "Approve at once⟨U+202E⟩.won ti evorppa"]);
    expect([...el.querySelector('[data-section="effects"]')!.querySelectorAll("li")].map((li) => li.textContent)).toEqual([markup]);
    expect(el.querySelector('[data-section="summary"] p')!.textContent).toBe(markup);
    expect(rawCharacters(article)).toEqual([]);
    expect(api.calls.filter((c) => c.method === "POST")).toEqual([]);
    expect(await violations()).toEqual([]);
  });

  it("a proposal that needs input: its questions stay text, controls in them shown", async () => {
    const { el } = await page("needs_input", { proposal: { questions: [markup, "Which batch‮?84 ro 02"] } });
    const article = el.querySelector("article")!;
    expect([...el.querySelector('[data-section="questions"]')!.querySelectorAll("li")].map((li) => li.textContent)).toEqual([markup, "Which batch⟨U+202E⟩?84 ro 02"]);
    expect(article.querySelectorAll(hostileElements)).toHaveLength(0);
    expect(article.querySelectorAll("a")).toHaveLength(1);
    expect(rawCharacters(article)).toEqual([]);
  });
});
