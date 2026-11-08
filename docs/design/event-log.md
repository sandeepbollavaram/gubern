# Run event log and resumable streams (Milestone D)

Status: **implemented** on `dev/dashboard-v2`. Depends on A (scopes) and B
(approvals).

## 1. What changed

A structured stream (`POST /api/agents/submit?events=1` with
`Accept: text/event-stream`) used to be a live pipe: a dropped connection
lost the run's events, and on an `/agent` run it stopped the run. With D,
each such submit is a **run**:

- its events are **redacted, then persisted** with a per-run sequence
  number;
- they are streamed with stable SSE ids, and a client resumes with
  `Last-Event-ID`.

The classic chunk stream (no `events=1`) and the JSON reply are unchanged,
and are not recorded.

## 2. Model

`internal/eventlog` writes to `events.db`. The file sits beside the other
stores, is created 0600, and uses versioned migrations.

- `runs`: `id` (`r-` + 16 hex), session, principal, status, start and end
  times, `last_seq`, idempotency key. Status follows the Phase 3 state machine:
  - **Live:** `running`, then `waiting_approval` while an approval is open,
    and `verifying` while the completion check runs.
  - **Final:** one of these:
    - `completed`: its check passed;
    - `partial`: the check still failed, or the run hit its budget;
    - `done`: it finished with no check, so nothing was verified;
    - `failed`, `cancelled`, or `interrupted`.
  - **How the final status is set:** the run reports it with a
    `run_outcome` event. A cancellation overrides it. Each check attempt is a
    `check_result` event, and live transitions are `run_status` events.
- `events`: the run, `seq` (unique per run, starting at 1, gap-free), type,
  time, and the event JSON after redaction.

Event types are the B and A ones (`text`, `tool_start`, `tool_end`,
`approval_required`, `approval_decided`, `status`), plus `run_started` and
`run_finished`. Every event carries `run_id` and `seq`.

## 3. Redaction before persistence

Redaction happens in `Append`, before the insert and before fan-out, so
neither the store, its WAL, nor any subscriber ever holds the raw value. It
covers every free-text field of an event: text, output, label, and the
approval's description, command and preview.

| Pattern | Examples |
|---|---|
| PEM private key blocks | `-----BEGIN ... PRIVATE KEY-----` |
| Gubern API keys | `<16hex>.<64hex>` |
| Provider keys with a known prefix | Anthropic, OpenAI, GitHub, Slack, AWS access key ids, Google API keys, GitLab, Telegram bot tokens |
| Tokens | JWTs; `Authorization:` and `X-API-Key:` header values |
| URL credentials | `scheme://user:password@host` |
| Secret-named assignments | `password=`, `token:`, `api_key=`, `client_secret=` and similar |

Ordinary text, including git hashes, is left alone. This is defence in
depth, not a guarantee: a secret in an unrecognised shape can still pass.

## 4. Streams

- **Frames** are `id: <seq>`, `event: <type>`, `data: <json>`. The first
  line is `retry: 3000`, and the stream ends with
  `event: done` / `data: {"run_id","status","last_seq"}`.
- **Resume:** `GET /api/runs/{id}/events` with `Last-Event-ID: <seq>` (or
  `?after=`) replays the stored events after it, then follows live. The
  replay and the live registration happen under one lock, so they can't
  overlap or leave a gap. The server also never sends `seq <= Last-Event-ID`.
- **Heartbeats:** an idle stream gets a `: ping` comment every 15 s
  (`GUBERN_SSE_HEARTBEAT`).
- **Slow clients:** a subscriber more than 256 events behind is cut off,
  and the server re-subscribes it from the store at its last sent event. The
  agent never blocks on a client.
- **Duplicates:** a submit with an `Idempotency-Key` that the same principal
  used within the last 10 minutes attaches to that run and replays it. It
  starts no second run. A key reused for another session is refused (409).
- **The response header `X-Gubern-Run-Id`** names the run.

## 5. Lifetime and cancellation

- **Detached from the request:** a run survives its client disconnecting,
  so the client can resume.
- **Detach grace:** with nobody watching for `GUBERN_RUN_DETACH_GRACE`
  (default 2 min), the run is cancelled, so an abandoned `/agent` run does
  not spend tokens unobserved.
- **Explicit cancel:** `POST /api/runs/{id}/cancel` cancels the run's
  context. An `/agent` run stops at its next step, and its open approval is
  cancelled. A plain chat answer finishes its current generation, and the
  run is still recorded as `cancelled`.
- **Restart:** runs still live (`running`, `waiting_approval`,
  `verifying`) are marked `interrupted` on the next start.
- **Resume (Phase 3):** an `/agent` run saves a checkpoint after every step
  in `checkpoints`. The checkpoint holds the conversation, the counters, and
  which calls of the current turn began executing; each is written before the
  thing it records happens. `POST /api/runs/{id}/resume` (`agents:run`, same
  session access as cancel) continues an `interrupted` run as a new run in the
  same session:
  - Results already recorded are reused, never re-run.
  - A call that never began (say, one waiting for an approval) is asked for
    again.
  - A read-only call that was in flight runs again.
  - A side-effecting call that had begun is not repeated. The model is told
    its outcome is unknown, and the audit log records
    `agent.action.unknown_outcome`.
  - A run can be resumed once (`409 already resumed`). The resume is audited
    as `run.resumed`.
- **Retention:** runs that ended more than `GUBERN_EVENT_RETENTION` ago
  (default 7 days) are deleted with their events.

## 6. API

| Route | Scope | Notes |
|---|---|---|
| `GET /api/runs?session_id=&limit=` | `sessions:read` | Runs of sessions the caller may read. |
| `GET /api/runs/{id}` | `sessions:read` | |
| `GET /api/runs/{id}/events` | `sessions:read` | SSE (resume), or JSON `{run, events}`. |
| `POST /api/runs/{id}/cancel` | `agents:run` | 202, or 409 if the run is not running. |
| `GET /api/activity?session_id=&limit=` | `sessions:read` | The activity timeline: every event except streamed text, newest first, filtered by session. |

## 7. Not in D

- Classic chunk streams are not recorded (kept unchanged on purpose).
- Streams cover runs started with `events=1`. Runs started from Telegram,
  WhatsApp or the TUI don't appear in the log; their approvals still do,
  through B.
