# Identity and authorization (Milestone A)

Status: **design, being implemented** on `dev/dashboard-v2`. Covers
milestone A in `dashboard-mobile-plan.md` §7 and resolves audit finding N6
(`GUBERN_PRODUCTION_AUDIT.md`, also `docs/security.md`).

## 1. Problem

Today a valid API key is the whole authorization decision for 38 routes, and
loopback callers need no key at all for 8 more. Roles exist but only one
route group reads them (N6). There is no notion of *who* acts: a key's
`UserID` is free text, and channel users (Telegram, WhatsApp, Slack) are
environment allow-lists unrelated to any person. Nothing owns a session, a
run or an approval, so any key can read or decide anyone's. The dashboard,
mobile app and shared approvals (milestones B, E, F, H) cannot be built
safely on that.

## 2. Principles

1. **Authentication ≠ authorization.** A credential proves *which credential*
   was presented. Authorization asks which **principal** acts, which
   **scopes** it holds, and whether the **resource** is inside its boundary.
2. **Default-deny.** No identity, an inactive principal, a missing scope, an
   out-of-boundary resource, a route without a rule, or no auth layer at all:
   each is a denial. Nothing falls back to admin.
3. **The backend is authoritative.** UI visibility is a convenience, never a
   control.
4. **No self-escalation.** No principal can grant itself or anyone else a
   scope it does not hold, and models, skills and tool output can never grant
   anything.
5. **Least privilege on migration.** Legacy credentials keep what their role
   meant, never more. Approval rights need a person.

## 3. Model

```
Person ─┐                      ┌─ Credential (API key, device credential)
        ├── Principal ─────────┤
Service ┘   (id, kind,         ├─ Permission grant (scope, resource, granted_by, expiry, revoked)
             status)           ├─ External identity (provider, subject, verified, revoked)
                               └─ Device (label, public key*, status, last seen, revoked)
Resource owner: (kind, id) → principal         * public key used from milestone H
```

| Concept | Meaning | Key rules |
|---|---|---|
| **Principal** | Whoever acts. `kind` is `person` or `service`; `status` is `active` or `disabled`. Stable random id; never an email or phone number. | Disabling a principal stops every credential it owns at once. |
| **Person** | A human. Only persons may decide approvals (`approvals:decide`). | Created by the owner, or by bootstrap (the owner). |
| **Service principal** | An integration, automation or legacy key. | Never treated as a person; can never hold `approvals:decide`. |
| **External identity** | `(provider, subject)`, e.g. `(telegram, 123456)`, linked to one principal. | Unique per active link; **linked only by an explicit verification step** (pairing, milestone F), never by matching names or numbers; revocable; audited. |
| **Device** | A paired phone or computer. Table exists from A; behaviour comes in F and H. | Revocable; last seen. |
| **Credential** | Proof of possession. Today: API keys (`apikeys.json`); later device credentials. | Bound to exactly one principal; revocable on its own without touching the principal. |
| **Permission grant** | `(principal, scope, resource)`, with `granted_by`, `created_at`, optional `expires_at`, `revoked_at`. | Explicit. Roles are only templates that produce grants. |
| **Resource owner** | `(kind, id)` → principal, for sessions and Forge jobs. Runs and approvals inherit their session's owner. | Claimed atomically on first use; another principal's claim is refused. |

### Storage

- **Identity store:** a SQLite database `identity.db`, kept beside
  `apikeys.json` in the same private per-user directory. It uses the
  existing `modernc.org/sqlite` driver, WAL mode and foreign keys. Schema
  changes are **versioned migrations** keyed on `PRAGMA user_version`, each
  applied in one transaction; a test runs every migration on an empty
  database and again on a migrated one.
- **Credential store:** `apikeys.json` stays where it is (hashes only). It is
  the credential store, not a second identity database. The binding from key
  id to principal lives in the identity store.
- **Identity audit:** every lifecycle change (principal created or disabled,
  grant added or revoked, credential bound or revoked, external identity
  linked or unlinked, owner recovery) is written to the identity audit table
  and to the server's HMAC-chained audit log when one is configured.

## 4. Scopes

The vocabulary comes from the real route list (§7), not from aspiration.

