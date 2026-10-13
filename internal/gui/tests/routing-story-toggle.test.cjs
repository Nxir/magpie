// Routing keeps its story open until the reader explicitly folds it.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { test } = require('node:test');
const { chromium, webkit } = require('playwright');
const assets = process.env.MAGPIE_STORY_ASSETS || path.resolve(__dirname, '../assets');
const now = new Date().toISOString();
const account = { id: 'codex', provider: 'codex', name: 'Codex', who: 'demo@example.com', icon: 'codex-color', kind: 'account', model: 'gpt-6-luna' };
const prompt = { window: 272000, tokens: 43900, counted: true, turns: 1,
  parts: [{ kind: 'system', tokens: 10000, items: [{ name: 'prompt', tokens: 10000 }] },
    { kind: 'tools', tokens: 9500, items: [{ name: 'exec_command', tokens: 9500 }] },
    { kind: 'memory', tokens: 5300, items: [{ name: '/demo/AGENTS.md', tag: 'project', tokens: 5300 }] },
    { kind: 'results', tokens: 14500, items: [{ name: 'exec_command', tag: 'result', n: 4, tokens: 14500 }] },
    { kind: 'chat', tokens: 4600, items: [{ name: '1', tag: 'turn', tokens: 4600 }] }] };
function request(id, extra = {}) {
  return { id, seq: id, time: now, agent: 'codex', model: 'codex/gpt-6-luna', provider: 'codex',
    kind: 'ambient_suggestions', order: [account], tries: [{ id: account.id, model: account.model, effort: 'medium', start: now, done: true, status: 200, ms: 5900, ttft: 5700 }],
    done: true, status: 200, ms: 5900, ttft: 5700, tokens: 44100, out: 200, prompt,
    usage: [{ in: 3200, cache_read: 40700, out: 200 }], ...extra };
}
function serve(lang, feed) {
  return async (r) => {
    const url = new URL(r.request().url()), json = (data) => r.fulfill({ json: data });
    if (url.pathname === '/boot.js') return r.fulfill({ contentType: 'text/javascript', body: `window.bootPrefs={lang:"${lang}",theme:"dark",web:true};` });
    if (url.pathname === '/wails/runtime.js') return r.fulfill({ contentType: 'text/javascript', body: 'export const Window={};' });
    if (url.pathname === '/api/state') return json({ agents: [{ id: 'codex', name: 'Codex', fields: [] }], profiles: [], settings: { lang, theme: 'dark' } });
    if (url.pathname === '/api/gateway/trace') {
      if (!url.searchParams.has('wait')) feed.count = 1;
      const routes = url.searchParams.has('wait') ? await new Promise((resolve) => { feed.next = resolve; }) : [request(100)];
      return json({ mine: true, now, seq: routes[0]?.id || 100, totals: { requests: 1, rerouted: 0, errors: 0 }, routes });
    }
    if (url.pathname === '/api/gateway/history') return json({ days: [], routes: [] });
    if (url.pathname === '/api/providers') return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true } });
    if (url.pathname === '/api/groups') return json({ groups: [] });
    if (url.pathname.startsWith('/api/')) return json({});
    const file = path.join(assets, url.pathname === '/' ? 'index.html' : url.pathname);
    await r.fulfill({ body: await fs.readFile(file), contentType: { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png' }[path.extname(file)] });
  };
}
for (const engine of process.env.BROWSER ? [process.env.BROWSER] : ['chromium', 'webkit']) {
  for (const lang of ['en', 'zh', 'zh-TW', 'ja', 'de']) {
    for (const width of [1440, 560, 420]) {
      test(`${engine} ${lang} ${width}px: request details start open and can be folded independently`, async (t) => {
        const browser = await (engine === 'webkit' ? webkit.launch() : chromium.launch({ channel: 'chromium' }));
        t.after(() => browser.close());
        const page = await browser.newPage({ viewport: { width, height: 900 }, reducedMotion: 'reduce' });
        page.setDefaultTimeout(5000);
        const feed = {}, errors = [];
        page.on('pageerror', (e) => errors.push(e.message));
        await page.route('**/*', serve(lang, feed));
        await page.goto('http://magpie.test/?view=routing');
        await page.locator('.rt-steps li.why').waitFor();
        const toggle = page.locator('.rt-story-toggle');
        assert.equal(await toggle.count(), 1, 'the story has its own disclosure');
        assert.equal(await toggle.getAttribute('aria-expanded'), 'true', 'routing reasons are open by default');
        const title = await page.evaluate(() => t('How the last request was routed'));
        assert.ok((await page.locator('.rt-log-head').innerText()).includes(title));
        assert.equal(await page.locator('.rt-cap').isVisible(), true);
        const text = await page.locator('.rt-steps').innerText();
        assert.match(text, /demo@example.com/);
        const remember = await page.locator('.rt-steps').evaluate((e) => { window.__reason=e.querySelector('li.why'); return !!window.__reason; });
        assert.equal(remember, true);
        await toggle.click();
        assert.equal(await page.locator('.rt-steps').isVisible(), false);
        assert.equal(await page.locator('.rt-cap').isVisible(), true, 'the stage still explains the route while folded');
        assert.equal(await toggle.getAttribute('aria-expanded'), 'false');
        await toggle.focus();
        await page.keyboard.press('Enter');
        assert.equal(await page.locator('.rt-steps').innerText(), text);
        assert.equal(await page.locator('.rt-steps').evaluate((e) => e.contains(window.__reason)), true, 'folding retains explanation nodes and logos');
        await toggle.click();
        await page.reload();
        await page.locator('.rt-story-toggle').waitFor();
        assert.equal(await page.locator('.rt-story-toggle').getAttribute('aria-expanded'), 'false', 'reload remembers the explicit choice');
        assert.equal(await page.locator('.rt-steps').isVisible(), false);
        for (let i=0;i<60 && !feed.next;i++) await page.waitForTimeout(50);
        assert.ok(feed.next);
        const next=feed.next;feed.next=null;
        next([request(101, {status:500,error:'provider failed',tries:[{id:account.id,model:account.model,start:now,done:true,status:500,error:'provider failed',ms:1000}]})]);
        await page.locator('.rt-req').filter({hasText:'500'}).waitFor();
        assert.equal(await page.locator('.rt-story-toggle').getAttribute('aria-expanded'), 'false', 'new requests retain the choice');
        assert.equal(await page.locator('.rt-cap').isVisible(), true);
        await page.locator('.rt-story-toggle').click();
        assert.match(await page.locator('.rt-steps').innerText(), /provider failed/);
        assert.deepEqual(errors, []);
      });
    }
  }
}
