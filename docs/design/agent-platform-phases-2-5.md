# Plan: agent-platform Phases 2–5, in the server, dashboard and app

Status: proposed, 2026-11-08. Branch `dev/dashboard-v2`, local only.

This plan carries the agent-platform roadmap
(`.vortex-ops/gubern-agent-platform-research/13_PHASED_IMPLEMENTATION_ROADMAP.md`)
through Phases 2–5. Each phase ships three things together: the server
capability, the dashboard surface and the mobile surface. A phase is done
only when its gate passes. "Exists" or "compiles" is not enough.

| Phase | What it gives people | Server | Dashboard | App |
|---|---|---|---|---|
| 2 Skills | Agents use reviewed, pinned know-how | Skills API, pinned install, sidecar, events, evals | Skills library, install review, usage | Skills list, approve installs, skill chips in chat |
| 3 Durable runs | Runs survive restarts, end verified or explicitly partial | Run state machine, resume, completion checks, team roles as profiles | Runs page, run timeline, resume, verification | Run detail, resume or stop, verification in chat |
| 4 Safe workspace | Agents edit inside a fenced workspace you can roll back | Trusted root, git checkpoints, sandbox adapters, network policy | Workspaces, diffs, checkpoint restore, sandbox status | Changed files and diffs, restore with step-up |
| 5 MCP, then browser | Agents use outside tools under the same rules | MCP client, connector consent, default-deny tools, fetch ladder | Connectors, tool risk review, browser steps | Connectors list, approve connector tools, browser steps |

## 0. Where things stand

Repository-verified on 2026-11-08:

- **Phase 1 (control loop).** Done on this branch:
  - ToolSpec, native tools on every built-in provider, and the text fallback.
  - The loop with budgets.
  - Action-hash approvals.
  - `gubern ai probe`.
- **Missing from this branch.** Ten commits are only on `dev/gubern-policy` (`git cherry HEAD dev/gubern-policy`):
  - per-tool `agents.policy` and audited denials;
  - `/agent --read-only` and the 10-minute run deadline;
  - `gubern runs`;
  - policy for sub-agents and team specialists;
  - two reference skills.
- **Phase 2 (skills).** Partly done:
  - The `internal/skills` loader handles the spec fields, discovery scopes, progressive disclosure and hash pinning.
  - The catalog is fenced as data, skill use is audited, and `gubern skills list` exists.
  - No API, install, sidecar or evals yet.
- **Phase 3 (durable runs).** Partly done:
  - The milestone D event log gives persisted, resumable run streams.
  - Milestone B gives durable approvals.
  - `EffectLedger` (`internal/agents/effect_ledger.go`) fences side effects on resume.
  - `/agent --check` is the first completion contract.
  - There is no run state machine, no resume after restart, and team mode still bypasses the loop (backlog T32).
- **Phase 4 (sandbox) and Phase 5 (MCP, browser).** Not started. There is no MCP code and no MCP dependency in `go.mod`.
- **Dashboard and app (milestones E and H).** Both cover chat, approvals, attention, activity, runs, status, cost, health and devices. Neither has skills, connectors, workspaces or run-detail views yet.

## 1. Step 0: integrate the policy branch (milestone J)

Cherry-pick the ten missing commits from `dev/gubern-policy` in order:
`f12de7d 5d5bfa4 6a2256f 20609ee 5e1f3bb 1c3b391 4d61db1 9ff23a7 dd0186d d9e6f90`.

- **Conflicts.** Expect some in `internal/agents` and `internal/api`, because this branch added the event log, approvals service and identity since then. Resolve them in favour of this branch's structure, keeping the policy behaviour.
- **Gate.**
  - `go build`, `go vet` and the agents/api/approvals tests pass, with Linux race tests in CI or WSL.
  - The binary-level checks from the 96-hour log are repeated:
    - a misspelt tool name refuses to start;
    - denying `run_terminal` disables `/run`;
    - `ask read_file` raises an approval.
- **Why first.** Every later phase routes new effectors (skills, MCP tools, resumed runs) through this one policy decision point.

## 2. Phase 2: Skills (milestone K)

### Server

