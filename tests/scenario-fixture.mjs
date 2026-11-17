// Synthetic HTTP fixture for scenario captures; it never starts Walter or changes production authentication.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { once } from 'node:events';

const assets = new Set([
  'index.html', 'app.js', 'sse.js', 'style.css', 'oat.css',
  'archaic-theme.css', 'walter.svg', 'headline-overlay.svg',
]);
const history = [
  { id: '00000000000000000000000000000001', title: 'Was ist Marmelade?', thinking: 'quick',
    updated_at: '2026-10-16T18:00:00Z', messages: [] },
  { id: '00000000000000000000000000000002', title: 'Ein Beispiel mit Tabellen',
    thinking: 'thoughtful', updated_at: '2026-10-15T09:00:00Z', messages: [] },
];
const json = (res, status, value) => {
  res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8' });
  res.end(JSON.stringify(value));
};

export async function createScenarioServer(fixture, webRoot) {
  assert.ok(['empty-chat', 'streaming-reply', 'history-with-conversations'].includes(fixture));
  let created = false;
  const conversation = {
    id: '00000000000000000000000000000003', title: 'Was ist Marmelade?',
    thinking: 'thoughtful', updated_at: '2026-10-16T18:00:00Z', messages: [],
  };
  const server = createServer(async (req, res) => {
    try {
      res.setHeader('Cache-Control', 'no-store');
      res.setHeader('Content-Security-Policy',
        "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'");
      const path = new URL(req.url, 'http://localhost').pathname;
      if (path === '/api/site') return json(res, 200, {});
      if (path === '/api/session') return json(res, 200, { csrf: 'scenario-token' });
      if (path === '/api/conversations' && req.method === 'GET') {
        return json(res, 200, fixture === 'history-with-conversations' ? history : []);
      }
      if (path === '/api/conversations' && req.method === 'POST' && fixture === 'streaming-reply') {
        assert.equal(req.headers['x-csrf-token'], 'scenario-token');
        const chunks = [];
        for await (const chunk of req) chunks.push(chunk);
        assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), { thinking: 'thoughtful' });
        created = true;
        return json(res, 201, conversation);
      }
      if (path === '/api/conversations/' + conversation.id + '/messages' &&
          req.method === 'POST' && fixture === 'streaming-reply' && created) {
        assert.equal(req.headers['x-csrf-token'], 'scenario-token');
        const chunks = [];
        for await (const chunk of req) chunks.push(chunk);
        assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()),
          { content: 'Was ist Marmelade?', thinking: 'thoughtful' });
        res.writeHead(200, { 'Content-Type': 'text/event-stream; charset=utf-8' });
        // Keep the first response pending. Closing the browser context ends the stream.
        res.write('event: start\ndata: {}\n\n');
        return;
      }
      if (path.startsWith('/api/') || path.startsWith('/auth/')) {
        return json(res, 404, { error: 'Unknown scenario request' });
      }
      const filename = path === '/' ? 'index.html' : path.slice(1);
      if (!assets.has(filename)) return json(res, 404, {});
      res.setHeader('Content-Type', filename.endsWith('.js') ? 'text/javascript'
        : filename.endsWith('.css') ? 'text/css'
        : filename.endsWith('.svg') ? 'image/svg+xml' : 'text/html');
      res.end(await readFile(join(webRoot, filename)));
    } catch (error) {
      console.error('Scenario fixture failed:', error);
      if (!res.headersSent) json(res, 500, { error: 'Scenario fixture failed' });
      else res.destroy(error);
    }
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  return {
    origin: 'http://127.0.0.1:' + server.address().port,
    async close() {
      server.closeAllConnections();
      await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
    },
  };
}
