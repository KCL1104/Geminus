"use client";
// The results page's line in motion (H-072; docs/design/H-072_TRACE_ANIMATION.md
// §2, §6; DESIGN.md, The Trace Motion Rule). It replaces T-034's column per
// step: the baseline and the scenario play side by side, Baseline on the left
// and Scenario on the right (stacked under 1100 px), each a 3D stage titled
// with its run's pill, under one playback bar and on one clock, so a pile of
// boards that drains on one side and not on the other is seen at the same
// moment. Selecting a step in either stage selects it in both (shared IDs).
//
// Each run's boards come from its own sampled trace, built once into its
// motion (motion.ts) on the model that run ran: the scenario's stage is its
// version with the scenario's patch, so a fourth FCT tester stands there as
// a fourth cabinet. A run without a trace (none recorded, or it failed to
// load) stands still with its results' colours and says why; with no trace
// at all there is no bar. It plays once the traces are there (autoplay),
// paused instead under reduced motion; Pause is the bar's first control.
import { useId, useMemo, useState } from "react";
import type { ModelIR, Result, Run } from "../../../lib/contracts";
import { useI18n } from "../../../lib/i18n/provider";
import { motion } from "../../playback/motion";
import { type BarTrace, PlaybackBar } from "../../playback/playback-bar";
import { playableTrace, runModel, type RunSide, stillReason } from "../../playback/run";
import { usePageClock } from "../../playback/still";
import type { TraceState } from "../../results/load";
import { LineKey } from "../../simulations/run-status";
import { type LoadView, type Stage, Twin3D } from "../viewer";
import styles from "./playback.module.css";

/** A completed run on this page: what it is, its Result (the horizon, the colours of a still line) and its trace as loaded. */
export type PlaybackRun = { run: Run; result: Result; trace: TraceState };

const SIDES = ["baseline", "scenario"] as const;

/** The stage a run plays on, or null when it has nothing to play. */
function useStage(side: RunSide, model: ModelIR, run: PlaybackRun): Stage | null {
  const trace = playableTrace(run.trace);
  const horizon = run.result.measurement_window.horizon_ticks;
  return useMemo(() => (trace === null ? null : { side, trace, motion: motion(trace, model, horizon) }), [side, trace, model, horizon]);
}

/** ir: the version both runs ran (the scenario's base); each run's own patch is applied to it here. */
export function Playback({ ir, runs, loadView }: { ir: ModelIR; runs: Record<RunSide, PlaybackRun>; loadView?: LoadView }) {
  const { t } = useI18n();
  const baselineTitle = useId();
  const scenarioTitle = useId();
  const titles = { baseline: baselineTitle, scenario: scenarioTitle };
  const [selected, setSelected] = useState<string | null>(null);
  const bRun = runs.baseline.run;
  const sRun = runs.scenario.run;
  const models = useMemo(() => ({ baseline: runModel(ir, bRun), scenario: runModel(ir, sRun) }), [ir, bRun, sRun]);
  const stages: Record<RunSide, Stage | null> = {
    baseline: useStage("baseline", models.baseline, runs.baseline),
    scenario: useStage("scenario", models.scenario, runs.scenario),
  };
  const playing = SIDES.filter((s) => stages[s] !== null);
  const still = SIDES.filter((s) => stages[s] === null);
  // One clock over the longer run: a run shorter than the other holds at its end.
  const duration = playing.length === 0 ? null : Math.max(...playing.map((s) => runs[s].result.measurement_window.horizon_ticks));
  const clock = usePageClock(duration);
  const traces: BarTrace[] = playing.map((s) => ({ side: s, trace: stages[s]!.trace, end: stages[s]!.motion.end }));

  return (
    <div className={styles.motion}>
      {clock && traces.length > 0 && <PlaybackBar clock={clock} unit={ir.time_unit} domain={ir.domain} traces={traces} />}
      <div className={styles.stages}>
        {SIDES.map((side) => {
          const stage = stages[side];
          // The key to what a stage shows sits under one stage of each kind (the lights, or a still line's colours).
          const legend = side === (stage ? playing.at(-1) : still.at(-1));
          return (
            <section key={side} aria-labelledby={titles[side]} className={styles.stage} data-stage={side}>
              <h3 id={titles[side]} className={styles.title}>
                <span className={styles.pill}>
                  <LineKey side={side} />
                  {t(`runs.${side}`)}
                </span>
              </h3>
              {stage === null && <p className={styles.caption}>{t(stillReason(runs[side].trace))}</p>}
              <Twin3D
                ir={models[side]}
                results={runs[side].result}
                selected={selected}
                onSelect={setSelected}
                stage={stage}
                clock={stage ? clock : null}
                label={stage ? t(`playback.stage.${side}`) : undefined}
                legend={legend}
                loadView={loadView}
              />
            </section>
          );
        })}
      </div>
    </div>
  );
}
