// A real screen of the product on the landing (H-048): the picture the
// capture script made (apps/web-e2e/scripts/landing-shots.mjs), one per
// theme where both exist, served from the app's own origin (the CSP allows
// img-src 'self' only). Each is WebP at its width on screen and at twice
// that; the browser picks one by the space it gets. Both themes' pictures
// are in the page and CSS shows the one the tokens show (landing.module.css),
// so a theme chosen in the menu applies at once. Every picture loads lazily,
// so a picture that is not shown is never fetched. (The first view's picture
// stays lazy too: a picture that is not lazy makes React put a preload link
// in the head, ahead of the CSP nonce meta that tests/layout.test.tsx pins
// as the head's first element; review round 1, L5.) A screenshot is framed
// as one (L9): its caption is a bar above the picture, on the well, so the
// picture's buttons do not read as the page's own.
import type { MessageKey, Translator } from "../../lib/i18n/translate";
import styles from "./landing.module.css";
import { type ShotData, shots } from "./shots";

export type ShotName = keyof typeof shots;
type Shown = "light" | "dark";

/** The widest a screen is drawn: the main region's content width (ui/tokens.css --gm-content-width, less the main's padding). */
const CONTENT = 1104;

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

/** A screen exists in the light theme only (the live demo's two): marked so the dark theme dims it (review round 1, L6). */
export function Shot({ name, t, altKey, captionKey }: { name: ShotName; t: Translator; altKey: MessageKey; captionKey: MessageKey }) {
  const shot: ShotData = shots[name];
  const all = pictures(name);
  const sizes = `(max-width: 720px) calc(100vw - 32px), ${Math.min(shot.width, CONTENT)}px`;
  const alt = t(altKey);
  return (
    <figure className={styles.shot} style={{ width: shot.width, maxWidth: "100%" }} data-live={all.length > 1 ? undefined : "light-only"}>
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
