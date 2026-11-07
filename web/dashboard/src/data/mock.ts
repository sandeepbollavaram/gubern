// In-memory mock of the Gubern API for the Phase 1 prototype. It simulates
// streaming replies, grouped tool steps, approvals with a 10-minute timeout
// (one decided "in Telegram"), clarifying questions and a multi-step run so
// every flow in the UX spec can be clicked through without a server.
import type {
  Approval,
  AttentionItem,
  GubernClient,
  Identity,
  ModelInfo,
  Output,
  QuestionSet,
  RunPlan,
  SendOptions,
  SessionSummary,
  Skill,
  SystemSummary,
  TranscriptItem,
  ToolStep,
} from "./model";

const MIN = 60_000;
const now = () => Date.now();
let seq = 100;
const nextId = (p: string) => `${p}-${++seq}`;

interface State {
  sessions: SessionSummary[];
  transcripts: Record<string, TranscriptItem[]>;
  approvals: Record<string, Approval>;
  questions: Record<string, QuestionSet>;
  plans: Record<string, RunPlan>;
  outputs: Output[];
  running: Set<string>;
  spendToday: number;
}

const t0 = now();

const state: State = {
  sessions: [
    { id: "s-build", title: "Fix the failing build in ~/app", kind: "agent", status: "needs-approval", updatedAt: t0 - 2 * MIN, unread: true },
    { id: "s-health", title: "Weekly route health review", kind: "run", status: "running", updatedAt: t0 - 6 * MIN },
    { id: "s-backup", title: "Set up nightly backups", kind: "agent", status: "needs-answer", updatedAt: t0 - 14 * MIN },
    { id: "s-latency", title: "Why is the api route slow?", kind: "chat", status: "done", updatedAt: t0 - 3 * 60 * MIN, pinned: true },
    { id: "s-tls", title: "Rotate the TLS certificates", kind: "team", status: "done", updatedAt: t0 - 26 * 60 * MIN },
    { id: "s-deps", title: "Dependency audit for web/dashboard", kind: "agent", status: "failed", updatedAt: t0 - 50 * 60 * MIN },
  ],
  transcripts: {},
  approvals: {},
  questions: {},
  plans: {},
  outputs: [
    { id: "o-1", title: "Route health report: week 45", type: "report", at: t0 - 6 * MIN, sessionId: "s-health" },
    { id: "o-2", title: "api route: latency findings", type: "report", at: t0 - 3 * 60 * MIN, sessionId: "s-latency" },
    { id: "o-3", title: "gubern.cue: rate limit for /v1", type: "config", at: t0 - 27 * 60 * MIN },
    { id: "o-4", title: "Forge build #212 (linux/amd64)", type: "build", at: t0 - 30 * 60 * MIN },
  ],
  running: new Set(["s-health"]),
  spendToday: 1.2,
};

state.approvals["a-1"] = {
  id: "a-1",
  sessionId: "s-build",
  tool: "run_terminal",
  risk: "HIGH RISK",
  description: "Install a missing package and rebuild",
  command: "npm install left-pad && npm run build",
  workspace: "~/projects/app",
  requestedBy: "Chat “Fix the failing build in ~/app”",
  expiresAt: t0 + 8 * MIN,
  state: "pending",
};

state.transcripts["s-build"] = [
  { kind: "user", id: "m1", text: "The build in ~/app is failing since this morning. Find out why and fix it.", at: t0 - 4 * MIN },
  {
    kind: "tools",
    id: "m2",
    tool: "read",
    steps: [
      { id: "t1", tool: "read_file", label: "Read `package.json`", status: "ok", durationMs: 40 },
      { id: "t2", tool: "read_file", label: "Read `src/format.ts`", status: "ok", durationMs: 32 },
      { id: "t3", tool: "search_files", label: "Searched for `left-pad` in 214 files", status: "ok", durationMs: 380 },
    ],
  },
  {
    kind: "tools",
    id: "m3",
    tool: "run_terminal",
    steps: [
      {
        id: "t4",
        tool: "run_terminal",
        label: "Ran `npm run build`",
        status: "failed",
        durationMs: 6200,
        detail: "src/format.ts(3,22): error TS2307: Cannot find module 'left-pad' or its corresponding type declarations.\nFound 1 error.",
      },
    ],
  },
  {
    kind: "assistant",
    id: "m4",
    text: "The build fails because `src/format.ts` imports **left-pad**, which was removed from `package.json` in this morning's cleanup commit. The quickest fix is to install it again and rebuild. That runs a terminal command, so I need your approval.",
    at: t0 - 2 * MIN,
  },
  { kind: "approval", id: "m5", approvalId: "a-1" },
];

