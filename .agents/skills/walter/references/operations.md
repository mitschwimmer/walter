# Installing Walter as an OCI container

The image is `ghcr.io/mitschwimmer/walter`. GitHub Actions publishes it after the image job’s Go/parser checks
pass on `main` and `v*` tags. Browser themes and Nix/race checks run independently;
verify their success for the exact source commit before installing the image.
Tag pushes do not trigger Nix checks, so use a verified identical main commit or
run Nix checks manually. `workflow_dispatch` can also publish the selected ref’s
SHA image (and `latest` when main is selected). Inspect the Actions run summary for the accepted
image digest and pin `ghcr.io/mitschwimmer/walter@sha256:...` in deployment.
Each publishing run also uploads a `walter-image` artifact containing
`image.lock.json` (repository, digest, source revision, platform and run URL).
The Digital Garden updater consumes this record from a successful main push.
CI restores Go module/build caches and compiles once on the runner; a named
Docker build context supplies that tested output to the same runtime Dockerfile.
Superseded runs are cancelled. Plain `docker build .` remains a standalone
source build. Images currently target linux/amd64. The container runs a static Go binary as
UID/GID 65532, includes public CA trust for outbound OIDC HTTPS, and has no shell.

## Runtime contract

| Setting | Container value |
|---|---|
| Process | `/walter` |
| User | `65532:65532` |
| Listen | `0.0.0.0:8080` inside the container |
| Persistent writable directory | `/data`, owned by 65532, mode 0700 |
| Readiness | HTTP `GET /healthz` on private port 8080 |
| Shutdown | SIGTERM, allow at least 20 seconds |
| Replicas | One writer per data directory |

For Incus OCI installation, declare the pinned image, one private NIC, a protected
persistent volume at `/data`, and runtime configuration in the infrastructure
repository. Restrict app access to the TLS proxy and inference access to trusted
backends on both IPv4 and IPv6. Do not publish app port 8080 directly. The image
needs no GPU devices; inference runs in a separate service. Container UID/GID
mapping must make the mounted directory writable to UID 65532.

Mount the OIDC client secret as a read-only file owned/readable by UID 65532,
mode 0400, and set `WALTER_OIDC_CLIENT_SECRET_FILE` to its container path, e.g.
`/run/secrets/walter-oidc`. The app reads it at startup; restart after rotation.
Only the path belongs in public container configuration. Deliver plaintext using
the infrastructure's runtime secret-management mechanism, outside Git, image
layers and infrastructure state. Do not set the environment-value alternative
`WALTER_OIDC_CLIENT_SECRET` at the same time.

## Identity and model configuration

Provide all required variables from [configuration](configuration.md). Register a dedicated
confidential OIDC client with exact callback
`https://walter.example.com/auth/callback`, substituting your real public origin.
Use client_secret_basic, S256 PKCE, openid/profile/groups scopes, and an ID-token
groups array containing the groups explicitly authorized for Walter. Set
`WALTER_ALLOWED_GROUPS` to those exact values. Use discovery's exact issuer URL.

For the default `llamacpp` backend, supply the full private chat completions URL
with `WALTER_INFERENCE_ENDPOINT` (or legacy `WALTER_LLAMA_ENDPOINT`).
Verify the model alias, context size and thinking flags against the installed
GGUF/template. The default profiles target `qwen38-chat`; set `WALTER_MODEL`
to that alias and remove old `WALTER_QUICK_JSON` / `WALTER_THOUGHTFUL_JSON`
overrides, or update them to the profiles in [configuration](configuration.md). Test German
answer text and separated reasoning for both thinking modes.

For Strata or another OpenAI Chat Completions server, set
`WALTER_INFERENCE_BACKEND=openai`, `WALTER_INFERENCE_ENDPOINT` to the full chat
completions URL, and the provider's model ID. Remove llama.cpp profile overrides;
see [configuration](configuration.md) for the portable defaults and Strata example. Mount a
dedicated bearer key as a read-only file readable by UID 65532 and set
`WALTER_INFERENCE_API_KEY_FILE`. Its environment-value alternative is
`WALTER_INFERENCE_API_KEY`; never configure both. Key delivery and rotation
follow the OIDC runtime-secret procedure above. Confirm name resolution and
network access from Walter's container; Pi's vault setup does not supply them
to Walter. Context usage is unknown for this backend.

## HTTPS proxy

Caddy terminates TLS and proxies private HTTP. Keep Caddy configuration under
its existing configuration owner. The reverse proxy needs progressive flushing:

```caddyfile
reverse_proxy PRIVATE_WALTER_ADDRESS:8080 {
    flush_interval -1
    transport http {
        dial_timeout 5s
        response_header_timeout 7205s
    }
}
```

Replace the private address and match timeout budgets to generation limits.
Do not buffer SSE or impose a shorter stream timeout. Walter uses the configured
`WALTER_PUBLIC_URL` as its authoritative origin, never forwarded identity headers.

## Backup and upgrades

Stop the one writer cleanly before taking a filesystem snapshot or copy of
`/data`; encrypt backups and preserve ownership and permissions. Do not take an
unsynchronized recursive copy while the app writes. Keep runtime secrets through
the existing secret-management system, separately from chat data.

Restore into a private persistent volume with the app stopped, then start one
process. Verify user isolation and resumed conversations. Pending placeholders
recover as failed; sessions are disposable and users sign in again. Keep a
pre-upgrade snapshot and the previous image digest until acceptance passes.

## Acceptance

Before publishing the route, verify real OIDC allowed/denied group login,
logout, cookies and CSRF rejection; incremental SSE through HTTPS; both thinking
profiles; stop/disconnect and upstream failures; restart persistence; ownership
boundaries; an offline backup restore; and intended IPv4/IPv6 exposure. Source
and image builds do not establish live acceptance.
