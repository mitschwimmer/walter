// Visual scenarios complement the existing interaction regression suite.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { createRequire } from 'node:module';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { platform, arch, release } from 'node:os';

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.WALTER_PLAYWRIGHT_MODULE || 'playwright');
const root = fileURLToPath(new URL('../', import.meta.url));
const output = process.env.WALTER_SCREENSHOT_DIR;
assert.ok(output, 'Set WALTER_SCREENSHOT_DIR to a fresh capture directory');
const revision = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim();
const viewports = { desktop: { width: 1071, height: 1579 }, mobile: { width: 390, height: 844 } };
const locale = 'de-DE', timezone = 'Europe/Berlin', reducedMotion = 'reduce';
const prompt = 'Was ist Marmelade?';
const id = '00000000000000000000000000000001';
const answer = '<p>Marmelade ist ein süßer Aufstrich aus Früchten und Zucker.</p><h2>Wichtige Merkmale</h2><ul><li>Früchte</li><li>Zucker</li></ul><table><thead><tr><th>Zutat</th><th>Menge</th></tr></thead><tbody><tr><td>Früchte</td><td>1 kg</td></tr></tbody></table><pre><code>fruechte = 1000;\nzucker = 500;</code></pre>';
const seed = { id, title: prompt, thinking: 'thoughtful', updated_at: '2026-10-16T18:00:00Z',
  messages: [{ role: 'user', content: prompt, status: 'complete' },
    { role: 'assistant', content: 'Marmelade', html: answer, status: 'complete' }] };
const json = (res, status, data) => {
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(data));
};

