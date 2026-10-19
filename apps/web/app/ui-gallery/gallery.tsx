"use client";
// Every shared component in each state (T-045, REQ-023): what the owner
// reviews and what the browser checks (apps/web-e2e) run axe, keyboard and
// screenshot checks on. The data is made up; the text is the catalog's.
import { type ReactNode, useMemo, useState } from "react";
import type { SortDescriptor } from "react-aria-components";
import type { Problem } from "../../lib/api/problem";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { Callout } from "../../ui/callout";
import { Checkbox } from "../../ui/checkbox";
import { ConfirmDialog } from "../../ui/dialog";
import { TextField } from "../../ui/field";
import list from "../../ui/list.module.css";
import { Select } from "../../ui/select";
import { StatusRegion } from "../../ui/status";
import { DataTable } from "../../ui/table";
import { ProblemView } from "../_components/problem-view";
import { Loading, ReadOnlyNotice, RemoteView } from "../_components/states";
import styles from "./gallery.module.css";

type Issue = { id: string; path: string; code: string; severity: "error" | "warning" };

const issues: Issue[] = [
  { id: "i1", path: "/nodes/review/capacity", code: "OUT_OF_RANGE", severity: "error" },
  { id: "i2", path: "/flows/f3/probability", code: "PROBABILITY_SUM", severity: "error" },
  { id: "i3", path: "/nodes/deploy/duration", code: "DISTRIBUTION_INVALID", severity: "warning" },
  { id: "i4", path: "/nodes/qa", code: "UNREACHABLE_NODE", severity: "warning" },
  { id: "i5", path: "/resources/reviewers", code: "RESOURCE_REQUIREMENT_INVALID", severity: "error" },
];
const longIssues: Issue[] = Array.from({ length: 500 }, (_, i) => ({ id: `l${i}`, path: `/nodes/n${i}/capacity`, code: "OUT_OF_RANGE", severity: i % 3 ? "warning" : "error" }));

const problem = (p: Partial<Problem> & Pick<Problem, "kind">): Problem => ({ code: null, status: null, message: "", requestId: null, details: [], retryAfterSeconds: null, ...p });
const forbidden = problem({ kind: "forbidden", code: "FORBIDDEN", status: 403, requestId: "req_01M4YDQK00TA00000000000001" });
const conflict = problem({ kind: "conflict", code: "MODEL_VERSION_CONFLICT", status: 409, message: "draft revision 7 is not the current revision 8", requestId: "req_01M4YDQK00TA00000000000002" });
const invalid = problem({ kind: "invalid", code: "REQUEST_VALIDATION_FAILED", status: 422, details: [{ path: "/nodes/review/capacity", code: "OUT_OF_RANGE", reason: "must be at most 1000" }] });
const network = problem({ kind: "network" });

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className={styles.section}>
      <h2>{title}</h2>
      {children}
    </section>
  );
}

