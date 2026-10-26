"use client";
import type { ReactNode } from "react";
import { FieldError, Input, Label, Text, TextArea, TextField as AriaTextField, type TextFieldProps as AriaTextFieldProps } from "react-aria-components";
import { cx } from "./cx";
import styles from "./field.module.css";

export type TextFieldProps = Omit<AriaTextFieldProps, "className" | "validationBehavior" | "children"> & {
  label: ReactNode;
  description?: ReactNode;
  /** The server's message for this field (a ValidationIssue), shown when isInvalid. */
  errorMessage?: ReactNode;
  /** Several lines of text (a request in a person's own words), in a text area that grows with the window's width. */
  multiline?: boolean;
};

/**
 * A labelled text input. Validation is the server's (ValidationIssue): the
 * field only shows it, and never runs the browser's own validation, whose
 * messages would come in the browser's language (validationBehavior "aria").
 */
export function TextField({ label, description, errorMessage, multiline = false, ...props }: TextFieldProps) {
  return (
    <AriaTextField {...props} validationBehavior="aria" className={cx(styles.field, multiline && styles.wide)}>
      <Label className={styles.label}>{label}</Label>
      {multiline ? <TextArea className={cx(styles.input, styles.area)} rows={3} /> : <Input className={styles.input} />}
      {description && (
        <Text slot="description" className={styles.description}>
          {description}
        </Text>
      )}
      <FieldError className={styles.error}>{errorMessage}</FieldError>
    </AriaTextField>
  );
}
