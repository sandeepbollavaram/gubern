# Plan: real backend for the new dashboard, finish Phase 1, QR pairing, mobile app

Status: 2026-11-07. The new dashboard shell (`dev/dashboard-v2`, `39ba68c`) runs
on a mock client. This plan makes it real, finishes the missing screens, and adds
**QR pairing** so a phone (Telegram, WhatsApp, Discord, or the Gubern mobile app)
can control the server: chat with agents, approve actions, browse files and
watch runs.

## 0. Where we are (verified in code, not assumed)

| Need | Today | Gap |
|---|---|---|
| Streamed agent replies | `POST /api/agents/submit` (SSE) | Text chunks only (`{"chunk": …}`); tool steps and approvals are plain text lines the TUI parses |
| Pending approvals | Runtime keeps a pending action per session, with an ID | No API lists them with tool, risk, command or expiry |
| Decide an approval | `POST /api/agents/approve {session_id, approved, action_id}` | Works for the async path. When Telegram is the approver, a dashboard decision can't reach the waiting call |
| Sessions | `GET /api/agents/history`, `/history/{id}` | No status (running, needs approval, failed), no title, no rename, pin or delete |
| Clarifying questions | Structured `ClarifyQuestion` exists, sent to Telegram only | No API to read or answer them |
| Team checkpoints, orchestration runs | `/api/checkpoints/*`, `/api/orchestration/*` | Usable as is |
| Keys, audit, AI cost, logs, status | Endpoints exist | Usable as is |
| Discord | — | No integration |
| Device pairing | — | Nothing; users paste IDs into env vars |
| Mobile app | — | Nothing |

## 1. Phase 1 gaps (screenshots re-checked)

Done: the shell, Home, conversation with approval card, tool groups, question
tray, run panel, `Ctrl+K`, Settings modal, Inbox, Outputs, Automations, light
and dark themes.

Still missing:

1. **Workspace page** (the project pattern): list grid and a detail page with
   a scoped composer, Instructions / Root / Policy / Memory / Automations rows,
   and recent sessions (spec F3d).
2. **Output viewer in the side panel**: open a report, config or diff next to
   the chat; Outputs counter in the title bar.
3. **Composer + menu**: attach file, add context (file or folder from the
   workspace), slash commands (`/` opens a command list).
4. **Live step detail**: command block and output with diff tinting while the
   step runs ("Working 31 s ▾").
5. **Usage**: context meter, cost so far, budget share from real data.
6. **Customize-style library** for Plugins, Channels and Providers (Yours /
   Discover tabs, status table with Connect).
7. **Route detail in the side panel**, replacing the old drawer.
8. **Focus mode** (hide the rail, full width), **help and status** popover.
9. **Mobile layout** under 768 px: bottom bar (Chat · Inbox · Operate ·
   Settings), full-screen approvals, cards instead of tables.
10. **Sign-in screen** for remote use (paste API key, or **scan to pair**).

## 2. Backend work (Phase 2a): give the UI a real contract

All new endpoints require an API key, are audit-logged when they change state,
and have Go tests. Existing endpoints keep their current behaviour.

1. **Structured agent events.** Add an event sink carried in the request
   context. `ExecuteLocalTool`, the approval path and the clarify path emit
   typed events next to the existing text lines.
   `POST /api/agents/submit` with `Accept: text/event-stream` and
   `?events=1` streams:
   - `text` (a chunk);
   - `tool_start` and `tool_end` (tool, label, status, duration, output
     excerpt);
   - `approval_required` (action ID, tool, risk, description, command or
     preview, workspace, expiry);
   - `approval_decided` (by, via);
   - `question` (structured options);
   - `status`;
   - `done`.

   Without `events=1` the old chunk stream is unchanged, so the TUI and
   scripts keep working.
2. **Pending approvals:** `GET /api/agents/pending` lists every pending
   action across sessions with the fields above.
3. **One approval, many channels.** An approval is registered once and can
   be decided from the dashboard, Telegram, Slack, WhatsApp, Discord or the
   app. The first decision wins; the others see "decided by X via Y".
   Expiry stays 10 minutes, then the action is rejected.
4. **Session metadata:** status, title, pinned, updated. Add
   `PATCH /api/agents/sessions/{id}` (rename, pin) and `DELETE`.
5. **Questions:** `GET /api/agents/sessions/{id}/questions` and
   `POST …/questions/{key}` to answer, shared with Telegram's buttons.
6. **Attention feed:** `GET /api/attention` merges pending approvals,
   unanswered questions, team checkpoints, failing routes and budget alerts,
   sorted by severity. The Inbox, the badge, Home and `Ctrl+K` all use it.
7. **Live updates:** `GET /api/events` (SSE) pushes "something changed"
   notices so screens refresh at once instead of polling.

## 3. Wire the dashboard (Phase 2b)

