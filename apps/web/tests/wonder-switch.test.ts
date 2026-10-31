// H-065: vitest.config.ts runs every unit test as each build, the production
// one (Muse Spark 1.3 alone, H-051) and one with Muse Wonder 1.0 on offer:
// this test fails if a project does not have the switch its name says, or if
// a project runs that is neither.
import { expect, it } from "vitest";
import { offeredModels, PROPOSAL_MODELS } from "../features/ai-proposals/proposal-models";

it("runs as the build its project names", ({ task }) => {
  const project = task.file.projectName;
  expect(["wonder-off", "wonder-on"]).toContain(project);
  expect(offeredModels()).toEqual(project === "wonder-on" ? PROPOSAL_MODELS : ["muse-spark-1.3"]);
});
