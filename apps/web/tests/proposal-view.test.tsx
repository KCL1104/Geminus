// @vitest-environment happy-dom
// T-031 (hackathon slice, H-029, H-031): the AI proposal review page with
// the app's own API client against the in-page stand-in for the proposal
// API (app/ui-gallery/proposal/stand-in.ts: it keeps versions, the draft,
// proposals and scenarios, answers stale bases with 409 and decided
// proposals as the server does, and reviews nothing: each sample's
// validation and risk are given). Every state of proposal.schema.json;
// the server's risk and checks beside the AI's own judgement, labelled;
// approve edit and approve scenario as separate actions with separate
// effects, reject with an optional reason; a stale base keeps the page and
// says what changed, never rebasing; the AI's words always as text; axe.
import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { HOSTILE_TESTER, hostileNames, type SampleState, sampleFor, SAMPLE_PROPOSAL_ID } from "../app/ui-gallery/proposal/samples";
import { createProposalStandIn, type ProposalStandIn, type ProposalStandInOptions } from "../app/ui-gallery/proposal/stand-in";
import { sampleLine } from "../app/ui-gallery/editor/sample-line";
import { ChangeList } from "../features/ai-proposals/change-list";
import { ProposalRoute } from "../features/ai-proposals/proposal-view";
import { visible } from "../features/ai-proposals/shown";
import { createApiClient } from "../lib/api/client";
import type { JsonPatch } from "../lib/contracts";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider, type Role } from "../lib/auth/session";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { mount, press, settle, unmountAll, violations } from "./ui-harness";

vi.setConfig({ testTimeout: 120_000 });

const en = createTranslator("en");
const zh = createTranslator("zh-Hant");

type Opts = Omit<ProposalStandInOptions, "sample"> & { role?: Role | null; locale?: Locale };

