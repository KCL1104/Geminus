// @vitest-environment happy-dom
// H-052: the judge sees what SAM 3.1 found. On the page of a proposal asked
// for with a photo, the stored photo (getProposalImage, fetched with the
// tenant selector and shown as an object URL) with each box of
// generated_by.segmenter.boxes drawn over it at the photo's own pixels, its
// number and concept beside it, and the same objects as a numbered list
// for those who cannot see the picture. A proposal stored before the boxes
// were kept shows the photo alone; a photo that cannot be read says so
// quietly; a box that is not inside the photo is not drawn. The stand-in
// (app/ui-gallery/proposal/stand-in.ts) serves the synthetic demo floor plan
// and the fake segmenter's boxes; no AI is called.
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { act } from "react";
import { describe, expect, it, onTestFinished, vi } from "vitest";
import { samplePhotoBoxes, samplePhotoBytes } from "../app/ui-gallery/proposal/sample-photo";
import { samplePhoto } from "../app/ui-gallery/proposal/samples";
import { createProposalStandIn, type ProposalStandInOptions } from "../app/ui-gallery/proposal/stand-in";
import { drawable, labelPlace, ProposalPhoto } from "../features/ai-proposals/proposal-photo";
import { ProposalRoute } from "../features/ai-proposals/proposal-view";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider } from "../lib/auth/session";
import type { SegmentBox } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { mount, rerender, settle, unmountAll, violations } from "./ui-harness";

vi.setConfig({ testTimeout: 120_000 });

const en = createTranslator("en");
const zh = createTranslator("zh-Hant");

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
const section = (el: Element) => el.querySelector<HTMLElement>("[data-section=photo]");

type Open = Omit<ProposalStandInOptions, "sample"> & { sample?: ProposalStandInOptions["sample"]; locale?: Locale; image?: (r: Response) => Response | Promise<Response> };

/** The proposal page on the stand-in; image replaces the stand-in's answer to getProposalImage. */
async function open(opts: Open = {}) {
  const api = createProposalStandIn({ sample: "photo", ...opts });
  const seen: { path: string; headers: Headers }[] = [];
  const fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input), "http://stand-in.invalid").pathname;
    const r = await api.fetch(input, init);
    if (!path.endsWith("/image")) return r;
    seen.push({ path, headers: new Headers(init?.headers) });
    return opts.image ? opts.image(r) : r;
  }) as typeof globalThis.fetch;
  const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch });
  const el = await mount(
    <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
      <ApiProvider client={client}>
        <ProposalRoute proposalId={api.proposalId!} pollMs={60_000} />
      </ApiProvider>
    </AuthProvider>,
    opts.locale ?? "en",
  );
  await waitFor(() => el.querySelector("[data-proposal-status]") !== null, "the proposal");
  await settle();
  return { el, api, seen };
}

const shown = (el: Element) => section(el)?.querySelector<HTMLImageElement>("img[src^='blob:']") ?? null;

/** Makes the photo's frame measure `width` CSS pixels wide (happy-dom lays nothing out), until the test ends. */
function drawnAt(width: number) {
  const original = HTMLElement.prototype.getBoundingClientRect;
  const spy = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    return this.tagName === "FIGURE" ? ({ x: 0, y: 0, top: 0, left: 0, right: width, bottom: 0, width, height: 0, toJSON: () => ({}) } as DOMRect) : original.call(this);
  });
  onTestFinished(() => spy.mockRestore());
}

/** A promise and what releases it. */
function gate(): { held: Promise<void>; release: () => void } {
  let release = () => {};
  const held = new Promise<void>((r) => (release = r));
  return { held, release };
}

