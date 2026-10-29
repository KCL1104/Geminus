// @vitest-environment happy-dom
// T-031 / H-027 ("簡潔"; "只保留所有應該要包含的按鈕功能"): the proposal review
// page in every state, its in-place confirmations and outcomes, and the
// model page with its new entry point keep only the controls each needs,
// by the rules of tests/simplicity.test.tsx: at most one primary action,
// no disabled control without a stated reason (a blocked Approve names
// its visible reason), no two buttons (or links) with one name and no two
// links to one place, each view together with the app shell. Every state
// is checked here with a fake API and session (the browser checks the
// signed-in page through the BFF on the e2e fake API in proposal.spec.ts).
import { act } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SAMPLE_MODEL_ID } from "../app/ui-gallery/editor/sample-line";
import { type SampleState, sampleFor, states } from "../app/ui-gallery/proposal/samples";
import { createProposalStandIn, type ProposalStandInOptions } from "../app/ui-gallery/proposal/stand-in";
import { ProposalRoute } from "../features/ai-proposals/proposal-view";
import { ModelView } from "../features/model-editor/model-view";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider } from "../lib/auth/session";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { key, mount, press, settle } from "./ui-harness";

const jar = vi.hoisted(() => ({ locale: undefined as string | undefined }));
vi.mock("next/navigation", async (orig) => ({ ...(await orig<typeof import("next/navigation")>()), useRouter: () => ({ push: () => {}, refresh: () => {} }) }));
vi.mock("next/headers", () => ({
  cookies: async () => ({ get: (name: string) => (name === "geminus_locale" && jar.locale !== undefined ? { name, value: jar.locale } : undefined) }),
  // The root layout reads the request's nonce (T-057); none here.
  headers: async () => new Headers(),
}));
vi.setConfig({ testTimeout: 180_000 });
beforeEach(() => {
  jar.locale = undefined;
});

const locales: Locale[] = ["en", "zh-Hant"];
const primaries = (el: ParentNode) => el.querySelectorAll('[data-variant="primary"]').length;
const unexplained = (el: ParentNode) =>
  [...el.querySelectorAll("[disabled], [aria-disabled=true], [data-disabled]")].filter((c) => {
    const ids = (c.getAttribute("aria-describedby") ?? "").split(/\s+/).filter(Boolean);
    return !ids.some((id) => (c.ownerDocument.getElementById(id)?.textContent ?? "").trim() !== "");
  }).length;

type Control = { role: "button" | "link"; name: string; href: string | null };

function accessibleName(el: Element): string {
  const ids = el.getAttribute("aria-labelledby");
  const name = ids ? ids.split(/\s+/).map((id) => el.ownerDocument.getElementById(id)?.textContent ?? "").join(" ") : (el.getAttribute("aria-label") ?? el.textContent ?? "");
  return name.replace(/\s+/g, " ").trim();
}

function controls(root: ParentNode): Control[] {
  const out: Control[] = [];
  for (const el of root.querySelectorAll("a[href], [role=link], button, [role=button]")) {
    if (el.closest("[hidden], [aria-hidden=true], [inert]")) continue;
    const role = el.getAttribute("role") ?? (el.tagName === "A" ? "link" : "button");
    if (role !== "button" && role !== "link") continue;
    const href = el.getAttribute("href");
    out.push({ role, name: accessibleName(el), href: href === null || href.startsWith("#") ? null : href });
  }
  return out;
}

function duplicates(cs: Control[]): string[] {
  const keys = [...cs.map((c) => `${c.role} "${c.name}"`), ...cs.flatMap((c) => (c.href === null ? [] : [`link to ${c.href}`]))];
  return [...new Set(keys.filter((k, i) => keys.indexOf(k) !== i))].sort();
}

