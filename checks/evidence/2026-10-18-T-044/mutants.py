#!/usr/bin/env python3
"""T-044 mutation run: bilingual UI foundation (REQ-022).

Each mutant weakens one piece of T-044 and must make a check fail.

Go mutants are applied with `go test -overlay` to the working tree (which
is never modified) and run ./internal/domainpack/... and
./internal/model/validate/...; KILLED means a test failed. Finding sites:
every c.fail("templates/...") call of conformance.go becomes a no-op in
turn, so each templates check is shown to be the one a broken pack trips.

Copy mutants change a file that a test reads at run time (a schema, a
names file, Go source parsed by a test) or a frontend file. They are
applied to a scratch copy of the repository (no .git, node_modules
symlinked), one at a time, and restored afterwards. A frontend mutant is
KILLED when `vitest run`, `eslint` or `tsc --noEmit` fails (the stages of
make frontend-test), in that order; the log says which.

EQUIVALENT lists mutants that cannot change behaviour, with the reason;
they are run anyway and must survive (a kill would mean the reason is
wrong).

Usage: python3 checks/evidence/2026-10-18-T-044/mutants.py [--check] [--only=NAME ...] [repo-root]
--check only confirms that every pattern occurs exactly once.
"""
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile

ONLY = {a.split("=", 1)[1] for a in sys.argv[1:] if a.startswith("--only=")}
ARGS = [a for a in sys.argv[1:] if a != "--check" and not a.startswith("--only=")]
CHECK_ONLY = "--check" in sys.argv[1:]
REPO = pathlib.Path(ARGS[0] if ARGS else pathlib.Path(__file__).resolve().parents[3])
GO_PKGS = ["./internal/domainpack/...", "./internal/model/validate/..."]
ENV = dict(os.environ, GOTOOLCHAIN="go1.27.2")

CONF = "internal/domainpack/conformance/conformance.go"
TMPL = "internal/domainpack/softwaredelivery/template.go"
DP = "internal/domainpack/domainpack.go"
NAMES = "internal/domainpack/softwaredelivery/templates/default_delivery_pipeline.zh-Hant.names.json"
SCHEMA = "schemas/common.schema.json"
CODES = "internal/model/validate/codes.go"
W = "apps/web/"