| Scope | Allows | Resource-scoped |
|---|---|---|
| `system:read` | status, metrics, healing, plugins, AI cost, key status | no |
| `system:manage` | config reload, shutdown | no |
| `audit:read` | audit log, chain verification | no |
| `security:read` | secrets status, server logs | no |
| `identity:manage` | API keys and grants | no |
| `workspaces:manage` | namespaces | no |
| `agents:read` | agent status, catalog, team roster | no |
| `agents:run` | submit to the coordinator, chat with a specialist | session |
| `sessions:read` | session list and history; cross-session comms feeds need the `*` resource | session |
| `approvals:read` | pending approvals, checkpoints, the attention feed's approval items | session |
| `approvals:decide` | approve or reject actions and checkpoints, edit checkpoints. **Person principals only.** | session |
| `runs:read` | orchestration runs and tasks, Forge jobs, research reports | session or job |
| `runs:write` | start orchestration, Forge builds, pipeline analysis | no |
| `devops:read` | DevOps server list | no |
| `devops:run` | DevOps commands on remote servers | no |
| `models:use` | the OpenAI-compatible `/v1/*` API | no |
| `notify:send` | send a notification through configured channels | no |
| `files:read`, `files:write`, `devices:read`, `devices:manage`, `integrations:read`, `integrations:manage` | **Reserved** for milestones F and H (no routes yet) | workspace or device |

**No implication between scopes.** `approvals:read` does not imply
`approvals:decide`, `sessions:read` does not imply every session, and
`devices:read` does not imply `devices:manage`.

**Resource values on a grant:**

- `own`: resources this principal owns;
- `*`: any resource;
- `<kind>:<id>`: exactly one resource.

A grant without a resource value is invalid.

### Role templates (applied when a key or person is created; editable later)

| Template | Grants |
|---|---|
| `owner` | every scope on `*` |
| `admin` | every scope on `*`; for a **service** principal, the same minus `approvals:decide` |
| `operator` | `system:read`, `agents:read`, `agents:run` own, `sessions:read` own, `approvals:read` own, `approvals:decide` own (persons only), `runs:read` own, `runs:write`, `devops:read`, `devops:run`, `models:use`, `notify:send` |
| `viewer` | `system:read`, `audit:read`, `security:read`, `agents:read`, `sessions:read` own, `approvals:read` own, `runs:read` own, `devops:read` |
| `readonly` | `system:read` |

A viewer therefore can never approve: it holds no `approvals:decide` grant.

## 5. Decision procedure

`authz.Decide(principal, scope, resource)` is a pure function with no I/O. It
can be tested exhaustively and returns a decision with a reason:

1. No principal → **deny** (`unauthenticated`).
2. Principal not active → **deny** (`inactive`).
3. `scope == approvals:decide` and the principal is not a person → **deny**
   (`not-a-person`).
4. Find an unexpired, unrevoked grant for `scope` → none: **deny**
   (`missing-scope`).
5. No resource (a route-level check) → **allow**.
6. Any grant on `*` → **allow**. A grant on `<kind>:<id>` equal to the
   resource → **allow**. A grant on `own` where the resource's owner is this
   principal → **allow**.
7. Otherwise **deny** (`out-of-boundary`).

Grants are read from the store for **every request**, not cached, so a
revocation applies to the very next request (test: *revoked permission takes
effect*).

## 6. Enforcement

```
request ─► authenticate (credential) ─► resolve principal (identity store) ─► route rule ─► handler ─► resource check
             401 if none/invalid           401 if unbound/revoked              403 if scope     403 if out of boundary
```

- **One registry.** Every protected route is registered as
  `s.handle(pattern, rule, handler)`. The rule names the scope, and optionally
  the resource kind the handler resolves.
- **Unknown routes are denied.** Registering a route without a rule is a
  programming error that fails a test (`TestEveryProtectedRouteHasARule`),
  and at runtime a missing rule is a 403.
- **No auth layer means deny.** Server instances without an auth layer refuse
  protected routes (503). Unit tests of other features opt out with a
  test-only switch that production code cannot set.
- **Resource checks** live in handlers, because the id comes from the path or
  body. Every check goes through one helper,
  `s.authorizeResource(r, scope, kind, id)`. Lists are filtered through the
  same helper, so a caller never sees an item they could not open.
- **Trusted context only.** The principal comes solely from the
  authenticated credential. A body field, query parameter or header can name
  a resource, but never an identity. No `X-User-*` header is read. The audit
  log records the principal id, never a caller-supplied name.
- **Loopback trust** (`GUBERN_TRUST_LOOPBACK`, default on) no longer skips
  authorization. It authenticates as the built-in service principal
  `local-loopback`, granted exactly what loopback could reach before:
  `system:read`, `system:manage`, `audit:read`, `security:read`. Relayed
  requests (fix F-06) never qualify.

## 7. Route authorization matrix

Authentication is required on every route below. "Resource" is the boundary
checked in the handler. "Any" in the principal column means a person or a
service principal.

