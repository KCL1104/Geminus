/**
 * The component gallery (/ui-gallery, T-045) is for review and the browser
 * checks, not for users: it answers 404 unless the deployment sets
 * GEMINUS_UI_GALLERY=1.
 */
export function galleryEnabled(): boolean {
  return process.env.GEMINUS_UI_GALLERY === "1";
}
