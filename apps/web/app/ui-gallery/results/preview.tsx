"use client";
// The results view in one state at a time (T-032), for review and the
// browser checks (apps/web-e2e): the app mounts no session yet, so the
// signed-in page cannot render in a browser; this page renders the same
// components with the synthetic board line (features/results/fixtures).
// No API is called. Its one control works: cancelling the running runs
// shows them cancelled.
import { useState } from "react";
import type { Comparison, Run } from "../../../lib/contracts";
import { useI18n } from "../../../lib/i18n/provider";
import {
  boardBaselineRun,
  boardComparison,
  boardResults,
  boardScenarioRun,
  boardTraces,
  boardVersion,
} from "../../../features/results/fixtures/board-line";
import type { ResultsData } from "../../../features/results/load";
import { ResultsView } from "../../../features/results/results-view";
import { RunPairStatus } from "../../../features/simulations/run-status";
import { Callout } from "../../../ui/callout";
import styles from "../gallery.module.css";

export type PreviewState = "results" | "incomparable" | "running" | "failed";

const progress = (revision: number, done: number) => ({ completed_replications: done, total_replications: 5, events_processed: done * 1300, revision });

const incomparable: Comparison = {
  ...boardComparison,
  comparable: false,
  incomparable_reasons: ["ENGINE_VERSION_MISMATCH"],
  warnings: [],
  metrics: boardComparison.metrics.map((m) => ({ ...m, absolute_delta: null, relative_delta: null, status: "incomparable", reason: "RUNS_NOT_COMPARABLE" })),
};

function data(comparison: Comparison, baseline: Run): ResultsData {
  return {
    comparison,
    version: boardVersion,
    baseline: { run: baseline, result: boardResults.baseline, trace: { state: "loaded", trace: boardTraces.baseline } },
    scenario: { run: boardScenarioRun(), result: boardResults.scenario, trace: { state: "loaded", trace: boardTraces.scenario } },
  };
}

export function ResultsPreview({ state }: { state: PreviewState }) {
  const { t } = useI18n();
  const [cancelled, setCancelled] = useState(false);
  const running: [Run, Run] = cancelled
    ? [boardBaselineRun({ status: "cancelled", progress: progress(5, 2) }), boardScenarioRun({ status: "cancelled", progress: progress(5, 1) })]
    : [boardBaselineRun({ status: "running", progress: progress(3, 2) }), boardScenarioRun({ status: "running", progress: progress(3, 1) })];
  const failed = boardScenarioRun({
    status: "failed",
    progress: progress(4, 2),
    error: { error: { code: "MODEL_VALIDATION_FAILED", message: "the run exceeded its event budget (max_events)", details: [{ path: "/execution_limits/max_events", reason: "the run exceeded its event budget", code: "EVENT_BUDGET_EXCEEDED" }], request_id: "req_01M4YDQK00V6H8S0ST4F7510KR" } },
  });
  const [b, s] = state === "running" ? running : state === "failed" ? [boardBaselineRun(), failed] : [boardBaselineRun(state === "incomparable" ? { engine_version: "des-go-0.4.0" } : {}), boardScenarioRun()];
  return (
    <>
      <h1>{t("galleryResults.title")}</h1>
      <p className={styles.intro}>{t("galleryResults.intro")}</p>
      <RunPairStatus baseline={b} scenario={s} canCancel cancelling={false} cancelProblem={null} onCancel={() => setCancelled(true)} />
      {(state === "failed" || cancelled) && <Callout tone="info">{t("runs.noComparison")}</Callout>}
      {(state === "results" || state === "incomparable") && <ResultsView data={data(state === "incomparable" ? incomparable : boardComparison, b)} />}
    </>
  );
}
