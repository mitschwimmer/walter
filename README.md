# Walter

Walter is a small German web UI for private conversations with llama.cpp. One Go process,
embedded HTML/CSS/ES modules, per-conversation JSON, native Kanidm OIDC, no
frontend build step. Inference is accessible only through the backend.

## Build and test

Go 1.25 or newer is required. The only direct dependencies are go-oidc and oauth2;
all dependencies are pinned by `go.mod` and `go.sum` and work without CGo.

```sh
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build -trimpath -o walter .
```

Optional browser parser regression test (Node is only a test tool):

```sh
node tests/sse.test.mjs
```

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
| `WALTER_OIDC_CLIENT_SECRET` | Required secret, delivered at runtime |
| `WALTER_ALLOWED_GROUPS` | `walter-users`, exact comma-separated claim values |
| `WALTER_MODEL` | Required model alias verified in the inference catalog |
| `WALTER_QUICK_JSON` | Required nonempty JSON object of verified model settings |
| `WALTER_THOUGHTFUL_JSON` | Required nonempty JSON object of verified model settings |
| `WALTER_LLAMA_ENDPOINT` | Required full private chat completions URL |
| `WALTER_LISTEN` | `127.0.0.1:8080`; select a private interface for a separate Caddy guest |
| `WALTER_DATA_DIR` | `data`; use an absolute persistent volume path in production |
| `WALTER_GENERATION_TIMEOUT` | `2h`, includes cold model loading and streaming |
| `WALTER_SESSION_TTL` | `12h`, also bounded by the ID token expiry |
| `WALTER_MAX_HISTORY_BYTES` | `48000`, rejects additional messages rather than silently truncating |
| `WALTER_MAX_OUTPUT_TOKENS` | `4096` |

Model settings allow `reasoning_effort`, `chat_template_kwargs`, `temperature`,
`top_p`, `top_k`, and `min_p`. The adapter always requests
`reasoning_format: deepseek`, so reasoning is separated upstream and discarded,
not displayed or stored. Never use the raw `none` reasoning format.

The installed GGUF and chat template determine which thinking flags work.
Both profiles are required instead of assigning unverified defaults.
A **candidate to verify**, not an accepted model configuration, is:

```text
WALTER_QUICK_JSON={"reasoning_effort":"none","chat_template_kwargs":{"enable_thinking":false}}
WALTER_THOUGHTFUL_JSON={"chat_template_kwargs":{"enable_thinking":true}}
```

Test both against the installed template, confirm the answer stream has no
reasoning tags, check answer quality and token limits, then supply the accepted
profiles. A byte budget is not a tokenizer/context guarantee: keep history and
output limits comfortably below the model's configured context. The backend
retains the conversation's original model alias and refuses generation if that
alias no longer matches configuration.

## Behavior and interruption policy

- Language and thinking mode are fixed per conversation. UI text stays German.
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
- SSE `start` identifies the assistant message; `delta` carries answer text;
  `done` means a complete reply was saved and synced. `error` is controlled,
  contains no upstream body or prompt. Delivery of `done` can itself fail after
  persistence; reopening the chat is authoritative. If persistence fails after
  generation, the pending record remains; inspect storage and restart recovery.
- A lost network response can leave submission acceptance uncertain. The browser
  never automatically retries a message; reopen the chat before resubmitting.
- Model output is plain text through `textContent`, with no HTML or Markdown
  execution. History is sorted by most recent activity.

## Authentication and trust

OIDC uses authorization code, S256 PKCE, one-use state and nonce, signature,
issuer, audience and expiry verification through go-oidc. Access requires an exact
allowed group in the ID token; missing, malformed or unrelated claims deny access.
Kanidm must emit the groups claim and grant the requested openid/profile/groups
scopes to the declared client. No email-based identity or proxy-header login.

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
