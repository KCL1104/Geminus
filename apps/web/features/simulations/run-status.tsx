"use client";
// The two runs of a comparison as they go (T-032, DESIGN.md): each named
// with its line key (dashed baseline, solid scenario) and its state; a
// progress bar while it runs; a failed run's error as the API's problem;
// one cancel for whatever is still going, for those who may cancel.
import { ProgressBar } from "react-aria-components";
import type { Run } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { ProblemView } from "../../app/_components/problem-view";
import { Button } from "../../ui/button";
import { type CancelProblem, isActive, runProblem } from "./run-pair";
import styles from "./simulations.module.css";

export type RunSide = "baseline" | "scenario";

/** The line key of a run: what tells baseline from scenario without colour (The Two Inks Rule). */
export function LineKey({ side }: { side: RunSide }) {
  return (
    <svg className={styles.key} width="24" height="8" viewBox="0 0 24 8" aria-hidden="true" focusable="false">
      <line x1="0" y1="4" x2="24" y2="4" data-run={side} />
    </svg>
  );
}

function RunLine({ side, run }: { side: RunSide; run: Run }) {
  const { t } = useI18n();
  const status =
    run.status === "running" && run.cancellation_requested ? t("runs.status.stopping") : t(`runs.status.${run.status}`);
  const { completed_replications: done, total_replications: total } = run.progress;
  const problem = run.status === "failed" ? runProblem(run) : null;
  return (
    <li className={styles.run} data-status={run.status}>
      <div className={styles.runHead}>
        <LineKey side={side} />
        <span className={styles.runName}>{t(`runs.${side}`)}</span>
        <span>{status}</span>
        {run.status === "completed" && <span className={styles.meta}>{t("runs.progress", { done: String(done), count: total })}</span>}
      </div>
      {run.status === "running" && total > 0 && (
        <ProgressBar
          className={styles.progress}
          aria-label={t(`runs.progress.${side}`)}
          minValue={0}
          maxValue={total}
          value={done}
          valueLabel={t("runs.progress", { done: String(done), count: total })}
        >
          {({ percentage, valueText }) => (
            <>
              <span className={styles.track} aria-hidden="true">
                <span className={styles.fill} style={{ width: `${percentage ?? 0}%` }} />
              </span>
              <span className={styles.meta}>{valueText}</span>
            </>
          )}
        </ProgressBar>
      )}
      {run.status === "failed" && (
        <>
          <p>{t(`runs.failed.${side}`)}</p>
          {problem && <ProblemView problem={problem} headingLevel={3} />}
        </>
      )}
    </li>
  );
}

export function RunPairStatus({ baseline, scenario, canCancel, cancelling, cancelProblem, onCancel }: {
  baseline: Run;
  scenario: Run;
  canCancel: boolean;
  cancelling: boolean;
  /** Whose cancel did not go through (one run's or both), with the problem the API answered. */
  cancelProblem: CancelProblem | null;
  onCancel: () => void;
}) {
  const { t } = useI18n();
  const active = isActive(baseline) || isActive(scenario);
  return (
    <section aria-labelledby="runs-heading" className={styles.section}>
      <h2 id="runs-heading">{t("runs.title")}</h2>
      <ul className={styles.runs}>
        <RunLine side="baseline" run={baseline} />
        <RunLine side="scenario" run={scenario} />
      </ul>
      {cancelProblem && (
        <>
          <p>{t(`runs.cancelFailed.${cancelProblem.side}`)}</p>
          <ProblemView problem={cancelProblem.problem} headingLevel={3} />
        </>
      )}
      {active && canCancel && (
        <Button onPress={() => { if (!cancelling) onCancel(); }}>{t("runs.cancel")}</Button>
      )}
    </section>
  );
}
