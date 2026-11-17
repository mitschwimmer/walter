// Build a changed-only PR gallery from independently captured Chromium screenshots.
import assert from 'node:assert/strict';
import { readFile, readdir, mkdir, copyFile, writeFile, appendFile } from 'node:fs/promises';
import { dirname, join, resolve, basename } from 'node:path';

const escapeHTML = value => String(value).replace(/[&<>"']/g,
  char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]);
const screenshotName = /^[a-z0-9.-]+(?:--[a-z0-9-]+){3}\.png$/;

function options(argv) {
  const flags = new Set(['--base', '--candidate', '--base-defs', '--candidate-defs', '--out']);
  const args = {};
  for (let i = 0; i < argv.length; i += 2) {
    assert.ok(flags.has(argv[i]) && argv[i + 1], 'Expected --base, --candidate, --base-defs, --candidate-defs and --out');
    args[argv[i].slice(2)] = resolve(argv[i + 1]);
  }
  for (const key of ['base', 'candidate', 'base-defs', 'candidate-defs', 'out']) assert.ok(args[key]);
  return args;
}

async function scenarioDefinitions(path) {
  const filenames = await readdir(path).catch(error => error.code === 'ENOENT' ? [] : Promise.reject(error));
  const records = new Map();
  for (const file of filenames.filter(name => name.endsWith('.json'))) {
    const definition = JSON.parse(await readFile(join(path, file)));
    assert.equal(file, definition.id + '.json');
    records.set(definition.id, JSON.stringify(definition));
  }
  return records;
}
const manifest = async directory => JSON.parse(await readFile(join(directory, 'manifest.json')));

function imageMap(m) {
  const map = new Map();
  for (const entry of m.images) {
    assert.ok(screenshotName.test(entry.file) && basename(entry.file) === entry.file,
      'Invalid screenshot filename: ' + entry.file);
    assert.ok(!map.has(entry.file), 'Duplicate screenshot: ' + entry.file);
    map.set(entry.file, entry);
  }
  return map;
}

function pngDimensions(bytes) {
  assert.equal(bytes.subarray(1, 4).toString('ascii'), 'PNG');
  return [bytes.readUInt32BE(16), bytes.readUInt32BE(20)];
}

