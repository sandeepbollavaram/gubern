// The real Gubern client: HTTP for reads and decisions, and the recorded,
// resumable event stream (milestone D) for runs. It implements the same
// GubernClient interface as the mock, so no screen changes. The server
// enforces every scope; this client only hides what the caller's scopes
// forbid (see useCan).
import { apiJSON, authFetch, HttpError } from "./auth";
import type {
  Approval,
  AttentionItem,
  GubernClient,
  Identity,
  ModelInfo,
  Output,
  Skill,
  SkillInstall,
  SkillList,
  Risk,
  SendOptions,
  SessionSummary,
  SystemSummary,
  ToolStep,
  TranscriptItem,
} from "./model";

// ---- wire types -------------------------------------------------------------

interface WirePending {
  session_id: string;
  action_id: string;
  action_hash?: string;
  tool: string;
  risk: string;
  description: string;
  command?: string;
  preview?: string;
  workspace?: string;
  created_at: string;
  expires_at: string;
}

interface WireApproval {
  id: string;
  session_id: string;
  tool: string;
  risk: string;
  description: string;
  command?: string;
  preview?: string;
  workspace?: string;
  action_hash: string;
  status: "pending" | "approved" | "rejected" | "expired" | "cancelled";
  expires_at: string;
  decided_at?: string;
  decided_by?: string;
  decided_via?: string;
}

export interface WireEvent {
  type: string;
  run_id?: string;
  seq?: number;
  session_id?: string;
  step_id?: string;
  tool?: string;
  label?: string;
  status?: string;
  duration_ms?: number;
  output?: string;
  approval?: WirePending;
  decided_via?: string;
  decided_by?: string;
  text?: string;
  skill_hash?: string;
  at?: number;
}

interface WireSkill {
  name: string;
  description: string;
  scope: string;
  path: string;
  hash: string;
  license?: string;
  compatibility?: string;
  allowed_tools?: string[];
  enabled: boolean;
  pinned?: boolean;
  tampered?: boolean;
  source?: string;
}

const skillFromWire = (s: WireSkill): Skill => ({
  name: s.name,
  description: s.description,
  scope: s.scope,
  path: s.path,
  hash: s.hash,
  license: s.license,
  compatibility: s.compatibility,
  allowedTools: s.allowed_tools ?? [],
  enabled: s.enabled,
  pinned: s.pinned,
  tampered: s.tampered,
  source: s.source,
});

interface WireRun {
  id: string;
  session_id: string;
  status: string;
  last_seq: number;
}

// ---- helpers ----------------------------------------------------------------

const risks: Risk[] = ["LOW RISK", "MEDIUM RISK", "HIGH RISK", "CRITICAL"];
const toRisk = (r: string): Risk => (risks.find((x) => r.toUpperCase().startsWith(x.split(" ")[0])) ?? "MEDIUM RISK");
const ms = (iso?: string) => (iso ? Date.parse(iso) : Date.now());
const viaLabel = (v?: string): Approval["decidedVia"] => (v === "telegram" || v === "slack" ? v : "dashboard");

function randomHex(bytes: number): string {
  const a = new Uint8Array(bytes);
  crypto.getRandomValues(a);
  return Array.from(a, (b) => b.toString(16).padStart(2, "0")).join("");
}

export const newSessionId = () => `s-${randomHex(8)}`;

/** Parses an SSE byte stream into events (id/event/data frames). */
export async function* readSSE(body: ReadableStream<Uint8Array>): AsyncGenerator<{ id?: string; event: string; data: string }> {
  const reader = body.getReader();
  const dec = new TextDecoder();
  let buf = "";
  let id: string | undefined;
  let event = "message";
  let data: string[] = [];
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return;
    buf += dec.decode(value, { stream: true });
    let nl: number;
    while ((nl = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, nl).replace(/\r$/, "");
      buf = buf.slice(nl + 1);
      if (line === "") {
        if (data.length) yield { id, event, data: data.join("\n") };
        id = undefined;
        event = "message";
        data = [];
      } else if (line.startsWith(":")) {
        // heartbeat
      } else if (line.startsWith("id:")) id = line.slice(3).trim();
      else if (line.startsWith("event:")) event = line.slice(6).trim();
      else if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
    }
  }
}

