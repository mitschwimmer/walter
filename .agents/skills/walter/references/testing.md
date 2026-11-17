# Browser tests and validation

Install optional browser tools outside the repository and run the fixture suite:

```sh
npm install --prefix ../walter-ui-tools playwright@1.62.1
../walter-ui-tools/node_modules/.bin/playwright install chromium
WALTER_PLAYWRIGHT_MODULE="$(realpath ../walter-ui-tools/node_modules/playwright)" node tests/ui.test.mjs
```

Use `WALTER_BROWSER_EXECUTABLE` for an existing Chromium and `WALTER_SCREENSHOT_DIR` for desktop/mobile captures. Use `WALTER_COLOR_SCHEME=light` or `dark`; CI runs both. The suite uses a local fixture server, not a production authentication bypass. It covers submission modes, streaming, follow-ups, stop, history, deep links, deletion, retry, session expiry, logout and responsive overflow.


## Named Rod scenarios and visual review

The scenario manifests remain under `tests/scenarios/*.json` with stable IDs and checkpoints. The independent Go test module under `tests/browser/` implements scenarios, synthetic HTTP fixtures and PNG-pixel comparisons using [Rod](https://github.com/go-rod/rod) and a real local Chromium/Chrome. Production Go modules, OIDC and inference do not depend on Rod.

From the repository root in the development shell:

```sh
go -C tests/browser test ./...
go -C tests/browser run . capture --scenario chat.reply.streaming --out ../../../walter-ui-scenarios
go -C tests/browser run . capture --out ../../../walter-ui-scenarios
```

The browser must already be installed. Set `WALTER_BROWSER_EXECUTABLE` to a NixOS-provided Chromium binary, or let Rod locate a browser on `PATH`. The tool does not download Chrome automatically and keeps the local Chrome sandbox enabled. Only set `WALTER_BROWSER_NO_SANDBOX=1` in a controlled CI environment that requires it. Capture names remain `<scenario>--<checkpoint>--<viewport>--<theme>.png`, using desktop (1071x1579), mobile (390x844), light and dark themes. `manifest.json` records the browser version, source commit and fixture hash. The test runner uses real DOM actions and CSS media emulation, fixed locale/timezone, synthetic dates and HTTP API responses, and no production authentication bypass. No screenshot goldens are committed.

The dedicated [Rod visual workflow](../../../../.github/workflows/visual.yml) captures the candidate and matching contracts from `main` in the **same installed browser** and writes a changed-only `report/index.html` and `summary.json` into the `walter-rod-visual-scenarios` Actions artifact. It compares decoded PNG pixels, generating a separate difference PNG for compatible dimensions. Added, removed and changed-contract scenarios do not masquerade as like-for-like UI changes. A scenario has to reference visible browser assertions before capture.

**Comparison boundary:** This branch replaces only the scenario screenshot/review workflow. Walter's pre-existing `tests/ui.test.mjs` and `tests/sse.test.mjs` still exercise broader regression cases through Node.js, and the OCI/Nix workflows still run those checks. Do not remove their coverage merely to claim the project is Node-free. Eliminating Node completely requires a separately verified native replacement for those tests.


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
