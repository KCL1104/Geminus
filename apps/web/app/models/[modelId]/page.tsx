"use client";
import { useRouter } from "next/navigation";
import { use } from "react";
import { ModelView } from "../../../features/model-editor/model-view";
import { RequireSession } from "../../_components/states";

export default function ModelPageRoute({ params }: { params: Promise<{ modelId: string }> }) {
  const { modelId } = use(params);
  const router = useRouter();
  return (
    <RequireSession>
      {/* A proposal the AI was asked for opens on its own page (T-031). */}
      <ModelView modelId={modelId} onProposal={(id) => router.push(`/proposals/${id}`)} />
    </RequireSession>
  );
}
