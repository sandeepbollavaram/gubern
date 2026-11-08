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

## 9. System-design check (17 concepts)

Checked against the code on 2026-11-08 (repository-verified; file paths are
where the evidence is). "Fit" says whether Gubern needs more of it now.

| # | Concept | Gubern today | Gap and where it goes |
|---|---|---|---|
| 1 | Load balancing | Edge proxy: round-robin and least-conn for HTTP, TCP and gRPC (`internal/proxy/http/balancer.go`). AI gateway picks provider key slots by score and falls back to any working one (`internal/gateway/router.go`). | Backends are not ejected when they fail: round-robin's `RecordResult` does nothing. Add outlier ejection; small, and lands in P. |
| 2 | Caching | In-process caches (LSP, tool output, A2A catalog). No response cache in the proxy, no prompt or semantic cache for models. | Provider prompt caching (Anthropic cache control) cuts eval and run cost. Add it with D4's hosted evals, in K. |
| 3 | CDN | Not used. The dashboard is embedded in the binary. | Not needed. Remote access (design G) stays on a relay, not a CDN. |
| 4 | API gateway | Yes: the edge proxy plus the AI gateway (`/v1` OpenAI-compatible) with auth, scopes, rate limits and audit. | MCP tools in N go through the same gateway rules. |
| 5–6 | Microservices or monolith | Modular monolith in one Go binary, by decision (ADR A2). | Keep it. Splitting adds failure modes before there's load to justify them. |
| 7 | Replication | Raft (`internal/cluster/raft.go`) replicates cluster config, but its log and snapshots are **in-memory even when a data directory is set**. Agent state (approvals, event log, identity, runs, effect ledger) is **node-local SQLite**. | No durable replication of agent state. Track P: on-disk Raft stores, then replicate or centralize approvals, the event log and identity. |
| 8 | Partitioning | None. Namespaces are logical (tenancy), not shards. | Not needed at current scale. Revisit only if one node's SQLite becomes the limit. |
| 9 | Message queues | In-process queues and a bus (`internal/agents/runtime.go`, `bus.go`). Nothing durable. | Phase 3 (L) adds a **durable run queue** on SQLite, with leases and at-least-once delivery fenced by the effect ledger. |
| 10 | Rate limiting | Yes: API, keys, webhooks and pairing, using the edge-resolved IP rather than a client header. AI daily budget. | Per-principal run, token and spend limits, and per-connector call limits (N). |
| 11 | Consistent hashing | None. | Only useful with P (pinning sessions to nodes). Not before. |
| 12 | CAP theorem | Config is CP through Raft. Agent state is single-node, so consistent but unavailable if the node is down. | For approvals, **consistency over availability**: a decision must never be applied twice or lost. P keeps approvals CP; a read-only activity view may serve stale data. |
| 13 | Horizontal scaling | Cluster membership exists (memberlist), but runs, approvals and streams live on the node that started them. | Can't spread agent work across nodes yet. Track P: shared store plus lease-based queue (from 9) plus session affinity. |
| 14 | Vertical scaling | The default today. | Fine for one team or server. Document sizing (runs per CPU, SQLite on SSD). |
| 15 | Fault tolerance | Healing checks, retries with backoff, resumable SSE (`Last-Event-ID`), the effect ledger. | Phase 3 (L): resume after restart, interrupted runs, the kill-9 gate. |
| 16 | Circuit breaker | Only in forge and plugin hooks. None for model providers, proxy backends or (future) MCP servers. | Add a breaker per provider and per MCP server (N), and in the proxy (P). An open breaker shows on Operate and Connectors in both UIs. |
| 17 | Event-driven architecture | Yes: the redacted, persisted run event log (milestone D) feeds SSE, the dashboard, the app and activity. | New phases add event types rather than new polling. Phase 3 turns run status into a state machine driven by these events. |

### Track P: scale-out (after O, or earlier if you run more than one node)

1. On-disk Raft log, stable and snapshot stores (raft-boltdb or SQLite-backed), plus a restart test.
2. Agent state moves to a replicated store: Raft-applied approvals and identity, and a replicated or shipped event log. Approvals stay CP.
3. A lease-based run queue across nodes, with session affinity by consistent hashing on the session id.
4. Proxy: passive health checks, outlier ejection and a circuit breaker per backend.
5. Gate: kill the leader during a waiting approval. The decision is applied exactly once, and the run resumes on another node.

The items added to existing phases are small and go in with them:

| Item | Phase |
|---|---|
| Prompt caching | K |
| Durable run queue | L |
| Circuit breakers for providers and MCP servers, per-connector rate limits | N |

## 10. Progress

Updated 2026-11-08 04:35 IST. Local commits only.