1. **Skills API.**
   - Endpoints:
     - `GET /api/skills` returns name, description, scope (repo, user or system), path, content hash, requested `allowed-tools`, enabled, and last used.
     - `GET /api/skills/{name}` adds the SKILL.md body, read-only and served as data.
     - `POST /api/skills/{name}/enable` and `/disable`.
   - New scopes:
     - `skills:read`, granted with `agents:read`;
     - `skills:manage`, admin only.
   - Every write is audit-logged.
2. **Pinned install.**
   - `gubern skills install <dir | git-url@commit>` and `POST /api/skills/install`.
   - The process is fetch, show, approve, then record:
     - fetch into a quarantine directory;
     - compute the tree hash;
     - show the SKILL.md, the requested tools and the file list;
     - require an approval (risk HIGH, so it can be approved from the phone with step-up);
     - record the name, source and hash in `skills.lock`.
   - At load, a skill whose files no longer match its pinned hash is disabled and flagged; it is never silently used.
3. **Sidecar.**
   - `agents/<product>.yaml` beside SKILL.md, for Gubern-only fields: risk hints, eval paths and a display name.
   - The file name waits on decision D2.
4. **Events.**
   - A `skill_activated` event (`skill`, `hash`, `requested_tools`) in the run event log.
   - The chat, timeline and activity views show which know-how shaped a run.
5. **Evals and the injection gate.**
   - **Deterministic test (no model needed).** A skill body containing policy-changing text cannot change any policy decision or approval outcome.
   - **Eval harness.** `gubern skills eval <name>` runs the skill's tasks against the configured models, with and without the skill, and reports pass rates.
   - **Real-model gain.** Measuring the gain on two model families needs real models (decisions D3 and D4).

### Dashboard

- Library gets a **Skills** tab:
  - **List.** Scope, enabled state, last used, and a flag on any skill whose files don't match its pinned hash.
  - **Detail.** The rendered SKILL.md, requested tools against what policy actually allows, a usage history taken from the activity data, and enable/disable.
- **Install review.** Paste a path or git URL, see the file list, hash and requested tools, then submit; the install itself waits for approval.
- **Chat.** A small "Skill: name" chip in the tool-step group when `skill_activated` arrives.

### App

- **Operate.** A read-only Skills list (name, scope, enabled, hash).
- **Chat.** Skill chips, the same as the dashboard.
- **Install approvals.** These arrive as normal approvals with the skill's file list in the preview, so they can be approved by slide plus step-up.

### Gate

- The injection test passes.
- An install round trip works end to end (quarantine, review, approval, lock, load), and a tampered file is detected.
- The dashboard and the app both show skill activation in a live run.
- The real-model gain report is attached once D3 and D4 are decided. Until then it is marked "not measured", not passed.

## 3. Phase 3: Durable runs and verification (milestone L)

### Server

1. **Run state machine.**
   - Statuses: `queued`, `running`, `waiting_approval`, `verifying`, `completed`, `partial`, `failed`, `cancelled` and `interrupted`.
   - Each status is stored on the run record in the event-log store, and each transition is an event.
   - `completed` is reachable only through verification. A run whose checks failed or were skipped ends `partial`, with the reasons recorded.
2. **Checkpoint and resume.**
   - **Checkpointing.** The loop saves its transcript, step index and budget usage after every step.
   - **On boot.** Runs left `running` become `interrupted`.
   - **Resume.** `POST /api/runs/{id}/resume` (`agents:run`) continues from the last checkpoint under the run's effect scope. The `EffectLedger` replays already-done side effects instead of repeating them.
   - **Approvals.** A run that was waiting on an approval picks up the decision after the restart.
3. **Completion contracts.**
   - `submit` accepts `checks` (commands, file assertions, a test command).
   - `/agent --check` becomes one case of this.
   - Verification emits `verification_started`, `check_passed`, `check_failed` and `verification_done`.
   - Checks run through the same approval path as any other command.
4. **Team mode becomes role profiles** (decision D1).
   - Coder, tester and reviewer become loop profiles (system prompt, tool subset and budget) run by the one loop.
   - This removes the separate team path that bypasses approvals and fixes the broken tester (T32).
   - Orchestration DAG nodes become loop runs too.
