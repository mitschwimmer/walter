// Independently selectable browser scenarios with screenshots from real Chromium.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFile, readdir, mkdir, writeFile } from 'node:fs/promises';
import { dirname, basename, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createScenarioServer } from './scenario-fixture.mjs';

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.WALTER_PLAYWRIGHT_MODULE || 'playwright');
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const scenarioDirectory = join(root, 'tests/scenarios');
const viewports = { desktop: { width: 1071, height: 1579 }, mobile: { width: 390, height: 844 } };
const themes = new Set(['light', 'dark']);
const idPattern = /^[a-z][a-z0-9]*(?:-[a-z0-9]+)*\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/;
const checkpointPattern = /^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/;
const sha256 = value => createHash('sha256').update(value).digest('hex');
const state = (page, name) => page.waitForFunction(value => document.body.dataset.view === value, name);
const noOverflow = page => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth);

const implementations = {
  'chat.start.empty': {
    fixture: 'empty-chat', checkpoint: 'ready',
    async run(page) {
      await state(page, 'start');
      assert.equal(await page.locator('#content').inputValue(), '');
      assert.equal(await page.locator('#content').isEnabled(), true);
      assert.equal(await page.locator('#quick').isVisible(), true);
      assert.equal(await page.locator('#thoughtful').isVisible(), true);
      await page.locator('#content').evaluate(element => element.blur());
    },
  },
  'chat.reply.streaming': {
    fixture: 'streaming-reply', checkpoint: 'waiting',
    async run(page) {
      await state(page, 'start');
      await page.locator('#content').fill('Was ist Marmelade?');
      await page.locator('#thoughtful').click();
      await state(page, 'conversation');
      const loader = page.locator('.response-loader');
      await loader.waitFor({ state: 'visible' });
      assert.equal(await loader.getAttribute('aria-label'), 'Walter bereitet die Antwort vor.');
      assert.equal(await page.locator('.response-status').textContent(), 'Warte auf das Modell …');
      assert.equal(await page.locator('.message').count(), 2);
      assert.equal(await page.locator('#stop').isVisible(), true);
    },
  },
  'chat.history.populated': {
    fixture: 'history-with-conversations', checkpoint: 'ready',
    async run(page) {
      await state(page, 'start');
      await page.locator('#history-link').click();
      await state(page, 'history');
      await page.waitForFunction(() => document.querySelectorAll('#chats li').length === 2);
      assert.equal(await page.locator('#chats li').count(), 2);
      assert.match(await page.locator('#chats').textContent(), /Marmelade/);
      assert.match(await page.locator('#chats').textContent(), /Tabellen/);
    },
  },
};

function argumentsFrom(argv) {
  const options = { webRoot: join(root, 'web'), output: null, revision: null, baselineContracts: null, id: null };
  const flags = { '--out': 'output', '--web-root': 'webRoot',
    '--revision': 'revision', '--baseline-contracts': 'baselineContracts' };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (flags[arg]) {
      assert.ok(argv[i + 1] && !argv[i + 1].startsWith('--'), 'Missing value for ' + arg);
      options[flags[arg]] = argv[++i];
    } else if (!arg.startsWith('-') && !options.id) {
      options.id = arg;
    } else {
      throw new Error('Unknown argument: ' + arg);
    }
  }
  options.webRoot = resolve(options.webRoot);
  if (options.output) options.output = resolve(options.output);
  return options;
}

async function definitions() {
  const names = (await readdir(scenarioDirectory)).filter(name => name.endsWith('.json')).sort();
  const found = [];
  for (const name of names) {
    const raw = await readFile(join(scenarioDirectory, name));
    const definition = JSON.parse(raw.toString());
    assert.match(definition.id, idPattern);
    assert.equal(name, definition.id + '.json');
    assert.ok(typeof definition.description === 'string' && definition.description.length > 0);
    assert.ok(Array.isArray(definition.checkpoints) && definition.checkpoints.length === 1);
    assert.ok(definition.checkpoints.every(checkpoint => checkpointPattern.test(checkpoint)));
    assert.ok(Array.isArray(definition.viewports) && definition.viewports.length > 0);
    assert.ok(Array.isArray(definition.themes) && definition.themes.length > 0);
    for (const key of definition.viewports) assert.ok(viewports[key], 'Unknown viewport: ' + key);
    for (const key of definition.themes) assert.ok(themes.has(key), 'Unknown theme: ' + key);
    for (const values of [definition.checkpoints, definition.viewports, definition.themes]) {
      assert.equal(values.length, new Set(values).size, 'Duplicate scenario variant: ' + name);
    }
    const implementation = implementations[definition.id];
    assert.ok(implementation, 'Scenario has no automated test: ' + definition.id);
    assert.equal(definition.fixture, implementation.fixture);
    assert.deepEqual(definition.checkpoints, [implementation.checkpoint]);
    found.push({ ...definition, raw, hash: sha256(raw) });
  }
  assert.deepEqual(Object.keys(implementations).sort(), found.map(definition => definition.id));
  return found;
}

