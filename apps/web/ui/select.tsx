"use client";
import type { KeyboardEvent, ReactNode } from "react";
import {
  Button as AriaButton,
  Label,
  ListBox,
  ListBoxItem,
  Popover,
  Select as AriaSelect,
  type SelectProps as AriaSelectProps,
  SelectValue,
} from "react-aria-components";
import { cx } from "./cx";
import { ChevronDownIcon } from "./icons";
import styles from "./select.module.css";

export type SelectItem = { id: string; label: string; lang?: string };

export type SelectProps = Omit<AriaSelectProps<SelectItem>, "className" | "children" | "value" | "onChange"> & {
  label: ReactNode;
  items: SelectItem[];
  value?: string | null;
  onChange?: (id: string) => void;
  /**
   * field: a form's label (13.5 px, 600). strip: the app strip's small
   * label, which a phone hides visually; it stays the control's name.
   */
  variant?: "field" | "strip";
  /** The value can be long (a name a person gave): cut it short with an ellipsis; the list and the control's name keep it whole. */
  clipValue?: boolean;
};

/** A single choice from a short list: a labelled button that opens a listbox. */
export function Select({ label, items, value, onChange, variant = "field", clipValue = false, ...props }: SelectProps) {
  const select = (
    <AriaSelect {...props} value={value} onChange={(key) => key !== null && onChange?.(String(key))} className={cx(styles.select, variant === "strip" && styles.strip)}>
      <Label className={styles.label}>{label}</Label>
      <AriaButton className={styles.trigger}>
        <SelectValue className={cx(styles.value, clipValue && styles.clip)} />
        <ChevronDownIcon />
      </AriaButton>
      <Popover className={styles.popover}>
        <ListBox items={items} className={styles.listbox}>
          {(item) => (
            <ListBoxItem id={item.id} textValue={item.label} className={styles.option}>
              <span lang={item.lang}>{item.label}</span>
            </ListBoxItem>
          )}
        </ListBox>
      </Popover>
    </AriaSelect>
  );
  return variant === "strip" ? <div onKeyDownCapture={onlyByChoice}>{select}</div> : select;
}

/**
 * In the app strip a change renders the whole page again (the language) or
 * recolours it (the theme), so it happens only when the reader chooses an
 * option in the open list (WCAG 3.2.2). On the closed control React Aria
 * would apply ArrowLeft, ArrowRight and type-ahead at once, as a native
 * select does; those keys stop here. ArrowUp, ArrowDown, Enter and Space
 * still open the list.
 */
function onlyByChoice(e: KeyboardEvent<HTMLDivElement>): void {
  const onTrigger = (e.target as Element).getAttribute("aria-haspopup") === "listbox";
  if (onTrigger && (e.key === "ArrowLeft" || e.key === "ArrowRight" || (e.key.length === 1 && e.key !== " "))) e.stopPropagation();
}
