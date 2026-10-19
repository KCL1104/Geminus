// @vitest-environment happy-dom
// T-045 / REQ-023 / TEST-033: the base components. Each is accessible by
// role and name, keeps React Aria's keyboard and focus behaviour, and has no
// axe violation (WCAG 2.0–2.2 A/AA, contrast excluded: tests/tokens.test.ts).
import { useState } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../lib/i18n/provider";
import { UiProvider } from "../ui/provider";
import { Button } from "../ui/button";
import { Callout } from "../ui/callout";
import { Checkbox } from "../ui/checkbox";
import { ConfirmDialog } from "../ui/dialog";
import { TextField } from "../ui/field";
import { Select } from "../ui/select";
import { StatusRegion } from "../ui/status";
import { DataTable } from "../ui/table";
import { key, mount, press, rerender, settle, violations } from "./ui-harness";

vi.setConfig({ testTimeout: 120_000 });

describe("the axe check", () => {
  it("finds what it should: a button without a name", async () => {
    await mount(<button type="button" />);
    expect((await violations()).join()).toMatch(/^button-name/);
  });
});

describe("Button", () => {
  it("is a button that does not submit forms, with a variant", async () => {
    const onPress = vi.fn();
    const el = await mount(<Button variant="primary" onPress={onPress}>Save</Button>);
    const b = el.querySelector("button")!;
    expect(b.getAttribute("type")).toBe("button");
    expect(b.dataset.variant).toBe("primary");
    await press(b);
    expect(onPress).toHaveBeenCalledTimes(1);
    expect(await violations()).toEqual([]);
  });

  it("is not pressable while disabled", async () => {
    const onPress = vi.fn();
    const el = await mount(<Button isDisabled onPress={onPress}>Save</Button>);
    await press(el.querySelector("button"));
    expect(onPress).not.toHaveBeenCalled();
    expect(el.querySelector("button")!.disabled).toBe(true);
  });
});

describe("Callout", () => {
  it("is an alert for danger and a note otherwise, with its heading", async () => {
    const el = await mount(
      <>
        <Callout tone="danger" heading="Failed">body</Callout>
        <Callout tone="info">note</Callout>
        <Callout tone="warning" role="status">soon</Callout>
      </>,
    );
    const [danger, info, warning] = [...el.querySelectorAll("[data-tone]")];
    expect(danger!.getAttribute("role")).toBe("alert");
    expect(danger!.querySelector("h2")?.textContent).toBe("Failed");
    expect(info!.getAttribute("role")).toBe("note");
    expect(warning!.getAttribute("role")).toBe("status");
    expect(await violations()).toEqual([]);
  });
});

describe("StatusRegion", () => {
  it("exists before it has anything to say, so a later message is announced", async () => {
    const el = await mount(<StatusRegion message={null} />);
    const region = el.querySelector("[role=status]")!;
    expect(region.textContent).toBe("");
    await rerender(<StatusRegion message="Saved" />);
    expect(el.querySelector("[role=status]")).toBe(region);
    expect(region.textContent).toBe("Saved");
    expect(await violations()).toEqual([]);
  });
});

describe("TextField", () => {
  it("labels the input, describes it and reports the server's error", async () => {
    const el = await mount(<TextField label="Name" description="Shown to everyone" isInvalid errorMessage="Required" isRequired />);
    const input = el.querySelector("input")!;
    const label = el.querySelector("label")!;
    expect(label.htmlFor).toBe(input.id);
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(input.required || input.getAttribute("aria-required") === "true").toBe(true);
    const described = (input.getAttribute("aria-describedby") ?? "").split(" ").map((id) => document.getElementById(id)?.textContent);
    expect(described).toEqual(expect.arrayContaining(["Shown to everyone", "Required"]));
    expect(await violations()).toEqual([]);
  });

  it("does not validate in the browser's language", async () => {
    const el = await mount(<TextField label="Name" isRequired />);
    expect(el.querySelector("input")!.required).toBe(false);
  });
});

describe("Checkbox", () => {
  it("is a labelled checkbox that toggles", async () => {
    const onChange = vi.fn();
    const el = await mount(<Checkbox onChange={onChange}>Include archived</Checkbox>);
    const input = el.querySelector("input[type=checkbox]")!;
    expect(el.querySelector("label")?.textContent).toContain("Include archived");
    await press(el.querySelector("label"));
    expect(onChange).toHaveBeenCalledWith(true);
    expect(input).not.toBeNull();
    expect(await violations()).toEqual([]);
  });
});

describe("Select", () => {
  it("is a labelled button that opens a listbox and reports the choice", async () => {
    const onChange = vi.fn();
    const el = await mount(
      <Select label="Language" placeholder="Choose" value="en" onChange={onChange}
        items={[{ id: "en", label: "English" }, { id: "zh-Hant", label: "繁體中文", lang: "zh-Hant" }]} />,
    );
    const button = el.querySelector("button")!;
    expect(button.textContent).toContain("English");
    expect(await violations()).toEqual([]);
    await press(button);
    await settle();
    const options = [...document.querySelectorAll("[role=option]")];
    expect(options.map((o) => o.textContent)).toEqual(["English", "繁體中文"]);
    expect(options[1]!.querySelector("[lang]")?.getAttribute("lang")).toBe("zh-Hant");
    await press(options[1]);
    await settle();
    expect(onChange).toHaveBeenCalledWith("zh-Hant");
  });
});

