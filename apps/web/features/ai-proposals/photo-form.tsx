"use client";
// The model page's one way to ask for a change from a photo (T-056, H-033):
// one button, beside the ask form's send button, that opens the file
// chooser for a PNG or JPEG photo or floor plan of the line (AskForm
// renders it in its actions, so the model page keeps one AI section and the
// editor stays where it was). The file goes to createImageProposal on the model's
// latest version, with the reader's language for the AI's prose; the
// server keeps only the image's pixels, answers with the proposal it stored
// (generating), and the page moves on to it. A segmenter finds the line's
// machines, the AI proposes stations and flow from them, and the server
// reviews the answer: nothing here proposes, decides or changes the model.
// While the editor has unsaved edits the button is blocked, focusable, with
// the reason beside it, and it opens nothing. Choosing the same file again
// (a retry) reuses its key (D-011); a file larger than the contract takes is
// refused here, before anything is sent.
import { useId, useRef, useState } from "react";
import { FileTrigger } from "react-aria-components";
import { ProblemView } from "../../app/_components/problem-view";
import { newIdempotencyKey } from "../../lib/api/client";
import { invalidRequestProblem, type Problem } from "../../lib/api/problem";
import { useApi } from "../../lib/api/provider";
import type { Model } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import type { MessageKey } from "../../lib/i18n/translate";
import { Button } from "../../ui/button";
import { StatusRegion } from "../../ui/status";
import styles from "./proposal.module.css";

/** The most bytes an image may have (proposal.schema.json source_image.size_bytes, the server's photo.MaxBytes: 5 MiB). */
export const MAX_PHOTO_BYTES = 5_242_880;

/** The image types the server takes (source_image.media_type). */
export const PHOTO_TYPES = ["image/png", "image/jpeg"] as const;

/**
 * The domains whose pack names what a segmenter looks for in a photo (the
 * electronics line pack's PhotoConcepts, T-056); the server fails a photo
 * of any other domain's model (PHOTO_NOT_SUPPORTED), so the page offers none.
 */
export const PHOTO_DOMAINS: readonly Model["domain"][] = ["electronics_line"];

/** What the form says for a refusal it knows, by the problem's detail or error code. */
function advice(p: Problem): MessageKey | undefined {
  const code = p.details[0]?.code ?? p.code;
  switch (code) {
    case "PAYLOAD_TOO_LARGE":
      return "photo.problem.tooLarge";
    case "IMAGE_FORMAT_REJECTED":
      return "photo.problem.format";
    case "IMAGE_INVALID":
      return "photo.problem.invalid";
    case "IMAGE_PIXELS_EXCEEDED":
      return "photo.problem.pixels";
  }
  return undefined;
}

export function PhotoForm({ model, blockedReason, reasonId, noticeId, onCreated }: {
  model: Model;
  /** Why sending must wait (unsaved edits the page would lose), or null. */
  blockedReason: string | null;
  /** The element that shows blockedReason on screen (the ask form's). */
  reasonId: string;
  /** The ask form's line saying that what is sent goes to an AI service (T-057, H-035). */
  noticeId: string;
  /** The server stored the proposal: follow it. */
  onCreated: (proposalId: string) => void;
}) {
  const api = useApi();
  const { t, locale } = useI18n();
  const hintId = useId();
  // The refusal, and what the form advises for it.
  const [problem, setProblem] = useState<{ problem: Problem; guidance: MessageKey | undefined } | null>(null);
  const [sending, setSending] = useState(false);
  const busy = useRef(false);
  const sent = useRef<{ request: string; key: string } | null>(null);
  const latest = model.latest_version;

  const send = async (files: FileList | null) => {
    const file = files?.[0];
    if (file === undefined || busy.current || blockedReason !== null) return;
    setProblem(null);
    if (file.size > MAX_PHOTO_BYTES) {
      setProblem({ problem: invalidRequestProblem("The image is larger than 5,242,880 bytes; nothing was sent."), guidance: "photo.problem.tooLarge" });
      return;
    }
    // The same file again on the same version (a retry) takes the same key; another a new one.
    const request = `${model.id} ${latest.id} ${locale} ${file.name} ${file.size} ${file.lastModified}`;
    if (sent.current?.request !== request) sent.current = { request, key: newIdempotencyKey() };
    busy.current = true;
    setSending(true);
    const r = await api.createImageProposal(model.id, { base_version_id: latest.id, output_locale: locale }, file, sent.current.key);
    busy.current = false;
    setSending(false);
    if (r.ok) onCreated(r.data.proposal_id);
    else setProblem({ problem: r.problem, guidance: advice(r.problem) });
  };

  const describedBy = [blockedReason !== null ? reasonId : null, hintId, noticeId].filter(Boolean).join(" ");
  const choose = (
    <Button blocked={blockedReason !== null} aria-describedby={describedBy}>
      {t("photo.choose")}
    </Button>
  );
  // In the ask form's actions row: the button beside Send, the hint and
  // any refusal on their own lines below.
  return (
    <div className={styles.photo} data-photo>
      {/* Blocked, the button opens nothing: no file chooser behind it. */}
      {blockedReason !== null ? (
        choose
      ) : (
        <FileTrigger acceptedFileTypes={PHOTO_TYPES} onSelect={(files) => void send(files)}>
          {choose}
        </FileTrigger>
      )}
      <p id={hintId} className={styles.reason}>
        {t("photo.hint")}
      </p>
      {problem && (
        <div className={styles.line}>
          <ProblemView problem={problem.problem} headingLevel={3} guidance={problem.guidance} />
        </div>
      )}
      <StatusRegion message={sending ? t("photo.sending") : null} />
    </div>
  );
}
