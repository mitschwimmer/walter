import { readEvents } from './sse.js';

const $ = id => document.getElementById(id);
let csrf, authenticated = false, current = null, controller = null;
let routeVersion = 0, activeHash = '', recovering = false;
let deleteTarget = null;
let siteBuild = '';

class WalterError extends Error {
  constructor(code, message, response = null) {
    super(message);
    this.code = code;
    const id = response?.headers.get('X-Walter-Request-ID');
    this.requestID = /^[a-f0-9]{32}$/.test(id || '') ? id : '';
    this.build = response?.headers.get('X-Walter-Build') || '';
    this.httpStatus = response?.status;
  }
}
function error(e) {
  const failure = e instanceof WalterError ? e : new WalterError('unexpected_error', 'Etwas ist schiefgelaufen. Bitte versuche es erneut.');
  $('error-message').textContent = failure.message;
  $('error-details').textContent = [
    'Fehler: ' + failure.code,
    'Build: ' + (failure.build || siteBuild || 'unbekannt'),
    'Request-ID: ' + (failure.requestID || 'nicht verfügbar'),
    'HTTP-Status: ' + (failure.httpStatus ?? 'nicht verfügbar'),
    'Zeit (UTC): ' + new Date(Date.now()).toISOString(),
  ].join('\n');
  $('copy-status').textContent = '';
  $('error').hidden = false;
}
function clearError() {
  $('error').hidden = true;
  $('error-details').textContent = '';
  $('copy-status').textContent = '';
}
function signedOut() {
  authenticated = false;
  current = null;
  deleteTarget = null;
  $('messages').replaceChildren();
  $('chats').replaceChildren();
  $('content').value = '';
  $('delete-dialog').close();
  $('account-menu').hidden = true;
  view('login');
}
async function api(path, options = {}) {
  let response;
  try {
    response = await fetch(path, { ...options, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf || '', ...options.headers } });
  } catch (e) {
    if (options.signal?.aborted) throw e;
    throw new WalterError('connection_failed', 'Walter ist gerade nicht erreichbar. Bitte versuche es erneut.');
  }
  if (response.status === 401) signedOut();
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    const failure = new WalterError('http_error', 'Anfrage fehlgeschlagen. Bitte versuche es erneut.', response);
    // Only Walter's own controlled problem responses, never a proxy's error body.
    if (failure.requestID && typeof body?.error === 'string') failure.message = body.error;
    throw failure;
  }
  return response;
}
function view(name) {
  document.body.dataset.view = name;
  $('loading').hidden = name !== 'loading';
  $('login').hidden = name !== 'login';
  $('chat').hidden = name !== 'start' && name !== 'conversation';
  $('history').hidden = name !== 'history';
  $('new').hidden = name === 'start';
  if (name === 'history') $('history-link').setAttribute('aria-current', 'page');
  else $('history-link').removeAttribute('aria-current');
  document.title = name === 'history' ? 'Historie · Walter' : current ? `${current.title} · Walter` : 'Walter · Frag’s Pferd.';
}
// Static UI icons; message content continues to use the restricted server renderer.
function uiIcon(pathData) {
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  for (const [name, value] of Object.entries({
    class: 'icon', 'aria-hidden': 'true', focusable: 'false', viewBox: '0 0 24 24',
    fill: 'none', stroke: 'currentColor', 'stroke-width': '1.8',
    'stroke-linecap': 'round', 'stroke-linejoin': 'round'
  })) svg.setAttribute(name, value);
  const path = document.createElementNS(ns, 'path');
  path.setAttribute('d', pathData);
  svg.append(path);
  return svg;
}
function bubble(m) {
  const section = document.createElement(m.role === 'assistant' ? 'article' : 'section');
  section.className = `message ${m.role}`;
  section.setAttribute('aria-label', m.role === 'user' ? 'Deine Frage' : 'Walters Antwort');
  const body = document.createElement('div');
  body.className = 'message-content';
  // Only the backend's restricted Markdown renderer produces this HTML.
  if (m.role === 'assistant' && typeof m.html === 'string') body.innerHTML = m.html;
  else body.textContent = m.content;
  if (m.role === 'assistant' && m.status === 'pending' && !body.hasChildNodes()) {
    const loader = document.createElement('div');
    loader.className = `response-loader turn-${Math.floor(Math.random() * 4)} tempo-${Math.floor(Math.random() * 3)}`;
    loader.setAttribute('role', 'status');
    loader.setAttribute('aria-label', 'Walter bereitet die Antwort vor.');
    section.classList.add('waiting');
    const status = document.createElement('div');
    status.className = 'response-status';
    status.textContent = 'Warte auf das Modell …';
    loader.append(status);
    body.append(loader);
  }
  if (m.role === 'assistant') {
    const speaker = document.createElement('header');
    speaker.className = 'speaker';
    speaker.setAttribute('aria-hidden', 'true');
    const horse = document.createElement('img');
    horse.src = '/walter.svg';
    horse.alt = '';
    horse.width = horse.height = 40;
    speaker.append(horse, 'Walter');
    section.append(speaker);
  }
  section.append(body);
  if (m.status !== 'complete' && m.status !== 'pending') {
    const status = document.createElement('small');
    status.textContent = { pending: 'Wird erstellt …', partial: 'Unvollständige Antwort', cancelled: 'Abgebrochen', failed: 'Fehlgeschlagen' }[m.status] || 'Unvollständige Antwort';
    section.append(status);
  }
  $('messages').append(section);
  return body;
}
function updateComposer() {
  const started = current?.messages.length > 0;
  document.querySelector('.conversation-heading').hidden = !started;
  $('conversation-title').textContent = current?.title || 'Neue Frage';
  $('composer-label').textContent = started ? 'Deine nächste Frage' : 'Deine Frage';
  $('content').placeholder = started ? 'Was möchtest du noch wissen?' : 'Was möchtest du wissen?';
  busy(!!controller);
}
function show(c) {
  current = c;
  $('messages').replaceChildren();
  c.messages.forEach(bubble);
  view(c.messages.length ? 'conversation' : 'start');
  updateComposer();
}
function contextFull(mode) {
  const usage = current?.context;
  return usage?.capacity > 0 && Number.isInteger(usage[mode]) && usage[mode] >= usage.capacity;
}
function updateContext(unavailable) {
  const completed = current?.messages.at(-1)?.status === 'complete' && current.messages.at(-1)?.role === 'assistant';
  $('context-info').hidden = unavailable || !completed;
  const usage = current?.context;
  const used = usage?.[current?.thinking];
  const format = new Intl.NumberFormat('de-DE');
  $('context-info').textContent = usage?.capacity > 0 && Number.isInteger(used)
    ? `Kontext: ${Math.min(100, Math.floor(100 * used / usage.capacity))}% · ${format.format(used)} / ${format.format(usage.capacity)} Tokens`
    : 'Kontext: unbekannt';
  $('context-full').hidden = unavailable || !contextFull('quick') || !contextFull('thoughtful');
}
function updateIdeas() {
  const input = $('content');
  document.querySelectorAll('.idea').forEach(button => {
    button.disabled = input.disabled || input.value.trim() !== '';
  });
}
$('content').addEventListener('input', updateIdeas);
document.querySelectorAll('.idea').forEach(button => {
  button.addEventListener('click', () => {
    const input = $('content');
    if (input.disabled || input.value.trim() || document.body.dataset.view !== 'start') return;
    input.value = button.dataset.question;
    updateIdeas();
    input.focus();
  });
});
function busy(value) {
  const unavailable = value || recovering || !!current?.messages.some(m => m.status === 'pending');
  const full = contextFull('quick') && contextFull('thoughtful');
  $('prompt-input').hidden = unavailable || full;
  updateContext(unavailable);
  for (const id of ['quick', 'thoughtful']) $(id).hidden = unavailable;
  for (const id of ['quick', 'thoughtful', 'delete', 'content', 'logout', 'confirm-delete']) $(id).disabled = unavailable;
  $('content').disabled = unavailable || full;
  for (const mode of ['quick', 'thoughtful']) {
    $(mode).disabled = unavailable || contextFull(mode);
    $(mode).title = contextFull(mode) ? 'Kontext voll – bitte eine neue Frage starten.' : mode === 'quick' ? 'Schnell antworten' : 'Bedacht antworten';
  }
  $('chats').querySelectorAll('button').forEach(button => button.disabled = value || recovering);
  for (const id of ['home', 'new', 'history-link']) {
    if (value || recovering) $(id).setAttribute('aria-disabled', 'true');
    else $(id).removeAttribute('aria-disabled');
  }
  $('stop').hidden = !value;
  $('composer').setAttribute('aria-busy', String(unavailable));
  $('activity').textContent = value ? 'Walter antwortet …' : recovering ? 'Gespräch wird geladen …' : '';
  $('activity').classList.toggle('sr-only', value);
  updateIdeas();
}
const dates = new Intl.DateTimeFormat('de-DE', { day: 'numeric', month: 'long', year: 'numeric' });
async function refreshHistory(version) {
  $('history-status').textContent = 'Gespräche werden geladen …';
  $('chats').replaceChildren();
  const list = await (await api('/api/conversations')).json();
  if (version !== routeVersion || !authenticated) return;
  $('history-status').textContent = list.length ? '' : 'Hier findest du deine Gespräche. Stell Walter deine erste Frage.';
  for (const c of list) {
    const item = document.createElement('li');
    const link = document.createElement('a');
    link.href = `#gespraech/${encodeURIComponent(c.id)}`;
    const title = document.createElement('span');
    title.className = 'conversation-title';
    title.textContent = c.title;
    const date = document.createElement('time');
    date.dateTime = c.updated_at;
    const updated = new Date(c.updated_at);
    date.textContent = Number.isNaN(updated.valueOf()) ? '' : dates.format(updated);
    link.append(uiIcon('M21 11.5a8.5 8.5 0 0 1-8.5 8.5H12a9 9 0 0 1-4-.9L3 21l1.9-5a9 9 0 0 1-.9-4v-.5A8.5 8.5 0 0 1 12.5 3h.5a8.5 8.5 0 0 1 8 8.5Z'), title, date);
    const remove = document.createElement('button');
    remove.type = 'button';
    remove.className = 'delete-conversation round-action';
    remove.append(uiIcon('M3 6h18M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2m3 0-1 14a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1L5 6m5 4v7m4-7v7'));
    remove.title = 'Gespräch löschen';
    remove.setAttribute('aria-label', `Löschen: ${c.title}`);
    remove.disabled = !!controller || recovering;
    remove.onclick = () => confirmDelete(c);
    item.append(link, remove);
    $('chats').append(item);
  }
}
async function route() {
  if (!authenticated) return;
  if (controller || recovering) {
    history.replaceState(null, '', activeHash || location.pathname);
    return;
  }
  const version = ++routeVersion;
  activeHash = location.hash;
  clearError();
  $('draft-hint').hidden = true;
  try {
    if (location.hash === '#historie') {
      view('history');
      $('history-title').focus({ preventScroll: true });
      await refreshHistory(version);
    } else if (location.hash.startsWith('#gespraech/')) {
      view('loading');
      const id = decodeURIComponent(location.hash.slice('#gespraech/'.length));
      const c = await (await api(`/api/conversations/${encodeURIComponent(id)}`)).json();
      if (version !== routeVersion || !authenticated) return;
      $('content').value = '';
      show(c);
      $(c.messages.length ? 'main' : 'content').focus({ preventScroll: true });
    } else {
      current = null;
      $('messages').replaceChildren();
      $('content').value = '';
      view('start');
      updateComposer();
      $('content').focus({ preventScroll: true });
      if (location.hash && location.hash !== '#main') history.replaceState(null, '', location.pathname);
      activeHash = location.hash;
    }
  } catch (e) {
    if (version !== routeVersion) return;
    if (authenticated) {
      view('history');
      $('history-status').textContent = 'Die Gespräche konnten nicht geladen werden. Öffne die Historie erneut.';
    }
    error(e);
  }
}
window.addEventListener('hashchange', route);
document.querySelector('.skip-link').addEventListener('click', e => {
  e.preventDefault();
  $('main').focus();
  $('main').scrollIntoView();
});
for (const id of ['home', 'new', 'history-link']) {
  $(id).addEventListener('click', e => {
    if (controller || recovering) { e.preventDefault(); return; }
    if ($(id).hash === location.hash || (!$(id).hash && !location.hash)) {
      e.preventDefault();
      route();
    }
  });
}
$('dismiss-error').onclick = clearError;
$('copy-error').onclick = async () => {
  const report = ['Walter-Fehlerbericht', $('error-details').textContent].join('\n');
  try {
    await navigator.clipboard.writeText(report);
    if (!$('error').hidden && report.endsWith($('error-details').textContent)) $('copy-status').textContent = 'Kopiert.';
  } catch {
    if (!$('error').hidden) $('copy-status').textContent = 'Kopieren nicht möglich. Markiere den Fehlerbericht und kopiere ihn manuell.';
  }
};
function confirmDelete(conversation) {
  if (!conversation || controller || recovering) return;
  deleteTarget = conversation;
  $('delete-dialog').returnValue = '';
  $('delete-dialog').querySelector('p').textContent = `„${conversation.title}“ wird endgültig gelöscht.`;
  $('delete-dialog').showModal();
}
$('delete').onclick = () => confirmDelete(current);
$('delete-dialog').addEventListener('close', async () => {
  const target = deleteTarget;
  deleteTarget = null;
  if ($('delete-dialog').returnValue !== 'delete' || !target || !authenticated || controller || recovering) return;
  recovering = true;
  busy(false);
  try {
    await api(`/api/conversations/${target.id}`, { method: 'DELETE' });
    if (current?.id === target.id) {
      current = null;
      $('messages').replaceChildren();
    }
    if (location.hash === '#historie') {
      await refreshHistory(routeVersion);
      if (authenticated) $('history-title').focus({ preventScroll: true });
    }
    else location.hash = 'historie';
  } catch (e) { error(e); }
  finally { recovering = false; busy(false); }
});
$('stop').onclick = () => controller?.abort();
$('logout').onclick = async () => {
  try { await api('/auth/logout', { method: 'POST' }); signedOut(); history.replaceState(null, '', location.pathname); }
  catch (e) { error(e); }
};

