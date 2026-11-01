// The electronics line's engine output for the review pages (H-072), read
// from its files at request time and handed to the page as data, never
// bundled (features/results/fixtures/electronics-line-objects.ts says why).
// Server only: the gallery pages call it before they render.
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { ELECTRONICS_LINE_DIR, type ElectronicsLineData, electronicsLineFiles } from "../../features/results/fixtures/electronics-line-objects";

export async function readElectronicsLine(): Promise<ElectronicsLineData> {
  const read = async <T,>(name: string): Promise<T> => JSON.parse(await readFile(join(process.cwd(), ELECTRONICS_LINE_DIR, name), "utf8")) as T;
  const f = electronicsLineFiles;
  const [model, baselineResult, scenarioResult, baselineTrace, scenarioTrace, comparison] = await Promise.all([
    read<ElectronicsLineData["model"]>(f.model),
    read<ElectronicsLineData["results"]["baseline"]>(f.baselineResult),
    read<ElectronicsLineData["results"]["scenario"]>(f.scenarioResult),
    read<ElectronicsLineData["traces"]["baseline"]>(f.baselineTrace),
    read<ElectronicsLineData["traces"]["scenario"]>(f.scenarioTrace),
    read<ElectronicsLineData["comparison"]>(f.comparison),
  ]);
  return { model, results: { baseline: baselineResult, scenario: scenarioResult }, traces: { baseline: baselineTrace, scenario: scenarioTrace }, comparison };
}
