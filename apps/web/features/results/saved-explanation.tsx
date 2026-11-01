"use client";
// A finished comparison's saved explanation (H-073 slice 1). The AI
// explanation of a scenario beside its baseline costs a provider call and a
// wait; once one is stored, the page shows it again instead of asking anew.
// With no analysis in the address, this asks the server for the newest
// completed analysis of the pair (listAnalyses, one item) and shows it in
// the explanation's own view, labelled with the model that wrote it (from
// the server's generated_by) and when it was saved. An editor can still ask
// again: the form it always was. With none saved, or an analysis in the
// address, it is the Analysis panel unchanged.
//
// A drop-in for <Analysis>, with the same props: results-view.tsx mounts it
// in Analysis's place (H-073's mount note). Every figure is the server's.
import { useEffect, useState } from "react";
import { ProblemView } from "../../app/_components/problem-view";
import type { Problem } from "../../lib/api/problem";
import { useApi } from "../../lib/api/provider";
import { canEdit, useAuth } from "../../lib/auth/session";
import type { Analyses } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { StatusRegion } from "../../ui/status";
import { Analysis, Explained } from "./analysis";
import { withNodes } from "./metrics-table";
import styles from "./results.module.css";

type Props = Parameters<typeof Analysis>[0];

/** A completed analysis of the pair, as listAnalyses gives it. */
export type SavedRecord = Analyses["items"][number];

export type Saved =
  | { state: "loading" }
  | { state: "none" }
  | { state: "found"; record: SavedRecord }
  | { state: "failed"; problem: Problem };

/**
 * The newest completed analysis of run `runId` beside baseline run
 * `baselineRunId`, and a retry for when the read failed. An item that is
 * not a completed analysis of exactly this pair is not shown here (as the
 * address's analysis of other runs is not; T-030 review r1 M2).
 */
export function useSavedAnalysis(runId: string, baselineRunId: string): [Saved, () => void] {
  const api = useApi();
  const [attempt, setAttempt] = useState(0);
  const key = `${runId} ${baselineRunId} ${attempt}`;
  const [answer, setAnswer] = useState<{ key: string; saved: Saved } | null>(null);
  useEffect(() => {
    const ctl = new AbortController();
    void api.listAnalyses(runId, baselineRunId, { limit: 1, signal: ctl.signal }).then((r) => {
      if (ctl.signal.aborted) return;
      if (!r.ok) return setAnswer({ key, saved: { state: "failed", problem: r.problem } });
      const item = r.data.items[0];
      const ours = item !== undefined && item.status === "completed" && item.analysis != null && item.run_id === runId && item.baseline_run_id === baselineRunId;
      setAnswer({ key, saved: ours ? { state: "found", record: item } : { state: "none" } });
    });
    return () => ctl.abort();
  }, [api, runId, baselineRunId, key]);
  const saved: Saved = answer?.key === key ? answer.saved : { state: "loading" };
  return [saved, () => setAttempt((n) => n + 1)];
}

export function SavedExplanation(props: Props) {
  const { analysisId, onAnalysis } = props;
  // Once the person asks again, the panel is the form and what follows it, never the saved one again.
  const [asking, setAsking] = useState(false);
  const change = (id: string | null) => {
    if (id === null) setAsking(true);
    onAnalysis(id);
  };
  if (analysisId !== null || asking) return <Analysis {...props} onAnalysis={change} />;
  return <SavedOrAsk {...props} onAnalysis={change} onAsk={() => setAsking(true)} />;
}

function SavedOrAsk({ onAsk, ...props }: Props & { onAsk: () => void }) {
  const { baseline, scenario, rows } = props;
  const { role } = useAuth();
  const { t, dateTime } = useI18n();
  const [saved, retry] = useSavedAnalysis(scenario.id, baseline.id);
  if (saved.state === "none") return <Analysis {...props} />;
  const editor = canEdit(role);
  return (
    <section aria-labelledby="analysis-heading" className={styles.section}>
      <h2 id="analysis-heading">{t("analysis.title")}</h2>
      <StatusRegion message={saved.state === "loading" ? t("analysis.saved.loading") : null} />
      {saved.state === "failed" && (
        <>
          <ProblemView problem={saved.problem} headingLevel={3} />
          <Button onPress={retry}>{t("retry")}</Button>
        </>
      )}
      {saved.state === "found" && (
        <>
          <p className={styles.note}>
            {withNodes(t("analysis.saved.when", { time: "\u0000time\u0000" }), {
              time: <time dateTime={saved.record.created_at}>{dateTime(saved.record.created_at)}</time>,
            })}
          </p>
          <Explained analysis={saved.record.analysis} rows={rows} generatedBy={saved.record.generated_by} modelId={editor ? scenario.model_id : null}
            analysisId={saved.record.id} baseline={baseline} />
          {editor && <Button onPress={onAsk}>{t("analysis.again")}</Button>}
        </>
      )}
    </section>
  );
}
