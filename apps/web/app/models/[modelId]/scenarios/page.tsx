"use client";
import { use } from "react";
import { ScenariosPage } from "../../../../features/models/model-lists";
import { RequireSession } from "../../../_components/states";

/** The model's scenarios (H-042: a section of the rail). */
export default function ModelScenariosPage({ params }: { params: Promise<{ modelId: string }> }) {
  const { modelId } = use(params);
  return (
    <RequireSession>
      <ScenariosPage modelId={modelId} />
    </RequireSession>
  );
}
