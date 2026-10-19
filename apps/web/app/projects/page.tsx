"use client";
import Link from "next/link";
import { useApi } from "../../lib/api/provider";
import { useI18n } from "../../lib/i18n/provider";
import list from "../../ui/list.module.css";
import { MorePages, RemoteView, RequireSession } from "../_components/states";
import { useRemote } from "../_components/use-remote";

function Projects() {
  const api = useApi();
  const { t, dateTime } = useI18n();
  const [remote, retry] = useRemote((signal) => api.listProjects({ signal }), api);
  return (
    <RemoteView remote={remote} what="projects" onRetry={retry}>
      {(page) =>
        page.items.length === 0 ? (
          <p className={list.empty}>{t("projects.empty")}</p>
        ) : (
          <>
            <ul className={list.list}>
              {page.items.map((p) => {
                const created = dateTime(p.created_at);
                return (
                  <li key={p.id}>
                    <Link href={`/projects/${p.id}`}>{p.name}</Link>
                    {created && <span className={list.meta}>{t("projects.created", { date: created })}</span>}
                  </li>
                );
              })}
            </ul>
            <MorePages cursor={page.next_cursor} shown={page.items.length} what="projects" />
          </>
        )
      }
    </RemoteView>
  );
}

export default function ProjectsPage() {
  const { t } = useI18n();
  return (
    <>
      <h1>{t("projects.title")}</h1>
      <RequireSession>
        <Projects />
      </RequireSession>
    </>
  );
}