type FullApproval = Approval & { actionHash: string };

function approvalFromWire(a: WireApproval): FullApproval {
  return {
    id: a.id,
    sessionId: a.session_id,
    tool: a.tool,
    risk: toRisk(a.risk),
    description: a.description,
    command: a.command || undefined,
    preview: a.preview || undefined,
    workspace: a.workspace || "",
    requestedBy: `Session ${a.session_id}`,
    expiresAt: ms(a.expires_at),
    state: a.status === "cancelled" ? "expired" : a.status,
    decidedBy: a.decided_by || undefined,
    decidedVia: a.decided_by ? viaLabel(a.decided_via) : undefined,
    decidedAt: a.decided_at ? ms(a.decided_at) : undefined,
    actionHash: a.action_hash,
  };
}

function approvalFromPending(p: WirePending): FullApproval {
  return {
    id: p.action_id,
    sessionId: p.session_id,
    tool: p.tool,
    risk: toRisk(p.risk),
    description: p.description,
    command: p.command || undefined,
    preview: p.preview || undefined,
    workspace: p.workspace || "",
    requestedBy: `Session ${p.session_id}`,
    expiresAt: ms(p.expires_at),
    state: "pending",
    actionHash: p.action_hash ?? "",
  };
}

interface SessionPrefs {
  pinned: string[];
  removed: string[];
  titles: Record<string, string>;
}

const prefs = {
  read(): SessionPrefs {
    try {
      return { pinned: [], removed: [], titles: {}, ...JSON.parse(localStorage.getItem("gubern.sessionPrefs") ?? "{}") };
    } catch {
      return { pinned: [], removed: [], titles: {} };
    }
  },
  write(p: SessionPrefs) {
    try {
      localStorage.setItem("gubern.sessionPrefs", JSON.stringify(p));
    } catch {
      /* ignore */
    }
  },
};

// ---- client -----------------------------------------------------------------

const EMPTY: TranscriptItem[] = [];

class HttpClient implements GubernClient {
  private transcripts: Record<string, TranscriptItem[]> = {};
  private approvals: Record<string, FullApproval> = {};
  private running = new Set<string>();
  private runOf: Record<string, string> = {};
  private lastSeq: Record<string, number> = {};
  private hydrated = new Set<string>();
  private fetchingApproval = new Set<string>();
  private listeners = new Set<() => void>();
  private nextLocal = 0;

  // ---- change notification ----
  subscribe = (l: () => void) => {
    this.listeners.add(l);
    return () => {
      this.listeners.delete(l);
    };
  };
  private changed() {
    this.listeners.forEach((l) => l());
  }
  private setTranscript(sid: string, items: TranscriptItem[]) {
    this.transcripts[sid] = items;
    this.changed();
  }
  private push(sid: string, item: TranscriptItem) {
    this.setTranscript(sid, [...(this.transcripts[sid] ?? []), item]);
  }
  private localId(p: string) {
    return `${p}-${++this.nextLocal}`;
  }

  // ---- reads ----
  whoami = async (): Promise<Identity> => {
    const me = await apiJSON<{ name: string; kind: string; scopes: string[] }>("/api/me");
    const s = new Set(me.scopes);
    const role = s.has("identity:manage") ? "admin" : s.has("agents:run") ? "operator" : "viewer";
    return { name: me.name, role, local: false, scopes: me.scopes, kind: me.kind };
  };

  summary = async (): Promise<SystemSummary> => {
    const [status, cost] = await Promise.all([
      apiJSON<{ routes: number }>("/api/status").catch(() => ({ routes: 0 })),
      apiJSON<{ total_usd: number; daily_budget: number }>("/api/ai/cost").catch(() => ({ total_usd: 0, daily_budget: 0 })),
    ]);
    const health = await apiJSON<{ checks?: { healthy: boolean }[] }>("/api/healing/status").catch(() => ({ checks: [] as { healthy: boolean }[] }));
    const failing = (health.checks ?? []).filter((c) => !c.healthy).length;
    return {
      worst: failing > 0 ? "degraded" : "ok",
      routes: status.routes ?? 0,
      routesFailing: failing,
      nodes: 1,
      spendToday: cost.total_usd ?? 0,
      budgetDaily: cost.daily_budget ?? 0,
      updatedAt: Date.now(),
    };
  };

