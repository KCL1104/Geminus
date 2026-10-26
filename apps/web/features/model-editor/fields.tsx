"use client";
// The properties panel's fields. A field shows the IR's value and the
// server's issues at its JSON Pointer (and, when the typed text is not a
// number at all, its own one-line reason: a JSON number is needed before
// anything can be sent); it commits on Enter or when focus leaves, never
// per key press. Every range and rule is the server's to check.
import { type ReactNode, useState } from "react";
import { validationKey } from "../../app/_components/problem-view";
import { useI18n } from "../../lib/i18n/provider";
import { TextField } from "../../ui/field";
import type { StepIssue } from "./issues";

/** The issues' messages: the catalog's for a known code, the server's (English) otherwise. */
export function IssueText({ issues }: { issues: readonly StepIssue[] }) {
  const { t, gap } = useI18n();
  return (
    <>
      {issues.map((i, n) => {
        const key = validationKey(i.code);
        return (
          <span key={n}>
            {n > 0 && gap}
            {key ? t(key) : <span lang="en">{i.message}</span>}
          </span>
        );
      })}
    </>
  );
}

const show = (v: number | undefined) => (v === undefined ? "" : String(v));

/** Keeps the typed text until the IR's value changes under it. */
function useText(value: string) {
  const [text, setText] = useState(value);
  const [shown, setShown] = useState(value);
  if (shown !== value) {
    setShown(value);
    setText(value);
  }
  return [text, setText] as const;
}

export function NumberField({ label, value, whole = false, optional = false, description, issues, readOnly, onCommit }: {
  label: string;
  value: number | undefined;
  whole?: boolean;
  /** Empty is a value: the member is removed (no limit). */
  optional?: boolean;
  description?: ReactNode;
  issues: readonly StepIssue[];
  readOnly: boolean;
  onCommit: (value: number | null) => void;
}) {
  const { t } = useI18n();
  const [text, setText] = useText(show(value));
  const [local, setLocal] = useState<string | null>(null);
  const commit = () => {
    const trimmed = text.trim();
    if (trimmed === show(value)) {
      setLocal(null);
      return;
    }
    if (trimmed === "" && optional) {
      setLocal(null);
      onCommit(null);
      return;
    }
    const n = Number(trimmed);
    if (trimmed === "" || !Number.isFinite(n) || (whole && !Number.isInteger(n))) {
      setLocal(t(whole ? "editor.field.wholeNumber" : "editor.field.number"));
      return;
    }
    setLocal(null);
    onCommit(n);
  };
  const invalid = local !== null || issues.length > 0;
  return (
    <TextField
      label={label}
      value={text}
      onChange={setText}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") commit();
      }}
      inputMode={whole ? "numeric" : "decimal"}
      isReadOnly={readOnly}
      isInvalid={invalid}
      description={description}
      errorMessage={
        <>
          {local}
          {local !== null && issues.length > 0 && " "}
          <IssueText issues={issues} />
        </>
      }
    />
  );
}

export function NameField({ value, issues, readOnly, autoFocus, onCommit }: {
  value: string;
  issues: readonly StepIssue[];
  readOnly: boolean;
  autoFocus: boolean;
  onCommit: (name: string) => void;
}) {
  const { t } = useI18n();
  const [text, setText] = useText(value);
  const commit = () => {
    if (text.trim() !== value) onCommit(text);
  };
  return (
    <TextField
      label={t("editor.field.name")}
      value={text}
      onChange={setText}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") commit();
      }}
      maxLength={200}
      isReadOnly={readOnly}
      autoFocus={autoFocus}
      isInvalid={issues.length > 0}
      description={t("editor.field.nameHint")}
      errorMessage={<IssueText issues={issues} />}
    />
  );
}
