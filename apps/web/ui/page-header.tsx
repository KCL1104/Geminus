"use client";
// The page's header band (H-040, DESIGN.md Layout): under the app strip, on
// the Pale Shell, the way back (the parents' links), the page's title, then
// the provenance row, label and value groups divided by hairline verticals
// that ends at the right edge in the page's actions. It reaches both edges
// of the window; the page under it keeps its own width.
import Link from "next/link";
import type { ReactNode } from "react";
import { useI18n } from "../lib/i18n/provider";
import styles from "./page-header.module.css";

export type Fact = { label: string; value: ReactNode; icon?: ReactNode };

export function PageHeader({ trail, title, facts = [], actions }: {
  trail: { href: string; label: string }[];
  title: ReactNode;
  facts?: Fact[];
  actions?: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <header className={styles.band}>
      <div className={styles.titleRow}>
        {trail.length > 0 && (
          <nav aria-label={t("nav.trail")}>
            <ol className={styles.trail}>
              {trail.map((c) => (
                <li key={c.href}>
                  <Link href={c.href}>{c.label}</Link>
                </li>
              ))}
            </ol>
          </nav>
        )}
        <h1>{title}</h1>
      </div>
      {(facts.length > 0 || actions) && (
        <div className={styles.row}>
          {facts.length > 0 && (
            <dl className={styles.facts}>
              {facts.map((f) => (
                <div key={f.label} className={styles.fact}>
                  <dt>{f.label}</dt>
                  <dd>
                    {f.icon}
                    {f.value}
                  </dd>
                </div>
              ))}
            </dl>
          )}
          {actions && <div className={styles.actions}>{actions}</div>}
        </div>
      )}
    </header>
  );
}
