import { useMemo, useState } from "react";
import type { ReactNode } from "react";
import { Bell, Bot, Cpu, KeyRound, MessagesSquare, Palette, Search, Shield, SlidersHorizontal, Wallet, X } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useStatus, useHealth } from "../lib/hooks";
import { reloadConfig } from "../lib/api";
import { AuditLog } from "../pages/AuditLog";
import { Secrets } from "../pages/Secrets";
import { useUIStore, type SettingsSection } from "../store";
import { Button, Dialog, Segmented, SettingRow, Toggle } from "../ui/primitives";
import { cx, relative } from "../ui/util";

const NAV: { id: SettingsSection; label: string; icon: LucideIcon; group: number }[] = [
  { id: "access", label: "Account and access", icon: KeyRound, group: 0 },
  { id: "general", label: "Appearance", icon: Palette, group: 1 },
  { id: "agent", label: "Agent", icon: Bot, group: 1 },
  { id: "models", label: "Models and providers", icon: Cpu, group: 1 },
  { id: "channels", label: "Channels", icon: MessagesSquare, group: 1 },
  { id: "notifications", label: "Notifications", icon: Bell, group: 1 },
  { id: "budget", label: "Budget and usage", icon: Wallet, group: 1 },
  { id: "security", label: "Security", icon: Shield, group: 2 },
  { id: "advanced", label: "Advanced", icon: SlidersHorizontal, group: 2 },
];

function Preview() {
  return (
    <span
      className="ml-2 rounded-full border border-ai/40 bg-ai/10 px-2 py-0.5 align-middle text-[11px] font-medium text-ai-foreground"
      title="This section shows simulated data until it is wired to the API (Phase 2)."
    >
      Preview data
    </span>
  );
}

function Section({ title, preview, children }: { title: string; preview?: boolean; children: ReactNode }) {
  return (
    <section className="mb-8">
      <h3 className="mb-1 text-[15px] font-semibold">
        {title}
        {preview && <Preview />}
      </h3>
      <div>{children}</div>
    </section>
  );
}

function ConfirmButton({
  label,
  confirmLabel,
  onConfirm,
  variant = "secondary",
}: {
  label: string;
  confirmLabel: string;
  onConfirm: () => Promise<void> | void;
  variant?: "secondary" | "danger";
}) {
  const [armed, setArmed] = useState(false);
  const [busy, setBusy] = useState(false);
  return armed ? (
    <span className="flex gap-2">
      <Button size="sm" variant="ghost" onClick={() => setArmed(false)}>
        Cancel
      </Button>
      <Button
        size="sm"
        variant={variant === "danger" ? "danger" : "primary"}
        busy={busy}
        onClick={async () => {
          setBusy(true);
          try {
            await onConfirm();
          } finally {
            setBusy(false);
            setArmed(false);
          }
        }}
      >
        {confirmLabel}
      </Button>
    </span>
  ) : (
    <Button size="sm" variant={variant} onClick={() => setArmed(true)}>
      {label}
    </Button>
  );
}

const MOCK_KEYS = [
  { id: "f65fe6616cfcced3", name: "tui-setup-key", role: "admin", created: Date.now() - 40 * 86_400_000, used: Date.now() - 60_000, current: true },
  { id: "9a2c0e11b7d04f52", name: "ci-deploy", role: "operator", created: Date.now() - 12 * 86_400_000, used: Date.now() - 3 * 3_600_000 },
  { id: "1d77a0c3e95b2a08", name: "grafana-readonly", role: "viewer", created: Date.now() - 90 * 86_400_000, used: Date.now() - 26 * 3_600_000 },
];

