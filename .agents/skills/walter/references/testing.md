# Browser tests and validation

Install optional browser tools outside the repository and run the fixture suite:

```sh
npm install --prefix ../walter-ui-tools playwright@1.62.1
../walter-ui-tools/node_modules/.bin/playwright install chromium
WALTER_PLAYWRIGHT_MODULE="$(realpath ../walter-ui-tools/node_modules/playwright)" node tests/ui.test.mjs
```

Use `WALTER_BROWSER_EXECUTABLE` for an existing Chromium and `WALTER_SCREENSHOT_DIR` for desktop/mobile captures. Use `WALTER_COLOR_SCHEME=light` or `dark`; CI runs both. The suite uses a local fixture server, not a production authentication bypass. It covers submission modes, streaming, follow-ups, stop, history, deep links, deletion, retry, session expiry, logout and responsive overflow.

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
