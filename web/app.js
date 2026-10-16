import { readEvents } from './sse.js';
const $ = id => document.getElementById(id);
let csrf, current = null, controller = null;
function error(e) { $('error').textContent = e.message; $('error').hidden = false; }
function clearError() { $('error').hidden = true; }
async function api(path, options = {}) {
  const response = await fetch(path, { ...options, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf || '', ...options.headers } });
  if (response.status === 401) { $('login').hidden = false; $('workspace').hidden = true; $('logout').hidden = true; }
  if (!response.ok) { const body = await response.json().catch(() => ({})); throw new Error(body.error || 'Anfrage fehlgeschlagen.'); }
  return response;
}
function bubble(m) {
  const section = document.createElement('section'); section.className = `message ${m.role}`;
  const heading = document.createElement('h2'); heading.textContent = m.role === 'user' ? 'Du' : 'Walter';
  const p = document.createElement('p'); p.textContent = m.content;
  section.append(heading, p);
  if (m.status !== 'complete') { const status = document.createElement('small'); status.textContent = { pending: 'Wird erstellt …', partial: 'Unvollständige Antwort', cancelled: 'Abgebrochen', failed: 'Fehlgeschlagen' }[m.status]; section.append(status); }
  $('messages').append(section); return p;
}
function show(c) {
  current = c; $('title').textContent = c.title;
  $('meta').textContent = `${c.language === 'de' ? 'Deutsch' : 'Englisch'} · ${c.thinking === 'quick' ? 'Schnell' : 'Gründlich'}`;
  $('messages').replaceChildren(); c.messages.forEach(bubble);
  $('delete').hidden = false; $('content').disabled = false; $('send').disabled = false;
}
async function refresh() {
  const list = await (await api('/api/conversations')).json(); $('chats').replaceChildren();
  for (const c of list) {
    const b = document.createElement('button'); b.textContent = c.title; b.setAttribute('aria-current', String(c.id === current?.id)); b.disabled = !!controller;
    b.onclick = () => load(c.id).catch(error); $('chats').append(b);
  }
}
async function load(id) { clearError(); show(await (await api(`/api/conversations/${id}`)).json()); await refresh(); }
function busy(value) {
  for (const id of ['new', 'delete', 'send', 'content', 'logout']) $(id).disabled = value;
  $('chats').querySelectorAll('button').forEach(b => b.disabled = value);
  $('stop').hidden = !value; $('activity').textContent = value ? 'Antwort wird erstellt …' : '';
}
$('new').onclick = () => $('create').showModal();
$('close').onclick = () => $('create').close();
$('create-form').onsubmit = async e => {
  e.preventDefault(); const submit = e.submitter; submit.disabled = true;
  try { clearError(); const values = Object.fromEntries(new FormData(e.target)); const c = await (await api('/api/conversations', { method: 'POST', body: JSON.stringify(values) })).json(); show(c); $('create').close(); await refresh(); $('content').focus(); } catch (e) { error(e); } finally { submit.disabled = false; }
};
$('delete').onclick = async () => {
  if (!current || !confirm('Diesen Chat endgültig löschen?')) return;
  try { await api(`/api/conversations/${current.id}`, { method: 'DELETE' }); current = null; $('messages').replaceChildren(); $('title').textContent = 'Ein neuer Gedanke?'; $('meta').textContent = 'Starte einen neuen Chat.'; $('delete').hidden = true; $('content').disabled = true; $('send').disabled = true; await refresh(); } catch (e) { error(e); }
};
$('stop').onclick = () => controller?.abort();
$('logout').onclick = async () => { try { await api('/auth/logout', { method: 'POST' }); location.reload(); } catch (e) { error(e); } };
$('composer').onsubmit = async e => {
  e.preventDefault(); if (!current || controller) return;
  const text = $('content').value.trim(); if (!text) return;
  clearError(); controller = new AbortController(); busy(true);
  let accepted = false, complete = false, frame = 0, output = '', target;
  try {
    const response = await api(`/api/conversations/${current.id}/messages`, { method: 'POST', body: JSON.stringify({ content: text }), signal: controller.signal });
    accepted = true; $('content').value = ''; bubble({ role: 'user', content: text, status: 'complete' }); target = bubble({ role: 'assistant', content: '', status: 'pending' });
    await readEvents(response.body, (name, data) => {
      if (name === 'delta') { output += data.text; if (!frame) frame = requestAnimationFrame(() => { target.textContent = output; frame = 0; }); }
      else if (name === 'error') throw new Error(data.error);
      else if (name === 'done') complete = true;
    });
    if (!complete) throw new Error('Verbindung unterbrochen. Chat erneut öffnen.');
  } catch (e) { if (e.name !== 'AbortError') error(e); }
  finally {
    if (frame) cancelAnimationFrame(frame); if (target) target.textContent = output;
    controller = null; busy(false);
    if (accepted) {
      // Cancellation closes the browser stream before the server commits. Poll
      // only while its persisted placeholder is pending; never resubmit a message.
      try {
        for (let attempt = 0; attempt < 20; attempt++) {
          const c = await (await api(`/api/conversations/${current.id}`)).json();
          if (!c.messages.some(m => m.status === 'pending')) { show(c); break; }
          if (attempt === 19) { show(c); throw new Error('Antwort noch nicht abgeschlossen. Bitte Chat später erneut öffnen.'); }
          await new Promise(resolve => setTimeout(resolve, 250));
        }
        await refresh();
      } catch (e) { error(e); }
    }
  }
};
try { const session = await (await api('/api/session')).json(); csrf = session.csrf; $('workspace').hidden = false; $('logout').hidden = false; await refresh(); } catch (e) { if ($('login').hidden) error(e); }
