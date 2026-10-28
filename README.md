# Walter

Walter is a small German web UI for private conversations with llama.cpp or an
OpenAI Chat Completions backend such as Strata. One Go process,
embedded HTML/CSS/ES modules, per-conversation JSON, native Kanidm OIDC, no
frontend build step. Inference is accessible only through the backend.

## Build and test

On NixOS, use the pinned development environment:

```fish
nix develop -c fish
hx .
go test ./...
go test -race ./...
go vet ./...
node tests/sse.test.mjs
```

The shell provides Go, gopls, Helix, Fish, SOPS/age, Node for parser tests, and
GCC for the race detector. `GOTOOLCHAIN=local` keeps the compiler under Nix
control. Go owns module dependencies and the interactive edit/test loop.
Keep private age keys outside Git and decrypt secrets only at runtime, for
example with `sops exec-env` or `sops exec-file`; Nix never decrypts them.

Build the release package with `nix build`; the binary is `result/bin/walter`.
`nix flake check` explicitly builds that package and runs its CGo-free Go tests
and vet checks. The Nix CI job also checks the dev shell, race detector, browser
parser and Nix formatting. `flake.lock` pins nixpkgs; `vendorHash` pins the
vendored module closure without committing a vendor directory. Update both
through native Nix/Go tools when changing their respective inputs.

The OCI workflow remains the deployment path; the Nix package is an additional
native release artifact. For other environments, Go 1.25 or newer is required.
The runtime uses go-oidc, oauth2 and Goldmark; signed-token tests also use go-jose.
All dependencies are pinned by `go.mod` and `go.sum` and work without CGo.

```sh
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build -trimpath -o walter .
```

Optional browser parser regression test (Node is only a test tool):

```sh
node tests/sse.test.mjs
```

Footer links appear when their URLs are configured, including on the login page.
Supply your actual operator/privacy pages; Walter does not generate legal text.
The small footer also shows the build's seven-character Git revision, including
when no footer links are configured. OCI CI and Nix builds embed their source
revision; ordinary `go build` uses Go's VCS metadata. Modified checkouts carry
`-dirty`; builds without revision metadata show `dev`.

For a source Docker build, pass the revision explicitly because the build context
does not include Git metadata:

```sh
docker build --build-arg REVISION="$(git rev-parse HEAD)" -t walter .
```

The optional browser regression suite uses Playwright against a local fixture
server, without an authentication bypass in Walter. Install Playwright in a
separate test-tools directory and point the suite at it:

```sh
npm install --prefix ../walter-ui-tools playwright@1.62.1
../walter-ui-tools/node_modules/.bin/playwright install chromium
WALTER_PLAYWRIGHT_MODULE="$(realpath ../walter-ui-tools/node_modules/playwright)" node tests/ui.test.mjs
```

`WALTER_BROWSER_EXECUTABLE` can select an existing Chromium binary, and
`WALTER_SCREENSHOT_DIR` optionally captures desktop/mobile views. The suite
covers both modes, streaming, follow-ups, stop, history, deep links, deletion,
submission failures, session expiry and responsive overflow.

Serve through Caddy on HTTPS; there is deliberately no authentication bypass or
insecure-cookie development mode. `/healthz` only proves that the app is running,
not inference availability. Startup fails if OIDC discovery or storage recovery
fails. Stop via SIGTERM or SIGINT; request cancellation then persists interruptions
before the HTTP server finishes shutdown.

## Configuration

