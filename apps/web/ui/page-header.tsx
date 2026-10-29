"use client";
// The page's header (H-040, restyled by H-041; DESIGN.md Layout): the way
// back (the parents' links), the page's title at Display size, its
// provenance as pills (label and value), and the page's actions at the
// right, level with the pills.
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
      <div className={styles.text}>
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
      </div>
      {actions && <div className={styles.actions}>{actions}</div>}
    </header>
  );
}
