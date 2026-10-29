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
// FIRST VIEWPORT: the promise as the page's h1, the how in one paragraph,
// the one primary action under it, then the results page itself at the
// content width.
// FORM: the decision record (candidate 7 of 7; seed 7d827b6e).
import type { MessageKey, Translator } from "../../lib/i18n/translate";
import { LandingAction } from "./cta";
import styles from "./landing.module.css";
import { Shot, type ShotName } from "./shot";

type Actor = "you" | "muse" | "sam" | "search" | "geminus" | "engine";

/** Who acts in a step, as a pill: a Meta model with its provider, Geminus's server, the engine or the person. */
function ActorPill({ actor, t }: { actor: Actor; t: Translator }) {
  if (actor === "muse" || actor === "sam" || actor === "search") {
    const name = actor === "muse" ? t("ai.model.muse-spark-1.3") : actor === "sam" ? t("ai.model.sam-3.1") : t("landing.actor.search");
    return (
      <li className={styles.actor} data-kind="ai">
        <span className={styles.provider}>{t("ai.provider.meta")}</span>{" "}
        <span>{name}</span>
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
    id: "approve", title: "landing.step.approve.title", body: "landing.step.approve.body", actors: ["you"],
    shot: { name: "proposal", alt: "landing.shot.proposal.alt", caption: "landing.shot.proposal.caption" },
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
  const heading = `landing-step-${step.id}`;
  return (
    <li className={styles.step} data-layout={step.shot?.beside ? "beside" : undefined}>
      <div className={styles.stepText}>
        <h3 id={heading}>{t(step.title)}</h3>
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

const models: { name: (t: Translator) => string; body: MessageKey }[] = [
  { name: (t) => t("ai.model.muse-spark-1.3"), body: "landing.meta.muse" },
  { name: (t) => t("ai.model.sam-3.1"), body: "landing.meta.sam" },
  { name: (t) => t("landing.actor.search"), body: "landing.meta.search" },
];

const industries: { name: MessageKey; detail?: MessageKey }[] = [
  { name: "landing.industry.electronics", detail: "landing.industry.electronics.steps" },
  { name: "landing.industry.shoes" },
  { name: "landing.industry.food" },
  { name: "landing.industry.carton" },
  { name: "landing.industry.warehouse" },
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
          {models.map((m) => (
            <div key={m.body} className={styles.pair}>
              <dt>
                <span>{m.name(t)}</span>{" "}
                <span className={styles.provider}>{t("ai.provider.meta")}</span>
              </dt>
              <dd>{t(m.body)}</dd>
            </div>
          ))}
        </dl>
        <p className={styles.note}>{t("landing.meta.named")}</p>
        <Shot name="proposed" t={t} altKey="landing.shot.proposed.alt" captionKey="landing.shot.proposed.caption" />
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
