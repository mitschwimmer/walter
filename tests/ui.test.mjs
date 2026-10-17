// Optional browser regression suite; Playwright is a test tool, not a runtime dependency.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile, mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
import { join } from 'node:path';
import { once } from 'node:events';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.WALTER_PLAYWRIGHT_MODULE || 'playwright');
const root = fileURLToPath(new URL('../web/', import.meta.url));
let signedIn = true, expireNext = false, rejectNext = false, rejectDelete = false, sequence = 0;
const conversations = new Map(), submissions = [], creations = [];
const answer = '<p>Marmelade ist ein süßer Aufstrich aus Früchten und Zucker.</p><h2>Wichtige Merkmale</h2><ul><li>Früchte</li><li>Zucker</li></ul><p><strong>Gut zu wissen:</strong> Walter kann auch Tabellen und Code darstellen.</p><table><thead><tr><th>Zutat</th><th>Menge</th></tr></thead><tbody><tr><td>Früchte</td><td>1 kg</td></tr></tbody></table><pre><code>&lt;script&gt;kein ausführbarer Code&lt;/script&gt;\n' + 'langes_wort_'.repeat(30) + '</code></pre>';
const json = (res, code, body) => { res.writeHead(code, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(body)); };
const server = createServer(async (req, res) => {
  try {
    res.setHeader('Content-Security-Policy', "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'");
    if (req.url === '/api/site') return json(res, 200, { imprint_url: 'https://example.com/impressum', privacy_url: 'https://example.com/datenschutz' });
    if (!req.url.startsWith('/api/') && !req.url.startsWith('/auth/')) {
      const filename = req.url === '/' ? 'index.html' : req.url.slice(1);
      if (!['index.html', 'app.js', 'style.css', 'sse.js', 'walter.svg'].includes(filename)) return json(res, 404, {});
      res.setHeader('Content-Type', filename.endsWith('.js') ? 'text/javascript' : filename.endsWith('.css') ? 'text/css' : filename.endsWith('.svg') ? 'image/svg+xml' : 'text/html');
      return res.end(await readFile(join(root, filename)));
    }
    if (expireNext) { expireNext = false; signedIn = false; }
    if (!signedIn) return json(res, 401, { error: 'Bitte erneut anmelden.' });
    if (req.url === '/api/session') return json(res, 200, { csrf: 'csrf' });
    if (req.method === 'POST' && req.url === '/auth/logout') { signedIn = false; return json(res, 200, {}); }
    if (req.method !== 'GET') assert.equal(req.headers['x-csrf-token'], 'csrf');
    let body = '';
    for await (const chunk of req) body += chunk;
    body = body ? JSON.parse(body) : {};
    if (req.url === '/api/conversations') {
      if (req.method === 'GET') return json(res, 200, [...conversations.values()].reverse());
      assert.deepEqual(Object.keys(body), ['thinking']);
      assert.ok(['quick', 'thoughtful'].includes(body.thinking));
      creations.push(body);
      const c = { id: String(++sequence).padStart(32, '0'), title: 'Neue Frage', thinking: body.thinking, updated_at: '2026-10-16T18:00:00Z', messages: [] };
      conversations.set(c.id, c);
      return json(res, 201, c);
    }
    const match = req.url.match(/^\/api\/conversations\/([^/]+)(\/messages)?$/);
    const c = match && conversations.get(match[1]);
    if (!c) return json(res, 404, { error: 'Gespräch nicht gefunden.' });
    if (req.method === 'GET') return json(res, 200, c);
    if (req.method === 'DELETE') {
      if (rejectDelete) { rejectDelete = false; return json(res, 503, { error: 'Löschen fehlgeschlagen.' }); }
      conversations.delete(c.id); res.writeHead(204); return res.end();
    }
    if (rejectNext) { rejectNext = false; return json(res, 503, { error: 'Bitte später erneut versuchen.' }); }
    submissions.push(body.content);
    c.title = body.content.slice(0, 60);
    const reply = { role: 'assistant', content: 'Marmelade', html: '<p>Marmelade</p>', status: 'pending' };
    c.messages.push({ role: 'user', content: body.content, status: 'complete' }, reply);
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const event = (name, data) => res.write(`event: ${name}\ndata: ${JSON.stringify(data)}\n\n`);
    event('start', {});
    event('snapshot', { html: reply.html });
    const update = setTimeout(() => { reply.html = '<p><strong>Marmelade</strong> ist ein Aufstrich.</p>'; event('snapshot', { html: reply.html }); }, 100);
    const finish = setTimeout(() => {
      reply.html = answer; reply.status = 'complete';
      event('snapshot', { html: answer }); event('done', {}); res.end();
    }, 500);
    res.on('close', () => {
      clearTimeout(update); clearTimeout(finish);
      if (reply.status === 'pending') reply.status = 'cancelled';
    });
  } catch (e) { console.error(e); if (!res.headersSent) json(res, 500, { error: 'Testserver-Fehler' }); else res.end(); }
});
server.listen(0, '127.0.0.1');
await once(server, 'listening');
const origin = `http://127.0.0.1:${server.address().port}`;
const browser = await chromium.launch({ headless: true, ...(process.env.WALTER_BROWSER_EXECUTABLE ? { executablePath: process.env.WALTER_BROWSER_EXECUTABLE } : {}), args: ['--no-sandbox', '--disable-dev-shm-usage'] });
const errors = [];
const page = await browser.newPage({ viewport: { width: 1071, height: 1579 } });
page.on('pageerror', e => errors.push(e.message));
const state = name => page.waitForFunction(name => document.body.dataset.view === name, name);
const idle = () => page.waitForFunction(() => document.querySelector('#composer').getAttribute('aria-busy') === 'false' && !document.querySelector('#content').disabled);
const inputFocused = () => page.waitForFunction(() => document.activeElement === document.querySelector('#content'));
async function screenshot(name) {
  if (!process.env.WALTER_SCREENSHOT_DIR) return;
  await mkdir(process.env.WALTER_SCREENSHOT_DIR, { recursive: true });
  await page.screenshot({ path: join(process.env.WALTER_SCREENSHOT_DIR, `${name}.png`), fullPage: true });
}
async function noOverflow() { assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'page overflows viewport'); }
try {
  await page.goto(origin);
  await state('start');
  await inputFocused();
  assert.equal(await page.locator('aside').count(), 0);
  await page.locator('#content').fill('Was ist Marmelade?');
  await screenshot('start-desktop');
  await page.locator('#thoughtful').click();
  await state('conversation');
  assert.equal(await page.locator('#stop').isVisible(), true);
  await page.locator('#history-link').dispatchEvent('click');
  await state('conversation'); // Navigation is locked until persistence finishes.
  await idle();
  assert.deepEqual(creations[0], { thinking: 'thoughtful' });
  assert.equal(await page.locator('.assistant table').count(), 1);
  assert.equal(await page.locator('#quick').isVisible(), false);
  assert.equal(await page.locator('#thoughtful').isVisible(), true);
  await page.locator('.skip-link').focus();
  await page.keyboard.press('Enter');
  assert.equal(await page.locator('.message').count(), 2);
  await noOverflow();
  await screenshot('conversation-desktop');
  const conversationURL = page.url();
  await page.locator('#content').fill('<img src=x onerror=alert(1)>');
  await page.locator('#thoughtful').click();
  await idle();
  assert.equal(await page.locator('.user img').count(), 0);
  assert.equal(await page.locator('.message').count(), 4);
  assert.equal(creations.length, 1);
  await page.locator('#history-link').click();
  await state('history');
  await page.waitForSelector('#chats a');
  assert.equal(await page.locator('#chats a').count(), 1);
  assert.match(await page.locator('#chats time').textContent(), /16. Oktober 2026/);
  await screenshot('history-desktop');
  await page.locator('#chats a').click();
  await state('conversation');
  await page.reload();
  await state('conversation');
  assert.equal(page.url(), conversationURL);
  assert.equal(await page.locator('.message').count(), 4);
  // Stop must persist the partial reply and never resend the message.
  await page.locator('#content').fill('Noch eine Frage');
  await page.locator('#thoughtful').click();
  await page.waitForFunction(() => document.querySelectorAll('.assistant').length === 3);
  await page.locator('#stop').click();
  await idle();
  assert.equal(submissions.length, 3);
  assert.equal(await page.locator('.assistant').last().locator('small').textContent(), 'Abgebrochen');
  await page.locator('#delete').click();
  await page.getByRole('button', { name: 'Behalten', exact: true }).click();
  assert.equal(conversations.size, 1);
  await page.locator('#delete').click();
  await page.locator('#confirm-delete').click();
  await state('history');
  await page.waitForFunction(() => document.querySelector('#history-status').textContent.startsWith('Hier findest'));
  assert.equal(conversations.size, 0);
  await screenshot('history-empty');
  await page.locator('#new').click();
  await state('start');
  await inputFocused();
  rejectNext = true;
  await page.locator('#content').fill('Frage bleibt erhalten');
  await page.locator('#quick').click();
  await page.waitForSelector('#draft-hint:not([hidden])');
  assert.equal(await page.locator('#content').inputValue(), 'Frage bleibt erhalten');
  assert.equal(submissions.length, 3);
  await page.locator('#quick').click();
  await idle();
  assert.equal(creations.length, 2); // Retry reuses the already-created empty conversation.
  assert.equal(submissions.length, 4);
  assert.equal(await page.locator('#quick').isVisible(), true);
  assert.equal(await page.locator('#thoughtful').isVisible(), false);
  for (const width of [320, 390, 768]) {
    await page.setViewportSize({ width, height: 844 });
    await noOverflow();
    await screenshot(`conversation-${width}`);
  }
  await page.locator('#new').click();
  await state('start');
  await inputFocused();
  await page.setViewportSize({ width: 390, height: 844 });
  await noOverflow();
  await screenshot('start-mobile');
  await page.locator('#history-link').click();
  await state('history');
  await noOverflow();
  await screenshot('history-mobile');
  // Delete without opening the conversation, including cancellation and errors.
  const retained = [...conversations.keys()][0];
  const removable = { id: String(++sequence).padStart(32, '0'), title: 'Nur dieses Gespräch', thinking: 'quick', updated_at: '2026-10-16T18:00:00Z', messages: [] };
  conversations.set(removable.id, removable);
  await page.locator('#history-link').click();
  await page.waitForFunction(() => document.querySelectorAll('#chats li').length === 2);
  const remove = page.getByRole('button', { name: 'Löschen: Nur dieses Gespräch', exact: true });
  await remove.click();
  await page.getByRole('button', { name: 'Behalten', exact: true }).click();
  assert.equal(conversations.size, 2);
  rejectDelete = true;
  await remove.click();
  await page.locator('#confirm-delete').click();
  await page.waitForSelector('#error:not([hidden])');
  assert.equal(conversations.size, 2);
  assert.equal(await page.locator('#chats li').count(), 2);
  await page.locator('#dismiss-error').click();
  await remove.click();
  await page.locator('#confirm-delete').click();
  await page.waitForFunction(() => document.querySelectorAll('#chats li').length === 1);
  assert.ok(conversations.has(retained));
  assert.ok(!conversations.has(removable.id));
  assert.equal(new URL(page.url()).hash, '#historie');
  for (const width of [320, 390]) {
    await page.setViewportSize({ width, height: 844 });
    await noOverflow();
  }
  await page.locator('#chats button').click();
  await page.locator('#confirm-delete').click();
  await page.waitForFunction(() => document.querySelector('#history-status').textContent.startsWith('Hier findest'));
  assert.equal(conversations.size, 0);
  await page.locator('#history .text-link').click();
  await state('start');
  await inputFocused();
  await page.locator('#home').click();
  await inputFocused();
  await page.locator('#history-link').click();
  await state('history');
  expireNext = true;
  await page.locator('#history-link').click();
  await state('login');
  assert.equal(await page.locator('#account-menu').isVisible(), false);
  assert.equal(await page.locator('.message').count(), 0);
  await screenshot('login-mobile');
  signedIn = true;
  await page.goto(origin);
  await state('start');
  await inputFocused();
  await page.locator('#logout').click();
  await state('login');
  assert.equal(signedIn, false);
  await page.setViewportSize({ width: 1071, height: 1579 });
  await screenshot('login-desktop');
  assert.deepEqual(errors, []);
  console.log('UI passed: both modes, streaming, follow-up, stop, history, deep links, delete/cancel, retry, session expiry, logout and responsive overflow.');
} finally {
  await browser.close();
  server.closeAllConnections();
  await new Promise(resolve => server.close(resolve));
}
