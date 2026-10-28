"use client";
// A scenario: run it beside its baseline (T-032, hackathon slice H-029).
import { useRouter } from "next/navigation";
import { use } from "react";
import { useApi } from "../../../lib/api/provider";
import { loadScenarioPage, resultsHref } from "../../../features/simulations/load";
import { SimulateView } from "../../../features/simulations/simulate-view";
import { RemoteView, RequireSession } from "../../_components/states";
import { useRemote } from "../../_components/use-remote";

function ScenarioRoute({ scenarioId }: { scenarioId: string }) {
  const api = useApi();
  const router = useRouter();
  const [remote, retry] = useRemote((signal) => loadScenarioPage(api, scenarioId, signal), api, scenarioId);
  return (
    <RemoteView remote={remote} what="scenario" onRetry={retry}>
      {(page) => (
        <SimulateView
          scenario={page.scenario}
          version={page.version}
          onStarted={(baseline, scenario) => router.push(resultsHref(page.scenario.id, baseline, scenario))}
        />
      )}
    </RemoteView>
  );
}

export default function ScenarioPage({ params }: { params: Promise<{ scenarioId: string }> }) {
  const { scenarioId } = use(params);
  return (
    <RequireSession>
      <ScenarioRoute scenarioId={scenarioId} />
    </RequireSession>
  );
}
