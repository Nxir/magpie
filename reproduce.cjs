// Replay identical simulated gateway responses against the PR base and head.
// No backend, account, model calls, or user configuration is used.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { execFileSync } = require("node:child_process");
const { chromium, webkit } = require("playwright");

const args = Object.fromEntries(process.argv.slice(2).map((arg) => {
  const at = arg.indexOf("=");
  assert(at > 2, "arguments use --name=value");
  return [arg.slice(2, at), arg.slice(at + 1)];
}));
const repo = path.resolve(args.repo || process.cwd());
const out = path.resolve(args.out || path.join(__dirname, "screenshots"));
const before = args.before || "7cc44b68a9afa0d92db689ff5ed0f5010a02b923";
const after = args.after || "0ae81b3c5a02a840d3d4983c9503cdbb1f86b291";
const anchor = new Date("2026-10-04T05:09:00Z");
const day = "2026-10-04";
const memoryA = "10000000-0000-4000-8000-000000000001";
const memoryB = "10000000-0000-4000-8000-000000000002";
const chat = "10000000-0000-4000-8000-000000000003";
const report = { before, after, simulated: true, fixture: [], cases: [] };
const assets = new Map();

function asset(ref, file) {
  const key = ref + ":internal/gui/assets/" + file;
  if (!assets.has(key)) assets.set(key, execFileSync("git", ["show", key], { cwd: repo, maxBuffer: 20 * 1024 * 1024 }));
  return assets.get(key);
}
function request(id, session, kind = "", sessionTitle = "") {
  const time = new Date(anchor.getTime() - (10 - id) * 10e3).toISOString();
  const model = kind ? "gpt-5.6-terra" : "gpt-6.1-sol";
  const seat = { id: "codex", provider: "codex", name: "Codex", who: "Simulated account", kind: "account", model };
  return { id, seq: id, time, agent: "codex", session, kind, sessionTitle, model: `codex/${model}`, provider: "codex",
    order: [seat], tries: [{ id: seat.id, model, effort: "medium", start: time, done: true, status: 200, ms: 5600, ttft: 1800 }],
    done: true, status: 200, ms: 5600, tokens: 28000, ttft: 1800, priced: true, cost: 0.01 * id };
}
function fixture(lang) {
  const title = lang === "zh" ? "课外班讨论（模拟）" : "Extracurricular classes (simulated)";
  return [request(1, chat, "", title), request(2, chat, "memory_consolidation", title),
    request(3, memoryA, "memory_consolidation"), request(4, memoryA, "memgen"),
    request(5, memoryB, "memory"), request(6, memoryA, "memory_consolidation")];
}
function serve(ref, lang, rows, feed, requests) {
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs={lang:"${lang}",theme:"dark",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname.startsWith("/api/")) requests.push(url.pathname);
    if (url.pathname === "/api/state") return json({ agents: [{ id: "codex", name: "Codex", fields: [] }], profiles: [], settings: { lang, theme: "dark" } });
    if (url.pathname === "/api/gateway/trace") {
      const updates = url.searchParams.has("wait") ? await new Promise((resolve) => { feed.next = resolve; }) : rows;
      return json({ mine: true, now: anchor.toISOString(), seq: updates.at(-1)?.seq || 6, totals: { requests: rows.length, rerouted: 0, errors: 0 }, routes: updates });
    }
    if (url.pathname === "/api/gateway/history") return json({ cut: false, days: [{ day, requests: rows.length }], routes: url.searchParams.get("day") ? rows : [] });
    if (url.pathname === "/api/gateway/session-titles") return json({ [chat]: rows[0].sessionTitle });
    if (url.pathname === "/api/groups") return json({ groups: [], models: [] });
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true } });
    if (url.pathname === "/api/clis") return json({ agents: [], providers: [] });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = url.pathname === "/" ? "index.html" : url.pathname.slice(1);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    try { return await route.fulfill({ body: asset(ref, file), contentType }); }
    catch { return route.fulfill({ status: 404, body: "" }); }
  };
}
async function capture(engine, lang, ref, version) {
  const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
  const context = await browser.newContext({ viewport: { width: 1800, height: 1000 }, timezoneId: "Asia/Singapore", locale: lang === "zh" ? "zh-CN" : "en-US", reducedMotion: "reduce" });
  const page = await context.newPage();
  page.setDefaultTimeout(5000);
  await page.clock.setFixedTime(anchor);
  const errors = [], requests = [], feed = {}, rows = fixture(lang);
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (msg) => { if (msg.type() === "error") errors.push(msg.text()); });
  await page.route("**/*", serve(ref, lang, rows, feed, requests));
  try {
    await page.goto("http://magpie.test/?view=routing");
    await page.locator(".rt-req").nth(5).waitFor();
    await page.locator(".rt-group-by button").nth(1).click();
    await page.locator("button.rt-session").nth(2).waitFor();
    const group = (id) => page.locator(`button.rt-session:has(.nm[title$="${id}"])`);
    const label = lang === "zh" ? "后台记忆整理" : "Background memory task";
    assert.equal(await group(memoryA).locator(".nm").textContent(), `Codex · ${version === "after" ? label : memoryA}`);
    assert.equal(await group(memoryB).locator(".nm").textContent(), `Codex · ${version === "after" ? label : memoryB}`);
    assert.equal(await group(chat).locator(".nm").textContent(), `Codex · ${rows[0].sessionTitle}`);
    assert.equal(await page.locator("button.rt-session").count(), 3);
    assert.equal(await group(memoryA).locator(".cost").textContent(), "≈$0.130");
    const groups = await page.locator("button.rt-session").evaluateAll((els) => els.map((e) => ({ name: e.querySelector(".nm").textContent, tooltip: e.querySelector(".nm").title, summary: e.querySelector(".summary").textContent, cost: e.querySelector(".cost").textContent })));
    await page.waitForTimeout(200);
    const toast = await page.locator("#status.err").textContent().catch(() => "");
    assert.equal(toast || "", "", `incomplete fixture: ${toast}; API paths: ${requests.join(", ")}`);
    const filename = `${engine}-${lang}-${version}.png`;
    await page.locator(".rt-cols > div").first().screenshot({ path: path.join(out, filename) });
    const folded = group(memoryA), handle = await folded.elementHandle();
    await folded.click();
    await page.waitForFunction(() => document.querySelectorAll(".rt-req").length === 3);
    for (let i = 0; i < 50 && !feed.next; i++) await page.waitForTimeout(20);
    assert(feed.next, "long poll started");
    const next = feed.next; feed.next = null;
    next([request(7, memoryA, "memory_consolidation")]);
    await page.waitForFunction(() => [...document.querySelectorAll("button.rt-session .cost")].some((e) => e.textContent === "≈$0.200"));
    assert.equal(await folded.getAttribute("aria-expanded"), "false");
    assert(await folded.evaluate((e, old) => e === old, handle));
    assert.equal(await page.locator("button.rt-session").count(), 3);
    await folded.click();
    await page.locator(".rt-group-by button").first().click();
    assert.equal(await page.locator(".rt-req").count(), 7);
    await page.locator(".rt-group-by button").nth(1).click();
    await page.setViewportSize({ width: 560, height: 800 });
    await page.waitForTimeout(100);
    const fit = await page.locator(".rt-reqs").evaluate((e) => ({ client: e.clientWidth, scroll: e.scrollWidth }));
    assert(fit.scroll <= fit.client + 1, `horizontal overflow: ${JSON.stringify(fit)}`);
    if (version === "after") await page.locator(".rt-cols > div").first().screenshot({ path: path.join(out, `${engine}-${lang}-after-narrow.png`) });
    assert.deepEqual(errors, []);
    report.cases.push({ engine, lang, version, ref, screenshot: filename, groups, liveUpdate: "passed", folds: "passed", byRequestCount: 7, narrow: "passed", errors });
    console.log(`${engine} ${lang} ${version}: passed`);
  } finally { feed.next?.([]); await browser.close(); }
}
async function compare(engine, lang) {
  const imgs = await Promise.all(["before", "after"].map(async (version) => "data:image/png;base64," + (await fs.readFile(path.join(out, `${engine}-${lang}-${version}.png`))).toString("base64")));
  const browser = await chromium.launch({ channel: "chromium" });
  const page = await browser.newPage({ viewport: { width: 1800, height: 1300 }, deviceScaleFactor: 1 });
  try {
    await page.setContent(`<!doctype html><html><head><meta charset="utf-8"><style>body{margin:0;padding:24px;background:#16171b;color:#eee;font:18px system-ui}h1{font-size:26px;margin:0 0 8px}p{color:#a6abb5;margin:0 0 20px;font-size:16px}.columns{display:grid;grid-template-columns:1fr 1fr;gap:20px}.heading{font-size:19px;margin-bottom:10px}.heading span{color:#a6abb5;font-size:14px}img{display:block;width:100%;border:1px solid #41434a;border-radius:8px}.footer{margin:20px 0 0;color:#b7bdc7;font-size:16px}</style></head><body><h1>${lang === "zh" ? "PR #753：后台记忆整理分组 · 修改前后" : "PR #753: Background memory groups · Before / after"}</h1><p>Identical simulated gateway responses · Real Magpie assets · ${engine} · ${lang === "zh" ? "Chinese" : "English"}</p><div class="columns"><div><div class="heading">${lang === "zh" ? "修改前" : "Before"} <span>${before.slice(0,8)}</span></div><img src="${imgs[0]}"></div><div><div class="heading">${lang === "zh" ? "修改后" : "After"} <span>${after.slice(0,8)}</span></div><img src="${imgs[1]}"></div></div><div class="footer">${lang === "zh" ? "相同的请求、会话 ID、费用和分组；只改变无名称记忆任务的标题与提示。模拟数据，无需 Codex 账号。" : "Same requests, session IDs, costs and groups. Only unnamed memory-task labels and tooltips change. Simulated data; no Codex account needed."}</div></body></html>`);
    await page.locator("img").last().waitFor();
    await page.evaluate(async () => { await Promise.all([...document.images].map((img) => img.decode())); });
    await page.locator("body").screenshot({ path: path.join(out, `${engine}-${lang}-comparison.png`) });
  } finally { await browser.close(); }
}
(async () => {
  await fs.mkdir(out, { recursive: true });
  report.fixture = fixture("en");
  for (const engine of ["chromium", "webkit"]) for (const lang of ["en", "zh"]) {
    await capture(engine, lang, before, "before");
    await capture(engine, lang, after, "after");
    await compare(engine, lang);
  }
  await fs.writeFile(path.join(out, "results.json"), JSON.stringify(report, null, 2) + "\n");
  console.log(`All 8 before/after browser cases passed. Artifacts: ${out}`);
})().catch((e) => { console.error(e); process.exitCode = 1; });
