---
name: gubern-agent-tool
description: >
  How to add or change a tool the Gubern agents can call (internal/agents):
  its schema and risk class, path and argument safety, approvals, the
  per-tool policy, and the tests it needs. Use when adding or editing a Tool.
license: Apache-2.0
metadata:
  project: gubern
---

# Adding or changing an agent tool

Gubern has three tool registries, and a tool can be in more than one:

- local tools (`NewLocalTools`, `RegisterLocalTools`): chat commands, `/agent`, Telegram, the API;
- team-mode specialists (`NewTrustedToolRegistry`): file tools run without a prompt;
- background sub-agents (`RegisterBuiltins`): run inside a sandbox directory.

All three apply `agents.policy`. A new registry must call `ApplyPolicy` too.

## Checklist

1. **Schema and risk.** Add a `builtinSpecs` entry in `toolspec.go`: a
   description written for the model, a JSON schema with `required` fields,
   and a risk class. `RiskRead` tools are the only ones a `--read-only` run
   offers, so don't mark anything that writes, runs a command or talks to
   the network as read.
2. **Policy name.** A sub-agent-only tool goes in `subAgentToolNames`
   (`policy.go`), or a policy naming it refuses to start.
3. **Paths.** Resolve every path from the model through `resolveLocal` (local
   tools) or `sandboxResolve` (sub-agents), including paths read back from
   files the tool wrote earlier (metadata, backups). Never `filepath.Join` a
   model path and use it directly.
4. **Arguments to programs.** Use `exec.Command` with separate arguments,
   never a shell. Put model-supplied paths after `--` (`git add -- <files>`),
   or a "file" like `-f` becomes an option.
5. **URLs and API paths.** Outbound HTTP goes through `safedial`. Reject
   non-canonical paths (`%`, `..`, `//`, `\`) before checking a deny-list,
   and don't follow redirects to a target you didn't check.
6. **Approval.** A tool that changes anything returns `*ApprovalError` with a
   `Preview` that shows exactly what will happen. Approved re-runs go through
   `executeApprovedLocal` / `ExecuteApproved`; add a case there if your tool
   has its own gate, and keep `policyTool` unwrapped first.
7. **Side effects.** Implement `SideEffecting() bool` for tools that change
   state, so durable runs don't repeat them.

## Tests

- One test per safety rule above that applies, written so it fails on code
  without the rule: run it against the old version once to check.
- Path equality in tests: compare against `filepath.EvalSymlinks(t.TempDir())`;
  macOS temp is behind `/var -> /private/var` and Windows CI uses short names
  (`RUNNER~1`), so a raw comparison passes locally and fails in CI.
- Then follow the `gubern-dev-checks` skill.
