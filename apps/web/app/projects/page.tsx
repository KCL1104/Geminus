"use client";
import Link from "next/link";
import { useApi } from "../../lib/api/provider";
import { MorePages, RemoteView, RequireSession } from "../_components/states";
import { useRemote } from "../_components/use-remote";

function Projects() {
  const api = useApi();
  const [remote, retry] = useRemote((signal) => api.listProjects({ signal }), api);
  return (
    <RemoteView remote={remote} what="projects" onRetry={retry}>
      {(page) =>
        page.items.length === 0 ? (
          <p>No projects yet.</p>
        ) : (
          <>
            <ul>
              {page.items.map((p) => (
                <li key={p.id}>
                  <Link href={`/projects/${p.id}`}>{p.name}</Link>
                </li>
              ))}
            </ul>
            <MorePages cursor={page.next_cursor} shown={page.items.length} what="projects" />
          </>
        )
      }
    </RemoteView>
  );
}

export default function ProjectsPage() {
  return (
    <>
      <h1>Projects</h1>
      <RequireSession>
        <Projects />
      </RequireSession>
    </>
  );
}
