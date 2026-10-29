"use client";
import { use } from "react";
import { RunsPage } from "../../../../features/models/model-lists";
import { RequireSession } from "../../../_components/states";

/** The model's runs (H-042: a section of the rail). */
export default function ModelRunsPage({ params }: { params: Promise<{ modelId: string }> }) {
  const { modelId } = use(params);
  return (
    <RequireSession>
      <RunsPage modelId={modelId} />
    </RequireSession>
  );
}
