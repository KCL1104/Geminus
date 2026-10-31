"use client";
// The photo a proposal was asked for with, and what the segmenter (SAM 3.1)
// found in it (H-052). The stored photo is read through the BFF with the
// tenant selector (getProposalImage), which an <img src> cannot send, and
// shown as an object URL (the CSP's img-src blob:), let go when the page
// goes. Each box of generated_by.segmenter.boxes is drawn over it in the
// photo's own pixels: an SVG whose viewBox is the photo's size, so the boxes
// scale with the picture; beside each, its number and (where there is room:
// not on a thin box, nor on a narrow screen) its concept. The same objects
// follow as a numbered list with their confidence, for those who
// cannot see the picture, which the overlay is hidden from. The boxes are
// the segmenter's guess, data from the server: one that is not inside the
// photo, or whose corners are out of order, is neither drawn nor listed, and
// a concept is text in a <bdi>. A proposal stored before the boxes were kept
// (or one where nothing was found) shows the photo alone; a photo that
// cannot be read, or has not come in PHOTO_TIMEOUT_MS, says so in a quiet
// line, never as an error of the page; while it loads, the frame says so and
// is busy. A label sits above its box where the photo, as drawn, has room for
// it, else inside the box's top edge, and never runs past the photo's side.
import { useEffect, useId, useRef, useState } from "react";
import { useApi } from "../../lib/api/provider";
import type { GeneratedBy, Proposal, SegmentBox } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { useSegmenterName } from "./generated-by";
import { visible } from "./shown";
import { data, useRich, Words } from "./words";
import styles from "./proposal.module.css";

type SourceImage = NonNullable<Proposal["source_image"]>;

/** The boxes that can be drawn on a width × height photo, in the server's order: whole pixels inside it, corners in order, a confidence from 0 to 1. */
export function drawable(boxes: readonly SegmentBox[] | undefined, width: number, height: number): SegmentBox[] {
  return (boxes ?? []).filter(
    (b) =>
      [b.x1, b.y1, b.x2, b.y2].every(Number.isInteger) &&
      b.x1 >= 0 && b.y1 >= 0 && b.x1 <= b.x2 && b.y1 <= b.y2 && b.x2 < width && b.y2 < height &&
      b.confidence >= 0 && b.confidence <= 1,
  );
}

/** How long the page waits for the photo before it says the photo could not be shown (review round 1, L5). */
export const PHOTO_TIMEOUT_MS = 20_000;
/** The room a label needs above its box, in rendered pixels: one 12 px line at 1.4, and a hair. */
const LABEL_PX = 18;

/**
 * Where a box's label goes, the photo drawn at `scale` rendered pixels per
 * photo pixel: above the box where the photo has room for it there, else
 * inside the box's top edge, so the frame never clips it (review round 1, L4).
 * An unknown scale (0) has no room.
 */
export function labelPlace(b: SegmentBox, scale: number): "above" | "inside" {
  return b.y1 * scale >= LABEL_PX ? "above" : "inside";
}

/** A thin box (a conveyor, an arrow) carries its number only: its concept would cover the photo beside it. */
const thin = (b: SegmentBox, width: number, height: number) => (b.x2 - b.x1 + 1) / width < 0.08 || (b.y2 - b.y1 + 1) / height < 0.08;
const percent = (v: number, of: number) => `${(v / of) * 100}%`;

/**
 * A label's position in percent of the photo: from its box's left edge for a
 * box in the photo's left half, else ending at its box's right edge; never
 * wider than the photo leaves it on that side (a longer one ends in an
 * ellipsis), so it never runs past the photo's edge.
 */
function labelStyle(b: SegmentBox, width: number, height: number) {
  const top = percent(b.y1, height);
  if (b.x1 < width / 2) {
    const left = percent(b.x1, width);
    return { align: "start", style: { left, top, maxWidth: `calc(100% - ${left})` } } as const;
  }
  const right = percent(width - 1 - b.x2, width);
  return { align: "end", style: { right, top, maxWidth: `calc(100% - ${right})` } } as const;
}

