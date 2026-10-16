# Validation

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