async function main() {
  const options = argumentsFrom(process.argv.slice(2));
  const all = await definitions();
  if (options.id) assert.ok(all.some(definition => definition.id === options.id), 'Unknown scenario: ' + options.id);
  let selected = all.filter(definition => !options.id || definition.id === options.id);
  if (options.baselineContracts) {
    const baselineDirectory = resolve(options.baselineContracts);
    const matching = [];
    for (const definition of selected) {
      const previous = await readFile(join(baselineDirectory, definition.id + '.json'))
        .catch(error => error.code === 'ENOENT' ? null : Promise.reject(error));
      if (previous && previous.equals(definition.raw)) matching.push(definition);
    }
    selected = matching;
  }
  const revision = options.revision || execFileSync('git', ['rev-parse', 'HEAD'],
    { cwd: root, encoding: 'utf8' }).trim();
  const fixtureSourceHash = sha256(await readFile(join(root, 'tests/scenario-fixture.mjs')));
  const manifest = {
    format: 1, revision, chromium: null, fixtureSourceHash,
    platform: process.platform, locale: 'de-DE', timezone: 'Europe/Berlin',
    deviceScaleFactor: 1, reducedMotion: 'reduce', images: [],
  };
  if (options.output) await mkdir(options.output, { recursive: true });
  let browser;
  try {
    if (selected.length) {
      browser = await chromium.launch({
        headless: true,
        ...(process.env.WALTER_BROWSER_EXECUTABLE ?
          { executablePath: process.env.WALTER_BROWSER_EXECUTABLE } : {}),
        args: ['--no-sandbox', '--disable-dev-shm-usage'],
      });
      manifest.chromium = browser.version();
    }
    for (const definition of selected) {
      for (const viewportName of definition.viewports) {
        for (const theme of definition.themes) {
          const fixture = await createScenarioServer(definition.fixture, options.webRoot);
          const context = await browser.newContext({
            viewport: viewports[viewportName], deviceScaleFactor: 1, colorScheme: theme,
            locale: 'de-DE', timezoneId: 'Europe/Berlin', reducedMotion: 'reduce',
          });
          try {
            // Walter varies the loader decoration randomly. Keep that variation fixed only in tests.
            await context.addInitScript(() => { Math.random = () => 0.25; });
            const page = await context.newPage();
            const errors = [];
            page.on('pageerror', error => errors.push(error.message));
            await page.goto(fixture.origin);
            await implementations[definition.id].run(page);
            assert.equal(await noOverflow(page), true, 'Horizontal page overflow');
            assert.equal(await page.locator('#error').isVisible(), false);
            assert.deepEqual(errors, []);
            await page.evaluate(() => document.fonts.ready);
            const name = [definition.id, definition.checkpoints[0], viewportName, theme].join('--') + '.png';
            if (options.output) {
              await page.screenshot({
                path: join(options.output, name), fullPage: true, animations: 'disabled', caret: 'hide',
              });
            }
            manifest.images.push({
              id: definition.id, checkpoint: definition.checkpoints[0],
              viewport: viewportName, theme, width: viewports[viewportName].width,
              height: viewports[viewportName].height, definitionHash: definition.hash, file: name,
            });
            console.log('PASS ' + name);
          } finally {
            await context.close();
            await fixture.close();
          }
        }
      }
    }
  } finally {
    if (browser) await browser.close();
    if (options.output) await writeFile(join(options.output, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
  }
  console.log('Passed ' + manifest.images.length + ' scenario variants.');
}
main().catch(error => { console.error(error); process.exitCode = 1; });
