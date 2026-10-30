// @vitest-environment happy-dom
// H-045: a model of the production and logistics domain goes through the
// generic views unchanged. For each of the pack's four templates (the
// pack's own files), the model page names the domain in both languages and
// offers no photo (that path is the electronics line's), the 2D canvas and
// the 3D scene have one step per IR node where the template puts it, and
// the Marey strip's rows are its steps in flow order with the sinks last.
// Nothing electronics-specific is needed or shown.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { canvasLayout } from "../features/model-editor/layout";
import { ModelView } from "../features/model-editor/model-view";
import { stepRows } from "../features/results/marey";
import { buildScene, fitDistance } from "../features/twin-3d/scene";
import { createApiClient } from "../lib/api/client";
import { draftETag } from "../lib/api/etag";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider } from "../lib/auth/session";
import type { Draft, Model, ModelIR } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { mount, settle } from "./ui-harness";

vi.setConfig({ testTimeout: 60_000 });

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const MDL = "mdl_01M4YDQK00TA00000000000077";
const MV = "mv_01M4YDQK00TA00000000000077";
const templates = ["shoe_factory", "food_plant", "carton_plant", "warehouse"] as const;

const template = (name: string): ModelIR => {
  const ir = JSON.parse(readFileSync(join(process.cwd(), `../../internal/domainpack/productionlogistics/templates/${name}.json`), "utf8")) as ModelIR;
  return { ...ir, model_id: MDL };
};

async function modelPage(ir: ModelIR, locale: Locale) {
  const model: Model = {
    id: MDL, organization_id: ORG, project_id: "prj_01M4YDQK00TA00000000000001", domain: "production_logistics", name: ir.name,
    draft: { revision: 1, base_version_id: MV, out_of_date: false }, latest_version: { id: MV, version: 1 }, created_at: "2026-10-29T08:00:00Z",
  };
  const draft: Draft = { model_id: MDL, base_version_id: MV, revision: 1, ir, hash: `sha256:${"0".repeat(64)}`, out_of_date: false, issues: [] };
  const client = createApiClient({
    session: () => ({ organizationId: ORG }),
    fetch: (async (input: RequestInfo | URL) => {
      const path = new URL(String(input), "http://app.test").pathname;
      if (path === `/api/v1/models/${MDL}`) return new Response(JSON.stringify(model), { status: 200 });
      if (path === `/api/v1/models/${MDL}/draft`) return new Response(JSON.stringify(draft), { status: 200, headers: { ETag: draftETag(1) } });
      throw new Error(`no fake answer for ${path}`);
    }) as typeof fetch,
  });
  const el = await mount(
    <AuthProvider value={{ session: { organizationId: ORG }, role: "editor" }}>
      <ApiProvider client={client}>
        <ModelView modelId={MDL} onProposal={() => {}} />
      </ApiProvider>
    </AuthProvider>,
    locale,
  );
  for (let i = 0; i < 200 && !el.querySelector("h1, [role=alert]"); i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
  await settle();
  return el;
}

const text = (el: Element | null | undefined) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();

describe("a production and logistics model in the generic views (H-045)", () => {
  it.each(templates.flatMap((t) => (["en", "zh-Hant"] as const).map((l) => [t, l] as const)))("%s in %s: the model page names the domain and offers no photo", async (name, locale) => {
    const t = createTranslator(locale);
    const el = await modelPage(template(name), locale);
    expect(el.querySelector("[role=alert]"), "the page loaded").toBeNull();
    const facts = [...el.querySelectorAll("header dl > div")].map((d) => [text(d.querySelector("dt")), text(d.querySelector("dd"))]);
    expect(facts).toContainEqual([t("band.domain"), t("domain.production_logistics.name")]);
    expect(text(el)).not.toContain("production_logistics");
    expect([...el.querySelectorAll("button")].map(text)).not.toContain(t("photo.choose"));
  });

  it.each(templates)("%s: the 2D canvas and the 3D scene have every step where the template puts it, and every flow", (name) => {
    const ir = template(name);
    const layout = canvasLayout(ir);
    for (const n of ir.nodes) expect(layout[n.id], n.id).toEqual(ir.views["2d"]!.positions![n.id]);
    const scene = buildScene(ir);
    expect(scene.nodes.map((n) => n.id)).toEqual(ir.nodes.map((n) => n.id));
    expect(scene.edges.map((e) => [e.from, e.to])).toEqual(ir.flows.map((f) => [f.from, f.to]));
    for (const n of scene.nodes) expect([n.position[0], n.position[2]], n.id).toEqual([layout[n.id]!.x / 60, layout[n.id]!.y / 40]);
    expect(Number.isFinite(fitDistance(scene, 16 / 9))).toBe(true);
  });

  it.each(templates)("%s: the Marey strip's rows are the steps in flow order, the sinks last", (name) => {
    const ir = template(name);
    const rows = stepRows(ir);
    const steps = ir.nodes.filter((n) => ["source", "process", "delay", "sink"].includes(n.type));
    expect([...rows.map((r) => r.id)].sort()).toEqual(steps.map((n) => n.id).sort());
    const firstSink = rows.findIndex((r) => r.sink);
    expect(rows.slice(firstSink).every((r) => r.sink)).toBe(true);
    // A step comes after the steps that feed it on the main line.
    const index = new Map(rows.map((r, i) => [r.id, i]));
    const main = { shoe_factory: ["cutting", "stitching", "lasting", "sole_attaching", "vulcanizing", "finishing", "inspection", "packing"],
      food_plant: ["mixing", "floor_time", "makeup", "final_proof", "baking", "cooling", "slicing", "detection"],
      carton_plant: ["corrugate_rsc", "ffg", "strapping", "palletizing"], warehouse: ["receiving", "putaway", "picking", "packing", "loading"] }[name];
    expect(main.map((id) => index.get(id))).toEqual([...main.map((id) => index.get(id)!)].sort((a, b) => a - b));
  });
});
