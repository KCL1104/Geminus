// The landing at / (H-048), for the hackathon's judges first and anyone who
// opens the app: what DryLine is, the loop it runs on real screens of the
// product, which Meta model does each step, the industries the templates
// cover and why it is safe to try. Only facts the product holds today
// (README.md, PRODUCT.md): no figure, customer or result beyond the
// synthetic demo, and no credential (the judges' sign-in is in the
// submission). Every string is the catalog's (REQ-022).
//
// H-088 (the owner's choice, direction B "Loop Spine"): centred, as Railway's
// and Supabase's pages are. On / the rail is the bar it is on a phone, at
// every width (app/_components/rail.tsx), and everything here sits in one
// centred 1104 px box with equal margins, the hairlines between sections
// running the full width. Short text is centred (the hero, the section heads,
// the closing line), long text is left-aligned inside the box.
// THESIS: the page is the record of one what-if, from the question to the
// decision; each step names who acts (you, a Meta model, the DryLine server,
// the engine). Under the hero, the loop band shows all five steps and who
// acts in each, so the first view has the Meta models, DryLine's check and
// the person's approval; each cell links to its station below, on a hairline
// spine down the page's centre, each with its real screen at the box's
// width. "You decide" is the one station without a screen: the person's step.
// It refuses the feature-card grid and the big-number hero.
// OWN-WORLD: the app's own system (DESIGN.md, H-041), with one written
// exception: the hero's h1 at 40 px on this page only (H-088). Cards in a
// hairline ring, Geist, the violet accent only for the AI, Ink for DryLine's
// own check, green only inside the screens' scenario data.
import type { MessageKey, Translator } from "../../lib/i18n/translate";
import { investigationsOffered } from "../investigations/flags";
import { templateName } from "../models/templates";
import { LandingAction } from "./cta";
import styles from "./landing.module.css";
import { Shot, type ShotName } from "./shot";

type Actor = "you" | "muse" | "sam" | "geminus" | "engine";

/** Who acts in a step, as a pill: a Meta model with its provider, DryLine's server, the engine or the person. */
function ActorPill({ actor, t }: { actor: Actor; t: Translator }) {
  if (actor === "muse" || actor === "sam") {
    return (
      <li className={styles.actor} data-kind="ai">
        <span className={styles.provider}>{t("ai.provider.meta")}</span>{" "}
        <span>{actor === "muse" ? t("ai.model.muse-spark-1.3") : t("ai.model.sam-3.1")}</span>
      </li>
    );
  }
  const name = actor === "you" ? t("landing.actor.you") : actor === "geminus" ? t("landing.actor.geminus") : t("landing.actor.engine");
  return (
    <li className={styles.actor} data-kind={actor === "geminus" ? "server" : "other"}>
      {name}
    </li>
  );
}

function Who({ actors, t }: { actors: Actor[]; t: Translator }) {
  return (
    <ul className={styles.who} aria-label={t("landing.who")}>
      {actors.map((a) => (
        <ActorPill key={a} actor={a} t={t} />
      ))}
    </ul>
  );
}

type ShotProps = { name: ShotName; alt: MessageKey; caption: MessageKey; own?: boolean };
type StepProps = { id: string; title: MessageKey; body: MessageKey; actors: Actor[]; shots: ShotProps[] };

