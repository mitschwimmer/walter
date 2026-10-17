import { readEvents } from './sse.js';

const $ = id => document.getElementById(id);
let csrf, authenticated = false, current = null, controller = null;
let routeVersion = 0, activeHash = '', recovering = false;
let deleteTarget = null;

function error(e) { $('error-message').textContent = e.message; $('error').hidden = false; }
function clearError() { $('error').hidden = true; }
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
  const response = await fetch(path, { ...options, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf || '', ...options.headers } });
  if (response.status === 401) signedOut();
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new Error(body.error || 'Anfrage fehlgeschlagen. Bitte erneut versuchen.');
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
function bubble(m) {
  const section = document.createElement('section');
  section.className = `message ${m.role}`;
  section.setAttribute('aria-label', m.role === 'user' ? 'Deine Frage' : 'Walters Antwort');
  const body = document.createElement('div');
  body.className = 'message-content';
  // Only the backend's restricted Markdown renderer produces this HTML.
  if (m.role === 'assistant' && typeof m.html === 'string') body.innerHTML = m.html;
  else body.textContent = m.content;
  if (m.role === 'assistant' && m.status === 'pending' && !body.hasChildNodes()) {
    const loader = document.createElement('div');
    loader.className = `response-loader turn-${Math.floor(Math.random() * 4)} tempo-${Math.floor(Math.random() * 3)} tone-${Math.floor(Math.random() * 3)}`;
    loader.setAttribute('role', 'status');
    loader.setAttribute('aria-label', 'Walter bereitet die Antwort vor.');
    body.append(loader);
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
  $('conversation-tools').hidden = !current;
  $('conversation-title').textContent = current?.title || 'Neue Frage';
  $('mode-label').textContent = current?.thinking === 'thoughtful' ? 'bedacht' : 'schnell';
  $('composer-label').textContent = started ? 'Deine nächste Frage' : 'Deine Frage';
  $('content').placeholder = started ? 'Was möchtest du noch wissen?' : 'Was möchtest du wissen?';
  for (const id of ['quick', 'thoughtful']) $(id).hidden = !!current && current.thinking !== id;
  busy(!!controller);
}
function show(c) {
  current = c;
  $('messages').replaceChildren();
  c.messages.forEach(bubble);
  view(c.messages.length ? 'conversation' : 'start');
  updateComposer();
}
function busy(value) {
  for (const id of ['quick', 'thoughtful', 'delete', 'content', 'logout', 'confirm-delete']) $(id).disabled = value || recovering;
  $('chats').querySelectorAll('button').forEach(button => button.disabled = value || recovering);
  for (const id of ['home', 'new', 'history-link']) {
    if (value || recovering) $(id).setAttribute('aria-disabled', 'true');
    else $(id).removeAttribute('aria-disabled');
  }
  $('stop').hidden = !value;
  $('composer').setAttribute('aria-busy', String(value));
  $('activity').textContent = value ? 'Walter antwortet …' : recovering ? 'Gespräch wird geladen …' : '';
  $('activity').classList.toggle('sr-only', value);
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
    link.append(title, date);
    const remove = document.createElement('button');
    remove.type = 'button';
    remove.className = 'delete-conversation';
    remove.textContent = 'Löschen';
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
      const c = await (await api(`/api/conversations/${id}`)).json();
      if (!c.messages.some(m => m.status === 'pending')) { show(c); return; }
      if (attempt === 19) { show(c); throw new Error('Antwort noch nicht abgeschlossen. Bitte das Gespräch später erneut öffnen.'); }
      await new Promise(resolve => setTimeout(resolve, 250));
    }
  } finally { recovering = false; busy(false); }
}
$('composer').onsubmit = async e => {
  e.preventDefault();
  if (controller || recovering || !authenticated) return;
  const text = $('content').value.trim();
  if (!text) { $('content').focus(); return; }
  const thinking = current?.thinking || e.submitter?.value || 'quick';
  clearError();
  $('draft-hint').hidden = true;
  controller = new AbortController();
  busy(true);
  let accepted = false, complete = false, target, id;
  try {
    if (!current) {
      current = await (await api('/api/conversations', { method: 'POST', body: JSON.stringify({ thinking }) })).json();
      activeHash = `#gespraech/${current.id}`;
      history.replaceState(null, '', activeHash);
      updateComposer();
    }
    id = current.id;
    const response = await api(`/api/conversations/${id}/messages`, { method: 'POST', body: JSON.stringify({ content: text }), signal: controller.signal });
    accepted = true;
    $('content').value = '';
    bubble({ role: 'user', content: text, status: 'complete' });
    target = bubble({ role: 'assistant', content: '', status: 'pending' });
    view('conversation');
    $('conversation-tools').hidden = false;
    $('content').placeholder = 'Was möchtest du noch wissen?';
    $('composer-label').textContent = 'Deine nächste Frage';
    await readEvents(response.body, (name, data) => {
      if (name === 'snapshot') target.innerHTML = data.html;
      else if (name === 'error') throw new Error(data.error);
      else if (name === 'done') complete = true;
    });
    if (!complete) throw new Error('Verbindung unterbrochen. Bitte das Gespräch erneut öffnen.');
  } catch (e) {
    if (e.name !== 'AbortError') error(e);
    if (!accepted && authenticated) $('draft-hint').hidden = false;
  } finally {
    controller = null;
    busy(false);
    if (accepted && authenticated) {
      try { await reloadReply(id); } catch (e) { error(e); }
    }
    if (authenticated) $('content').focus({ preventScroll: true });
  }
};
async function loadSite() {
  try {
    const response = await fetch('/api/site');
    if (!response.ok) return;
    const site = await response.json();
    if (site.revision) {
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
