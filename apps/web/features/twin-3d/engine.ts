// The PlayCanvas adapter of the 3D view (T-033, ADR-008): it draws a Scene
// (scene.ts) and nothing else, keeps an orbit camera, and turns a point on
// the canvas into the step under it. It is handed the library by the
// viewer's lazy import, so PlayCanvas never enters the page's main bundle,
// and it holds no model: setScene replaces every object.
import type * as PlayCanvas from "playcanvas";
import { pick, type Scene, type SceneNode, type Vec3 } from "./scene";

type PC = typeof PlayCanvas;

/** The design tokens the scene is drawn in (read from the page when the view opens). */
export type Palette = { sheet: string; ink: string; edge: string; rule: string; baseline: string; scenario: string; busy: string };

export type View = {
  setScene(scene: Scene): void;
  /** Redraws in another theme's tokens (the page's theme changed while the view was open). */
  setPalette(palette: Palette): void;
  setSelected(id: string | null): void;
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
const MAX_PITCH = 85;

export function createView(pc: PC, canvas: HTMLCanvasElement, palette: Palette): View {
  const app = new pc.Application(canvas, { graphicsDeviceOptions: { antialias: true, alpha: false } });
  app.setCanvasFillMode(pc.FILLMODE_NONE);
  app.setCanvasResolution(pc.RESOLUTION_AUTO);
  const color = (css: string) => {
    const c = new pc.Color();
    c.fromString(css);
    return c;
  };
  const camera = new pc.Entity("camera");
  camera.addComponent("camera", { fov: 40 });
  app.root.addChild(camera);
  const sun = new pc.Entity("sun");
  sun.addComponent("light", { type: "directional", intensity: 1.1 });
  sun.setEulerAngles(50, 30, 0);
  app.root.addChild(sun);

  const materials = new Map<string, PlayCanvas.StandardMaterial>();
  const material = (c: PlayCanvas.Color) => {
    const key = c.toString(false);
    let m = materials.get(key);
    if (!m) {
      m = new pc.StandardMaterial();
      m.diffuse = c;
      m.update();
      materials.set(key, m);
    }
    return m;
  };
  // The palette's colours (setPalette replaces them all). A queue sits
  // between Rule and Control Edge: Rule alone vanishes on the white sheet.
  let idle = new pc.Color();
  let busy = new pc.Color();
  let tones = { ink: idle, baseline: idle, queue: idle };
  const line = new pc.Color();
  const applyPalette = (p: Palette) => {
    idle = color(p.edge);
    busy = color(p.busy);
    tones = { ink: color(p.ink), baseline: color(p.baseline), queue: new pc.Color().lerp(color(p.rule), idle, 0.5) };
    line.copy(color(p.baseline));
    if (camera.camera) camera.camera.clearColor = color(p.sheet);
    app.scene.ambientLight = color(p.rule);
    for (const m of materials.values()) m.destroy();
    materials.clear();
  };
  applyPalette(palette);
  // A step's colour: Graphite Ink when selected; with results, a process
  // from Control Edge (idle) toward the busy colour (fully used); else by kind.
  const tone = (n: SceneNode, selected: boolean): PlayCanvas.Color => {
    if (selected) return tones.ink;
    if (n.utilization !== null) return new pc.Color().lerp(idle, busy, n.utilization);
    if (n.type === "source" || n.type === "sink") return tones.baseline;
    if (n.type === "queue") return tones.queue;
    return idle;
  };

  let scene: Scene = { nodes: [], edges: [], center: [0, 0, 0], radius: 5 };
  let selected: string | null = null;
  const objects = new Map<string, PlayCanvas.Entity>();
  const ends: [PlayCanvas.Vec3, PlayCanvas.Vec3][] = [];

  // The orbit: yaw and pitch in degrees about the target, at a distance.
  // It starts facing the line (yaw 0), which runs left to right as in 2D.
  const orbit = { yaw: 0, pitch: 40, distance: 10, target: new pc.Vec3() };
  const place = () => {
    const yaw = (orbit.yaw * Math.PI) / 180;
    const pitch = (orbit.pitch * Math.PI) / 180;
    camera.setPosition(
      orbit.target.x + orbit.distance * Math.cos(pitch) * Math.sin(yaw),
      orbit.target.y + orbit.distance * Math.sin(pitch),
      orbit.target.z + orbit.distance * Math.cos(pitch) * Math.cos(yaw),
    );
    camera.lookAt(orbit.target);
  };
  const reset = () => {
    orbit.yaw = 0;
    orbit.pitch = 40;
    orbit.distance = scene.radius * 1.9;
    orbit.target.set(...scene.center);
    place();
  };

  const paint = () => {
    for (const n of scene.nodes) {
      const e = objects.get(n.id);
      const mi = e?.render?.meshInstances[0];
      if (mi) mi.material = material(tone(n, n.id === selected));
    }
  };

  app.on("update", () => {
    for (const [a, b] of ends) app.drawLine(a, b, line, true);
  });
  app.start();

  const view: View = {
    setScene(next) {
      const first = scene.nodes.length === 0;
      for (const e of objects.values()) e.destroy();
      objects.clear();
      scene = next;
      for (const n of next.nodes) {
        // A draft may repeat an ID; the first stands, as in the layout (review r1).
        if (objects.has(n.id)) continue;
        const e = new pc.Entity(n.id);
        e.addComponent("render", { type: n.shape });
        e.setLocalScale(...n.size);
        e.setPosition(...n.position);
        e.setEulerAngles(0, n.rotationY, 0);
        app.root.addChild(e);
        objects.set(n.id, e);
      }
      ends.length = 0;
      for (const edge of next.edges) ends.push([new pc.Vec3(...edge.start), new pc.Vec3(...edge.end)]);
      paint();
      if (first) reset();
    },
    setPalette(p) {
      applyPalette(p);
      paint();
    },
    setSelected(id) {
      selected = id;
      paint();
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
    },
    destroy() {
      app.destroy();
    },
  };
  return view;
}
