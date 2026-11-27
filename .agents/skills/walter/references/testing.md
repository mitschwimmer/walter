# Browser tests and validation

Walter's browser regression suite and screenshot scenarios are native Go tests using a **real installed Chromium** through [Archaic Go visualtest](https://github.com/archaic-work/visual-test) and Rod. All test-only dependencies live in `tests/browser/go.mod`; the production `go.mod` and JavaScript frontend are unchanged.

From the repository root (in `nix develop` on NixOS):

```sh
go -C tests/browser test ./... -v
go -C tests/browser vet ./...
go -C tests/browser run . capture --scenario chat.reply.streaming --out ../../../walter-screenshots
go -C tests/browser run . capture --out ../../../walter-screenshots
```

The first command runs `TestBrowserInteractions` under both light and dark themes and `TestSSEBrowserModule`. The SSE test executes the **real** `web/sse.js` ES module inside Chromium and covers all byte chunk sizes, split UTF-8, CRLF, multiline frames, incomplete streams, and a large HTML snapshot. The browser test uses a local synthetic HTTP server and exercises starter questions, theme selection, submission modes, streaming and progress, cancellation, persistence, retry, history and deep links, delete/cancel/failure, session expiry/logout, keyboard submission safety, context exhaustion, Markdown rendering, responsive layout and privacy invariants. Neither suite bypasses production OIDC or contacts external inference services.

Install Chromium through `nix develop` or system packages. Set `WALTER_BROWSER_EXECUTABLE` if Chromium is not on `PATH`. The browser is not automatically downloaded; only controlled CI should set `WALTER_BROWSER_NO_SANDBOX=1`. Tests skip browser execution when no browser is installed, so check that the browser job actually ran before claiming browser validation.

## Named scenarios and main-based PR reviews

Scenario definitions live in `tests/scenarios/<area>.<capability>.<case>.json`. Their executable fixture is `tests/browser/fixture.go`, with assertions and checkpoint capture in `tests/browser/main.go`. Existing IDs include `chat.start.empty`, `chat.reply.streaming#waiting`, `chat.reply.completed#ready`, and `chat.history.populated`. The shared library validates the contracts and creates real Chromium screenshots in desktop (1071×1579), mobile (390×844), light and dark variants. The resulting `manifest.json` includes revision, browser version, environment and per-scenario fixture/assertion fingerprints. Captures are not committed as PNG goldens. The completed-answer scenario reopens a synthetic conversation with a heading, table and code block. Existing scenario IDs and viewport sizes stay stable; changes to the shared fixture/assertion contract may classify earlier captures as contract-changed rather than pixel differences.

The manual Tekton CI path runs the Go/browser checks and the same visual runner against the exact candidate SHA and explicit `main` baseline SHA (the PR base for main-targeted changes, a pinned main revision otherwise). The operator retrieves the manifest, changed-only HTML report, PNG differences, JSON summary and captures from the CI installation. Pixel differences are review information, not a failed assertion; missing captures, browser assertions and a failed identical-image self-comparison fail CI. Evidence must refer to the exact candidate and base revisions; do not claim a green GitHub status check or upload sensitive review artifacts to public GitHub.

To reproduce a comparison locally with a separate checked-out `main` worktree:

```sh
go -C tests/browser run . capture --out ../../../walter-screenshots/candidate --base-root /path/to/walter-main
```

The base checkout should be the exact PR base commit for reproducibility. Fixtures remain synthetic and independently initialized per viewport/theme. Extend existing scenarios or add IDs for new UI behavior, updating their Go assertions in the same change.

## Build metadata

Use Go 1.25 or newer outside Nix. Runtime dependencies are go-oidc, oauth2 and Goldmark; signed-token tests also use go-jose. Builds work without CGo; the race detector needs GCC/CGo. Keep runtime dependency pins in the root `go.mod` / `go.sum`, and browser-only dependencies in `tests/browser/go.mod` / `go.sum`.

Go builds derive the revision from VCS metadata; ko and Nix embed it with `-X main.revision=...`. Modified checkouts display `-dirty`; missing metadata shows `dev`. Use the [ko packaging procedure](operations.md#daemonless-oci-packaging) for OCI publication.

## Manual CI and separate native Nix validation

Submit the exact candidate and baseline commits through the operator's external Tekton installation. A successful run must include module verification, Go tests, vet, race detection, static build, real Chromium UI/SSE tests, browser-module vet and visual captures/self-comparison. Release runs repeat those checks for the reviewed ref and return the source SHA and immutable image digest. GitHub no longer runs CI; keep the evidence with the reviewed change.

The native Nix package remains independent of the fast CI path. In a clean checkout run:

```sh
nix flake check --no-update-lock-file --print-build-logs
nix fmt -- flake.nix
git diff --exit-code -- flake.nix
nix build --no-update-lock-file
```

Run the native checks for changes to the flake, dependency pins, packaging and native build contract. The flake check builds Walter and runs Go tests/vet; formatting must leave `flake.nix` unchanged. The resulting executable is `result/bin/walter`. For a credential-free OCI packaging check use the non-pushing ko command in [operations](operations.md#daemonless-oci-packaging); examine the OCI config for `linux/amd64`, user `65532:65532` and entrypoint `/ko-app/walter`. Do not replace these manual checks with a second hosted CI path.
