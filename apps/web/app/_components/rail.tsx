"use client";
// The app's left rail (H-042, DESIGN.md Layout): the brand at the top (the
// way to the projects), inside a model its four sections (the canvas, its
// scenarios, its AI proposals and its runs: each a page that exists), and at
// the bottom the account and settings. A page inside a model that is not
// under /models/<id> names its model with useRailModel once it has loaded it.
import Link from "next/link";
import { usePathname } from "next/navigation";
import { createContext, type ReactNode, useContext, useEffect, useState } from "react";
import { useI18n } from "../../lib/i18n/provider";
import type { Profile } from "../../lib/auth/profile";
import type { Theme } from "../../lib/theme/theme";
import { AskIcon, BranchIcon, CanvasIcon, RunsIcon } from "../../ui/icons";
import shell from "../../ui/shell.module.css";
import { AccountMenu } from "./account";

const RailContext = createContext<{ model: string | null; set: (id: string | null) => void }>({ model: null, set: () => {} });

export function RailProvider({ children }: { children: ReactNode }) {
  const [model, set] = useState<string | null>(null);
  return <RailContext.Provider value={{ model, set }}>{children}</RailContext.Provider>;
}

/** A page inside a model names it, so the rail offers the model's sections while the page is open. */
export function useRailModel(modelId: string | null | undefined): void {
  const { set } = useContext(RailContext);
  useEffect(() => {
    if (!modelId) return;
    set(modelId);
    return () => set(null);
  }, [modelId, set]);
}

/** The hook as an element, for a page that knows its model only inside a render prop. */
export function RailModel({ id }: { id: string }) {
  useRailModel(id);
  return null;
}

const sections = ["canvas", "scenarios", "proposals", "runs"] as const;
type Section = (typeof sections)[number];
const icons: Record<Section, () => ReactNode> = { canvas: CanvasIcon, scenarios: BranchIcon, proposals: AskIcon, runs: RunsIcon };
const href = (model: string, s: Section) => (s === "canvas" ? `/models/${model}` : `/models/${model}/${s}`);

/** The section a path belongs to: its own page, or a scenario's, its results' or a proposal's. */
function sectionOf(path: string, model: string): Section | null {
  if (path === `/models/${model}`) return "canvas";
  for (const s of sections) if (s !== "canvas" && path.startsWith(`/models/${model}/${s}`)) return s;
  if (path.startsWith("/scenarios/")) return "scenarios";
  if (path.startsWith("/proposals/")) return "proposals";
  return null;
}

export function AppRail({ initialTheme, profile }: { initialTheme: Theme; profile: Profile | null }) {
  const { t } = useI18n();
  const path = usePathname() ?? "";
  const named = useContext(RailContext).model;
  // Under /models/<id> the address names the model; elsewhere the page does.
  const model = /^\/models\/(mdl_[0-9A-Za-z]+)/.exec(path)?.[1] ?? named;
  const current = model === null ? null : sectionOf(path, model);
  return (
    <nav aria-label={t("rail.label")} className={shell.rail}>
      <Link href="/projects" className={shell.home} aria-label={t("nav.projects")}>
        <svg className={shell.mark} width="22" height="22" viewBox="0 0 20 20" aria-hidden="true" focusable="false">
          <rect x="0.75" y="0.75" width="12.5" height="12.5" rx="3.5" className={shell.markFront} />
          <rect x="6.75" y="6.75" width="12.5" height="12.5" rx="3.5" className={shell.markBack} />
        </svg>
      </Link>
      {model !== null && (
        <ul className={shell.sections}>
          {sections.map((s) => {
            const Icon = icons[s];
            return (
              <li key={s}>
                <Link href={href(model, s)} className={shell.item} aria-label={t(`rail.${s}`)} aria-current={current === s ? "page" : undefined}>
                  <Icon />
                  <span className={shell.tip} aria-hidden="true">
                    {t(`rail.${s}`)}
                  </span>
                </Link>
              </li>
            );
          })}
        </ul>
      )}
      <div className={shell.foot}>
        <AccountMenu initialTheme={initialTheme} profile={profile} />
      </div>
    </nav>
  );
}
