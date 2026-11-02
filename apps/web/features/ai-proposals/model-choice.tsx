"use client";
// H-049: the person chooses the model of each proposal: Muse Spark 1.3 (the
// default: faster, costs less) or Muse Wonder 1.0 (Meta's most capable
// model, in preview: slower, costs more). One labelled Select, as the ask
// form's "what to make" is, with a line under it that says what the choice
// means. Only Muse Wonder adds `model` to a request (modelField): a request
// on the default is byte for byte what it was, and the deployment's model
// answers it. The proposal's generated_by names the model that answered.
// H-051: off unless NEXT_PUBLIC_GEMINUS_OFFER_WONDER=1 (offeredModels); with
// one model offered there is nothing to choose, so the choice is not shown
// and every request is on the default. H-065: Muse Wonder is the choice for
// harder changes, and its line says it thinks longer (several minutes) and
// costs more; still off in production builds until live tests pass.
// 2026-11-02: in Ask AI's composer the same choice is a chip of the tool
// row (ModelChip): the same models, default and offer, its line under the
// box (ModelHint) as its description.
import { useId } from "react";
import type { ProposalModel } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { Select } from "../../ui/select";
import { DEFAULT_MODEL, offeredModels } from "./proposal-models";
import styles from "./proposal.module.css";

export { DEFAULT_MODEL, offeredModels, PROPOSAL_MODELS } from "./proposal-models";

/** The request's model field: none for the default, the chosen model otherwise. */
export function modelField(model: ProposalModel): { model?: ProposalModel } {
  return model === DEFAULT_MODEL ? {} : { model };
}

export function ModelChoice({ value, onChange, label }: {
  value: ProposalModel;
  onChange: (model: ProposalModel) => void;
  /** The control's label; "Model" when absent. */
  label?: string;
}) {
  const { t } = useI18n();
  const hintId = useId();
  const models = offeredModels();
  if (models.length < 2) return null;
  return (
    <div className={styles.askMode} data-model-choice>
      <Select
        label={label ?? t("ask.model")}
        items={models.map((id) => ({ id, label: t(`ai.model.${id}`) }))}
        value={value}
        onChange={(id) => onChange(models.find((m) => m === id) ?? DEFAULT_MODEL)}
        aria-describedby={hintId}
      />
      <p id={hintId} className={styles.hint}>
        {t(`ask.modelHint.${value}`)}
      </p>
    </div>
  );
}

/** Ask AI's composer (2026-11-02): the same choice as a chip of its tool row; hintId names the line ModelHint shows under the box. Nothing when there is one model. */
export function ModelChip({ value, onChange, hintId }: { value: ProposalModel; onChange: (model: ProposalModel) => void; hintId: string }) {
  const { t } = useI18n();
  const models = offeredModels();
  if (models.length < 2) return null;
  return (
    <span data-model-choice>
      <Select
        variant="chip"
        label={t("ask.model")}
        items={models.map((id) => ({ id, label: t(`ai.model.${id}`) }))}
        value={value}
        onChange={(id) => onChange(models.find((m) => m === id) ?? DEFAULT_MODEL)}
        aria-describedby={hintId}
      />
    </span>
  );
}

/** ModelChip's line, what the chosen model means; nothing when there is one model. */
export function ModelHint({ value, id, className }: { value: ProposalModel; id: string; className?: string }) {
  const { t } = useI18n();
  if (offeredModels().length < 2) return null;
  return (
    <p id={id} className={className} data-model-hint>
      {t(`ask.modelHint.${value}`)}
    </p>
  );
}
