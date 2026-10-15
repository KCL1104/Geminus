"use client";
import Link from "next/link";
import { use } from "react";
import { useApi } from "../../../lib/api/provider";
import { RemoteView, RequireSession } from "../../_components/states";
import { useRemote } from "../../_components/use-remote";

function Models({ projectId }: { projectId: string }) {
  const api = useApi();
  const remote = useRemote((signal) => api.listModels(projectId, { signal }), api, projectId);
  return (
    <RemoteView remote={remote} what="models">
      {(page) =>
        page.items.length === 0 ? (
          <p>No models in this project.</p>
        ) : (
          <ul>
            {page.items.map((m) => (
              <li key={m.id}>
                <Link href={`/models/${m.id}`}>{m.name}</Link> — version {m.latest_version.version}
                {m.draft.out_of_date && " (draft out of date)"}
              </li>
            ))}
          </ul>
        )
      }
    </RemoteView>
  );
}

export default function ProjectPage({ params }: { params: Promise<{ projectId: string }> }) {
  const { projectId } = use(params);
  return (
    <>
      <h1>Models</h1>
      <p>
        <Link href="/projects">All projects</Link>
      </p>
      <RequireSession>
        <Models projectId={projectId} />
      </RequireSession>
    </>
  );
}