| Variable | Required / default |
|---|---|
| `WALTER_PUBLIC_URL` | Required public HTTPS origin, no path, e.g. `https://walter.example.com` (proposed hostname) |
| `WALTER_OIDC_ISSUER` | Required, use the exact discovery `issuer` for the dedicated Kanidm client |
| `WALTER_OIDC_CLIENT_ID` | Required, dedicated client, e.g. `walter` |
| `WALTER_OIDC_CLIENT_SECRET` | Runtime secret; use this or the file setting, never both |
| `WALTER_OIDC_CLIENT_SECRET_FILE` | Path to a mounted secret file; preferred for OCI |
| `WALTER_ALLOWED_GROUPS` | `walter-users`, exact comma-separated claim values |
| `WALTER_IMPRINT_URL` | Optional HTTPS URL for the **Impressum** footer link |
| `WALTER_PRIVACY_URL` | Optional HTTPS URL for the **Datenschutz** footer link |
| `WALTER_MODEL` | Required model alias verified in the inference catalog |
| `WALTER_QUICK_JSON` | Optional replacement profile for **schnell**; defaults below |
| `WALTER_THOUGHTFUL_JSON` | Optional replacement profile for **bedacht**; defaults below |
| `WALTER_INFERENCE_BACKEND` | `llamacpp` (default) or `openai` (Chat Completions) |
| `WALTER_INFERENCE_ENDPOINT` | Full private chat completions URL, including `/v1/chat/completions`; required for `openai` |
| `WALTER_LLAMA_ENDPOINT` | Legacy endpoint setting for `llamacpp`; use this or `WALTER_INFERENCE_ENDPOINT`, never both |
| `WALTER_INFERENCE_API_KEY` | Optional bearer credential; use this or the file setting, never both |
| `WALTER_INFERENCE_API_KEY_FILE` | Path to a mounted bearer credential file; preferred for OCI |
| `WALTER_LISTEN` | `127.0.0.1:8080`; select a private interface for a separate Caddy guest |
| `WALTER_DATA_DIR` | `data`; use an absolute persistent volume path in production |
| `WALTER_GENERATION_TIMEOUT` | `2h`, includes cold model loading and streaming |
| `WALTER_SESSION_TTL` | `12h`, also bounded by the ID token expiry |
| `WALTER_MAX_HISTORY_BYTES` | `48000`, rejects additional messages rather than silently truncating |
| `WALTER_MAX_OUTPUT_TOKENS` | `4096`; fallback when a profile omits `max_tokens` |

For `llamacpp`, model settings allow `reasoning_effort`, `chat_template_kwargs`, `temperature`,
`top_p`, `top_k`, `min_p`, `presence_penalty`, `repeat_penalty`, and `max_tokens`
(a positive integer). A profile's `max_tokens` takes precedence over
`WALTER_MAX_OUTPUT_TOKENS`. The adapter always requests streaming and
`reasoning_format: deepseek`, so reasoning is separated upstream and discarded,
not displayed or stored. Never use the raw `none` reasoning format.
Walter also requests `return_progress: true` and `timings_per_token: true`.
Recent llama.cpp servers stream `prompt_progress`; the waiting line shows
`Prefill: N%` from `processed / total` (including the cached prefix).
A nonempty `reasoning_content` delta switches the line to `Denkt nach …`;
when present, `timings.predicted_n` adds the actual generated-token count,
including any generation markers. It is activity, not percent complete:
llama.cpp does not know the eventual length of thinking.
Only phase and numeric counters reach the browser; reasoning is discarded.
Servers that omit these extensions keep the neutral waiting label until text
arrives; Walter never invents a percentage or infers thinking from the mode.
Progress disappears with the first answer snapshot and is not persisted.

The default llama.cpp profiles target `qwen38-chat`. Set `WALTER_MODEL=qwen38-chat` and
leave the profile variables unset to use these defaults. Existing nonempty
profile variables replace the whole corresponding profile; remove them or
update them to the values below to adopt the new settings.

```text
WALTER_MODEL=qwen38-chat
WALTER_QUICK_JSON={"chat_template_kwargs":{"enable_thinking":false,"preserve_thinking":false},"temperature":0.7,"top_p":0.8,"top_k":20,"min_p":0.0,"presence_penalty":1.5,"repeat_penalty":1.0,"max_tokens":2048}
WALTER_THOUGHTFUL_JSON={"chat_template_kwargs":{"enable_thinking":true,"preserve_thinking":false,"reasoning_effort":"medium"},"temperature":1.0,"top_p":0.95,"top_k":20,"min_p":0.0,"presence_penalty":0.0,"repeat_penalty":1.0,"max_tokens":8192}
```

The installed GGUF and chat template determine which thinking flags work.
Test both modes against the installed template, confirm the answer stream has no
reasoning tags, and check answer quality and token limits. Walter includes its
German system prompt and eligible conversation history in `messages`.
A byte budget is not a tokenizer/context guarantee: keep history and
output limits comfortably below the model's configured context. The backend
retains the conversation's original model alias and refuses generation if that
alias no longer matches configuration.

### OpenAI Chat Completions / Strata

