# Walter

Walter is a small German web UI for private conversations with llama.cpp or an
OpenAI Chat Completions backend such as Strata. One Go process,
embedded HTML/CSS/ES modules, per-conversation JSON, native Kanidm OIDC, no
frontend build step. Inference is accessible only through the backend.

Start with the [Walter maintenance skill](.agents/skills/walter/SKILL.md) for
building, testing, configuration, debugging, and deployment.

## Session diagnostics

Failed HTTP requests replay structured events with a generated `request_id`, a static
`route` pattern, the response status and, when relevant, a fixed `auth_reason`
or `rejection_reason`. A `session_expired` reason means the local application
TTL elapsed; `session_unknown` means Walter has no record of the supplied cookie
(for example, after a process restart). A `cookie_missing` reason means no session
cookie was sent. Successful requests remain silent. No cookie, identity, request
path, prompt or token value is logged.

OIDC ID tokens are validated at login; their expiry no longer shortens the local
session TTL. Walter still stores sessions only in memory, so restarts require
signing in again.