async function reloadReply(id) {
  recovering = true;
  busy(false);
  try {
    // Stop closes the browser stream before the server commits. Poll only the
    // persisted placeholder; a message is never automatically resubmitted.
    for (let attempt = 0; attempt < 20; attempt++) {
      const response = await api(`/api/conversations/${id}`);
      const c = await response.json();
      if (!c.messages.some(m => m.status === 'pending')) { show(c); return; }
      if (attempt === 19) { show(c); throw new WalterError('reply_pending', 'Antwort noch nicht abgeschlossen. Bitte das Gespräch später erneut öffnen.', response); }
      await new Promise(resolve => setTimeout(resolve, 250));
    }
  } finally { recovering = false; busy(false); }
}
$('composer').onsubmit = async e => {
  e.preventDefault();
  if (controller || recovering || !authenticated) return;
  const text = $('content').value.trim();
  if (!text) { $('content').focus(); return; }
  // Never silently choose a thinking mode, including for keyboard submission.
  const thinking = e.submitter?.value;
  if (thinking !== 'quick' && thinking !== 'thoughtful') return;
  if (contextFull(thinking)) return;
  clearError();
  $('draft-hint').hidden = true;
  controller = new AbortController();
  busy(true);
  $('stop').focus({ preventScroll: true });
  let accepted = false, complete = false, target, id, response;
  try {
    if (!current) {
      current = await (await api('/api/conversations', { method: 'POST', body: JSON.stringify({ thinking }) })).json();
      activeHash = `#gespraech/${current.id}`;
      history.replaceState(null, '', activeHash);
      updateComposer();
    }
    id = current.id;
    response = await api(`/api/conversations/${id}/messages`, { method: 'POST', body: JSON.stringify({ content: text, thinking }), signal: controller.signal });
    accepted = true;
    current.thinking = thinking;
    $('content').value = '';
    bubble({ role: 'user', content: text, status: 'complete' });
    target = bubble({ role: 'assistant', content: '', status: 'pending' });
    view('conversation');
    document.querySelector('.conversation-heading').hidden = false;
    $('conversation-title').textContent = current.title || 'Gespräch';
    $('content').placeholder = 'Was möchtest du noch wissen?';
    $('composer-label').textContent = 'Deine nächste Frage';
    await readEvents(response.body, (name, data) => {
      if (name === 'snapshot') {
        target.innerHTML = data.html;
        target.parentElement.classList.toggle('waiting', !target.hasChildNodes());
      }
      else if (name === 'progress') {
        const status = target.querySelector('.response-status');
        if (!status) return;
        if (data.phase === 'prefill') status.textContent = `Prefill: ${data.percent ?? 0}%`;
        else if (data.phase === 'thinking') status.textContent = data.tokens > 0 ? `Denkt nach … · ${data.tokens} Tokens erzeugt` : 'Denkt nach …';
      }
      else if (name === 'error') throw new WalterError('reply_failed', 'Walter konnte seine Antwort nicht beenden. Bitte öffne das Gespräch erneut.', response);
      else if (name === 'done') complete = true;
    });
    if (!complete) throw new WalterError('stream_interrupted', 'Die Verbindung wurde unterbrochen. Bitte öffne das Gespräch erneut.', response);
  } catch (e) {
    if (!controller.signal.aborted) {
      if (e instanceof WalterError) error(e);
      else if (accepted) error(new WalterError('stream_interrupted', 'Die Verbindung wurde unterbrochen. Bitte öffne das Gespräch erneut.', response));
      else error(e);
    }
    if (!accepted && authenticated) $('draft-hint').hidden = false;
  } finally {
    controller = null;
    if (accepted && authenticated) {
      // Keep the original generation request ID if recovery also fails.
      try { await reloadReply(id); } catch (e) { if ($('error').hidden) error(e); }
    } else busy(false);
    if (authenticated && !$('prompt-input').hidden) $('content').focus({ preventScroll: true });
  }
};
async function loadSite() {
  try {
    const response = await fetch('/api/site');
    if (!response.ok) return;
    const site = await response.json();
    if (site.revision) {
      siteBuild = site.revision;
      $('revision').textContent = `Build ${site.revision}`;
      $('revision').hidden = false;
      $('footer').hidden = false;
    }
    for (const [id, url] of [['imprint', site.imprint_url], ['privacy', site.privacy_url]]) {
      if (!url) continue;
      const parsed = new URL(url);
      if (parsed.protocol !== 'https:') continue;
      $(id).href = parsed.href;
      $(id).hidden = false;
      $('footer').hidden = false;
    }
  } catch { /* Optional footer links must not prevent sign-in. */ }
}
loadSite();
try {
  const session = await (await api('/api/session')).json();
  csrf = session.csrf;
  authenticated = true;
  $('account-menu').hidden = false;
  await route();
} catch (e) { if ($('login').hidden) { view('login'); error(e); } }
