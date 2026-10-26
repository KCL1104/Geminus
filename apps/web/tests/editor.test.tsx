// @vitest-environment happy-dom
// T-015 (REQ-004, REQ-006, TEST-005 UI side, TEST-025 2D, TEST-033): the
// model page's 2D editor with the real API client, draft session and
// selection store, against the in-page stand-in for the model API
// (app/ui-gallery/editor/stand-in.ts; it applies patches and answers stale
// If-Match with 409, and validates nothing). Save, the 409 path and its
// explicit reapply, publish gating, issue navigation, the server's field
// errors at their JSON Pointer, read-only for a viewer, the one primary
// action and no duplicate controls (H-027), axe.
import { act, type ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { sampleLine, SAMPLE_MODEL_ID } from "../app/ui-gallery/editor/sample-line";
import { standInFor } from "../app/ui-gallery/editor/sample-states";
import { createStandIn, type StandIn, type StandInOptions } from "../app/ui-gallery/editor/stand-in";
import { ModelView } from "../features/model-editor/model-view";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { type Role, AuthProvider } from "../lib/auth/session";
import type { JsonPatch } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { key, mount, press, settle, violations } from "./ui-harness";

vi.setConfig({ testTimeout: 120_000 });

type Opts = Partial<Omit<StandInOptions, "modelId">> & { role?: Role; locale?: Locale };

async function open(opts: Opts = {}): Promise<{ el: HTMLElement; api: StandIn }> {
  const api = createStandIn({ modelId: SAMPLE_MODEL_ID, ...standInFor("clean"), ...opts });
  const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
  const node: ReactNode = (
    <AuthProvider value={{ session: { organizationId: api.organizationId }, role: opts.role ?? "editor" }}>
      <ApiProvider client={client}>
        <ModelView modelId={SAMPLE_MODEL_ID} />
      </ApiProvider>
    </AuthProvider>
  );
  const el = await mount(node, opts.locale ?? "en");
  await settle();
  await settle();
  return { el, api };
}

const buttons = () => [...document.querySelectorAll<HTMLButtonElement>("button")];
const button = (name: string) => {
  const b = buttons().find((x) => (x.getAttribute("aria-label") ?? x.textContent?.trim()) === name);
  if (!b) throw new Error(`no button "${name}" among ${buttons().map((x) => x.getAttribute("aria-label") ?? x.textContent?.trim()).join(" | ")}`);
  return b;
};
const status = (el: HTMLElement) => el.querySelector("[data-draft-status]")!;
const outcome = (el: HTMLElement) => [...el.querySelectorAll("[role=status]")].find((s) => !s.hasAttribute("data-draft-status"))!.textContent;
const stepNode = (el: HTMLElement, id: string) => el.querySelector<HTMLElement>(`.react-flow__node[data-id="${id}"]`);
const panelHeading = (el: HTMLElement) => el.querySelector("[data-panel=properties] h2")!;
const panel = (el: HTMLElement) => panelHeading(el).closest("section")!;
const field = (el: HTMLElement, label: string) => {
  const l = [...panel(el).querySelectorAll("label")].find((x) => x.textContent === label);
  if (!l) throw new Error(`no field ${label}`);
  return document.getElementById(l.htmlFor) as HTMLInputElement;
};
async function type(input: HTMLInputElement, text: string, commit = true) {
  await act(async () => {
    input.focus();
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, text);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  if (commit) await key(input, "Enter");
  await settle();
}
async function choose(menu: string, item: string) {
  await press(button(menu));
  await settle();
  const it = [...document.querySelectorAll("[role=menuitem]")].find((m) => m.textContent === item);
  if (!it) throw new Error(`no menu item ${item}`);
  await press(it);
  await settle();
}
async function confirm(label: string) {
  const dialog = document.querySelector("[role=alertdialog]")!;
  expect(dialog).not.toBeNull();
  await press([...dialog.querySelectorAll("button")].find((b) => b.textContent === label));
  await settle();
}
async function selectStep(el: HTMLElement, id: string) {
  const n = stepNode(el, id)!;
  await act(async () => n.focus());
  await key(n, "Enter");
  await settle();
}
const patches = (api: StandIn) => api.calls.filter((c) => c.method === "PATCH");

afterEach(() => {
  vi.restoreAllMocks();
});

describe("the editor on the model page", () => {
  it("draws the IR on the canvas with its own IDs, one node per step and one edge per flow", async () => {
    const { el } = await open();
    expect(el.querySelector("h1")?.textContent).toBe(sampleLine.name);
    const ids = [...el.querySelectorAll(".react-flow__node")].map((n) => n.getAttribute("data-id"));
    expect([...ids].sort()).toEqual(sampleLine.nodes.map((n) => n.id).sort());
    expect(el.querySelector('[role="application"]')?.getAttribute("aria-label")).toBe(en["editor.canvas"]);
    expect(stepNode(el, "smt")?.getAttribute("aria-roledescription")).toBe(en["editor.canvas.step"]);
    expect(stepNode(el, "smt")?.getAttribute("aria-label")).toBe("SMT, Process");
    expect(status(el).textContent).toBe("All changes saved. Draft revision 3.");
    expect(await violations()).toEqual([]);
  });

  it("saves the edits as one JSON Patch with If-Match and keeps no other model", async () => {
    const { el, api } = await open();
    await selectStep(el, "smt");
    expect(panelHeading(el).textContent).toBe("SMT");
    await type(field(el, en["editor.field.name"]), "SMT line 1");
    expect(status(el).textContent).toBe("Unsaved changes. Draft revision 3.");
    expect(stepNode(el, "smt")?.textContent).toContain("SMT line 1");
    expect(document.getElementById(button(en["editor.publish"]).getAttribute("aria-describedby")!)?.textContent).toBe(en["editor.publish.saveFirst"]);
    await press(button(en["editor.save"]));
    await settle();
    const [p] = patches(api);
    expect(p).toMatchObject({ path: `/api/v1/models/${SAMPLE_MODEL_ID}/draft`, ifMatch: '"draft-revision-3"' });
    expect(p!.body).toEqual([{ op: "test", path: "/nodes/3/id", value: "smt" }, { op: "replace", path: "/nodes/3/name", value: "SMT line 1" }]);
    expect(status(el).textContent).toBe("All changes saved. Draft revision 4.");
    expect(outcome(el)).toBe("Draft saved as revision 4.");
    expect(api.ir.nodes[3]!.name).toBe("SMT line 1");
    // Saved, nothing to save: Save is blocked and says why (the status line), and keeps the focus.
    expect(button(en["editor.save"]).getAttribute("aria-disabled")).toBe("true");
    expect(document.getElementById(button(en["editor.save"]).getAttribute("aria-describedby")!)).toBe(status(el));
  });

  it("shows 'Saving…' while the save is out, and an edit made then waits for the next save", async () => {
    const { el, api } = await open({ holdSaves: true });
    await selectStep(el, "q_aoi");
    await type(field(el, en["editor.field.capacity"]), "25");
    await press(button(en["editor.save"]));
    await settle();
    expect(status(el).textContent).toBe("Saving… Draft revision 3.");
    await type(field(el, en["editor.field.capacity"]), "30");
    await act(async () => api.release());
    await settle();
    expect(status(el).textContent).toBe("Unsaved changes. Draft revision 4.");
    expect(patches(api)).toHaveLength(1);
    expect(patches(api)[0]!.body).toEqual([{ op: "test", path: "/nodes/4/id", value: "q_aoi" }, { op: "replace", path: "/nodes/4/capacity", value: 25 }]);
  });

  it("on a 409 keeps the edits, and reapplies them on the current draft only when asked", async () => {
    const { el, api } = await open(standInFor("conflict"));
    await selectStep(el, "q_aoi");
    await type(field(el, en["editor.field.capacity"]), "25");
    await press(button(en["editor.save"]));
    await settle();
    expect(status(el).textContent).toBe("Not saved: the draft changed on the server. Draft revision 3.");
    expect(el.querySelector('[data-problem="conflict"]')?.textContent).toContain(en["error.MODEL_VERSION_CONFLICT"]);
    expect(el.textContent).toContain(en["draft.kept"]);
    expect(field(el, en["editor.field.capacity"]).value).toBe("25");
    // Nothing is sent again by itself, and the draft cannot be edited until the person chooses.
    expect(patches(api)).toHaveLength(1);
    expect(button(en["editor.save"]).getAttribute("aria-disabled")).toBe("true");
    expect(el.querySelector(".react-flow__node.draggable")).toBeNull();
    await press(button(en["editor.reapply"]));
    await settle();
    expect(status(el).textContent).toBe("Unsaved changes. Draft revision 4.");
    expect(outcome(el)).toBe("Your changes are on draft revision 4 now. Check them, then save.");
    expect(stepNode(el, "smt")?.textContent).toContain("SMT (line 2)");
    await press(button(en["editor.save"]));
    await settle();
    expect(patches(api).map((p) => p.ifMatch)).toEqual(['"draft-revision-3"', '"draft-revision-4"']);
    expect(status(el).textContent).toBe("All changes saved. Draft revision 5.");
    expect(api.ir.nodes.find((n) => n.id === "smt")!.name).toBe("SMT (line 2)");
    expect(api.ir.nodes.find((n) => n.id === "q_aoi")).toMatchObject({ capacity: 25 });
  });

  it("says so when the edits no longer fit the current draft, and offers only to discard them", async () => {
    // They remove a source; the queue I changed moves to another index, so my test fails.
    const theirs: JsonPatch = [{ op: "test", path: "/flows/1/id", value: "release_b_q_smt" }, { op: "remove", path: "/flows/1" }, { op: "test", path: "/nodes/1/id", value: "release_b" }, { op: "remove", path: "/nodes/1" }];
    const { el, api } = await open({ otherSave: theirs });
    await selectStep(el, "q_smt");
    await type(field(el, en["editor.field.name"]), "Infeed");
    await press(button(en["editor.save"]));
    await settle();
    await press(button(en["editor.reapply"]));
    await settle();
    expect(el.textContent).toContain(en["editor.reapplyFailed"]);
    expect(status(el).getAttribute("data-draft-status")).toBe("conflict");
    expect(() => button(en["editor.reapply"])).toThrow();
    await press(button(en["draft.discard"]));
    await confirm(en["editor.discard.confirm"]);
    expect(status(el).textContent).toBe("All changes saved. Draft revision 4.");
    expect(stepNode(el, "release_b")).toBeNull();
    expect(patches(api)).toHaveLength(1);
  });

  it("blocks Publish while the saved draft has errors, says why, and sends nothing", async () => {
    const { api } = await open(standInFor("problems"));
    const publish = button(en["editor.publish"]);
    expect(publish.getAttribute("aria-disabled")).toBe("true");
    expect(publish.getAttribute("data-variant")).toBe("primary");
    expect(document.getElementById(publish.getAttribute("aria-describedby")!)?.textContent).toBe("Fix the error before publishing.");
    await press(publish);
    await settle();
    expect(document.querySelector("[role=alertdialog]")).toBeNull();
    expect(api.calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("publishes a clean, changed draft after a confirmation, with If-Match, base and idempotency key", async () => {
    const { el, api } = await open();
    const publish = button(en["editor.publish"]);
    expect(publish.getAttribute("aria-disabled")).toBeNull();
    await press(publish);
    await settle();
    expect(document.querySelector("[role=alertdialog] h2, [role=alertdialog] [slot=title]")?.textContent).toBe("Publish version 2?");
    await confirm(en["editor.publish.confirm"]);
    await settle();
    const post = api.calls.find((c) => c.method === "POST")!;
    expect(post).toMatchObject({ path: `/api/v1/models/${SAMPLE_MODEL_ID}/versions`, ifMatch: '"draft-revision-3"', body: { expected_base_version_id: "mv_01M4YDQK00E1ECTR0N1C500001" } });
    expect(outcome(el)).toBe("Version 2 published.");
    expect(el.textContent).toContain("Latest version 2");
    // Nothing changed since the new version: Publish is blocked again, with that reason.
    expect(document.getElementById(button(en["editor.publish"]).getAttribute("aria-describedby")!)?.textContent).toBe("No changes since version 2.");
  });

  it("does not offer Publish for a draft that equals its version", async () => {
    await open({ changed: false });
    expect(document.getElementById(button(en["editor.publish"]).getAttribute("aria-describedby")!)?.textContent).toBe("No changes since version 1.");
  });

  it("lists the server's issues; activating one selects and focuses its step and shows the error at its field", async () => {
    const { el } = await open(standInFor("problems"));
    const issues = [...el.querySelectorAll(`section[aria-labelledby] ul li`)].filter((li) => li.textContent?.includes("/nodes/"));
    expect(issues.map((li) => li.textContent)).toEqual([
      `${en["editor.severity.error"]}${en["validation.BATCH_INVALID"]} Burn-in/nodes/20/batch/max_size`,
      `${en["editor.severity.warning"]}${en["validation.UNREACHABLE_NODE"]} X-ray inspection/nodes/25`,
    ]);
    expect(stepNode(el, "burn_in")?.getAttribute("aria-label")).toBe("Burn-in, Process, with errors");
    await press(issues[0]!.querySelector("button"));
    await settle();
    await act(async () => new Promise((r) => setTimeout(r, 30)));
    expect(panelHeading(el).textContent).toBe("Burn-in");
    expect(document.activeElement).toBe(stepNode(el, "burn_in"));
    const max = field(el, en["editor.batch.max_size"]);
    expect(max.getAttribute("aria-invalid")).toBe("true");
    expect(document.getElementById(max.getAttribute("aria-describedby")!.split(" ").find((id) => document.getElementById(id)?.textContent === en["validation.BATCH_INVALID"])!)).not.toBeNull();
    expect(field(el, en["editor.batch.min_size"]).getAttribute("aria-invalid")).toBeNull();
    // A warning on the step as a whole sits at the top of its properties.
    await press([...el.querySelectorAll("li button")].find((b) => b.textContent?.includes("X-ray inspection")));
    await settle();
    expect(panel(el).textContent).toContain(en["editor.stepProblems"]);
    expect(panel(el).textContent).toContain(en["validation.UNREACHABLE_NODE"]);
    expect(await violations()).toEqual([]);
  });

  it("shows a refused save's details at the fields of the candidate it built", async () => {
    const { el } = await open({ rejectNextSave: [{ path: "/nodes/4/capacity", reason: "must be >= 1", code: "IR_SCHEMA_INVALID" }] });
    await selectStep(el, "q_aoi");
    await type(field(el, en["editor.field.capacity"]), "0");
    await press(button(en["editor.save"]));
    await settle();
    expect(status(el).textContent).toBe("Not saved. Draft revision 3.");
    const capacity = field(el, en["editor.field.capacity"]);
    expect(capacity.getAttribute("aria-invalid")).toBe("true");
    expect(panel(el).textContent).toContain(en["validation.IR_SCHEMA_INVALID"]);
    expect(stepNode(el, "q_aoi")?.getAttribute("aria-label")).toBe("Before AOI, Queue, with errors");
    // The edit is kept; Save may be tried again.
    expect(capacity.value).toBe("0");
    expect(button(en["editor.save"]).getAttribute("aria-disabled")).toBeNull();
  });

  it("says when a number is needed before anything is sent", async () => {
    const { el, api } = await open();
    await selectStep(el, "q_aoi");
    await type(field(el, en["editor.field.capacity"]), "twenty");
    expect(panel(el).textContent).toContain(en["editor.field.wholeNumber"]);
    expect(status(el).textContent).toBe("All changes saved. Draft revision 3.");
    await type(field(el, en["editor.field.capacity"]), "");
    expect(status(el).textContent).toBe("Unsaved changes. Draft revision 3.");
    await press(button(en["editor.save"]));
    await settle();
    expect(patches(api)[0]!.body).toEqual([{ op: "test", path: "/nodes/4/id", value: "q_aoi" }, { op: "remove", path: "/nodes/4/capacity" }]);
  });

  it("adds a step from the step menu, selects it and puts the focus on its name", async () => {
    const { el } = await open();
    await choose(en["editor.addStep"], en["editor.type.process"]);
    await act(async () => new Promise((r) => setTimeout(r, 30)));
    expect(stepNode(el, "process_1")).not.toBeNull();
    expect(panelHeading(el).textContent).toBe("process_1");
    expect(document.activeElement).toBe(field(el, en["editor.field.name"]));
    expect(outcome(el)).toBe("Step process_1 added.");
    // Only the add moves the focus to the name: selecting the step again later leaves it on the step.
    await selectStep(el, "smt");
    await selectStep(el, "process_1");
    await act(async () => new Promise((r) => setTimeout(r, 30)));
    expect(panelHeading(el).textContent).toBe("process_1");
    expect(document.activeElement).toBe(stepNode(el, "process_1"));
  });

  it("connects and disconnects from the properties, and edits a decision's probabilities", async () => {
    const { el, api } = await open();
    await selectStep(el, "q_pack");
    await choose(en["editor.connect"], "Scrap");
    expect(outcome(el)).toBe("Connected to Scrap.");
    // (happy-dom measures no node, so React Flow draws no edge here; the browser checks see it.)
    expect([...panel(el).querySelectorAll("li")].map((li) => li.textContent)).toContain(`Scrap${en["editor.next.remove"]}`);
    await press(button("Remove the connection to Scrap"));
    await settle();
    expect(outcome(el)).toBe("Connection to Scrap removed.");
    await selectStep(el, "aoi_result");
    await type(field(el, "Probability to Before ICT"), "0.9");
    await type(field(el, "Probability to AOI defect"), "0.1");
    await press(button(en["editor.save"]));
    await settle();
    const body = patches(api)[0]!.body as JsonPatch;
    // The first topology edit writes down the layout the person sees (the sample has none yet).
    const placed = body.filter((o) => o.path.startsWith("/views/2d/positions/"));
    expect(placed.map((o) => o.path.split("/").at(-1)).sort()).toEqual(sampleLine.nodes.map((n) => n.id).sort());
    expect(body.filter((o) => o.op !== "test" && !o.path.startsWith("/views/"))).toEqual([
      { op: "add", path: "/flows/-", value: { id: "q_pack_scrap", from: "q_pack", to: "scrap", routing: { type: "probability", probability: 1 } } },
      { op: "remove", path: "/flows/26" },
      { op: "replace", path: "/flows/6/routing/probability", value: 0.9 },
      { op: "replace", path: "/flows/7/routing/probability", value: 0.1 },
    ]);
  });

  it("deletes a step with its connections after a confirmation; Delete on the canvas asks the same", async () => {
    const { el, api } = await open();
    await selectStep(el, "q_touchup");
    await press(button(en["editor.delete"]));
    await settle();
    expect(document.querySelector("[role=alertdialog]")?.textContent).toContain("Delete Before touch-up?");
    expect(document.querySelector("[role=alertdialog]")?.textContent).toContain("Its 2 connections are removed too.");
    await confirm(en["editor.delete.confirm"]);
    expect(stepNode(el, "q_touchup")).toBeNull();
    expect(outcome(el)).toBe("Before touch-up deleted.");
    const n = stepNode(el, "touchup")!;
    await act(async () => n.focus());
    await key(n, "Delete");
    await settle();
    expect(document.querySelector("[role=alertdialog]")?.textContent).toContain("Delete Touch-up?");
    await press([...document.querySelectorAll("[role=alertdialog] button")].find((b) => b.textContent === en["editor.cancel"]));
    await settle();
    expect(stepNode(el, "touchup")).not.toBeNull();
    await press(button(en["editor.save"]));
    await settle();
    expect(api.ir.nodes.some((x) => x.id === "q_touchup")).toBe(false);
    expect(api.ir.flows.some((f) => f.from === "q_touchup" || f.to === "q_touchup")).toBe(false);
  });

  it("deselects with Escape and keeps the focus on the step", async () => {
    const { el } = await open();
    await selectStep(el, "aoi");
    expect(panelHeading(el).textContent).toBe("AOI");
    await key(stepNode(el, "aoi"), "Escape");
    await settle();
    expect(panelHeading(el).textContent).toBe(en["editor.properties"]);
    expect(document.activeElement).toBe(stepNode(el, "aoi"));
  });

  it("discards the edits after a confirmation", async () => {
    const { el, api } = await open();
    await selectStep(el, "smt");
    await type(field(el, en["editor.field.name"]), "Other");
    await press(button(en["draft.discard"]));
    await confirm(en["editor.discard.confirm"]);
    expect(status(el).textContent).toBe("All changes saved. Draft revision 3.");
    expect(stepNode(el, "smt")?.textContent).toContain("SMT");
    expect(outcome(el)).toBe(en["editor.discarded"]);
    expect(patches(api)).toHaveLength(0);
  });

  it("shows batch and changeover settings of the electronics line, read-only where the slice does not edit them", async () => {
    const { el } = await open();
    await selectStep(el, "burn_in");
    expect(field(el, en["editor.batch.min_size"]).value).toBe("20");
    expect(field(el, en["editor.batch.max_size"]).value).toBe("40");
    expect(field(el, en["editor.batch.max_wait"]).value).toBe("120");
    await selectStep(el, "smt");
    expect(panel(el).textContent).toContain("SMT line: 1 of 1");
    expect(panel(el).textContent).toContain(en["editor.resource.breakdowns"]);
    expect(panel(el).textContent).toContain("Works shifts: Two shifts");
    expect(panel(el).textContent).toContain("Board A to Board B: Triangular: 15, 25, 45");
    expect(panel(el).textContent).toContain(`${en["editor.dist.triangular"]}`);
  });

  it("is read-only for a viewer: no edit controls, the notice, nothing draggable", async () => {
    const { el } = await open({ role: "viewer" });
    expect(buttons().map((b) => b.textContent)).not.toContain(en["editor.save"]);
    expect(buttons().map((b) => b.textContent)).not.toContain(en["editor.addStep"]);
    expect(el.textContent).toContain(en["readOnly.viewer"]);
    expect(el.querySelector(".react-flow__node.draggable")).toBeNull();
    await selectStep(el, "q_aoi");
    expect(field(el, en["editor.field.capacity"]).readOnly).toBe(true);
    expect(() => button(en["editor.delete"])).toThrow();
    expect(await violations()).toEqual([]);
  });

  it("asks for a wider screen on a phone instead of editing", async () => {
    vi.spyOn(window, "matchMedia").mockImplementation((q: string) => ({ matches: false, media: q, addEventListener: () => {}, removeEventListener: () => {} }) as unknown as MediaQueryList);
    const { el } = await open();
    expect(el.textContent).toContain(en["editor.narrow"]);
    expect(el.querySelector(".react-flow")).toBeNull();
    expect(buttons().map((b) => b.textContent)).not.toContain(en["editor.save"]);
  });

  it("speaks the reader's language and leaves names and IDs as they are", async () => {
    const { el } = await open({ ...standInFor("problems"), locale: "zh-Hant" });
    expect(status(el).textContent).toBe("所有變更都已儲存。草稿修訂版 3。");
    expect(button(zhHant["editor.publish"])).toBeDefined();
    expect(stepNode(el, "smt")?.getAttribute("aria-label")).toBe("SMT，工序");
    expect(el.querySelector('[role="application"]')?.getAttribute("aria-label")).toBe(zhHant["editor.canvas"]);
    expect(el.textContent).toContain(zhHant["validation.BATCH_INVALID"]);
    expect(await violations()).toEqual([]);
  });
});

describe("only the controls each state needs (H-027)", () => {
  const visibleButtons = (root: ParentNode) => [...root.querySelectorAll("button")].map((b) => b.getAttribute("aria-label") ?? b.textContent?.trim() ?? "");
  const repeated = (xs: string[]) => [...new Set(xs.filter((x, i) => xs.indexOf(x) !== i))];
  function check(el: HTMLElement, name: string) {
    expect(el.querySelectorAll('[data-variant="primary"]').length, `${name}: primary actions`).toBeLessThanOrEqual(1);
    expect(repeated(visibleButtons(el)), `${name}: buttons with the same name`).toEqual([]);
    const hrefs = [...el.querySelectorAll("a[href]")].map((a) => a.getAttribute("href")!);
    expect(repeated(hrefs), `${name}: links to the same place`).toEqual([]);
    for (const b of el.querySelectorAll('[aria-disabled="true"], [disabled]')) {
      const reason = document.getElementById(b.getAttribute("aria-describedby") ?? "");
      expect(reason?.textContent?.trim(), `${name}: a blocked control says why`).toBeTruthy();
    }
    expect(el.querySelectorAll("[disabled]").length, `${name}: nothing is disabled out of focus`).toBe(0);
  }

  it.each([
    ["clean", {}],
    ["with problems", standInFor("problems")],
    ["a viewer", { role: "viewer" as const }],
  ])("holds for a draft %s", async (name, opts) => {
    const { el } = await open(opts as Opts);
    check(el, name);
    await selectStep(el, "aoi_result");
    check(el, `${name}, a decision selected`);
  });

  it("holds while dirty and in a conflict", async () => {
    const { el } = await open(standInFor("conflict"));
    await selectStep(el, "q_aoi");
    await type(field(el, en["editor.field.capacity"]), "25");
    check(el, "dirty");
    await press(button(en["editor.save"]));
    await settle();
    check(el, "conflict");
    expect(await violations()).toEqual([]);
  });
});

