"use client";
// The model's scenarios, each a link to its page (T-032): the way back to a
// scenario and its earlier results once the person has left them. Every
// version's, newest version first (T-057 review r1 M5: a publish must not
// hide the scenarios of the version before it). Nothing shows while the
// model has none.
import Link from "next/link";
import type { ApiClient, Result } from "../../lib/api/client";
import { MorePages, RemoteView } from "../../app/_components/states";
import { useRemote } from "../../app/_components/use-remote";
import { useApi } from "../../lib/api/provider";
import type { Scenario } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import styles from "./simulations.module.css";

type Listed = { items: { scenario: Scenario; version: number }[]; more: boolean };

// ponytail: one request for the versions' first page and one per version; a model-level scenario list on the API makes it one when models have many versions.
async function loadScenarios(api: ApiClient, modelId: string, signal: AbortSignal): Promise<Result<Listed>> {
  const versions = await api.listVersions(modelId, { signal });
  if (!versions.ok) return versions;
  const pages = await Promise.all(versions.data.items.map((v) => api.listScenarios(v.id, { signal })));
  const items: Listed["items"] = [];
  let more = versions.data.next_cursor !== null;
  for (const [i, page] of pages.entries()) {
    if (!page.ok) return page;
    more ||= page.data.next_cursor !== null;
    items.push(...page.data.items.map((scenario) => ({ scenario, version: versions.data.items[i]!.version })));
  }
  return { ok: true, data: { items, more }, requestId: versions.requestId, etag: null };
}

/** latestVersionId: the model's latest version, so a publish reads the list again; empty: what shows when there is none (H-042: the Scenarios page says so). */
export function ScenarioList({ modelId, latestVersionId, empty = null }: { modelId: string; latestVersionId: string; empty?: React.ReactNode }) {
  const api = useApi();
  const { t } = useI18n();
  const [remote, retry] = useRemote((signal) => loadScenarios(api, modelId, signal), api, `${modelId} ${latestVersionId}`);
  return (
    <RemoteView remote={remote} what="scenarios" onRetry={retry} headingLevel={2}>
      {({ items, more }) =>
        items.length === 0 ? (
          empty
        ) : (
          <section aria-labelledby="scenarios-heading" className={styles.section}>
            <h2 id="scenarios-heading">{t("model.scenarios.title")}</h2>
            <ul className={styles.earlier}>
              {items.map(({ scenario, version }) => (
                <li key={scenario.id}>
                  <Link href={`/scenarios/${scenario.id}`}>{scenario.name}</Link>
                  <span className={styles.status}>{t("model.scenarios.version", { version: String(version) })}</span>
                </li>
              ))}
            </ul>
            <MorePages cursor={more ? "more" : null} shown={items.length} what="scenarios" />
          </section>
        )
      }
    </RemoteView>
  );
}
