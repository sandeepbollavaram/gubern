# Agents

Gubern runs an AI **coordinator** that classifies your message and dispatches
it to the right capability: code/file work, app builds, research, devops,
data pipelines, or multi-agent orchestration. You talk to it from the TUI
(`gubern ui`), the web dashboard, Telegram, or the HTTP API.

## AI providers

Configure one or more via `gubern setup` or environment variables. The gateway
tries providers in priority order and falls back on failure.

| Provider | Env | Notes |
|---|---|---|
| Anthropic Claude | `GUBERN_ANTHROPIC_KEY` | Best reasoning |
| DeepSeek | `GUBERN_DEEPSEEK_KEY` | Cheap, OpenAI-compatible |
| OpenAI | `GUBERN_OPENAI_KEY` | GPT-4o family |
| Google Gemini | `GUBERN_GEMINI_KEY` | Gemini 1.5 |
| Groq | `GUBERN_GROQ_KEY` | Very fast, free tier |
| AWS Bedrock | `GUBERN_BEDROCK_REGION` + AWS creds | SigV4-signed |
| Azure OpenAI | `GUBERN_AZURE_OPENAI_KEY/_ENDPOINT/_DEPLOYMENT` | |
| OpenRouter | `GUBERN_OPENROUTER_KEY` | 75+ models |
| Ollama | `GUBERN_OLLAMA_URL` | Local, free |

A daily USD budget can cap spend; usage is visible at `/api/ai/cost`.

## Capabilities

### Code & files

The agent has tools for listing, reading, searching, writing and editing files,
running terminal commands, git status/diff/add/commit, and undoing its last
write. Writes and terminal commands are **approval-gated**: the agent asks
before touching your machine. Tools are chosen from your request (commands
and keywords), not by the model itself, and the model doesn't see tool output.

Not reachable from chat yet: an `http_get` tool (web fetching happens inside
the research agent) and **LSP diagnostics** (`lsp_diagnostics`). Both exist in
the code but nothing routes requests to them.

### Model-driven runs: `/agent <task>` (new)

`/agent fix the failing test in internal/foo` hands the task to the model
with the same local tools: it chooses which tools to call, sees their
results, and keeps going until it answers. Every write, command and commit
still goes through the approval gate (Telegram if configured, otherwise the
TUI/API approve call; pending approvals expire after 15 minutes), and each
decision is in the audit log. A run stops after 20 model turns, 10 minutes
or 250k tokens, and when the client that started it disconnects (nobody is
left to approve its actions or read its answer). Works with every built-in provider (Claude,
OpenAI, DeepSeek, Groq, OpenRouter, Azure OpenAI, Gemini, Bedrock via the
Converse API, Ollama). Models without native tool calling (many small
local models) fall back to a text protocol: the model asks for a tool in a
JSON block. It is less reliable, and every call still goes through the same
checks and approvals. Without
`/agent`, requests use the existing command and keyword routing.

> _"Read main.go, find the bug in the retry loop, and fix it."_

Give a run a completion check to make "done" mean something:

```
/agent --check "go test ./internal/foo/..." fix the failing test
```

When the model says it is finished, Gubern runs the check (through the
approval gate, like any command). If it fails, the output goes back to the
model, up to three times. The run ends with "✓ Verified" or "✗ Not verified";
without a check, the answer is only the model's claim.

To ask about code without letting the model change anything:

```
/agent --read-only explain how the retry loop in main.go backs off
```

A read-only run offers the model only the read tools (read, list, search,
git status and diff). Writes, commands and git changes are not offered, and
if the model asks for one anyway it is refused without an approval prompt and
recorded as `agent.policy.denied` in the audit log. `--read-only` and
`--check` can be combined; the check is your command, so it still runs.

`gubern runs` lists recent runs from the audit log: when each started, its
session, how it ended (`done`, `budget_exceeded`, `cancelled` when the client
disconnected, `model_error`) and whether its `--check` passed. A run marked
`interrupted` started but never finished because the server stopped or crashed
during it; look at what it changed before running it again.

Not every model can drive this loop. `gubern ai probe` sends each configured
provider a few small requests and checks that it calls a tool, uses typed
arguments, reads a tool result back and ignores an instruction planted in a
result; nothing is executed. It exits non-zero if any check fails, and
`--provider ollama` limits it to one provider. Each provider costs a few
short model calls.