state.questions["q-1"] = {
  id: "q-1",
  sessionId: "s-backup",
  answers: {},
  questions: [
    {
      key: "what",
      title: "What should the nightly backup include?",
      options: [
        { value: "config", label: "Config and secrets", hint: "gubern.cue, API keys store, encrypted secrets" },
        { value: "data", label: "Config and data", hint: "Also the audit log, agent memory and conversation history" },
        { value: "all", label: "Everything", hint: "Includes plugin registry and TLS certificates" },
      ],
    },
    {
      key: "where",
      title: "Where should backups go?",
      options: [
        { value: "local", label: "A local folder", hint: "~/gubern-backups, kept for 14 days" },
        { value: "s3", label: "S3-compatible bucket", hint: "Needs a bucket and access key" },
      ],
    },
    {
      key: "when",
      title: "When should it run?",
      options: [
        { value: "0200", label: "02:00 every night" },
        { value: "0400", label: "04:00 every night" },
      ],
    },
  ],
};

state.transcripts["s-backup"] = [
  { kind: "user", id: "b1", text: "Set up nightly backups for this server.", at: t0 - 15 * MIN },
  { kind: "assistant", id: "b2", text: "A few quick questions so the backup does what you expect.", at: t0 - 14 * MIN },
  { kind: "status", id: "b3", text: "Asking a question", tone: "warning" },
];

state.plans["s-health"] = {
  sessionId: "s-health",
  steps: [
    { id: "p1", title: "Collect route list and health checks", status: "done" },
    { id: "p2", title: "Pull p50 / p95 latency for 7 days", status: "done" },
    { id: "p3", title: "Compare error rates against last week", status: "done" },
    { id: "p4", title: "Check certificate expiry for TLS routes", status: "current" },
    { id: "p5", title: "Flag routes with failing backends", status: "todo" },
    { id: "p6", title: "Suggest rate-limit changes", status: "todo" },
    { id: "p7", title: "Write the weekly report", status: "todo" },
  ],
  outputs: [{ id: "o-1", title: "Route health report: week 45 (draft)", type: "report", at: t0 - 6 * MIN }],
  used: [
    { label: "Workspace", value: "~/gubern" },
    { label: "Model", value: "claude-sonnet-5-5" },
    { label: "Tools", value: "metrics, read_file, tls" },
    { label: "Memory", value: "Read · project gubern" },
  ],
};

state.transcripts["s-health"] = [
  { kind: "user", id: "h1", text: "Run the weekly route health review and write a report.", at: t0 - 9 * MIN },
  {
    kind: "tools",
    id: "h2",
    tool: "metrics",
    steps: [
      { id: "ht1", tool: "metrics", label: "Queried route latency (7 days)", status: "ok", durationMs: 820 },
      { id: "ht2", tool: "metrics", label: "Queried error rates (14 days)", status: "ok", durationMs: 640 },
      { id: "ht3", tool: "tls", label: "Checked certificate for `api.example.com`", status: "running", startedAt: t0 - 40_000 },
    ],
  },
  {
    kind: "assistant",
    id: "h3",
    text: "Latency is flat week over week. Error rate on **api** rose from 0.2% to 1.4%, all from one backend. Checking certificates next.",
    at: t0 - 6 * MIN,
  },
];

state.transcripts["s-latency"] = [
  { kind: "user", id: "l1", text: "Why is the api route slow today?", at: t0 - 3 * 60 * MIN },
  {
    kind: "tools",
    id: "l2",
    tool: "metrics",
    steps: [
      { id: "lt1", tool: "metrics", label: "Queried p95 latency for `api`", status: "ok", durationMs: 510 },
      { id: "lt2", tool: "health", label: "Checked 4 backends of `api`", status: "ok", durationMs: 220 },
    ],
  },
  {
    kind: "assistant",
    id: "l3",
    text: "p95 latency on **api** went from 140 ms to 610 ms at 09:12. One backend, `10.0.0.7:8080`, is answering slowly (2.1 s average) while the other three are normal.\n\nOptions:\n1. Drain `10.0.0.7:8080` until it recovers.\n2. Lower its weight to 1 so it gets little traffic.\n\nI'd drain it. It's reversible, and the other three backends have headroom.",
    at: t0 - 3 * 60 * MIN + MIN,
  },
];