| Method | Route | Scope | Resource | Principals | Before A |
|---|---|---|---|---|---|
| POST | `/internal/reload` | `system:manage` | — | any | `protected` |
| POST | `/internal/shutdown` | `system:manage` | — | any | `protected` |
| GET | `/metrics` | `system:read` | — | any | `protected` |
| GET | `/api/status` | `system:read` | — | any | `protected` |
| GET | `/api/plugins` | `system:read` | — | any | `protected` |
| GET | `/api/secrets/status` | `security:read` | — | any | `protected` |
| GET | `/api/audit` | `audit:read` | — | any | `protected` |
| POST | `/api/audit/verify` | `audit:read` | — | any | `protected` |
| GET | `/api/keys` | `identity:manage` | — | any | admin |
| POST | `/api/keys` | `identity:manage` | no escalation: requested grants ⊆ caller's | any | admin |
| DELETE | `/api/keys/{id}` | `identity:manage` | — | any | admin |
| GET | `/api/namespaces` | `workspaces:manage` | — | any | admin |
| POST | `/api/namespaces` | `workspaces:manage` | — | any | admin |
| DELETE | `/api/namespaces/{id}` | `workspaces:manage` | — | any | admin |
| GET | `/api/namespaces/{id}/stats` | `workspaces:manage` | — | any | admin |
| GET | `/api/ai/cost` | `system:read` | — | any | any key (N6) |
| GET | `/api/keys/status` | `system:read` | — | any | any key |
| GET | `/api/healing/status` | `system:read` | — | any | any key |
| GET | `/api/healing/events` | `system:read` | — | any | any key |
| GET | `/api/logs` | `security:read` | — | any | any key |
| GET | `/api/research/reports` | `runs:read` | report: needs `*` until reports carry an owner | any | any key |
| GET | `/api/research/reports/{name}` | `runs:read` | report: needs `*` | any | any key |
| GET | `/api/devops/servers` | `devops:read` | — | any | any key |
| POST | `/api/devops/command` | `devops:run` | — | any | any key |
| POST | `/api/pipeline/analyze` | `runs:write` | — | any | any key |
| POST | `/api/orchestrate` | `runs:write` | session in body: claimed or owned | any | any key |
| GET | `/api/orchestration/runs` | `runs:read` | filtered to runs whose session the caller may read | any | any key |
| GET | `/api/orchestration/runs/{id}/tasks` | `runs:read` | the run's session | any | any key |
| POST | `/api/agents/submit` | `agents:run` | session: claimed on first use, else owned | any | any key |
| GET | `/api/agents/status` | `agents:read` | — | any | any key |
| GET | `/api/agents/catalog` | `agents:read` | — | any | any key |
| POST | `/api/agents/approve` | `approvals:decide` | session | **person** | any key |
| GET | `/api/agents/pending` | `approvals:read` | filtered by session | any | any key (unpushed) |
| GET | `/api/attention` | `approvals:read` (approval items), `system:read` (health items) | filtered per item | any | any key (unpushed) |
| GET | `/api/agents/history` | `sessions:read` | filtered by session | any | any key |
| GET | `/api/agents/history/{id}` | `sessions:read` | session | any | any key |
| POST | `/api/forge/build` | `runs:write` | job: owner recorded | any | any key |
| GET | `/api/forge/status/{id}` | `runs:read` | job | any | any key |
| GET | `/api/forge/jobs` | `runs:read` | filtered by job | any | any key |
| POST | `/api/notify` | `notify:send` | — | any | any key |
| GET | `/v1/models` | `models:use` | — | any | any key |
| POST | `/v1/chat/completions` | `models:use` | — | any | any key |
| POST | `/v1/responses` | `models:use` | — | any | any key |
| GET | `/api/team/agents` | `agents:read` | — | any | any key |
| GET | `/api/team/status` | `agents:read` | — | any | any key |
| GET | `/api/agents/team/status` | `agents:read` | — | any | any key |
| GET | `/api/agents/comms` | `sessions:read` | cross-session feed: needs `*` | any | any key |
| GET | `/api/agents/comms/stream` | `sessions:read` | cross-session feed: needs `*` | any | any key |
| GET | `/api/agents/team/{id}/messages` | `sessions:read` | cross-session feed: needs `*` | any | any key |
| POST | `/api/agents/{id}/chat` | `agents:run` | session in body | any | any key |
| GET | `/api/checkpoints` | `approvals:read` | filtered by the checkpoint's session | any | any key |
| POST | `/api/checkpoints/{id}/approve` | `approvals:decide` | the checkpoint's session | **person** | any key |
| POST | `/api/checkpoints/{id}/reject` | `approvals:decide` | the checkpoint's session | **person** | any key |
| POST | `/api/checkpoints/{id}/edit` | `approvals:decide` | the checkpoint's session | **person** | any key |

**Not in the matrix, with the reason:**

