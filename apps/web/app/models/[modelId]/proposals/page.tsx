"use client";
import { use } from "react";
import { ProposalsPage } from "../../../../features/models/model-lists";
import { RequireSession } from "../../../_components/states";

/** The model's proposals (H-042: a section of the rail). */
export default function ModelProposalsPage({ params }: { params: Promise<{ modelId: string }> }) {
  const { modelId } = use(params);
  return (
    <RequireSession>
      <ProposalsPage modelId={modelId} />
    </RequireSession>
  );
}