state.transcripts["s-tls"] = [
  { kind: "user", id: "c1", text: "Rotate the TLS certificates for all https routes.", at: t0 - 26 * 60 * MIN },
  { kind: "assistant", id: "c2", text: "Done. Rotated 3 certificates; the next expiry is in 89 days.", at: t0 - 26 * 60 * MIN + 4 * MIN },
];

state.transcripts["s-deps"] = [
  { kind: "user", id: "d1", text: "Audit the dashboard's npm dependencies.", at: t0 - 50 * 60 * MIN },
  {
    kind: "tools",
    id: "d2",
    tool: "run_terminal",
    steps: [
      {
        id: "dt1",
        tool: "run_terminal",
        label: "Ran `npm audit --json`",
        status: "failed",
        durationMs: 30_000,
        detail: "npm error code ETIMEDOUT\nnpm error network request to https://registry.npmjs.org failed",
      },
    ],
  },
  { kind: "status", id: "d3", text: "Stopped: the npm registry did not respond (timeout after 30 s).", tone: "danger" },
];

// ---------------------------------------------------------------- events
const listeners = new Set<() => void>();
function emit() {
  for (const l of listeners) l();
}

function patchTranscript(id: string, fn: (items: TranscriptItem[]) => TranscriptItem[]) {
  state.transcripts[id] = fn(state.transcripts[id] ?? []);
  emit();
}

function patchSession(id: string, patch: Partial<SessionSummary>) {
  state.sessions = state.sessions.map((s) => (s.id === id ? { ...s, ...patch, updatedAt: now() } : s));
}

// A pending approval expires after 10 minutes, as on the server.
setInterval(() => {
  let changed = false;
  for (const a of Object.values(state.approvals)) {
    if (a.state === "pending" && a.expiresAt <= now()) {
      state.approvals[a.id] = { ...a, state: "expired" };
      patchSession(a.sessionId, { status: "failed" });
      changed = true;
    }
  }
  if (changed) emit();
}, 1000);

// Shows the "decided elsewhere" path: a second approval, created shortly
// after load, is approved in Telegram 25 s later.
setTimeout(() => {
  const id = "a-2";
  state.approvals[id] = {
    id,
    sessionId: "s-health",
    tool: "write_file",
    risk: "MEDIUM RISK",
    description: "Save the weekly report",
    preview: "+ docs/reports/2026-W45-route-health.md (new file, 84 lines)",
    workspace: "~/gubern",
    requestedBy: "Run “Weekly route health review”",
    expiresAt: now() + 10 * MIN,
    state: "pending",
  };
  patchSession("s-health", { status: "needs-approval" });
  patchTranscript("s-health", (items) => [...items, { kind: "approval", id: nextId("m"), approvalId: id }]);
  setTimeout(() => {
    const a = state.approvals[id];
    if (a?.state !== "pending") return;
    state.approvals[id] = { ...a, state: "approved", decidedBy: "@sandy", decidedVia: "telegram", decidedAt: now() };
    patchSession("s-health", { status: "running" });
    emit();
  }, 25_000);
}, 8_000);

// ---------------------------------------------------------------- replies
function streamText(sessionId: string, text: string, done: () => void) {
  const id = nextId("m");
  patchTranscript(sessionId, (items) => [...items, { kind: "assistant", id, text: "", at: now(), streaming: true }]);
  const words = text.split(/(\s+)/);
  let i = 0;
  const timer = setInterval(() => {
    if (!state.running.has(sessionId)) {
      clearInterval(timer);
      patchTranscript(sessionId, (items) =>
        items.map((m) => (m.kind === "assistant" && m.id === id ? { ...m, streaming: false, text: m.text + " …(stopped)" } : m)),
      );
      return;
    }
    const chunk = words.slice(i, i + 3).join("");
    i += 3;
    patchTranscript(sessionId, (items) => items.map((m) => (m.kind === "assistant" && m.id === id ? { ...m, text: m.text + chunk } : m)));
    if (i >= words.length) {
      clearInterval(timer);
      patchTranscript(sessionId, (items) => items.map((m) => (m.kind === "assistant" && m.id === id ? { ...m, streaming: false } : m)));
      done();
    }
  }, 45);
}

