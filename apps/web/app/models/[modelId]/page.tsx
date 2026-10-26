"use client";
import { use } from "react";
import { ModelView } from "../../../features/model-editor/model-view";
import { RequireSession } from "../../_components/states";

export default function ModelPageRoute({ params }: { params: Promise<{ modelId: string }> }) {
  const { modelId } = use(params);
  return (
    <RequireSession>
      <ModelView modelId={modelId} />
    </RequireSession>
  );
}
