// Real-browser check of the locale switcher against `next start` (T-044 evidence).
const { chromium } = require(process.env.PW);
(async () => {
  const browser = await chromium.launch();
  const page = await (await browser.newContext()).newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
  await page.goto("http://127.0.0.1:3917/projects");
  const before = { lang: await page.getAttribute("html", "lang"), h1: await page.textContent("h1"), select: await page.inputValue("select") };
  await page.selectOption("select", "zh-Hant");
  await page.waitForFunction(() => document.documentElement.lang === "zh-Hant", null, { timeout: 10000 });
  await page.waitForFunction(() => document.querySelector("h1")?.textContent === "專案", null, { timeout: 10000 });
  const after = { lang: await page.getAttribute("html", "lang"), h1: await page.textContent("h1"), h2: await page.textContent("h2"), url: page.url(), select: await page.inputValue("select"),
    cookie: (await page.context().cookies()).map((c) => `${c.name}=${c.value}; path=${c.path}; sameSite=${c.sameSite}; secure=${c.secure}; httpOnly=${c.httpOnly}`) };
  await page.reload();
  const reloaded = { lang: await page.getAttribute("html", "lang"), h1: await page.textContent("h1") };
  await page.selectOption("select", "en");
  await page.waitForFunction(() => document.querySelector("h1")?.textContent === "Projects", null, { timeout: 10000 });
  const back = { lang: await page.getAttribute("html", "lang"), h1: await page.textContent("h1") };
  console.log(JSON.stringify({ before, after, reloaded, back, errors }, null, 1));
  await browser.close();
})().catch((e) => { console.error(e); process.exit(1); });