describe("the photo and what SAM 3.1 found (H-052)", () => {
  it("draws each box over the photo at its pixels, with its number and concept, and lists the objects", async () => {
    drawnAt(600);
    const { el, api, seen } = await open();
    await waitFor(() => shown(el) !== null, "the photo");
    const s = section(el)!;
    expect(text(s.querySelector("h2"))).toBe(en("proposal.photo"));
    expect(text(s)).toContain(en("proposal.photo.boxesHint", { model: "SAM 3.1" }));
    // The stored photo, read with the tenant selector, at its own size.
    expect(seen.map((x) => [x.path, x.headers.get("X-Organization-ID")])).toEqual([[`/api/v1/ai-proposals/${api.proposalId}/image`, api.organizationId]]);
    const img = shown(el)!;
    expect([img.alt, img.getAttribute("width"), img.getAttribute("height")]).toEqual([en("proposal.photo.alt"), "1200", "520"]);
    // The boxes in the photo's pixels: the overlay's viewBox is the photo's size, each rect the box's corners (inclusive).
    const svg = s.querySelector("svg")!;
    expect(svg.getAttribute("viewBox")).toBe("0 0 1200 520");
    expect(svg.closest("[aria-hidden=true]")).not.toBeNull();
    const rects = [...svg.querySelectorAll("rect")].map((r) => ["x", "y", "width", "height"].map((a) => Number(r.getAttribute(a))));
    expect(rects).toEqual(samplePhotoBoxes.map((b) => [b.x1, b.y1, b.x2 - b.x1 + 1, b.y2 - b.y1 + 1]));
    expect(rects[0]).toEqual([40, 190, 190, 120]);
    // Each label at its box's top left corner, in percent of the photo, numbered as the list;
    // a thin box (the conveyors, 16 of 520 pixels high) carries its number only.
    const labels = [...s.querySelectorAll<HTMLElement>("[data-box]")];
    expect(labels.map((l) => text(l))).toEqual(samplePhotoBoxes.map((b, i) => (b.concept === "conveyor belt" ? `${i + 1}` : `${i + 1} ${b.concept}`)));
    expect(labels.map((l) => text(l)).slice(5, 7)).toEqual(["6 packing station", "7"]);
    expect([parseFloat(labels[0]!.style.left), parseFloat(labels[0]!.style.top)]).toEqual([(40 / 1200) * 100, (190 / 520) * 100]);
    // Drawn 600 px wide (half size), every box has room above it for its label.
    expect(labels.every((l) => l.dataset.place === "above")).toBe(true);
    // A box in the photo's right half has its label end at the box's right edge, so it stays inside the photo.
    expect(labels.map((l) => l.dataset.align)).toEqual(samplePhotoBoxes.map((b) => (b.x1 >= 600 ? "end" : "start")));
    expect([parseFloat(labels[5]!.style.right), labels[5]!.style.left]).toEqual([((1200 - 1160) / 1200) * 100, ""]);
    expect(labels.every((l) => l.style.maxWidth.startsWith("calc(100% -"))).toBe(true);
    // The same objects as text, in the same order.
    const list = s.querySelector("ol")!;
    expect(list.getAttribute("aria-label")).toBe(en("proposal.photo.objects", { model: "SAM 3.1" }));
    expect([...list.querySelectorAll("li")].map((li) => text(li))).toEqual(
      samplePhotoBoxes.map((b) => en("proposal.photo.object", { concept: b.concept, confidence: `${Math.round(b.confidence * 100)}%` })),
    );
    expect(list.querySelector("li bdi")?.textContent).toBe("smt line");
    expect(await violations()).toEqual([]);
  });

  it("says it in zh-Hant, the concepts as the data they are", async () => {
    const { el } = await open({ locale: "zh-Hant" });
    await waitFor(() => shown(el) !== null, "the photo");
    const s = section(el)!;
    expect(text(s.querySelector("h2"))).toBe(zh("proposal.photo"));
    expect(shown(el)!.alt).toBe(zh("proposal.photo.alt"));
    expect(text(s.querySelector("ol li"))).toBe(zh("proposal.photo.object", { concept: "smt line", confidence: "93%" }));
    expect(await violations()).toEqual([]);
  });

  it("puts a label inside its box when the box starts at the photo's top", async () => {
    const top: SegmentBox = { concept: "arrow", x1: 10, y1: 0, x2: 200, y2: 60, confidence: 0.5 };
    const { el } = await open({ proposal: { generated_by: { provider: "meta", model: "muse-spark-1.3", segmenter: { provider: "meta", model: "sam-3.1", objects: 1, boxes: [top] } } } });
    await waitFor(() => shown(el) !== null, "the photo");
    expect([...section(el)!.querySelectorAll<HTMLElement>("[data-box]")].map((l) => l.dataset.place)).toEqual(["inside"]);
  });

  it("shows the photo alone for a proposal stored before the boxes were kept, with no error", async () => {
    const { el } = await open({ proposal: { generated_by: { provider: "meta", model: "muse-spark-1.3", segmenter: { provider: "meta", model: "sam-3.1", objects: 12 } } } });
    await waitFor(() => shown(el) !== null, "the photo");
    const s = section(el)!;
    expect([s.querySelector("svg"), s.querySelector("ol"), s.querySelector("[data-box]"), el.querySelector("[role=alert]")]).toEqual([null, null, null, null]);
    expect(text(s)).not.toContain(en("proposal.photo.boxesHint", { model: "SAM 3.1" }));
    expect(await violations()).toEqual([]);
  });

  it("draws and lists only the boxes inside the photo", async () => {
    const outside: SegmentBox = { concept: "arrow", x1: 1100, y1: 10, x2: 1200, y2: 20, confidence: 0.5 };
    const reversed: SegmentBox = { concept: "arrow", x1: 30, y1: 10, x2: 20, y2: 20, confidence: 0.5 };
    const inside: SegmentBox = { concept: "rectangle", x1: 0, y1: 100, x2: 1199, y2: 519, confidence: 1 };
    const { el } = await open({ proposal: { generated_by: { provider: "meta", segmenter: { provider: "meta", objects: 3, boxes: [outside, reversed, inside] } } } });
    await waitFor(() => shown(el) !== null, "the photo");
    const s = section(el)!;
    expect(s.querySelectorAll("rect")).toHaveLength(1);
    expect([...s.querySelectorAll("[data-box]")].map((l) => text(l))).toEqual(["1 rectangle"]);
    expect(s.querySelectorAll("ol li")).toHaveLength(1);
    expect(drawable([outside, reversed, inside], 1200, 520)).toEqual([inside]);
    expect(drawable(undefined, 1200, 520)).toEqual([]);
    expect(drawable([{ ...inside, confidence: 1.5 }, { ...inside, x1: 0.5 }, { ...inside, y1: -1 }], 1200, 520)).toEqual([]);
  });

  it("says quietly that the photo could not be shown, and still lists the objects", async () => {
    for (const image of [
      () => new Response(JSON.stringify({ error: { code: "SERVICE_UNAVAILABLE", message: "down", request_id: "req_01M4YDQK00V6H8S0ST4F7510KR" } }), { status: 503, headers: { "Content-Type": "application/json" } }),
      () => new Response("<svg xmlns='http://www.w3.org/2000/svg'/>", { status: 200, headers: { "Content-Type": "image/svg+xml" } }),
    ]) {
      const { el } = await open({ image });
      await waitFor(() => el.querySelector("[data-photo-unavailable]") !== null, "the note");
      const s = section(el)!;
      expect(text(s.querySelector("[data-photo-unavailable]"))).toBe(en("proposal.photo.unavailable"));
      expect([s.querySelector("img"), s.querySelector("svg"), el.querySelector("[role=alert]")]).toEqual([null, null, null]);
      expect(s.querySelectorAll("ol li")).toHaveLength(samplePhotoBoxes.length);
      expect(await violations()).toEqual([]);
      unmountAll();
    }
  });

  it("asks for no photo for a proposal asked for in words", async () => {
    const { el, seen } = await open({ sample: "ready" });
    expect([section(el), seen]).toEqual([null, []]);
  });

  it("lets go of the photo's object URL when the page goes", async () => {
    const revoke = vi.spyOn(URL, "revokeObjectURL");
    const { el } = await open();
    await waitFor(() => shown(el) !== null, "the photo");
    const url = shown(el)!.src;
    expect(url).toMatch(/^blob:/);
    unmountAll();
    expect(revoke).toHaveBeenCalledWith(url);
    revoke.mockRestore();
  });
});

