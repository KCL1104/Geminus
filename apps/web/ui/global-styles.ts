// The app's global styles, for the root layout and for global-error, which
// replaces the layout when it fails. The fonts are self-hosted from their
// npm packages (OFL-1.1, docs/THIRD_PARTY_NOTICES.md): the build copies the
// files into /_next/static, so they load from the app's own origin and
// nothing is fetched from a font service. Archivo with its weight and width
// axes (DESIGN.md: "narrow before small"); Noto Sans TC by weight, split by
// unicode-range so a page loads only the characters it shows.
import "@fontsource-variable/archivo/standard.css";
import "@fontsource-variable/noto-sans-tc/wght.css";
import "./tokens.css";
import "./base.css";
