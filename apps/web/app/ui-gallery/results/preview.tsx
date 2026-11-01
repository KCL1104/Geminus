"use client";
// The results view in one state at a time (T-032), for review and the
// browser checks (apps/web-e2e): the app mounts no session yet, so the
// signed-in page cannot render in a browser; this page renders the same
// components with the synthetic board line, handed over as data by the
// page (features/results/fixtures). No API is called: what the results
// read for themselves (the model's runs for the reproducibility card, H-073)
// comes from the in-page stand-in (stand-in.ts). Its one control works:
// cancelling the running runs shows them cancelled. H-072: "line" and
// "still" show the electronics line template's runs (a fourth FCT tester),
// whose traces play in the line in motion; "still" holds it paused at
// Day 1, 22:00 (StillPlayback), so its screenshot does not move.
import { useMemo, useState } from "react";
import { createApiClient } from "../../../lib/api/client";
import { ApiProvider } from "../../../lib/api/provider";
import type { Comparison, Run } from "../../../lib/contracts";
import { useI18n } from "../../../lib/i18n/provider";
import { type BoardLineData, boardBaselineRunOf, boardScenarioRunOf, boardVersionOf } from "../../../features/results/fixtures/board-line-objects";
import { type ElectronicsLineData, lineBaselineRunOf, lineScenarioRunOf, lineVersionOf, STILL_AT } from "../../../features/results/fixtures/electronics-line-objects";
import { StillPlayback } from "../../../features/playback/still";
import type { ResultsData } from "../../../features/results/load";
import { ResultsView } from "../../../features/results/results-view";
import { RunPairStatus } from "../../../features/simulations/run-status";
import { ShownStatus, StatusRegion } from "../../../ui/status";
import styles from "../gallery.module.css";
import { createResultsStandIn } from "./stand-in";

export type PreviewState = "results" | "incomparable" | "running" | "failed" | "line" | "still";

const progress = (revision: number, done: number) => ({ completed_replications: done, total_replications: 5, events_processed: done * 1300, revision });

function incomparable(c: Comparison): Comparison {
  return {
    ...c,
    comparable: false,
    incomparable_reasons: ["ENGINE_VERSION_MISMATCH"],
    warnings: [],
    metrics: c.metrics.map((m) => ({ ...m, absolute_delta: null, relative_delta: null, status: "incomparable", reason: "RUNS_NOT_COMPARABLE" })),
  };
}

function results(data: BoardLineData, comparison: Comparison, baseline: Run): ResultsData {
  return {
    comparison,
    version: boardVersionOf(data.model),
    baseline: { run: baseline, result: data.results.baseline, trace: { state: "loaded", trace: data.traces.baseline } },
    scenario: { run: boardScenarioRunOf(data.model), result: data.results.scenario, trace: { state: "loaded", trace: data.traces.scenario } },
  };
}

/** The results view over the stand-in, one client for as long as the data stays. */
function Results({ data }: { data: ResultsData }) {
  const client = useMemo(() => {
    const standIn = createResultsStandIn(data);
    return createApiClient({ session: () => ({ organizationId: standIn.organizationId }), fetch: standIn.fetch });
  }, [data]);
  return (
    <ApiProvider client={client}>
      <ResultsView data={data} />
    </ApiProvider>
  );
}

function lineResults(line: ElectronicsLineData): ResultsData {
  return {
    comparison: line.comparison,
    version: lineVersionOf(line.model),
    baseline: { run: lineBaselineRunOf(line.model), result: line.results.baseline, trace: { state: "loaded", trace: line.traces.baseline } },
    scenario: { run: lineScenarioRunOf(line.model), result: line.results.scenario, trace: { state: "loaded", trace: line.traces.scenario } },
  };
}

/** line: the electronics line's engine output, for the "line" and "still" states. */
export function ResultsPreview({ state, data, line }: { state: PreviewState; data: BoardLineData; line?: ElectronicsLineData }) {
  const { t } = useI18n();
  // Built once: the stages' motion is keyed on these runs and traces.
  const results = useMemo(() => (line ? lineResults(line) : null), [line]);
  if ((state === "line" || state === "still") && results) {
    const view = <Results data={results} />;
    return (
      <>
        <h1>{t("galleryResults.title")}</h1>
        <p className={styles.intro}>{t("galleryResults.introLine")}</p>
        <RunPairStatus baseline={results.baseline.run} scenario={results.scenario.run} canCancel cancelling={false} cancelProblem={null} onCancel={() => {}} />
        <StatusRegion message={t("runs.done")} quiet />
        {state === "still" ? <StillPlayback at={STILL_AT}>{view}</StillPlayback> : view}
      </>
    );
  }
  return <BoardResults state={state} data={data} />;
}

function BoardResults({ state, data }: { state: PreviewState; data: BoardLineData }) {
  const { t } = useI18n();
  const [cancelled, setCancelled] = useState(false);
  const ir = data.model;
  // Built once per state, so the results' own reads are not asked again on every render.
  const shown = useMemo(
    () => (state === "results" || state === "incomparable" ? results(data, state === "incomparable" ? incomparable(data.comparison) : data.comparison, boardBaselineRunOf(ir, state === "incomparable" ? { engine_version: "des-go-0.4.0" } : {})) : null),
    [data, ir, state],
  );
  const running: [Run, Run] = cancelled
    ? [boardBaselineRunOf(ir, { status: "cancelled", progress: progress(5, 2) }), boardScenarioRunOf(ir, { status: "cancelled", progress: progress(5, 1) })]
    : [boardBaselineRunOf(ir, { status: "running", progress: progress(3, 2) }), boardScenarioRunOf(ir, { status: "running", progress: progress(3, 1) })];
  const failed = boardScenarioRunOf(ir, {
    status: "failed",
    progress: progress(4, 2),
    error: { error: { code: "MODEL_VALIDATION_FAILED", message: "the run exceeded its event budget (max_events)", details: [{ path: "/execution_limits/max_events", reason: "the run exceeded its event budget", code: "EVENT_BUDGET_EXCEEDED" }], request_id: "req_01M4YDQK00V6H8S0ST4F7510KR" } },
  });
  const [b, s] = shown ? [shown.baseline.run, shown.scenario.run] : state === "running" ? running : [boardBaselineRunOf(ir), failed];
  return (
    <>
      <h1>{t("galleryResults.title")}</h1>
      <p className={styles.intro}>{t("galleryResults.intro")}</p>
      <RunPairStatus baseline={b} scenario={s} canCancel cancelling={false} cancelProblem={null} onCancel={() => setCancelled(true)} />
      <StatusRegion message={state === "failed" || cancelled ? t("runs.noComparison") : state === "running" ? null : t("runs.done")} quiet />
      {(state === "failed" || cancelled) && <ShownStatus message={t("runs.noComparison")} />}
      {shown && <Results data={shown} />}
    </>
  );
}