# (name, file, old, new)
GO = [
    # conformance: the templates step
    ("tp-not-checked", CONF, "\ttp, ok := c.s.Pack.(domainpack.TemplateProvider)\n\tif !ok {", "\ttp, ok := c.s.Pack.(domainpack.TemplateProvider)\n\tif true || !ok {"),
    ("list-empty-ok", CONF, "if len(names) == 0 || slices.Contains(names, \"\") ||", "if slices.Contains(names, \"\") ||"),
    ("list-blank-ok", CONF, "if len(names) == 0 || slices.Contains(names, \"\") ||", "if len(names) == 0 ||"),
    ("list-dupes-ok", CONF, "|| len(slices.Compact(slices.Sorted(slices.Values(names)))) != len(names) {", "|| false {"),
    ("list-panic-continues", CONF, "\tif !c.call(\"templates/list\", func() { names = tp.Templates() }) {\n\t\treturn\n\t}", "\tif !c.call(\"templates/list\", func() { names = tp.Templates() }) {\n\t\tnames = nil\n\t}"),
    ("valid-domain-unchecked", CONF, "if ir.Domain != c.s.Pack.ID() || ir.ModelID", "if ir.ModelID"),
    ("valid-modelid-unchecked", CONF, "|| ir.ModelID != templateModelID ||", "||"),
    ("valid-name-unchecked", CONF, "|| ir.Name != modelName {", "{"),
    ("valid-generic-skipped", CONF, "if issues := genericIssues(ir); len(issues) > 0 {\n\t\t\t\tc.fail(\"templates/valid\"", "if issues := genericIssues(ir); false && len(issues) > 0 {\n\t\t\t\tc.fail(\"templates/valid\""),
    ("valid-warning-fails", CONF, "slices.ContainsFunc(domain, isError) {\n\t\t\t\t\tc.fail(\"templates/valid\"", "len(domain) > 0 {\n\t\t\t\t\tc.fail(\"templates/valid\""),
    ("valid-pack-rules-skipped", CONF, "&& slices.ContainsFunc(domain, isError) {\n\t\t\t\t\tc.fail(\"templates/valid\"", "&& false && slices.ContainsFunc(domain, isError) {\n\t\t\t\t\tc.fail(\"templates/valid\""),
    ("valid-nil-ok", CONF, "if err != nil || ir == nil {\n\t\t\t\tc.fail(\"templates/valid\"", "if err != nil {\n\t\t\t\tc.fail(\"templates/valid\""),
    ("valid-error-ok", CONF, "if err != nil || ir == nil {\n\t\t\t\tc.fail(\"templates/valid\"", "if ir == nil {\n\t\t\t\tc.fail(\"templates/valid\""),
    ("deterministic-skipped", CONF, "ok && (err != nil || again == nil || encoded(again) != encoded(ir)) {", "ok && false && (err != nil || again == nil || encoded(again) != encoded(ir)) {"),
    ("locales-compared-with-names", CONF, "if s := withoutNames(ir); first == \"\" {", "if s := encoded(ir); first == \"\" {"),
    ("locales-skipped", CONF, "} else if s != first {", "} else if false && s != first {"),
    ("one-locale-only", CONF, "\t\tfor _, locale := range domainpack.Locales {\n\t\t\tir, err, ok := get(", "\t\tfor _, locale := range domainpack.Locales[:1] {\n\t\t\tir, err, ok := get("),
    ('bad-locales-none', CONF, 'range []domainpack.Locale{"", "fr", "EN", "zh-hant", "zh-Hans", "zh", "zh-TW", "zh-Hant-TW", "en-US", "en-GB"} {', 'range []domainpack.Locale{} {'),
    ('bad-locales-no-regions', CONF, 'range []domainpack.Locale{"", "fr", "EN", "zh-hant", "zh-Hans", "zh", "zh-TW", "zh-Hant-TW", "en-US", "en-GB"} {', 'range []domainpack.Locale{"", "fr", "EN", "zh-hant", "zh-Hans"} {'),
    ("unknown-locale-model-ok", CONF, "get(\"templates/unknown-locale\", name, bad); ok && (err == nil || ir != nil) {", "get(\"templates/unknown-locale\", name, bad); ok && (err == nil || false && ir != nil) {"),
    ('unknown-template-model-ok', CONF, 'get("templates/unknown-template", name, domainpack.Locales[0]); ok && (err == nil || ir != nil) {', 'get("templates/unknown-template", name, domainpack.Locales[0]); ok && (err == nil || false && ir != nil) {'),
    ('unknown-template-no-variants', CONF, 'for _, v := range []string{strings.ToUpper(name), strings.ToLower(name), " " + name, name + " "} {', 'for _, v := range []string{strings.ToUpper(name)}[:0] {'),
    ('copies-unchecked', CONF, '\t\t\t\tif encoded(ir) != before {', '\t\t\t\tif false && encoded(ir) != before {'),
    ('copies-shallow', CONF, '\t\t\t\tfor i := range other.Resources {', '\t\t\t\tfor i := range other.Resources[:0] {'),
    ('copies-call-skipped', CONF, '&& other != nil {\n\t\t\t\tconst changed', '&& other != nil && false {\n\t\t\t\tconst changed'),
    ('concurrent-no-template', CONF, '\t\t\tnames := tp.Templates()\n\t\t\tif len(names) == 0 {', '\t\t\tnames := tp.Templates()\n\t\t\tif true || len(names) == 0 {'),
    ("templates-step-dropped", CONF, "{\"templates\", c.templates},", ""),
    # softwaredelivery: locales and names
    ("unknown-locale-accepted", TMPL, "if !slices.Contains(domainpack.Locales, locale) {\n\t\treturn nil, ErrUnknownLocale", "if false && !slices.Contains(domainpack.Locales, locale) {\n\t\treturn nil, ErrUnknownLocale"),
    ("not-localized", TMPL, "\tif err := localize(read, &ir, templateName, locale); err != nil {\n\t\treturn nil, err\n\t}", "\tif err := localize(read, &ir, templateName, domainpack.English); err != nil {\n\t\treturn nil, err\n\t}"),
    ("localize-error-ignored", TMPL, "\tif err := localize(read, &ir, templateName, locale); err != nil {\n\t\treturn nil, err\n\t}", "\t_ = localize(read, &ir, templateName, locale)"),
    ("english-also-read", TMPL, "\tif locale == domainpack.English {\n\t\treturn nil\n\t}\n\traw, err", "\tif false && locale == domainpack.English {\n\t\treturn nil\n\t}\n\traw, err"),
    ("unknown-members-ok", TMPL, "if err := json.Unmarshal(raw, &names, json.RejectUnknownMembers(true)); err != nil {", "if err := json.Unmarshal(raw, &names); err != nil {"),
    ('missing-name-kept', TMPL, '\t\tif !ok || strings.TrimSpace(n) == "" {', '\t\tif false && (!ok || strings.TrimSpace(n) == "") {'),
    ('blank-name-ok', TMPL, '\t\tif !ok || strings.TrimSpace(n) == "" {', '\t\tif !ok || n == "" {'),
    ('control-name-ok', TMPL, 'if strings.ContainsFunc(n, unicode.IsControl) || utf8', 'if false && strings.ContainsFunc(n, unicode.IsControl) || utf8'),
    ('long-name-ok', TMPL, '|| utf8.RuneCountInString(n) > maxNameLength {', '|| false {'),
    ('long-name-off-by-one', TMPL, '|| utf8.RuneCountInString(n) > maxNameLength {', '|| utf8.RuneCountInString(n) >= maxNameLength {'),
    ("extra-name-ok", TMPL, "\t\tif len(used) != len(c.from) {", "\t\tif false && len(used) != len(c.from) {"),
    ("unnamed-gets-name", TMPL, "\t\tif *name == \"\" {\n\t\t\treturn nil // unnamed", "\t\tif false && *name == \"\" {\n\t\t\treturn nil // unnamed"),
    ("pack-templates-empty", TMPL, "func (Pack) Templates() []string { return Templates() }", "func (Pack) Templates() []string { return nil }"),
    ("pack-template-ignores-locale", TMPL, "\treturn Template(templateName, modelID, name, locale)\n}", "\treturn Template(templateName, modelID, name, domainpack.English)\n}"),
    # domainpack
    ("locales-default-not-first", DP, "var Locales = []Locale{English, TraditionalChinese}", "var Locales = []Locale{TraditionalChinese, English}"),
    ("locales-only-english", DP, "var Locales = []Locale{English, TraditionalChinese}", "var Locales = []Locale{English}"),
]

