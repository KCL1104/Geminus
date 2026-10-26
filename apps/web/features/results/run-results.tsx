"use client";
// A scenario's run and its baseline's, from queued to the results (T-032):
// the runs' state while they go, with one cancel; then, when both have
// completed, the results; when one has not, nothing to compare. The
// outcome is a status message (ADR-021: in the page, no toast). The runs
// must be this scenario's and a baseline (patch []), or nothing of them is
// shown (run-pair.ts checkPair).
import Link from "next/link";
import { useCallback } from "react";
import { useApi } from "../../lib/api/provider";
import { canEdit, useAuth } from "../../lib/auth/session";
import type { Run } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { ProblemView } from "../../app/_components/problem-view";
import { Loading, RemoteView } from "../../app/_components/states";
import { useRemote } from "../../app/_components/use-remote";
import { StatusRegion } from "../../ui/status";
import { checkPair, pairPhase } from "../simulations/run-pair";
import { RunPairStatus } from "../simulations/run-status";
import { useRunPair } from "../simulations/use-run-pair";
import { loadResults } from "./load";
import { ResultsView } from "./results-view";

function Completed({ baseline, scenario }: { baseline: Run; scenario: Run }) {
  const api = useApi();
  const [remote, retry] = useRemote((signal) => loadResults(api, baseline, scenario, signal), api, `${baseline.id}:${scenario.id}`);
  return (
    <RemoteView remote={remote} what="results" onRetry={retry}>
      {(data) => <ResultsView data={data} />}
    </RemoteView>
  );
}

export function RunResults({ scenarioId, baselineId, scenarioRunId, pollMs }: { scenarioId: string; baselineId: string; scenarioRunId: string; pollMs?: number }) {
  const api = useApi();
  const { role } = useAuth();
  const { t } = useI18n();
  const [scenario, retryScenario] = useRemote((signal) => api.getScenario(scenarioId, { signal }), api, scenarioId);
  const accept = useCallback((b: Run, s: Run) => checkPair(scenarioId, b, s), [scenarioId]);
  const pair = useRunPair(api, baselineId, scenarioRunId, { pollMs, accept });
  const phase = pairPhase(pair.baseline, pair.scenario);

  return (
    <RemoteView remote={scenario} what="scenario" onRetry={retryScenario}>
      {(s) => (
        <>
          <h1>{s.name}</h1>
          <p>
            <Link href={`/scenarios/${s.id}`}>{t("runs.back")}</Link>
          </p>
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
              {pair.baseline && pair.scenario && (
                <RunPairStatus
                  baseline={pair.baseline}
                  scenario={pair.scenario}
                  canCancel={canEdit(role)}
                  cancelling={pair.cancelling}
                  cancelProblem={pair.cancelProblem}
                  onCancel={() => void pair.cancel()}
                />
              )}
              {/* The runs' outcome, announced when it arrives (WCAG 4.1.3); progress itself is not. */}
              {pair.baseline && pair.scenario && (
                <StatusRegion message={phase === "completed" ? t("runs.done") : phase === "stopped" ? t("runs.noComparison") : null} />
              )}
              {phase === "completed" && pair.baseline && pair.scenario && <Completed baseline={pair.baseline} scenario={pair.scenario} />}
            </>
          )}
        </>
      )}
    </RemoteView>
  );
}
