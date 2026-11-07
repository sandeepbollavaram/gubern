# Gubern dashboard: UX specification

This replaces the current `/dashboard/`. The visual patterns come from
[dashboard-ux-reference.md](dashboard-ux-reference.md). This document defines
**how the product behaves**: who uses it, what they must get done, the flows,
the states, and the rules every screen follows.

## 1. Who uses it and what they need to get done

| Person | Context | Main need |
|---|---|---|
| **Operator** (owner, often solo) | Desk, runs Gubern on a VPS or laptop | Know everything is healthy; change config safely |
| **On-call responder** | Phone, away from desk, notified by Telegram or Slack | Decide on a pending agent action in seconds, safely |
| **Builder** | Desk, working with the coding agent | Ask, watch the agent work, review its changes |
| **Viewer** (read-only key) | Anywhere | See status and history; cannot change anything |

**What they need to get done,** most frequent and urgent first:

1. **"Is everything OK?"** answered within 3 seconds of opening the app.
2. **Approve or reject** a pending agent action safely, from any device.
3. **Ask the agent** to do something, watch it, and stop or steer it.
4. **Diagnose** a failing route, node or run, and fix it.
5. **Change configuration** (routes, providers, budget, channels, approval
   policy) without breaking production.
6. **Audit** who did what and prove the log wasn't tampered with.
7. **Manage access**: API keys, registered Slack, Telegram and WhatsApp
   users.

## 2. UX principles (each screen is reviewed against these)

1. **Attention first.** Anything waiting on a human (approvals, team
   checkpoints, failing routes, budget alerts) appears in one **Inbox**. It is
   also shown as a rail badge, at the top of `Ctrl+K`, and as an optional
   browser notification. Nothing urgent hides on a sub-page.
2. **Show what will happen before it happens.** Every change states its
   effect (a diff or preview), its risk level and where it applies before the
   user commits. Afterwards it says what happened and offers **Undo** where
   undo is possible.
3. **Two ways to do anything.** Ask in chat, or click. Agent answers link to
   the real screen ("Open route `api` ›"). Screens offer **Ask about this**,
   which pre-fills the composer with context.
4. **Progressive disclosure.** Summary → detail panel → raw (logs, JSON,
   CUE). Each layer is one click deeper, never forced on the user.
