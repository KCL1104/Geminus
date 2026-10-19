"use client";
import type { ReactNode } from "react";
import { FieldError, Input, Label, Text, TextField as AriaTextField, type TextFieldProps as AriaTextFieldProps } from "react-aria-components";
import styles from "./field.module.css";

export type TextFieldProps = Omit<AriaTextFieldProps, "className" | "validationBehavior" | "children"> & {
  label: ReactNode;
  description?: ReactNode;
  /** The server's message for this field (a ValidationIssue), shown when isInvalid. */
  errorMessage?: ReactNode;
};

/**
 * A labelled text input. Validation is the server's (ValidationIssue): the
 * field only shows it, and never runs the browser's own validation, whose
 * messages would come in the browser's language (validationBehavior "aria").
 */
export function TextField({ label, description, errorMessage, ...props }: TextFieldProps) {
  return (
    <AriaTextField {...props} validationBehavior="aria" className={styles.field}>
      <Label className={styles.label}>{label}</Label>
      <Input className={styles.input} />
      {description && (
        <Text slot="description" className={styles.description}>
          {description}
        </Text>
      )}
      <FieldError className={styles.error}>{errorMessage}</FieldError>
    </AriaTextField>
  );
}