async function shellControls(locale: Locale): Promise<Control[]> {
  jar.locale = locale;
  const { default: RootLayout } = await import("../app/layout");
  const html = renderToStaticMarkup(await RootLayout({ children: null }));
  const c = controls(new DOMParser().parseFromString(`<!doctype html>${html}`, "text/html"));
  expect(c.map((x) => x.role), "the shell's controls were found").toContain("link");
  return c;
}

async function waitFor(check: () => boolean, what: string, ms = 10_000): Promise<void> {
  const until = Date.now() + ms;
  while (!check()) {
    if (Date.now() > until) throw new Error(`timed out waiting for ${what}`);
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
}

const text = (el: Element | null | undefined) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();
const button = (el: ParentNode, name: string) => {
  const b = [...el.querySelectorAll("button")].find((x) => text(x) === name);
  if (!b) throw new Error(`no button "${name}"`);
  return b;
};

async function proposal(sample: SampleState, locale: Locale, opts: Omit<ProposalStandInOptions, "sample"> = {}) {
  const api = createProposalStandIn({ sample, ...opts });
  const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
  const el = await mount(
    <AuthProvider value={{ session: { organizationId: api.organizationId }, role: sampleFor(sample).role ?? "editor" }}>
      <ApiProvider client={client}>
        <ProposalRoute proposalId={api.proposalId!} pollMs={60_000} />
      </ApiProvider>
    </AuthProvider>,
    locale,
  );
  await waitFor(() => el.querySelector("[data-proposal-status]") !== null, "the proposal");
  await settle();
  return el;
}

async function check(el: HTMLElement, locale: Locale, offered: string[]) {
  const c = [...(await shellControls(locale)), ...controls(el)];
  // What the state offers is really there, so the rule is not vacuous.
  expect(c.map((x) => x.name)).toEqual(expect.arrayContaining(offered));
  expect(duplicates(c)).toEqual([]);
  expect(primaries(el), "primary actions").toBeLessThanOrEqual(1);
  expect(unexplained(el), "disabled controls without a stated reason").toBe(0);
}

for (const locale of locales) {
  const t = createTranslator(locale);
  // H-042: no back link on the page; the rail is the way out, and its Projects link is always in the shell.
  const back = t("nav.projects");
  describe(`${locale}: the proposal review keeps only the controls it needs (H-027)`, () => {
    const offered: Record<SampleState, string[]> = {
      ready: [t("proposal.approve.edit", { version: "2" }), t("proposal.reject"), back],
      scenario: [t("proposal.approve.scenario"), t("proposal.reject"), back],
      "new-steps": [t("proposal.approve.edit", { version: "2" }), t("proposal.reject"), back],
      photo: [t("proposal.approve.edit", { version: "2" }), t("proposal.reject"), back],
      generating: [back],
      needs_input: [t("proposal.reject"), back],
      invalid: [back],
      failed: [back],
      conflict: [t("proposal.approve.edit", { version: "2" }), back],
      stale: [t("proposal.reject"), back],
      approved: [back],
      rejected: [back],
      conflicted: [back],
      hostile: [t("proposal.approve.edit", { version: "2" }), back],
      viewer: [back],
    };
    it.each([...states])("%s, inside the app shell", async (sample) => {
      const el = await proposal(sample, locale);
      await check(el, locale, offered[sample]);
      // The review never blocks a control: what cannot be done is not offered (no disabled-forever control).
      expect(el.querySelectorAll("[disabled], [aria-disabled=true], [data-disabled]"), "disabled controls").toHaveLength(0);
    });

    it("the approve confirmation, the reject confirmation and each outcome", async () => {
      const approve = t("proposal.approve.edit", { version: "2" });
      const confirm = t("proposal.approve.confirm.edit", { version: "2" });
      let el = await proposal("ready", locale);
      await press(button(el, approve));
      await check(el, locale, [confirm, t("proposal.cancel")]);
      await press(button(el, confirm));
      await waitFor(() => el.querySelector("[data-outcome]") !== null, "the edit outcome");
      await check(el, locale, [back]);

      el = await proposal("scenario", locale);
      await press(button(el, t("proposal.approve.scenario")));
      await press(button(el, t("proposal.approve.confirm.scenario")));
      await waitFor(() => el.querySelector("[data-outcome]") !== null, "the scenario outcome");
      await check(el, locale, [t("proposal.outcome.openScenario"), back]);

      el = await proposal("ready", locale);
      await press(button(el, t("proposal.reject")));
      await check(el, locale, [t("proposal.reject.confirm"), t("proposal.cancel")]);
      await press(button(el, t("proposal.reject.confirm")));
      await waitFor(() => el.querySelector("[data-outcome]") !== null, "the reject outcome");
      await check(el, locale, [back]);

      el = await proposal("conflict", locale);
      await press(button(el, approve));
      await press(button(el, confirm));
      await waitFor(() => el.querySelector("[data-conflict]") !== null, "the conflict");
      await check(el, locale, [back]);

      el = await proposal("ready", locale, { refuseNext: { status: 503, code: "SERVICE_UNAVAILABLE", message: "down" } });
      await press(button(el, approve));
      await press(button(el, confirm));
      await waitFor(() => el.querySelector("[role=alert]") !== null, "the problem");
      await check(el, locale, [approve, t("proposal.reject")]);

      // Refused on the server's recheck (D-069 B1, review round 1 L4): Reject only, nothing disabled.
      for (const refuseNext of [
        { status: 422, code: "MODEL_VALIDATION_FAILED", message: "the proposal does not pass the server's review again; nothing was changed" },
        { status: 409, code: "LIFECYCLE_CONFLICT", message: "the server's risk changed", details: [{ path: "/server_risk_level", reason: "the server now computes high, not medium" }] },
      ]) {
        el = await proposal("ready", locale, { refuseNext });
        await press(button(el, approve));
        await press(button(el, confirm));
        await waitFor(() => el.querySelector("[data-refused]") !== null, `the refusal (${refuseNext.status})`);
        await check(el, locale, [t("proposal.reject"), back]);
        expect(controls(el).map((c) => c.name), "Approve is not offered again").not.toContain(approve);
        expect(el.querySelectorAll("[disabled], [aria-disabled=true], [data-disabled]"), "disabled controls").toHaveLength(0);
      }
    });

    it("the model page with the entry point: clean, with unsaved edits, refused and on a 409", async () => {
      const page = async (opts: Omit<ProposalStandInOptions, "sample"> = {}) => {
        const api = createProposalStandIn({ sample: null, ...opts });
        const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
        const el = await mount(
          <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
            <ApiProvider client={client}>
              <ModelView modelId={SAMPLE_MODEL_ID} onProposal={() => {}} />
            </ApiProvider>
          </AuthProvider>,
          locale,
        );
        await waitFor(() => el.querySelector("[data-ask]") !== null, "the model page");
        await settle();
        return { el, api };
      };
      const intent = async (el: HTMLElement, value: string) => {
        const label = [...el.querySelectorAll("[data-ask] label")].find((l) => text(l) === t("ask.intent"))!;
        const input = document.getElementById((label as HTMLLabelElement).htmlFor) as HTMLTextAreaElement;
        await act(async () => {
          Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, value);
          input.dispatchEvent(new Event("input", { bubbles: true }));
        });
      };
      const send = t("ask.send");
      let { el } = await page();
      await check(el, locale, [send, t("editor.publish"), t("editor.save")]);

      ({ el } = await page({ refuseNext: { status: 503, code: "AI_POLICY_NOT_APPROVED", message: "off" } }));
      await intent(el, "Burn-in batches of 48 boards");
      await press(button(el, send));
      await waitFor(() => el.querySelector("[data-ask] [role=alert]") !== null, "the refusal");
      await check(el, locale, [send]);

      const moved = await page();
      el = moved.el;
      moved.api.publishOther();
      await intent(el, "Burn-in batches of 48 boards");
      await press(button(el, send));
      await waitFor(() => el.querySelector("[data-ask] [role=alert]") !== null, "the conflict");
      await check(el, locale, [send, t("ask.reload")]);

      ({ el } = await page());
      const n = el.querySelector<HTMLElement>('.react-flow__node[data-id="smt"]')!;
      await act(async () => n.focus());
      await key(n, "Enter");
      await settle();
      const nameLabel = [...el.querySelectorAll("[data-panel=properties] label")].find((l) => text(l) === t("editor.field.name"))!;
      const name = document.getElementById((nameLabel as HTMLLabelElement).htmlFor) as HTMLInputElement;
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(name, "SMT line 1");
        name.dispatchEvent(new Event("input", { bubbles: true }));
      });
      await key(name, "Enter");
      await settle();
      expect(button(el, send).getAttribute("aria-disabled")).toBe("true");
      await check(el, locale, [send, t("draft.discard")]);
    });

    // T-056: the electronics line's model page with the photo entry point
    // too: one button for it, which is not primary; blocked with its reason
    // while edits are unsaved; and after a refused photo, still one of each.
    it("the electronics line's model page with the photo entry point: clean, refused and with unsaved edits", async () => {
      const page = async (opts: Omit<ProposalStandInOptions, "sample"> = {}) => {
        const api = createProposalStandIn({ sample: null, ...opts });
        const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
        const el = await mount(
          <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
            <ApiProvider client={client}>
              <ModelView modelId={SAMPLE_MODEL_ID} onProposal={() => {}} />
            </ApiProvider>
          </AuthProvider>,
          locale,
        );
        await waitFor(() => el.querySelector("[data-photo]") !== null, "the model page");
        await settle();
        return { el, api };
      };
      const choose = t("photo.choose");
      let { el } = await page();
      await check(el, locale, [choose, t("ask.send"), t("editor.publish"), t("editor.save")]);
      expect(button(el, choose).getAttribute("data-variant")).toBe("secondary");
      expect(el.querySelectorAll("[data-photo] button"), "one control for the photo").toHaveLength(1);

      ({ el } = await page());
      const input = el.querySelector<HTMLInputElement>("[data-photo] input[type=file]")!;
      await act(async () => {
        Object.defineProperty(input, "files", { configurable: true, value: [new File([new Uint8Array([0x47, 0x49, 0x46, 0x38])], "x.gif", { type: "image/gif" })] });
        input.dispatchEvent(new Event("change", { bubbles: true }));
      });
      await waitFor(() => el.querySelector("[data-photo] [role=alert]") !== null, "the refusal");
      await check(el, locale, [choose, t("ask.send")]);

      ({ el } = await page());
      const n = el.querySelector<HTMLElement>('.react-flow__node[data-id="smt"]')!;
      await act(async () => n.focus());
      await key(n, "Enter");
      await settle();
      const nameLabel = [...el.querySelectorAll("[data-panel=properties] label")].find((l) => text(l) === t("editor.field.name"))!;
      const name = document.getElementById((nameLabel as HTMLLabelElement).htmlFor) as HTMLInputElement;
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(name, "SMT line 1");
        name.dispatchEvent(new Event("input", { bubbles: true }));
      });
      await key(name, "Enter");
      await settle();
      expect(button(el, choose).getAttribute("aria-disabled")).toBe("true");
      expect(el.querySelector("[data-photo] input[type=file]"), "a blocked photo opens no chooser").toBeNull();
      await check(el, locale, [choose, t("draft.discard")]);
    });
  });
}

// The rule can fail: a blocked control with no reason is found.
describe("the checks themselves", () => {
  it("count a blocked control without a reason, and a repeated name", async () => {
    const el = await mount(
      <>
        <button aria-disabled="true">x</button>
        <a href="/a">a</a>
        <a href="/a">b</a>
      </>,
    );
    expect(unexplained(el)).toBe(1);
    expect(duplicates(controls(el))).toEqual(["link to /a"]);
  });
});