# (name, file, old, new, kind) where kind is "go" (run GO_PKGS in the copy) or "web"
COPY = [
    ("names-missing-resource", NAMES, "\n    \"ci_runner\": \"CI 執行器\",", "", "go"),
    ("names-extra-node", NAMES, "\"nodes\": {}", "\"nodes\": {\"review\": \"審查\"}", "go"),
    ("names-english-copy", NAMES, "\"reviewer\": \"審查者\"", "\"reviewer\": \"Reviewer\"", "go"),
    ("contract-code-missing", SCHEMA, "\n        \"UNREACHABLE_NODE\",", "", "go"),
    ("contract-locale-missing", SCHEMA, "\"enum\": [\n        \"en\",\n        \"zh-Hant\"\n      ]", "\"enum\": [\n        \"en\"\n      ]", "both"),
    ("code-not-in-contract", CODES, "\tDatasetBindingInvalid      = \"DATASET_BINDING_INVALID\"", "\tDatasetBindingInvalid      = \"DATASET_BINDING_INVALID\"\n\tNewCode                    = \"NEW_CODE\"", "go"),
    # translator and formatting
    ("plural-always-other", W + "lib/i18n/translate.ts", "text = (plural.select(count) === \"one\" ? m.one : undefined) ?? m.other;", "text = m.other;", "web"),
    ("plural-count-unchecked", W + "lib/i18n/translate.ts", "if (typeof count !== \"number\") throw", "if (false) throw", "web"),
    ("missing-param-shown", W + "lib/i18n/translate.ts", "if (v === undefined) throw new Error(`message ${key} has no value for {${name}}`);", "if (v === undefined) return `{${name}}`;", "web"),
    ("numbers-unformatted", W + "lib/i18n/translate.ts", "return typeof v === \"number\" ? numbers.format(v) : v;", "return String(v);", "web"),
    ('date-shape-unchecked', W + 'lib/i18n/translate.ts', 'if (m === null || !exists(m) || Number.isNaN(d.getTime()))', 'if (Number.isNaN(d.getTime()))', 'web'),
    ('date-invalid-unchecked', W + 'lib/i18n/translate.ts', 'if (m === null || !exists(m) || Number.isNaN(d.getTime()))', 'if (m === null || !exists(m))', 'web'),
    ('date-calendar-unchecked', W + 'lib/i18n/translate.ts', 'if (m === null || !exists(m) || Number.isNaN(d.getTime()))', 'if (m === null || Number.isNaN(d.getTime()))', 'web'),
    ('date-month-length-ignored', W + 'lib/i18n/translate.ts', 'const days = new Date(Date.UTC(year!, month!, 0)).getUTCDate();', 'const days = 31;', 'web'),
    ('date-hour-24', W + 'lib/i18n/translate.ts', 'hour! <= 23', 'hour! <= 24', 'web'),
    ('date-case-strict', W + 'lib/i18n/translate.ts', '(?:Z|[+-](\\d{2}):(\\d{2}))$/i;', '(?:Z|[+-](\\d{2}):(\\d{2}))$/;', 'web'),
    ('params-inherited', W + 'lib/i18n/translate.ts', 'const v = Object.hasOwn(params, name) ? params[name] : undefined;', 'const v = params[name];', 'web'),
    ('date-nonstring-shown', W + 'lib/i18n/provider.tsx', '      if (typeof iso !== "string") return "";\n', '', 'web'),
    ('gap-zh-space', W + 'lib/i18n/provider.tsx', 'gap: locale === "zh-Hant" ? "" : " ",', 'gap: " ",', 'web'),
    ('remembered-locale-default', W + 'lib/i18n/provider.tsx', '() => cookieLocale(document.cookie), () => defaultLocale);', '() => defaultLocale, () => defaultLocale);', 'web'),
    ('cookie-last-wins', W + 'lib/i18n/locale.ts', 'for (const pair of header.split(";"))', 'for (const pair of header.split(";").reverse())', 'web'),
    ('cookie-name-suffix', W + 'lib/i18n/locale.ts', 'pair.slice(0, at).trim() === localeCookie', 'pair.slice(0, at).trim().endsWith(localeCookie)', 'web'),
    ("zh-catalog-is-en", W + "lib/i18n/translate.ts", "{ en, \"zh-Hant\": zhHant }", "{ en, \"zh-Hant\": en }", "web"),
    ("duration-short", W + "lib/i18n/translate.ts", "unitDisplay: \"long\"", "unitDisplay: \"short\"", "web"),
    # locale choice
    ("locale-case-insensitive", W + "lib/i18n/locale.ts", "locales.find((l) => l === value)", "locales.find((l) => l.toLowerCase() === value?.toLowerCase())", "web"),
    ("locale-prefix", W + "lib/i18n/locale.ts", "locales.find((l) => l === value)", "locales.find((l) => value?.startsWith(l))", "web"),
    ("default-zh", W + "lib/i18n/locale.ts", "export const defaultLocale: Locale = \"en\";", "export const defaultLocale: Locale = \"zh-Hant\";", "web"),
    ("cookie-never-secure", W + "lib/i18n/locale.ts", "${secure ? \"; Secure\" : \"\"}", "", "web"),
    ("cookie-no-samesite", W + "lib/i18n/locale.ts", "; SameSite=Lax", "", "web"),
    ("cookie-session-only", W + "lib/i18n/locale.ts", "; Max-Age=31536000", "", "web"),
    ("server-ignores-cookie", W + "lib/i18n/server.ts", "return parseLocale((await cookies()).get(localeCookie)?.value);", "void (await cookies());\n  return parseLocale(undefined);", "web"),
    # provider
    ("provider-date-throws", W + "lib/i18n/provider.tsx", "      } catch {\n        return iso;\n      }", "      } catch (e) {\n        throw e;\n      }", "web"),
    ("provider-ignores-locale", W + "lib/i18n/provider.tsx", "const value = useMemo(() => i18nFor(locale), [locale]);", "const value = useMemo(() => i18nFor(defaultLocale), []);", "web"),
    ("context-default-zh", W + "lib/i18n/provider.tsx", "createContext<I18n>(i18nFor(defaultLocale))", "createContext<I18n>(i18nFor(\"zh-Hant\"))", "web"),
    # problem view
    ("code-not-explained", W + "app/_components/problem-view.tsx", "return p.code !== null ? `error.${p.code}` : guidance[p.kind];", "return guidance[p.kind];", "web"),
    ("kind-guidance-dropped", W + "app/_components/problem-view.tsx", "return p.code !== null ? `error.${p.code}` : guidance[p.kind];", "return p.code !== null ? `error.${p.code}` : undefined;", "web"),
    ("validation-never-known", W + "app/_components/problem-view.tsx", "return code !== undefined && Object.hasOwn(en, key) ? (key as MessageKey) : null;", "return null;", "web"),
    ("validation-any-key", W + "app/_components/problem-view.tsx", "return code !== undefined && Object.hasOwn(en, key) ? (key as MessageKey) : null;", "return code !== undefined ? (key as MessageKey) : null;", "web"),
    ("message-unmarked", W + "app/_components/problem-view.tsx", "<span lang=\"en\">{problem.message}</span>", "<span>{problem.message}</span>", "web"),
    ("reason-unmarked", W + "app/_components/problem-view.tsx", "<span lang=\"en\">{d.reason}</span>", "<span>{d.reason}</span>", "web"),
    ("not-found-echoes", W + "app/_components/problem-view.tsx", "const shown = problem.kind !== \"not_found\";", "const shown = true;", "web"),
    ("retry-after-count", W + "app/_components/problem-view.tsx", "t(\"problem.retryAfter\", { count: problem.retryAfterSeconds })", "t(\"problem.retryAfter\", { count: 2 })", "web"),
    # states
    ('pending-hidden', W + 'app/_components/states.tsx', '{pending > 0 && (', '{false && (', 'web'),
    ('revision-wrong', W + 'app/_components/states.tsx', '{ revision: String(session.remote.revision) }', '{ revision: "0" }', 'web'),
    ('revision-grouped', W + 'app/_components/states.tsx', '{ revision: String(session.remote.revision) }', '{ revision: session.remote.revision }', 'web'),
    ('status-no-gap', W + 'app/_components/states.tsx', '{t(`draft.status.${session.status}`)}\n        {gap}\n', '{t(`draft.status.${session.status}`)}\n', 'web'),
    ('technical-space', W + 'app/_components/problem-view.tsx', '{t("problem.technical")}\n          {gap}', '{t("problem.technical")}\n          {" "}', 'web'),
    ("more-pages-count", W + "app/_components/states.tsx", "t(`morePages.${what}`, { count: shown })", "t(`morePages.${what}`, { count: 2 })", "web"),
    ("loading-fixed", W + "app/_components/states.tsx", "{t(`loading.${what}`)}", "{t(\"loading.projects\")}", "web"),
    ("read-only-swapped", W + "app/_components/states.tsx", "t(role === null ? \"readOnly.unknownRole\" : \"readOnly.viewer\")", "t(role === null ? \"readOnly.viewer\" : \"readOnly.unknownRole\")", "web"),
    # switcher, shell and pages
    ("switch-no-cookie", W + "app/_components/locale-switcher.tsx", "document.cookie = localeCookieValue(parseLocale(e.target.value), location.protocol === \"https:\");", "void e;", "web"),
    ("switch-no-refresh", W + "app/_components/locale-switcher.tsx", "          onChanged();\n", "", "web"),
    ("menu-no-refresh", W + "app/_components/locale-switcher.tsx", "onChanged={() => router.refresh()}", "onChanged={() => void router}", "web"),
    ("switch-shows-codes", W + "app/_components/locale-switcher.tsx", "{t(`locale.${l}`)}", "{l}", "web"),
    ("switch-not-current", W + "app/_components/locale-switcher.tsx", "defaultValue={locale}", "defaultValue=\"en\"", "web"),
    ("switch-unlabelled", W + "app/_components/locale-switcher.tsx", "<label htmlFor={id}>{t(\"locale.label\")}</label>", "<span>{t(\"locale.label\")}</span>", "web"),
    ("html-lang-fixed", W + "app/layout.tsx", "<html lang={locale}>", "<html lang=\"en\">", "web"),
    ("shell-provider-en", W + "app/layout.tsx", "<I18nProvider locale={locale}>", "<I18nProvider locale=\"en\">", "web"),
    ("shell-nav-en", W + "app/layout.tsx", "const t = createTranslator(locale);", "const t = createTranslator(\"en\");", "web"),
    ("home-en", W + "app/page.tsx", "const t = createTranslator(await requestLocale());", "const t = createTranslator(\"en\");", "web"),
    ('created-raw', W + 'app/projects/page.tsx', '{created && <> · {t("projects.created", { date: created })}</>}', '<> · {t("projects.created", { date: String(p.created_at) })}</>', 'web'),
    ('created-hidden', W + 'app/projects/page.tsx', '{created && <> · {t("projects.created", { date: created })}</>}', '', 'web'),
    ('not-found-en', W + 'app/not-found.tsx', '  const t = createTranslator(await requestLocale());', '  const t = createTranslator("en");', 'web'),
    ('not-found-title-en', W + 'app/not-found.tsx', 'return { title: createTranslator(await requestLocale())("notFound.title") };', 'return { title: createTranslator("en")("notFound.title") };', 'web'),
    ('crash-shows-message', W + 'app/_components/crash.tsx', '<p>{t("crash.body")}</p>', '<p>{error.message}</p>', 'web'),
    ('crash-no-reference', W + 'app/_components/crash.tsx', '{error.digest && <p>', '{false && error.digest && <p>', 'web'),
    ('crash-no-retry', W + 'app/_components/crash.tsx', 'onClick={() => retry()}', 'onClick={() => {}}', 'web'),
    # lint rule
    ("lint-no-strings-off", W + "eslint.config.mjs", "noStrings: true", "noStrings: false", "web"),
    ("lint-props-checked", W + "eslint.config.mjs", "ignoreProps: true", "ignoreProps: false", "web"),
    ('lint-attr-literal', W + 'eslint.config.mjs', '        { selector: `JSXAttribute[name.name=${textAttributes}] > Literal[value=${letters}]`, message: catalogText },\n', '', 'web'),
    ('lint-attr-paths', W + 'eslint.config.mjs', '  ...through(`JSXAttribute[name.name=${textAttributes}] > JSXExpressionContainer`, 0, 3),\n', '', 'web'),
    ('lint-attr-depth-0', W + 'eslint.config.mjs', 'JSXExpressionContainer`, 0, 3),', 'JSXExpressionContainer`, 1, 3),', 'web'),
    ('lint-child-paths', W + 'eslint.config.mjs', '  ...through(child, 1, 3),\n', '', 'web'),
    ('lint-child-depth-1', W + 'eslint.config.mjs', '  ...through(child, 1, 3),', '  ...through(child, 1, 1),', 'web'),
    ('lint-callback-paths', W + 'eslint.config.mjs', '  ...through(`${child} CallExpression > ArrowFunctionExpression`, 0, 3),\n', '', 'web'),
    ('lint-no-concatenation', W + 'eslint.config.mjs', 'BinaryExpression[operator="+"], ', '', 'web'),
    ('lint-any-binary', W + 'eslint.config.mjs', 'BinaryExpression[operator="+"]', 'BinaryExpression', 'web'),
    ('lint-no-cast', W + 'eslint.config.mjs', 'TSAsExpression, ', '', 'web'),
    ('lint-no-array', W + 'eslint.config.mjs', ", ArrayExpression)';", ")';", 'web'),
    ('lint-condition-any-path', W + 'eslint.config.mjs', 'from + ` > ${passThrough}`.repeat(min + i)', 'from + ` ${passThrough}`.repeat(min + i)', 'web'),
    ('lint-latin-only', W + 'eslint.config.mjs', 'const letters = "/\\\\p{L}/u";', 'const letters = "/[A-Za-z]/";', 'web'),
    ('lint-no-text-props', W + 'eslint.config.mjs', '|label|text|message|description|heading|caption|tooltip|hint|helperText|errorMessage)$/', '|label)$/', 'web'),
    ('lint-submit-value', W + 'eslint.config.mjs', 'JSXOpeningElement[name.name="input"]:has(', 'JSXOpeningElement[name.name="inputx"]:has(', 'web'),
    ('lint-object-props', W + 'eslint.config.mjs', 'JSXAttribute > JSXExpressionContainer > ObjectExpression > Property', 'JSXAttribute > JSXExpressionContainer > ObjectExpression > ObjectExpression > Property', 'web'),
    ('lint-document-title', W + 'eslint.config.mjs', '[object.name="document"][property.name="title"]', '[object.name="documentx"][property.name="title"]', 'web'),
    ('lint-dialog-globals', W + 'eslint.config.mjs', '"no-restricted-globals": ["error",', '"no-restricted-globals": ["off",', 'web'),
    ('lint-dialog-properties', W + 'eslint.config.mjs', '"no-restricted-properties": ["error",', '"no-restricted-properties": ["off",', 'web'),
    ("lint-attr-title-missing", W + "eslint.config.mjs", "|title|alt|", "|alt|", "web"),
    ("lint-condition-literal", W + "eslint.config.mjs", "          { selector: `${path} > Literal[value=${letters}]`, message: catalogText },\n", "", "web"),
    ("lint-condition-template", W + "eslint.config.mjs", "          { selector: `${path} > TemplateLiteral`, message: catalogText },\n", "", "web"),
    ("lint-condition-no-fragment", W + "eslint.config.mjs", ":matches(JSXElement, JSXFragment) > JSXExpressionContainer", "JSXElement > JSXExpressionContainer", "web"),
]