  sessions = async (): Promise<SessionSummary[]> => {
    const [hist, pending] = await Promise.all([
      apiJSON<{ sessions: { session_id: string; summary: string; updated_at: string }[] }>("/api/agents/history").catch(() => ({ sessions: [] })),
      apiJSON<{ approvals: WireApproval[] }>("/api/approvals?status=pending").catch(() => ({ approvals: [] })),
    ]);
    const p = prefs.read();
    const needs = new Set(pending.approvals.map((a) => a.session_id));
    const seen = new Set<string>();
    const out: SessionSummary[] = [];
    for (const s of hist.sessions) {
      if (p.removed.includes(s.session_id)) continue;
      seen.add(s.session_id);
      out.push({
        id: s.session_id,
        title: p.titles[s.session_id] || s.summary || s.session_id,
        kind: "chat",
        status: needs.has(s.session_id) ? "needs-approval" : this.running.has(s.session_id) ? "running" : "done",
        updatedAt: ms(s.updated_at),
        pinned: p.pinned.includes(s.session_id),
      });
    }
    // Sessions started in this tab that the server has not listed yet.
    for (const sid of Object.keys(this.transcripts)) {
      if (seen.has(sid) || p.removed.includes(sid)) continue;
      const first = this.transcripts[sid].find((i) => i.kind === "user");
      out.push({
        id: sid,
        title: p.titles[sid] || (first && first.kind === "user" ? first.text.slice(0, 60) : sid),
        kind: "agent",
        status: needs.has(sid) ? "needs-approval" : this.running.has(sid) ? "running" : "done",
        updatedAt: Date.now(),
        pinned: p.pinned.includes(sid),
      });
    }
    return out.sort((a, b) => b.updatedAt - a.updatedAt);
  };

  attention = async (): Promise<AttentionItem[]> => {
    const body = await apiJSON<{ items: { id: string; kind: string; severity: AttentionItem["severity"]; title: string; detail?: string; href: string; at: string; expires_at?: string }[] }>("/api/attention").catch(
      () => ({ items: [] }),
    );
    const kinds: Record<string, AttentionItem["kind"]> = { approval: "approval", checkpoint: "checkpoint", check: "node", slo: "route" };
    return body.items.map((i) => ({
      id: i.id,
      kind: kinds[i.kind] ?? "route",
      title: i.title,
      detail: i.detail ?? "",
      severity: i.severity,
      href: i.href,
      at: ms(i.at),
      expiresAt: i.expires_at ? ms(i.expires_at) : undefined,
    }));
  };

  outputs = async (): Promise<Output[]> => {
    const [jobs, reports] = await Promise.all([
      apiJSON<{ jobs: { id: string; message: string; state: string }[] }>("/api/forge/jobs").catch(() => ({ jobs: [] })),
      apiJSON<{ reports: { title: string; file_path: string; saved_at: string }[] }>("/api/research/reports").catch(() => ({ reports: [] })),
    ]);
    return [
      ...reports.reports.map((r) => ({ id: `report:${r.file_path}`, title: r.title, type: "report" as const, at: ms(r.saved_at) })),
      ...jobs.jobs.map((j) => ({ id: `job:${j.id}`, title: `Forge: ${j.message.slice(0, 60)} (${j.state})`, type: "build" as const, at: Date.now() })),
    ];
  };

  models = async (): Promise<ModelInfo[]> => {
    const body = await apiJSON<{ data: { id: string }[] }>("/v1/models").catch(() => ({ data: [] }));
    const list = body.data.map((m) => ({ id: m.id, label: m.id, purpose: "", provider: m.id.split(/[-/]/)[0] }));
    return list.length ? list : [{ id: "auto", label: "Auto", purpose: "The server picks the model", provider: "gubern" }];
  };

