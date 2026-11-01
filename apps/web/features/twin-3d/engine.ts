// The PlayCanvas adapter of the 3D view (T-033, H-072; ADR-008): it draws a
// Scene (scene.ts) and, while a run plays, its boards and lights
// (boards.ts), keeps an orbit camera, and turns a point on the canvas into
// the step under it. It is handed the library by the viewer's lazy import,
// so PlayCanvas never enters the page's main bundle, and it holds no model:
// setScene replaces every part.
//
// - One draw per shape and material (brief §7): every primitive of the
//   equipment, every board and every light is an instance of a box, a
//   cylinder or a sphere, grouped by its material (instancing).
// - At most one draw per animation frame: setTime only marks the time; the
//   app's own frame reads the playback once (Playback.at) and draws it, and
//   the canvas renders only when something changed (autoRender off), so a
//   paused stage costs nothing.
// - Colours are the tokens (Palette, read from the page by viewer.tsx).
//   The light is set so a face looking up shows its token exactly; the
//   lights and their sockets are flat (unlit), the token itself.
import type * as PlayCanvas from "playcanvas";
import type { StationState } from "../playback/motion";
import type { Placement } from "./boards";
import { BOARD, LAMP, type Shape } from "./kits";
import { COLUMN_MAX, FOV, fitDistance, ITEM_RISE, type Part, pick, type Scene, selectionFrame, START_PITCH, type Vec3 } from "./scene";

type PC = typeof PlayCanvas;
export type Side = "baseline" | "scenario";

/** The design tokens the scene is drawn in (read from the page when the view opens, and again when the theme changes). */
export type Palette = {
  sheet: string;
  ink: string;
  ink2: string;
  ink4: string;
  edge: string;
  rule: string;
  ruleStrong: string;
  baseline: string;
  scenario: string;
  /** Amber Edge: a fully used process, with results (T-033). */
  busy: string;
  /** Warning Mark: a light while blocked or changing over. */
  warning: string;
  error: string;
};

/** What a stage plays (H-072): whose run, and where its boards are and what its lights show at a time. */
export type Playback = {
  readonly side: Side;
  /** boards.ts placeBoards of the motion's frame at the tick. */
  at(tick: number): Placement;
  /** Called after each draw with what was drawn (the captions under the stage), outside React. */
  onDraw?(placement: Placement): void;
};

export type View = {
  setScene(scene: Scene): void;
  /** Redraws in another theme's tokens (the page's theme changed while the view was open). */
  setPalette(palette: Palette): void;
  setSelected(id: string | null): void;
  /** Playback (T-034): a column on each node, as tall as the sampled items there, in the run's colour; null removes them. */
  setOccupancy(counts: Readonly<Record<string, number>> | null, run: Side): void;
  /** The line in motion (H-072): what plays, or null for the line standing still (no boards, no lights). */
  setPlayback(playback: Playback | null): void;
  /** The time shown, in ticks: drawn on the view's next animation frame (at most one draw a frame). */
  setTime(tick: number): void;
  /** The step at a point of the canvas (CSS pixels from its top left), or null. */
  stepAt(x: number, y: number): string | null;
  orbit(dYaw: number, dPitch: number): void;
  zoom(factor: number): void;
  pan(dx: number, dy: number): void;
  reset(): void;
  resize(width: number, height: number): void;
  destroy(): void;
};

const MIN_PITCH = 10;
const COLUMN_WIDTH = 0.5;
const MAX_PITCH = 85;
/** The light: from above, in front and to the left. Ambient plus the key on a face looking up is 1 (linear). */
const KEY = (() => {
  const v = [-0.4, 1, 0.6];
  const l = Math.hypot(...v);
  return v.map((x) => x / l) as [number, number, number];
})();
const AMBIENT = 0.55;
/** A utilization's tint is one of this many steps (one material each). */
const TINTS = 20;
/** The outlined board's hollow: its inset from the tile's edge. */
const INSET = 0.07;

type Group = { entity: PlayCanvas.Entity; mesh: PlayCanvas.MeshInstance; vb: PlayCanvas.VertexBuffer | null; data: Float32Array };
type Batch = { shape: Shape; material: PlayCanvas.StandardMaterial; values: number[] };

