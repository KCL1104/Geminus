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
// cannot be read says so in a quiet line, never as an error of the page.
import { useEffect, useId, useState } from "react";
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

/** Where a label goes: above its box, or inside it when the box starts at the photo's top. */
const place = (b: SegmentBox, height: number) => (b.y1 / height < 0.08 ? "inside" : "above");
/** A thin box (a conveyor, an arrow) carries its number only: its concept would cover the photo beside it. */
const thin = (b: SegmentBox, width: number, height: number) => (b.x2 - b.x1 + 1) / width < 0.08 || (b.y2 - b.y1 + 1) / height < 0.08;
const percent = (v: number, of: number) => `${(v / of) * 100}%`;

/** The photo's section, or nothing for a proposal asked for in words. */
export function ProposalPhoto({ proposalId, image, generatedBy }: { proposalId: string; image: SourceImage; generatedBy: GeneratedBy | undefined }) {
  const api = useApi();
  const { t, number } = useI18n();
  const rich = useRich();
  const headingId = useId();
  const model = useSegmenterName(generatedBy);
  // The photo's object URL once it is read (null while it loads).
  const [url, setUrl] = useState<string | null>(null);
  const [unavailable, setUnavailable] = useState(false);

  useEffect(() => {
    const ctl = new AbortController();
    let made: string | null = null;
    void api.getProposalImage(proposalId, { signal: ctl.signal }).then((r) => {
      if (ctl.signal.aborted) return;
      if (!r.ok || r.data.type !== image.media_type) return setUnavailable(true);
      made = URL.createObjectURL(r.data);
      setUrl(made);
    });
    return () => {
      ctl.abort();
      if (made !== null) URL.revokeObjectURL(made);
    };
  }, [api, proposalId, image.media_type]);

  const { width, height } = image;
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
        <figure className={styles.photoFrame} style={{ aspectRatio: `${width} / ${height}`, maxWidth: width }}>
          {url !== null && (
            <>
              {/* An object URL of the stored bytes: next/image cannot take one. */}
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={url} alt={t("proposal.photo.alt")} width={width} height={height} onError={() => setUnavailable(true)} />
              {boxes.length > 0 && (
                <div className={styles.boxes} aria-hidden="true">
                  <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" focusable="false">
                    {boxes.map((b, i) => (
                      <rect key={i} x={b.x1} y={b.y1} width={b.x2 - b.x1 + 1} height={b.y2 - b.y1 + 1} />
                    ))}
                  </svg>
                  {boxes.map((b, i) => (
                    <span key={i} className={styles.boxLabel} data-box={i + 1} data-place={place(b, height)} style={{ left: percent(b.x1, width), top: percent(b.y1, height) }}>
                      {i + 1}
                      {!thin(b, width, height) && <bdi className={styles.boxConcept}> {visible(b.concept)}</bdi>}
                    </span>
                  ))}
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