5. **Kill-9 gate harness.**
   - An integration test using the fake model.
   - It starts 20 runs with side effects, kills the server at random steps, restarts it and resumes.
   - It asserts zero duplicate side effects and that every run ends `completed` or `partial`.

### Dashboard

- A **Runs** page:
  - A filterable list (status, session, age, duration).
  - **Run detail.** The full event timeline, tool steps with output, approvals with who decided and how, verification results, and links to the chat.
  - **Actions.** Resume, Stop and Retry.
- An **"Interrupted"** banner on Home and in the chat, with Resume.
- A **verification card** at the end of a chat run: check list, passed or failed, and output.

### App

- **Run detail screen**, reached from Operate → Recent runs and from the chat header. It shows the timeline, the verification results, and Resume or Stop.
- The chat shows the verification card and the interrupted banner.
- **No push notifications in this phase.** Real push needs a relay or FCM/APNs and is part of the remote-access design. The app keeps polling while open.

### Gate

- The kill-9 harness shows zero duplicate side effects.
- Every run ends verified or explicitly partial.
- A team run's tester can actually run a command through the approval path.
- An interrupted run is resumed from the phone.

## 4. Phase 4: Safe workspace runtime (milestone M)

### Server

1. **Trusted root.**
   - Build mode defaults `trusted_root` to the run's workspace.
   - The workspace is chosen per run (`workspaces:manage` for adding roots).
   - Paths outside it are refused before the policy check.
2. **Git checkpoints.**
   - After each file-changing step, the server commits the workspace state to a shadow ref (`refs/gubern/checkpoints/<run>/<step>`). The user's branch and index are never touched.
   - `GET /api/runs/{id}/changes` returns changed files and diffs per step.
   - `POST /api/runs/{id}/restore` (`files:write` plus approval) restores a step.
3. **Sandbox adapter.**
   - An interface with three implementations:
     - **Linux.** A restricted process with bubblewrap, or Landlock plus seccomp; a filesystem view limited to the workspace; no network by default.
     - **Windows.** Job Objects (kill the tree, CPU and memory caps) plus path containment. This is documented honestly as containment, not isolation.
     - **macOS.** Best effort, documented.
   - The run records which adapter and limits applied.
4. **Network policy per run.** Off (the default), an allow-list, or on. Changing it is an approval.
5. **Escape suite.**
   - Tests that try to write outside the root, follow symlinks, path-traverse, read secrets, reach the network and fork-bomb.
   - The suite must be 100% blocked and audited on Linux (CI runs on Linux).

### Dashboard

- **Workspaces page.** Trusted roots, the sandbox adapter and its limits for this server's operating system (with honest wording), and the default network policy.
- **Run detail additions.** A per-step changed-files list, a diff viewer, a checkpoint timeline, and Restore (behind approval).

### App

- **Run detail additions.** A changed-files list and a read-only diff viewer.
- **Restore.** It asks for an approval, so it needs step-up on the phone.
- **Sandbox badge.** Runs show "Isolated (Linux)" or "Contained (Windows)".

### Gate

- The escape suite is 100% blocked and audited on Linux, and the limits for other platforms are documented.
- Restoring a checkpoint returns the workspace byte-for-byte.

## 5. Phase 5: MCP client, then the browser (milestones N and O)

### Server: MCP (N)

1. **Client.**
   - Uses the official Go MCP SDK. Its version and maturity are checked before adoption; the research reports v1.8.0, which is unverified.
   - Transports: stdio and streamable HTTP. No deprecated features (Roots, Sampling).
2. **Connector registry.**
   - Endpoints:
     - `GET /api/connectors` (`integrations:read`).
     - `POST /api/connectors` (`integrations:manage`), which adds a server by command or URL. Adding one is an approval.
   - **Environment.** The child process gets a scrubbed environment; secrets come from the keyring by reference, never inline.
3. **Default-deny tools.**
   - Each discovered tool starts disabled.
   - An admin enables it and assigns a risk class; the default is HIGH.
   - Server annotations such as `readOnlyHint` are shown but never lower risk on their own.