function runTools(sessionId: string, tool: string, steps: Omit<ToolStep, "status">[], then: () => void) {
  const id = nextId("m");
  patchTranscript(sessionId, (items) => [...items, { kind: "tools", id, tool, steps: [] }]);
  let i = 0;
  const tick = () => {
    if (!state.running.has(sessionId)) return;
    if (i >= steps.length) return then();
    const step: ToolStep = { ...steps[i], status: "running", startedAt: now() };
    patchTranscript(sessionId, (items) => items.map((m) => (m.kind === "tools" && m.id === id ? { ...m, steps: [...m.steps, step] } : m)));
    setTimeout(
      () => {
        patchTranscript(sessionId, (items) =>
          items.map((m) => (m.kind === "tools" && m.id === id ? { ...m, steps: m.steps.map((s) => (s.id === step.id ? { ...s, status: "ok" } : s)) } : m)),
        );
        i++;
        tick();
      },
      500 + Math.round(Math.random() * 500),
    );
  };
  tick();
}

function finish(sessionId: string, status: SessionSummary["status"] = "done") {
  state.running.delete(sessionId);
  patchSession(sessionId, { status });
  state.spendToday = Math.round((state.spendToday + 0.03) * 100) / 100;
  // Deliver any message queued while the agent was busy.
  const queued = (state.transcripts[sessionId] ?? []).find((m) => m.kind === "user" && m.queued);
  if (queued && queued.kind === "user") {
    patchTranscript(sessionId, (items) => items.map((m) => (m.id === queued.id ? { ...m, queued: false } : m)));
    respond(sessionId, queued.text);
  }
  emit();
}

function respond(sessionId: string, text: string) {
  state.running.add(sessionId);
  patchSession(sessionId, { status: "running" });
  const lower = text.toLowerCase();
  if (lower === "set up backups") {
    runTools(
      sessionId,
      "read",
      [
        { id: nextId("t"), tool: "read_file", label: "Read `gubern.cue`" },
        { id: nextId("t"), tool: "list_directory", label: "Checked `~/gubern-backups` (does not exist yet)" },
      ],
      () =>
        streamText(
          sessionId,
          "Here's the plan: a nightly automation at **04:00** that copies the config, secrets, audit log, agent memory and conversation history into `~/gubern-backups/<date>/`, keeps the last 14 copies, and tells you on Telegram if a run fails.\n\nCreating the automation writes `automations/nightly-backup.cue`, so it needs your approval.",
          () => {
            const id = nextId("a");
            state.approvals[id] = {
              id,
              sessionId,
              tool: "write_file",
              risk: "MEDIUM RISK",
              description: "Create the nightly backup automation",
              preview: "+ automations/nightly-backup.cue (new file, 22 lines)",
              workspace: "~/gubern",
              requestedBy: "Chat “Set up nightly backups”",
              expiresAt: now() + 10 * MIN,
              state: "pending",
            };
            patchTranscript(sessionId, (items) => [...items, { kind: "approval", id: nextId("m"), approvalId: id }]);
            finish(sessionId, "needs-approval");
          },
        ),
    );
    return;
  }
  if (/(install|deploy|restart|delete|rm )/.test(lower)) {
    runTools(
      sessionId,
      "read",
      [
        { id: nextId("t"), tool: "read_file", label: "Read `gubern.cue`" },
        { id: nextId("t"), tool: "git_status", label: "Checked `git status` (clean)" },
      ],
      () =>
        streamText(sessionId, "I can do that. It runs a command on this machine, so it needs your approval first.", () => {
          const id = nextId("a");
          state.approvals[id] = {
            id,
            sessionId,
            tool: "run_terminal",
            risk: "HIGH RISK",
            description: "Run the requested command",
            command: text.replace(/^please\s+/i, ""),
            workspace: "~/gubern",
            requestedBy: "This chat",
            expiresAt: now() + 10 * MIN,
            state: "pending",
          };
          patchTranscript(sessionId, (items) => [...items, { kind: "approval", id: nextId("m"), approvalId: id }]);
          finish(sessionId, "needs-approval");
        }),
    );
    return;
  }
  runTools(
    sessionId,
    "metrics",
    [
      { id: nextId("t"), tool: "status", label: "Read system status (12 routes, 3 nodes)" },
      { id: nextId("t"), tool: "metrics", label: "Queried error rate for the last hour" },
    ],
    () =>
      streamText(
        sessionId,
        "Everything is healthy apart from one thing: the **api** route has one slow backend, `10.0.0.7:8080`, answering in about 2 s while the others answer in under 150 ms.\n\nI'd drain that backend until it recovers. Want me to prepare that change for your review?",
        () => finish(sessionId),
      ),
  );
}

