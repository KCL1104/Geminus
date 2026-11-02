"use client";
import type { ReactNode } from "react";
import { Checkbox as AriaCheckbox, type CheckboxProps as AriaCheckboxProps } from "react-aria-components";
import styles from "./checkbox.module.css";
import { CheckIcon, DashIcon } from "./icons";

export type CheckboxProps = Omit<AriaCheckboxProps, "className" | "children"> & {
  children?: ReactNode;
  /** chip: a capsule in a composer's tool row (Ask AI, 2026-11-02), its box small inside it; ticked, it takes the accent wash. */
  variant?: "box" | "chip";
};

/** React Aria's checkbox: a label around a hidden native input, with our box. */
export function Checkbox({ children, variant = "box", ...props }: CheckboxProps) {
  return (
    <AriaCheckbox {...props} className={variant === "chip" ? `${styles.checkbox} ${styles.chip}` : styles.checkbox}>
      {({ isSelected, isIndeterminate }) => (
        <>
          <span className={styles.box}>{isIndeterminate ? <DashIcon /> : isSelected ? <CheckIcon /> : null}</span>
          {children}
        </>
      )}
    </AriaCheckbox>
  );
}
