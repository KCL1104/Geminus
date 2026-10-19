"use client";
import type { ReactNode } from "react";
import { Checkbox as AriaCheckbox, type CheckboxProps as AriaCheckboxProps } from "react-aria-components";
import styles from "./checkbox.module.css";
import { CheckIcon, DashIcon } from "./icons";

export type CheckboxProps = Omit<AriaCheckboxProps, "className" | "children"> & { children?: ReactNode };

/** React Aria's checkbox: a label around a hidden native input, with our box. */
export function Checkbox({ children, ...props }: CheckboxProps) {
  return (
    <AriaCheckbox {...props} className={styles.checkbox}>
      {({ isSelected, isIndeterminate }) => (
        <>
          <span className={styles.box}>{isIndeterminate ? <DashIcon /> : isSelected ? <CheckIcon /> : null}</span>
          {children}
        </>
      )}
    </AriaCheckbox>
  );
}
