# Browser tests and validation

Walter's browser regression suite and screenshot scenarios are native Go tests using a **real installed Chromium** through [Archaic Go visualtest](https://github.com/mitschwimmer/go-skill/tree/main/visualtest) and Rod. All test-only dependencies live in `tests/browser/go.mod`; the production `go.mod` and JavaScript frontend are unchanged.

From the repository root (in `nix develop` on NixOS):

```sh
go -C tests/browser test ./... -v
go -C tests/browser vet ./...
go -C tests/browser run . capture --scenario chat.reply.streaming --out ../../../walter-screenshots
go -C tests/browser run . capture --out ../../../walter-screenshots
```

The first command runs `TestBrowserInteractions` under both light and dark themes and `TestSSEBrowserModule`. The SSE test executes the **real** `web/sse.js` ES module inside Chromium and covers all byte chunk sizes, split UTF-8, CRLF, multiline frames, incomplete streams, and a large HTML snapshot. The browser test uses a local synthetic HTTP server and exercises submission modes, streaming and progress, cancellation, persistence, retry, history and deep links, delete/cancel/failure, session expiry/logout, keyboard submission safety, context exhaustion, Markdown rendering, responsive layout and privacy invariants. Neither suite bypasses production OIDC or contacts external inference services.

Install Chromium through `nix develop` or system packages. Set `WALTER_BROWSER_EXECUTABLE` if Chromium is not on `PATH`. The browser is not automatically downloaded; only controlled CI should set `WALTER_BROWSER_NO_SANDBOX=1`. Tests skip browser execution when no browser is installed, so check that the browser job actually ran before claiming browser validation.

## Named scenarios and main-based PR reviews

Scenario definitions live in `tests/scenarios/<area>.<capability>.<case>.json`. Their executable fixture is `tests/browser/fixture.go`, with assertions and checkpoint capture in `tests/browser/main.go`. Existing IDs include `chat.start.empty`, `chat.reply.streaming#waiting`, and `chat.history.populated`. The shared library validates the contracts and creates real Chromium screenshots in desktop (1071×1579), mobile (390×844), light and dark variants. The resulting `manifest.json` includes revision, browser version, environment and per-scenario fixture/assertion fingerprints. Captures are not committed as PNG goldens.

The [visual workflow](../../../../.github/workflows/visual.yml) checks out the PR merge result and the exact `main` PR-base SHA (or a pinned `main` checkout for PRs targeting another branch). The same Go runner captures both revisions with their actual web assets, while `visualtest.Compare` reports **unchanged, changed, added, removed and contract-changed** views. The changed-only HTML report, PNG difference images, JSON summary and captures are kept in the `walter-go-visual-scenarios` artifact. Pixel differences are informational for human review; failed browser assertions or missing comparable captures fail CI. This PR-side workflow is read-only and uses no secrets.

To reproduce a comparison locally with a separate checked-out `main` worktree:

```sh
go -C tests/browser run . capture --out ../../../walter-screenshots/candidate --base-root /path/to/walter-main
```

The base checkout should be the exact PR base commit for reproducibility. Fixtures remain synthetic and independently initialized per viewport/theme. Extend existing scenarios or add IDs for new UI behavior, updating their Go assertions in the same change.

## Build metadata

Use Go 1.25 or newer outside Nix. Runtime dependencies are go-oidc, oauth2 and Goldmark; signed-token tests also use go-jose. Builds work without CGo; the race detector needs GCC/CGo. Keep runtime dependency pins in the root `go.mod` / `go.sum`, and browser-only dependencies in `tests/browser/go.mod` / `go.sum`.

Go builds derive the revision from VCS metadata; OCI CI and Nix embed it with `-X main.revision=...`. Modified checkouts display `-dirty`; missing metadata shows `dev`. Source Docker builds need an explicit revision:

```sh
docker build --build-arg REVISION="$(git rev-parse HEAD)" -t walter .
```
