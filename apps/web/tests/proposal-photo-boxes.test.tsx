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
import { describe, expect, it, vi } from "vitest";
import { samplePhotoBoxes, samplePhotoBytes } from "../app/ui-gallery/proposal/sample-photo";
import { samplePhoto } from "../app/ui-gallery/proposal/samples";
import { createProposalStandIn, type ProposalStandInOptions } from "../app/ui-gallery/proposal/stand-in";
import { drawable } from "../features/ai-proposals/proposal-photo";
import { ProposalRoute } from "../features/ai-proposals/proposal-view";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider } from "../lib/auth/session";
import type { SegmentBox } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { mount, settle, unmountAll, violations } from "./ui-harness";

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

type Open = Omit<ProposalStandInOptions, "sample"> & { sample?: ProposalStandInOptions["sample"]; locale?: Locale; image?: (r: Response) => Response };

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

describe("the photo and what SAM 3.1 found (H-052)", () => {
  it("draws each box over the photo at its pixels, with its number and concept, and lists the objects", async () => {
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
    expect(labels.every((l) => l.dataset.place === "above")).toBe(true);
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
