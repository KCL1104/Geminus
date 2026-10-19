"use client";
import type { ReactNode } from "react";
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
import { ChevronDownIcon } from "./icons";
import styles from "./select.module.css";

export type SelectItem = { id: string; label: string; lang?: string };

export type SelectProps = Omit<AriaSelectProps<SelectItem>, "className" | "children" | "value" | "onChange"> & {
  label: ReactNode;
  items: SelectItem[];
  value?: string | null;
  onChange?: (id: string) => void;
};

/** A single choice from a short list: a labelled button that opens a listbox. */
export function Select({ label, items, value, onChange, ...props }: SelectProps) {
  return (
    <AriaSelect {...props} value={value} onChange={(key) => key !== null && onChange?.(String(key))} className={styles.select}>
      <Label className={styles.label}>{label}</Label>
      <AriaButton className={styles.trigger}>
        <SelectValue className={styles.value} />
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
}
