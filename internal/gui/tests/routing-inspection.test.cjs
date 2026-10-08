// Inspecting a request holds its identity while its own progress, the list
// and counts keep updating. Live, selection, filtering and replay are distinct.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { test } = require('node:test');
const { chromium, webkit } = require('playwright');
const assets = process.env.ASSET_DIR || path.resolve(__dirname, '../assets');
const now = new Date(), historyDay = '2026-09-01';
function req(id, extra = {}) {
  const time = new Date(+now + (id - 100) * 1000).toISOString();
  const account = { id: 'codex', provider: 'codex', name: 'Codex', who: 'demo@example.com', icon: 'codex-color', kind: 'account', model: 'model-' + id };
  return { id, seq: id, time, agent: 'codex', model: 'codex/' + account.model, provider: 'codex', kind: 'review',
    order: [account], tries: [{ id: account.id, model: account.model, start: time, done: true, status: 200, ms: 5900 }],
    done: true, status: 200, ms: 5900, tokens: 11000 + id, out: 200,
    prompt: { window: 272000, tokens: 10000 + id, counted: true, parts: [{ kind: 'chat', tokens: 10000 + id, items: [{ name: '1', tag: 'turn', tokens: 10000 + id }] }] },
    ...extra };
}
function serve(lang, feed) {
  return async (route) => {
    const url = new URL(route.request().url()), json = (data) => route.fulfill({ json: data });
    if (url.pathname === '/boot.js') return route.fulfill({ contentType: 'text/javascript', body: `window.bootPrefs={lang:"${lang}",theme:"dark",web:true};` });
    if (url.pathname === '/wails/runtime.js') return route.fulfill({ contentType: 'text/javascript', body: 'export const Window={};' });
    if (url.pathname === '/api/state') return json({ agents: [{ id: 'codex', name: 'Codex', fields: [] }], profiles: [], settings: { lang, theme: 'dark' } });
    if (url.pathname === '/api/providers') return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true } });
    if (url.pathname === '/api/groups') return json({ groups: [] });
    if (url.pathname === '/api/gateway/trace') {
      const routes = url.searchParams.has('wait') ? await new Promise((resolve) => { feed.next = resolve; }) : feed.initial;
      return json({ mine: true, seq: feed.seq, now: now.toISOString(), totals: { requests: feed.total, rerouted: 0, errors: 0 }, routes });
    }
    if (url.pathname === '/api/gateway/history') return json({ days: [{ day: historyDay, requests: 1 }], cut: false,
      routes: url.searchParams.get('day') ? [req(50, { time: historyDay + 'T10:00:00Z' })] : [] });
    if (url.pathname.startsWith('/api/')) return json({});
    const file = path.join(assets, url.pathname === '/' ? 'index.html' : url.pathname);
    await route.fulfill({ body: await fs.readFile(file), contentType: { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png' }[path.extname(file)] });
  };
}
async function start(t, engine, lang, width, initial = [req(100)]) {
  const browser = await (engine === 'webkit' ? webkit.launch() : chromium.launch({ channel: 'chromium' }));
  const page = await browser.newPage({ viewport: { width, height: 1250 }, reducedMotion: 'reduce' });
  page.setDefaultTimeout(5000);
  const feed = { initial, seq: 100, total: 1 }, errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await page.route('**/*', serve(lang, feed));
  t.after(async () => { feed.next?.([]); await browser.close(); assert.deepEqual(errors, []); });
  const send = async (rs, total = feed.total + 1, seq = feed.seq + 1) => {
    for (let i = 0; i < 150 && !feed.next; i++) await page.waitForTimeout(20);
    assert.ok(feed.next, 'the trace poll is waiting');
    const answer = feed.next; feed.next = null;
    feed.total = total; feed.seq = seq; answer(rs);
    for (let i = 0; i < 150 && !feed.next; i++) await page.waitForTimeout(20);
    assert.ok(feed.next, 'the trace update was applied');
  };
  await page.goto('http://magpie.test/?view=routing');
  await page.locator('.rt-brief').waitFor();
  return { page, send, feed };
}
async function click(page, target) {
  await target.waitFor({ state: 'visible' });
  const v = await page.locator('#view-routing').boundingBox();
  await page.mouse.move(v.x + 20, v.y + v.height / 2);
  for (let i = 0; i < 100; i++) {
    const b = await target.boundingBox();
    if (b.y >= v.y && b.y + b.height <= v.y + v.height) break;
    await page.mouse.wheel(0, b.y < v.y ? -130 : 130);
    await page.waitForTimeout(40);
  }
  await page.waitForTimeout(100);
  await target.click();
}
const selected = (page, id) => page.waitForFunction((id) => document.querySelector('.rt-brief-path')?.textContent.includes('model-' + id)
  && document.querySelector('.rt-req[aria-pressed="true"]')?.textContent.includes('model-' + id), id);
const back = (page, lang) => page.locator('.rt-log-head button', { hasText: { en: 'Back to live', zh: '回到实时', 'zh-TW': '回到即時', ja: 'ライブに戻る', de: 'Zurück zu live' }[lang] });
for (const engine of process.env.BROWSER ? [process.env.BROWSER] : ['chromium', 'webkit']) {
  for (const lang of ['en', 'zh', 'zh-TW', 'ja', 'de']) {
    for (const width of [1440, 420]) {
      test(`${engine} ${lang} ${width}px: expanding details holds one request without freezing its progress`, async (t) => {
        const running = req(100, { done: false, prompt: { ...req(100).prompt, counted: false }, tries: [{ ...req(100).tries[0], done: false, status: 0 }] });
        const { page, send } = await start(t, engine, lang, width, [running]);
        const story = page.locator('.rt-detail-toggle'), context = page.locator('.rt-ctx-toggle');
        await context.click();
        await selected(page, 100);
        await story.click();
        const top = await page.locator('#view-routing').evaluate((v) => v.scrollTop);
        await send([req(100, { ms: 7100, prompt: { ...req(100).prompt, tokens: 12345 } }), req(101)]);
        await page.waitForFunction(() => document.querySelectorAll('.rt-req').length === 2);
        assert.match(await page.locator('.rt-brief-path').innerText(), /model-100/, 'a new request cannot replace the request held for reading');
        assert.match(await page.locator('.rt-brief .duration .v').innerText(), /7\.1/, 'the held request can finish with its final timing');
        assert.match(await page.locator('.rt-ctx .ctx-big b').innerText(), /12\.3K/, 'its own final token count replaces the estimate');
        assert.equal(await page.locator('.rt-new-requests').isVisible(), true);
        assert.match(await page.locator('.rt-new-requests').innerText(), /1/);
        assert.equal(await page.locator('.rt-stats b').first().innerText(), '2', 'global statistics keep updating');
        assert.equal(await page.locator('.rt-req').count(), 2, 'new requests enter the list');
        assert.equal(await page.locator('#view-routing').evaluate((v) => v.scrollTop), top, 'incoming calls do not scroll the inspector');
        await context.click();
        await send([req(102)]);
        await selected(page, 100);
        assert.match(await page.locator('.rt-new-requests').innerText(), /2/, 'the other open disclosure keeps the hold');
        await story.click();
        await selected(page, 102);
        assert.equal(await back(page, lang).count(), 0, 'closing both releases only the automatic detail hold');
        await send([req(103)]);
        await selected(page, 103);
        await story.click();
        await send([req(104)]);
        await selected(page, 103);
        await click(page, back(page, lang));
        await selected(page, 104);
        assert.equal(await story.getAttribute('aria-expanded'), 'true', 'returning live preserves disclosure preferences');
        await send([req(105)]);
        await selected(page, 105);
        await context.click();
        await send([req(106)]);
        await selected(page, 105);
        await page.reload();
        await page.locator('.rt-ctx .ctx-waffle').waitFor();
        assert.equal(await back(page, lang).count(), 0, 'restored preferences do not create a hold');
        await send([req(107)]);
        await selected(page, 107);
        // Explicitly choosing even the newest request is an inspection.
        await click(page, page.locator('.rt-req').first());
        await send([req(108)]);
        await selected(page, 107);
        await click(page, story);
        await click(page, context);
        await selected(page, 107);
        assert.equal(await back(page, lang).count(), 1, 'closing details does not clear an explicit request selection');
        await click(page, back(page, lang));
        await selected(page, 108);
      });
    }
  }
  for (const lang of ['en', 'zh']) {
    test(`${engine} ${lang}: opening retained context holds the request its counts belong to`, async (t) => {
      const { page, send } = await start(t, engine, lang, 1440);
      await send([req(101, { done: false, prompt: null, tries: [{ ...req(101).tries[0], done: false, status: 0 }] })]);
      await selected(page, 101);
      assert.match(await page.locator('.rt-ctx-toggle').innerText(), /10\.1K/);
      await page.locator('.rt-ctx-toggle').click();
      await selected(page, 100);
      assert.match(await page.locator('.rt-ctx .ctx-crumbs').innerText(), /#100/);
      await send([req(101), req(102)]);
      await selected(page, 100);
      await page.locator('.rt-ctx-toggle').click();
      await selected(page, 102);
    });
    test(`${engine} ${lang}: a detail hold respects filtering, replay, history and gateway restart`, async (t) => {
      const { page, send } = await start(t, engine, lang, 1440, [req(100), req(99, { kind: 'thread_title' })]);
      const story = page.locator('.rt-detail-toggle');
      await story.click();
      await send([req(101, { kind: 'thread_title' })]);
      await selected(page, 100);
      await click(page, page.locator('.rt-log-head button', { hasText: lang === 'zh' ? /^重放$/ : /^Replay$/ }));
      await page.locator('.rt-replay').waitFor();
      await click(page, page.locator('.rp-top button', { hasText: lang === 'zh' ? '停止重放' : 'Stop replay' }));
      await selected(page, 100);
      await send([req(102)]);
      await selected(page, 100);
      await story.click();
      await selected(page, 102);
      await story.click();
      // The selected request is outside the new purpose; follow that scope.
      await click(page, page.locator('#rtPurpose'));
      const title = await page.evaluate(() => purposeOptions(['kind:thread_title'])[0].name);
      await page.locator('.rt-purpose-menu').getByRole('menuitemcheckbox', { name: title, exact: true }).click();
      await page.keyboard.press('Escape');
      await selected(page, 101);
      assert.equal(await back(page, lang).count(), 0);
      await send([req(103, { kind: 'thread_title' })]);
      await selected(page, 103);
      await click(page, page.locator('#rtPurposeClear'));
      await selected(page, 103);
      // A historical selection must not absorb a live update reusing its id.
      await click(page, page.locator('.rt-days .rt-day').nth(1));
      await selected(page, 50);
      await send([req(50, { model: 'codex/live-reused-id' })]);
      await selected(page, 50);
      await click(page, back(page, lang));
      await selected(page, 103);
      await story.click(); // close the restored preference
      await story.click(); // deliberately hold again
      await send([req(104)]);
      await selected(page, 103);
      await send([req(1)], 1, 1);
      await selected(page, 1);
      assert.equal(await back(page, lang).count(), 0, 'gateway restart releases IDs from the previous run');
      await story.click();
      await story.click();
      await click(page, page.locator('.rt-log-head button', { hasText: lang === 'zh' ? /^重放$/ : /^Replay$/ }));
      await page.locator('.rt-replay').waitFor();
      await send([req(1, { model: 'codex/restarted-during-replay' })], 1, 0);
      await click(page, page.locator('.rp-top button', { hasText: lang === 'zh' ? '停止重放' : 'Stop replay' }));
      await page.waitForFunction(() => document.querySelector('.rt-brief-path')?.textContent.includes('restarted-during-replay'));
      assert.equal(await back(page, lang).count(), 0, 'replay cannot restore a selection from the previous gateway run');
    });
  }
}
