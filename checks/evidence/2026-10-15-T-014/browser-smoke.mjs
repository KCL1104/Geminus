import { chromium } from "playwright-core";
const base = process.argv[2];
const browser = await chromium.launch({ executablePath: "/opt/pw-browsers/chromium" });
const page = await browser.newPage();
const external = [];
const errors = [];
page.on("request", (r) => { if (!r.url().startsWith(base)) external.push(r.url()); });
page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
page.on("pageerror", (e) => errors.push(String(e)));
page.on("response", (r) => { if (r.status() >= 400) errors.push(`${r.status()} ${r.url()}`); });
const out = {};
for (const path of ["/", "/projects", "/projects/prj_01M4YDQK00TA00000000000001", "/models/mdl_01M4YDQK004AGVNEGJQJ1NMM0N", "/models/not-an-id"]) {
  await page.goto(base + path, { waitUntil: "networkidle" });
  out[path] = { h: await page.locator("h1, h2").allInnerTexts(), alert: await page.locator('[role="alert"]').count() };
}
console.log(JSON.stringify({ out, external, errors }, null, 1));
await browser.close();
