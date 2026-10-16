import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
const source = await readFile(new URL('../web/sse.js', import.meta.url), 'utf8');
const { readEvents } = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);
const text = ': comment\r\nevent: delta\r\ndata: {"text":"Gründlich 😀"}\r\n\r\nevent: done\ndata: {"message_id":\n' + 'data: "123"}\n\n';
const bytes = new TextEncoder().encode(text);
for (let size = 1; size <= bytes.length; size++) {
  const events = [];
  const stream = new ReadableStream({ start(controller) { for (let i = 0; i < bytes.length; i += size) controller.enqueue(bytes.slice(i, i + size)); controller.close(); } });
  await readEvents(stream, (...event) => events.push(event));
  assert.deepEqual(events, [['delta', { text: 'Gründlich 😀' }], ['done', { message_id: '123' }]]);
}
await assert.rejects(readEvents(new ReadableStream({ start(c) { c.enqueue(new TextEncoder().encode('data: {}')); c.close(); } }), () => {}), /unvollständig/);
console.log('SSE parser: all chunk sizes, UTF-8, CRLF, multiline and incomplete frames passed');
