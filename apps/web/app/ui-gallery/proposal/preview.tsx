"use client";
// The AI proposal review on sample proposals, for review and the browser
// checks (T-031): the app mounts no session yet, so the signed-in page
// cannot render in a browser; this page renders the real ProposalRoute,
// API client and decisions, signed in as an editor (a viewer in "viewer"),
// against the in-page stand-in (stand-in.ts) instead of the Go API. No AI
// is called. Its links go to the other review pages. Not part of the product.
import Link from "next/link";
import { useMemo, useState } from "react";
import { createApiClient } from "../../../lib/api/client";
import { ApiProvider } from "../../../lib/api/provider";
import { AuthProvider } from "../../../lib/auth/session";
import { useI18n } from "../../../lib/i18n/provider";
import { Callout } from "../../../ui/callout";
import { type ProposalHrefs, ProposalRoute } from "../../../features/ai-proposals/proposal-view";
import styles from "../editor/preview.module.css";
import { type SampleState, sampleFor, states } from "./samples";
import { createProposalStandIn } from "./stand-in";

const hrefs: ProposalHrefs = { model: () => "/ui-gallery/editor", scenario: () => "/ui-gallery/results" };

export function ProposalPreview({ state }: { state: SampleState }) {
  const { t } = useI18n();
  const [standIn] = useState(() => createProposalStandIn({ sample: state }));
  const client = useMemo(() => createApiClient({ session: () => ({ organizationId: standIn.organizationId }), fetch: standIn.fetch }), [standIn]);
  const auth = useMemo(() => ({ session: { organizationId: standIn.organizationId }, role: sampleFor(state).role ?? ("editor" as const) }), [standIn, state]);
  return (
    <>
      <Callout tone="info">{t("galleryProposal.intro")}</Callout>
      <nav aria-label={t("galleryProposal.states")} className={styles.states}>
        {states.map((s) => (
          <Link key={s} href={`/ui-gallery/proposal?state=${s}`} aria-current={s === state ? "page" : undefined}>
            {t(`galleryProposal.state.${s}`)}
          </Link>
        ))}
      </nav>
      <AuthProvider value={auth}>
        <ApiProvider client={client}>
          <ProposalRoute proposalId={standIn.proposalId!} hrefs={hrefs} />
        </ApiProvider>
      </AuthProvider>
    </>
  );
}
