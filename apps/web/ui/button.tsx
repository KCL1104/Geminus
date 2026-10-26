"use client";
import type { Ref } from "react";
import { Button as AriaButton, type ButtonProps as AriaButtonProps } from "react-aria-components";
import styles from "./button.module.css";
import { cx } from "./cx";

export type ButtonProps = Omit<AriaButtonProps, "className"> & {
  /**
   * primary: the page's main action; danger: destroys or discards; quiet:
   * low emphasis; run: runs a scenario, and nothing else (DESIGN.md Buttons,
   * Deep Scenario Green: green means scenario).
   */
  variant?: "primary" | "secondary" | "danger" | "quiet" | "run";
  /**
   * A blocked action (DESIGN.md Buttons, Disabled): it looks disabled and
   * does nothing when pressed, but stays focusable as aria-disabled, so
   * focus is not lost when it blocks; aria-describedby names the reason on
   * screen beside it.
   */
  blocked?: boolean;
  className?: string;
  ref?: Ref<HTMLButtonElement>;
};

/** React Aria's button (type="button" unless set): press, keyboard and focus behaviour included. */
export function Button({ variant = "secondary", className, blocked = false, onPress, ...props }: ButtonProps) {
  return (
    <AriaButton
      {...props}
      onPress={blocked ? undefined : onPress}
      aria-disabled={blocked ? "true" : undefined}
      data-blocked={blocked || undefined}
      data-variant={variant}
      className={cx(styles.button, className)}
    />
  );
}