EQUIVALENT = {
    "list-panic-continues": "after a Templates() panic the pack already fails (templates/list panicked); going on with no names only adds the unknown-template probe, which can add a finding but never remove one",
    "date-invalid-unchecked": "a string that passes the shape and calendar checks is a valid Date, and Intl.DateTimeFormat.format would throw RangeError on an Invalid Date anyway",
}


def fail_sites(src):
    """Start offsets of every c.fail("templates/...") call."""
    return [m.start() for m in re.finditer(r'c\.fail\("templates/', src)]


def call_end(src, start):
    depth = 0
    i = src.index("(", start)
    while True:
        ch = src[i]
        if ch == "(":
            depth += 1
        elif ch == ")":
            depth -= 1
            if depth == 0:
                return i + 1
        elif ch == '"':
            i += 1
            while src[i] != '"':
                i += 2 if src[i] == "\\" else 1
        i += 1


def noop_site(src, start):
    end = call_end(src, start)
    return src[:start] + "if false {\n" + src[start:end] + "\n}" + src[end:]


def go_overlay(tmp, i, f, src):
    m = pathlib.Path(tmp) / f"m{i}.go"
    m.write_text(src)
    ov = pathlib.Path(tmp) / f"m{i}.json"
    ov.write_text(json.dumps({"Replace": {str(REPO / f): str(m)}}))
    p = subprocess.run(["go", "test", "-count=1", "-timeout", "600s", "-overlay", str(ov), *GO_PKGS],
                       cwd=REPO, capture_output=True, text=True, env=ENV)
    return verdict(p, "go test")


