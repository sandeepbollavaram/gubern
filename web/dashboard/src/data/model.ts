// Data model for the new dashboard (docs/design/dashboard-ux-spec.md). Screens
// depend only on these types and on GubernClient; Phase 1 serves them from an
// in-memory mock, and Phase 2 swaps in the HTTP/SSE client without changing any
// screen.

export type Role = "admin" | "operator" | "viewer";
export type Risk = "LOW RISK" | "MEDIUM RISK" | "HIGH RISK" | "CRITICAL";
export type RunStatus = "running" | "needs-approval" | "needs-answer" | "failed" | "done";
export type SessionKind = "chat" | "agent" | "team" | "run";
export type Mode = "chat" | "agent" | "team" | "research";
export type ViewMode = "normal" | "thinking" | "verbose";

export interface Identity {
  name: string;
  role: Role;
  /** Signed in automatically because the browser is on the server itself. */
  local: boolean;
  /** Scopes the principal holds (GET /api/me); absent in the mock. */
  scopes?: string[];
  /** "person" or "service". */
  kind?: string;
}

export interface SystemSummary {
  worst: "ok" | "degraded" | "down";
  routes: number;
  routesFailing: number;
  nodes: number;
  spendToday: number;
  budgetDaily: number;
  updatedAt: number;
}

export interface SessionSummary {
  id: string;
  title: string;
  kind: SessionKind;
  status: RunStatus;
  updatedAt: number;
  pinned?: boolean;
  unread?: boolean;
}

export interface ToolStep {
  id: string;
  /** Tool name as the agent calls it, e.g. "run_terminal". */
  tool: string;
  /** Plain-language line: "Ran `go test ./...`". */
  label: string;
  status: "running" | "ok" | "failed";
  durationMs?: number;
  /** When a running step started, for its elapsed timer. */
  startedAt?: number;
  /** Raw output shown when the step is expanded. */
  detail?: string;
}

export interface Approval {
  id: string;
  sessionId: string;
  tool: string;
  risk: Risk;
  description: string;
  command?: string;
  preview?: string;
  workspace: string;
  requestedBy: string;
  expiresAt: number;
  state: "pending" | "approved" | "rejected" | "expired";
  decidedBy?: string;
  decidedVia?: "dashboard" | "telegram" | "slack";
  decidedAt?: number;
}

/** An Agent Skill (SKILL.md) in the workspace; knowledge, never a grant. */
export interface Skill {
  name: string;
  description: string;
  /** Where it was found: "project" or "user". */
  scope: string;
  /** SKILL.md path, relative to the workspace. */
  path: string;
  /** SHA-256 of SKILL.md. */
  hash: string;
  license?: string;
  compatibility?: string;
  /** Tools the skill asks for; policy may narrow them, never widen. */
  allowedTools: string[];
  enabled: boolean;
  /** Installed through an approved install and recorded in skills.lock. */
  pinned?: boolean;
  /** Pinned, but its files changed since approval: not offered to agents. */
  tampered?: boolean;
  source?: string;
  /** From the Gubern sidecar (agents/gubern.yaml). */
  displayName?: string;
  tags?: string[];
  evals?: string;
  sidecarError?: string;
}

/** A fetched skill waiting for someone to approve installing it. */
export interface SkillInstall {
  approvalId: string;
  /** The install's own session; its approval shows there. */
  sessionId: string;
  source: string;
  treeHash: string;
  skill: Skill;
  files: { path: string; size: number; sha256: string }[];
}

export interface SkillList {
  skills: Skill[];
  /** Skills that failed to load, with the reason. */
  problems: { path: string; error: string }[];
}

export type TranscriptItem =
  | { kind: "user"; id: string; text: string; at: number; queued?: boolean }
  | { kind: "assistant"; id: string; text: string; at: number; streaming?: boolean }
  | { kind: "tools"; id: string; tool: string; steps: ToolStep[] }
  | { kind: "approval"; id: string; approvalId: string }
  | { kind: "skill"; id: string; name: string; hash: string }
  /** The server stopped during this run; it can be resumed from its checkpoint. */
  | { kind: "interrupted"; id: string; runId: string }
  | { kind: "status"; id: string; text: string; tone: "ai" | "warning" | "danger" }
  | { kind: "answers"; id: string; pairs: { label: string; value: string }[] };

export interface QuestionOption {
  value: string;
  label: string;
  hint?: string;
}

export interface Question {
  key: string;
  title: string;
  options: QuestionOption[];
}

export interface QuestionSet {
  id: string;
  sessionId: string;
  questions: Question[];
  answers: Record<string, string>;
}

export interface PlanStep {
  id: string;
  title: string;
  status: "done" | "current" | "todo" | "failed";
}

export interface Output {
  id: string;
  title: string;
  type: "report" | "build" | "config" | "file";
  at: number;
  sessionId?: string;
}

export interface RunPlan {
  sessionId: string;
  steps: PlanStep[];
  outputs: Output[];
  used: { label: string; value: string }[];
}

export type AttentionKind = "approval" | "question" | "route" | "node" | "budget" | "checkpoint";

export interface AttentionItem {
  id: string;
  kind: AttentionKind;
  title: string;
  detail: string;
  /** Severity used for ordering and the leading icon. */
  severity: "critical" | "high" | "medium" | "info";
  /** Where "Review" / "Open" goes, relative to the app root. */
  href: string;
  at: number;
  /** For approvals: deadline, to show a countdown. */
  expiresAt?: number;
}

export interface SendOptions {
  mode: Mode;
  model: string;
  workspace: string;
  /** Agent mode: a command that must pass before the run counts as done. */
  check?: string;
}

export interface ModelInfo {
  id: string;
  label: string;
  purpose: string;
  provider: string;
}

// GubernClient is the only thing screens talk to.
export interface GubernClient {
  whoami(): Promise<Identity>;
  summary(): Promise<SystemSummary>;
  sessions(): Promise<SessionSummary[]>;
  attention(): Promise<AttentionItem[]>;
  outputs(): Promise<Output[]>;
  models(): Promise<ModelInfo[]>;
  workspaces(): Promise<string[]>;
  /** Adds a folder as a workspace (workspaces:manage); returns its name. */
  addWorkspace(path: string): Promise<string>;
  skills(): Promise<SkillList>;
  skill(name: string): Promise<{ skill: Skill; body: string }>;
  setSkillEnabled(name: string, enabled: boolean): Promise<Skill>;
  installSkill(source: string): Promise<SkillInstall>;

  /** Live views: synchronous snapshot plus change subscription. */
  transcript(sessionId: string): TranscriptItem[];
  approval(approvalId: string): Approval | undefined;
  questions(sessionId: string): QuestionSet | undefined;
  plan(sessionId: string): RunPlan | undefined;
  isRunning(sessionId: string): boolean;
  subscribe(listener: () => void): () => void;

  /** Starts a session when sessionId is undefined; returns its id. */
  send(sessionId: string | undefined, text: string, opts: SendOptions): Promise<string>;
  stop(sessionId: string): void;
  /** Continues an interrupted run from its last checkpoint (Phase 3). */
  resumeRun(sessionId: string, runId: string): Promise<void>;
  decide(approvalId: string, approve: boolean): Promise<void>;
  answer(setId: string, key: string, value: string): void;
  skipQuestion(setId: string, key: string): void;
  rename(sessionId: string, title: string): void;
  setPinned(sessionId: string, pinned: boolean): void;
  remove(sessionId: string): void;
}
