// Seeds the demo's what-if scenarios (deploy/demo/whatifs.json) on a model's
// latest version through the product's own API, signed in as an editor
// through the BFF in a browser: each is a manual scenario (T-028) whose patch
// tests the ID and the old value of everything it replaces (D-014), so a
// model that differs from the template refuses it instead of changing
// something else. A scenario whose name is already on the version is
// skipped, so the script can run again. With --run it then presses "Run
// baseline and scenario" on each scenario's page, as a person would, and
// waits for the results page. Nothing here asks the AI.
//
// Usage (the password only from the environment, never an argument):
//   WEB=https://… ORG=org_… MODEL=mdl_… EDITOR_EMAIL=… EDITOR_PASSWORD=… \
//     node deploy/demo/seed-whatifs.cjs [--run]
// Needs Playwright with Chromium (apps/web-e2e's).
const { readFileSync } = require("node:fs");
const { join } = require("node:path");


const spec = JSON.parse(readFileSync(join(__dirname, "whatifs.json"), "utf8"));

/** The JSON Patch for one scenario's changes on ir: a test of each target's ID and old value before every replace. */
function patchFor(ir, changes) {
  const ops = [];
  for (const c of changes) {
    const [list, id] = c.resource ? ["resources", c.resource] : ["nodes", c.node];
    const at = ir[list].findIndex((x) => x.id === id);
    if (at < 0) throw new Error(`no ${list} ${id} in the model`);
    ops.push({ op: "test", path: `/${list}/${at}/id`, value: id });
    // Expand "*" over the array it names.
    let paths = [[]];
    let node = ir[list][at];
    for (const key of c.path) {
      if (key === "*") {
        paths = paths.flatMap((p) => node.map((_, i) => [...p, String(i)]));
        node = node[0];
      } else {
        paths = paths.map((p) => [...p, key]);
        node = node?.[key];
      }
    }
    for (const p of paths) {
      const path = `/${list}/${at}/${p.join("/")}`;
      ops.push({ op: "test", path, value: c.from }, { op: "replace", path, value: c.to });
    }
  }
  return ops;
}

async function main() {
  const { WEB, ORG, MODEL, EDITOR_EMAIL, EDITOR_PASSWORD } = process.env;
  if (!WEB || !ORG || !MODEL || !EDITOR_EMAIL || !EDITOR_PASSWORD) throw new Error("set WEB, ORG, MODEL, EDITOR_EMAIL and EDITOR_PASSWORD");
  const { chromium } = require(join(__dirname, "../../apps/web-e2e/node_modules/@playwright/test"));
  const browser = await chromium.launch();
  const page = await browser.newPage();
  await page.goto(WEB + "/auth/login");
  await page.waitForURL(/auth0\.com/, { timeout: 30000 });
  const user = page.locator("input[name=username]");
  await user.fill(EDITOR_EMAIL);
  const pw = page.locator("input[name=password]");
  if (!(await pw.isVisible().catch(() => false))) {
    await user.press("Enter");
    await pw.waitFor({ state: "visible", timeout: 30000 });
  }
  await pw.fill(EDITOR_PASSWORD);
  await pw.press("Enter");
  await page.waitForURL((u) => !u.hostname.endsWith("auth0.com"), { timeout: 45000 });

  const api = (method, path, body) =>
    page.evaluate(
      async ([method, path, body, org, key]) => {
        const headers = { "X-Organization-ID": org, "X-Geminus-Request": "1" };
        if (body !== null) Object.assign(headers, { "Content-Type": "application/json", "Idempotency-Key": key });
        const r = await fetch("/api/v1" + path, { method, headers, body: body === null ? undefined : JSON.stringify(body) });
        return { status: r.status, body: await r.json().catch(() => null) };
      },
      [method, path, body ?? null, ORG, `key-${crypto.randomUUID()}`],
    );

  const model = await api("GET", `/models/${MODEL}`);
  if (model.status !== 200) throw new Error(`model: ${model.status}`);
  const versionId = model.body.latest_version.id;
  const version = await api("GET", `/model-versions/${versionId}`);
  const existing = await api("GET", `/model-versions/${versionId}/scenarios`);
  const have = new Map((existing.body.items ?? []).map((s) => [s.name, s.id]));
  console.log(`model ${model.body.name}, version ${version.body.version}`);

  const ids = [];
  for (const s of spec.scenarios) {
    if (have.has(s.name)) {
      console.log(`  kept    ${s.name} (${have.get(s.name)})`);
      ids.push([s.name, have.get(s.name)]);
      continue;
    }
    const r = await api("POST", `/model-versions/${versionId}/scenarios`, { name: s.name, patch: patchFor(version.body.ir, s.changes) });
    if (r.status !== 201 && r.status !== 200) throw new Error(`${s.name}: ${r.status} ${JSON.stringify(r.body).slice(0, 400)}`);
    console.log(`  created ${s.name} (${r.body.id})`);
    ids.push([s.name, r.body.id]);
  }

  if (process.argv.includes("--run")) {
    for (const [name, id] of ids) {
      await page.goto(`${WEB}/scenarios/${id}`);
      await page.getByRole("button", { name: "Run baseline and scenario" }).click();
      await page.waitForURL(/\/results\?/, { timeout: 60000 });
      await page.locator("table").first().waitFor({ timeout: 15 * 60000 });
      console.log(`  ran     ${name}: ${page.url().replace(WEB, "")}`);
    }
  }
  await browser.close();
}

module.exports = { patchFor, spec };
if (require.main === module) {
  main().catch((e) => {
    console.error("ERROR", e.message.split("\n")[0]);
    process.exit(1);
  });
}
