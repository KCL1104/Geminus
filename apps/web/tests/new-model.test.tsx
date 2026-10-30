// @vitest-environment happy-dom
// H-045: on the project page an editor or owner starts a new model from a
// template. The page offers every template the contract names, under its
// domain's name with a one-line description, in both languages; the name
// follows the chosen template until the person types one; the request is
// createModel with an Idempotency-Key and the reader's language, retried
// with the same key; a refusal is the API's problem; success opens the
// new model's canvas. A viewer is not offered it. Keyboard: the arrows
// move between the templates, Cancel gives the focus back to the action.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { act, Suspense } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { type Auth, AuthProvider } from "../lib/auth/session";
import type { Model } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { TEMPLATE_GROUPS, templateDescription, templateName } from "../features/models/templates";
import { key, mount, press, settle, violations } from "./ui-harness";

const nav = vi.hoisted(() => ({ pushed: [] as string[] }));
vi.mock("next/navigation", async (orig) => ({
  ...(await orig<typeof import("next/navigation")>()),
  useRouter: () => ({ push: (href: string) => nav.pushed.push(href), refresh: () => {} }),
}));

vi.setConfig({ testTimeout: 60_000 });

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const PRJ = "prj_01M4YDQK00TA00000000000001";
const NEW = "mdl_01M4YDQK00TA00000000000077";
const auth = (role: Auth["role"]): Auth => ({ session: { organizationId: ORG }, role });

const model = (id: string, name: string, domain: Model["domain"]): Model => ({
  id, organization_id: ORG, project_id: PRJ, domain, name,
  draft: { revision: 1, base_version_id: "mv_01M4YDQK00TA00000000000001", out_of_date: false },
  latest_version: { id: "mv_01M4YDQK00TA00000000000001", version: 1 }, created_at: "2026-10-29T08:00:00Z",
});

type Call = { method: string; path: string; key: string | null; body: unknown };

/** The API behind the app's own client: the project's models, and createModel answering from `answers` in turn (201 once they run out). */
function fakeApi(answers: (() => Response | Promise<Response>)[] = []) {
  const calls: Call[] = [];
  const client = createApiClient({
    session: () => ({ organizationId: ORG }),
    fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), "http://app.test").pathname;
      const method = init?.method ?? "GET";
      const headers = new Headers(init?.headers);
      calls.push({ method, path, key: headers.get("Idempotency-Key"), body: init?.body ? JSON.parse(String(init.body)) : null });
      if (path !== `/api/v1/projects/${PRJ}/models`) throw new Error(`no fake answer for ${method} ${path}`);
      if (method === "GET") return new Response(JSON.stringify({ items: [model("mdl_01M4YDQK00TA00000000000001", "Line 1", "electronics_line")], next_cursor: null }), { status: 200 });
      const next = answers.shift();
      if (next) return next();
      const body = JSON.parse(String(init?.body)) as { name: string; domain: Model["domain"] };
      return new Response(JSON.stringify(model(NEW, body.name, body.domain)), { status: 201 });
    }) as typeof fetch,
  });
  return { client, calls, creates: () => calls.filter((c) => c.method === "POST") };
}

const problem = (status: number, code: string) => () =>
  new Response(JSON.stringify({ error: { code, message: "refused", request_id: "req_01M4YDQK00V6H8S0ST4F7510KR" } }), { status });

async function projectPage(opts: { role?: Auth["role"]; locale?: Locale; answers?: (() => Response | Promise<Response>)[] } = {}) {
  const api = fakeApi(opts.answers);
  const { default: ProjectPage } = await import("../app/projects/[projectId]/page");
  const el = await mount(
    <AuthProvider value={auth(opts.role === undefined ? "editor" : opts.role)}>
      <ApiProvider client={api.client}>
        <Suspense fallback={null}>
          <ProjectPage params={Promise.resolve({ projectId: PRJ })} />
        </Suspense>
      </ApiProvider>
    </AuthProvider>,
    opts.locale ?? "en",
  );
  await settle();
  await settle();
  return { el, api };
}