- `/health`, `/ready` and `/dashboard/` are public by design. They expose no
  data beyond the public health document and the static UI.
- `/studio/` keeps its admin-key gate. Moving it to `identity:manage`-style
  scopes is tracked as a gap.
- `/a2a/` is in-process only (shared token).
- `/webhook/*` is per-channel signed traffic. Those identities are channel
  external identities (§8).

## 8. Channel identities (Telegram, WhatsApp, Slack)

Milestone A provides the **model and the store**: external identities,
verification, uniqueness, revocation and audit. It does not yet route channel
messages through scopes; that comes in B, where approvals move to the shared
service, and F, where pairing creates verified links.

Until then:

- The environment allow-lists keep working as the channel's own access
  control.
- **One hard rule already applies:** an external identity that has been
  **revoked** in the identity store is refused by its channel even if it is
  still in an environment allow-list. Revocation has to win.

## 9. Legacy credential migration

On first start with the identity store, as one idempotent migration step:

| Existing key | Becomes | Why |
|---|---|---|
| Admin role, description `tui-setup-key`, `auto-created` or `setup key` | Bound to the **owner person** (created at bootstrap) | These are the local operator's own bootstrap keys, created on this machine for the TUI and setup |
| Any other key | A **legacy service principal** `legacy-key-<id>`, with grants from its role template **as a service** | Keeps what the role meant; never `approvals:decide`, because a key's holder is not a verified person |
| A key with no or unknown roles | A legacy service principal with **no grants** | It can't be classified safely, so it gets nothing; logged |
| A revoked key | Not migrated (its tombstone stays) | — |

**Effects:**

- `viewer`, `operator` and `readonly` keys lose the access N6 wrongly gave
  them. This is the fix.
- Legacy **admin** service keys keep admin scopes **except approvals**. If a
  person used such a key to approve, the owner binds the key to that person
  (`gubern identity bind-key <key-id> --person <id>`), which restores
  approvals for a verified human.
- Nothing is silently promoted: no key gains a scope its role did not
  include.

**Rollback:** an older binary ignores `identity.db` and runs with its old
behaviour, **which reopens N6**. The identity store is left intact, so
upgrading again resumes exactly where it was. Rollback is therefore possible
but not safe, and the release notes must say so.

## 10. Owner bootstrap and recovery

- **Bootstrap:** the first start with an empty identity store creates one
  person, *Owner*, with the `owner` template. Its credential is the existing
  `tui-key` / setup key, or the key auto-created for an empty store, as today.
- **Recovery:** `gubern identity recover-owner`, a local CLI command with no
  network endpoint:
  1. It refuses while the server answers on its management address. Stopping
     the server proves control of the machine, and avoids two writers on the
     key store.
  2. It issues a new owner credential, binds it to the owner person
     (re-enabling that person if disabled), and writes the secret to the
     `tui-key` file (mode 0600).
  3. It prints only the key id, never the secret.
  4. It records an audit event.

  Holding filesystem access to the server's private config directory is the
  proof of ownership, the same trust boundary that protects `apikeys.json`
  itself.

## 11. Decisions (ADR summary)

| # | Decision | Rejected alternatives |
|---|---|---|
| ADR-A1 | Scopes with resource values, roles as templates | **Pure RBAC:** too coarse for "your own sessions only", and it caused N6. **ABAC/OPA policies:** more power than needed now; the OPA engine stays for the edge. |
| ADR-A2 | Identity in one SQLite DB beside the key store; keys stay in `apikeys.json` | **Move keys into SQLite:** churn and migration risk with no security gain now. **A separate identity service:** a second process for a single-node product. |
| ADR-A3 | Grants read per request, no cache | **Cached grants:** stale permissions after revocation. A single-node SQLite read is cheap enough. |
| ADR-A4 | Only persons may decide approvals | **Any key with the scope:** an automation could approve itself, which defeats human-in-the-loop. |
| ADR-A5 | Ownership claimed on first use (sessions), recorded at creation (jobs) | **Owner sent by the client:** spoofable. **Caller-chosen ids treated as private:** guessable. |
| ADR-A6 | Loopback trust becomes a fixed service principal | **Remove loopback trust:** breaks the local CLI and status flows. **Keep the bypass:** routes would run with no identity. |
| ADR-A7 | Owner recovery only through a local CLI with the server stopped | **A recovery HTTP endpoint:** an unauthenticated network path to full control. |

## 12. Not in milestone A

- The persistent approval service (B).
- The event log and SSE replay (D).
- Pairing, device credentials and step-up (F, H).
- Routing channel messages through scopes (B, F).

The tables and interfaces they need exist, so none of these require changes
to the person model.
