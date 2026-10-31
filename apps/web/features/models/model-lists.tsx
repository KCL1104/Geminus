"use client";
// A model's sections beside its canvas (H-042, the rail): its scenarios,
// the AI's proposals and the runs, each a list on the APIs that already
// existed (listVersions + listScenarios, listProposals, listSimulations).
// The model's name is a pill in the header, not a link: the rail's Canvas
// is the way back to it (H-027: no two links to the same place).
import Link from "next/link";
import type { ReactNode } from "react";
import { useApi } from "../../lib/api/provider";
import type { Model, Proposal, Run } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { PageHeader } from "../../ui/page-header";
import list from "../../ui/list.module.css";
import { MorePages, RemoteView } from "../../app/_components/states";
import { useRemote } from "../../app/_components/use-remote";
import { ScenarioList } from "../simulations/scenario-list";
import { visible } from "../ai-proposals/shown";

function Section({ model, title, children }: { model: Model; title: string; children: ReactNode }) {
  const { t } = useI18n();
  return (
    <>
      <PageHeader trail={[]} title={title} facts={[{ label: t("rail.canvas"), value: model.name }]} />
      {children}
    </>
  );
}

function WithModel({ modelId, children }: { modelId: string; children: (model: Model) => ReactNode }) {
  const api = useApi();
  const [remote, retry] = useRemote((signal) => api.getModel(modelId, { signal }), api, modelId);
  return (
    <RemoteView remote={remote} what="model" onRetry={retry}>
      {children}
    </RemoteView>
  );
}

export function ScenariosPage({ modelId }: { modelId: string }) {
  const { t } = useI18n();
  return (
    <WithModel modelId={modelId}>
      {(model) => (
        <Section model={model} title={t("rail.scenarios")}>
          <ScenarioList modelId={model.id} latestVersionId={model.latest_version.id} empty={<p className={list.empty}>{t("scenarios.empty")}</p>} />
        </Section>
      )}
    </WithModel>
  );
}

const proposalTone: Readonly<Record<Proposal["status"], string>> = {
  generating: "busy",
  needs_input: "warning",
  ready: "accent",
  invalid: "error",
  failed: "error",
  approved: "ok",
  rejected: "neutral",
  conflicted: "warning",
};

// H-052: a proposal the server refused or the AI never finished is folded
// away under the list, counted, so the first look is the ones to review.
const unfinished = (p: Proposal) => p.status === "failed" || p.status === "invalid";

export function ProposalsPage({ modelId }: { modelId: string }) {
  const api = useApi();
  const { t, dateTime } = useI18n();
  const [remote, retry] = useRemote((signal) => api.listProposals(modelId, { signal }), api, modelId);
  const rows = (items: readonly Proposal[]) => (
    <ul className={list.list}>
      {items.map((p) => (
        <li key={p.proposal_id}>
          <Link href={`/proposals/${p.proposal_id}`}>{p.summary == null ? t("proposals.noSummary") : <bdi>{visible(p.summary, "prose")}</bdi>}</Link>
          <span className={list.pill} data-tone={proposalTone[p.status]}>
            {t(`proposals.state.${p.status}`)}
          </span>
          <span className={list.meta}>{t(`proposals.mode.${p.mode}`)}</span>
          <span className={list.meta}>{dateTime(p.created_at)}</span>
        </li>
      ))}
    </ul>
  );
  return (
    <WithModel modelId={modelId}>
      {(model) => (
        <Section model={model} title={t("proposals.title")}>
          <RemoteView remote={remote} what="proposals" onRetry={retry} headingLevel={2}>
            {(page) => {
              if (page.items.length === 0) return <p className={list.empty}>{t("proposals.empty")}</p>;
              const shown = page.items.filter((p) => !unfinished(p));
              const folded = page.items.filter(unfinished);
              return (
                <>
                  {shown.length > 0 && rows(shown)}
                  {folded.length > 0 && (
                    <details className={list.folded}>
                      <summary>{t("proposals.unfinished", { count: folded.length })}</summary>
                      {rows(folded)}
                    </details>
                  )}
                  <MorePages cursor={page.next_cursor} shown={page.items.length} what="proposals" />
                </>
              );
            }}
          </RemoteView>
        </Section>
      )}
    </WithModel>
  );
}

const runTone: Readonly<Record<Run["status"], string>> = { queued: "neutral", running: "busy", completed: "ok", failed: "error", cancelled: "neutral" };

export function RunsPage({ modelId }: { modelId: string }) {
  const api = useApi();
  const { t, dateTime } = useI18n();
  const [remote, retry] = useRemote((signal) => api.listSimulations(modelId, { signal }), api, modelId);
  return (
    <WithModel modelId={modelId}>
      {(model) => (
        <Section model={model} title={t("runs.list.title")}>
          <RemoteView remote={remote} what="runs" onRetry={retry} headingLevel={2}>
            {(page) =>
              page.items.length === 0 ? (
                <p className={list.empty}>{t("runs.list.empty")}</p>
              ) : (
                <>
                  <ul className={list.list}>
                    {page.items.map((r) => (
                      <li key={r.id}>
                        {r.scenario_id === null ? <span className={list.name}>{r.name}</span> : <Link href={`/scenarios/${r.scenario_id}`}>{r.name}</Link>}
                        <span className={list.pill} data-tone={runTone[r.status]}>
                          {r.status === "running" && r.cancellation_requested ? t("runs.status.stopping") : t(`runs.status.${r.status}`)}
                        </span>
                        <span className={list.meta}>
                          {t("runs.progress", { done: String(r.progress.completed_replications), count: r.progress.total_replications })}
                        </span>
                        <span className={list.meta}>{dateTime(r.created_at)}</span>
                      </li>
                    ))}
                  </ul>
                  <MorePages cursor={page.next_cursor} shown={page.items.length} what="runs" />
                </>
              )
            }
          </RemoteView>
        </Section>
      )}
    </WithModel>
  );
}
