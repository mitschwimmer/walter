# Browser tests and validation

Install optional browser tools outside the repository and run the fixture suite:

```sh
npm install --prefix ../walter-ui-tools playwright@1.62.1
../walter-ui-tools/node_modules/.bin/playwright install chromium
WALTER_PLAYWRIGHT_MODULE="$(realpath ../walter-ui-tools/node_modules/playwright)" node tests/ui.test.mjs
```

Use `WALTER_BROWSER_EXECUTABLE` for an existing Chromium and `WALTER_SCREENSHOT_DIR` for desktop/mobile captures. Use `WALTER_COLOR_SCHEME=light` or `dark`; CI runs both. The suite uses a local fixture server, not a production authentication bypass. It covers submission modes, streaming, follow-ups, stop, history, deep links, deletion, retry, session expiry, logout and responsive overflow.

The Nix workflow checks the package, tests/vet, development-shell race detector, SSE parser and Nix formatting. The OCI workflow runs Go checks, both browser themes and image packaging. Inspect the exact source commit’s Actions results; old validation notes do not prove the current tree passes. Report unavailable checks explicitly.

## Visual scenarios

The existing interaction suite remains the functional regression gate. The separate
`tests/visual.test.mjs` runner captures named visual checkpoints from the real
embedded web assets using a fresh browser context and synthetic local HTTP
fixture per scenario/variant. Definitions live in `tests/scenarios/<id>.json`;
each ID must have a matching test case and all declared checkpoints must be
captured. No production authentication bypass or real inference is involved.

Run with the same Playwright 1.62.1/Chromium installation:

```sh
WALTER_PLAYWRIGHT_MODULE="$(realpath ../walter-ui-tools/node_modules/playwright)" \
WALTER_SCREENSHOT_DIR="$(mktemp -d)" node tests/visual.test.mjs
```

Use `WALTER_BROWSER_EXECUTABLE` for a compatible installed Chromium on NixOS.
The runner records both 1071×1579 desktop and 390×844 mobile views in light/dark,
with German locale, Europe/Berlin timezone, device scale 1, fixed synthetic dates,
reduced motion, hidden caret and deterministic loader randomness. Use a fresh
output directory. Captures include stable PNG names and `manifest.json` with the
full Git revision, environment and scenario contracts.

The read-only `.github/workflows/visual.yml` workflow captures the proposed merge
and exact PR base in the same Ubuntu 24.04 job, browser installation and font
environment. It validates definitions and compares PNGs using the pinned
`github.com/archaic-work/visual-test` CLI (commit
`39114999d6b5ccbb7653c3376872727c0db882fa`). A base predating the catalog is
reported as added views, not an unchanged comparison. A matching contract
implies identical runner source and scenario definition across checkouts;
runner edits conservatively invalidate all scenario contracts. Split shared
fixtures/case code into smaller contract units if this becomes noisy.

Download the seven-day `visual-report` artifact and open `index.html` for
changed-only before/after/diff images and `summary.json`. Raw captures and
manifests are available in `visual-captures`. Assertions, missing captures
and comparison errors fail the job; actual pixel changes are informational
for review. Do not commit generated PNGs or publish production data.

## Build metadata

Use Go 1.25 or newer outside Nix. Runtime dependencies are go-oidc, oauth2 and
Goldmark; signed-token tests also use go-jose. Builds work without CGo; the race
detector needs GCC/CGo. Keep dependency pins in `go.mod` / `go.sum`.

Go builds derive the revision from VCS metadata; OCI CI and Nix embed it with
`-X main.revision=...`. Modified checkouts display `-dirty`; missing metadata
shows `dev`. Source Docker builds need an explicit revision:

```sh
docker build --build-arg REVISION="$(git rev-parse HEAD)" -t walter .
```