export function createView(pc: PC, canvas: HTMLCanvasElement, palette: Palette): View {
  const app = new pc.Application(canvas, { graphicsDeviceOptions: { antialias: true, alpha: false } });
  app.setCanvasFillMode(pc.FILLMODE_NONE);
  app.setCanvasResolution(pc.RESOLUTION_AUTO);
  // Render only when something changed: a paused stage, or one without a run, draws nothing per frame.
  app.autoRender = false;
  const redraw = () => void (app.renderNextFrame = true);
  const device = app.graphicsDevice;
  const color = (css: string) => {
    const c = new pc.Color();
    c.fromString(css);
    return c;
  };
  const camera = new pc.Entity("camera", app);
  camera.addComponent("camera", { fov: FOV });
  app.root.addChild(camera);
  const sun = new pc.Entity("sun", app);
  // PlayCanvas takes a light's colour × intensity below 1 as sRGB: this intensity is the key's linear share.
  sun.addComponent("light", { type: "directional", intensity: ((1 - AMBIENT) / KEY[1]) ** (1 / 2.2) });
  sun.setRotation(new pc.Quat().setFromDirections(pc.Vec3.UP, new pc.Vec3(...KEY)));
  app.root.addChild(sun);
  const gray = AMBIENT ** (1 / 2.2);
  app.scene.ambientLight = new pc.Color(gray, gray, gray);

  const meshes: Record<Shape, PlayCanvas.Mesh> = {
    box: pc.Mesh.fromGeometry(device, new pc.BoxGeometry()),
    cylinder: pc.Mesh.fromGeometry(device, new pc.CylinderGeometry({ capSegments: 24 })),
    sphere: pc.Mesh.fromGeometry(device, new pc.SphereGeometry({ latitudeBands: 12, longitudeBands: 16 })),
  };
  const format = pc.VertexFormat.getDefaultInstancingFormat(device);

  // Materials by colour: shaded, or flat (the token itself: lights and their sockets).
  const materials = new Map<string, PlayCanvas.StandardMaterial>();
  const material = (c: PlayCanvas.Color, flat = false) => {
    const key = `${flat ? "flat" : "lit"}:${c.toString(false)}`;
    let m = materials.get(key);
    if (!m) {
      m = new pc.StandardMaterial();
      m.fresnelModel = pc.FRESNEL_NONE;
      m.useSkybox = false;
      m.useFog = false;
      if (flat) {
        m.useLighting = false;
        m.useTonemap = false;
        m.diffuse = new pc.Color(0, 0, 0);
        m.emissive = c;
      } else m.diffuse = c;
      m.update();
      materials.set(key, m);
    }
    return m;
  };

  // A set of instanced draws, one per shape and material; draw() replaces what it shows.
  const layer = () => {
    const groups = new Map<string, Group>();
    const make = (shape: Shape, m: PlayCanvas.StandardMaterial): Group => {
      const entity = new pc.Entity(undefined, app);
      const mesh = new pc.MeshInstance(meshes[shape], m);
      entity.addComponent("render", { meshInstances: [mesh], castShadows: false, receiveShadows: false });
      app.root.addChild(entity);
      return { entity, mesh, vb: null, data: new Float32Array(0) };
    };
    return {
      draw(batches: ReadonlyMap<string, Batch>) {
        for (const [key, g] of groups) if (!batches.has(key)) g.entity.enabled = false;
        for (const [key, b] of batches) {
          let g = groups.get(key);
          if (!g) groups.set(key, (g = make(b.shape, b.material)));
          const n = b.values.length / 7;
          if (!g.vb || g.vb.numVertices < n) {
            const room = Math.max(16, 2 ** Math.ceil(Math.log2(Math.max(1, n))));
            g.vb?.destroy();
            g.data = new Float32Array(room * 16);
            g.vb = new pc.VertexBuffer(device, format, room, { usage: pc.BUFFER_DYNAMIC, data: g.data });
            g.mesh.setInstancing(g.vb);
          }
          for (let i = 0; i < n; i++) matrix(g.data, i, b.values, i * 7);
          if (n > 0) g.vb.unlock(0, n * 64);
          g.mesh.instancingCount = n;
          g.entity.enabled = n > 0;
        }
        redraw();
      },
      clear() {
        for (const g of groups.values()) {
          g.entity.destroy();
          g.vb?.destroy();
        }
        groups.clear();
      },
    };
  };
  const equipment = layer();
  const tiles = layer();
  const lamps = layer();
  const add = (batches: Map<string, Batch>, shape: Shape, m: PlayCanvas.StandardMaterial, p: Vec3, s: Vec3, yaw: number) => {
    const key = `${shape}:${m.id}`;
    let b = batches.get(key);
    if (!b) batches.set(key, (b = { shape, material: m, values: [] }));
    b.values.push(p[0], p[1], p[2], s[0], s[1], s[2], yaw);
  };

  // The palette's colours (setPalette replaces them all). A queue's column sits between Rule and Control Edge: Rule alone vanishes on the white sheet.
  let tones = { sheet: new pc.Color(), ink: new pc.Color(), body: new pc.Color(), trim: new pc.Color(), belt: new pc.Color(), detail: new pc.Color(), queue: new pc.Color(), busy: new pc.Color() };
  let inks = { baseline: new pc.Color(), scenario: new pc.Color() };
  let states: Record<StationState, PlayCanvas.Color> = { busy: new pc.Color(), idle: new pc.Color(), blocked: new pc.Color(), setup: new pc.Color(), failed: new pc.Color() };
  const applyPalette = (p: Palette) => {
    const trim = color(p.edge);
    tones = { sheet: color(p.sheet), ink: color(p.ink), body: color(p.ruleStrong), trim, belt: color(p.rule), detail: color(p.ink2), queue: new pc.Color().lerp(color(p.rule), trim, 0.5), busy: color(p.busy) };
    inks = { baseline: color(p.baseline), scenario: color(p.scenario) };
    // DESIGN.md, The Trace Motion Rule: Ink busy, Ink 4 idle, Warning while blocked or changing over, Error while down.
    states = { busy: color(p.ink), idle: color(p.ink4), blocked: color(p.warning), setup: color(p.warning), failed: color(p.error) };
    if (camera.camera) camera.camera.clearColor = color(p.sheet);
    equipment.clear();
    tiles.clear();
    lamps.clear();
    for (const m of materials.values()) m.destroy();
    materials.clear();
  };
  applyPalette(palette);

  let scene: Scene = { nodes: [], edges: [], center: [0, 0, 0], radius: 5, columns: false, parts: [], stations: {}, links: {} };
  let selected: string | null = null;
  let utilization = new Map<string, number>();
  // A part's material: with results, a process's body goes from Control Edge (idle) toward Amber Edge (fully used).
  const partMaterial = (part: Part) => {
    switch (part.tone) {
      case "body": {
        const u = part.node === null ? undefined : utilization.get(part.node);
        if (u !== undefined) return material(new pc.Color().lerp(tones.trim, tones.busy, Math.round(u * TINTS) / TINTS));
        return material(tones.body);
      }
      case "ring":
        return material(tones.sheet, true);
      default:
        return material(tones[part.tone]);
    }
  };
  const paint = () => {
    const batches = new Map<string, Batch>();
    for (const part of scene.parts) add(batches, part.shape, partMaterial(part), part.position, part.size, part.yaw);
    // The selected step: a Graphite Ink frame on the floor around it (scene.ts selectionFrame).
    const chosen = selected === null ? undefined : scene.nodes.find((n) => n.id === selected);
    if (chosen) for (const bar of selectionFrame(chosen)) add(batches, bar.shape, material(tones.ink, true), bar.position, bar.size, bar.yaw);
    equipment.draw(batches);
    stack();
  };

  // The line in motion: the playback, the time to show, and whether that time is drawn yet.
  let playback: Playback | null = null;
  let time = 0;
  let due = false;
  const drawPlacement = (placement: Placement | null, side: Side) => {
    const boards = new Map<string, Batch>();
    const lights = new Map<string, Batch>();
    if (placement) {
      // Baseline boards are outlined (a tile with a hollow of the sheet), scenario boards solid: The Two Inks Rule's dash in 3D.
      const ink = material(inks[side]);
      const hollow = side === "baseline" ? material(tones.sheet) : null;
      const inner: Vec3 = [BOARD[0] - 2 * INSET, BOARD[1] + 0.004, BOARD[2] - 2 * INSET];
      for (const b of placement.boards) {
        add(boards, "box", ink, b.position, BOARD, b.yaw);
        if (hollow) add(boards, "box", hollow, b.position, inner, b.yaw);
      }
      const d = 2 * LAMP;
      for (const l of placement.lights) add(lights, "sphere", material(states[l.state], true), l.position, [d, d, d], 0);
    }
    tiles.draw(boards);
    lamps.draw(lights);
  };
  app.on("update", () => {
    if (!due) return;
    due = false;
    const p = playback;
    if (!p) return drawPlacement(null, "scenario");
    const placement = p.at(time);
    drawPlacement(placement, p.side);
    p.onDraw?.(placement);
  });

  // T-034's columns: one per node, made once per scene and only resized.
  const columns = new Map<string, PlayCanvas.Entity>();
  let occupancy: { counts: Readonly<Record<string, number>>; run: Side } | null = null;
  const stack = () => {
    for (const n of scene.nodes) {
      const count = occupancy?.counts[n.id] ?? 0;
      let c = columns.get(n.id);
      if (count === 0) {
        if (c) c.enabled = false;
        continue;
      }
      if (!c) {
        c = new pc.Entity(`${n.id}:items`, app);
        c.addComponent("render", { type: "box" });
        app.root.addChild(c);
        columns.set(n.id, c);
      }
      const h = Math.min(COLUMN_MAX, count * ITEM_RISE);
      c.enabled = true;
      c.setLocalScale(COLUMN_WIDTH, h, COLUMN_WIDTH);
      c.setPosition(n.position[0], n.position[1] + n.size[1] / 2 + h / 2, n.position[2]);
      const mi = c.render?.meshInstances[0];
      if (mi && occupancy) mi.material = material(inks[occupancy.run]);
    }
    redraw();
  };

  // The orbit: yaw and pitch in degrees about the target, at a distance.
  // It starts facing the line (yaw 0), which runs left to right as in 2D.
  const orbit = { yaw: 0, pitch: START_PITCH, distance: 10, target: new pc.Vec3() };
  const place = () => {
    const yaw = (orbit.yaw * Math.PI) / 180;
    const pitch = (orbit.pitch * Math.PI) / 180;
    camera.setPosition(
      orbit.target.x + orbit.distance * Math.cos(pitch) * Math.sin(yaw),
      orbit.target.y + orbit.distance * Math.sin(pitch),
      orbit.target.z + orbit.distance * Math.cos(pitch) * Math.cos(yaw),
    );
    camera.lookAt(orbit.target);
    redraw();
  };
  // The distance at which the first view holds the whole line, and the
  // tallest column playback can raise, in perspective (scene.ts fitDistance).
  const fit = () => fitDistance(scene, Math.max(0.5, canvas.clientWidth / Math.max(1, canvas.clientHeight)));
  const reset = () => {
    orbit.yaw = 0;
    orbit.pitch = START_PITCH;
    orbit.distance = scene.nodes.length === 0 ? scene.radius * 1.9 : fit();
    orbit.target.set(...scene.center);
    place();
  };

  app.start();
  redraw();

  const view: View = {
    setScene(next) {
      const first = scene.nodes.length === 0;
      for (const e of columns.values()) e.destroy();
      columns.clear();
      scene = next;
      utilization = new Map(next.nodes.flatMap((n) => (n.utilization === null ? [] : [[n.id, n.utilization] as const])));
      paint();
      due = true;
      if (first) reset();
    },
    setPalette(p) {
      applyPalette(p);
      for (const e of columns.values()) e.destroy();
      columns.clear();
      paint();
      due = true;
    },
    setSelected(id) {
      selected = id;
      paint();
    },
    setOccupancy(counts, run) {
      occupancy = counts && { counts, run };
      stack();
    },
    setPlayback(next) {
      playback = next;
      due = true;
    },
    setTime(tick) {
      time = tick;
      due = true;
    },
    stepAt(x, y) {
      const cam = camera.camera;
      if (!cam) return null;
      const near = cam.screenToWorld(x, y, cam.nearClip);
      const far = cam.screenToWorld(x, y, cam.farClip);
      const dir: Vec3 = [far.x - near.x, far.y - near.y, far.z - near.z];
      return pick(scene, [near.x, near.y, near.z], dir);
    },
    orbit(dYaw, dPitch) {
      orbit.yaw += dYaw;
      orbit.pitch = Math.min(MAX_PITCH, Math.max(MIN_PITCH, orbit.pitch + dPitch));
      place();
    },
    zoom(factor) {
      orbit.distance = Math.min(scene.radius * 8, Math.max(2, orbit.distance * factor));
      place();
    },
    pan(dx, dy) {
      // Along the ground, in the camera's own left–right and forward–back.
      const yaw = (orbit.yaw * Math.PI) / 180;
      const k = orbit.distance / 600;
      orbit.target.x += (-Math.cos(yaw) * dx - Math.sin(yaw) * dy) * k;
      orbit.target.z += (Math.sin(yaw) * dx - Math.cos(yaw) * dy) * k;
      place();
    },
    reset,
    resize(width, height) {
      app.resizeCanvas(width, height);
      redraw();
    },
    destroy() {
      equipment.clear();
      tiles.clear();
      lamps.clear();
      for (const m of Object.values(meshes)) m.destroy();
      app.destroy();
    },
  };
  return view;
}

/** Writes instance i's world matrix (column-major): at p, turned yaw degrees about y, scaled by s; v holds p, s, yaw from offset o. */
function matrix(out: Float32Array, i: number, v: readonly number[], o: number) {
  const a = (v[o + 6]! * Math.PI) / 180;
  const c = Math.cos(a);
  const s = Math.sin(a);
  const [sx, sy, sz] = [v[o + 3]!, v[o + 4]!, v[o + 5]!];
  const k = i * 16;
  out[k] = c * sx;
  out[k + 1] = 0;
  out[k + 2] = -s * sx;
  out[k + 3] = 0;
  out[k + 4] = 0;
  out[k + 5] = sy;
  out[k + 6] = 0;
  out[k + 7] = 0;
  out[k + 8] = s * sz;
  out[k + 9] = 0;
  out[k + 10] = c * sz;
  out[k + 11] = 0;
  out[k + 12] = v[o]!;
  out[k + 13] = v[o + 1]!;
  out[k + 14] = v[o + 2]!;
  out[k + 15] = 1;
}
