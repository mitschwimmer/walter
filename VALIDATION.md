# Validation

## OpenAI Chat Completions backend (2026-10-26)

Validated with Go 1.25.3 on linux/amd64:

- Go tests, vet, race detector, CGo-free release build and Node SSE parser tests passed.
- HTTP fixtures cover both portable profiles, bearer authentication and optional
  credentials, mounted key files, legacy llama.cpp configuration, hidden
  reasoning, role-only/usage stream chunks, unsupported metadata and redirect
  rejection. llama.cpp context requests also verify bearer authentication.
- No dependencies or frontend code changed.

Not run: Nix packaging (Nix unavailable), live Strata streaming/thinking behavior,
or deployment. The Strata example follows Digital Garden's pi model definition;
it still needs a dedicated runtime credential and reachable endpoint in Walter's
deployment.

Validated in the implementation workspace with Go 1.25.3, linux/amd64:

- `CGO_ENABLED=0 go test -count=1 -v ./...` — passed all test groups.
- `CGO_ENABLED=0 go build -buildvcs=false -trimpath -o walter .` — passed.
- `CGO_ENABLED=1 go test -race ./...` — passed. CGo is used only by Go's
  race detector; the application build and normal test suite require no CGo.
- `node tests/sse.test.mjs` — passed all transport chunk sizes, split UTF-8,
  CRLF, multiline data and incomplete-frame detection.
- JavaScript ES module syntax check — passed.

The workspace has an incomplete enclosing Git metadata directory; VCS stamping
was explicitly disabled for its build. A normal checkout/extracted source can
use the README build command. Toolchain installation and caches are not shipped.

Go integration tests use real HTTP servers and signed ID tokens: authorization
code exchange with Basic client auth, PKCE, nonce, audience/expiry, allowed and
denied groups, cookie flags, callback replay, logout, session expiry, API origin
and CSRF checks, foreign-user isolation, concurrent atomic persistence, sort and
restart recovery. Inference tests cover successful persisted replies, incomplete
streams, hidden reasoning, length truncation, client disconnect cancellation and
final-save failure preventing done events.

Not run: actual browser interaction, live Kanidm login, actual GGUF thinking
behavior/GPU inference, Caddy proxy acceptance, Nix packaging/guest deployment,
IPv4/IPv6 firewall acceptance, and live backup/restore. These need the actual
infrastructure checkout, runtime configuration and host access; see deploy/README.md.

OCI build/publish is validated by the GitHub Actions workflow; Docker is not
available in the local implementation workspace.

## Streaming Markdown (2026-10-16)

Validated with Go 1.25.3 on linux/amd64:

- `go test ./...`, `go vet ./...` and `go test -race ./...` passed.
- `CGO_ENABLED=0 go build -trimpath` passed.
- `go mod verify`, `node tests/sse.test.mjs` and JavaScript module syntax checks passed.
- Added coverage for headings, emphasis, links, lists, quotes, tables, code fences,
  unfinished Markdown, raw HTML and unsafe URL rejection. HTTP integration tests
  check final snapshots on success and upstream failure, identical reopened HTML,
  plain user content and Markdown-only persistence. A paused upstream test checks
  timer flushing and reader cancellation when the client write fails. Existing
  disconnect and final-save failure tests continue to pass.
- Updated `vendorHash` from the NAR SHA-256 of `go mod vendor` output. Verified
  the same calculation reproduces the previous dependency hash exactly.

Not run: a full Nix build (Nix is unavailable), browser visual/interaction checks
(Chromium is unavailable and the browser download failed), or live inference.

## German UI redesign (2026-10-16)

- Go 1.25.3 tests, vet, race detector and CGo-free release build passed.
- Node SSE regressions, JavaScript syntax and whitespace checks passed.
- Added API coverage for German-only conversation creation, legacy conversation
  metadata, optional HTTPS footer URLs and the public site-links endpoint.
- Added a Playwright fixture-server suite for both submission modes, streamed and
  reopened replies, follow-ups, stop, history, deletion/cancellation, retry,
  session expiry, logout and narrow-screen overflow. CI runs it separately and
  retains desktop/mobile screenshots for review.

Local browser execution is blocked: Chromium exits during startup in the
restricted workspace. Browser interaction/layout results must come from CI;
this is not recorded as a local browser pass. No live OIDC/inference or Nix
package deployment was performed for this change.