  workspaces = async (): Promise<string[]> => {
    const st = await apiJSON<{ working_dir?: string }>("/api/status").catch(() => ({ working_dir: "" }));
    return [st.working_dir || "default"];
  };

  skills = async (): Promise<SkillList> => {
    const b = await apiJSON<{ skills: WireSkill[]; problems: { path: string; error: string }[] }>("/api/skills");
    return { skills: b.skills.map(skillFromWire), problems: b.problems ?? [] };
  };

  skill = async (name: string) => {
    const b = await apiJSON<{ skill: WireSkill; body: string }>(`/api/skills/${encodeURIComponent(name)}`);
    return { skill: skillFromWire(b.skill), body: b.body };
  };

  setSkillEnabled = async (name: string, enabled: boolean): Promise<Skill> => {
    const b = await apiJSON<{ skill: WireSkill }>(`/api/skills/${encodeURIComponent(name)}/${enabled ? "enable" : "disable"}`, { method: "POST" });
    this.changed();
    return skillFromWire(b.skill);
  };

  installSkill = async (source: string): Promise<SkillInstall> => {
    const b = await apiJSON<{ approval_id: string; session_id: string; source: string; tree_hash: string; skill: WireSkill; files: { path: string; size: number; sha256: string }[] }>(
      "/api/skills/install",
      { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ source }) },
    );
    this.changed();
    return { approvalId: b.approval_id, sessionId: b.session_id, source: b.source, treeHash: b.tree_hash, skill: skillFromWire(b.skill), files: b.files };
  };

  // ---- live views ----
  transcript = (sid: string): TranscriptItem[] => {
    if (!this.hydrated.has(sid)) {
      this.hydrated.add(sid);
      void this.hydrate(sid);
    }
    return this.transcripts[sid] ?? EMPTY;
  };

  approval = (id: string): Approval | undefined => {
    const a = this.approvals[id];
    if (!a && !this.fetchingApproval.has(id)) {
      this.fetchingApproval.add(id);
      void apiJSON<WireApproval>(`/api/approvals/${encodeURIComponent(id)}`)
        .then((w) => {
          this.approvals[id] = approvalFromWire(w);
          this.changed();
        })
        .catch(() => undefined);
    }
    return a;
  };

  questions = () => undefined;
  plan = () => undefined;
  isRunning = (sid: string) => this.running.has(sid);

  private async hydrate(sid: string) {
    try {
      const hist = await apiJSON<{ messages: { role: string; content: string; timestamp: string }[] }>(`/api/agents/history/${encodeURIComponent(sid)}`);
      const items: TranscriptItem[] = hist.messages.map((m, i) =>
        m.role === "user" ? { kind: "user", id: `h-${i}`, text: m.content, at: ms(m.timestamp) } : { kind: "assistant", id: `h-${i}`, text: m.content, at: ms(m.timestamp) },
      );
      // Keep anything this tab added while history loaded.
      this.setTranscript(sid, [...items, ...(this.transcripts[sid] ?? [])]);
    } catch {
      /* new session, or no access: nothing to show */
    }
    try {
      const pend = await apiJSON<{ approvals: WireApproval[] }>(`/api/approvals?status=pending&session_id=${encodeURIComponent(sid)}`);
      for (const w of pend.approvals) {
        if (this.approvals[w.id]) continue;
        this.approvals[w.id] = approvalFromWire(w);
        this.push(sid, { kind: "approval", id: this.localId("ap"), approvalId: w.id });
      }
    } catch {
      /* ignore */
    }
    // Reattach to a run still going (e.g. after a page reload).
    try {
      const runs = await apiJSON<{ runs: WireRun[] }>(`/api/runs?session_id=${encodeURIComponent(sid)}&limit=1`);
      const run = runs.runs[0];
      if (run && run.status === "running" && !this.runOf[sid]) {
        this.runOf[sid] = run.id;
        this.running.add(sid);
        this.changed();
        void this.follow(sid, run.id);
      }
    } catch {
      /* ignore */
    }
  }

  // ---- event handling ----
  private apply(sid: string, ev: WireEvent) {
    if (ev.seq && ev.run_id) {
      if (ev.seq <= (this.lastSeq[ev.run_id] ?? 0)) return; // duplicate after a resume
      this.lastSeq[ev.run_id] = ev.seq;
    }
    const items = [...(this.transcripts[sid] ?? [])];
    const last = items[items.length - 1];
    switch (ev.type) {
      case "text": {
        if (last && last.kind === "assistant" && last.streaming) {
          items[items.length - 1] = { ...last, text: last.text + (ev.text ?? "") };
        } else {
          items.push({ kind: "assistant", id: this.localId("a"), text: ev.text ?? "", at: Date.now(), streaming: true });
        }
        break;
      }
      case "tool_start": {
        const step: ToolStep = { id: ev.step_id ?? this.localId("st"), tool: ev.tool ?? "tool", label: ev.label ?? ev.tool ?? "", status: "running", startedAt: Date.now() };
        if (last && last.kind === "tools") items[items.length - 1] = { ...last, steps: [...last.steps, step] };
        else items.push({ kind: "tools", id: this.localId("t"), tool: step.tool, steps: [step] });
        break;
      }
      case "tool_end": {
        for (let i = items.length - 1; i >= 0; i--) {
          const it = items[i];
          if (it.kind !== "tools") continue;
          const idx = it.steps.findIndex((s) => s.id === ev.step_id);
          if (idx < 0) continue;
          const steps = [...it.steps];
          steps[idx] = {
            ...steps[idx],
            status: ev.status === "failed" ? "failed" : ev.status === "waiting" ? "running" : "ok",
            durationMs: ev.duration_ms,
            detail: ev.output || steps[idx].detail,
          };
          items[i] = { ...it, steps };
          break;
        }
        break;
      }
      case "skill_activated": {
        if (ev.label) items.push({ kind: "skill", id: this.localId("sk"), name: ev.label, hash: ev.skill_hash ?? "" });
        break;
      }
      case "approval_required": {
        if (ev.approval) {
          const a = approvalFromPending({ ...ev.approval, session_id: ev.approval.session_id || sid });
          const known = this.approvals[a.id];
          if (!known) items.push({ kind: "approval", id: this.localId("ap"), approvalId: a.id });
          // A decision may have arrived first (another channel); keep it.
          if (!known || known.state === "pending") this.approvals[a.id] = a;
        }
        break;
      }
      case "approval_decided": {
        const id = ev.approval?.action_id;
        if (id && this.approvals[id]) {
          const st = ev.status === "approved" ? "approved" : ev.status === "rejected" ? "rejected" : "expired";
          this.approvals[id] = { ...this.approvals[id], state: st, decidedBy: ev.decided_by, decidedVia: viaLabel(ev.decided_via), decidedAt: Date.now() };
        }
        break;
      }
      case "run_finished": {
        for (let i = items.length - 1; i >= 0; i--) {
          const it = items[i];
          if (it.kind === "assistant" && it.streaming) items[i] = { ...it, streaming: false };
        }
        if (ev.status === "cancelled") items.push({ kind: "status", id: this.localId("s"), text: "Stopped.", tone: "warning" });
        if (ev.status === "failed") items.push({ kind: "status", id: this.localId("s"), text: "The run failed.", tone: "danger" });
        this.running.delete(sid);
        break;
      }
      default:
        return;
    }
    this.setTranscript(sid, items);
  }

  /** Consumes a run stream; on a dropped connection resumes from the last event. */
  private async consume(sid: string, runId: string, body: ReadableStream<Uint8Array>) {
    for await (const frame of readSSE(body)) {
      if (frame.event === "done") {
        this.running.delete(sid);
        this.changed();
        return true;
      }
      try {
        this.apply(sid, JSON.parse(frame.data) as WireEvent);
      } catch {
        /* malformed frame */
      }
    }
    void runId;
    return false;
  }

  private async follow(sid: string, runId: string) {
    let delay = 1000;
    while (this.running.has(sid) && this.runOf[sid] === runId) {
      try {
        const res = await authFetch(`/api/runs/${encodeURIComponent(runId)}/events`, {
          headers: { Accept: "text/event-stream", "Last-Event-ID": String(this.lastSeq[runId] ?? 0) },
        });
        if (res.status === 401 || res.status === 403 || res.status === 404) break;
        if (res.ok && res.body && (await this.consume(sid, runId, res.body))) return;
        delay = 1000;
      } catch {
        /* network drop: retry */
      }
      await new Promise((r) => setTimeout(r, delay));
      delay = Math.min(delay * 2, 10_000);
    }
    this.running.delete(sid);
    this.changed();
  }

  // ---- actions ----
  send = async (sessionId: string | undefined, text: string, opts: SendOptions): Promise<string> => {
    const sid = sessionId ?? newSessionId();
    const prefix = opts.mode === "agent" ? "/agent " : opts.mode === "team" ? "/team " : opts.mode === "research" ? "research " : "";
    this.hydrated.add(sid);
    this.push(sid, { kind: "user", id: this.localId("u"), text, at: Date.now() });
    this.running.add(sid);
    this.changed();
    const res = await authFetch("/api/agents/submit?events=1", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "text/event-stream", "Idempotency-Key": randomHex(12) },
      body: JSON.stringify({ message: prefix + text, session_id: sid }),
    }).catch(() => undefined);
    if (!res || !res.ok || !res.body) {
      this.running.delete(sid);
      const why = !res ? "The server could not be reached." : res.status === 403 ? "You do not have permission to run agents here." : `The server refused the request (${res.status}).`;
      this.push(sid, { kind: "status", id: this.localId("s"), text: why, tone: "danger" });
      return sid;
    }
    const runId = res.headers.get("X-Gubern-Run-Id") ?? "";
    this.runOf[sid] = runId;
    void this.consume(sid, runId, res.body).then((finished) => {
      if (!finished && runId) void this.follow(sid, runId);
    });
    return sid;
  };

  stop = (sid: string) => {
    const runId = this.runOf[sid];
    if (runId) void authFetch(`/api/runs/${encodeURIComponent(runId)}/cancel`, { method: "POST" });
  };

  decide = async (approvalId: string, approve: boolean): Promise<void> => {
    let a = this.approvals[approvalId];
    if (!a || !a.actionHash) {
      a = approvalFromWire(await apiJSON<WireApproval>(`/api/approvals/${encodeURIComponent(approvalId)}`));
    }
    try {
      const res = await apiJSON<{ approval: WireApproval }>(`/api/approvals/${encodeURIComponent(approvalId)}/decision`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ approved: approve, action_hash: a.actionHash, via: "dashboard" }),
      });
      this.approvals[approvalId] = approvalFromWire(res.approval);
      this.changed();
    } catch (e) {
      // Someone else decided first, it expired, or it changed: show the truth.
      try {
        this.approvals[approvalId] = approvalFromWire(await apiJSON<WireApproval>(`/api/approvals/${encodeURIComponent(approvalId)}`));
        this.changed();
      } catch {
        /* ignore */
      }
      if (e instanceof HttpError && e.status === 409) return;
      throw e;
    }
  };

  answer = () => undefined;
  skipQuestion = () => undefined;

  rename = (sid: string, title: string) => {
    const p = prefs.read();
    p.titles[sid] = title;
    prefs.write(p);
    this.changed();
  };
  setPinned = (sid: string, pinned: boolean) => {
    const p = prefs.read();
    p.pinned = pinned ? [...new Set([...p.pinned, sid])] : p.pinned.filter((x) => x !== sid);
    prefs.write(p);
    this.changed();
  };
  remove = (sid: string) => {
    const p = prefs.read();
    p.removed = [...new Set([...p.removed, sid])];
    prefs.write(p);
    this.changed();
  };
}

export const httpClient: GubernClient = new HttpClient();
