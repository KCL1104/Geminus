"use client";
// The gallery's sample states as one menu (H-042: the owner found a row of
// fifteen links too much): a labelled button that opens the states grouped,
// and a choice opens that state's page. Review surface only; production
// never serves the gallery (GEMINUS_UI_GALLERY is forbidden there).
import { useRouter } from "next/navigation";
import type { KeyboardEvent } from "react";
import { Button, Header, Label, ListBox, ListBoxItem, ListBoxSection, Popover, Select, SelectValue } from "react-aria-components";
import { ChevronDownIcon } from "../../ui/icons";
import select from "../../ui/select.module.css";
import styles from "./editor/preview.module.css";

export type StateGroup = { title: string | null; states: { id: string; label: string }[] };

export function StateMenu({ label, groups, current, href }: { label: string; groups: StateGroup[]; current: string; href: (id: string) => string }) {
  const router = useRouter();
  // Only a choice in the open list navigates (WCAG 3.2.2): the closed button ignores the arrows and type-ahead.
  const onlyByChoice = (e: KeyboardEvent<HTMLDivElement>) => {
    const onTrigger = (e.target as Element).getAttribute("aria-haspopup") === "listbox";
    if (onTrigger && (e.key === "ArrowLeft" || e.key === "ArrowRight" || e.key.length === 1)) e.stopPropagation();
  };
  return (
    <div className={styles.menu} data-state-menu onKeyDownCapture={onlyByChoice}>
      <Select value={current} onChange={(key) => key !== null && key !== current && router.push(href(String(key)))} className={select.select}>
        <Label className={select.label}>{label}</Label>
        <Button className={select.trigger}>
          <SelectValue className={select.value} />
          <ChevronDownIcon />
        </Button>
        <Popover className={select.popover}>
          <ListBox className={select.listbox}>
            {groups.map((g, i) => (
              <ListBoxSection key={g.title ?? i}>
                {g.title && <Header className={styles.group}>{g.title}</Header>}
                {g.states.map((s) => (
                  <ListBoxItem key={s.id} id={s.id} textValue={s.label} className={select.option}>
                    {s.label}
                  </ListBoxItem>
                ))}
              </ListBoxSection>
            ))}
          </ListBox>
        </Popover>
      </Select>
    </div>
  );
}
