# API Reference

The management API listens on `:9090` (configurable). Responses are JSON.
Every response carries an `X-Correlation-ID` and the security headers
(HSTS over TLS, `X-Content-Type-Options`, CSP, etc.).

Machine-readable sketch: [openapi.yaml](openapi.yaml).

## Authentication

Protected endpoints require an API key, sent as `Authorization: Bearer <key>`
or `X-API-Key: <key>`. Issue keys with `POST /api/keys` or `gubern setup`.

Each key authenticates as a principal, and every route requires one scope
(the full route → scope matrix is in `docs/design/identity-authorization.md`
§7). Errors:

- `401 {"error":"authentication required","reason":...}`: no key, an
  invalid or revoked key, or a key bound to no principal.
- `403 {"error":"forbidden","reason":...}`: `missing-scope`,
  `out-of-boundary` (someone else's session, job or report), `not-a-person`
  (only people decide approvals) or `inactive`.
- `503 {"error":"authorization unavailable"}`: the identity store is not
  available; nothing is allowed.

- **Public** (no auth): `/health`, `/ready`, `/dashboard/`.
- **Loopback** without a key: only `/internal/*`, `/metrics`, `/api/status`,
  `/api/plugins`, `/api/secrets/status` and `/api/audit*`; set
  `GUBERN_TRUST_LOOPBACK=false` to require a key there too.
- Everything else always requires a key.

Rate limits apply per source IP, per API key (1000 req/min default; admin keys
unlimited), and globally (10000 req/min), plus per-IP burst auto-banning.

## Health & readiness

```
GET /health
→ {"status":"ok","version":"v0.2.0","config_hash":"…","uptime":"3m12s"}

GET /ready
→ 200 {"ready":true}   or   503 {"ready":false,"reason":"agent queue saturated (…)"}
```

`cluster_name` and `routes` (per-route name, protocol, listen address and
active connections) are included in `/health` only for trusted loopback
callers (the CLI, the local dashboard and TUI). Remote callers get status,
version, config hash and uptime; route counts are in the authenticated
`GET /api/status`.

## Status & metrics

```
GET  /api/status          → extended status (TLS provider, routes, plugins, …)
GET  /api/ai/cost         → today's AI spend and budget
GET  /metrics             → Prometheus exposition
```

## Agents

```
POST /api/agents/submit
  body: {"message":"...","session_id":"optional"}
  Accept: text/event-stream  → SSE chunks: data: {"chunk":"..."} …, then event: done
  else                       → {"response":"...","session_id":"..."}

GET  /api/agents/status              → {active_agents, total_messages, queue_depth}
POST /api/agents/approve             → {"session_id":"...","approved":true,"action_id":"optional"}
GET  /api/agents/history             → list sessions (newest first)
GET  /api/agents/history/{id}        → messages for a session
```

`session_id` must match `^[A-Za-z0-9_.-]{1,64}$`. Responses are capped at 8 MB.

Each approval line ends with `(action <id>)`, a 16-hex-digit digest of the
tool, its parameters and the preview. Send that id as `action_id` and the
decision applies only to that action: if a different one is pending the call
returns `409` and nothing changes. Without `action_id` the call resolves
whatever is pending for the session, as before; the TUI always sends it.

SSE chunks stream **as the model generates them** (true token streaming):
direct coordinator answers arrive line by line while the reply is being
written; routed tasks (team, build, research, …) arrive as a single chunk
when they complete. Concatenating all chunks always yields exactly the
non-streaming `response` text.

## Skills

Workspace [Agent Skills](agents.md) (`SKILL.md` folders under `.agents/`,
`.claude/` or `.opencode/skills`), the same set `/agent` offers the model.
Skills are knowledge, never authority: reading one runs nothing, and
`allowed_tools` is a request that policy may narrow, never a grant.

```
GET  /api/skills                  (agents:read)   → {"skills":[...],"problems":[{"path","error"}]}
GET  /api/skills/{name}           (agents:read)   → {"skill":{...},"body":"SKILL.md instructions"}
POST /api/skills/{name}/disable   (system:manage) → {"skill":{...,"enabled":false}}
POST /api/skills/{name}/enable    (system:manage) → {"skill":{...,"enabled":true}}
```

A skill is `{name, description, scope, path, hash, license?, compatibility?,
allowed_tools?, enabled}`. `path` is relative to the workspace and `hash` is
the SHA-256 of its `SKILL.md`. A disabled skill stays on disk but is left out
of the catalog the model sees. The switches are kept in
`<user config>/gubern/skills-state.json`, and each change is audit-logged as
`skill.enabled` or `skill.disabled`. A file that changed after it was listed
is refused on read (`500`) until it is listed again.

### Installing a skill

```
POST /api/skills/install  (system:manage)  {"source": "<folder> | https://host/repo.git@<40-hex commit>"}
  → 202 {approval_id, session_id, source, tree_hash, skill, files:[{path,size,sha256}]}
```

The skill is copied into quarantine (`<user config>/gubern/skills-quarantine`),
checked against the spec and hashed, file by file. Git sources must name a
full commit, so the content can't move after review; only https is fetched,
with no hooks or submodules. Symlinks, special files and anything over 200
files or 4 MB are refused.

Nothing is installed yet: the call opens a **HIGH RISK approval** whose
preview lists every file, its SHA-256 and the tree hash. The requester owns
the approval's session, so they or their paired phone (with step-up) can
decide it. Only on approve is the skill moved to
`<workspace>/.agents/skills/<name>` and recorded in
`<user config>/gubern/skills.lock` with the source, tree hash, approver and
approval id. Rejected or expired installs are deleted from quarantine. Audit
actions: `skill.install_requested`, `skill.installed`,
`skill.install_declined`, `skill.install_failed`.

A skill in the lock whose files no longer match its tree hash is reported
with `"tampered": true`. It is not offered to the model until it is
reinstalled. Pinned skills carry `"pinned": true` and their `source`.

## OpenAI-compatible API

Any OpenAI-speaking tool (Claude Code, Aider, Cline, Cursor, …) can use
Gubern as its AI backend — with provider routing, failover, budget, and cost
tracking — by pointing `OPENAI_BASE_URL` at Gubern:

```
export OPENAI_BASE_URL=http://localhost:9090/v1
export OPENAI_API_KEY=<your-gubern-key>
```

```
GET  /v1/models            → servable model IDs (one per configured provider model)
POST /v1/chat/completions  → standard chat-completions body
                             "stream": true → SSE chat.completion.chunk frames,
                             forwarded live as the provider produces tokens,
                             then a usage frame and data: [DONE]
POST /v1/responses         → minimal OpenAI Responses API (buffered)
```

Auth is unified: the same key works via `Authorization: Bearer` or
`X-API-Key`. Requests route to the provider serving the requested model
(exact match on configured models, then family heuristics, then the primary
provider). Mid-stream provider errors are reported in-band as an SSE
`{"error": …}` object, since the 200 status is already committed once
streaming starts.

## Gubern Forge

```
POST /api/forge/build       → start an app build
GET  /api/forge/status/{id} → build job status
GET  /api/forge/jobs        → list jobs   (503 if no AI provider configured)
```

## API keys (`identity:manage`)

```
GET    /api/keys                 → list keys for ?org= (hashes never exposed)
POST   /api/keys                 → issue: {user_id, org_id, roles, ttl_seconds, rate_limit_rpm}
                                   → {id, secret, principal_id, …}  (secret shown once)
DELETE /api/keys/{id}            → revoke (and unbind)
```

A new key gets its own **service principal** with the grants of `roles`
(`admin`, `operator`, `viewer`, `readonly`); the caller must already hold
every one of those grants (403 otherwise). Service principals never decide
approvals. People get keys with `gubern identity add-person` on the server
machine.

Keys are bcrypt-hashed (SHA-256 prehash), persisted atomically on issue/revoke.

## Audit

```
GET  /api/audit             → recent entries
POST /api/audit/verify      → verify the hash chain
```

CLI equivalents add reporting and archival:

```bash
gubern audit verify
gubern audit report --since 2026-01-01 --format markdown --output report.md
gubern audit archive
```

## Other data endpoints

```
GET  /api/healing/status         GET  /api/research/reports
GET  /api/devops/servers         POST /api/pipeline/analyze
GET  /api/namespaces             POST /api/orchestrate
GET  /api/secrets/status         GET  /api/plugins
GET  /api/logs
```

## Control plane (Windows-safe SIGHUP/SIGTERM)

```
POST /internal/reload       → re-read + re-validate config (gubern reload)
POST /internal/shutdown     → graceful shutdown (gubern stop)
```

Both honour loopback trust (see Authentication above).