const steps: StepProps[] = [
  {
    id: "describe", title: "landing.step.describe.title", body: "landing.step.describe.body", actors: ["you"],
    shots: [{ name: "canvas", alt: "landing.shot.canvas.alt", caption: "landing.shot.canvas.caption" }],
  },
  {
    // H-088: the proposal page, on the gallery's sample (no AI called; its caption says so): the changes,
    // DryLine's own check beside the AI's claim, and the decision the next step is about.
    id: "propose", title: "landing.step.propose.title", body: "landing.step.propose.body", actors: ["muse", "sam", "geminus"],
    shots: [{ name: "proposed", alt: "landing.shot.proposed.alt", caption: "landing.shot.proposed.caption" }],
  },
  // The person's step has no screen of its own: its decision is in the proposal's frame just above.
  { id: "approve", title: "landing.step.approve.title", body: "landing.step.approve.body", actors: ["you"], shots: [] },
  {
    id: "simulate", title: "landing.step.simulate.title", body: "landing.step.simulate.body", actors: ["you", "engine", "geminus"],
    shots: [
      { name: "results", alt: "landing.shot.results.alt", caption: "landing.shot.results.caption" },
      { name: "marey", alt: "landing.shot.marey.alt", caption: "landing.shot.marey.caption" },
    ],
  },
  {
    // Muse Spark's own explanation from the live demo (review round 1, M2), at its own width.
    id: "explain", title: "landing.step.explain.title", body: "landing.step.explain.body", actors: ["muse", "geminus"],
    shots: [{ name: "explained", alt: "landing.shot.explained.alt", caption: "landing.shot.explained.caption", own: true }],
  },
];

/** The loop band (H-088): the five steps in a row, each with who acts, each a link to its station below. */
function Band({ t }: { t: Translator }) {
  return (
    <ol className={styles.band} aria-label={t("landing.loop.band")} data-band>
      {steps.map((s) => (
        <li key={s.id} className={styles.cell}>
          <span className={styles.dot} aria-hidden="true">
            <span />
          </span>
          <a href={`#step-${s.id}`} className={styles.jump}>
            {t(s.title)}
          </a>
          <Who actors={s.actors} t={t} />
        </li>
      ))}
    </ol>
  );
}

/**
 * A station on the spine: its number, the step and who acts on one row (stacked on a phone), what happens under
 * them, then the screens it happens on.
 */
function Station({ step, t }: { step: StepProps; t: Translator }) {
  return (
    <li id={`step-${step.id}`} className={styles.station}>
      <div className={styles.stationHead}>
        <div className={styles.stationTitle}>
          <h3>{t(step.title)}</h3>
          <Who actors={step.actors} t={t} />
        </div>
        <p>{t(step.body)}</p>
      </div>
      {step.shots.map((s) => (
        <Shot key={s.name} name={s.name} t={t} altKey={s.alt} captionKey={s.caption} own={s.own} />
      ))}
    </li>
  );
}

/**
 * Meta's models, and the one tool of the Meta Model API that the Muse models use (review round 1, M1: a tool, not a model).
 * H-065: not Muse Wonder 1.0, in any build, also one that offers it for a proposal (NEXT_PUBLIC_GEMINUS_OFFER_WONDER=1):
 * its landing line is not approved (H-049's landing.meta.wonder stays in the catalogs, unused, until the owner decides it).
 */
const meta: { name: MessageKey; kind: MessageKey; body: MessageKey }[] = [
  { name: "ai.model.muse-spark-1.3", kind: "ai.provider.meta", body: "landing.meta.muse" },
  { name: "ai.model.sam-3.1", kind: "ai.provider.meta", body: "landing.meta.sam" },
  { name: "landing.meta.searchName", kind: "landing.meta.tool", body: "landing.meta.search" },
];

const industries: { name: MessageKey; detail?: MessageKey }[] = [
  // The picker's own names (H-045), so the landing and the product agree.
  { name: templateName("electronics_assembly_test_line"), detail: "landing.industry.electronics.steps" },
  { name: templateName("shoe_factory") },
  { name: templateName("food_plant") },
  { name: templateName("carton_plant") },
  { name: templateName("warehouse") },
];

const safety: [MessageKey, MessageKey][] = [
  ["landing.safe.proposes", "landing.safe.proposes.body"],
  ["landing.safe.checks", "landing.safe.checks.body"],
  ["landing.safe.person", "landing.safe.person.body"],
  ["landing.safe.versions", "landing.safe.versions.body"],
  ["landing.safe.synthetic", "landing.safe.synthetic.body"],
];

/**
 * H-061 A8 (H-056, H-060: the Guardrails revised): in a build with AI
 * investigations on, the AI may act for the person who asks, so "only
 * suggests" and "a person approves" would no longer be the whole truth; the
 * first and third rules say what it may do and what it never does.
 */