describe("ConfirmDialog", () => {
  function Harness({ onConfirm }: { onConfirm: () => void }) {
    const [open, setOpen] = useState(true);
    return (
      <ConfirmDialog isOpen={open} onOpenChange={setOpen} heading="Discard changes?" confirmLabel="Discard" cancelLabel="Keep" tone="danger" onConfirm={onConfirm}>
        Your 3 changes are lost.
      </ConfirmDialog>
    );
  }

  it("is an alert dialog, labelled and described, with focus inside and Cancel first", async () => {
    const onConfirm = vi.fn();
    await mount(<Harness onConfirm={onConfirm} />);
    await settle();
    const dialog = document.querySelector("[role=alertdialog]")!;
    expect(dialog).not.toBeNull();
    expect(document.getElementById(dialog.getAttribute("aria-labelledby")!)?.textContent).toBe("Discard changes?");
    expect(document.getElementById(dialog.getAttribute("aria-describedby")!)?.textContent).toBe("Your 3 changes are lost.");
    const buttons = [...dialog.querySelectorAll("button")];
    expect(buttons.map((b) => b.textContent)).toEqual(["Keep", "Discard"]);
    expect(dialog.contains(document.activeElement)).toBe(true);
    expect(await violations()).toEqual([]);
  });

  it("confirms with its button and closes; Escape closes without confirming", async () => {
    const onConfirm = vi.fn();
    await mount(<Harness onConfirm={onConfirm} />);
    await settle();
    await key(document.querySelector("[role=alertdialog]"), "Escape");
    await settle();
    expect(document.querySelector("[role=alertdialog]")).toBeNull();
    expect(onConfirm).not.toHaveBeenCalled();
    await mount(<Harness onConfirm={onConfirm} />);
    await settle();
    await press([...document.querySelectorAll("[role=alertdialog] button")].find((b) => b.textContent === "Discard"));
    await settle();
    expect(onConfirm).toHaveBeenCalledTimes(1);
    expect(document.querySelector("[role=alertdialog]")).toBeNull();
  });
});

type Row = { id: string; name: string; count: number };
const rows = (n: number): Row[] => Array.from({ length: n }, (_, i) => ({ id: `r${i}`, name: `Row ${i}`, count: i }));
const columns = [
  { id: "name", name: "Name", isRowHeader: true, allowsSorting: true },
  { id: "count", name: "Count" },
] as const;

describe("DataTable", () => {
  it("is a grid with column headers, a row header and sort state", async () => {
    const el = await mount(
      <DataTable label="Issues" columns={[...columns]} rows={rows(3)} sortDescriptor={{ column: "name", direction: "ascending" }}
        renderCell={(r, c) => (c === "name" ? r.name : String(r.count))} empty="Nothing here" />,
    );
    await settle();
    const grid = el.querySelector("[role=grid]")!;
    expect(grid.getAttribute("aria-label")).toBe("Issues");
    expect([...grid.querySelectorAll("[role=columnheader]")].map((h) => h.textContent)).toEqual(["Name", "Count"]);
    expect(grid.querySelector("[role=columnheader]")!.getAttribute("aria-sort")).toBe("ascending");
    expect(grid.querySelectorAll("[role=rowheader]").length).toBe(3);
    expect(await violations()).toEqual([]);
  });

  it("says when it is empty", async () => {
    const el = await mount(<DataTable label="Issues" columns={[...columns]} rows={[]} renderCell={() => ""} empty="Nothing here" />);
    await settle();
    expect(el.textContent).toContain("Nothing here");
    expect(await violations()).toEqual([]);
  });

  it("virtualizes a long table and renders it only in the browser", async () => {
    const el = await mount(<DataTable label="Issues" columns={[...columns]} rows={rows(500)} renderCell={(r, c) => (c === "name" ? r.name : String(r.count))} empty="" />);
    await settle();
    const grid = el.querySelector("[role=grid]")!;
    expect(grid.getAttribute("aria-rowcount")).toBe("501");
    expect(grid.querySelectorAll("[role=row]").length).toBeLessThan(100);
    expect(el.querySelector("[data-virtualized]")).not.toBeNull();
  });

  it("renders a long table on the server as a placeholder only", () => {
    const html = renderToStaticMarkup(
      <I18nProvider locale="en">
        <UiProvider>
          <DataTable label="Issues" columns={[...columns]} rows={rows(500)} renderCell={(r) => r.name} empty="" />
          <DataTable label="Short" columns={[...columns]} rows={rows(3)} renderCell={(r) => r.name} empty="" />
        </UiProvider>
      </I18nProvider>,
    );
    expect(html).toContain("data-pending");
    expect(html).not.toContain("Row 499");
    expect(html).toContain("Row 2");
  });
});