const text = (el: Element | null | undefined) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();
const button = (el: ParentNode, name: string) => [...el.querySelectorAll<HTMLButtonElement>("button")].find((b) => text(b) === name);
const form = (el: Element) => el.querySelector<HTMLElement>("[data-new-model]");
const radios = (el: ParentNode) => [...el.querySelectorAll<HTMLInputElement>('input[type="radio"]')];
const radioName = (r: HTMLInputElement) => text(document.getElementById(r.getAttribute("aria-labelledby") ?? ""));
const nameField = (el: Element, label: string) => {
  const l = [...el.querySelectorAll("label")].find((x) => text(x) === label);
  if (!l) throw new Error(`no field ${label}`);
  return document.getElementById(l.htmlFor) as HTMLInputElement;
};
async function type(input: HTMLInputElement, value: string) {
  await act(async () => {
    input.focus();
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await settle();
}
async function choose(el: Element, name: string) {
  const r = radios(el).find((x) => radioName(x) === name);
  if (!r) throw new Error(`no template ${name}`);
  await press(r.closest("label"));
  await settle();
}
async function open(el: Element, t = createTranslator("en")) {
  await press(button(el, t("newModel.open")));
  await settle();
  const f = form(el);
  if (!f) throw new Error("the form did not open");
  return f;
}
async function submit(f: HTMLElement, t = createTranslator("en")) {
  await press(button(f, t("newModel.create")));
  await settle();
  await settle();
}

beforeEach(() => {
  nav.pushed = [];
});

describe("the templates the page offers", () => {
  it("are exactly the contract's create-model templates, each under its own domain", () => {
    const schema = JSON.parse(readFileSync(join(__dirname, "../../../schemas/http.schema.json"), "utf8")) as {
      $defs: { CreateModel: { oneOf: { properties: { domain: { const: string }; template: { const?: string; enum?: string[] } } }[] } };
    };
    const contract = schema.$defs.CreateModel.oneOf.flatMap((alt) => {
      const tpl = alt.properties.template;
      return (tpl.enum ?? [tpl.const!]).map((name) => `${alt.properties.domain.const}/${name}`);
    });
    const offered = TEMPLATE_GROUPS.flatMap((g) => g.templates.map((t) => `${g.domain}/${t}`));
    expect([...offered].sort()).toEqual([...contract].sort());
    expect(offered).toContain("production_logistics/shoe_factory");
    expect(offered).toHaveLength(6);
  });

  it("have a name and a one-line description in both languages, each its own", () => {
    const seen = new Set<string>();
    for (const g of TEMPLATE_GROUPS) {
      for (const t of g.templates) {
        for (const k of [templateName(t), templateDescription(t)]) {
          expect(en[k], k).toBeTruthy();
          expect(zhHant[k], k).toBeTruthy();
          expect(zhHant[k], k).not.toBe(en[k]);
          expect(seen.has(en[k]), `${k} repeats another`).toBe(false);
          seen.add(en[k]);
        }
        expect(en[templateDescription(t)].length, t).toBeLessThanOrEqual(120);
      }
    }
  });
});

describe("a new model from a template on the project page (H-045)", () => {
  it("is offered to an editor and an owner, not to a viewer", async () => {
    for (const role of ["editor", "owner"] as const) {
      const { el } = await projectPage({ role });
      expect(button(el, en["newModel.open"]), role).toBeDefined();
      expect(button(el, en["newModel.open"])!.getAttribute("data-variant"), "not the page's primary").toBe("secondary");
    }
    for (const role of ["viewer", null] as const) {
      const { el } = await projectPage({ role });
      expect(text(el), `${role}: the models are listed`).toContain("Line 1");
      expect(button(el, en["newModel.open"]), `${role ?? "no role"}`).toBeUndefined();
      expect(form(el)).toBeNull();
    }
  });

  it.each(["en", "zh-Hant"] as const)("lists every template under its domain's name, in %s, and starts on the first with its name", async (locale) => {
    const t = createTranslator(locale);
    const { el } = await projectPage({ locale });
    const f = await open(el, t);
    expect(text(f.querySelector("h2"))).toBe(t("newModel.title"));
    expect([...f.querySelectorAll("h3")].map(text)).toEqual(TEMPLATE_GROUPS.map((g) => t(`domain.${g.domain}.name`)));
    const rs = radios(f);
    expect(rs.map(radioName)).toEqual(TEMPLATE_GROUPS.flatMap((g) => g.templates.map((x) => t(templateName(x)))));
    for (const r of rs) {
      const tpl = r.closest("[data-template]")!.getAttribute("data-template") as Parameters<typeof templateName>[0];
      expect(text(document.getElementById(r.getAttribute("aria-describedby") ?? "")), tpl).toBe(t(templateDescription(tpl)));
    }
    expect(rs[0]!.checked).toBe(true);
    expect(document.activeElement, "the focus moves to the chosen template").toBe(rs[0]);
    expect(nameField(f, t("newModel.name")).value).toBe(t(templateName("electronics_assembly_test_line")));
    // The action is replaced by the form, whose Create is the page's one primary.
    expect(button(el, t("newModel.open"))).toBeUndefined();
    expect(el.querySelectorAll('[data-variant="primary"]')).toHaveLength(1);
    expect(await violations()).toEqual([]);
  });

  it.each(["en", "zh-Hant"] as const)("creates the chosen template with a key and the reader's language, in %s, then opens the model", async (locale) => {
    const t = createTranslator(locale);
    const { el, api } = await projectPage({ locale });
    const f = await open(el, t);
    await choose(f, t(templateName("shoe_factory")));
    expect(nameField(f, t("newModel.name")).value, "the name follows the template").toBe(t(templateName("shoe_factory")));
    await submit(f, t);
    const [c] = api.creates();
    expect(api.creates()).toHaveLength(1);
    expect(c!.path).toBe(`/api/v1/projects/${PRJ}/models`);
    expect(c!.body).toEqual({ name: t(templateName("shoe_factory")), domain: "production_logistics", template: "shoe_factory", locale });
    expect(c!.key).toMatch(/^[\x21-\x7e]{8,128}$/);
    expect(nav.pushed).toEqual([`/models/${NEW}`]);
  });

  it("keeps a name the person typed when the template changes, trims it, and refuses an empty one", async () => {
    const { el, api } = await projectPage();
    const f = await open(el);
    const field = nameField(f, en["newModel.name"]);
    await type(field, "   ");
    await submit(f);
    expect(api.creates(), "no request without a name").toHaveLength(0);
    expect(text(f)).toContain(en["newModel.nameRequired"]);
    expect(field.getAttribute("aria-invalid")).toBe("true");
    await type(field, "  Plant B warehouse ");
    expect(text(f)).not.toContain(en["newModel.nameRequired"]);
    await choose(f, en[templateName("warehouse")]);
    expect(field.value).toBe("  Plant B warehouse ");
    await submit(f);
    expect(api.creates().map((c) => c.body)).toEqual([{ name: "Plant B warehouse", domain: "production_logistics", template: "warehouse", locale: "en" }]);
  });

  it("shows a refusal as the API's problem, retries with the same key, and takes a new key for another request", async () => {
    const { el, api } = await projectPage({ answers: [problem(403, "FORBIDDEN"), problem(503, "SERVICE_UNAVAILABLE")] });
    const f = await open(el);
    await choose(f, en[templateName("food_plant")]);
    await submit(f);
    expect(f.querySelector('[role="alert"]')?.getAttribute("data-problem")).toBe("forbidden");
    expect(nav.pushed).toEqual([]);
    await submit(f);
    expect(f.querySelector('[role="alert"]')?.getAttribute("data-problem")).toBe("unavailable");
    await submit(f);
    const keys = api.creates().map((c) => c.key);
    expect(keys).toHaveLength(3);
    expect(new Set(keys).size, "a retry of the same request reuses its key").toBe(1);
    expect(f.querySelector('[role="alert"]'), "the problem goes once it worked").toBeNull();
    expect(nav.pushed).toEqual([`/models/${NEW}`]);

    const again = await projectPage({ answers: [problem(409, "IDEMPOTENCY_KEY_CONFLICT")] });
    const g = await open(again.el);
    await submit(g);
    expect(g.querySelector('[role="alert"]')?.getAttribute("data-problem")).toBe("conflict");
    await choose(g, en[templateName("carton_plant")]);
    await submit(g);
    const [first, second] = again.api.creates().map((c) => c.key);
    expect(first).not.toBe(second);
  });

  it("does not close while a create is in flight, so it cannot be sent twice", async () => {
    let answer!: (r: Response) => void;
    const { el, api } = await projectPage({ answers: [() => new Promise<Response>((r) => (answer = r))] });
    const f = await open(el);
    await submit(f);
    await press(button(f, en["newModel.cancel"]));
    await settle();
    expect(form(el), "Cancel is blocked while the request is out").not.toBeNull();
    await act(async () => answer(new Response(JSON.stringify(model(NEW, "Line 2", "electronics_line")), { status: 201 })));
    await settle();
    expect(api.creates()).toHaveLength(1);
    expect(nav.pushed).toEqual([`/models/${NEW}`]);
  });

  it("moves between the templates with the arrow keys, and Cancel gives the focus back to the action", async () => {
    const { el, api } = await projectPage();
    const f = await open(el);
    const rs = radios(f);
    await key(rs[0], "ArrowDown");
    await settle();
    expect(rs[1]!.checked, "ArrowDown chooses the next template").toBe(true);
    expect(document.activeElement).toBe(rs[1]);
    expect(nameField(f, en["newModel.name"]).value).toBe(en[templateName("shoe_factory")]);
    await press(button(f, en["newModel.cancel"]));
    await settle();
    expect(form(el)).toBeNull();
    expect(document.activeElement).toBe(button(el, en["newModel.open"]));
    expect(api.creates()).toHaveLength(0);
  });
});
