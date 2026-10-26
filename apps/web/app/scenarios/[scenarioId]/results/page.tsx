"use client";
// A scenario's run and its baseline's, followed to their results (T-032).
// The address names both runs, so the page survives a reload; a missing,
// repeated or malformed run ID is not found, and nothing is asked for it.
import { use } from "react";
import { isID } from "../../../../lib/api/ids";
import type { Problem } from "../../../../lib/api/problem";
import { RunResults } from "../../../../features/results/run-results";
import { ProblemView } from "../../../_components/problem-view";
import { RequireSession } from "../../../_components/states";

type Search = Record<string, string | string[] | undefined>;

const notFound: Problem = { kind: "not_found", code: null, status: null, message: "The runs in the address do not exist.", requestId: null, details: [], retryAfterSeconds: null };
const one = (v: string | string[] | undefined) => (typeof v === "string" ? v : "");

export default function ResultsPage({ params, searchParams }: { params: Promise<{ scenarioId: string }>; searchParams: Promise<Search> }) {
  const { scenarioId } = use(params);
  const q = use(searchParams);
  const baseline = one(q.baseline);
  const scenario = one(q.scenario);
  const valid = isID("scn", scenarioId) && isID("sim", baseline) && isID("sim", scenario) && baseline !== scenario;
  return <RequireSession>{valid ? <RunResults scenarioId={scenarioId} baselineId={baseline} scenarioRunId={scenario} /> : <ProblemView problem={notFound} />}</RequireSession>;
}
