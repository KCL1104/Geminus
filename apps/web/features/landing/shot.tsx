// A real screen of the product on the landing (H-048): the picture the
// capture script made (apps/web-e2e/scripts/landing-shots.mjs), one per
// theme where both exist, served from the app's own origin (the CSP allows
// img-src 'self', and blob: for H-052's photo). Each is WebP at its width
// and at twice that; the browser picks one by the space it gets. H-088: a
// screen spans the landing's 1104 px box, so every frame shares both edges;
// one taken narrow on purpose (the live explanation, 408 px) keeps its own
// width, centred. Both themes' pictures are in the page and CSS shows the
// one the tokens show (landing.module.css), so a theme chosen in the menu
// applies at once. Every picture loads lazily, so a picture that is not
// shown is never fetched. (A picture that is not lazy makes React put a
// preload link in the head, ahead of the CSP nonce meta that
// tests/layout.test.tsx pins as the head's first element; review round 1,
// L5.) A screenshot is framed as one (L9): its caption is a bar above the
// picture, on the well, so the picture's buttons do not read as the page's
// own.
import type { MessageKey, Translator } from "../../lib/i18n/translate";
import styles from "./landing.module.css";
import { type ShotData, shots } from "./shots";

export type ShotName = keyof typeof shots;
type Shown = "light" | "dark";

/** The landing's box (landing.module.css): the main region's content width (ui/tokens.css --gm-content-width, less 2 × 24 px). */
export const BOX = 1104;

/** The pictures of a screen: per theme, its files at 1x and 2x as a srcset. */
export function pictures(name: ShotName): { theme: Shown; src: string; srcSet: string }[] {
  const shot: ShotData = shots[name];
  return (["light", "dark"] as const).flatMap((theme) => {
    const files = shot.themes[theme];
    if (files === undefined || files.length === 0) return [];
    const srcSet = files.map((f, i) => `/landing/${f} ${shot.width * (i + 1)}w`).join(", ");
    return [{ theme, src: `/landing/${files[0]}`, srcSet }];
  });
}

/**
 * A screen, framed. At the box's width, or with own at its own width (centred). A screen that exists
 * in the light theme only (the live demo's) is marked so the dark theme dims it (review round 1, L6).
 */
export function Shot({ name, t, altKey, captionKey, own = false }: { name: ShotName; t: Translator; altKey: MessageKey; captionKey: MessageKey; own?: boolean }) {
  const shot: ShotData = shots[name];
  const all = pictures(name);
  const sizes = `(max-width: 720px) calc(100vw - 32px), ${own ? Math.min(shot.width, BOX) : BOX}px`;
  const alt = t(altKey);
  return (
    <figure
      className={styles.shot}
      style={own ? { width: shot.width, maxWidth: "100%" } : undefined}
      data-fit={own ? "own" : "box"}
      data-live={all.length > 1 ? undefined : "light-only"}
    >
      <figcaption>{t(captionKey)}</figcaption>
      {all.map((p) => (
        // A plain img: the pictures are made at their sizes ahead of time, so the app needs no image optimizer at run time.
        // eslint-disable-next-line @next/next/no-img-element
        <img
          key={p.theme}
          className={all.length > 1 ? styles[p.theme] : undefined}
          src={p.src}
          srcSet={p.srcSet}
          sizes={sizes}
          width={shot.width}
          height={shot.height}
          alt={alt}
          loading="lazy"
          decoding="async"
        />
      ))}
    </figure>
  );
}