5. **Live, but calm.** Data updates over SSE without layout jumps. A changed
   value highlights briefly. Every live view says how fresh it is ("updated
   4 s ago").
6. **Never lose work.** Composer drafts survive reloads. A dropped stream
   reconnects and resumes. Long runs continue server-side and can be
   reopened.
7. **Role-aware.** A viewer sees disabled controls, each with a reason ("Needs
   operator role"), not missing buttons. Admin-only areas say so.
8. **Fast for experts, obvious for newcomers.** Every action has a visible
   control; frequent ones also have shortcuts. Shortcuts are listed in
   `Ctrl+/` and shown in menus.

## 3. Information architecture

```
Rail
├─ [Gubern mark]  ⟨collapse⟩                       Ctrl+B
├─ + New chat                                       Ctrl+Shift+O
├─ Search                                           Ctrl+K
├─ Inbox  (badge: count needing a decision)         G I
├─ Workspaces        (namespaces / trusted roots)   G W
├─ Outputs           (research reports, builds, generated configs)
├─ Automations       (scheduled agent tasks, orchestration runs)
├─ Operate ▾         Overview · Routes · Traffic · Nodes · Healing      G O
├─ Build ▾           Plugins · Forge · Studio
├─ Recents           chats + agent runs + team runs, with status icons
│                    (Pinned / Today / Yesterday / Earlier)
└─ [Account row]     key name · role ▾ → Settings · Usage & cost ·
                     Shortcuts · Docs · System status · Sign out

Main column          conversation, or the selected page
Side panel           details: run panel, route detail, diff, logs, report
                     (resizable; Ctrl+. toggles it)

Settings (modal, searchable)
  Account & access   API keys, current session, sign out everywhere
  General            Appearance (theme, density, text size, width, motion)
  Agent              approval policy (Manual / Auto in trusted root),
                     workspace root, default activity view
                     (Normal / Thinking / Verbose)
  Models & providers providers, keys status, default model, routing
  Channels           Telegram · Slack · WhatsApp: status, registered users
                     (operator / read-only), test message
  Notifications      per event × channel (browser, Telegram, Slack, WhatsApp)
  Budget & usage     daily and monthly limits, spend by provider and model
  Security           secrets status, TLS and certificates, audit log + verify
  Advanced           reload config, export config, danger zone (shutdown)
```

**Where each current page goes:**

| Current page | New place |
|---|---|
| Overview | Operate → Overview |
| Routes, Route Builder | Operate → Routes; the builder opens in the side panel |
| Traffic, Metrics | Operate → Traffic (metrics as route and node detail tabs) |
| Nodes, Healing | Operate → Nodes, Operate → Healing |
| Agents | Recents + run panel; Inbox for approvals |
| Plugins | Build → Plugins |
| Secrets, Security | Settings → Security |
| Audit Log | Settings → Security → Audit (also in `Ctrl+K`) |
| Namespaces | Workspaces |
| Settings | Settings modal |

## 4. Key flows

### F1: Sign-in and first run

1. **Opened on the server itself** (loopback): the app signs in automatically
   with the local key and shows *Signed in as `admin` (local)*.
2. **Opened remotely:** a single field, "Paste an API key". The key is checked
   and the role shown before entering. Errors appear inline ("Key revoked",
   "Key expired").
3. **First run** shows a **setup checklist** on Home (dismissable, resumable):
   connect an AI provider · set a monthly budget · register yourself on
   Telegram or Slack for approvals · add your first route. Each item opens
   the exact settings row.

### F2: Home ("is everything OK?")

```
            [mark]  Good evening, admin
  ● All systems healthy · 12 routes · 3 nodes · $1.20 today of $10
  ┌──────────────────────────────────────────────────────────────┐
  │ Ask Gubern, or type / for commands                           │
  │ +                                               mic   ⏎      │
  └──────────────────────────────────────────────────────────────┘
   Workspace ▾                      Model ▾ · Effort    Approval ▾
  NEEDS YOU (2)                                         View inbox
   [!] run_terminal in ~/app — HIGH RISK — 8 min left  Review
   [!] Route `api`: 3 of 4 backends failing             Open
  RECENT OUTPUTS                                        View all
   [card] [card] [card]
```

- The status line is one sentence, colour-coded with an icon. It shows the
  **worst** current state first ("2 routes failing").
- **Needs you** appears only when something does; it's never an empty box.

### F3: Ask the agent and watch it

1. **Send.** The message appears, and the reply **streams as it arrives**.
   Send becomes **Stop** while the agent is running.
2. **Tool steps** show as one-line rows with an icon, a verb and the target
   ("Read `internal/api/keys.go`", "Ran `go test ./...` · 14 s · passed").
   They collapse by default in Normal view and expand on click. The view mode
   (Normal / Thinking / Verbose) is switchable per session from the title
   menu.
3. **Steering.** While a run is active, the composer reads "Enter to send,
   Ctrl+Enter to queue", and a **Stop** button sits inside the field.
   - **Enter** interrupts at the next safe point and sends.
   - **Ctrl+Enter** queues the message, shown as "Will send after this
     step". It can be edited or removed before it goes.
3b. **Live step.** The running step shows "••• Running · 31s ▾" with an
   elapsed timer. Expanded, it shows the **exact command** (mono, copy
   button) and a scrolling output box. Diff output is tinted, and stdout
   and stderr are labelled.
4. **On finish:** each answer has a row of actions (copy · retry ·
   👍 👎 · *Open in side panel*). Generated outputs appear as a card that
   opens in the side panel.
5. **On failure:** a red step with the error summary, **Retry step**, and
   *Show details* (raw output).
5b. **Grouping.** Consecutive steps using the same tool collapse into one
   row: "Ran 6 terminal commands, **1 failed** ›". The failure count shows in
   red, with thumbnails or snippets on the right. A floating **↓ Latest**
   button appears when the user has scrolled up while output streams.
5c. **Long, multi-step runs** (orchestration, team): a **"Step 4 of 13"**
   pill in the title bar opens the run panel. The panel has a **Progress**
   timeline (earlier steps collapsed, current step marked, upcoming steps
   listed, failed steps flagged), an **Outputs** gallery, and **Used in this
   run**: workspace, tools, providers and models, memory, plugins. The plan
   steps come from `/api/orchestration/runs/{id}/tasks`.
6. **Reload or reconnect:** the transcript restores, and a still-running
   agent resumes streaming. A banner reads "Reconnected, caught up".

### F3b: The agent asks clarifying questions

Gubern's coordinator already asks structured clarifying questions (today they
go to Telegram as button rows). In the dashboard:

1. The transcript shows a status pill, **"Asking a question"** (amber), under
   the agent's lead-in sentence.
2. A **question tray** replaces the area above the composer:
   - the question as its title, with a pager **‹ 1 of 3 ›** and a close (×)
     button;
   - numbered options, each with a one-line hint; keys `1`–`9` pick one;
   - a **"Something else"** free-text row, and **Skip**.
3. The composer stays usable underneath ("Or reply directly…"). Typing there
   answers the current question in free text.
4. When the last question is answered, the tray closes and the answers show
   in the transcript as a compact summary ("Workspace: ~/app · Scope: tests
   only"), each editable until the agent starts.
5. **Questions answered elsewhere** (Telegram) update the tray live. Closing
   the tray keeps the questions in the Inbox until they're answered or the
   run is cancelled.

### F3c: Model, effort and usage controls

- **Model menu** (composer chip "Sonnet 5.5 · Medium ▾"):
  - every configured model from every provider, each with a one-line
    purpose and its provider;
  - keys **1–9** pick a model;
  - a cost hint for expensive models, and "Local · no cost" for Ollama;
  - **Effort ›**: Low, Medium (*Recommended*), High, Max (⚠ "uses about
    3× more"), each with one line of explanation;
  - **More models ›** opens Settings → Models & providers.
- **Run panel → Usage** (the counterpart of Claude's context meter): tokens
  used / context window for the session as a segmented bar (messages,
  system prompt, tools, memory), plus **cost so far** and the share of
  today's budget. It turns amber at 80 % of the context window or budget.

### F3d: Workspace page (project pattern)

`Workspaces` lists namespaces and trusted roots as a two-column grid (icon,
name, last activity, pin), with search, sort and **New workspace**. A
workspace page has:

- a breadcrumb, "Workspaces / name ▾", a title, and a composer scoped to the
  workspace ("New session in ~/projects/app");
- under the composer: where it runs (node), the model and the approval
  policy;
- the workspace's recent sessions with dates, or an empty state explaining
  that agents in this workspace share its instructions and memory;
- a right column of rows, each with one action:
  - **Instructions** (`AGENTS.md`): Edit
  - **Root folder** (the trusted root, with its containment note): Change
  - **Approval policy** (Manual / Auto in root): Change
  - **Memory** (episodic memory for this workspace): View
  - **Automations**: Add
  - **Routes** (routes in this namespace): View

### F4: Approve or reject (the most important flow)

An approval is shown in **three places at once**, kept in sync live: inline
in the transcript, in the **Inbox**, and as a notification (browser and/or
Telegram / Slack).

```
┌ Approval needed ───────────────────────────── HIGH RISK ─┐
│ run_terminal  in  ~/projects/app                          │
│ $ npm install left-pad && npm run build                   │
│ Requested by: Chat "Fix build" · Telegram chat 4242       │
│ Expires in 8:12 · rejected automatically when it expires  │
│ [ Reject ]   [ Approve ]        Show full preview ›       │
└───────────────────────────────────────────────────────────┘
```

- **Content:** tool, risk level (colour, icon *and* word), the exact command
  or a diff for file edits, the workspace path, who or what asked, and a
  countdown to the 10-minute timeout. A request with no detail says so and
  recommends rejecting.
- **Button weight:** for LOW and MEDIUM risk, *Approve* is the primary
  button. For HIGH and CRITICAL, *Reject* is visually equal to *Approve*, and
  *Approve* requires a second click ("Click again to approve", 3 s window).
  This guards against reflex clicks.
- **Keyboard:** `A` approve and `R` reject when the card has focus. The
  Inbox supports `J`/`K` to move between items.
- **After deciding:** the card collapses to "Approved by you · 12:03 ›
  audit", and the agent continues.
- **Decided elsewhere** (for example on Telegram): the card updates live to
  "Approved in Telegram by @sandy".
- **Expired:** "Expired, rejected automatically" with *Ask again*.
- **Phone:** the notification deep-links to a full-screen approval view with
  thumb-sized buttons, sticky at the bottom.

### F5: Team run and checkpoints

- The **side panel becomes the run panel**:
  - an agent roster with live status (idle · working · waiting · done ·
    failed);
  - a checkpoint stepper;
  - changed files;
  - the messages between agents (collapsed).
- **A checkpoint waiting for review** shows the step's diff and a summary.
  The choices are **Approve**, **Edit** (inline editor, then approve the
  edited version) and **Reject with note**. All three are mirrored in the
  Inbox.

### F6: Diagnose a failing route

1. Starts from a Home attention card, a Recents status icon, or Operate →
   Routes (failing rows sort first).
2. The **route detail** opens in the side panel. Its header shows status,
   p50/p95 latency and error rate with sparklines. Tabs: Backends, Recent
   errors, Config (read-only CUE), Logs.
3. **Actions:** Drain or undrain a backend (confirm), Edit route (F7),
   *Ask Gubern about this route* (composer pre-filled with route name and
   recent errors).

### F7: Change configuration safely (routes, providers, budget, channels, policy)

1. **Edit** in a form with inline validation, which explains *why* a value is
   invalid. The live CUE preview is togglable.
2. **Review** shows a **diff against the running config** and the effects in
   plain words ("Route `api` will start requiring client certificates").
3. **Apply.** Hot reload with a progress state, then a toast "Applied ·
   **Undo**" for 30 s. Undo restores the previous version.
4. **Failure** names the field, and nothing is half-applied.
5. Every apply is written to the audit log with who and what.

### F8: Access management

- **API keys** appear as a table: name, role, created, last used, a *Current*
  badge, Revoke.
  - **Issue key** shows the secret **once**, with Copy and a warning.
  - **Revoke** confirms by name. Revoking the *current* key requires typing
    its name, because it signs you out.
- **Channel users** (Slack, Telegram, WhatsApp):
  - registered users with their role (operator / read-only);
  - **Add user** (paste the ID shown by `/vortex whoami`);
  - recent refused attempts ("U_RANDOM tried `deploy` 2 h ago, Register?").

### F9: Audit

- A filterable timeline (actor, action, resource, time) with a detail panel.
- **Verify chain** runs the HMAC check and shows a result banner (✓ intact,
  *n* entries; or ✗ broken at entry *k*, with details).

## 5. State catalogue (every data view implements all of these)

| State | Behaviour |
|---|---|
| Loading (> 300 ms) | Skeleton in the final layout's shape; no spinner-only screens; nothing jumps when data arrives |
| Empty | One sentence on what belongs here, a primary action, and **templates** where they make sense (Automations: "Nightly health report", "Cert expiry check", "Dependency audit") |
| Error | Inline near the cause, `role="alert"`, plain message plus **Retry**, and *Details* (status code, request ID) |
| Offline or reconnecting | A thin top banner, "Reconnecting… (attempt 3)"; data marked stale; actions that need the server disabled, with the reason |
| No permission | The control is visible but disabled, with a tooltip saying which role it needs |
| Stale | "Updated 2 min ago", with a manual refresh |
| Partial | Per-section errors; one failing widget never blanks the page |

## 6. Interaction rules

- **Confirmation levels:**
  - none, for safe or reversible actions (with an undo toast);
  - a confirm dialog naming the exact object, for destructive actions;
  - typed confirmation, for actions that cut off access or stop the server
    (revoke current key, shutdown).
- **Feedback:** every mutation gets a toast (success, with undo where
  possible) or an inline error. Buttons show a busy state and are disabled
  while their request is in flight.
- **Keyboard:**

  | Keys | Action |
  |---|---|
  | `Ctrl+K` | search |
  | `Ctrl+/` | shortcuts sheet |
  | `Ctrl+Shift+O` | new chat |
  | `Ctrl+B` | rail |
  | `Ctrl+.` | side panel |
  | `Esc` | close the top layer |
  | `G` then `I` / `O` / `W` | go to Inbox / Operate / Workspaces |
  | `J` / `K` | move in lists |
  | `A` / `R` | approve / reject on a focused approval |
  | `Enter` / `Shift+Enter` | send / newline |

  Visible focus rings everywhere; tab order follows the visual order; a
  skip-to-content link.
- **Accessibility:** text contrast ≥ 4.5:1 in both themes; status never
  conveyed by colour alone (icon and word too); icon buttons carry
  `aria-label`; live regions announce new approvals and run completion;
  `prefers-reduced-motion` respected.
- **Motion:** 150–200 ms colour and opacity transitions only; no layout
  animation; streaming text without a typewriter delay.
- **Responsive:**

  | Width | Layout |
  |---|---|
  | ≥ 1280 px | rail + main + side panel |
  | 768–1279 px | rail collapses to icons; side panel becomes an overlay |
  | < 768 px | single column with a bottom bar (Chat · Inbox · Operate · Settings); approvals full-screen; tables become cards |

  Touch targets ≥ 44 px.
- **Focus mode:** collapse everything except the conversation (`Ctrl+B`, plus
  full screen).
- **Writing:** plain words, numbers with units ("142 ms", "$1.20"), relative
  times with the exact time on hover, sentence case, no jargon in status
  lines.
- **Privacy:** fonts and assets are bundled; the dashboard makes no
  third-party requests. Secrets are never displayed after creation.

## 7. What "good" looks like (acceptance for each phase)

- The health question is answerable from Home without scrolling at 1280×720.
- From a notification, a decision on an approval takes **≤ 10 s** and
  **≤ 2 taps** on a phone.
- Every destructive action shows what it affects and is either confirmed or
  undoable.
- Each flow F1–F9 works with the keyboard alone.
- Lighthouse accessibility score ≥ 95; no horizontal scroll at 375 px.

## 8. Build plan

The prototype is built **inside `web/dashboard`** as the new app shell, with
mocked data behind the real API types. What you click through in review then
becomes the product, with nothing thrown away.

| Phase | Output |
|---|---|
| 1 | App shell, design tokens (brand colours, light and dark), rail, `Ctrl+K`, Settings modal, and Home, Conversation, Approval, Inbox, Route detail and Settings rows, on mock data, for review |
| 2 | Wire Phase 1 to the live API (auth, SSE, approvals, runs, keys, audit) |
| 3 | Move every remaining current page into the new IA; remove the old pages |
| 4 | New admin write APIs (routes, providers, budget, channels, policy, notifications), each audit-logged and admin-only |
| 5 | Mobile layout, accessibility pass, end-to-end tests of F1–F9 |
