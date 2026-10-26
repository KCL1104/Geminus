"use client";
// An AI proposal: its state, the diff, the AI's assumptions, the server's
// checks and risk beside the AI's judgement, and a person's decision
// (T-031, hackathon slice H-029).
import { use } from "react";
import { ProposalRoute } from "../../../features/ai-proposals/proposal-view";
import { RequireSession } from "../../_components/states";

export default function ProposalPage({ params }: { params: Promise<{ proposalId: string }> }) {
  const { proposalId } = use(params);
  return (
    <RequireSession>
      <ProposalRoute proposalId={proposalId} />
    </RequireSession>
  );
}
