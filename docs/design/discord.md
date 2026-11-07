# Discord (Milestone I)

Status: **implemented** on `dev/dashboard-v2`. Depends on B (shared
approvals) and F (pairing).

## 1. Shape

Gubern answers Discord's **HTTP interactions** endpoint at
`/webhook/discord`, so it needs no long-lived gateway connection and no
privileged intents.

**Verification:** every request is checked with the application's Ed25519
public key over `timestamp ‖ body`. A missing, forged or tampered signature,
or a timestamp more than 5 minutes away, gets 401 before anything is parsed
or decided.

**Commands** (register them in the Discord developer portal):

| Command | What it does |
|---|---|
| `/ask message:<text>` | Sends the message to the agent in session `discord:<user>`. Acknowledged at once (deferred, ephemeral); the answer replaces the reply. |
| `/approve id:<approval id>`, `/reject id:<approval id>` | Decides an approval. |
| `/pair code:<code>` | Links the Discord account to a person (F). |

**Buttons:** approval notices posted to `GUBERN_DISCORD_APPROVAL_CHANNEL`
carry Approve and Reject buttons (`approve:<id>`, `reject:<id>`).

**Privacy:** replies are ephemeral, so only the person who acted sees them,
and mentions are disabled in everything Gubern posts.

## 2. Same rules as every channel

| Rule | How Discord follows it |
|---|---|
| One decision path (B) | Buttons and `/approve` / `/reject` call the shared `DecideApproval` as `discord:<user id>`. The first decision across all channels wins. |
| Who may act | A user on `GUBERN_DISCORD_ALLOWED_USERS`, or one linked by pairing. Everyone else gets a pointer to `/pair`. |
| Linked users | Decide as their person. B's authorizer applies `approvals:decide` on the session, and the decision is recorded under the person's principal id. |
| Revocation wins (F) | A revoked link is refused even when the allow-list names the user. |
| Pairing | `/pair` works for anyone, because the code is the credential. It is rate limited per account, single use, and refuses accounts linked to someone else. A code made for another channel is refused. |
| Notices | New approvals are posted with buttons. When an approval is decided elsewhere, the outcome is posted, so no live buttons are left for a settled action. |

## 3. Configuration

| Variable | Meaning |
|---|---|
| `GUBERN_DISCORD_PUBLIC_KEY` | The application public key (hex). Required; without it the endpoint is not mounted. |
| `GUBERN_DISCORD_BOT_TOKEN` | Used for posting approval notices. |
| `GUBERN_DISCORD_APPROVAL_CHANNEL` | The channel id for notices. |
| `GUBERN_DISCORD_ALLOWED_USERS` | Comma-separated user ids allowed before pairing. |
| `GUBERN_DISCORD_API_URL` | Optional API base (a proxy or test double). It must be https unless it is on this machine. |

The interactions endpoint URL must reach Gubern from Discord. That is a
public path, so remote-access decision G applies (`remote-access.md`). Only
`/webhook/discord` needs to be reachable, never the admin API.

## 4. Tests

- **Unit** (`internal/messaging/discord_test.go`):
  - signature checks: missing, forged, stale, tampered body;
  - PING;
  - decisions follow identity: allow-listed, revoked, stranger, paired;
  - `/pair` from anyone, with an ephemeral reply;
  - malformed ids;
  - notices with buttons and bot authorization.
- **Real server** (`integration/discord_test.go`):
  - a real run's approval is announced with buttons;
  - a forged interaction gets 401, and a stranger is refused;
  - an allow-listed press approves, recorded `discord:111` via `discord`;
  - the dashboard then gets 409;
  - `/pair` links a stranger, and a second person's code conflicts;
  - a linked user's decision is recorded under the person;
  - revocation beats the allow-list.
