"use client";
// The scenarios on the model's latest version, each a link to its page
// (T-032): the way back to a scenario and its earlier results once the
// person has left them. Nothing shows while the version has none.
import Link from "next/link";
import { MorePages, RemoteView } from "../../app/_components/states";
import { useRemote } from "../../app/_components/use-remote";
import { useApi } from "../../lib/api/provider";
import { useI18n } from "../../lib/i18n/provider";
import styles from "./simulations.module.css";

// ponytail: only the latest version's scenarios; older versions' stay reachable from their proposals. List every version's if people ask for them.
export function ScenarioList({ versionId, version }: { versionId: string; version: number }) {
  const api = useApi();
  const { t } = useI18n();
  const [remote, retry] = useRemote((signal) => api.listScenarios(versionId, { signal }), api, versionId);
  return (
    <RemoteView remote={remote} what="scenarios" onRetry={retry} headingLevel={2}>
      {(page) =>
        page.items.length > 0 && (
          <section aria-labelledby="scenarios-heading" className={styles.section}>
            <h2 id="scenarios-heading">{t("model.scenarios.title", { version: String(version) })}</h2>
            <ul className={styles.earlier}>
              {page.items.map((s) => (
                <li key={s.id}>
                  <Link href={`/scenarios/${s.id}`}>{s.name}</Link>
                </li>
              ))}
            </ul>
            <MorePages cursor={page.next_cursor} shown={page.items.length} what="scenarios" />
          </section>
        )
      }
    </RemoteView>
  );
}