export function Gallery() {
  const { t } = useI18n();
  const [status, setStatus] = useState<string | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [unit, setUnit] = useState("minute");
  const [sort, setSort] = useState<SortDescriptor>({ column: "path", direction: "ascending" });
  const sorted = useMemo(() => {
    const key = sort.column as keyof Issue;
    const out = [...issues].sort((a, b) => String(a[key]).localeCompare(String(b[key])));
    return sort.direction === "descending" ? out.reverse() : out;
  }, [sort]);
  const columns = [
    { id: "path", name: t("gallery.table.path"), isRowHeader: true, allowsSorting: true },
    { id: "code", name: t("gallery.table.code"), allowsSorting: true },
    { id: "severity", name: t("gallery.table.severity"), allowsSorting: true },
  ];
  const cell = (row: Issue, column: string) =>
    column === "severity" ? t(row.severity === "error" ? "gallery.table.error" : "gallery.table.warning") : <code>{row[column as "path" | "code"]}</code>;

  return (
    <>
      <h1>{t("gallery.title")}</h1>
      <p className={styles.intro}>{t("gallery.intro")}</p>
      <StatusRegion message={status} />

      <Section title={t("gallery.buttons")}>
        <div className={styles.row}>
          <Button variant="primary">{t("gallery.button.primary")}</Button>
          <Button>{t("gallery.button.secondary")}</Button>
          <Button variant="danger">{t("gallery.button.danger")}</Button>
          <Button variant="quiet">{t("gallery.button.quiet")}</Button>
          <Button variant="primary" isDisabled>
            {t("gallery.button.disabled")}
          </Button>
        </div>
      </Section>

      <Section title={t("gallery.fields")}>
        <TextField label={t("gallery.field.name")} description={t("gallery.field.nameHint")} defaultValue="Delivery pipeline" isRequired />
        <TextField label={t("gallery.field.capacity")} defaultValue="1200" inputMode="numeric" isInvalid errorMessage={t("gallery.field.capacityError")} />
        <div className={styles.row}>
          <Select
            label={t("gallery.field.unit")}
            value={unit}
            onChange={setUnit}
            items={["minute", "hour", "day"].map((u) => ({ id: u, label: t(`gallery.unit.${u}` as "gallery.unit.minute") }))}
          />
          <Checkbox>{t("gallery.field.archived")}</Checkbox>
        </div>
      </Section>

      <Section title={t("gallery.messages")}>
        <Callout tone="info">{t("gallery.callout.info")}</Callout>
        <Callout tone="warning">{t("gallery.callout.warning")}</Callout>
        <Callout tone="success">{t("gallery.callout.success")}</Callout>
        <Callout tone="danger" heading={t("gallery.callout.dangerHeading")} headingLevel={3}>
          {t("gallery.callout.danger")}
        </Callout>
      </Section>

      <Section title={t("gallery.status")}>
        <Button variant="primary" onPress={() => setStatus(t("gallery.status.saved"))}>
          {t("gallery.status.action")}
        </Button>
      </Section>

      <Section title={t("gallery.dialog")}>
        <Button variant="danger" onPress={() => setDialogOpen(true)}>
          {t("gallery.dialog.open")}
        </Button>
        <ConfirmDialog
          isOpen={dialogOpen}
          onOpenChange={setDialogOpen}
          heading={t("gallery.dialog.heading")}
          confirmLabel={t("gallery.dialog.confirm")}
          cancelLabel={t("gallery.dialog.cancel")}
          onConfirm={() => setStatus(t("gallery.dialog.done"))}
        >
          {t("gallery.dialog.body")}
        </ConfirmDialog>
      </Section>

      <Section title={t("gallery.table")}>
        <DataTable label={t("gallery.table.label")} columns={columns} rows={sorted} renderCell={cell} rowText={(r) => r.path} empty={t("gallery.table.empty")} sortDescriptor={sort} onSortChange={setSort} selectionMode="multiple" />
        <DataTable label={t("gallery.table.emptyLabel")} columns={columns} rows={[]} renderCell={cell} rowText={(r) => r.path} empty={t("gallery.table.empty")} />
        <DataTable label={t("gallery.table.longLabel")} columns={columns} rows={longIssues} renderCell={cell} rowText={(r) => r.path} empty={t("gallery.table.empty")} />
      </Section>

      <Section title={t("gallery.states")}>
        <Loading what="projects" />
        <p className={list.empty}>{t("projects.empty")}</p>
        <ReadOnlyNotice role="viewer" />
        <ProblemView problem={forbidden} headingLevel={3} />
        <ProblemView problem={conflict} headingLevel={3} />
        <ProblemView problem={invalid} headingLevel={3} />
        <RemoteView remote={{ state: "failed", problem: network }} what="projects" onRetry={() => {}} headingLevel={3}>
          {() => null}
        </RemoteView>
      </Section>
    </>
  );
}
