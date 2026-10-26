"use client";
import { Menu, MenuItem, MenuTrigger, Popover } from "react-aria-components";
import { Button } from "./button";
import { ChevronDownIcon } from "./icons";
// The menu floats like the Select's list and looks like it (DESIGN.md Overlays).
import styles from "./select.module.css";

export type MenuEntry = { id: string; label: string };

/**
 * A button that opens a list of actions (DESIGN.md Overlays, menu): the
 * arrow keys move, Enter or a click acts, Escape closes and focus returns to
 * the button. React Aria names the list by its button.
 */
export function ActionMenu({ label, items, onAction }: { label: string; items: MenuEntry[]; onAction: (id: string) => void }) {
  return (
    <MenuTrigger>
      <Button>
        {label}
        <ChevronDownIcon />
      </Button>
      <Popover className={styles.popover} placement="bottom start">
        <Menu items={items} onAction={(key) => onAction(String(key))} className={styles.listbox}>
          {(item) => (
            <MenuItem id={item.id} textValue={item.label} className={styles.option}>
              {item.label}
            </MenuItem>
          )}
        </Menu>
      </Popover>
    </MenuTrigger>
  );
}
