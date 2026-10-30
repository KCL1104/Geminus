// The create-model templates the packs offer (http.schema.json CreateModel,
// one alternative per domain), grouped by domain in the order the project
// page lists them: the P0 electronics line, the production and logistics
// pack's four (H-045), then the software delivery example. Each template's
// name and one-line description are in the catalogs (template.<id>.*);
// the domain's name is the pack's (domain.<id>.name). A test keeps this
// list equal to the contract's, so a template the contract adds shows here.
import type { CreateModel } from "../../lib/contracts";

export type TemplateDomain = CreateModel["domain"];
/** A domain's templates, as the contract pairs them. */
export type TemplateOf<D extends TemplateDomain> = Extract<CreateModel, { domain: D }>["template"];
export type Template = CreateModel["template"];

type Group = { [D in TemplateDomain]: { domain: D; templates: readonly TemplateOf<D>[] } }[TemplateDomain];

export const TEMPLATE_GROUPS: readonly Group[] = [
  { domain: "electronics_line", templates: ["electronics_assembly_test_line"] },
  { domain: "production_logistics", templates: ["shoe_factory", "food_plant", "carton_plant", "warehouse"] },
  { domain: "software_delivery", templates: ["default_delivery_pipeline"] },
];

const domains = new Map<Template, TemplateDomain>(TEMPLATE_GROUPS.flatMap((g) => g.templates.map((t): [Template, TemplateDomain] => [t, g.domain])));

/** The template the form starts on: the first one listed. */
export const FIRST_TEMPLATE: Template = TEMPLATE_GROUPS[0]!.templates[0]!;

/** The create-model request for a template, its domain the one the contract pairs it with. */
export function createRequest(template: Template, name: string, locale: CreateModel["locale"]): CreateModel {
  const domain = domains.get(template);
  if (domain === undefined) throw new Error(`not a template: ${template}`);
  // The pair is the contract's (TEMPLATE_GROUPS is typed against it).
  return { name, domain, template, locale } as CreateModel;
}

export const isTemplate = (id: string): id is Template => domains.has(id as Template);

export const templateName = (t: Template) => `template.${t}.name` as const;
export const templateDescription = (t: Template) => `template.${t}.description` as const;
export const domainName = (d: TemplateDomain) => `domain.${d}.name` as const;
