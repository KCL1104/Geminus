"use client";
// The AI proposal review on sample proposals, for review and the browser
// checks (T-031): every state of a proposal, and cases a live API rarely
// gives (a hostile answer, a base that moves just before the approval).
// This page renders the real ProposalRoute, API client and decisions,
// signed in as an editor (a viewer in "viewer"), against the in-page
// stand-in (stand-in.ts) instead of the Go API. The signed-in page itself
// (/proposals/<id>, through the BFF) is checked in the browser against the
// e2e fake API (apps/web-e2e/tests/proposal.spec.ts). No AI is called. Its
// links go to the other review pages. Not part of the product.
import { useMemo, useState } from "react";
import { createApiClient } from "../../../lib/api/client";
import { ApiProvider } from "../../../lib/api/provider";
import { AuthProvider } from "../../../lib/auth/session";
import { useI18n } from "../../../lib/i18n/provider";
import { Callout } from "../../../ui/callout";
import { type ProposalHrefs, ProposalRoute } from "../../../features/ai-proposals/proposal-view";
import { StateMenu } from "../state-menu";
import { type SampleState, sampleFor, states } from "./samples";
import { createProposalStandIn } from "./stand-in";

// The fifteen samples in three groups (H-042); every state stays: the tests use each.
const groups = [
  { id: "review", states: ["ready", "scenario", "new-steps", "photo", "hostile", "viewer"] },
  { id: "progress", states: ["generating", "needs_input", "invalid", "failed"] },
  { id: "decided", states: ["approved", "rejected", "conflicted", "conflict", "stale"] },
] as const satisfies readonly { id: string; states: readonly (typeof states)[number][] }[];

const hrefs: ProposalHrefs = { model: () => "/ui-gallery/editor", scenario: () => "/ui-gallery/results" };

export function ProposalPreview({ state }: { state: SampleState }) {
  const { t } = useI18n();
  const [standIn] = useState(() => createProposalStandIn({ sample: state }));
  const client = useMemo(() => createApiClient({ session: () => ({ organizationId: standIn.organizationId }), fetch: standIn.fetch }), [standIn]);
  const auth = useMemo(() => ({ session: { organizationId: standIn.organizationId }, role: sampleFor(state).role ?? ("editor" as const) }), [standIn, state]);
  return (
    <>
      <Callout tone="info">{t("galleryProposal.intro")}</Callout>
      <StateMenu
        label={t("galleryProposal.states")}
        current={state}
        href={(s) => `/ui-gallery/proposal?state=${s}`}
        groups={groups.map((g) => ({
          title: t(`galleryProposal.group.${g.id}`),
          states: g.states.map((s) => ({ id: s, label: t(`galleryProposal.state.${s}`) })),
        }))}
      />
      <AuthProvider value={auth}>
        <ApiProvider client={client}>
          <ProposalRoute proposalId={standIn.proposalId!} hrefs={hrefs} />
        </ApiProvider>
      </AuthProvider>
    </>
  );
}
