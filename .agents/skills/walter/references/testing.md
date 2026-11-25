# Browser tests and validation

The browser regression runner is a separate Go module in `tests/browser`.
It uses Rod and the pinned Archaic Visualtest library. No Node.js, npm or
Playwright is needed. Run from a checkout using the pinned Nix development shell:

```sh
nix develop
root="$PWD"
cd tests/browser
VISUALTEST_BROWSER_EXECUTABLE="$(command -v chromium)" go run -mod=mod . -root "$root" -out "$(mktemp -d)"
```

To compare against a separate stable main checkout, add `-base /path/to/main`.
The same compiled runner uses actual assets from both checkouts. Seven named
scenarios in `tests/scenarios` cover login, composer, streaming, complete reply,
history and deletion across desktop/mobile and light/dark. The runner also
asserts interaction behavior, retry, cancellation, context limits, logout,
session expiry, keyboard entry and the real browser SSE parser (all chunk
sizes, UTF-8, CRLF, incomplete frames and large snapshots).

Fixture servers use synthetic data and fresh isolated browser contexts, never
production OIDC or inference. The contract includes the runner and fixture
source and excludes the application web assets. The runner validates declared
checkpoints, uses the installed Chromium from the pinned Nix shell, fixes
locale/timezone/viewport/theme, and records full source revisions and manifests.
A stable main checkout predating the catalog reports all scenarios as added.

The OCI workflow compares the proposed merge or push to stable main, using a
single pinned Chromium installation and read-only checkout permissions. It
uploads raw captures and the changed-only HTML/JSON gallery for seven days.
Test/capture errors fail CI; pixel differences are informational. The Nix
workflow checks package, tests/vet, race detector and Nix formatting.
Inspect the exact commit's Actions results. Report unavailable checks explicitly.

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