**J: policy integration. Done.**
- Nine commits from `dev/gubern-policy` landed on this branch; the run-deadline commit was already here as `d3dfaab`.
- The full suite passes.
- On the binary:
  - a misspelt tool refuses to start;
  - `run_terminal` deny reports "disabled by policy";
  - `read_file` ask opens an approval in the shared service.

**K: skills. Done, except real-model measurements.**

| Item | Commit |
|---|---|
| Skills API: list, read, on/off switches, audited | `624e0a5` |
| `skill_activated` run event | `99f1975` |
| Unreadable skill roots are reported, not hidden | `09aa4bb` |
| Injection gate test | `f386dad` |
| Pinned installs behind a HIGH RISK approval, `skills.lock`, tamper detection (catalog fails closed) | `65e5842` |
| `gubern skills install` | `62098f1` |
| `agents/gubern.yaml` sidecar | `ac27abe` |
| `gubern skills eval`, which approves only the declared check | `11eb17e` |
| Terminal approvals carry their command | `06e2557` |
| Claude prompt caching, with cached tokens counted | `fde77d9` |
| Dashboard Skills page, install dialog, sidecar fields | `bc46685`, `f753810`, `cfd8a68` |
| App: Skills on Operate, chip in chat, changed-skill warning | `14b9c24`, `5751571` |

- **Live checks:**
  - an install requested over the API was approved on the phone by slide and PIN, then flagged after a file was edited;
  - a CLI install with `--wait` reported the approval;
  - `skills eval` ran end to end on the fake model (2/2 with and without the skill).
- **Open:**
  - measuring real gains needs a real model (local Qwen, or Claude after D7);
  - the git install path and live Claude caching are not verified against the network;
  - Bedrock Converse has no cache points yet.

**L: durable runs. Started.**

**L.1: run state machine. Done.**
- **Commits:** `2e01426` (server), `a1ea555` (dashboard and app), `9676a0f` (`dist`).
- **States:**
  - live: running, waiting_approval, verifying;
  - final: completed, partial, done, failed, cancelled, interrupted.
- **Events:** each check attempt is a `check_result` event, and the run reports a `run_outcome`.
- **Live check:** a passing check ended `completed`; a failing one ended `partial` after three attempts.

**L.2: checkpoint and resume. Done.**
- **Commits:**

  | Commit | What |
  |---|---|
  | `7925287` | Loop checkpoints and resume |
  | `c2cdea9` | Server and API |
  | `4918be3` | Dashboard and app Resume (`dist`: `0cf67a1`) |
  | `87beeec` | Fix: a crashed server's pid file blocked restart on Windows |

- **Live check:**
  - a run waiting for a write approval was hard-killed;
  - after restart it showed `interrupted`;
  - resume asked for the approval again, and the file was written exactly once;
  - a second resume was refused.

**L.3: declared checks. Done.**
- **Commits:** `b66fbfa` (server), `becc4f9` (dashboard), `ac90af7` (app).

**Workspaces: done.** Brought forward from M at your request.
- **Commits:** `2683f61` (server), `15d0731` (dashboard), `56cf508` (app).
- **What it does:**
  - `/agent` runs can work in any of several folders: the default plus ones an admin adds;
  - clients pick by name;
  - with a `trusted_root` configured, a workspace must lie inside it.
- **Live check:** a relative write landed in the chosen folder, not the default one.
- **Not verified:** the dashboard and app pickers in a browser or on the emulator. The browser tool hit a usage limit and `adb` stopped responding.

**L.4: team roles as loop profiles. Done.**
- **Commits:** `f5991b2`; checks for `/team` in `ed8c1fd`.
- **What it does:**
  - coder, tester and reviewer run on the one loop;
  - the tester's commands go through approvals, which fixes T32;
  - each role gets its own toolset.
- **Live check:** a team run ended `completed`.

**L.5: kill-9 gate. Done.** (`872c6e6`)
- 20 runs, 15 hard-killed and 10 resumed; every side effect happened at most once.
- The test fails, as it should, when resume re-runs in-flight commands.

**Phase 3 gate:** met.
- Zero duplicate side effects under kill -9.
- Every run ends verified, explicitly partial or done (unverified).
- The team tester runs commands through approvals.
- The phone can resume a run; this was unit-tested, but not yet tried live on the device.

**M.1: git checkpoints. Done.**
- **Commits:** `26f204f` (server), `2be14ff` (dashboard), `5ff3c4a` (app).
- **What it does:**
  - hidden-ref snapshots before a run and after each change;
  - per-step diffs;
  - restore only after a HIGH-risk approval.
- **Live check:** a file the agent rewrote was restored to its original content, and HEAD was untouched.

