"""T-032 mutation check: each mutant must make its test file fail; the file is restored after each."""
import subprocess
import sys
from pathlib import Path

web = Path(sys.argv[1])
mutants = [
    ("lanes: scenario stacks from the top", "features/results/marey.ts", "runLines(input.scenario, false)", "runLines(input.scenario, true)", "tests/marey.test.ts"),
    ("a truncated trace is extended", "features/results/marey.ts", "if (!trace.truncated && !lost && !left", "if (!lost && !left", "tests/marey.test.ts"),
    ("a queue entry that cannot be placed is guessed", "features/results/marey.ts", "if (r === null) lost = true;\n          else enter(t, r);\n          break;\n        }\n        case \"service_start\":", "if (r !== null) enter(t, r);\n          break;\n        }\n        case \"service_start\":", "tests/marey.test.ts"),
    ("blocking is drawn as service", "features/results/marey.ts", "if (transit && transit.end !== null) held = { to: null };", "", "tests/marey.test.ts"),
    ("rework back edge kept in the ranks", "features/results/marey.ts", "if (c === \"grey\") back.add(`${top.id}\\u0000${to}`);", "if (c === \"grey\") void 0;", "tests/marey.test.ts"),
    ("sinks not last", "features/results/marey.ts", "const sorted = [...steps.filter((n) => n.type !== \"sink\").sort(order), ...steps.filter((n) => n.type === \"sink\").sort(order)];", "const sorted = [...steps].sort(order);", "tests/marey.test.ts"),
    ("a missing run-level metric is left out", "features/results/metrics.ts", "if (!m && f.optional) continue;", "if (!m) continue;", "tests/results-metrics.test.ts"),
    ("relative delta recomputed in the browser", "features/results/metrics.ts", "absolute: metric.absolute_delta, relative: metric.relative_delta,", "absolute: metric.absolute_delta, relative: metric.baseline ? (metric.scenario! - metric.baseline) / metric.baseline : null,", "tests/results-metrics.test.ts"),
    ("CI taken for a median", "features/results/metrics.ts", "const ci = stat === \"mean\" ? summary?.confidence_interval_mean : null;", "const ci = summary?.confidence_interval_mean;", "tests/results-metrics.test.ts"),
    ("relative shown as a plain number", "features/results/format.ts", "return i18n.number(value, { ...percent, ...signed });", "return i18n.number(value, { ...precise, ...signed });", "tests/results-metrics.test.ts"),
    ("ratio difference not in points", "features/results/format.ts", "if (unit === \"ratio\") return i18n.t(\"compare.points\"", "if (unit === \"never\") return i18n.t(\"compare.points\"", "tests/results-metrics.test.ts"),
    ("an older answer replaces a newer one", "features/simulations/run-pair.ts", "return next.progress.revision >= current.progress.revision ? next : current;", "return next;", "tests/simulations.test.ts"),
    ("a run from another scenario is accepted", "features/simulations/run-pair.ts", "if (scenario.scenario_id !== scenarioId) return notThisScenario();", "", "tests/results-view.test.tsx"),
    ("the baseline gets the scenario", "features/simulations/run-config.ts", "baseline: { model_version_id: scenario.base_version_id, name: runName(baselineName), scenario_patch: [], config },", "baseline: { model_version_id: scenario.base_version_id, name: runName(baselineName), scenario_id: scenario.id, config },", "tests/simulations.test.ts"),
    ("a new key on every retry", "features/simulations/run-config.ts", "if (previous !== null && previous.fingerprint === fingerprint) return previous;", "", "tests/results-view.test.tsx"),
    ("a second cancel press is sent", "features/simulations/run-status.tsx", "if (!cancelling) onCancel();", "onCancel();", "tests/results-simplicity.test.tsx"),
    ("the cancel offered to a viewer", "features/results/run-results.tsx", "canCancel={canEdit(role)}", "canCancel", "tests/results-view.test.tsx"),
    ("no word when runs cannot be compared", "features/results/results-view.tsx", "{!comparison.comparable && (", "{false && (", "tests/results-view.test.tsx"),
    ("the summary claims a change when not comparable", "features/results/metrics.ts", "if (!comparison.comparable) return null;", "", "tests/results-metrics.test.ts"),
    ("polling goes on after both runs end", "features/simulations/use-run-pair.ts", "if (isActive(b.data) || isActive(s.data)) timer = setTimeout(poll, pollMs);", "timer = setTimeout(poll, pollMs);", "tests/results-view.test.tsx"),
    ("no chart notes for a missing trace", "features/results/marey-chart.tsx", "if (traces[side] === null && traces[other] !== null) return", "if (false) return", "tests/results-view.test.tsx"),
    ("the review page is always on", "app/ui-gallery/results/page.tsx", "if (!galleryEnabled()) notFound();", "", "tests/results-preview.test.tsx"),
    ("a colour literal in a feature stylesheet", "features/results/results.module.css", ".rowRule {\n  stroke: var(--gm-color-rule);", ".rowRule {\n  stroke: #dadad6;", "tests/design-rules.test.ts"),
    ("the run variant is not green", "ui/button.module.css", "  background: var(--gm-color-scenario);\n  color: var(--gm-color-on-scenario);", "  background: var(--gm-color-ink);\n  color: var(--gm-color-on-scenario);", "tests/tokens.test.ts"),
    ("a run created with a draft ID", "lib/api/client.ts", "if (!isID(\"mv\", body.model_version_id)) return notFound(\"The version\");", "", "tests/runs-api.test.ts"),
]

killed = 0
for name, file, old, new, test in mutants:
    path = web / file
    original = path.read_text()
    if original.count(old) != 1:
        print(f"NOT APPLIED ({original.count(old)} matches): {name}")
        continue
    path.write_text(original.replace(old, new))
    try:
        r = subprocess.run(["npx", "vitest", "run", test], cwd=web, capture_output=True, text=True, timeout=600)
    finally:
        path.write_text(original)
    status = "killed" if r.returncode != 0 else "SURVIVED"
    killed += r.returncode != 0
    print(f"{status}: {name} ({test})", flush=True)
print(f"{killed}/{len(mutants)} killed")
