"use client";
// H-061 A8 (PROPOSED; H-056 §4 step 2): "Let the AI test these", under a
// completed explanation's "What to try next", for an owner or editor in a
// build with investigations on. It starts an investigation from this
// analysis (entry B): its baseline is reused and its suggestions seed the AI,
// which then saves, runs and explains up to 3 of them by itself and never
// publishes. When the baseline cannot be reused (it changes the model, or is
// over the AI's caps) the page says why instead; when the reader already has
// an investigation running on this model, the button is a link to watch it.
// The same request again (a retry) reuses its key (D-011).
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { ProblemView } from "../../app/_components/problem-view";
import { newIdempotencyKey } from "../../lib/api/client";
import type { Problem } from "../../lib/api/problem";
import { useApi } from "../../lib/api/provider";
import { useAuth } from "../../lib/auth/session";
import type { PlannerModel, ProposalModel, Run } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { AskIcon } from "../../ui/icons";
import { StatusRegion } from "../../ui/status";
import { advice } from "../ai-proposals/ask-form";
import styles from "../results/results.module.css";
import { PlannerChoice } from "./act-fields";
import { DEFAULT_PLANNER } from "./flags";
import { baselineFits, fromAnalysis } from "./request";

export function TestThese({ modelId, analysisId, baseline, aiModel }: {
  modelId: string;
  analysisId: string;
  /** The analysis's baseline run, which the investigation reuses. */
  baseline: Run;
  /** The proposal model chosen above the suggestions (H-049). */
  aiModel: ProposalModel;
}) {
  const api = useApi();
  const router = useRouter();
  const { userId } = useAuth();
  const { t, locale } = useI18n();
  const [planner, setPlanner] = useState<PlannerModel>(DEFAULT_PLANNER);
  const [running, setRunning] = useState<string | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [busy, setBusy] = useState(false);
  const sent = useRef<{ request: string; key: string } | null>(null);
  // A person who has left the results is not pulled to the investigation when it answers (as ProposeNext).
  const here = useRef(true);
  useEffect(() => {
    here.current = true;
    return () => {
      here.current = false;
    };
  }, []);

  /** The reader's own running investigation on this model, if the first page lists one. */
  const findRunning = useCallback(
    async (signal?: AbortSignal): Promise<string | null> => {
      if (userId == null) return null;
      const r = await api.listInvestigations(modelId, { signal });
      if (!r.ok) return null;
      return r.data.items.find((i) => i.status === "running" && i.created_by === userId && i.model_id === modelId)?.id ?? null;
    },
    [api, modelId, userId],
  );
  useEffect(() => {
    const ctl = new AbortController();
    void findRunning(ctl.signal).then((id) => {
      if (!ctl.signal.aborted) setRunning(id);
    });
    return () => ctl.abort();
  }, [findRunning]);

  const fits = baselineFits(baseline);
  if (running !== null) {
    return (
      <p className={styles.proposeNext}>
        <Link href={`/investigations/${running}`}>{t("investigation.watch")}</Link>
      </p>
    );
  }
  if (fits !== "fits") return <p className={styles.note}>{t(fits === "changed" ? "investigation.testThese.changed" : "investigation.testThese.limits")}</p>;

  const start = async () => {
    if (busy) return;
    setBusy(true);
    setProblem(null);
    const body = fromAnalysis({ analysis: analysisId, locale, model: aiModel, planner });
    const request = `${modelId} ${JSON.stringify(body)}`;
    if (sent.current?.request !== request) sent.current = { request, key: newIdempotencyKey() };
    const r = await api.createInvestigation(modelId, body, sent.current.key);
    if (!here.current) return;
    setBusy(false);
    if (r.ok) return router.push(`/investigations/${r.data.investigation_id}`);
    // Already running (here or elsewhere): when it is on this model, offer to watch it.
    if (r.problem.details[0]?.code === "INVESTIGATION_RUNNING") {
      const id = await findRunning();
      if (!here.current) return;
      if (id !== null) return setRunning(id);
    }
    setProblem(r.problem);
  };

  return (
    <div className={styles.proposeNext} data-test-these>
      <p className={styles.note}>{t("investigation.testTheseHint")}</p>
      <PlannerChoice value={planner} onChange={setPlanner} />
      <Button variant="ai" icon={<AskIcon />} blocked={busy} onPress={() => void start()}>
        {t("investigation.testThese")}
      </Button>
      {problem && <ProblemView problem={problem} headingLevel={3} guidance={advice(problem)} />}
      <StatusRegion message={busy ? t("investigation.starting") : null} />
    </div>
  );
}