4. **One path.** MCP tool calls go through the same ToolSpec, policy, approval, audit, event log and effect ledger as local tools. Results are fenced as data.

### Server: browser ladder (O)

- **Rung 1.** Hardened fetch: size caps, content types, redirect rules, and no internal addresses.
- **Rung 2.** Playwright scripts in an ephemeral profile, as an optional dependency (decision D5).
  - Downloads are quarantined.
  - Each page visit and action is an event with a screenshot.
- **Rung 3.** The accessibility-tree mode stays deferred, per ADR A10.

### Dashboard

- **Connectors page.**
  - Add a server: command or URL, then a consent screen listing its tools, their annotations and proposed risk.
  - Per-tool enable and risk.
  - Health, last error and usage taken from the audit log.
- **Run timeline.** Browser steps appear with their screenshots.

### App

- **Operate.** A read-only Connectors list with health.
- **Approvals.** MCP tool calls arrive as normal approvals, labelled with the connector and tool.
- **Chat.** Browser steps appear with their screenshots.

### Gate

MCP and browser side effects go through the identical approval, trace and verification path. A test proves an MCP tool can't skip policy, even when it advertises `readOnlyHint`.

## 6. Applies to every phase

- **Authorization.**
  - New routes go into `routeScopes`, which is default-deny.
  - The phone's scope ceiling stays as it is (read access plus decide). A phone approves installs, restores and connector tools; it never manages them directly.
- **Audit and docs.**
  - Every admin write is audit-logged.
  - `docs/openapi.yaml`, `docs/api.md` and `docs/security.md` are updated in the same commit as the route.
- **Event types.** New events (`skill_activated`, `verification_*`, `checkpoint_created`, `sandbox_denied`, `browser_step`) go through the redaction layer before they are stored or streamed.
- **Tests.**
  - The integration harness uses the fake model.
  - The dashboard gets Vitest and Playwright smoke tests.
  - The app gets widget tests and runs on the emulator against the scratch server.
- **Results are recorded, never invented.**
- **Commits.** Local only. No push, merge or release without your go-ahead.

## 7. Order and size

One engineer with an AI assistant. Effort ranges are estimates.

| Milestone | Work | Depends on | Size |
|---|---|---|---|
| J | Integrate `dev/gubern-policy` | – | 0.5–1 day |
| K | Phase 2: skills (server, dashboard, app) | J, D2 | 1.5–2 weeks |
| L | Phase 3: durable runs, verification, role profiles | J, D1 | 2.5–3.5 weeks |
| M | Phase 4: workspace, checkpoints, sandbox (Linux and Windows) | L | 4–7 weeks |
| N | Phase 5a: MCP client and connectors | J (L recommended) | 2–3 weeks |
| O | Phase 5b: browser ladder, rungs 1–2 | N, D5 | 2–3 weeks |

K and L can overlap: the skills API and UI don't touch the run engine. N can start once L's state machine lands.

## 8. Decisions

Decided on 2026-11-08:

| # | Decision | Outcome | Effect |
|---|---|---|---|
| D1 | Team mode | **Fold into role profiles on the one loop.** | L removes the separate team path. |
| D2 | Sidecar and lock names | **`gubern`**: `agents/gubern.yaml`, `skills.lock` under the Gubern config directory | K |
| D3 and D4 | Eval models and budget | **Local (Qwen on Ollama) plus Claude, with a monthly cap.** Needs your Anthropic key in the keyring and the cap value before the first hosted eval run. | K and L gates. The eval runner refuses to start a hosted run without a cap. |
| D6 | Sandbox platforms | **Linux and Windows together.** Linux gets bubblewrap or Landlock isolation. Windows gets Job Objects plus an AppContainer spike for filesystem isolation. macOS is best effort. | M grows to **4–7 weeks**. Both escape suites gate the phase. Whatever AppContainer can't do is documented. |

Still open:

| # | Decision | Recommendation | Blocks |
|---|---|---|---|
| D5 | Browser rung 2 needs Node and Playwright as an optional dependency. Accept? | Yes, optional, off by default | O |
| D7 | The hosted eval cap (USD per month) | Your call; the runner enforces it | First hosted eval |