#### Skills

`/agent` reads [Agent Skills](https://agentskills.io) — `SKILL.md` folders
written for Claude Code, Codex, OpenCode and similar tools — from the
workspace's `.agents/skills/`, `.claude/skills/` and `.opencode/skills/`
directories. Only each skill's name and description are given to the model,
in its first message and marked as text from the repository (never in the
system prompt); it reads the full `SKILL.md` with `read_file` when a task
calls for it. Skills are instructions, not permissions: they can't run anything on
load, `allowed-tools` is ignored for now, and every action a skill suggests
still goes through the approval gate. Invalid skills (bad name, name not
matching the folder, missing description, unsupported YAML such as `[a, b]`
lists, symlinked files, files over 256 KiB) are skipped with a warning in the
log. Skills are re-read on every run. `gubern skills list` (or `--dir <workspace>`)
shows which ones load and why any are skipped.

Admins can switch a skill off without deleting it (dashboard **Skills**, or
`POST /api/skills/{name}/disable`); it is then left out of what the model is
shown. To add a skill from elsewhere, run
`gubern skills install <folder | https://host/repo.git@<full commit>>` (or
use **Install skill** in the dashboard). The server copies it into
quarantine and fingerprints every file. It then opens a HIGH RISK approval
listing them, which you can decide in the dashboard, in chat, or on a paired
phone with step-up. The skill is installed only if approved, and is pinned in
`skills.lock`. If a pinned skill's files change later, it is flagged as
changed and not offered until it is installed again. Add `--wait 10m` to have
the command wait for the decision. See [api.md](api.md#skills).

To see whether a skill helps, give it eval tasks (`evals.json` in the skill,
or the path in `agents/gubern.yaml`):

```json
{"tasks": [{"name": "changelog", "task": "write the changelog entry for this release", "check": "grep -q Unreleased CHANGELOG.md"}]}
```

`gubern skills eval <name> [--runs 3]` runs each task as an `/agent` run with
that completion check, with the skill on and then off, and prints the pass
rates. The tasks really run in the server's workspace, so use a throwaway
one. The runner approves only the declared check command in its own eval
sessions and rejects anything else, so a task needing other approvals simply
fails. The skill's on/off state is restored afterwards. The result is only
as meaningful as the model the server uses.

#### Team mode: coder, tester, reviewer

`/team [--check "<command>"] <goal>` runs three role profiles in turn on the
same loop as `/agent`, with the same tools, policy, approvals, audit and run
events:

| Role | Tools | Job |
|---|---|---|
| Coder | All local tools | Makes the change |
| Tester | Read tools, `git_status`, `git_diff`, `run_terminal` | Runs the tests and reports with evidence; no edits |
| Reviewer | Read-only | Reviews the change and ends with `VERDICT: APPROVE` or `VERDICT: CHANGES REQUESTED` |

A tool outside a role's set is refused ("not available to this role").

With `--check`, the tester's run must make the check pass. The team ends:

- `completed` when the check passes and the reviewer approves;
- `partial` when the check fails or the reviewer requests changes;
- `done` when no check was given (unverified).

This replaces the separate specialist pipeline, whose tester could not run
commands. That pipeline is still used only when no local tools are
configured.

### Gubern Forge — autonomous app builds

Forge builds complete apps (and Android APKs) from a prompt, in a sandbox, and
delivers the artifact. Requires an AI provider.

> _"Build a Flutter expense tracker with charts and send me the APK."_

### Research agent

Web search + page fetch + summarize into a saved report. The fetcher pins DNS
resolution and re-validates redirects to prevent SSRF.

> _"Research current best practices for Postgres connection pooling."_

### DevOps agent

SSH into your servers, run Docker/Nginx operations, and report back.

> _"Restart the api container on web-2 and confirm it's healthy."_

### Data pipelines

Analyse CSV/JSON and generate charts.

> _"Summarise orders.csv and chart revenue per region."_

### Multi-agent orchestration

The orchestrator decomposes a goal into a task DAG and runs tasks with
dependency + concurrency control. Unknown or cyclic dependencies fail fast
rather than stranding silently. Tasks may set `max_attempts` (capped at 3) for
bounded retries on executor failure, and optional `verification_criteria` to
check executor output after each attempt. **Partial:** only structured criteria
are enforced today (`nonempty`, `contains:…`, `prefix:…`). Free-text criteria
are rejected when the plan is validated (the orchestration fails fast with
`unsupported verification_criteria` instead of running); as defence in depth, a
persisted task that still carries unsupported criteria fails at run time as
`unsupported`. There is no shell/test harness auto-verification yet.

Capability discovery uses the A2A agent catalog (`GET /api/agents/catalog`,
API-key gated like the other agent endpoints): validated static manifests with
capability-based selection and bounded delegation contracts (depth/fan-out
limits). A planner task may set `required_capabilities` (plus optional
`permission_profile`) *instead of* `agent`; setting both is a plan error. The
catalog is registry data only — listing an agent does not mean it is running.
Live specialist status comes from `GET /api/team/agents`; both are shown on the
dashboard **Agents** page.

> _"Research the top 2 caching libraries, benchmark both, and write up which to use."_

## Conversation memory

Conversations persist in a SQLite database with full-text search. Each session
has an ID; the coordinator includes recent turns as context. Legacy JSON
conversations are migrated automatically on first run.

- List sessions: `GET /api/agents/history`
- One session: `GET /api/agents/history/{id}`
- Submit (SSE streaming with `Accept: text/event-stream`): `POST /api/agents/submit`

`session_id` is validated (`[A-Za-z0-9_.-]{1,64}`); omit it to have the server
generate one.

## Approval gate

Tools that touch the real machine require explicit approval (TUI `[Y]/[N]`,
Telegram reply, or `POST /api/agents/approve`). This is the primary safety
control for unattended runs — keep it on in production. Approvals can be
bound to the exact action shown (`action_id`, see [api.md](api.md)) and
expire after 15 minutes.

### Local workspace trust (Auto Mode foundations)

Configure in `gubern.cue` under `agents.local_tools` (defaults: empty
`trusted_root`, `auto_mode: false`):

```cue
agents: {
	local_tools: {
		trusted_root: "/path/to/your/project" // or a path relative to the work dir
		auto_mode:    true                    // requires trusted_root
	}
}
```

Environment overrides: `GUBERN_AGENTS_LOCAL_TOOLS_TRUSTED_ROOT`,
`GUBERN_AGENTS_LOCAL_TOOLS_AUTO_MODE`. Invalid or missing trusted directories
fail at startup with a clear field path. When both the coordinator working
directory and `trusted_root` are set, startup requires **work dir ⊆ trusted_root**
(lexical containment). Prefer `trusted_root` equal to the project root (or `.`
relative to that root), or an absolute ancestor that contains the work dir.
A `trusted_root` that is only a subdirectory of the work dir is rejected so the
shell cwd is not left outside the FS trust boundary.

| Field | Meaning |
|---|---|
| `trusted_root` | When set, all local FS tool paths must resolve inside this directory (lexical + symlink checks). Empty = no workspace confinement (approval remains the control). |
| `auto_mode` | With a `trusted_root`, routine in-scope write/edit/create_project may skip repeated approval prompts. Shell (`run_terminal`) stays approval-gated. Release builds still force shell approval. |

Auto Mode keeps the approval prompt for `gubern.cue` and for paths inside
`.git`, `.githooks` and `.husky`, because hooks would otherwise run on a later
approved command. A custom `core.hooksPath` is not detected.

Auto Mode is **not** unrestricted host access. Out-of-scope paths are denied by
the runtime.

### Sandbox for `run_terminal`

`agents.local_tools.sandbox` runs each approved `run_terminal` command inside an
operating-system boundary. What that boundary is depends on the platform, and
`GET /api/workspaces` reports it (`sandbox.label`, `enforced`, `not_enforced`):

```cue
agents: local_tools: sandbox: {
	mode:          "auto"   // "auto" (default), "required" (refuse to start without it), "off"
	network:       "on"     // "off": Linux commands get loopback only
	max_processes: 256      // processes at once (stops a fork bomb)
	memory_mb:     8192     // the whole tree; Windows only; 0 = no cap
	read_paths: []          // Linux: extra folders commands may read (toolchains)
}
```

| | Linux (bubblewrap) | Windows (Job Object) | macOS |
|---|---|---|---|
| Label | Isolated (Linux) | Contained (Windows) | Not sandboxed |
| Writable | the workspace only (trusted root, else the work dir) | anything the server's user can write | anything |
| Home folder and server state | hidden; commands get a private home that persists between commands (in the cache dir, `sandbox-home`) | visible | visible |
| Network off | enforced (own network namespace) | **not enforced** | not enforced |
| Process cap | yes (`prlimit`, counted in the sandbox's user namespace) | yes | no |
| Memory cap | no (needs cgroups) | yes | no |
| Whole tree dies with the command | yes (PID namespace) | yes (the job is ended when the command exits) | process group on timeout |

On Linux it needs `bwrap` (package `bubblewrap`) and unprivileged user
namespaces; Ubuntu 24.04's AppArmor default blocks those
(`kernel.apparmor_restrict_unprivileged_userns`), in which case `auto` runs
unsandboxed and logs why, and `required` refuses to start. On Windows the
command starts suspended and is assigned to its job before it runs, so nothing
it starts escapes the job. Windows containment is not isolation: an
AppContainer would add a file-system and network fence, and is a later step.

**Network per run.** `network` is the default. A run can ask for its own
(`"network": "on"` or `"off"` on `POST /api/agents/submit`, or the Network
control in the dashboard and app composers):

- asking for `off` always works: it only narrows;
- asking for `on` when the default is `off` opens a HIGH-risk
  `network_access` approval before any command runs; if it isn't approved,
  the run continues with the network off and says so;
- where the sandbox can't enforce network policy (Windows, or no sandbox)
  nothing is asked and the run is told the setting isn't enforced.

The request is never read from message text, and only the coordinator's
decision reaches the terminal tool. A resumed run keeps the network it was
granted. `/agent --read-only` runs no commands and isn't asked.

Each `run_terminal` result records `"sandbox": "<adapter>"`. Commands run by
background sub-agents (`run_command`) are not wrapped yet. Approval, the
workspace check, the scrubbed environment and the process-tree kill apply
with or without a sandbox. `GUBERN_AGENTS_LOCAL_TOOLS_SANDBOX_MODE` and
`GUBERN_AGENTS_LOCAL_TOOLS_SANDBOX_NETWORK` override the mode and network.

### Per-tool policy

`agents.policy` turns tools off or makes them always ask, for chat commands,
`/agent` runs, team-mode specialists, background sub-agents, Telegram and the
API alike:

```cue
agents: {
	policy: {
		deny_tools: ["run_terminal"]          // never runs; not offered to the model
		ask_tools:  ["read_file", "git_commit"] // always asks first
	}
}
```

An `ask_tools` entry needs approval even for a read-only tool and even where
Auto Mode would skip the prompt; one approval covers both the policy and the
tool's own gate. Tool names are checked at startup: an unknown name, or a tool
in both lists, refuses to start (so a typo can't leave a tool enabled). The
names are `create_project`, `edit_file`, `find_files`, `git_add`,
`git_commit`, `git_diff`, `git_status`, `list_directory`, `read_file`,
`run_terminal`, `search_files`, `undo` and `write_file`, plus the tools
background sub-agents use: `http_get`, `lsp_diagnostics`, `run_command`,
`send_message` and `vortex_api` (`read_file` and `write_file` cover the
sub-agent tools of the same name too). Like `local_tools`,
the policy is read at startup; restart to change it. Attempts to use a denied
tool (a chat command, or an `/agent` model asking for it) are recorded as
`agent.policy.denied` in the audit log.

### Hot reload (`SIGHUP` / `gubern reload`)

Config reload swaps the live `gubern.cue` in the config `Holder` (same path as
routes and policy). **`agents.local_tools` is validated at process startup only:**
`RegisterLocalTools` binds `trusted_root` and `auto_mode` into the coordinator’s
local tool registry once; there is no reload hook to rebuild that registry or the
specialist trusted-tool set. In-flight agent work may still be executing tools
built from the previous settings.

To change `trusted_root` or `auto_mode`, edit `gubern.cue` and **restart Gubern**
(`gubern stop` then `gubern start`, or your service manager). Do not rely on
reload to tighten or widen workspace trust on a running process.
