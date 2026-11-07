# Approval service (Milestone B)

Status: **implemented** on `dev/dashboard-v2`. Depends on milestone A
(`identity-authorization.md`).

## 1. Problem

Before B, an approval-gated agent action lived in three places:

- the coordinator's in-memory pending map;
- Telegram's own approval map, with its own ids and timeout;
- an API path that raced the two.

None of these was persisted, and none recorded who decided. A restart lost
everything that was pending. WhatsApp and Slack could not decide at all.

## 2. Model

`internal/approvals` stores one record per approval in `approvals.db`. The
file sits beside the key and identity stores, is created 0600, and uses
versioned migrations (`PRAGMA user_version`).

| Field | Meaning |
|---|---|
| `id` | 16 random hex digits. The TUI's `(action <id>)` marker and the `action_id` API field carry it. |
| `action_hash` | Full SHA-256 of the canonical action: tool, parameters, command, description and preview. The JSON encoding has its object keys sorted. |
| `status` | `pending` → one of `approved`, `rejected`, `expired`, `cancelled`. Every status except `pending` is final. |
| `expires_at` | `created_at` + TTL. The TTL defaults to 15 min; `GUBERN_APPROVAL_TTL` sets it, from 1 s to 24 h. |
| `decided_by` | The principal id. For a channel user not linked to a principal, it is `<channel>:<subject>`. |
| `decided_via` | `api`, `dashboard`, `mobile`, `tui`, `telegram`, `whatsapp`, `slack`, `discord`, or `system` (expiry, cancellation). |
| `reason` | The decider's optional note, or why the system closed the approval. |

## 3. One decision path

```
any channel ─► Coordinator.DecideApproval ─► approvals.Service.Decide ─► (winner) run the parked action
```

`Decide` runs these checks in order:

1. The approval exists.
2. It is still pending.
3. It has not expired. If it has, it is marked `expired`.
4. If the decision names an action hash, the hash matches.
5. The authorizer accepts the decider.

It then makes **one conditional UPDATE**:
`... WHERE id = ? AND status = 'pending'`. Exactly one concurrent decision
changes a row. Every other decision gets `ErrNotPending` and changes nothing.

How the decision is used depends on where the action came from:

- **Parked actions** come from file and terminal tools started by chat, the
  TUI or a channel. The winning decision runs the action, after
  recomputing its hash from the live parameters. If anything changed, the
  action does not run.
- **Waiting runs** come from `/agent` loops, sandboxed tools and DevOps. The
  run blocks in `Service.Wait` until the approval reaches a final status. The
  loop also re-checks the hash of the request before running it.

A session has at most one open approval. A newer action cancels the older
one ("superseded"), so an old id can never approve a new action.

## 4. Who may decide

The approval authorizer is `internal/cmd/approvals.go`. It reuses milestone
A's scopes:

| Decider | Rule |
|---|---|
| API, dashboard, mobile app, TUI | The authenticated principal must be a **person** holding `approvals:decide` on the approval's session: its own session, or `*`. Service principals never qualify. |
| Channel user **linked** to a principal (pairing, milestone F) | The same rule, applied to the linked principal. The decision is recorded under that principal. |
| Channel user whose link was **revoked** | Refused. |
| Channel user **not linked** yet | Accepted if the channel's own allow-list admitted them (Telegram allowed ids, WhatsApp allowed numbers, Slack operator users). `GUBERN_REQUIRE_LINKED_APPROVERS=true` refuses them. Pairing (milestone F) now creates links. Requiring linked approvers stays opt-in, so existing allow-list setups keep working on upgrade; see `pairing.md` §5. |
| Anything else, including the legacy session-only call without a decider | Refused. A service with no authorizer configured refuses every decision. |

## 5. Channels

- **Telegram:** each new approval is posted to `GUBERN_TELEGRAM_DEFAULT_CHAT`
  with Approve and Reject buttons (`approve:<id>`, `reject:<id>`). A button
  press by an allowed user, or `/approve [id]` / `/reject [id]`, decides as
  `telegram:<user id>`.
- **WhatsApp:** each approval is announced to `GUBERN_WA_DEFAULT_PHONE` with
  buttons. A button reply, or `/approve [id]` / `/reject [id]`, decides as
  `whatsapp:<number>`.
- **Slack:** `/vortex approve <id>` and `/vortex reject <id>` decide as
  `slack:<team>-<user>`. Only operator-level users can use them.
- **When an approval ends**, every channel that did not decide it is told the
  outcome, so no phone keeps showing live buttons for a settled action.
- **`GUBERN_TELEGRAM_API_URL`** points the bot at a self-hosted Bot API server
  (or a test double). It must be https unless the server is on this machine.

## 6. API

| Route | Scope | Notes |
|---|---|---|
| `GET /api/approvals?status=&session_id=&limit=` | `approvals:read` | Only approvals of sessions the caller may read. |
| `GET /api/approvals/{id}` | `approvals:read` | |
| `POST /api/approvals/{id}/decision` | `approvals:decide` | Body: `{approved, action_hash (required), reason?, via?}`. Answers 200 with the approval, or 409 with `not-pending`, `expired` or `action-mismatch`. |
| `POST /api/agents/approve` | `approvals:decide` | Kept for the TUI. `action_id` is the approval id. It takes the same decision path. |
| `GET /api/agents/pending` | `approvals:read` | Lists only open, unexpired approvals from the store. |

## 7. Restart handling

When the service opens, every approval still `pending` from the previous
process becomes `cancelled` ("server restarted") and is audited. The run
waiting for it is gone, so a decision made afterwards must not run anything.
A graceful stop cancels open approvals earlier, as their runs end ("run ended
before a decision").

## 8. Audit

Every lifecycle event goes to the HMAC-chained audit log, with the decider as
the actor and the session, tool, risk, action hash, status, channel and
reason:

- `approval.requested`
- `approval.approved`
- `approval.rejected`
- `approval.expired`
- `approval.cancelled`
- `approval.mismatch`
- `approval.refused`

## 9. Not in B

- **Team checkpoints** (`/api/checkpoints`) keep their own store. They are
  authorized by A's scopes, but are not yet approval records.
- **Discord** arrives with milestone I. **Mobile** step-up signatures arrive
  with milestone H.
- **Pending approvals don't survive a restart.** By design, they are
  cancelled rather than resumed.
