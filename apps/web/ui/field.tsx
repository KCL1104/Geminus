"use client";
import type { ReactNode, Ref } from "react";
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
  /** The text area's height in lines (multiline only). */
  rows?: number;
  /**
   * A second sentence after the description, on the same line, with the
   * caller's ID so other controls it applies to can name it too; the field
   * names it in its aria-describedby, after the description.
   */
  notice?: { id: string; text: ReactNode };
  /** The single-line input itself, for a form that moves the focus to it (a refused submit). */
  inputRef?: Ref<HTMLInputElement>;
};

/**
 * A labelled text input. Validation is the server's (ValidationIssue): the
 * field only shows it, and never runs the browser's own validation, whose
 * messages would come in the browser's language (validationBehavior "aria").
 */
export function TextField({ label, description, errorMessage, multiline = false, rows = 3, notice, inputRef, ...props }: TextFieldProps) {
  const describedBy = [props["aria-describedby"], notice?.id].filter(Boolean).join(" ") || undefined;
  return (
    <AriaTextField {...props} aria-describedby={describedBy} validationBehavior="aria" className={cx(styles.field, multiline && styles.wide)}>
      <Label className={styles.label}>{label}</Label>
      {multiline ? <TextArea className={cx(styles.input, styles.area)} rows={rows} /> : <Input ref={inputRef} className={styles.input} />}
      {(description || notice) && (
        <p className={styles.description}>
          {description && <Text slot="description">{description}</Text>}
          {description && notice && " "}
          {notice && <span id={notice.id}>{notice.text}</span>}
        </p>
      )}
      <FieldError className={styles.error}>{errorMessage}</FieldError>
    </AriaTextField>
  );
}