// H-052 review round 1: labels placed by the room drawn on screen (L4), the
// photo's loading state and its time limit (L5), and no object URL made or
// kept for a page that has gone or a proposal that changed (L3).
const PNG = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3]);
const IMAGE = { media_type: "image/png" as const, width: 1200, height: 520, size_bytes: PNG.length, content_hash: samplePhoto.content_hash };
const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const A = "aip_01M4YDQK00E1ECTR0N1C5A0001";
const B = "aip_01M4YDQK00E1ECTR0N1C5A0002";
const png = () => new Response(PNG, { status: 200, headers: { "Content-Type": "image/png" } });
const down = () => new Response(JSON.stringify({ error: { code: "SERVICE_UNAVAILABLE", message: "down", request_id: "req_01M4YDQK00V6H8S0ST4F7510KR" } }), { status: 503, headers: { "Content-Type": "application/json" } });

/** ProposalPhoto alone, on a fetch that answers each proposal's photo with answer(id). */
async function photoAlone(answer: (id: string) => Promise<Response>, props: { proposalId?: string; timeoutMs?: number; boxes?: SegmentBox[] } = {}) {
  const fetch = (async (input: RequestInfo | URL) => answer(new URL(String(input), "http://stand-in.invalid").pathname.split("/")[4]!)) as typeof globalThis.fetch;
  const client = createApiClient({ session: () => ({ organizationId: ORG }), fetch });
  const view = (id: string) => (
    <ApiProvider client={client}>
      <ProposalPhoto
        proposalId={id}
        image={IMAGE}
        generatedBy={props.boxes ? { provider: "meta", segmenter: { provider: "meta", model: "sam-3.1", objects: props.boxes.length, boxes: props.boxes } } : undefined}
        timeoutMs={props.timeoutMs}
      />
    </ApiProvider>
  );
  const el = await mount(view(props.proposalId ?? A));
  return { el, show: (id: string) => rerender(view(id)) };
}

