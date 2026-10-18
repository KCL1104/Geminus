"use client";
import Link from "next/link";
import { use } from "react";
import { useApi } from "../../../lib/api/provider";
import { useI18n } from "../../../lib/i18n/provider";
import { MorePages, RemoteView, RequireSession } from "../../_components/states";
import { useRemote } from "../../_components/use-remote";

function Models({ projectId }: { projectId: string }) {
  const api = useApi();
  const { t } = useI18n();
  const [remote, retry] = useRemote((signal) => api.listModels(projectId, { signal }), api, projectId);
  return (
    <RemoteView remote={remote} what="models" onRetry={retry}>
      {(page) =>
        page.items.length === 0 ? (
          <p>{t("models.empty")}</p>
        ) : (
          <>
            <ul>
              {page.items.map((m) => (
                <li key={m.id}>
                  <Link href={`/models/${m.id}`}>{m.name}</Link> — {t("models.version", { version: String(m.latest_version.version) })}
                  {m.draft.out_of_date && <> {t("models.outOfDate")}</>}
                </li>
              ))}
            </ul>
            <MorePages cursor={page.next_cursor} shown={page.items.length} what="models" />
          </>
        )
      }
    </RemoteView>
  );
}

export default function ProjectPage({ params }: { params: Promise<{ projectId: string }> }) {
  const { projectId } = use(params);
  const { t } = useI18n();
  return (
    <>
      <h1>{t("models.title")}</h1>
      <p>
        <Link href="/projects">{t("models.allProjects")}</Link>
      </p>
      <RequireSession>
        <Models projectId={projectId} />
      </RequireSession>
    </>
  );
}
