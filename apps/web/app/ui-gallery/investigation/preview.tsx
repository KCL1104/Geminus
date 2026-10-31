"use client";
// The AI investigation page on sample investigations, for review and the
// browser checks (H-061 A8): every state a person can meet, and AI text that
// tries to hide or reorder what it says. This page renders the real
// InvestigationRoute and API client against the in-page stand-in
// (stand-in.ts) instead of the Go API, signed in as the requester (a viewer
// in "viewer"), with the clock fixed at two minutes after the sample
// started. No AI is called; Stop changes only this page. Its links go to
// the other review pages. Not part of the product.
import { useMemo, useState } from "react";
import { createApiClient } from "../../../lib/api/client";
import { ApiProvider } from "../../../lib/api/provider";
import { AuthProvider } from "../../../lib/auth/session";
import { useI18n } from "../../../lib/i18n/provider";
import { Callout } from "../../../ui/callout";
import { type InvestigationHrefs, InvestigationRoute } from "../../../features/investigations/investigation-view";
import { StateMenu } from "../state-menu";
import { type SampleState, sampleCaller, sampleInvestigation, states } from "./samples";
import { createInvestigationStandIn } from "./stand-in";

const groups = [
  { id: "going", states: ["running", "planning", "refused", "viewer"] },
  { id: "ended", states: ["finished", "stopped", "edit", "hostile"] },
] as const satisfies readonly { id: string; states: readonly (typeof states)[number][] }[];

// One address per thing, as the product's are (so the page never seems to link one place twice): the other review pages, told apart by the ID.
const hrefs: InvestigationHrefs = {
  proposal: (id) => `/ui-gallery/proposal?state=scenario&proposal=${id}`,
  scenario: (id) => `/ui-gallery/results?scenario=${id}`,
  results: (_s, _b, run, analysis) => `/ui-gallery/results?run=${run}${analysis ? `&analysis=${analysis}` : ""}`,
};

/** Two minutes after the samples started (2026-11-01T06:00:00Z): 18 minutes left. */
const NOW = Date.parse("2026-11-01T06:02:00Z");
const now = () => NOW;

export function InvestigationPreview({ state }: { state: SampleState }) {
  const { t } = useI18n();
  const [standIn] = useState(() => createInvestigationStandIn({ investigation: sampleInvestigation(state), caller: sampleCaller(state) }));
  const client = useMemo(() => createApiClient({ session: () => ({ organizationId: standIn.organizationId }), fetch: standIn.fetch }), [standIn]);
  const auth = useMemo(() => ({ session: { organizationId: standIn.organizationId }, ...sampleCaller(state) }), [standIn, state]);
  return (
    <>
      <Callout tone="info">{t("galleryInvestigation.intro")}</Callout>
      <StateMenu
        label={t("galleryInvestigation.states")}
        current={state}
        href={(s) => `/ui-gallery/investigation?state=${s}`}
        groups={groups.map((g) => ({
          title: t(`galleryInvestigation.group.${g.id}`),
          states: g.states.map((s) => ({ id: s, label: t(`galleryInvestigation.state.${s}`) })),
        }))}
      />
      <AuthProvider value={auth}>
        <ApiProvider client={client}>
          <InvestigationRoute investigationId={standIn.investigationId} hrefs={hrefs} now={now} />
        </ApiProvider>
      </AuthProvider>
    </>
  );
}
