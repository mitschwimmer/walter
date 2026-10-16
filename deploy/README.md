# Installing Walter as an OCI container

The image is `ghcr.io/mitschwimmer/walter`. GitHub Actions publishes it after tests
pass on `main` and version tags. Inspect the Actions run summary for the accepted
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

Provide all required variables from the root README. Register a dedicated
confidential OIDC client with exact callback
`https://walter.example.com/auth/callback`, substituting your real public origin.
Use client_secret_basic, S256 PKCE, openid/profile/groups scopes, and an ID-token
groups array containing the groups explicitly authorized for Walter. Set
`WALTER_ALLOWED_GROUPS` to those exact values. Use discovery's exact issuer URL.

`WALTER_LLAMA_ENDPOINT` is required: supply the full private chat completions URL.
Verify the model alias, context size and thinking flags against the installed
GGUF/template. Both thinking profiles are deliberately required. Test answer
text and separated reasoning for both languages and both thinking modes.

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
