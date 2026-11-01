"use client";
// An AI proposal: its state, the diff, the AI's assumptions, the server's
// checks and risk beside the AI's judgement, and a person's decision
// (T-031, hackathon slice H-029). H-073 (5): an approved photo proposal's
// follow-up request opens the model page's Ask AI.
import { useRouter } from "next/navigation";
import { use } from "react";
import { ProposalRoute } from "../../../features/ai-proposals/proposal-view";
import { RequireSession } from "../../_components/states";

export default function ProposalPage({ params }: { params: Promise<{ proposalId: string }> }) {
  const { proposalId } = use(params);
  const router = useRouter();
  return (
    <RequireSession>
      <ProposalRoute proposalId={proposalId} onAsk={(modelId) => router.push(`/models/${modelId}`)} />
    </RequireSession>
  );
}