async function open(sample: SampleState, opts: Opts = {}): Promise<{ el: HTMLElement; api: ProposalStandIn }> {
  const api = createProposalStandIn({ sample, ...opts });
  const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
  const role = opts.role === undefined ? (sampleFor(sample).role ?? "editor") : opts.role;
  const el = await mount(
    <AuthProvider value={{ session: { organizationId: api.organizationId }, role }}>
      <ApiProvider client={client}>
        <ProposalRoute proposalId={api.proposalId ?? SAMPLE_PROPOSAL_ID} pollMs={25} />
      </ApiProvider>
    </AuthProvider>,
    opts.locale ?? "en",
  );
  await settle();
  await waitFor(() => el.querySelector("h1, [role=alert]") !== null, "the proposal page");
  return { el, api };
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
const buttons = (el: ParentNode) => [...el.querySelectorAll<HTMLButtonElement>("button")];
const names = (el: ParentNode) => buttons(el).map((b) => text(b));
const button = (el: ParentNode, name: string) => {
  const b = buttons(el).find((x) => text(x) === name);
  if (!b) throw new Error(`no button "${name}" among ${names(el).join(" | ")}`);
  return b;
};
const status = (el: Element) => el.querySelector("[data-proposal-status]");
const section = (el: Element, name: string) => el.querySelector(`[data-section="${name}"]`);
const decisions = (api: ProposalStandIn) => api.calls.filter((c) => c.method === "POST");
async function type(input: HTMLInputElement | HTMLTextAreaElement, value: string) {
  await act(async () => {
    input.focus();
    const proto = input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await settle();
}
const field = (el: Element, label: string) => {
  const l = [...el.querySelectorAll("label")].find((x) => text(x) === label);
  if (!l) throw new Error(`no field ${label}`);
  return document.getElementById(l.htmlFor) as HTMLInputElement | HTMLTextAreaElement;
};

describe("a ready edit proposal", () => {
  it("shows the server's risk and checks beside the AI's own judgement, clearly labelled, with no violation", async () => {
    const { el } = await open("ready");
    const s = sampleFor("ready").proposal;
    expect(text(el.querySelector("h1"))).toBe(en("proposal.title.edit", { model: "Board assembly and test line (sample)" }));
    expect(text(status(el))).toBe(en("proposal.status.ready"));
    expect(text(el)).toContain(s.summary!);
    const server = el.querySelector('[data-cell="server"]')!;
    const ai = el.querySelector('[data-cell="ai"]')!;
    // The authority is the server's; the AI's claim and confidence are its own, shown apart.
    expect(text(server.querySelector("h3"))).toBe(en("proposal.risk.server"));
    expect(text(server)).toContain(en("proposal.risk.level.medium"));
    expect(text(server)).toContain(en("proposal.checks.pass", { version: "1" }));
    expect(text(ai.querySelector("h3"))).toBe(en("proposal.risk.ai"));
    expect(text(ai)).toContain(en("proposal.risk.level.low"));
    expect(text(ai)).toContain(en("proposal.risk.confidence", { percent: "90%" }));
    expect(text(ai)).not.toContain(en("proposal.risk.level.medium"));
    expect(text(section(el, "risk"))).toContain(en("proposal.risk.counted.medium"));
    expect(await violations()).toEqual([]);
  });

  it("shows the diff in human terms by step, with the exact JSON Patch and its hash behind a disclosure", async () => {
    const { el } = await open("ready");
    const s = sampleFor("ready").proposal;
    const changes = section(el, "changes")!;
    expect(text(changes.querySelector("h2"))).toBe(en("proposal.changes", { version: "1" }));
    const lines = [...changes.querySelectorAll("[data-change]")].map(text);
    expect(lines).toEqual([
      `Burn-in ${en("proposal.change.value", { field: en("editor.batch.min_size"), before: "20", after: "48" })}`,
      `Burn-in ${en("proposal.change.value", { field: en("editor.batch.max_size"), before: "40", after: "48" })}`,
    ]);
    const details = changes.querySelector("details")!;
    expect(details.open).toBe(false);
    expect(text(details.querySelector("summary"))).toBe(en("proposal.changes.patch"));
    expect(JSON.parse(details.querySelector("pre")!.textContent!)).toEqual(s.changes);
    expect(text(details)).toContain(s.patch_hash!);
  });

  it("lists the AI's assumptions and expectations as the AI's", async () => {
    const { el } = await open("ready");
    const s = sampleFor("ready").proposal;
    expect(text(section(el, "assumptions")?.querySelector("h2"))).toBe(en("proposal.assumptions"));
    expect([...section(el, "assumptions")!.querySelectorAll("li")].map(text)).toEqual(s.assumptions);
    expect(text(section(el, "effects")?.querySelector("h2"))).toBe(en("proposal.effects"));
    expect([...section(el, "effects")!.querySelectorAll("li")].map(text)).toEqual(s.expected_effects);
  });

  it("approves only after the person confirms, with the reviewed base and a key, and shows the new version", async () => {
    const { el, api } = await open("ready");
    const approve = en("proposal.approve.edit", { version: "2" });
    expect(names(el)).toEqual(expect.arrayContaining([approve, en("proposal.reject")]));
    // Reading and polling never decide anything (the AI never approves).
    expect(decisions(api)).toEqual([]);
    await press(button(el, approve));
    expect(text(el)).toContain(en("proposal.approve.question.edit", { version: "2", base: "1" }));
    expect(decisions(api)).toEqual([]);
    expect(names(el)).toEqual(expect.arrayContaining([approve, en("proposal.cancel")]));
    expect(names(el)).not.toContain(en("proposal.reject"));
    await press(button(el, approve));
    await waitFor(() => text(el).includes(en("proposal.outcome.edit", { version: "2" })), "the outcome");
    const [call] = decisions(api);
    expect([call!.path, call!.body]).toEqual([`/api/v1/ai-proposals/${api.proposalId}/approve`, { expected_base_version_id: api.baseVersionId }]);
    expect(call!.key).toMatch(/^[\x21-\x7e]{8,128}$/);
    // The page re-reads the proposal and the model: the server says what happened.
    expect(text(status(el))).toMatch(new RegExp(`^${en("proposal.status.approved", { date: "XX" }).replace("XX", ".+")}$`));
    expect(text(el)).toContain(en("proposal.latest.newer", { version: "2" }));
    expect(api.versions().map((v) => v.version)).toEqual([1, 2]);
    expect(names(el)).not.toContain(approve);
    expect(names(el)).not.toContain(en("proposal.reject"));
    expect(document.activeElement?.closest("[data-outcome]")).not.toBeNull();
    expect(await violations()).toEqual([]);
  });

  it("goes back without a request when the person cancels", async () => {
    const { el, api } = await open("ready");
    const approve = en("proposal.approve.edit", { version: "2" });
    await press(button(el, approve));
    await press(button(el, en("proposal.cancel")));
    expect(decisions(api)).toEqual([]);
    expect(names(el)).toEqual(expect.arrayContaining([approve, en("proposal.reject")]));
    expect(document.activeElement).toBe(button(el, approve));
  });

  it("sends one approval when the confirmation is pressed twice", async () => {
    const { el, api } = await open("ready", { holdDecisions: true });
    const approve = en("proposal.approve.edit", { version: "2" });
    await press(button(el, approve));
    const confirm = button(el, approve);
    await press(confirm);
    await press(confirm);
    expect(text(el)).toContain(en("proposal.approving"));
    api.release();
    await waitFor(() => text(el).includes(en("proposal.outcome.edit", { version: "2" })), "the outcome");
    expect(decisions(api)).toHaveLength(1);
  });

  it("says when the model's draft had unpublished changes and was kept as it was", async () => {
    const { el } = await open("ready", { dirtyDraft: true });
    const approve = en("proposal.approve.edit", { version: "2" });
    await press(button(el, approve));
    await press(button(el, approve));
    await waitFor(() => text(el).includes(en("proposal.outcome.edit", { version: "2" })), "the outcome");
    expect(text(el)).toContain(en("proposal.outcome.draftKept"));
  });

  it("shows a refused approval's problem, keeps the proposal open, and retries with the same key", async () => {
    const { el, api } = await open("ready", { refuseNext: { status: 503, code: "SERVICE_UNAVAILABLE", message: "the dataset snapshots could not be read; nothing was changed (retry later)", details: [{ path: "", reason: "the dataset storage failed", code: "DATASET_STORAGE_FAILED" }] } });
    const approve = en("proposal.approve.edit", { version: "2" });
    await press(button(el, approve));
    await press(button(el, approve));
    await waitFor(() => el.querySelector('[role=alert][data-problem="unavailable"]') !== null, "the problem");
    expect(text(el.querySelector("[role=alert]"))).toContain(en("error.SERVICE_UNAVAILABLE"));
    expect(text(el.querySelector("[role=alert] details"))).toContain("req_01M4YDQK00V6H8S0ST4F7510KR");
    // Still ready: the decision is still there.
    await press(button(el, approve));
    await press(button(el, approve));
    await waitFor(() => text(el).includes(en("proposal.outcome.edit", { version: "2" })), "the outcome");
    const keys = decisions(api).map((c) => c.key);
    expect(keys).toHaveLength(2);
    expect(keys[0]).toBe(keys[1]);
  });

  // D-069 (b) B1: an approval the server refuses on its own recheck leaves
  // the proposal ready, but it can never be approved as it is (T-031 review
  // round 1 L4): Approve is not offered again; a note says why and what to
  // do (reject, ask again), and Reject stays.
  it("after a failed revalidation (422) shows why, approves nothing, and offers Reject, not Approve again", async () => {
    const { el, api } = await open("ready", { refuseNext: { status: 422, code: "MODEL_VALIDATION_FAILED", message: "the proposal does not pass the server's review again; nothing was changed", details: [{ path: "/dataset_bindings/0/content_hash", reason: "x", code: "DATASET_BINDING_INVALID", severity: "error" }] } });
    const approve = en("proposal.approve.edit", { version: "2" });
    await press(button(el, approve));
    await press(button(el, approve));
    await waitFor(() => el.querySelector("[role=alert]") !== null, "the problem");
    expect(text(el.querySelector("[role=alert]"))).toContain(en("validation.DATASET_BINDING_INVALID"));
    expect(api.versions()).toHaveLength(1);
    expect(names(el)).not.toContain(approve);
    expect(names(el)).toContain(en("proposal.reject"));
    const note = section(el, "decision")?.querySelector("[data-refused]");
    expect(note?.getAttribute("data-refused")).toBe("checks");
    expect(text(note?.querySelector("[role=note]"))).toBe(en("proposal.refused.checks"));
    expect(document.activeElement?.closest("[data-refused]")).not.toBeNull();
    expect(text(el)).not.toContain(en("proposal.effect.edit", { version: "2" }));
    expect(el.querySelectorAll("[aria-disabled=true], [disabled]")).toHaveLength(0);
    expect(await violations()).toEqual([]);
    // Reject and ask again: the one way on.
    await press(button(el, en("proposal.reject")));
    await press(button(el, en("proposal.reject.confirm")));
    await waitFor(() => text(el).includes(en("proposal.outcome.rejected")), "the outcome");
    expect(decisions(api).map((c) => c.path)).toEqual([`/api/v1/ai-proposals/${api.proposalId}/approve`, `/api/v1/ai-proposals/${api.proposalId}/reject`]);
  });

  for (const path of ["/server_risk_level", "/risk_level"]) {
    it(`after a 409 because the server's risk changed (${path}) shows why, keeps the reviewed page and offers Reject, not Approve again`, async () => {
      const { el, api } = await open("ready", {
        refuseNext: { status: 409, code: "LIFECYCLE_CONFLICT", message: "the server's risk of this proposal is not the one it was reviewed with; nothing was changed (ask for a new proposal)", details: [{ path, reason: "the server now computes high, not medium" }] },
      });
      const approve = en("proposal.approve.edit", { version: "2" });
      await press(button(el, approve));
      await press(button(el, approve));
      await waitFor(() => el.querySelector("[data-refused]") !== null, "the refusal note");
      const note = section(el, "decision")!.querySelector("[data-refused]")!;
      expect(note.getAttribute("data-refused")).toBe("risk");
      expect(text(note.querySelector("[role=note]"))).toBe(en("proposal.refused.risk"));
      // Why, as the server said it; the page re-read what the server holds (still ready, nothing applied).
      expect(text(el.querySelector("[role=alert]"))).toContain("the server now computes high, not medium");
      expect(text(status(el))).toBe(en("proposal.status.ready"));
      const post = api.calls.findIndex((c) => c.method === "POST");
      expect(api.calls.slice(post + 1).map((c) => c.path)).toContain(`/api/v1/ai-proposals/${api.proposalId}`);
      expect(names(el)).not.toContain(approve);
      expect(names(el)).toContain(en("proposal.reject"));
      expect(section(el, "changes")?.querySelectorAll("[data-change]").length).toBe(2);
      expect(decisions(api)).toHaveLength(1);
      expect(api.versions()).toHaveLength(1);
      expect(document.activeElement?.closest("[data-refused]")).not.toBeNull();
      expect(await violations()).toEqual([]);
    });
  }
});

describe("approve scenario: a separate action with a separate effect", () => {
  it("saves a scenario and links to it to run, and makes no version", async () => {
    const { el, api } = await open("scenario");
    expect(text(el.querySelector("h1"))).toBe(en("proposal.title.scenario", { model: "Board assembly and test line (sample)" }));
    expect(text(el)).toContain(en("proposal.effect.scenario", { version: "1" }));
    // The AI claims more than the server computes: the counted risk is the higher, the AI's (D-027), and each cell keeps its own.
    expect(text(el.querySelector('[data-cell="server"]'))).toContain(en("proposal.risk.level.low"));
    expect(text(el.querySelector('[data-cell="ai"]'))).toContain(en("proposal.risk.level.medium"));
    expect(text(section(el, "risk"))).toContain(en("proposal.risk.counted.medium"));
    const approve = en("proposal.approve.scenario");
    expect(names(el)).not.toContain(en("proposal.approve.edit", { version: "2" }));
    await press(button(el, approve));
    expect(text(el)).toContain(en("proposal.approve.question.scenario", { base: "1" }));
    await press(button(el, approve));
    await waitFor(() => text(el).includes(en("proposal.outcome.scenario")), "the outcome");
    const link = [...el.querySelectorAll("a")].find((a) => text(a) === en("proposal.outcome.openScenario"));
    expect(link?.getAttribute("href")).toBe(`/scenarios/${api.scenarios()[0]!.id}`);
    expect(api.versions()).toHaveLength(1);
    expect(await violations()).toEqual([]);
  });
});

describe("reject, with an optional reason", () => {
  it("rejects with the reason the person typed", async () => {
    const { el, api } = await open("ready");
    await press(button(el, en("proposal.reject")));
    expect(text(el)).toContain(en("proposal.reject.question"));
    await type(field(el, en("proposal.reject.reason")), "Batches stay at 20–40 until the chamber is replaced.");
    expect(await violations()).toEqual([]);
    await press(button(el, en("proposal.reject.confirm")));
    await waitFor(() => text(el).includes(en("proposal.outcome.rejected")), "the outcome");
    expect(decisions(api).map((c) => [c.path, c.body])).toEqual([[`/api/v1/ai-proposals/${api.proposalId}/reject`, { comment: "Batches stay at 20–40 until the chamber is replaced." }]]);
    expect(text(el)).toContain("Batches stay at 20–40 until the chamber is replaced.");
    expect(api.versions()).toHaveLength(1);
  });

  it("rejects without a reason as an empty body", async () => {
    const { el, api } = await open("ready");
    await press(button(el, en("proposal.reject")));
    await press(button(el, en("proposal.reject.confirm")));
    await waitFor(() => text(el).includes(en("proposal.outcome.rejected")), "the outcome");
    expect(decisions(api)[0]!.body).toEqual({});
  });

  // D-011 (review round 1 L3): a retry of the same rejection is the same
  // request, with the same key; another reason is another request.
  const unavailable = { status: 503, code: "SERVICE_UNAVAILABLE", message: "the API is busy; nothing was changed" };
  const rejectWith = async (el: HTMLElement, reason: string) => {
    await press(button(el, en("proposal.reject")));
    await type(field(el, en("proposal.reject.reason")), reason);
    await press(button(el, en("proposal.reject.confirm")));
  };

  it("retries a refused rejection with the same key", async () => {
    const { el, api } = await open("ready", { refuseNext: unavailable });
    await rejectWith(el, "Not now.");
    await waitFor(() => el.querySelector('[role=alert][data-problem="unavailable"]') !== null, "the problem");
    await rejectWith(el, "Not now.");
    await waitFor(() => text(el).includes(en("proposal.outcome.rejected")), "the outcome");
    const calls = decisions(api);
    expect(calls.map((c) => c.body)).toEqual([{ comment: "Not now." }, { comment: "Not now." }]);
    expect(calls[0]!.key).toMatch(/^[\x21-\x7e]{8,128}$/);
    expect(calls[1]!.key).toBe(calls[0]!.key);
  });

  it("sends a changed reason as a new request, with a new key", async () => {
    const { el, api } = await open("ready", { refuseNext: unavailable });
    await rejectWith(el, "Not now.");
    await waitFor(() => el.querySelector('[role=alert][data-problem="unavailable"]') !== null, "the problem");
    await rejectWith(el, "Not until the racks arrive.");
    await waitFor(() => text(el).includes(en("proposal.outcome.rejected")), "the outcome");
    const calls = decisions(api);
    expect(calls.map((c) => c.body)).toEqual([{ comment: "Not now." }, { comment: "Not until the racks arrive." }]);
    expect(calls[1]!.key).not.toBe(calls[0]!.key);
  });
});

describe("a stale base", () => {
  it("keeps the page when the model moved just before approving, says what changed, and does not rebase", async () => {
    const { el, api } = await open("conflict");
    const approve = en("proposal.approve.edit", { version: "2" });
    await press(button(el, approve));
    await press(button(el, approve));
    await waitFor(() => el.querySelector("[data-conflict]") !== null, "the conflict note");
    const note = el.querySelector("[data-conflict]")!;
    expect(text(note)).toContain(en("proposal.conflict.title"));
    expect(text(note)).toContain(en("proposal.conflict.body", { base: "1", latest: "2" }));
    expect(text(note)).toContain(api.versions()[1]!.comment!);
    // The note is the answer to the 409: no error alert beside it.
    expect(el.querySelector("[role=alert]")).toBeNull();
    // The person's place: the diff they reviewed is still on the page.
    expect(section(el, "changes")?.querySelectorAll("[data-change]").length).toBe(2);
    expect(text(status(el))).toBe(en("proposal.status.conflicted"));
    expect(document.activeElement?.closest("[data-conflict]")).not.toBeNull();
    // Nothing applied, nothing asked again on the new version by itself:
    // one approval, on the reviewed base, and no other (review round 1 L2).
    expect(api.versions().map((v) => v.version)).toEqual([1, 2]);
    expect(api.calls.filter((c) => c.path.endsWith("/ai-proposals"))).toEqual([]);
    expect(decisions(api).map((c) => [c.path, c.body])).toEqual([[`/api/v1/ai-proposals/${api.proposalId}/approve`, { expected_base_version_id: api.baseVersionId }]]);
    await act(async () => {
      await new Promise((r) => setTimeout(r, 100));
    });
    expect(decisions(api)).toHaveLength(1);
    expect(names(el)).not.toContain(approve);
    expect(await violations()).toEqual([]);
  });

  it("does not offer Approve when the model has a newer version already (it could never be approved), and says why", async () => {
    const { el, api } = await open("stale");
    // A ready proposal's base never becomes the latest again: no disabled-forever control (H-027), no version number that exists.
    expect(names(el)).toEqual([en("proposal.reject")]);
    expect(text(el)).not.toContain(en("proposal.effect.edit", { version: "2" }));
    expect(text(section(el, "decision")?.querySelector("[role=note]"))).toBe(en("proposal.stale", { base: "1", latest: "2" }));
    expect(text(el)).toContain(en("proposal.latest.newer", { version: "2" }));
    expect(el.querySelectorAll("[aria-disabled=true]")).toHaveLength(0);
    expect(decisions(api)).toEqual([]);
    expect(await violations()).toEqual([]);
  });

  it("shows a proposal that is already conflicted as such", async () => {
    const { el } = await open("conflicted");
    expect(text(status(el))).toBe(en("proposal.status.conflicted"));
    expect(text(el.querySelector("[data-conflict]"))).toContain(en("proposal.conflict.body", { base: "1", latest: "2" }));
    expect(names(el)).toEqual([]);
  });
});

describe("the other states", () => {
  it("follows a generating proposal until it is ready, and never decides on its own", async () => {
    const { el, api } = await open("generating");
    expect(text(status(el))).toBe(en("proposal.status.generating"));
    expect(text(el)).toContain(en("proposal.generatingHint"));
    expect(await violations()).toEqual([]);
    await waitFor(() => text(status(el)) === en("proposal.status.ready"), "ready");
    expect(api.calls.filter((c) => c.path === `/api/v1/ai-proposals/${api.proposalId}`).length).toBeGreaterThan(1);
    expect(section(el, "changes")?.querySelectorAll("[data-change]").length).toBe(2);
    expect(decisions(api)).toEqual([]);
  });

  it("asks the AI's questions when it needs input; there is nothing to approve, it can be rejected", async () => {
    const { el } = await open("needs_input");
    const s = sampleFor("needs_input").proposal;
    expect(text(status(el))).toBe(en("proposal.status.needs_input"));
    expect(text(section(el, "questions")?.querySelector("h2"))).toBe(en("proposal.questions"));
    expect([...section(el, "questions")!.querySelectorAll("li")].map(text)).toEqual(s.questions);
    expect(text(section(el, "questions"))).toContain(en("proposal.questionsHint"));
    expect(section(el, "changes")).toBeNull();
    expect(names(el)).toEqual([en("proposal.reject")]);
    expect(await violations()).toEqual([]);
  });

  it("shows why the server refused the AI's answer (invalid), and offers no decision", async () => {
    const { el } = await open("invalid");
    expect(text(status(el))).toBe(en("proposal.status.invalid"));
    const server = el.querySelector('[data-cell="server"]')!;
    expect(text(server)).toContain(en("proposal.checks.failed", { count: 1 }));
    expect(text(server)).toContain(en("validation.ID_POOL_VIOLATION"));
    expect(el.querySelector('[data-cell="ai"]')).toBeNull();
    expect(text(el)).toContain(en("proposal.askAgain"));
    expect(names(el)).toEqual([]);
    expect(await violations()).toEqual([]);
  });

  it("shows a failed generation as the API's problem, with its request ID one click away", async () => {
    const { el } = await open("failed");
    expect(text(status(el))).toBe(en("proposal.status.failed"));
    const alert = el.querySelector("[role=alert]")!;
    expect(text(alert)).toContain(en("error.SERVICE_UNAVAILABLE"));
    expect(text(alert.querySelector("details"))).toContain("req_01M4YDQK00V6H8S0ST4F7510KR");
    expect(names(el)).toEqual([]);
    expect(await violations()).toEqual([]);
  });

  it("treats a failure whose code is not the API's own (an Object member such as toString) as a protocol problem (review round 1 I3)", async () => {
    for (const code of ["toString", "constructor", "__proto__"]) {
      const { el } = await open("failed", { proposal: { error: { error: { code: code as never, message: "x", request_id: "req_01M4YDQK00V6H8S0ST4F7510KR" } } } });
      const alert = el.querySelector("[role=alert]")!;
      expect(alert.getAttribute("data-problem"), code).toBe("protocol");
      expect(text(alert.querySelector("details")), code).toContain("The proposal failed without an API error.");
      unmountAll();
    }
  });

  it("shows an approved edit with the version it created, and an approved scenario with its link", async () => {
    const { el } = await open("approved");
    expect(text(status(el))).toMatch(new RegExp(`^${en("proposal.status.approved", { date: "XX" }).replace("XX", ".+")}$`));
    expect(text(el)).toContain(en("proposal.decided.version", { version: "2" }));
    expect(names(el)).toEqual([]);
  });

  it("shows a rejected proposal with its reason", async () => {
    const { el } = await open("rejected");
    const s = sampleFor("rejected").proposal;
    expect(text(status(el))).toMatch(new RegExp(`^${en("proposal.status.rejected", { date: "XX" }).replace("XX", ".+")}$`));
    expect(text(el)).toContain(s.decision_comment!);
    expect(names(el)).toEqual([]);
  });

  it("lets a viewer read but not decide", async () => {
    const { el } = await open("viewer");
    expect(text(el)).toContain(en("proposal.readOnly.viewer"));
    expect(names(el)).toEqual([]);
    expect(section(el, "changes")?.querySelectorAll("[data-change]").length).toBe(2);
  });

  it("renders new steps with their connections (a photo proposal, T-056) with the same components", async () => {
    const { el } = await open("new-steps");
    const items = [...section(el, "changes")!.querySelectorAll<HTMLElement>("[data-change]")];
    // The line itself, without what the new entity holds (its own list under it).
    const line = (li: HTMLElement) => {
      const copy = li.cloneNode(true) as HTMLElement;
      copy.querySelector("ul")?.remove();
      return text(copy);
    };
    // A new entity is named with its ID (review round 1 M1): its name is the AI's.
    const named = (name: string, id: string) => en("proposal.named", { name, id });
    expect(line(items[0]!)).toBe(`${en("proposal.change.added")} ${en("proposal.change.step", { name: named("X-ray inspection", "node_01m4ydqk00e1ectr0n1c5x0001"), type: en("editor.type.process") })} ${en("proposal.change.placed")}`);
    // What the new step holds: its time (in the model's unit) and the resource it uses, by name.
    expect([...items[0]!.querySelectorAll("ul li")].map(text)).toEqual([
      en("proposal.change.hold", { field: en("editor.timing.process"), value: en("proposal.value.distribution", { distribution: en("editor.dist.summary", { type: en("editor.dist.triangular"), values: "1.2, 1.5, 2.4" }), unit: en("editor.unit.minute") }) }),
      en("proposal.change.hold", { field: en("proposal.field.uses", { resource: named("X-ray machine", "resource_01m4ydqk00e1ectr0n1c5x0001") }), value: "1" }),
    ]);
    expect(items.map(line)).toContain(`${en("proposal.change.added")} ${en("proposal.change.connection", { from: "AOI", to: named("Before X-ray", "node_01m4ydqk00e1ectr0n1c5x0002") })}`);
    expect(await violations()).toEqual([]);
  });

  it("speaks Traditional Chinese, numbers and dates included", async () => {
    const { el } = await open("ready", { locale: "zh-Hant" });
    expect(text(el.querySelector("h1"))).toBe(zh("proposal.title.edit", { model: "Board assembly and test line (sample)" }));
    expect(names(el)).toEqual(expect.arrayContaining([zh("proposal.approve.edit", { version: "2" }), zh("proposal.reject")]));
    expect(text(el.querySelector('[data-cell="ai"]'))).toContain(zh("proposal.risk.confidence", { percent: "90%" }));
    expect(await violations()).toEqual([]);
  });

  it("says when the proposal does not exist (or is another organization's), and loads nothing more", async () => {
    const { el, api } = await open("ready", { missing: true });
    expect(el.querySelector('[role=alert][data-problem="not_found"]')).not.toBeNull();
    expect(api.calls.map((c) => c.path)).toEqual([`/api/v1/ai-proposals/${api.proposalId}`]);
  });
});

describe("the AI's words are data, never markup or links", () => {
  it("renders hostile summary, assumptions, effects and names as text, makes no link and decides nothing", async () => {
    const { el, api } = await open("hostile");
    const s = sampleFor("hostile").proposal;
    const page = text(el);
    for (const words of [s.summary!, ...s.assumptions!, ...s.expected_effects!]) expect(page).toContain(words.replace(/\s+/g, " ").trim());
    expect(el.querySelectorAll("img, script, iframe, object, embed, svg[onload], [onerror], [onclick], [style*='url']")).toHaveLength(0);
    // The page's own links only: back to the model. Nothing the AI wrote became one.
    expect([...el.querySelectorAll("a")].map((a) => a.getAttribute("href"))).toEqual([`/models/${api.modelId}`]);
    for (const a of el.querySelectorAll("[href]")) expect(a.getAttribute("href")).not.toMatch(/evil|javascript:/i);
    // "Approve" in the AI's words is not an approval; the server's risk stands.
    expect(decisions(api)).toEqual([]);
    expect(text(el.querySelector('[data-cell="server"]'))).toContain(en("proposal.risk.level.high"));
    expect(text(section(el, "risk"))).toContain(en("proposal.risk.counted.high"));
    expect(await violations()).toEqual([]);
  });

  it("renders the hostile names in its patch as text, each isolated, hidden characters shown, the look-alike tester by its ID (review round 1)", async () => {
    const { el } = await open("hostile");
    const changes = section(el, "changes")!;
    const lines = [...changes.querySelectorAll("[data-change]")].map((li) => {
      const copy = li.cloneNode(true) as Element;
      copy.querySelector("ul")?.remove();
      return text(copy);
    });
    const quoted = (name: string, id?: string) => (id === undefined ? en("proposal.value.text", { text: name }) : en("proposal.named", { name: en("proposal.value.text", { text: name }), id }));
    expect(lines).toEqual(expect.arrayContaining([
      `${en("proposal.change.added")} ${en("proposal.change.resource", { name: en("proposal.named", { name: hostileNames.lookalike, id: HOSTILE_TESTER }) })}`,
      `ICT ${en("proposal.change.value", { field: en("proposal.field.usesResource"), before: quoted("ICT tester", "ict_tester"), after: quoted(hostileNames.lookalike, HOSTILE_TESTER) })}`,
      `Burn-in ${en("proposal.change.value", { field: en("proposal.field.name"), before: quoted("Burn-in"), after: quoted(visible(hostileNames.reversed)) })}`,
      `AOI machine ${en("proposal.change.value", { field: en("proposal.field.name"), before: quoted("AOI machine"), after: quoted(visible(hostileNames.hidden)) })}`,
      `Pack and ship ${en("proposal.change.value", { field: en("proposal.field.name"), before: quoted("Pack and ship"), after: quoted(hostileNames.markup) })}`,
    ]));
    expect([...changes.querySelectorAll("bdi")].map((b) => b.textContent)).toEqual(expect.arrayContaining([hostileNames.markup, visible(hostileNames.reversed), visible(hostileNames.hidden)]));
    expect(el.querySelectorAll("img, script, iframe, object, embed, [onerror]")).toHaveLength(0);
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    for (let n = walker.nextNode(); n; n = walker.nextNode()) expect(n.textContent, "no raw format or control character").not.toMatch(/[\p{Cf}\p{Cs}\p{Zl}\p{Zp}\u0000-\u0008\u000B-\u001F\u007F-\u009F]/u);
  });
});

describe("how the changes' values read", () => {
  it("in the model's time unit, as a percentage, as no limit, and as the exact patch when it cannot be read", async () => {
    const at = (id: string) => sampleLine.nodes.findIndex((n) => n.id === id);
    const flow = sampleLine.flows.findIndex((f) => f.id === "ict_result_q_burn_in");
    const changes: JsonPatch = [
      { op: "replace", path: `/nodes/${at("ict")}/service_time/distribution/mode`, value: 2.5 },
      { op: "remove", path: `/nodes/${at("q_aoi")}/capacity` },
      { op: "replace", path: `/flows/${flow}/routing/probability`, value: 0.9 },
      { op: "remove", path: `/nodes/${at("burn_in")}/batch/max_wait` },
    ];
    const cases = [["en", en, ["2 minutes", "2.5 minutes", "120 minutes"]], ["zh-Hant", zh, ["2 分鐘", "2.5 分鐘", "120 分鐘"]]] as const;
    for (const [locale, t, minutes] of cases) {
      const el = await mount(<ChangeList base={sampleLine} version={1} changes={changes} patchHash={undefined} />, locale);
      expect([...el.querySelectorAll("[data-change]")].map(text)).toEqual([
        `ICT ${t("proposal.change.value", { field: t("proposal.field.param", { field: t("editor.timing.process"), param: t("editor.param.mode") }), before: minutes[0], after: minutes[1] })}`,
        `Before AOI ${t("proposal.change.value", { field: t("proposal.field.capacity"), before: "20", after: t("proposal.value.noLimit") })}`,
        `${t("proposal.change.connection", { from: "ICT result", to: "Before burn-in" })} ${t("proposal.change.value", { field: t("proposal.field.probability"), before: "95%", after: "90%" })}`,
        `Burn-in ${t("proposal.change.value", { field: t("editor.batch.max_wait"), before: minutes[2], after: t("proposal.value.noLimit") })}`,
      ]);
      unmountAll();
    }
    // A patch that does not apply to the base: no guessed list, the exact patch open.
    const el = await mount(<ChangeList base={sampleLine} version={1} changes={[{ op: "replace", path: "/nodes/99/name", value: "x" }]} patchHash={undefined} />);
    expect(el.querySelectorAll("[data-change]")).toHaveLength(0);
    expect(text(el)).toContain(en("proposal.changes.unreadable", { version: "1" }));
    expect(el.querySelector("details")?.open).toBe(true);
  });
});