Use the `openai` backend for the streaming Chat Completions API. It shares
Walter's cancellation, persistence and SSE handling, but sends no llama.cpp
extensions (`reasoning_format`, progress/timing requests or template flags).
Profiles accept `temperature`, `top_p`, `presence_penalty`, `max_tokens` and
an explicitly configured `reasoning_effort` when the provider supports it.
llama.cpp-only profile settings fail startup rather than being silently ignored.
Remove existing llama.cpp profile overrides when switching backends.

The portable defaults are `{"max_tokens":2048}` for **schnell** and
`{"max_tokens":4096}` for **bedacht**. These differ only in output budget;
they do not switch model thinking on/off. Actual reasoning controls and model
limits must be verified for the selected provider. Separated `reasoning_content`
is discarded; the provider must keep reasoning out of answer `content`.
Walter does not send a developer-role message or request tools.

The Digital Garden pi sandbox uses this Strata model and endpoint:

```text
WALTER_INFERENCE_BACKEND=openai
WALTER_INFERENCE_ENDPOINT=http://strata.internal:8080/v1/chat/completions
WALTER_MODEL=qwen3.8-flash-next-iq3_s
WALTER_INFERENCE_API_KEY_FILE=/run/secrets/walter-strata
```

Leave `WALTER_LLAMA_ENDPOINT`, `WALTER_QUICK_JSON` and
`WALTER_THOUGHTFUL_JSON` unset to use these defaults. Strata's pi configuration
declares no `reasoning_effort` support, so do not add that setting for this model.
Its declared output limit is 4096 tokens and context window is 32768; these are
configuration metadata, not live measurements.

Provide Walter with its own runtime credential and network access. Pi's
`__strata_api_key__` is a placeholder replaced by its Agent Vault proxy and is
not an API key for a direct Walter connection. The `strata.internal` hostname is
mapped by that vault; Walter's runtime needs equivalent name resolution or a
reachable endpoint. HTTP carries credentials and prompts over the trusted LAN;
use HTTPS when the deployment provides it.

Credentials are read at startup, sent only by the backend, and never exposed
through Walter's API. Restart after rotating a mounted key. Inference and context
requests reject redirects to keep prompts and credentials on the configured
endpoint. No OpenAI Responses or Anthropic Messages adapter is included.

## Behavior and interruption policy

- Walter answers in German. There is no language selector; old conversation files
  remain readable, including those previously marked English.
- The start page submits the first question directly using **schnell** or
  **bedacht**. Each follow-up can use either mode; the selected mode is saved with the conversation.
  Prompt controls are hidden until generation and reply persistence finish.
  While waiting for the first answer text, the spinner appears directly on the page background.
- The menu opens **Historie** as a separate view. Conversation URLs use
  `#gespraech/<id>` and can be bookmarked; the logo or **Neue Frage** returns
  to the start page. History, login and deletion confirmation share the same
  responsive layout. Navigation is locked while generating or saving a reply.
- Titles use the first 60 Unicode characters of the first submitted message.
- One generation per user; different users may generate concurrently. Deleting
  any chat while that user has a generation running returns 409.
- Submitting stores the user message plus a pending assistant placeholder before
  contacting inference. The placeholder becomes complete, cancelled, failed or
  partial at the end. No token-by-token disk writes.
- Stop or browser disconnect cancels the upstream HTTP request. Partial answer
  text is retained with explicit status; incomplete assistant messages are
  excluded from future inference history. User messages remain in history.
- A server timeout/error with text becomes partial; without text it becomes
  failed. A token-limit finish is partial, never a successful complete reply.
- Restart marks pending placeholders failed. In-flight tokens since the initial
  commit are lost on a crash. Sessions and OIDC flows are intentionally in memory;
  restarting signs users out, while conversations persist.
- SSE `start` identifies the assistant message; `snapshot` carries the full
  rendered reply in its `html` field, immediately for the first text and about
  every 100 ms thereafter while text changes, including during upstream pauses.
  Remaining text is flushed before completion or upstream failure;
  `done` means a complete reply was saved and synced. `error` is controlled,
  contains no upstream body or prompt. Delivery of `done` can itself fail after
  persistence; reopening the chat is authoritative. If persistence fails after
  generation, the pending record remains; inspect storage and restart recovery.
- A lost network response can leave submission acceptance uncertain. The browser
  never automatically retries a message; reopen the chat before resubmitting.