function titleFrom(text: string) {
  const t = text.trim().replace(/\s+/g, " ");
  return t.length > 48 ? t.slice(0, 47) + "…" : t;
}

// ---------------------------------------------------------------- client
const delay = <T>(v: T, ms = 220) => new Promise<T>((r) => setTimeout(() => r(structuredClone(v)), ms));

function attentionItems(): AttentionItem[] {
  const items: AttentionItem[] = [];
  for (const a of Object.values(state.approvals)) {
    if (a.state !== "pending") continue;
    items.push({
      id: `att-${a.id}`,
      kind: "approval",
      title: `${a.tool} in ${a.workspace}`,
      detail: a.command ?? a.preview ?? a.description,
      severity: a.risk === "CRITICAL" ? "critical" : a.risk === "HIGH RISK" ? "high" : "medium",
      href: `/chat/${a.sessionId}?approval=${a.id}`,
      at: a.expiresAt - 10 * MIN,
      expiresAt: a.expiresAt,
    });
  }
  for (const q of Object.values(state.questions)) {
    if (Object.keys(q.answers).length >= q.questions.length) continue;
    const s = state.sessions.find((x) => x.id === q.sessionId);
    items.push({
      id: `att-${q.id}`,
      kind: "question",
      title: `Questions from “${s?.title ?? "agent"}”`,
      detail: `${q.questions.length - Object.keys(q.answers).length} unanswered`,
      severity: "medium",
      href: `/chat/${q.sessionId}`,
      at: s?.updatedAt ?? now(),
    });
  }
  items.push({
    id: "att-route-api",
    kind: "route",
    title: "Route api: 1 of 4 backends slow",
    detail: "10.0.0.7:8080 · p95 2.1 s · error rate 1.4%",
    severity: "high",
    href: "/operate/routes?route=api",
    at: now() - 40 * MIN,
  });
  const order = { critical: 0, high: 1, medium: 2, info: 3 };
  return items.sort((a, b) => order[a.severity] - order[b.severity] || b.at - a.at);
}

const mockSkills: Skill[] = [
  {
    name: "gubern-dev-checks",
    description: "How to check a change to the Gubern codebase before calling it done: formatting, vet, unit tests, the integration suite, and the dashboard.",
    scope: "project",
    path: ".agents/skills/gubern-dev-checks/SKILL.md",
    hash: "204ed8d9f93f8283d47e53b7bd4e2cefa80467372b89627cee3d490aaafa6aff",
    license: "Apache-2.0",
    allowedTools: ["run_terminal", "read_file"],
    enabled: true,
  },
  {
    name: "gubern-release-check",
    description: "Pre-release checklist: changelog, version bump, signed tags and the release workflow.",
    scope: "project",
    path: ".agents/skills/gubern-release-check/SKILL.md",
    hash: "ef8d729ffae4b1c0d2e3f405162738495a6b7c8d9e0f1a2b3c4d5e6f708192a3",
    allowedTools: [],
    enabled: false,
  },
];

