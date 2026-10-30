// The landing at / (H-048), for the hackathon's judges first and anyone who
// opens the app: what Geminus is, the loop it runs on real screens of the
// product, which Meta model does each step, the industries the templates
// cover and why it is safe to try. Only facts the product holds today
// (README.md, PRODUCT.md): no figure, customer or result beyond the
// synthetic demo, and no credential (the judges' sign-in is in the
// submission). Every string is the catalog's (REQ-022).
//
// Direction (impeccable, a Persuade surface inside the Operate product):
// THESIS: the page is the record of one what-if, from the question to the
// decision; each step names who acts (you, a Meta model, the Geminus
// server, the engine) beside the screen it happens on. It refuses the
// feature-card grid and the big-number hero.
// OWN-WORLD: the app's own system, unchanged (DESIGN.md, H-041): the
// near-white or near-black ground, cards in a hairline ring, Geist at the
// five sizes, the violet accent only for the AI, Ink for Geminus's own
// check, green only inside the screens' scenario data.
// STORY: a first-time reader learns in one view that Geminus tries a change
// on a digital twin before the real line, sees the results screen, and can
// sign in; scrolling, they follow the loop step by step, see which Meta
// model does what, the templates' industries, and the safety rules.
// FIRST VIEWPORT: the product's name and mark, the promise as the page's
// h1, the how in one paragraph, the one primary action under it, then the
// results' key metrics as a framed screenshot.
// FORM: the decision record (candidate 7 of 7; seed 7d827b6e).
import type { MessageKey, Translator } from "../../lib/i18n/translate";
import shell from "../../ui/shell.module.css";
import { templateName } from "../models/templates";
import { LandingAction } from "./cta";
import styles from "./landing.module.css";
import { Shot, type ShotName } from "./shot";

type Actor = "you" | "muse" | "sam" | "geminus" | "engine";

/** Who acts in a step, as a pill: a Meta model with its provider, Geminus's server, the engine or the person. */
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

type StepProps = {
  id: string;
  title: MessageKey;
  body: MessageKey;
  actors: Actor[];
  shot?: { name: ShotName; alt: MessageKey; caption: MessageKey; beside?: boolean };
};

const steps: StepProps[] = [
  {
    id: "describe", title: "landing.step.describe.title", body: "landing.step.describe.body", actors: ["you"],
    shot: { name: "canvas", alt: "landing.shot.canvas.alt", caption: "landing.shot.canvas.caption" },
  },
  { id: "propose", title: "landing.step.propose.title", body: "landing.step.propose.body", actors: ["muse", "sam", "geminus"] },
  {
    // Muse Spark's own proposal from the live demo (review round 1, M2): it names the model that wrote it.
    id: "approve", title: "landing.step.approve.title", body: "landing.step.approve.body", actors: ["you"],
    shot: { name: "proposed", alt: "landing.shot.proposed.alt", caption: "landing.shot.proposed.caption" },
  },
  {
    id: "simulate", title: "landing.step.simulate.title", body: "landing.step.simulate.body", actors: ["you", "engine", "geminus"],
    shot: { name: "marey", alt: "landing.shot.marey.alt", caption: "landing.shot.marey.caption" },
  },
  {
    id: "explain", title: "landing.step.explain.title", body: "landing.step.explain.body", actors: ["muse", "geminus"],
    shot: { name: "explained", alt: "landing.shot.explained.alt", caption: "landing.shot.explained.caption", beside: true },
  },
];

function Step({ step, t }: { step: StepProps; t: Translator }) {
  return (
    <li className={styles.step} data-layout={step.shot?.beside ? "beside" : undefined}>
      <div className={styles.stepText}>
        <h3>{t(step.title)}</h3>
        <p>{t(step.body)}</p>
        <ul className={styles.who} aria-label={t("landing.who")}>
          {step.actors.map((a) => (
            <ActorPill key={a} actor={a} t={t} />
          ))}
        </ul>
      </div>
      {step.shot && <Shot name={step.shot.name} t={t} altKey={step.shot.alt} captionKey={step.shot.caption} />}
    </li>
  );
}

/** Meta's models, and the one tool of the Meta Model API that Muse Spark uses (review round 1, M1: a tool, not a model). */
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

export function Landing({ t }: { t: Translator }) {
  return (
    <div className={styles.landing}>
      <section className={styles.hero} aria-labelledby="landing-title">
        <div className={styles.heroText}>
          {/* The product's name and mark (review round 1, M3): the rail shows the mark only. */}
          <p className={styles.brand}>
            <svg className={shell.mark} width="22" height="22" viewBox="0 0 20 20" aria-hidden="true" focusable="false">
              <rect x="0.75" y="0.75" width="12.5" height="12.5" rx="3.5" className={shell.markFront} />
              <rect x="6.75" y="6.75" width="12.5" height="12.5" rx="3.5" className={shell.markBack} />
            </svg>
            {t("app.name")}
          </p>
          <h1 id="landing-title">{t("landing.title")}</h1>
          <p className={styles.intro}>{t("landing.lede")}</p>
          <LandingAction />
          <p className={styles.note}>{t("landing.synthetic")}</p>
        </div>
        <Shot name="results" t={t} altKey="landing.shot.results.alt" captionKey="landing.shot.results.caption" />
      </section>

      <section className={styles.section} aria-labelledby="landing-loop">
        <h2 id="landing-loop">{t("landing.loop.title")}</h2>
        <p className={styles.intro}>{t("landing.loop.intro")}</p>
        <ol className={styles.steps}>
          {steps.map((s) => (
            <Step key={s.id} step={s} t={t} />
          ))}
        </ol>
      </section>

      <section className={styles.section} aria-labelledby="landing-meta">
        <h2 id="landing-meta">{t("landing.meta.title")}</h2>
        <p className={styles.intro}>{t("landing.meta.intro")}</p>
        <dl className={styles.facts}>
          {meta.map((m) => (
            <div key={m.body} className={styles.pair}>
              <dt>
                <span>{t(m.name)}</span>{" "}
                <span className={styles.provider}>{t(m.kind)}</span>
              </dt>
              <dd>{t(m.body)}</dd>
            </div>
          ))}
        </dl>
        <p className={styles.note}>{t("landing.meta.named")}</p>
      </section>

      <section className={styles.section} aria-labelledby="landing-industries">
        <h2 id="landing-industries">{t("landing.industries.title")}</h2>
        <p className={styles.intro}>{t("landing.industries.intro")}</p>
        <ul className={styles.industries}>
          {industries.map((i) => (
            <li key={i.name}>
              {t(i.name)}
              {i.detail && <span>{t(i.detail)}</span>}
            </li>
          ))}
        </ul>
      </section>

      <section className={styles.section} aria-labelledby="landing-safe">
        <h2 id="landing-safe">{t("landing.safe.title")}</h2>
        <dl className={styles.facts}>
          {safety.map(([term, body]) => (
            <div key={term} className={styles.pair}>
              <dt>{t(term)}</dt>
              <dd>{t(body)}</dd>
            </div>
          ))}
        </dl>
      </section>

      <p className={styles.close}>{t("landing.close")}</p>
    </div>
  );
}
