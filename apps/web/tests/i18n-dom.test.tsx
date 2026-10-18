// @vitest-environment happy-dom
// REQ-022 / TEST-031: the shell, the states and every problem render in
// zh-Hant from the catalog; data the user or server supplied is shown as
// it came, marked with its language where it is the server's English.
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import ProjectsPage from "../app/projects/page";
import { LocaleMenu, LocaleSwitcher } from "../app/_components/locale-switcher";
import { ProblemView } from "../app/_components/problem-view";
import { DraftStatus } from "../app/_components/states";
import { createApiClient } from "../lib/api/client";
import { networkProblem, type Problem } from "../lib/api/problem";
import { ApiProvider } from "../lib/api/provider";
import { type Auth, AuthProvider } from "../lib/auth/session";
import { I18nProvider } from "../lib/i18n/provider";
import type { Locale } from "../lib/i18n/locale";
import type { Draft } from "../lib/contracts";
import { openDraft, reduceDraft } from "../lib/state/draft";
import { zhHant } from "../messages/zh-Hant";
import { readFileSync } from "node:fs";
import { join } from "node:path";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const router = vi.hoisted(() => ({ refreshed: 0 }));
vi.mock("next/navigation", () => ({ useRouter: () => ({ refresh: () => { router.refreshed++; } }) }));

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const signedIn: Auth = { session: { accessToken: "t", organizationId: ORG }, role: "editor" };
const draft = JSON.parse(readFileSync(join(process.cwd(), "../../testdata/contracts/valid-draft-with-issues.json"), "utf8")) as Draft;

let roots: Root[] = [];
afterEach(() => {
  for (const r of roots) act(() => r.unmount());
  roots = [];
  document.body.innerHTML = "";
  document.cookie = "geminus_locale=; Max-Age=0; Path=/";
});

async function mount(node: ReactNode, locale: Locale = "zh-Hant"): Promise<HTMLElement> {
  const el = document.createElement("div");
  document.body.append(el);
  const root = createRoot(el);
  roots.push(root);
  await act(async () => root.render(<I18nProvider locale={locale}>{node}</I18nProvider>));
  return el;
}
const settle = () => act(async () => { for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0)); });

const problem = (p: Partial<Problem>): Problem => ({ ...networkProblem(""), ...p });

describe("zh-Hant", () => {
  it("renders the projects page and its states in Chinese", async () => {
    const api = createApiClient({
      baseUrl: "https://api.test",
      session: () => signedIn.session,
      fetch: (async () => new Response(JSON.stringify({ items: [{ id: "prj_01M4YDQK00TA00000000000001", name: "Pipeline", organization_id: ORG, created_at: "2026-10-15T08:00:00Z", created_by: "usr_01M4YDQK00TA00000000000001" }], next_cursor: "c2" }))) as typeof fetch,
    });
    const out = await mount(<AuthProvider value={{ session: null, role: null }}><ProjectsPage /></AuthProvider>);
    expect(out.querySelector("h1")?.textContent).toBe("專案");
    expect(out.textContent).toContain(zhHant["problem.heading.unauthenticated"]);
    const el = await mount(<AuthProvider value={signedIn}><ApiProvider client={api}><ProjectsPage /></ApiProvider></AuthProvider>);
    await settle();
    expect(el.textContent).toContain("Pipeline"); // user data, as entered
    expect(el.textContent).toMatch(/建立於 2026年10月15日/);
    expect(el.querySelector('[role="note"]')?.textContent).toBe("顯示前 1 個專案。還有更多專案，此版本尚無法翻頁。");
  });

  it("explains each error code in Chinese and keeps the server's English message as data", async () => {
    const el = await mount(<ProblemView problem={problem({ kind: "conflict", code: "MODEL_VERSION_CONFLICT", status: 409, message: "draft revision is stale", requestId: "req_01M4YDQK00TA00000000000001" })} />);
    expect(el.querySelector("h2")?.textContent).toBe(zhHant["problem.heading.conflict"]);
    expect(el.textContent).toContain(zhHant["error.MODEL_VERSION_CONFLICT"]);
    const server = el.querySelector('[lang="en"]');
    expect(server?.textContent).toBe("draft revision is stale");
    expect(el.textContent).toContain("請求 ID：");
  });

  it("names each validation issue by its code, and an unknown code by the server's reason", async () => {
    const el = await mount(<ProblemView problem={problem({ kind: "invalid", code: "MODEL_VALIDATION_FAILED", status: 422, details: [
      { path: "/flows/0/to", reason: "unknown node x", code: "UNKNOWN_REFERENCE", severity: "error" },
      { path: "/nodes/2", reason: "pack rule", code: "PACK_SPECIFIC_RULE", severity: "warning" },
    ] })} />);
    const items = [...el.querySelectorAll("li")];
    expect(items[0]?.textContent).toBe(`/flows/0/to ${zhHant["validation.UNKNOWN_REFERENCE"]}`);
    expect(items[1]?.querySelector('[lang="en"]')?.textContent).toBe("pack rule");
  });

  it("counts in Chinese", async () => {
    const edit = { op: "replace", path: "/name", value: "x" } as const;
    const s = reduceDraft(openDraft({ ...draft, revision: 3 }), { type: "edit", ops: [edit, edit] });
    const el = await mount(<AuthProvider value={signedIn}><DraftStatus session={s} onReload={async () => null} onDiscard={() => {}} /></AuthProvider>);
    expect(el.textContent).toContain("草稿修訂版 3。");
    expect(el.textContent).toContain("2 項變更尚未儲存。");
    const wait = await mount(<ProblemView problem={problem({ kind: "rate_limited", code: "RATE_LIMITED", status: 429, retryAfterSeconds: 30 })} />);
    expect(wait.textContent).toContain("請在 30 秒後重試。");
  });

  it("remembers a new choice in the language cookie only", async () => {
    let refreshed = 0;
    const el = await mount(<LocaleSwitcher onChanged={() => refreshed++} />, "en");
    const select = el.querySelector("select")!;
    expect(select.value).toBe("en");
    expect([...select.options].map((o) => [o.value, o.textContent])).toEqual([["en", "English"], ["zh-Hant", "繁體中文"]]);
    expect(el.querySelector("label")?.textContent).toBe("Language");
    await act(async () => {
      select.value = "zh-Hant";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(document.cookie).toContain("geminus_locale=zh-Hant");
    expect(refreshed).toBe(1);
  });

  it("re-renders the server components after a choice in the app shell", async () => {
    const el = await mount(<LocaleMenu />, "zh-Hant");
    const select = el.querySelector("select")!;
    expect(select.value).toBe("zh-Hant");
    expect(el.querySelector("label")?.textContent).toBe("語言");
    const before = router.refreshed;
    await act(async () => {
      select.value = "en";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(document.cookie).toContain("geminus_locale=en");
    expect(router.refreshed).toBe(before + 1);
  });
});