// A fresh local server for each scenario/variant. No live OIDC or model calls.
async function fixture(name) {
  assert.ok(['signed-out', 'empty-chat', 'streaming-reply', 'complete-reply'].includes(name), 'Unknown fixture: ' + name);
  const saved = name === 'complete-reply';
  const server = createServer(async (req, res) => {
    try {
      res.setHeader('Content-Security-Policy', "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'");
      if (req.url === '/api/site') return json(res, 200, { revision: 'fixture', imprint_url: 'https://example.com/impressum', privacy_url: 'https://example.com/datenschutz' });
      if (!req.url.startsWith('/api/') && !req.url.startsWith('/auth/')) {
        const file = req.url === '/' ? 'index.html' : req.url.slice(1);
        const types = { 'index.html': 'text/html', 'app.js': 'text/javascript', 'sse.js': 'text/javascript', 'style.css': 'text/css', 'oat.css': 'text/css', 'archaic-theme.css': 'text/css', 'walter.svg': 'image/svg+xml', 'headline-overlay.svg': 'image/svg+xml' };
        if (!Object.hasOwn(types, file)) return json(res, 404, {});
        res.setHeader('Content-Type', types[file]);
        return res.end(await readFile(join(root, 'web', file)));
      }
      if (name === 'signed-out') return json(res, 401, { error: 'Bitte erneut anmelden.' });
      if (req.url === '/api/session') return json(res, 200, { csrf: 'fixture-csrf' });
      if (req.url === '/api/conversations' && req.method === 'GET') return json(res, 200, saved ? [seed] : []);
      if (req.url === '/api/conversations/' + id && req.method === 'GET' && saved) {
        return json(res, 200, { ...seed, context: { capacity: 16384, quick: 4096, thoughtful: 8192 } });
      }
      if (name === 'streaming-reply' && req.method === 'POST') {
        assert.equal(req.headers['x-csrf-token'], 'fixture-csrf');
        let body = '';
        for await (const chunk of req) body += chunk;
        const data = JSON.parse(body);
        if (req.url === '/api/conversations') {
          assert.deepEqual(data, { thinking: 'thoughtful' });
          return json(res, 201, { ...seed, messages: [] });
        }
        if (req.url === '/api/conversations/' + id + '/messages') {
          assert.deepEqual(data, { content: prompt, thinking: 'thoughtful' });
          res.writeHead(200, { 'Content-Type': 'text/event-stream' });
          res.write('event: start\ndata: {}\n\n');
          // Hold the stream at a deterministic checkpoint until context teardown.
          return;
        }
      }
      return json(res, 404, { error: 'Unexpected fixture request' });
    } catch (error) {
      console.error(error);
      if (!res.headersSent) json(res, 500, { error: 'Fixture failure' });
      else res.end();
    }
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  return { origin: 'http://127.0.0.1:' + server.address().port,
    close: async () => {
      server.closeAllConnections();
      await new Promise(resolve => server.close(resolve));
    } };
}

const state = (page, name) => page.waitForFunction(value => document.body.dataset.view === value, name);
const visible = (page, selector) => page.locator(selector).waitFor({ state: 'visible' });
async function complete(page, origin) {
  await page.goto(origin + '/#gespraech/' + id);
  await state(page, 'conversation');
  await visible(page, '.assistant pre');
  await visible(page, '.assistant table');
  await visible(page, '#context-info');
  assert.equal(await page.locator('.user .message-content').textContent(), prompt);
  assert.equal(await page.locator('#context-info').textContent(), 'Kontext: 50% · 8.192 / 16.384 Tokens');
}
const cases = {
  'auth.login.required': { fixture: 'signed-out', checkpoints: ['login'], async run(page, origin, shot) {
    await page.goto(origin);
    await state(page, 'login');
    await visible(page, '#login a');
    assert.equal(await page.locator('#account-menu').isVisible(), false);
    await shot('login');
  } },
  'chat.composer.ready': { fixture: 'empty-chat', checkpoints: ['ready'], async run(page, origin, shot) {
    await page.goto(origin);
    await state(page, 'start');
    await page.waitForFunction(() => !document.querySelector('#content').disabled);
    assert.equal(await page.locator('#quick').isEnabled(), true);
    assert.equal(await page.locator('#thoughtful').isEnabled(), true);
    await page.locator('#content').fill(prompt);
    await shot('ready');
  } },
  'chat.reply.streaming': { fixture: 'streaming-reply', checkpoints: ['waiting'], async run(page, origin, shot) {
    await page.goto(origin);
    await state(page, 'start');
    await page.locator('#content').fill(prompt);
    await page.locator('#thoughtful').click();
    await state(page, 'conversation');
    await visible(page, '.response-loader');
    assert.equal(await page.locator('.response-status').textContent(), 'Warte auf das Modell …');
    assert.equal(await page.locator('#stop').isVisible(), true);
    assert.equal(await page.locator('#content').isVisible(), false);
    await shot('waiting');
  } },
  'chat.reply.complete': { fixture: 'complete-reply', checkpoints: ['answer'], async run(page, origin, shot) {
    await complete(page, origin);
    await shot('answer');
  } },
  'chat.history.populated': { fixture: 'complete-reply', checkpoints: ['list'], async run(page, origin, shot) {
    await page.goto(origin + '/#historie');
    await state(page, 'history');
    await visible(page, '#chats a');
    assert.equal(await page.locator('#chats li').count(), 1);
    assert.equal(await page.locator('#chats .conversation-title').textContent(), prompt);
    assert.equal(await page.locator('#chats time').textContent(), '16. Oktober 2026');
    await shot('list');
  } },
  'chat.history.empty': { fixture: 'empty-chat', checkpoints: ['list'], async run(page, origin, shot) {
    await page.goto(origin + '/#historie');
    await state(page, 'history');
    await page.waitForFunction(() => document.querySelector('#history-status').textContent.startsWith('Hier findest'));
    assert.equal(await page.locator('#chats li').count(), 0);
    await shot('list');
  } },
  'chat.delete.confirmation': { fixture: 'complete-reply', checkpoints: ['dialog'], async run(page, origin, shot) {
    await complete(page, origin);
    await page.locator('#delete').click();
    await visible(page, '#delete-dialog[open]');
    assert.ok((await page.locator('#delete-dialog p').textContent()).includes(prompt));
    await shot('dialog');
    await page.getByRole('button', { name: 'Behalten', exact: true }).click();
    assert.equal(await page.locator('#delete-dialog').isVisible(), false);
  } }
};

const files = (await readdir(join(root, 'tests/scenarios'))).filter(file => file.endsWith('.json')).sort();
const definitions = [];
const contracts = {};
// Conservative shared contract: changes to this runner invalidate all scenarios.
// Application assets are deliberately excluded from the fingerprint.
const source = await readFile(fileURLToPath(import.meta.url));
for (const file of files) {
  const def = JSON.parse(await readFile(join(root, 'tests/scenarios', file), 'utf8'));
  assert.match(def.id, /^[a-z][a-z0-9]*(?:-[a-z0-9]+)*\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/);
  assert.equal(file, def.id + '.json');
  assert.ok(!Object.hasOwn(contracts, def.id), 'Duplicate scenario');
  assert.ok(Object.hasOwn(cases, def.id), 'Missing scenario test: ' + def.id);
  assert.equal(def.fixture, cases[def.id].fixture);
  assert.deepEqual(def.checkpoints, cases[def.id].checkpoints);
  for (const [values, allowed] of [[def.viewports, Object.keys(viewports)], [def.themes, ['light', 'dark']]]) {
    assert.ok(values.length && new Set(values).size === values.length && values.every(value => allowed.includes(value)));
  }
  const normalized = Object.fromEntries(Object.keys(def).sort().map(key => [key, def[key]]));
  contracts[def.id] = createHash('sha256').update(JSON.stringify(normalized)).update('\0').update(source).digest('hex');
  definitions.push(def);
}
assert.deepEqual(Object.keys(cases).sort(), Object.keys(contracts).sort(), 'Catalog and test cases must match');
assert.ok(definitions.length, 'Empty visual catalog');
await mkdir(output, { recursive: true });
assert.equal((await readdir(output)).length, 0, 'Capture directory must be empty');
const browser = await chromium.launch({ headless: true, chromiumSandbox: !process.env.CI,
  ...(process.env.WALTER_BROWSER_EXECUTABLE ? { executablePath: process.env.WALTER_BROWSER_EXECUTABLE } : {}) });
const manifest = { format: 1, revision, environment: { browser: browser.version(),
  platform: platform() + '/' + arch() + '/' + release(), locale, timezone, deviceScaleFactor: 1, reducedMotion },
  contracts, images: [] };
try {
  for (const def of definitions) {
    for (const viewport of def.viewports) for (const theme of def.themes) {
      const server = await fixture(def.fixture);
      let context;
      try {
        const size = viewports[viewport];
        context = await browser.newContext({ viewport: size, colorScheme: theme, locale, timezoneId: timezone,
          deviceScaleFactor: 1, reducedMotion });
        await context.addInitScript(() => { Math.random = () => 0.25; });
        const page = await context.newPage();
        const errors = [];
        page.on('pageerror', error => errors.push(error.message));
        const captured = new Set();
        await cases[def.id].run(page, server.origin, async checkpoint => {
          assert.ok(def.checkpoints.includes(checkpoint) && !captured.has(checkpoint), 'Unexpected checkpoint: ' + checkpoint);
          await visible(page, '#revision');
          await page.evaluate(() => document.fonts.ready.then(() => true));
          assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'Document overflow');
          const file = [def.id, checkpoint, viewport, theme].join('--') + '.png';
          await page.screenshot({ path: join(output, file), fullPage: true, animations: 'disabled', caret: 'hide' });
          manifest.images.push({ id: def.id, checkpoint, viewport, theme, ...size, file });
          captured.add(checkpoint);
        });
        assert.deepEqual([...captured].sort(), [...def.checkpoints].sort(), 'Missing checkpoint');
        assert.deepEqual(errors, []);
      } finally {
        try { if (context) await context.close(); } finally { await server.close(); }
      }
    }
  }
  await writeFile(join(output, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
  console.log('Captured ' + manifest.images.length + ' visual checkpoints for ' + revision);
} finally {
  await browser.close();
}
