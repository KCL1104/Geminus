"use client";
// H-061 A8 (PROPOSED): what Ask AI asks besides the person's words when the
// AI is to act on them (H-056 §14 AskForm). In scenario mode, the settings of
// every run the AI starts (the run form's four fields, H-047's defaults,
// within H-060's caps, said beside them) and the aim (one of four metrics,
// the median lead time by default; the server fixes its direction). In both
// modes, the planner, only in a build that offers Muse Wonder 1.0 as
// planner (H-061 §14); with one planner there is nothing to choose.
import { useId } from "react";
import type { InvestigationObjectiveMetric, ModelTimeUnit, PlannerModel } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { TextField } from "../../ui/field";
import { Select } from "../../ui/select";
import type { RunField, RunForm } from "../simulations/run-config";
import styles from "../ai-proposals/proposal.module.css";
import { DEFAULT_PLANNER, offeredPlanners } from "./flags";
import { type ActIssue, MAX_DAYS, MAX_REPLICATIONS, OBJECTIVES } from "./request";

const issueKey = {
  required: "run.issue.required",
  number: "run.issue.number",
  positive: "run.issue.positive",
  warmup: "run.issue.warmup",
  replications: "run.issue.replications",
  seed: "run.issue.seed",
  aiReplications: "investigation.issue.aiReplications",
  aiHorizon: "investigation.issue.aiHorizon",
} as const satisfies Record<ActIssue, string>;

export function PlannerChoice({ value, onChange }: { value: PlannerModel; onChange: (p: PlannerModel) => void }) {
  const { t } = useI18n();
  const hintId = useId();
  const planners = offeredPlanners();
  if (planners.length < 2) return null;
  return (
    <div className={styles.askMode} data-planner-choice>
      <Select
        label={t("investigation.planner")}
        items={planners.map((id) => ({ id, label: t(`investigation.planner.${id}`) }))}
        value={value}
        onChange={(id) => onChange(planners.find((p) => p === id) ?? DEFAULT_PLANNER)}
        aria-describedby={hintId}
      />
      <p id={hintId} className={styles.hint}>
        {t(`investigation.plannerHint.${value}`)}
      </p>
    </div>
  );
}

export function RunSettings({ unit, form, onForm, issues, objective, onObjective }: {
  unit: ModelTimeUnit;
  form: RunForm;
  onForm: (f: RunForm) => void;
  issues: Partial<Record<RunField, ActIssue>>;
  objective: InvestigationObjectiveMetric;
  onObjective: (m: InvestigationObjectiveMetric) => void;
}) {
  const { t, gap } = useI18n();
  const headingId = useId();
  const field = (name: RunField, label: string) => {
    const issue = issues[name];
    return (
      <TextField
        label={label}
        value={form[name]}
        onChange={(v) => onForm({ ...form, [name]: v })}
        inputMode={name === "horizon" || name === "warmup" ? "decimal" : "numeric"}
        autoComplete="off"
        isInvalid={issue !== undefined}
        errorMessage={issue ? t(issueKey[issue], { max: name === "seed" ? Number.MAX_SAFE_INTEGER : MAX_REPLICATIONS, days: MAX_DAYS }) : undefined}
      />
    );
  };
  return (
    <div className={styles.act} role="group" aria-labelledby={headingId}>
      <p id={headingId} className={styles.hint}>
        {t("investigation.runs")}
        {gap}
        {t("investigation.runs.hint")}
      </p>
      <div className={styles.actFields}>
        {field("horizon", t("run.horizon", { unit: t(`run.unit.${unit}`) }))}
        {field("warmup", t("run.warmup", { unit: t(`run.unit.${unit}`) }))}
        {field("replications", t("run.replications"))}
        {field("seed", t("run.seed"))}
      </div>
      <Select
        label={t("investigation.objective")}
        items={OBJECTIVES.map((id) => ({ id, label: t(`investigation.objective.${id}`) }))}
        value={objective}
        onChange={(id) => onObjective(OBJECTIVES.find((m) => m === id) ?? OBJECTIVES[0]!)}
      />
    </div>
  );
}
