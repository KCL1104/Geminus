"use client";
import { type ReactNode, useSyncExternalStore } from "react";
import {
  Cell,
  Column,
  Row,
  type Selection,
  type SortDescriptor,
  Table,
  TableBody,
  TableHeader,
  TableLayout,
  Virtualizer,
} from "react-aria-components";
import { useI18n } from "../lib/i18n/provider";
import { Checkbox } from "./checkbox";
import { cx } from "./cx";
import { SortIcon } from "./icons";
import styles from "./table.module.css";

export type TableColumn = { id: string; name: string; isRowHeader?: boolean; allowsSorting?: boolean };

/** Above this many rows a table is virtualized and rendered in the browser only (ADR-021). */
export const virtualizeAbove = 50;
const rowHeight = 32;
const virtualHeight = 480;

const noSubscription = () => () => {};
/** False while rendering on the server and hydrating, true after. */
function useIsClient(): boolean {
  return useSyncExternalStore(noSubscription, () => true, () => false);
}

/**
 * A data grid: React Aria's table with its keyboard model, sorting and
 * selection. Long tables are virtualized (only the visible rows are in the
 * DOM; aria-rowcount gives the total), and are rendered only after
 * hydration: React Aria 1.21 builds a large collection on the server in
 * quadratic time (ADR-021). Sorting is the caller's: sortDescriptor shows
 * it, onSortChange asks for another.
 */
export function DataTable<T extends { id: string }>({ label, columns, rows, renderCell, rowText, empty, sortDescriptor, onSortChange, selectionMode = "none", selectedKeys, onSelectionChange }: {
  label: string;
  columns: TableColumn[];
  rows: T[];
  renderCell: (row: T, column: string) => ReactNode;
  /** The row as text: what type-ahead matches and what selection announces ("… selected"). */
  rowText: (row: T) => string;
  empty: ReactNode;
  sortDescriptor?: SortDescriptor;
  onSortChange?: (d: SortDescriptor) => void;
  selectionMode?: "none" | "multiple";
  selectedKeys?: Selection;
  onSelectionChange?: (keys: Selection) => void;
}) {
  const { t } = useI18n();
  const isClient = useIsClient();
  const virtual = rows.length > virtualizeAbove;
  if (virtual && !isClient) return <div className={styles.pending} style={{ height: virtualHeight }} data-pending="" />;
  const selectable = selectionMode === "multiple";
  const table = (
    <Table
      aria-label={label}
      sortDescriptor={sortDescriptor}
      onSortChange={onSortChange}
      selectionMode={selectionMode}
      selectedKeys={selectedKeys}
      onSelectionChange={onSelectionChange}
      className={cx(styles.table, virtual && styles.virtual)}
      style={virtual ? { height: virtualHeight } : undefined}
    >
      <TableHeader className={styles.header}>
        {selectable && (
          <Column className={styles.selectCell}>
            <Checkbox slot="selection" aria-label={t("table.selectAll")} />
          </Column>
        )}
        {columns.map((c) => (
          <Column key={c.id} id={c.id} isRowHeader={c.isRowHeader} allowsSorting={c.allowsSorting} textValue={c.name} className={styles.column}>
            {({ sortDirection }) => (
              <span className={styles.columnInner}>
                {c.name}
                {c.allowsSorting && <SortIcon direction={sortDirection} />}
              </span>
            )}
          </Column>
        ))}
      </TableHeader>
      <TableBody items={rows} renderEmptyState={() => <p className={styles.empty}>{empty}</p>}>
        {(row) => (
          <Row id={row.id} textValue={rowText(row)} className={styles.row}>
            {selectable && (
              <Cell className={styles.selectCell}>
                <Checkbox slot="selection" aria-label={t("table.selectRow")} />
              </Cell>
            )}
            {columns.map((c) => (
              <Cell key={c.id} className={styles.cell}>
                {renderCell(row, c.id)}
              </Cell>
            ))}
          </Row>
        )}
      </TableBody>
    </Table>
  );
  return (
    <div className={styles.scroll} data-virtualized={virtual ? "" : undefined}>
      {virtual ? (
        <Virtualizer layout={TableLayout} layoutOptions={{ rowHeight, headingHeight: rowHeight }}>
          {table}
        </Virtualizer>
      ) : (
        table
      )}
    </div>
  );
}
