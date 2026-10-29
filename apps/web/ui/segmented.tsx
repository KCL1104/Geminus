"use client";
// One form for every one-of-n choice (DESIGN.md Segmented Controls, H-041):
// a well with the selected item raised on it, its label at 600. React
// Aria's ToggleButtonGroup gives it the role, the arrow keys and the name;
// exactly one item is always selected.
import { ToggleButton, ToggleButtonGroup } from "react-aria-components";
import styles from "./segmented.module.css";

export function Segmented<T extends string>({ label, items, value, onChange, autoFocus = false }: {
  label: string;
  items: readonly { id: T; label: string; lang?: string }[];
  value: T;
  onChange: (value: T) => void;
  /** Focus the selected item when it mounts (the first control of a menu). */
  autoFocus?: boolean;
}) {
  return (
    <ToggleButtonGroup
      aria-label={label}
      selectionMode="single"
      disallowEmptySelection
      selectedKeys={[value]}
      onSelectionChange={(keys) => {
        const next = [...keys][0];
        if (next !== undefined && next !== value) onChange(next as T);
      }}
      className={styles.group}
    >
      {items.map((item) => (
        <ToggleButton key={item.id} id={item.id} lang={item.lang} autoFocus={autoFocus && item.id === value} className={styles.item}>
          {item.label}
        </ToggleButton>
      ))}
    </ToggleButtonGroup>
  );
}
