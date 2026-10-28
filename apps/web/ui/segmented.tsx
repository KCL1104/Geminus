"use client";
// One form for every one-of-n choice (DESIGN.md Segmented Controls): a
// Control Edge outline with 6 px corners, Raised items divided by Control
// Edge, the selected one in Graphite Ink with a 600 Reverse Ink label. React
// Aria's ToggleButtonGroup gives it the role, the arrow keys and the name;
// exactly one item is always selected.
import { ToggleButton, ToggleButtonGroup } from "react-aria-components";
import styles from "./segmented.module.css";

export function Segmented<T extends string>({ label, items, value, onChange }: {
  label: string;
  items: readonly { id: T; label: string }[];
  value: T;
  onChange: (value: T) => void;
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
        <ToggleButton key={item.id} id={item.id} className={styles.item}>
          {item.label}
        </ToggleButton>
      ))}
    </ToggleButtonGroup>
  );
}