describe("the photo's labels, loading and time limit (H-052 review round 1)", () => {
  it("puts a label above its box only where the photo, as drawn, has room for it, else inside the box's top edge", async () => {
    // 18 rendered pixels for a label: at 390 px the photo is drawn about 318 px wide.
    expect([labelPlace({ concept: "arrow", x1: 0, y1: 40, x2: 9, y2: 50, confidence: 1 }, 318 / 1200), labelPlace({ concept: "arrow", x1: 0, y1: 100, x2: 9, y2: 120, confidence: 1 }, 318 / 1200)]).toEqual(["inside", "above"]);
    expect([labelPlace({ concept: "arrow", x1: 0, y1: 40, x2: 9, y2: 50, confidence: 1 }, 1), labelPlace({ concept: "arrow", x1: 0, y1: 40, x2: 9, y2: 50, confidence: 1 }, 0)]).toEqual(["above", "inside"]);
    drawnAt(318);
    const low: SegmentBox = { concept: "rectangle", x1: 10, y1: 40, x2: 300, y2: 200, confidence: 0.9 };
    const high: SegmentBox = { concept: "rectangle", x1: 10, y1: 100, x2: 300, y2: 300, confidence: 0.9 };
    const edge: SegmentBox = { concept: "packing station", x1: 1150, y1: 300, x2: 1199, y2: 400, confidence: 0.9 };
    const { el } = await photoAlone(async () => png(), { boxes: [low, high, edge] });
    await waitFor(() => el.querySelector("img[src^='blob:']") !== null, "the photo");
    await settle();
    const labels = [...el.querySelectorAll<HTMLElement>("[data-box]")];
    expect(labels.map((l) => [l.dataset.place, l.dataset.align])).toEqual([["inside", "start"], ["above", "start"], ["above", "end"]]);
    // The box at the right edge: its label ends at the photo's right edge, and never runs past it.
    expect([labels[2]!.style.right, labels[2]!.style.left, labels[2]!.style.maxWidth]).toEqual(["0%", "", "calc(100% - 0%)"]);
  });

  it("says the photo is loading while it is read, and is busy until then", async () => {
    const { held, release } = gate();
    const { el } = await photoAlone(async () => {
      await held;
      return png();
    });
    const figure = el.querySelector("figure")!;
    expect([figure.getAttribute("aria-busy"), text(figure), el.querySelector("img")]).toEqual(["true", en("proposal.photo.loading"), null]);
    expect(await violations()).toEqual([]);
    release();
    await waitFor(() => el.querySelector("img[src^='blob:']") !== null, "the photo");
    expect([el.querySelector("figure")!.getAttribute("aria-busy"), text(el.querySelector("figure"))]).toEqual(["false", ""]);
  });

  it("gives up on a photo that does not come in time, with the quiet note, and makes no object URL for it later", async () => {
    const make = vi.spyOn(URL, "createObjectURL");
    const { held, release } = gate();
    const { el } = await photoAlone(
      async () => {
        await held;
        return png();
      },
      { timeoutMs: 50 },
    );
    await waitFor(() => el.querySelector("[data-photo-unavailable]") !== null, "the note");
    expect([text(el.querySelector("[data-photo-unavailable]")), el.querySelector("figure"), el.querySelector("[role=alert]")]).toEqual([en("proposal.photo.unavailable"), null, null]);
    release();
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect([make.mock.calls.length, el.querySelector("img")]).toEqual([0, null]);
    make.mockRestore();
  });

  it("makes no object URL when the page goes before the photo arrives", async () => {
    const make = vi.spyOn(URL, "createObjectURL");
    const { held, release } = gate();
    const seen: string[] = [];
    await photoAlone(async (id) => {
      seen.push(id);
      await held;
      return png();
    });
    await waitFor(() => seen.length === 1, "the request");
    unmountAll();
    release();
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(make).not.toHaveBeenCalled();
    make.mockRestore();
  });

  it("starts over for another proposal: loading its photo, not the last one's note", async () => {
    const { held, release } = gate();
    const { el, show } = await photoAlone(async (id) => {
      if (id === A) return down();
      await held;
      return png();
    });
    await waitFor(() => el.querySelector("[data-photo-unavailable]") !== null, "A's note");
    await show(B);
    expect([el.querySelector("[data-photo-unavailable]"), el.querySelector("figure")?.getAttribute("aria-busy")]).toEqual([null, "true"]);
    release();
    await waitFor(() => el.querySelector("img[src^='blob:']") !== null, "B's photo");
    expect(el.querySelector("[data-photo-unavailable]")).toBeNull();
  });
});

describe("the gallery's sample photo", () => {
  it("is the synthetic demo floor plan as stored, with the fake segmenter's boxes", () => {
    const photos = join(process.cwd(), "../../testdata/photos");
    const file = readFileSync(join(photos, "electronics-line-demo.png"));
    const bytes = samplePhotoBytes();
    expect(Buffer.from(bytes).equals(file)).toBe(true);
    expect([`sha256:${createHash("sha256").update(bytes).digest("hex")}`, bytes.length]).toEqual([samplePhoto.content_hash, samplePhoto.size_bytes]);
    const segments = JSON.parse(readFileSync(join(photos, "electronics-line-demo.segments.json"), "utf8")) as {
      width: number; height: number; segments: { concept: string; box: { x1: number; y1: number; x2: number; y2: number }; confidence: number }[];
    };
    expect([segments.width, segments.height]).toEqual([samplePhoto.width, samplePhoto.height]);
    expect(samplePhotoBoxes).toEqual(segments.segments.map((s) => ({ concept: s.concept, ...s.box, confidence: s.confidence })));
  });
});