async function main() {
  const args = options(process.argv.slice(2));
  const [base, candidate, baseDefs, candidateDefs] = await Promise.all([
    manifest(args.base), manifest(args.candidate),
    scenarioDefinitions(args['base-defs']), scenarioDefinitions(args['candidate-defs']),
  ]);
  assert.equal(base.format, 1);
  assert.equal(candidate.format, 1);
  if (base.images.length) {
    for (const field of ['chromium', 'platform', 'locale', 'timezone', 'deviceScaleFactor', 'reducedMotion']) {
      assert.equal(base[field], candidate[field], 'Incompatible capture environment: ' + field);
    }
  }
  const baseImages = imageMap(base), candidateImages = imageMap(candidate);
  const baseFixtureFile = join(dirname(args['base-defs']), 'scenario-fixture.mjs');
  const candidateFixtureFile = join(dirname(args['candidate-defs']), 'scenario-fixture.mjs');
  const [baseFixture, candidateFixture] = await Promise.all([
    readFile(baseFixtureFile).catch(error => error.code === 'ENOENT' ? null : Promise.reject(error)),
    readFile(candidateFixtureFile),
  ]);
  const fixtureChanged = baseFixture !== null && !baseFixture.equals(candidateFixture);
  const rows = [];
  const counts = { unchanged: 0, changed: 0, added: 0, removed: 0, 'contract-changed': 0 };
  await mkdir(join(args.out, 'images/main'), { recursive: true });
  await mkdir(join(args.out, 'images/candidate'), { recursive: true });

  for (const entry of candidate.images) {
    const name = entry.file;
    const inBase = baseDefs.has(entry.id);
    const contractChanged = inBase &&
      (baseDefs.get(entry.id) !== candidateDefs.get(entry.id) || fixtureChanged);
    let status = !inBase ? 'added' : contractChanged ? 'contract-changed' : 'unchanged';
    let before = null, dimensionsMatch = false;
    const current = await readFile(join(args.candidate, name));
    const baseline = baseImages.get(name);
    if (status === 'unchanged') {
      assert.ok(baseline, 'Missing baseline for unchanged scenario contract: ' + name);
      before = await readFile(join(args.base, name));
      if (!before.equals(current)) {
        status = 'changed';
        dimensionsMatch = pngDimensions(before).join('x') === pngDimensions(current).join('x');
      }
    }
    counts[status]++;
    if (status === 'unchanged') continue;
    const afterFile = 'images/candidate/' + name;
    await copyFile(join(args.candidate, name), join(args.out, afterFile));
    let beforeFile = null;
    if (before) {
      beforeFile = 'images/main/' + name;
      await copyFile(join(args.base, name), join(args.out, beforeFile));
    }
    rows.push({ name, status, beforeFile, afterFile, dimensionsMatch });
  }

  for (const [id] of baseDefs) {
    if (candidateDefs.has(id)) continue;
    counts.removed++;
    rows.push({ name: id, status: 'removed', beforeFile: null, afterFile: null });
  }
  for (const entry of baseImages.values()) {
    if (!candidateDefs.has(entry.id) || baseDefs.get(entry.id) !== candidateDefs.get(entry.id) || fixtureChanged) continue;
    assert.ok(candidateImages.has(entry.file), 'Candidate omitted a baseline variant: ' + entry.file);
  }

  const images = row => {
    if (!row.afterFile) return '<p>The scenario definition was retired.</p>';
    const after = '<figure><img loading="lazy" src="' + escapeHTML(row.afterFile) + '" alt="Candidate screenshot"><figcaption>Candidate</figcaption></figure>';
    if (!row.beforeFile) return '<div class="images">' + after + '</div>';
    const before = '<figure><img loading="lazy" src="' + escapeHTML(row.beforeFile) + '" alt="Main baseline"><figcaption>main</figcaption></figure>';
    const diff = row.dimensionsMatch ?
      '<figure class="diff"><div><img loading="lazy" src="' + escapeHTML(row.beforeFile) +
      '" alt=""><img loading="lazy" src="' + escapeHTML(row.afterFile) +
      '" alt="Pixel difference"></div><figcaption>Difference overlay</figcaption></figure>' : '';
    return '<div class="images">' + before + after + diff + '</div>';
  };
  const cards = rows.map(row => '<section><h2>' + escapeHTML(row.name) +
    ' <small>' + escapeHTML(row.status) + '</small></h2>' + images(row) + '</section>').join('\n');
  const css = 'body{margin:2rem auto;max-width:1100px;padding:0 1rem;font:16px system-ui;background:#171d1a;color:#f7f5ee}' +
    'a{color:inherit}small,figcaption{color:#abb8ae}section{border-top:1px solid #526157;padding:1rem 0 2rem}' +
    '.images{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,300px),1fr));gap:1rem}' +
    'figure{margin:0;min-width:0}figure img{display:block;max-width:100%;height:auto;border:1px solid #526157}' +
    '.diff div{position:relative}.diff img:last-child{position:absolute;inset:0;mix-blend-mode:difference}' +
    '.diff img{width:100%}figcaption{padding:.5rem 0}small{font-size:.75rem}';
  const summary = {
    baseRevision: base.revision, candidateRevision: candidate.revision,
    browser: candidate.chromium, counts, entries: rows,
    note: 'Exact PNG byte comparison; scenario contract changes are reviewed separately.',
  };
  const heading = '<h1>Walter visual scenario review</h1><p>main ' +
    escapeHTML(base.revision.slice(0, 12)) + ' → candidate ' +
    escapeHTML(candidate.revision.slice(0, 12)) + '</p><p>' +
    Object.entries(counts).map(([label, number]) => escapeHTML(label) + ': ' + number).join(' · ') +
    '</p><p>Only changed, added, removed and contract-changed views appear below.</p>';
  const html = '<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">' +
    '<title>Walter visual review</title><style>' + css + '</style><main>' + heading +
    (cards || '<p>No visual changes.</p>') + '</main></html>\n';
  await Promise.all([
    writeFile(join(args.out, 'index.html'), html),
    writeFile(join(args.out, 'summary.json'), JSON.stringify(summary, null, 2) + '\n'),
  ]);
  if (process.env.GITHUB_STEP_SUMMARY) {
    await appendFile(process.env.GITHUB_STEP_SUMMARY, '### Visual scenarios\n\n' +
      '| Unchanged | Changed | Added | Removed | Contract changed |\n|---:|---:|---:|---:|---:|\n|' +
      [counts.unchanged, counts.changed, counts.added, counts.removed, counts['contract-changed']].join('|') +
      '|\n\nThe changed-only gallery is in the walter-visual-scenarios workflow artifact.\n');
  }
  console.log('Visual comparison: ' + JSON.stringify(counts));
}
main().catch(error => { console.error(error); process.exitCode = 1; });
