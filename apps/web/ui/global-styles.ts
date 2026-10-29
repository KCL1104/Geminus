// The app's global styles, for the root layout and for global-error, which
// replaces the layout when it fails. The fonts are self-hosted from their
// npm packages (OFL-1.1, docs/THIRD_PARTY_NOTICES.md): the build copies the
// files into /_next/static, so they load from the app's own origin and
// nothing is fetched from a font service. Geist and Geist Mono by weight
// (H-041); Noto Sans TC by weight, split by unicode-range so a page loads
// only the characters it shows.
import "@fontsource-variable/geist/wght.css";
import "@fontsource-variable/geist-mono/wght.css";
import "@fontsource-variable/noto-sans-tc/wght.css";
import "./tokens.css";
import "./base.css";
