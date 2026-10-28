---
name: walter
description: Maintain, extend, test, debug, and release mitschwimmer/walter, the Go web chat harness with OIDC, llama.cpp or OpenAI Chat Completions, embedded browser assets, JSON persistence, Nix packaging, and OCI deployment. Use for changes or operational work in this repository.
---

# Walter

## Navigate and preserve invariants

Keep one Go process and the current cohesive `main` package; extract boundaries only for demonstrated needs. Use standard Go and browser APIs; keep the frontend free of a build step.

- Read `main.go` for startup, routes and API behavior; `config.go` for startup configuration and profile validation.
- Read `auth.go` for OIDC, in-memory sessions and CSRF. Preserve PKCE, one-use state/nonce, exact group matching, secure cookies and Origin checks. Never add an authentication bypass.
- Read `store.go` for per-user JSON. Keep one writer per data directory, atomic replacement, file/directory syncing, and recovery of pending replies before serving traffic.
- Read `inference.go`, `context.go` and `markdown.go` for upstream SSE, optional token counts and rendering. Preserve prompt/credential redirect rejection, separated reasoning disposal, cancellation, partial-answer persistence and bounded handoffs. Join request goroutines before returning.
- Read `diagnostics.go` and `recorder.go` before adding logs. Use `requestLogger(ctx)` and fixed diagnostic messages/attributes; never log prompts, tokens, URLs, user IDs or arbitrary error strings. Use `logFailure` at handling boundaries, including handled dependency failures.
- Read `web/app.js`, `web/sse.js`, `web/index.html` and `web/style.css` for the German UI. Store original Markdown, render assistant HTML on the server, and keep user text plain.
- Keep API and stored JSON compatible with existing clients and conversations. `done` means a complete reply was synced; a lost response requires reopening before retrying.

## Develop and verify

Run commands from the repository root. Use `nix develop` for the pinned Go/gopls, Node, GCC and secret tools. Keep Helix and the preferred shell in workstation configuration; `hx .` can use the project’s gopls. Go owns the edit/test loop, Nix owns provisioning and packaging. Keep `GOTOOLCHAIN=local` in the development shell.

```sh
go test ./...
go test -race ./...
go vet ./...
node tests/sse.test.mjs
CGO_ENABLED=0 go build -trimpath -o walter .
```

Use real HTTP fixtures and temporary storage with the standard `testing` package. Run focused regressions while editing, then all Go tests and race detection when request lifecycle or shared state changes. For frontend changes, run the browser suite described in [testing](references/testing.md). Use [historical validation](references/validation-history.md) only for prior evidence.
Report checks not run; fixture tests do not establish live OIDC/inference or proxy acceptance.

Build the native release using `nix build`; inspect `result/bin/walter`. Run `nix flake check` and `nix fmt -- flake.nix` for packaging changes. Keep `flake.lock` pinned; update `vendorHash` through Nix when Go dependencies change. Do not decrypt secrets in Nix or commit private keys.

## Make common changes

- For configuration or profiles, update `config.go`, configuration tests and [configuration](references/configuration.md). Validate at startup and pass explicit values; keep SOPS outside application logic.
- For API/storage changes, update route and persistence tests, preserve user isolation and interruption semantics, and document compatibility decisions.
- For inference changes, test both backends, malformed/multiline streams, output limits, hidden reasoning, disconnects, write failures and final-save failures. Keep metadata optional for generation while recording actual dependency failures.
- For diagnostics changes, test silent successes, failure replay through debug exactly once, redaction, original record ordering/scopes, overflow, cancellation, panic propagation, concurrent isolation and late work.
- For dependency/toolchain changes, keep Go modules, Nix packaging and the source Docker build consistent. Keep dependencies minimal and justify additions.

## Configure, debug and operate

Read [configuration and behavior](references/configuration.md) for environment variables, profile contracts, API/SSE semantics and trust boundaries. Mount secrets through the `_FILE` settings, restart after rotation, and never configure both secret sources. Run `go run .` only with valid OIDC/model configuration and the HTTPS proxy; there is no insecure development mode.

Read [operations](references/operations.md) for OCI releases, Incus runtime requirements, Caddy, backup/restore and live acceptance. CI publishes images from main and version tags; install by digest. Keep infrastructure configuration in its owning repository.

Inspect failed-request flight recorder output by request correlation and fixed operation/error fields. Successful requests are silent. Startup/background failures log immediately. The recorder retains at most 128
records, reports dropped records on failure, and replays original levels/times
through the sink threshold. Use only fixed operational strings with the allowed
keys in `redactAttrs`; unknown messages/attributes are redacted before capture.
`WithAttrs` / `WithGroup` retain their scopes. Use fixed group names, never
request-derived strings. Join request work before returning; a late
logger call emits a correlated lifecycle error without reopening the buffer. Reopen a conversation to establish persisted state after uncertain delivery; investigate storage and restart recovery for pending replies after save failures. Keep detailed failure diagnostics free of conversation content and credentials.

Update this skill and its references whenever behavior, architecture, tooling or maintenance procedures change. Keep README and deployment entry points as links rather than parallel guides.
