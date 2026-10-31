"use client";
// H-062: on the model page, above the AI request and its photo button, what
// the new-model page left when the model was created but its photo was not
// sent (photo-start.ts): that the model exists and the photo can be sent
// again with the button below, then the API's refusal as every page shows
// one, with the photo form's advice for it.
import { useEffect, useState } from "react";
import { ProblemView } from "../../app/_components/problem-view";
import type { PhotoRefusal } from "../ai-proposals/photo-form";
import { useI18n } from "../../lib/i18n/provider";
import { Callout } from "../../ui/callout";
import { clearPhotoNotSent, photoNotSent } from "./photo-start";

/**
 * The refusal left for modelId, read once when the page opens and then
 * forgotten, so it is shown this time only (a component that moves, as the
 * request does between the side panel and a phone's column, keeps it).
 */
export function usePhotoNotSent(modelId: string): PhotoRefusal | null {
  const [refusal] = useState(() => photoNotSent(modelId));
  useEffect(() => clearPhotoNotSent(modelId), [modelId]);
  return refusal;
}

export function PhotoNotSent({ refusal }: { refusal: PhotoRefusal }) {
  const { t } = useI18n();
  return (
    <div data-photo-not-sent>
      <Callout tone="warning" role="alert" heading={t("modelPhoto.notSent.title")}>
        <p>{t("modelPhoto.notSent.body", { button: t("photo.choose") })}</p>
      </Callout>
      <ProblemView problem={refusal.problem} headingLevel={3} guidance={refusal.guidance} />
    </div>
  );
}