**M.2: sandbox adapters. Done (server).**
- **What it does:** `agents.local_tools.sandbox` (default `auto`) wraps every approved `run_terminal` command.
  - **Linux, bubblewrap ("Isolated").** Only the workspace is writable. Home and server state are hidden; commands get a private home that persists in the cache dir. System folders are read-only. Each command has its own PID namespace, a process cap via `prlimit`, and with `network: "off"` its own network namespace.
  - **Windows, Job Object ("Contained").** The command starts suspended and joins its job before it runs. The job ends when the command exits, so background processes die with it. Processes and memory are capped. Files and network are not fenced, and the API says so.
  - **Other platforms:** none, and the API says why.
- **Visible as:** `GET /api/workspaces` returns `sandbox` (label, enforced, not enforced), and each `run_terminal` result records its adapter.
- **Escape suite:** 8 escape tests plus a private-home test in `internal/sandbox`, each with a control. CI installs bubblewrap and runs them with `GUBERN_SANDBOX_REQUIRED=1`.
  - **WSL Ubuntu, as an unprivileged uid:** all passed; mutation runs (network always shared; host `/tmp` exposed) failed 4 tests as they should.
  - **One unexplained failure:** the fork-bomb test failed once in the first full run. It then passed 25 full runs in a row; the cause is not known.
  - **Windows job tests:** pass. Removing the process cap or kill-on-close makes them fail.
- **Live check (Windows):** `start /b ping … & echo launched`.
  - Sandbox off: the call took 7.0s and `ping` was left running.
  - With the job: 1.9s, and nothing left behind.
- **Not done:** `run_command` (sub-agents) and Forge builds aren't wrapped. There is no Linux memory cap (needs cgroups), no seccomp, and no AppContainer spike yet.
- **Network default:** stays "on" until M.4 makes it a per-run choice behind an approval; flipping it before that would break `go`/`npm` downloads silently.

- **Commits:** `02a60f8` (server), `a3ead2b` + `ab38398` (dashboard), `45051d3` (app).
- **Dashboard:** a Workspaces page with the folders (add, remove, missing folders flagged) and a sandbox card that shows what is and isn't enforced, word for word from the server. Checked in a browser in mock mode, including remove and phone width.
- **App:** a sandbox badge in the agent composer, and a Sandbox panel in Operate.
  - On the emulator, re-paired with the scratch server, both showed "Contained (Windows)" with the server's limits.

**M.4: network per run. Done.**
- **Commits:** `d4fe651` (server), `38d1662` + `1f22924` (dashboard), `ae6ff80` (app), plus a fix to how Windows reports network (below).
- **What it does:** a submit may ask for `"network": "on"` or `"off"`. It is a request, not a grant; the coordinator decides before any command runs.
  - Narrowing to off always works.
  - Widening to on, when the default is off, opens a HIGH-risk `network_access` approval. If it is rejected, the run continues offline and says so.
  - Where the sandbox can't enforce network policy, nothing is asked and the run is told so.
  - Only the granted value reaches `run_terminal`; the checkpoint keeps it for a resume. Team runs follow the same rule.
- **Tests:** a rules table (9 cases) and an end-to-end `/agent` run with a recording sandbox (4 cases), checking the network each command actually got. Making the tool read the client's request instead of the grant fails the "rejected" case. There is also an API validation test.
- **Live check found a bug:** Windows reported its network as "on", so the server thought network policy was enforceable there. A run asking for network off was told "loopback only" while it still had the network. Fixed: Windows always reports "not enforced", and the live run now says so.
- **Default:** still "on". Flipping it to "off" is a product decision: it would make every Linux run without an approval offline.

**M.5: `run_command` in the sandbox. Done.**
- Background sub-agents' `run_command` now goes through the same sandbox as `run_terminal`, via one shared `runContained`. Its folder is the only writable one, and it gets the run's granted network.
- **Tests:** a recording-sandbox test (writable folder, granted vs. default network), plus a run of `go version` under the real job object. Both pass on Windows. On Linux they skip, because WSL has no Go installed.
- **Found while testing:** the Linux fork-bomb test failed again. The prlimit cap didn't apply in **2 of about 90 runs**, letting all 200 processes start.
  - It didn't reproduce in 40 runs of the test alone, 15 full-suite runs, or 3 runs from a cold WSL start.
  - The cap is now reported as "best effort", and cgroups `pids.max` is the planned fix.
- **Environment gap:** `TestRunCommand_EnvironmentIsScrubbed` fails in WSL only because Go isn't installed there; it runs `go env`.

**Next: the rest of M.** Remaining:
- a reliable Linux process cap (cgroups `pids.max`);
- wrapping Forge builds;
- the AppContainer spike (Windows file and network fence).

Then Phase 5: N (MCP client and connectors).