# A mutant that does not build, an ESLint config ESLint refuses, or a test
# that ran out of time proves nothing about the code: each is a harness
# error, never a kill (T-044 review L1, L2).
HARNESS_ERRORS = {
    "BUILD-FAILED": ("build failed", "setup failed"),
    "CONFIG-INVALID": ("Configuration for rule", "should NOT have duplicate items", "Failed to load config", "ConfigError"),
    "TIMEOUT": ("Test timed out", "panic: test timed out", "Hook timed out"),
}


def verdict(p, stage):
    o = p.stdout + p.stderr
    if p.returncode == 0:
        return "SURVIVED", ""
    for kind, marks in HARNESS_ERRORS.items():
        if any(m in o for m in marks):
            return kind, o[-300:].replace("\n", " ")
    first = next((l.strip() for l in o.splitlines() if l.strip().startswith(("--- FAIL", "panic:", "FAIL ", "×", "error TS"))), "")
    if not first:
        first = next((l.strip() for l in o.splitlines() if " error " in l), "")
    return "KILLED", f"{stage}: {first[:110]}"


def copy_repo(tmp):
    dst = pathlib.Path(tmp) / "repo"
    shutil.copytree(REPO, dst, symlinks=True, ignore=shutil.ignore_patterns(".git", "node_modules", ".next", "bin", ".venv", "coverage", "tsconfig.tsbuildinfo"))
    (dst / "apps/web/node_modules").symlink_to(REPO / "apps/web/node_modules")
    return dst


