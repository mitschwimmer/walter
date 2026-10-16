// Handles CRLF (including split CR/LF), LF, multiline data and UTF-8 boundaries.
export async function readEvents(body, onEvent) {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = '', name = 'message', data = [], afterCR = false;
  const line = value => {
    if (value === '') {
      if (data.length) onEvent(name, JSON.parse(data.join('\n')));
      name = 'message'; data = [];
    } else if (!value.startsWith(':')) {
      const colon = value.indexOf(':');
      const field = colon < 0 ? value : value.slice(0, colon);
      let valuePart = colon < 0 ? '' : value.slice(colon + 1);
      if (valuePart.startsWith(' ')) valuePart = valuePart.slice(1);
      if (field === 'event') name = valuePart;
      if (field === 'data') data.push(valuePart);
    }
  };
  try {
    while (true) {
      const { value, done } = await reader.read();
      const text = decoder.decode(value, { stream: !done });
      for (const ch of text) {
        if (afterCR) { afterCR = false; if (ch === '\n') continue; }
        if (ch === '\r' || ch === '\n') { line(buffer); buffer = ''; afterCR = ch === '\r'; }
        // Full HTML snapshots can grow beyond the 1 MiB source limit after JSON escaping.
        else { buffer += ch; if (buffer.length > 32 * 2 ** 20) throw new Error('Antwort zu groß.'); }
      }
      if (done) { if (buffer || data.length) throw new Error('Antwort unvollständig.'); return; }
    }
  } finally { reader.releaseLock(); }
}