/** The photo's section, or nothing for a proposal asked for in words. */
export function ProposalPhoto({ proposalId, image, generatedBy, timeoutMs = PHOTO_TIMEOUT_MS }: { proposalId: string; image: SourceImage; generatedBy: GeneratedBy | undefined; timeoutMs?: number }) {
  const api = useApi();
  const { t, number } = useI18n();
  const rich = useRich();
  const headingId = useId();
  const model = useSegmenterName(generatedBy);
  const frame = useRef<HTMLElement>(null);
  // The photo as read for one proposal (key): its object URL (null while it
  // loads), or that it could not be shown. A state of another proposal is
  // not this one's: the page starts over from loading (review round 1, L3).
  const key = `${proposalId} ${image.media_type}`;
  const [photo, setPhoto] = useState<{ key: string; url: string | null; unavailable: boolean }>({ key, url: null, unavailable: false });
  const { url, unavailable } = photo.key === key ? photo : { url: null, unavailable: false };
  const [drawnWidth, setDrawnWidth] = useState(0);

  useEffect(() => {
    const ctl = new AbortController();
    let made: string | null = null;
    // A photo that has not come in time is given up on, with the quiet note (L5).
    const timer = setTimeout(() => {
      ctl.abort();
      setPhoto({ key, url: null, unavailable: true });
    }, timeoutMs);
    void api.getProposalImage(proposalId, { signal: ctl.signal }).then((r) => {
      // Gone (the page, the proposal) or given up on: no object URL is made.
      if (ctl.signal.aborted) return;
      clearTimeout(timer);
      if (!r.ok || r.data.type !== image.media_type) return setPhoto({ key, url: null, unavailable: true });
      made = URL.createObjectURL(r.data);
      setPhoto({ key, url: made, unavailable: false });
    });
    return () => {
      clearTimeout(timer);
      ctl.abort();
      if (made !== null) URL.revokeObjectURL(made);
    };
  }, [api, key, proposalId, image.media_type, timeoutMs]);

  // How wide the photo is drawn, for the labels' room (L4).
  useEffect(() => {
    const el = frame.current;
    if (el === null || url === null) return;
    const measure = () => setDrawnWidth(el.getBoundingClientRect().width);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [url]);

  const { width, height } = image;
  const scale = drawnWidth / width;
  const boxes = drawable(generatedBy?.segmenter?.boxes, width, height);
  const named = model !== null && boxes.length > 0;
  return (
    <section className={styles.section} data-section="photo" aria-labelledby={headingId}>
      <h2 id={headingId}>{t("proposal.photo")}</h2>
      {named && <p className={styles.hint}>{t("proposal.photo.boxesHint", { model })}</p>}
      {unavailable ? (
        <p className={styles.hint} data-photo-unavailable>
          {t("proposal.photo.unavailable")}
        </p>
      ) : (
        <figure ref={frame} className={styles.photoFrame} style={{ aspectRatio: `${width} / ${height}`, maxWidth: width }} aria-busy={url === null}>
          {url === null && <span className={styles.photoLoading}>{t("proposal.photo.loading")}</span>}
          {url !== null && (
            <>
              {/* An object URL of the stored bytes: next/image cannot take one. */}
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={url} alt={t("proposal.photo.alt")} width={width} height={height} onError={() => setPhoto({ key, url: null, unavailable: true })} />
              {boxes.length > 0 && (
                <div className={styles.boxes} aria-hidden="true">
                  <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" focusable="false">
                    {boxes.map((b, i) => (
                      <rect key={i} x={b.x1} y={b.y1} width={b.x2 - b.x1 + 1} height={b.y2 - b.y1 + 1} />
                    ))}
                  </svg>
                  {boxes.map((b, i) => {
                    const { align, style } = labelStyle(b, width, height);
                    return (
                      <span key={i} className={styles.boxLabel} data-box={i + 1} data-place={labelPlace(b, scale)} data-align={align} style={style}>
                        {i + 1}
                        {!thin(b, width, height) && <bdi className={styles.boxConcept}> {visible(b.concept)}</bdi>}
                      </span>
                    );
                  })}
                </div>
              )}
            </>
          )}
        </figure>
      )}
      {named && (
        <ol className={styles.objects} aria-label={t("proposal.photo.objects", { model })}>
          {boxes.map((b, i) => (
            <li key={i}>
              <Words parts={rich("proposal.photo.object", { concept: data(b.concept), confidence: number(b.confidence, { style: "percent", maximumFractionDigits: 0 }) })} />
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