function AccessSection() {
  const toast = useUIStore((s) => s.toast);
  const [keys, setKeys] = useState(MOCK_KEYS);
  return (
    <Section title="API keys" preview>
      <p className="mb-3 text-[13px] text-muted">Each key has a role. A revoked key stops working at once, and stays revoked after a restart.</p>
      <div className="overflow-x-auto rounded-lg border border-border">
        <table className="w-full text-sm">
          <thead className="bg-surface-2 text-left text-[12px] text-muted">
            <tr>
              <th className="px-3 py-2 font-medium">Name</th>
              <th className="px-3 py-2 font-medium">Role</th>
              <th className="px-3 py-2 font-medium">Created</th>
              <th className="px-3 py-2 font-medium">Last used</th>
              <th className="px-3 py-2" />
            </tr>
          </thead>
          <tbody>
            {keys.map((k) => (
              <tr key={k.id} className="border-t border-border">
                <td className="px-3 py-2">
                  <span className="font-medium">{k.name}</span>
                  {k.current && <span className="ml-2 rounded-full bg-primary/15 px-1.5 py-0.5 text-[11px] text-primary">Current</span>}
                  <span className="block font-mono text-[11px] text-muted">{k.id}</span>
                </td>
                <td className="px-3 py-2 capitalize">{k.role}</td>
                <td className="px-3 py-2 text-muted">{relative(k.created)}</td>
                <td className="px-3 py-2 text-muted">{relative(k.used)}</td>
                <td className="px-3 py-2 text-right">
                  {k.current ? (
                    <span className="text-[12px] text-muted">This session</span>
                  ) : (
                    <ConfirmButton
                      label="Revoke"
                      confirmLabel={`Revoke ${k.name}`}
                      variant="danger"
                      onConfirm={() => {
                        setKeys((ks) => ks.filter((x) => x.id !== k.id));
                        toast({ text: `Revoked ${k.name}`, tone: "success" });
                      }}
                    />
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="mt-3">
        <Button size="sm" variant="primary" onClick={() => toast({ text: "Issuing keys is wired in Phase 2 (POST /api/keys).", tone: "info" })}>
          Issue a key
        </Button>
      </div>
    </Section>
  );
}

function GeneralSection() {
  const theme = useUIStore((s) => s.theme);
  const setPref = useUIStore((s) => s.setPref);
  return (
    <Section title="Display">
      <SettingRow title="Theme" description="System follows your operating system setting.">
        <Segmented
          label="Theme"
          value={theme}
          onChange={(v) => setPref("theme", v)}
          options={[
            { value: "system", label: "System" },
            { value: "light", label: "Light" },
            { value: "dark", label: "Dark" },
          ]}
        />
      </SettingRow>
      <SettingRow title="Motion" description="Animations follow your system's reduced-motion setting.">
        <span className="text-[13px] text-muted">System</span>
      </SettingRow>
    </Section>
  );
}

function AgentSection() {
  const viewMode = useUIStore((s) => s.viewMode);
  const setPref = useUIStore((s) => s.setPref);
  const [policy, setPolicy] = useState<"manual" | "auto">("manual");
  return (
    <>
      <Section title="Approvals" preview>
        <SettingRow
          title="Approval policy"
          description={
            policy === "manual"
              ? "Manual: the agent asks before every file change and command."
              : "Auto: routine file changes inside the workspace root run without asking. Commands, git hooks, .git and gubern.cue always ask."
          }
        >
          <Segmented
            label="Approval policy"
            value={policy}
            onChange={setPolicy}
            options={[
              { value: "manual", label: "Manual" },
              { value: "auto", label: "Auto in root" },
            ]}
          />
        </SettingRow>
        <SettingRow title="Workspace root" description="Agents can only read and write inside this folder.">
          <code className="rounded bg-surface-2 px-2 py-1 font-mono text-[13px]">~/gubern</code>
        </SettingRow>
      </Section>
      <Section title="Conversation">
        <SettingRow title="Default activity view" description="How much of the agent's tool use new sessions show.">
          <Segmented
            label="Default activity view"
            value={viewMode}
            onChange={(v) => setPref("viewMode", v)}
            options={[
              { value: "normal", label: "Normal" },
              { value: "thinking", label: "Thinking" },
              { value: "verbose", label: "Verbose" },
            ]}
          />
        </SettingRow>
      </Section>
    </>
  );
}

function ModelsSection() {
  const rows = [
    { name: "Anthropic", detail: "4 models · key set", ok: true },
    { name: "OpenAI", detail: "Not configured", ok: false },
    { name: "Ollama (local)", detail: "http://127.0.0.1:11434 · 2 models", ok: true },
  ];
  return (
    <Section title="Providers" preview>
      {rows.map((r) => (
        <SettingRow key={r.name} title={r.name} description={r.detail}>
          {r.ok ? <span className="text-[13px] text-success">✓ Connected</span> : <Button size="sm">Connect</Button>}
        </SettingRow>
      ))}
    </Section>
  );
}

function ChannelsSection() {
  const rows = [
    { name: "Telegram", detail: "Bot @gubern_ops_bot · 2 registered users", ok: true },
    { name: "Slack", detail: "Workspace T01ABC · 1 operator, 3 read-only · 2 refused attempts today", ok: true },
    { name: "WhatsApp", detail: "Not configured", ok: false },
  ];
  return (
    <Section title="Channels" preview>
      <p className="mb-2 text-[13px] text-muted">
        Only registered users can command Gubern from a channel. Unregistered people can run whoami to get their IDs.
      </p>
      {rows.map((r) => (
        <SettingRow key={r.name} title={r.name} description={r.detail}>
          {r.ok ? <Button size="sm">Manage users</Button> : <Button size="sm">Set up</Button>}
        </SettingRow>
      ))}
    </Section>
  );
}

function NotificationsSection() {
  const events = ["Approval needed", "Agent run finished", "Route down", "Certificate expiring", "Budget at 80%"];
  const channels = ["Browser", "Telegram", "Slack"];
  const [on, setOn] = useState<Record<string, boolean>>({
    "Approval needed|Browser": true,
    "Approval needed|Telegram": true,
    "Route down|Telegram": true,
    "Budget at 80%|Slack": true,
  });
  return (
    <Section title="Where each event goes" preview>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-[12px] text-muted">
              <th className="py-2 font-medium">Event</th>
              {channels.map((c) => (
                <th key={c} className="py-2 text-center font-medium">
                  {c}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {events.map((e) => (
              <tr key={e} className="border-t border-border">
                <td className="py-3">{e}</td>
                {channels.map((c) => (
                  <td key={c} className="py-3 text-center">
                    <Toggle label={`${e} via ${c}`} checked={!!on[`${e}|${c}`]} onChange={(v) => setOn((o) => ({ ...o, [`${e}|${c}`]: v }))} />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Section>
  );
}

function Meter({ label, used, limit, unit }: { label: string; used: number; limit: number; unit: string }) {
  const pct = Math.min(100, Math.round((used / limit) * 100));
  return (
    <div className="py-3">
      <div className="mb-1.5 flex justify-between text-sm">
        <span>{label}</span>
        <span className="tabular-nums text-muted">
          {unit}
          {used.toFixed(2)} of {unit}
          {limit} · {pct}%
        </span>
      </div>
      <div className="h-2 overflow-hidden rounded-full bg-surface-2" role="meter" aria-label={label} aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
        <div className={cx("h-full rounded-full", pct >= 80 ? "bg-warning" : "bg-primary")} style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

function BudgetSection() {
  return (
    <Section title="Spend" preview>
      <Meter label="Today" used={1.2} limit={10} unit="$" />
      <Meter label="This month" used={41.8} limit={50} unit="$" />
      <p className="mt-1 text-[13px] text-warning">At this pace you'll reach the monthly limit in about 2 days.</p>
    </Section>
  );
}

function SecuritySection() {
  return (
    <>
      <Section title="Secrets">
        <Secrets />
      </Section>
      <Section title="Audit log">
        <AuditLog />
      </Section>
    </>
  );
}

function AdvancedSection() {
  const toast = useUIStore((s) => s.toast);
  const { data: status } = useStatus();
  const { data: health } = useHealth();
  const [typed, setTyped] = useState("");
  const node = status?.node_id ?? "this server";
  return (
    <>
      <Section title="This server">
        <SettingRow title="Cluster" description="Name and node ID reported by the server.">
          <span className="font-mono text-[13px] text-muted">
            {status?.cluster_name ?? health?.cluster_name ?? "—"} · {node}
          </span>
        </SettingRow>
        <SettingRow title="Reload configuration" description="Re-reads gubern.cue and applies changes without dropping connections.">
          <ConfirmButton
            label="Reload"
            confirmLabel="Reload now"
            onConfirm={async () => {
              try {
                await reloadConfig();
                toast({ text: "Configuration reloaded.", tone: "success" });
              } catch (e) {
                toast({ text: `Reload failed: ${(e as Error).message}`, tone: "error" });
              }
            }}
          />
        </SettingRow>
      </Section>
      <section className="rounded-lg border border-danger/40 p-4">
        <h3 className="text-[15px] font-semibold text-danger">Danger zone</h3>
        <p className="mt-1 text-[13px] text-muted">
          Shutting down stops every route, agent run and channel on this server. Type <code className="font-mono text-foreground">{node}</code> to confirm.
        </p>
        <div className="mt-3 flex gap-2">
          <input
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            aria-label="Type the node ID to confirm shutdown"
            className="h-9 flex-1 rounded border border-border bg-surface-2 px-3 font-mono text-sm outline-none focus:border-danger"
          />
          <Button
            variant="danger"
            disabled={typed !== node}
            onClick={async () => {
              const res = await fetch("/internal/shutdown", { method: "POST" });
              toast({ text: res.ok ? "Shutdown started." : `Shutdown failed (${res.status}).`, tone: res.ok ? "success" : "error" });
              setTyped("");
            }}
          >
            Shut down
          </Button>
        </div>
      </section>
    </>
  );
}

const CONTENT: Record<SettingsSection, () => ReactNode> = {
  access: AccessSection,
  general: GeneralSection,
  agent: AgentSection,
  models: ModelsSection,
  channels: ChannelsSection,
  notifications: NotificationsSection,
  budget: BudgetSection,
  security: SecuritySection,
  advanced: AdvancedSection,
};

export function SettingsModal() {
  const section = useUIStore((s) => s.settings);
  const open = useUIStore((s) => s.openSettings);
  const close = useUIStore((s) => s.closeSettings);
  const [filter, setFilter] = useState("");
  const nav = useMemo(() => NAV.filter((n) => n.label.toLowerCase().includes(filter.toLowerCase())), [filter]);
  if (!section) return null;
  const Body = CONTENT[section];
  return (
    <Dialog open onClose={close} title="Settings" hideTitle className="flex h-[min(760px,86vh)] max-w-5xl">
      <div className="flex h-full w-full">
        <aside className="flex w-60 shrink-0 flex-col border-r border-border bg-rail p-3">
          <div className="mb-3 flex items-center justify-between px-1">
            <span className="text-[15px] font-semibold">Settings</span>
          </div>
          <label className="mb-2 flex h-9 items-center gap-2 rounded border border-border bg-surface px-2.5 text-sm focus-within:border-primary/60">
            <Search className="h-4 w-4 text-muted" aria-hidden />
            <input
              data-autofocus
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Search"
              aria-label="Search settings"
              className="w-full bg-transparent outline-none placeholder:text-muted focus-visible:outline-none"
            />
          </label>
          <nav aria-label="Settings sections" className="space-y-0.5">
            {nav.map((n, i) => (
              <div key={n.id}>
                {i > 0 && nav[i - 1].group !== n.group && <div className="my-2 border-t border-border" />}
                <button
                  type="button"
                  aria-current={section === n.id ? "page" : undefined}
                  onClick={() => open(n.id)}
                  className={cx(
                    "flex h-9 w-full cursor-pointer items-center gap-2.5 rounded px-2.5 text-sm",
                    section === n.id ? "bg-selected text-foreground" : "text-muted hover:bg-accent hover:text-foreground",
                  )}
                >
                  <n.icon className="h-4 w-4" aria-hidden />
                  {n.label}
                </button>
              </div>
            ))}
          </nav>
        </aside>
        <div className="relative flex-1 overflow-y-auto px-8 py-6">
          <button
            type="button"
            onClick={close}
            aria-label="Close settings"
            className="absolute right-4 top-4 flex h-8 w-8 cursor-pointer items-center justify-center rounded text-muted hover:bg-accent hover:text-foreground"
          >
            <X className="h-4 w-4" />
          </button>
          <h2 className="mb-6 text-xl font-semibold">{NAV.find((n) => n.id === section)?.label}</h2>
          <Body />
        </div>
      </div>
    </Dialog>
  );
}
