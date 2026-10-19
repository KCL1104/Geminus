"use client";
import { Button as AriaButton, type ButtonProps as AriaButtonProps } from "react-aria-components";
import styles from "./button.module.css";
import { cx } from "./cx";

export type ButtonProps = Omit<AriaButtonProps, "className"> & {
  /** primary: the page's main action; danger: destroys or discards; quiet: low emphasis. */
  variant?: "primary" | "secondary" | "danger" | "quiet";
  className?: string;
};

/** React Aria's button (type="button" unless set): press, keyboard and focus behaviour included. */
export function Button({ variant = "secondary", className, ...props }: ButtonProps) {
  return <AriaButton {...props} data-variant={variant} className={cx(styles.button, className)} />;
}
