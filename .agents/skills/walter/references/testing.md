# Browser tests and validation

Install optional browser tools outside the repository and run the fixture suite:

```sh
npm install --prefix ../walter-ui-tools playwright@1.62.1
../walter-ui-tools/node_modules/.bin/playwright install chromium
WALTER_PLAYWRIGHT_MODULE="$(realpath ../walter-ui-tools/node_modules/playwright)" node tests/ui.test.mjs
```

Use `WALTER_BROWSER_EXECUTABLE` for an existing Chromium and `WALTER_SCREENSHOT_DIR` for desktop/mobile captures. Use `WALTER_COLOR_SCHEME=light` or `dark`; CI runs both. The suite uses a local fixture server, not a production authentication bypass. It covers submission modes, streaming, follow-ups, stop, history, deep links, deletion, retry, session expiry, logout and responsive overflow.


## Named UI scenarios and visual review

Keep the original fixture regression suite above; independently selectable screenshot scenarios live in `tests/scenarios/*.json` and `tests/scenarios.mjs`. They serve the checked-out HTML/CSS/JS in real headless Chromium with a local, synthetic HTTP fixture (`tests/scenario-fixture.mjs`), not production OIDC or inference.

With the same optional Playwright installation:

```sh
WALTER_PLAYWRIGHT_MODULE="$(realpath ../walter-ui-tools/node_modules/playwright)" \
  node tests/scenarios.mjs chat.reply.streaming --out ../walter-ui-scenarios
```

Omit the scenario ID to run all three. Captures use desktop (1071x1579), mobile (390x844), and light/dark themes; filenames encode the scenario ID, checkpoint, viewport and theme. The runner writes `manifest.json` with the full commit SHA and browser version. `WALTER_BROWSER_EXECUTABLE` supports NixOS-provisioned Chromium. Scenario captures disable incidental motion, hide the caret, seed the waiting-loader variation, and await browser fonts. Browser assertions run even without `--out`.

Scenario IDs follow `area.capability.case` with optional named checkpoints. Preserve their meaning across implementation changes; add or retire scenarios explicitly. Register each manifest in the runner with visible assertions and a fixture. Keep synthetic fixture content public-safe and do not commit generated screenshot goldens.

The separate [visual scenario workflow](../../../../.github/workflows/visual.yml) runs candidate scenarios on PRs targeting `main`. It renders unchanged scenario contracts against the exact PR base commit of main, using the same candidate test harness, browser and fixtures with the base commit's actual UI assets. New, retired and modified contracts are reported separately; a fixture-module change flags existing contracts for review. The `walter-visual-scenarios` artifact contains raw captures and a changed-only `report/index.html` with before/after/difference overlays where comparable, plus `report/summary.json`. New scenarios have no comparable main baseline until merged. Test/capture errors fail CI; visual differences are review evidence.


The Nix workflow checks the package, tests/vet, development-shell race detector, SSE parser and Nix formatting. The OCI workflow runs Go checks, both browser themes and image packaging. Inspect the exact source commit’s Actions results; old validation notes do not prove the current tree passes. Report unavailable checks explicitly.

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
