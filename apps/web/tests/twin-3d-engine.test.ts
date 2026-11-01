// @vitest-environment happy-dom
// H-072 review: the PlayCanvas adapter of the 3D view (features/twin-3d/engine.ts)
// on the real library, drawn on its null graphics device (no WebGL here).
// Every box, cylinder and sphere the view draws shares one mesh per shape,
// and the view keeps those meshes for as long as it is open: a theme change
// (setPalette, which rebuilds every draw in the new tokens) or a new scene
// never leaves a draw on a mesh PlayCanvas has destroyed, so the stage goes
// on drawing in the new theme instead of throwing on every frame.
import * as pc from "playcanvas";
import { afterEach, describe, expect, it } from "vitest";
import { motion } from "../features/playback/motion";
import { BOARD_LIMIT, placeBoards } from "../features/twin-3d/boards";
import { createView, type Palette, type View } from "../features/twin-3d/engine";
import { buildScene } from "../features/twin-3d/scene";
import { readElectronicsLine } from "./fixtures/electronics-line";

const data = readElectronicsLine();
const horizon = data.results.baseline.measurement_window.horizon_ticks;

const light: Palette = {
  sheet: "#ffffff", ink: "#0a0a0a", ink2: "#525252", ink4: "#a3a3a3", edge: "#8a8a8a", rule: "#ebebeb", ruleStrong: "#e0e0e0",
  baseline: "#6b6b6b", scenario: "#08784e", busy: "#d29a3a", warning: "#b26b00", error: "#c42430",
};
const dark: Palette = {
  sheet: "#111111", ink: "#ededed", ink2: "#a1a1a1", ink4: "#525252", edge: "#6e6e6e", rule: "#232323", ruleStrong: "#2e2e2e",
  baseline: "#a1a1a1", scenario: "#3dd68c", busy: "#b07a1e", warning: "#f5b14c", error: "#ff6b6b",
};

/** PlayCanvas as the viewer loads it, but on the null graphics device; `app` is the application the view made. */
function nullPlayCanvas() {
  const made: { app: pc.Application | null } = { app: null };
  class Application extends pc.Application {
    constructor(canvas: HTMLCanvasElement, options: pc.AppOptions & Record<string, unknown> = {} as never) {
      super(canvas, { ...options, graphicsDevice: new pc.NullGraphicsDevice(canvas) });
      made.app = this;
    }
  }
  return { lib: { ...pc, Application } as typeof pc, made };
}

/** Each draw of the view's: whether its mesh is alive (PlayCanvas has not destroyed it: its vertex and index buffers are there). */
function draws(app: pc.Application) {
  const out: { entity: string; alive: boolean }[] = [];
  const walk = (e: pc.GraphNode) => {
    const render = (e as pc.Entity).render;
    for (const mi of render?.meshInstances ?? []) out.push({ entity: e.name, alive: mi.mesh !== null && mi.mesh.vertexBuffer !== null && mi.mesh.indexBuffer[0] !== undefined });
    for (const c of e.children) walk(c);
  };
  walk(app.root);
  return out;
}

let view: View | null = null;
afterEach(() => {
  view?.destroy();
  view = null;
});

describe("the 3D view on PlayCanvas", () => {
  it("keeps drawing every part, board and light on live meshes after a theme change", () => {
    const { lib, made } = nullPlayCanvas();
    const canvas = document.createElement("canvas");
    view = createView(lib, canvas, light);
    const scene = buildScene(data.model);
    const m = motion(data.traces.baseline, data.model, horizon);
    view.setScene(scene);
    view.setPlayback({ side: "baseline", at: (tick) => placeBoards(scene, m.at(tick, { limit: BOARD_LIMIT })) });
    view.setTime(10 * 3_600_000);
    const app = made.app!;
    app.fire("update", 0);
    const before = draws(app);
    expect(before.length).toBeGreaterThan(3);
    expect(before.filter((d) => !d.alive)).toEqual([]);

    // The page's theme changes while the view is open: every draw is made again in the new tokens.
    view.setPalette(dark);
    view.setTime(11 * 3_600_000);
    app.fire("update", 0);
    const after = draws(app);
    expect(after.length).toBeGreaterThan(3);
    expect(after.filter((d) => !d.alive)).toEqual([]);

    // And back, and with a new scene: still every draw on a live mesh.
    view.setPalette(light);
    view.setScene(buildScene(data.model));
    app.fire("update", 0);
    expect(draws(app).filter((d) => !d.alive)).toEqual([]);
  });
});
