"use client";
// A scenario's run and its baseline's, from queued to the results (T-032):
// the runs' state while they go, with one cancel; then, when both have
// completed, the results; when one has not, nothing to compare. The
// outcome is a status message (ADR-021: in the page, no toast). The runs
// must be this scenario's and a baseline (patch []), or nothing of them is
// shown (run-pair.ts checkPair).
import { useCallback } from "react";
import { useApi } from "../../lib/api/provider";
import { canEdit, useAuth } from "../../lib/auth/session";
import type { Run } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { ProblemView } from "../../app/_components/problem-view";
import { Loading, RemoteView } from "../../app/_components/states";
import { useRemote } from "../../app/_components/use-remote";
import { PageHeader } from "../../ui/page-header";
import { ShownStatus, StatusRegion } from "../../ui/status";
import { checkPair, pairPhase } from "../simulations/run-pair";
import { LineKey, RunPairStatus, type RunSide } from "../simulations/run-status";
import { useRunPair } from "../simulations/use-run-pair";
import { loadResults } from "./load";
import { ResultsView } from "./results-view";

type AnalysisRef = { id: string | null; onChange: (id: string | null) => void };

function Completed({ baseline, scenario, analysis }: { baseline: Run; scenario: Run; analysis?: AnalysisRef }) {
  const api = useApi();
  const [remote, retry] = useRemote((signal) => loadResults(api, baseline, scenario, signal), api, `${baseline.id}:${scenario.id}`);
  return (
    <RemoteView remote={remote} what="results" onRetry={retry}>
      {(data) => <ResultsView data={data} analysis={analysis} />}
    </RemoteView>
  );
}

export function RunResults({ scenarioId, baselineId, scenarioRunId, pollMs, analysis }: {
  scenarioId: string;
  baselineId: string;
  scenarioRunId: string;
  pollMs?: number;
  /** The AI explanation in the address (T-030). */
  analysis?: AnalysisRef;
}) {
  const api = useApi();
  const { role } = useAuth();
  const { t, gap } = useI18n();
  // Completed, each run's state is in the header band's provenance row (H-040); until then the Runs section
  // says it, with progress, failures and cancel (each thing once).
  const runFact = (side: RunSide, run: Run) => ({
    label: t(`runs.${side}`),
    icon: <LineKey side={side} />,
    value: (
      <>
        {t("runs.status.completed")}
        {gap}
        {t("runs.progress", { done: String(run.progress.completed_replications), count: run.progress.total_replications })}
      </>
    ),
  });
  const [scenario, retryScenario] = useRemote((signal) => api.getScenario(scenarioId, { signal }), api, scenarioId);
  const accept = useCallback((b: Run, s: Run) => checkPair(scenarioId, b, s), [scenarioId]);
  const pair = useRunPair(api, baselineId, scenarioRunId, { pollMs, accept });
  const phase = pairPhase(pair.baseline, pair.scenario);

  return (
    <RemoteView remote={scenario} what="scenario" onRetry={retryScenario}>
      {(s) => (
        <>
          <PageHeader
            trail={[
              { href: `/models/${s.model_id}`, label: t("scenario.back") },
              { href: `/scenarios/${s.id}`, label: t("runs.back") },
            ]}
            title={s.name}
            facts={phase === "completed" && pair.baseline && pair.scenario && !pair.refused ? [runFact("baseline", pair.baseline), runFact("scenario", pair.scenario)] : []}
          />
          {pair.refused ? (
            <ProblemView problem={pair.refused} />
          ) : (
            <>
              {pair.problem && (
                <RemoteView remote={{ state: "failed", problem: pair.problem }} what="runs" onRetry={pair.retry}>
                  {() => null}
                </RemoteView>
              )}
              {phase === "loading" && !pair.problem && <Loading what="runs" />}
              {/* Completed, the band says so; the section stays for progress, failures and cancel. */}
              {pair.baseline && pair.scenario && phase !== "completed" && (
                <RunPairStatus
                  baseline={pair.baseline}
                  scenario={pair.scenario}
                  canCancel={canEdit(role)}
                  cancelling={pair.cancelling}
                  cancelProblem={pair.cancelProblem}
                  onCancel={() => void pair.cancel()}
                />
              )}
              {/* The runs' outcome, announced when it arrives (WCAG 4.1.3); progress itself is not. The region
                  is never shown, so its look does not change as it speaks: completed is on each run's line
                  (H-039), and a stopped run's outcome is framed beside it (review M2). */}
              {pair.baseline && pair.scenario && (
                <StatusRegion message={phase === "completed" ? t("runs.done") : phase === "stopped" ? t("runs.noComparison") : null} quiet />
              )}
              {phase === "stopped" && <ShownStatus message={t("runs.noComparison")} />}
              {phase === "completed" && pair.baseline && pair.scenario && <Completed baseline={pair.baseline} scenario={pair.scenario} analysis={analysis} />}
            </>
          )}
        </>
      )}
    </RemoteView>
  );
}
