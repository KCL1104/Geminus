// @vitest-environment happy-dom
// H-073 review: the Marey chart's table gives each step's longest finished
// wait in minutes, and from two hours on with its hours. On a phone its rows
// are grids of two cells of about 150 px (results.module.css), where
// "391.49 分鐘（6.5 小時）" on one unbroken line ran under the next cell. The
// minutes stay one unbroken figure in their <data>; the hours follow outside
// it, whole, after a place where a narrow cell may break the line
// (results/figure.tsx). The trace here is the board line's with every time
// 30 times as long, so its waits run to hours; nothing else is changed.
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MareyChart } from "../features/results/marey-chart";
import { boardLine, boardResults, boardTraces } from "../features/results/fixtures/board-line";
import type { TraceArtifact } from "../lib/contracts";
import { I18nProvider } from "../lib/i18n/provider";
import type { Locale } from "../lib/i18n/locale";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { figureParts } from "./figure-parts";

const SLOWER = 30;
const slower = (t: TraceArtifact): TraceArtifact => ({ ...t, events: t.events.map((e) => ({ ...e, time_tick: e.time_tick * SLOWER })) });

function table(locale: Locale): Element {
  const end = boardResults.baseline.measurement_window.horizon_ticks * SLOWER;
  const host = document.createElement("div");
  host.innerHTML = renderToStaticMarkup(
    <I18nProvider locale={locale}>
      <MareyChart
        ir={boardLine.model}
        baseline={{ trace: { state: "loaded", trace: slower(boardTraces.baseline) }, endTick: end }}
        scenario={{ trace: { state: "loaded", trace: slower(boardTraces.scenario) }, endTick: end }}
      />
    </I18nProvider>,
  );
  const caption = (locale === "en" ? en : zhHant)["marey.table.caption"];
  const found = [...host.querySelectorAll("table")].find((t) => t.querySelector("caption")?.textContent === caption);
  if (!found) throw new Error("no trace table");
  return found;
}

describe("the Marey chart's table (H-073 review)", () => {
  it.each([
    ["en", " min", "space", /^\(\d+(\.\d)? h\)$/],
    ["zh-Hant", " 分鐘", "wbr", /^（\d+(\.\d)? 小時）$/],
  ] as const)("keeps a wait of two hours or more and its hours each whole, free to go on two lines (%s)", (locale, unit, gap, hours) => {
    const long = [...table(locale).querySelectorAll("td")].filter((td) => Number(td.querySelector("data")?.getAttribute("value") ?? 0) >= 120);
    expect(long.length).toBeGreaterThan(0);
    for (const td of long) {
      const p = figureParts(td);
      expect(p.data.endsWith(unit), p.data).toBe(true);
      expect(p.gap, p.data).toBe(gap);
      expect(p.after, p.data).toMatch(hours);
    }
  });
});