const safetyActing: [MessageKey, MessageKey][] = [
  ["landing.safe.acts", "landing.safe.acts.body"],
  ["landing.safe.checks", "landing.safe.checks.body"],
  ["landing.safe.publishes", "landing.safe.publishes.body"],
  ["landing.safe.versions", "landing.safe.versions.body"],
  ["landing.safe.synthetic", "landing.safe.synthetic.body"],
];

/** The Meta model the lede names, the same in both languages. */
const MUSE = "Muse Spark";

/**
 * A name kept on one line: its spaces become no-break spaces on screen. The zh-Hant lede broke "Muse Spark" in two
 * at every width (H-088 review: keep-all leaves the name's space as the break nearest the balance point). In
 * Chinese the space after the name only sets it apart from the next word, so it is kept with that word too, and
 * the line breaks at the sentence's punctuation (「…產線照片。」 / 「Muse Spark 擬好修改…」). The catalog keeps
 * plain spaces: the lede is also the meta description, and strings.spec reads both as one space.
 */
export function whole(text: string, name: string): string {
  const kept = name.replaceAll(" ", "\u00a0");
  const at = new RegExp(`${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}( (?=\\p{Script=Han}))?`, "gu");
  return text.replace(at, (_, han?: string) => (han === undefined ? kept : `${kept}\u00a0`));
}

export function Landing({ t }: { t: Translator }) {
  return (
    <div className={styles.landing}>
      <section className={styles.hero} aria-labelledby="landing-title">
        <div className={styles.heroText}>
          <h1 id="landing-title">{t("landing.title")}</h1>
          <p className={styles.lede}>{whole(t("landing.lede"), MUSE)}</p>
          <LandingAction />
        </div>
      </section>

      <section className={styles.section} aria-labelledby="landing-loop">
        <div className={styles.box}>
          <div className={styles.head}>
            <h2 id="landing-loop">{t("landing.loop.title")}</h2>
            <p className={styles.intro}>{t("landing.loop.intro")}</p>
          </div>
          <Band t={t} />
          <ol className={styles.stations} data-steps>
            {steps.map((s) => (
              <Station key={s.id} step={s} t={t} />
            ))}
          </ol>
        </div>
      </section>

      <section className={styles.section} aria-labelledby="landing-meta">
        <div className={styles.box}>
          <div className={styles.head}>
            <h2 id="landing-meta">{t("landing.meta.title")}</h2>
            <p className={styles.intro}>{t("landing.meta.intro")}</p>
          </div>
          <dl className={styles.columns}>
            {meta.map((m) => (
              <div key={m.body} className={styles.column}>
                <dt>
                  <span>{t(m.name)}</span>{" "}
                  <span className={styles.provider}>{t(m.kind)}</span>
                </dt>
                <dd>{t(m.body)}</dd>
              </div>
            ))}
          </dl>
          <p className={styles.named}>{t("landing.meta.named")}</p>
        </div>
      </section>

      <section className={styles.section} aria-labelledby="landing-industries">
        <div className={styles.box}>
          <div className={styles.head}>
            <h2 id="landing-industries">{t("landing.industries.title")}</h2>
            <p className={styles.intro}>{t("landing.industries.intro")}</p>
          </div>
          <ul className={styles.industries}>
            {industries.map((i) => (
              <li key={i.name}>
                {t(i.name)}
                {i.detail && <span>{t(i.detail)}</span>}
              </li>
            ))}
          </ul>
        </div>
      </section>

      <section className={styles.section} aria-labelledby="landing-safe">
        <div className={styles.box}>
          <div className={styles.head}>
            <h2 id="landing-safe">{t("landing.safe.title")}</h2>
          </div>
          <dl className={styles.facts}>
            {(investigationsOffered() ? safetyActing : safety).map(([term, body]) => (
              <div key={term} className={styles.pair}>
                <dt>{t(term)}</dt>
                <dd>{t(body)}</dd>
              </div>
            ))}
          </dl>
        </div>
      </section>

      <div className={styles.close}>
        <p>{t("landing.close")}</p>
      </div>
    </div>
  );
}