def run_copy(dst, kind):
    web = dst / "apps/web"
    if kind in ("go", "both"):
        p = subprocess.run(["go", "test", "-count=1", "-timeout", "600s", *GO_PKGS], cwd=dst, capture_output=True, text=True, env=ENV)
        v = verdict(p, "go test")
        if v[0] != "SURVIVED" or kind == "go":
            return v
    for stage, cmd in (("vitest", ["npx", "vitest", "run"]), ("eslint", ["npx", "eslint", "--max-warnings=0", "."]), ("tsc", ["npx", "tsc", "--noEmit"])):
        p = subprocess.run(cmd, cwd=web, capture_output=True, text=True, env=ENV)
        v = verdict(p, stage)
        if v[0] != "SURVIVED":
            return v
    return "SURVIVED", ""


def wanted(name):
    return not ONLY or name in ONLY


def main() -> int:
    bad = []
    for name, f, old, new, *_ in GO + COPY:
        n = (REPO / f).read_text().count(old)
        if n != 1:
            bad.append(f"{name}: pattern occurs {n} times in {f}")
    conf = (REPO / CONF).read_text()
    sites = fail_sites(conf)
    if len(sites) < 8:
        bad.append(f"only {len(sites)} templates finding sites")
    if bad:
        print("\n".join(bad))
        return 2
    if CHECK_ONLY:
        print(f"ok: {len(GO)} go mutants, {len(COPY)} copy mutants, {len(sites)} finding sites")
        return 0
    results = []
    with tempfile.TemporaryDirectory() as tmp:
        i = 0
        for start in sites:
            line = conf.count("\n", 0, start) + 1
            name = f"site/{conf[start:start + 60].split(',')[0][8:-1]}@{line}"
            if not wanted(name):
                continue
            i += 1
            results.append((name, *go_overlay(tmp, i, CONF, noop_site(conf, start))))
            print("\t".join(results[-1]), flush=True)
        for name, f, old, new in GO:
            if not wanted(name):
                continue
            i += 1
            src = (REPO / f).read_text()
            results.append((name, *go_overlay(tmp, i, f, src.replace(old, new))))
            print("\t".join(results[-1]), flush=True)
        dst = copy_repo(tmp)
        base = run_copy(dst, "both")
        if base[0] != "SURVIVED":
            print(f"baseline copy fails: {base}")
            return 2
        for name, f, old, new, kind in COPY:
            if not wanted(name):
                continue
            path = dst / f
            orig = path.read_text()
            path.write_text(orig.replace(old, new))
            try:
                results.append((name, *run_copy(dst, kind)))
            finally:
                path.write_text(orig)
            print("\t".join(results[-1]), flush=True)
    wrong = [r for r in results if r[1] in HARNESS_ERRORS or (r[1] == "SURVIVED") != (r[0] in EQUIVALENT)]
    killed = sum(r[1] == "KILLED" for r in results)
    print(f"total={len(results)} killed={killed} equivalent={sum(r[0] in EQUIVALENT for r in results)} unexpected={len(wrong)}")
    for r in wrong:
        print("UNEXPECTED\t" + "\t".join(r))
    return 1 if wrong else 0


if __name__ == "__main__":
    sys.exit(main())
