"use client";
// H-049: the person chooses the model of each proposal: Muse Spark 1.3 (the
// default: faster, costs less) or Muse Wonder 1.0 (Meta's most capable
// model, in preview: slower, costs more). One labelled Select, as the ask
// form's "what to make" is, with a line under it that says what the choice
// means. Only Muse Wonder adds `model` to a request (modelField): a request
// on the default is byte for byte what it was, and the deployment's model
// answers it. The proposal's generated_by names the model that answered.
import { useId } from "react";
import type { ProposalModel } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { Select } from "../../ui/select";
import styles from "./proposal.module.css";

/** The models a person may choose, the default first (common.schema.json ProposalModel). */
export const PROPOSAL_MODELS: readonly ProposalModel[] = ["muse-spark-1.3", "muse-wonder-1.0"];

export const DEFAULT_MODEL: ProposalModel = "muse-spark-1.3";

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
  return (
    <div className={styles.askMode} data-model-choice>
      <Select
        label={label ?? t("ask.model")}
        items={PROPOSAL_MODELS.map((id) => ({ id, label: t(`ai.model.${id}`) }))}
        value={value}
        onChange={(id) => onChange(PROPOSAL_MODELS.find((m) => m === id) ?? DEFAULT_MODEL)}
        aria-describedby={hintId}
      />
      <p id={hintId} className={styles.hint}>
        {t(`ask.modelHint.${value}`)}
      </p>
    </div>
  );
}