- An `httpClient` implementing the existing `GubernClient` interface (the
  screens don't change). The mock stays available with `?mock=1` for demos and
  UI tests.
- Auth: the stored API key is sent as `Authorization: Bearer`; a 401 opens the
  sign-in screen. The loopback auto-sign-in uses the local key the server
  provides to the CLI today.
- Every screen keeps the state catalogue from the spec (loading, empty,
  error, offline with reconnect, no permission).

## 4. QR pairing: connect a phone in one scan (Phase 3)

**What the user does.** Settings → *Devices and channels* → **Pair a device** →
choose what to connect (Gubern app, Telegram, WhatsApp, Discord) and a role
(operator or read-only) → a QR code appears → scan it with the phone → the
dashboard asks **"Pair Pixel 8 as operator?"** → confirm → done. The device
shows up in a list with *last seen* and **Revoke**.

**How each channel works:**

| Channel | The QR contains | What happens on the phone | What the server does |
|---|---|---|---|
| Gubern app | `gubern://pair?u=<server URL>&c=<one-time code>&f=<TLS fingerprint>` | The app opens, shows the server name and fingerprint, asks to pair | Exchanges the code for a **device-scoped API key** with the chosen role, after the on-screen confirm |
| Telegram | `https://t.me/<bot>?start=<code>` | Telegram opens the bot and sends `/start <code>` | Registers that Telegram user ID (and DM chat) in the allow-list after confirm. Replaces copying IDs into `GUBERN_TELEGRAM_ALLOWED_IDS` |
| WhatsApp | `https://wa.me/<number>?text=PAIR%20<code>` | WhatsApp opens a chat with the message pre-filled; the user taps Send | Registers the sender's number after confirm |
| Discord | Bot invite or DM link with `<code>` | The user DMs the bot `/pair <code>` | Registers the Discord user ID after confirm (**new Discord channel**, built like the Slack and Telegram bots: signed interactions, per-user sessions, allow-list) |

**Security rules:**

- Codes are random (128-bit), **single-use**, and expire after **5 minutes**.
  They're bound to the role chosen on screen, rate-limited, and audit-logged.
- **Two-sided confirm.** A scanned code only *requests* pairing; the person at
  the dashboard approves it, showing the device name and channel identity. A
  photographed QR is useless without that click.
- The QR never contains a long-lived secret. The app receives its key over
  TLS after the confirm; the fingerprint in the QR pins the server's
  certificate, so a fake server on the same Wi-Fi can't pose as it.
- Device keys are ordinary API keys: listed, revocable, and revocations
  survive restarts (audit fix F-03).
- Channel registrations land in the same allow-lists the Slack, Telegram and
  WhatsApp fixes introduced. Unregistered senders still get only `whoami`.

**Reachability.** On the same network, the QR carries the LAN address. For
use away from home, the server needs an address the phone can reach (the
Gubern edge with TLS on a public host, or a tunnel). The pairing screen says
which address the QR contains and warns when it is a private address.

## 5. Gubern mobile app (Phase 4)

**Stack: React Native with Expo** (one TypeScript codebase for Android and
iOS). It reuses the dashboard's `GubernClient` types and data client, so the
phone and the web always agree on the API.

**MVP screens:**

1. **Pair:** camera QR scan, server name and fingerprint, waiting for the
   desktop confirm.
2. **Inbox:** approvals with full detail and big Approve / Reject buttons
   (high risk needs a hold-to-approve); questions; failing routes.
3. **Chat:** sessions, streaming replies, tool steps, the question tray,
   stop, queue.
4. **Files:** browse and read files inside the workspace root through the
   agent's read-only tools. Edits go through the agent, so they hit the same
   approval gate as everywhere else.
5. **Runs and status:** progress timelines, health, spend.
6. **Settings:** server, device name, notifications, sign out (revokes the
   device key).

**Push notifications:** when an approval is needed, a run finishes, or a
route fails. The notification deep-links to that item.

**"Control the whole PC" stays inside the safety model.** The phone gets
exactly what an API key of its role gets. Shell commands, writes outside the
workspace, git and config changes always need an approval, and the phone can
grant one only if its role allows it. There is no raw remote shell.

## 6. How we keep it free of errors

Every step lands with:

- **Go:** unit tests for each new endpoint, including auth, role, expiry,
  single-use and replay cases, plus `go vet`, `golangci-lint v2.15` and
  `go test -race` for touched packages.
- **Dashboard:** `tsc -b`, `eslint`, `vite build`, and a browser walkthrough
  of the changed flows in both themes. Later, Playwright end-to-end tests for
  flows F1–F9 run against a real `gubern start` with a fake AI provider.
- **Mobile:** type check, lint, and an Expo build that runs on a device.
- **CI green** before anything is pushed. Pushing `dev/dashboard-v2` and any
  merge need your go-ahead.

## 7. Order of work

| # | Step | Depends on |
|---|---|---|
| 1 | Structured agent events + pending approvals endpoint | — |
| 2 | Shared approval (first decision wins, any channel) | 1 |
| 3 | Session metadata, questions API, attention feed, live updates | 1 |
| 4 | Dashboard `httpClient` + sign-in; mock behind `?mock=1` | 1–3 |
| 5 | Phase 1 gaps 1–8 against real data | 4 |
| 6 | Pairing core: codes, confirm, device keys, Devices screen | 4 |
| 7 | Telegram and WhatsApp pairing | 6 |
| 8 | Discord channel + pairing | 6 |
| 9 | Mobile app MVP (Expo) | 6 |
| 10 | Mobile layout for the web dashboard, Playwright E2E | 5 |

Steps 1–4 make the dashboard real; that comes first because nothing else
matters while the screens show simulated data.