export const mockClient: GubernClient = {
  whoami: () => delay<Identity>({ name: "admin", role: "admin", local: true }),
  summary: () =>
    delay<SystemSummary>({
      worst: "degraded",
      routes: 12,
      routesFailing: 1,
      nodes: 3,
      spendToday: state.spendToday,
      budgetDaily: 10,
      updatedAt: now(),
    }),
  sessions: () => delay([...state.sessions].sort((a, b) => b.updatedAt - a.updatedAt)),
  attention: () => delay(attentionItems()),
  outputs: () => delay(state.outputs),
  models: () =>
    delay<ModelInfo[]>([
      { id: "claude-opus-5-5", label: "Opus 5.5", purpose: "Complex work and long agent runs", provider: "Anthropic" },
      { id: "claude-sonnet-5-5", label: "Sonnet 5.5", purpose: "Everyday work, fast and capable", provider: "Anthropic" },
      { id: "claude-haiku-5-5", label: "Haiku 5.5", purpose: "Quick answers and classification", provider: "Anthropic" },
      { id: "llama3.1:8b", label: "Llama 3.1 8B", purpose: "Runs locally through Ollama; no cost", provider: "Ollama" },
    ]),
  workspaces: () => delay(["~/gubern", "~/projects/app", "~/projects/site"]),
  skills: () => delay({ skills: [...mockSkills], problems: [{ path: ".agents/skills/draft/SKILL.md", error: "frontmatter: name is required" }] }),
  skill: (name) => {
    const s = mockSkills.find((x) => x.name === name);
    return s ? delay({ skill: s, body: `# ${s.name}

${s.description}

1. Read the change.
2. Run the checks.` }) : Promise.reject(new Error("skill not found"));
  },
  setSkillEnabled: (name, enabled) => {
    const i = mockSkills.findIndex((x) => x.name === name);
    if (i < 0) return Promise.reject(new Error("skill not found"));
    mockSkills[i] = { ...mockSkills[i], enabled };
    emit();
    return delay(mockSkills[i]);
  },

  transcript: (id) => state.transcripts[id] ?? EMPTY,
  approval: (id) => state.approvals[id],
  questions: (sessionId) => Object.values(state.questions).find((q) => q.sessionId === sessionId),
  plan: (sessionId) => state.plans[sessionId],
  isRunning: (sessionId) => state.running.has(sessionId),
  subscribe(listener) {
    listeners.add(listener);
    return () => listeners.delete(listener);
  },

  async send(sessionId, text, opts: SendOptions) {
    let id = sessionId;
    if (!id) {
      id = nextId("s");
      state.sessions = [
        { id, title: titleFrom(text), kind: opts.mode === "team" ? "team" : opts.mode === "chat" ? "chat" : "agent", status: "running", updatedAt: now() },
        ...state.sessions,
      ];
    }
    const busy = state.running.has(id);
    patchTranscript(id, (items) => [...items, { kind: "user", id: nextId("m"), text, at: now(), queued: busy }]);
    if (!busy) respond(id, text);
    return id;
  },
  stop(sessionId) {
    state.running.delete(sessionId);
    patchSession(sessionId, { status: "done" });
    patchTranscript(sessionId, (items) => [...items, { kind: "status", id: nextId("m"), text: "Stopped by you.", tone: "warning" }]);
  },
  async decide(approvalId, approve) {
    await delay(null, 350);
    const a = state.approvals[approvalId];
    if (!a || a.state !== "pending") return;
    state.approvals[approvalId] = { ...a, state: approve ? "approved" : "rejected", decidedBy: "you", decidedVia: "dashboard", decidedAt: now() };
    if (approve) {
      state.running.add(a.sessionId);
      patchSession(a.sessionId, { status: "running" });
      runTools(a.sessionId, "run_terminal", [{ id: nextId("t"), tool: "run_terminal", label: `Ran \`${a.command ?? a.tool}\``, durationMs: 4100 }], () =>
        streamText(a.sessionId, "Done. The command finished without errors.", () => finish(a.sessionId)),
      );
    } else {
      patchSession(a.sessionId, { status: "done" });
      patchTranscript(a.sessionId, (items) => [...items, { kind: "status", id: nextId("m"), text: "Rejected. Nothing was run.", tone: "warning" }]);
    }
    emit();
  },
  answer(setId, key, value) {
    const q = state.questions[setId];
    if (!q) return;
    state.questions[setId] = { ...q, answers: { ...q.answers, [key]: value } };
    const done = Object.keys(state.questions[setId].answers).length >= q.questions.length;
    if (done) {
      const pairs = q.questions.map((qq) => ({
        label: qq.title.replace(/\?$/, ""),
        value: qq.options.find((o) => o.value === state.questions[setId].answers[qq.key])?.label ?? state.questions[setId].answers[qq.key],
      }));
      patchTranscript(q.sessionId, (items) => [...items.filter((m) => m.kind !== "status"), { kind: "answers", id: nextId("m"), pairs }]);
      respond(q.sessionId, "set up backups");
      return;
    }
    emit();
  },
  skipQuestion(setId, key) {
    this.answer(setId, key, "(skipped)");
  },
  rename(sessionId, title) {
    patchSession(sessionId, { title });
    emit();
  },
  setPinned(sessionId, pinned) {
    patchSession(sessionId, { pinned });
    emit();
  },
  remove(sessionId) {
    state.sessions = state.sessions.filter((s) => s.id !== sessionId);
    emit();
  },
};

const EMPTY: TranscriptItem[] = [];
