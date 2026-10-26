import { readFile } from "node:fs/promises";
import { join } from "node:path";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { type BoardLineData, boardLineFiles } from "../../../features/results/fixtures/board-line-objects";
import { galleryEnabled } from "../../../lib/gallery";
import { requestLocale } from "../../../lib/i18n/server";
import { createTranslator } from "../../../lib/i18n/translate";
import { type PreviewState, ResultsPreview } from "./preview";

// The results view for review (T-032), off unless GEMINUS_UI_GALLERY=1,
// like the component gallery. One state per address: ?state=running,
// failed or incomparable; the results otherwise. The engine output is read
// from its files at request time and handed over as data: bundled as a
// module, its numbers could be rewritten by the minifier
// (features/results/fixtures/board-line-objects.ts).
export const dynamic = "force-dynamic";

const states: readonly PreviewState[] = ["results", "incomparable", "running", "failed"];

async function boardLine(): Promise<BoardLineData> {
  const read = async <T,>(name: string): Promise<T> => JSON.parse(await readFile(join(process.cwd(), "features/results/fixtures", name), "utf8")) as T;
  const [model, baselineResult, scenarioResult, baselineTrace, scenarioTrace, comparison] = await Promise.all([
    read<BoardLineData["model"]>(boardLineFiles.model),
    read<BoardLineData["results"]["baseline"]>(boardLineFiles.baselineResult),
    read<BoardLineData["results"]["scenario"]>(boardLineFiles.scenarioResult),
    read<BoardLineData["traces"]["baseline"]>(boardLineFiles.baselineTrace),
    read<BoardLineData["traces"]["scenario"]>(boardLineFiles.scenarioTrace),
    read<BoardLineData["comparison"]>(boardLineFiles.comparison),
  ]);
  return { model, results: { baseline: baselineResult, scenario: scenarioResult }, traces: { baseline: baselineTrace, scenario: scenarioTrace }, comparison };
}

export async function generateMetadata(): Promise<Metadata> {
  if (!galleryEnabled()) return {};
  return { title: createTranslator(await requestLocale())("galleryResults.title") };
}

export default async function ResultsPreviewPage({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  if (!galleryEnabled()) notFound();
  const asked = (await searchParams).state;
  const state = states.find((s) => s === asked) ?? "results";
  return <ResultsPreview state={state} data={await boardLine()} />;
}