- Assistant Markdown is rendered by Goldmark on the server, including tables,
  code blocks, headings, lists and links. Raw HTML, custom attributes and unsafe
  URLs stay disabled. Streaming and reopened replies use the same renderer;
  only original Markdown is stored and sent back to inference. User messages
  remain plain text. History is sorted by most recent activity.

## Conversation context indicator

For the `llamacpp` backend, after a complete answer the follow-up controls show the retained conversation's
context usage. The backend fetches the selected model's effective per-slot
capacity from `/props?model=...` and counts tokens using
`POST /v1/chat/completions/input_tokens` (or the equivalent unversioned path).
Both modes use their actual configured profiles and the same message construction
as inference: the system prompt, user messages and complete assistant answers.
An empty next user turn includes the next turn's framing without counting a draft
or accidentally continuing the last assistant answer. Discarded reasoning and
incomplete answers are excluded. This is context occupancy, not answer progress.

Measurements are refreshed when a completed conversation is opened, including
after a reply is saved. They are hidden during generation and recovery and for
incomplete replies; they are not persisted as a potentially stale model limit.
The selected mode's percentage and token counts appear next to the send buttons.
Each button is disabled only when its measured token count reaches capacity;
when both are full, a link starts a new question. A sub-100% reading does not
promise that an arbitrarily long next question or answer will fit.

Before storing or generating a submitted question, the backend also counts the
actual prompt for the chosen mode and rejects it with 413 if there is no room
for an output token. This protects requests that bypass the browser controls and
large drafts that did not fit in the displayed remaining space. The configured
maximum output allowance is not treated as a compulsory reservation.

The auxiliary calls are bounded by a shared three-second deadline per measurement
or submission check. If metadata or token counting is unavailable, invalid or
unsupported, the UI shows `Kontext: unbekannt` and the usual generation path
remains available. The inference server still enforces its own limits. Auxiliary
URLs preserve a configured reverse-proxy base path; expose `/props` and the
input-token endpoint privately alongside chat completions for this feature.

With `openai`, Walter skips these llama.cpp-specific auxiliary calls and shows
`Kontext: unbekannt`. Generation remains available; the provider enforces its
context limit. Walter's byte-based history limit still applies.

## Authentication and trust

OIDC uses authorization code, S256 PKCE, one-use state and nonce, signature,
issuer, audience and expiry verification through go-oidc. Access requires an exact
allowed group in the ID token; missing, malformed or unrelated claims deny access.
Kanidm must emit the groups claim and grant the requested openid/profile/groups
scopes to the declared client. No email-based identity or proxy-header login.

The start page checks the local session before showing sign-in; `/auth/login`
returns directly to the start page when that session is still valid.

Session cookies are opaque 256-bit random values; only hashes are used as
server-side lookup keys. Cookies have `__Host-` names, Secure, HttpOnly, path `/`
and SameSite=Lax. Mutation requests require exact configured Origin plus a
session-bound CSRF token, including logout. Sessions are bounded by configured
TTL and ID-token expiry; role revocations take effect on reauthentication or
session expiry. Logout removes the local session; it does not terminate the
provider's SSO session.

Identity storage keys hash a JSON tuple of issuer and subject. Conversation paths
accept only server-generated hex identifiers and all operations resolve beneath
the authenticated user's directory. The data directory must be writable only by
the app/administrator; filesystem symlinks and local administrators are trusted.
The app never consults forwarded headers for origin, redirects or cookie security.
Caddy supplies HTTPS; `WALTER_PUBLIC_URL` is authoritative.

## Deployment and operations

See [deploy/README.md](deploy/README.md) for OCI installation and the acceptance checklist.

## OCI image

GitHub Actions tests and publishes `ghcr.io/mitschwimmer/walter:latest` on pushes
to `main`, plus immutable `sha-<full-commit>` tags. Version tags (`v*`) also
publish that version tag. Pull requests run tests and build without publishing.
Images currently target `linux/amd64`. Pin the published image **digest** for
installation; the workflow summary records it. See [deployment](deploy/README.md).

## Diagnostics

Failures are logged once where handled, with a fixed operation name, cause type,
filesystem errno or upstream HTTP status where available. Arbitrary upstream
error strings, response bodies, URLs, credentials and conversation text are
excluded. Browser error messages remain controlled. Configuration-file and OIDC
discovery failures preserve wrapped causes internally for error classification.
