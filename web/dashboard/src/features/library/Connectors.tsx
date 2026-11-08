import { useState } from "react";
import {
  AlertTriangle,
  KeyRound,
  Plug,
  PlugZap,
  Power,
  RefreshCw,
  Trash2,
} from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { client, useCan, useConnectors } from "../../data/client";
import type {
  Connector,
  ConnectorInput,
  ConnectorTool,
  ToolPolicy,
} from "../../data/model";
import { useUIStore } from "../../store";
import {
  Button,
  Dialog,
  EmptyState,
  InlineError,
  Segmented,
  Skeleton,
} from "../../ui/primitives";
import { cx } from "../../ui/util";
import { PageHeader } from "./PageHeader";

// Connectors (Phase 5, Milestone N): external tools from MCP servers. A
// connector is a third-party tool provider: enabling it is an approval,
// every tool call asks for one unless you allow that tool, and nothing it
// says (descriptions, results) is treated as an instruction.

const stateTone: Record<Connector["status"]["state"], string> = {
  connected: "bg-success",
  connecting: "bg-warning",
  error: "bg-danger",
  disabled: "bg-muted",
};

export function Connectors() {
  const { data, isLoading, error, refetch } = useConnectors();
  const canManage = useCan("system:manage");
  const [adding, setAdding] = useState(false);
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-5xl px-5 py-8">
        <PageHeader
          title="Connectors"
          body="External tools from MCP servers. They extend what agents can do, never what they're allowed to do."
          action={
            canManage ? (
              <Button
                variant="primary"
                icon={<Plug className="h-4 w-4" />}
                onClick={() => setAdding(true)}
              >
                Add connector
              </Button>
            ) : undefined
          }
        />
        <AddConnectorDialog open={adding} onClose={() => setAdding(false)} />
        <p className="mb-5 rounded-lg border border-border bg-surface px-4 py-3 text-[13px] text-muted">
          A new connector starts disabled. Turning it on asks for an approval
          that shows exactly what runs. Each tool call then asks for your
          approval unless you set that tool to{" "}
          <span className="font-medium text-foreground">Allow</span>. Tool
          descriptions and results come from the server and are passed to agents
          as third-party data.
        </p>
        {error ? (
          <InlineError
            message="Couldn't load connectors."
            onRetry={() => void refetch()}
          />
        ) : isLoading ? (
          <div className="space-y-4">
            <Skeleton className="h-40" />
            <Skeleton className="h-40" />
          </div>
        ) : !data?.length ? (
          <EmptyState
            icon={<Plug className="h-9 w-9" />}
            title="No connectors"
            body="Add an MCP server, a local program or a remote https endpoint, to give agents its tools."
          />
        ) : (
          <div className="space-y-5">
            {data.map((c) => (
              <ConnectorCard key={c.id} c={c} canManage={canManage} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function ConnectorCard({ c, canManage }: { c: Connector; canManage: boolean }) {
  const qc = useQueryClient();
  const toast = useUIStore((s) => s.toast);
  const [busy, setBusy] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [secretFor, setSecretFor] = useState<string | undefined>();
  const refresh = () =>
    qc.invalidateQueries({ queryKey: ["gubern", "connectors"] });
  const act = async (fn: () => Promise<unknown>, ok: string) => {
    setBusy(true);
    try {
      await fn();
      toast({ text: ok, tone: "success" });
    } catch (e) {
      toast({
        text: e instanceof Error ? e.message : "That didn't work.",
        tone: "error",
      });
    } finally {
      setBusy(false);
      void refresh();
    }
  };
  const st = c.status;
  const offered = st.tools.filter(
    (t) => t.available && t.policy !== "deny",
  ).length;
  const where =
    c.transport === "stdio"
      ? `${c.command ?? ""} ${(c.args ?? []).join(" ")}`.trim()
      : (c.url ?? "");
  const missing = Object.entries(c.secretsSet)
    .filter(([, set]) => !set)
    .map(([n]) => n);
  return (
    <section className="rounded-xl border border-border bg-surface">
      <div className="flex flex-wrap items-start gap-3 border-b border-border px-5 py-4">
        <span
          className={cx(
            "mt-1.5 h-2.5 w-2.5 shrink-0 rounded-full",
            stateTone[st.state],
          )}
          aria-hidden
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-[15px] font-semibold">{c.name}</h2>
            <span className="font-mono text-[12px] text-muted">{c.id}</span>
            <Tag>
              {c.transport === "stdio" ? "Local program" : "Remote (HTTP)"}
            </Tag>
            <Tag>
              {st.state === "connected"
                ? "Connected"
                : st.state === "error"
                  ? "Error"
                  : st.state === "connecting"
                    ? "Connecting"
                    : "Disabled"}
            </Tag>
            {c.transport === "stdio" && <Tag>Network {c.network ?? "off"}</Tag>}
          </div>
          {c.description && (
            <p className="mt-0.5 text-[13px] text-muted">{c.description}</p>
          )}
          <p
            className="mt-1 truncate font-mono text-[12px] text-muted"
            title={where}
          >
            {where}
          </p>
          {st.state === "connected" && (
            <p className="mt-1 text-[12.5px] text-muted">
              {st.server} {st.serverVersion} · protocol {st.protocol} ·{" "}
              {sandboxText(c, st.sandbox)} · {offered} tool
              {offered === 1 ? "" : "s"} offered
            </p>
          )}
          {(st.error || c.lastError) && st.state !== "connected" && (
            <p className="mt-1 flex items-start gap-1.5 text-[12.5px] text-danger">
              <AlertTriangle
                className="mt-0.5 h-3.5 w-3.5 shrink-0"
                aria-hidden
              />
              <span className="break-words">{st.error || c.lastError}</span>
            </p>
          )}
          {missing.length > 0 && (
            <p className="mt-1 text-[12.5px] text-warning">
              Credential not set: {missing.join(", ")}
            </p>
          )}
        </div>
        {canManage && (
          <div className="flex flex-wrap gap-2">
            {c.enabled ? (
              <>
                <Button
                  size="sm"
                  icon={<RefreshCw className="h-3.5 w-3.5" />}
                  disabled={busy}
                  onClick={() =>
                    void act(
                      () => client.reconnectConnector(c.id),
                      `Reconnected ${c.name}.`,
                    )
                  }
                >
                  Reconnect
                </Button>
                <Button
                  size="sm"
                  icon={<Power className="h-3.5 w-3.5" />}
                  disabled={busy}
                  onClick={() =>
                    void act(
                      () => client.disableConnector(c.id),
                      `${c.name} is off. Its tools are gone from runs.`,
                    )
                  }
                >
                  Turn off
                </Button>
              </>
            ) : (
              <Button
                size="sm"
                variant="primary"
                icon={<PlugZap className="h-3.5 w-3.5" />}
                disabled={busy}
                onClick={() =>
                  void act(
                    () => client.enableConnector(c.id),
                    `Approval requested. Approve it in the Inbox (or on your phone) to turn ${c.name} on.`,
                  )
                }
              >
                Turn on
              </Button>
            )}
            {Object.keys(c.secretsSet).map((n) => (
              <Button
                key={n}
                size="sm"
                icon={<KeyRound className="h-3.5 w-3.5" />}
                onClick={() => setSecretFor(n)}
              >
                {c.secretsSet[n] ? `Replace ${n}` : `Set ${n}`}
              </Button>
            ))}
            <Button
              size="sm"
              variant="ghost"
              icon={<Trash2 className="h-3.5 w-3.5" />}
              aria-label={`Remove ${c.name}`}
              onClick={() => setRemoving(true)}
            />
          </div>
        )}
      </div>
      {st.tools.length > 0 && (
        <ul className="divide-y divide-border">
          {st.tools.map((t) => (
            <ToolRow key={t.name} c={c} t={t} canManage={canManage} />
          ))}
        </ul>
      )}
      {st.state === "error" && st.stderr && (
        <pre className="max-h-40 overflow-auto border-t border-border bg-background px-5 py-2 font-mono text-[11.5px] text-muted">
          {st.stderr}
        </pre>
      )}
      <SecretDialog
        connector={c}
        name={secretFor}
        onClose={() => setSecretFor(undefined)}
      />
      <Dialog
        open={removing}
        onClose={() => setRemoving(false)}
        title={`Remove ${c.name}?`}
        className="max-w-md"
      >
        <div className="space-y-4 p-5">
          <p className="text-[13px] text-muted">
            The connector is stopped and its tools leave every run. Its stored
            credentials are deleted.
          </p>
          <div className="flex justify-end gap-2">
            <Button onClick={() => setRemoving(false)}>Cancel</Button>
            <Button
              variant="danger"
              disabled={busy}
              onClick={() =>
                void act(
                  () => client.removeConnector(c.id),
                  `Removed ${c.name}.`,
                ).then(() => setRemoving(false))
              }
            >
              Remove
            </Button>
          </div>
        </div>
      </Dialog>
    </section>
  );
}

function sandboxText(c: Connector, adapter?: string) {
  if (c.transport === "http") return "remote server";
  if (!adapter || adapter === "none") return "not sandboxed";
  return adapter === "bubblewrap"
    ? "isolated (Linux)"
    : adapter === "job-object"
      ? "contained (Windows): not fenced from files or network"
      : adapter;
}

const policyOptions: { value: ToolPolicy; label: string }[] = [
  { value: "ask", label: "Ask" },
  { value: "allow", label: "Allow" },
  { value: "deny", label: "Deny" },
];

function ToolRow({
  c,
  t,
  canManage,
}: {
  c: Connector;
  t: ConnectorTool;
  canManage: boolean;
}) {
  const qc = useQueryClient();
  const toast = useUIStore((s) => s.toast);
  return (
    <li className="flex flex-wrap items-start gap-3 px-5 py-3">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span
            className={cx(
              "font-mono text-[13px]",
              !t.available && "text-muted line-through",
            )}
          >
            {t.fullName}
          </span>
          <Tag>External</Tag>
          {!t.available && <Tag tone="warning">Unavailable</Tag>}
        </div>
        <p className="mt-0.5 text-[12.5px] text-muted">
          <span className="text-[11px] uppercase tracking-wide">
            From the server:
          </span>{" "}
          {t.description || t.title || "no description"}
        </p>
        {t.problem && (
          <p className="mt-0.5 text-[12px] text-warning">{t.problem}</p>
        )}
      </div>
      {t.available && (
        <div className="shrink-0">
          {canManage ? (
            <Segmented<ToolPolicy>
              label={`Policy for ${t.fullName}`}
              value={t.policy}
              options={policyOptions}
              onChange={async (p) => {
                try {
                  await client.setConnectorToolPolicy(c.id, t.name, p);
                  void qc.invalidateQueries({
                    queryKey: ["gubern", "connectors"],
                  });
                } catch (e) {
                  toast({
                    text:
                      e instanceof Error
                        ? e.message
                        : "Couldn't change the policy.",
                    tone: "error",
                  });
                }
              }}
            />
          ) : (
            <Tag>{policyOptions.find((o) => o.value === t.policy)?.label}</Tag>
          )}
        </div>
      )}
    </li>
  );
}

function Tag({
  children,
  tone,
}: {
  children: React.ReactNode;
  tone?: "warning";
}) {
  return (
    <span
      className={cx(
        "rounded border px-1.5 py-px text-[11px] font-medium",
        tone === "warning"
          ? "border-warning/40 text-warning"
          : "border-border text-muted",
      )}
    >
      {children}
    </span>
  );
}

function SecretDialog({
  connector,
  name,
  onClose,
}: {
  connector: Connector;
  name?: string;
  onClose: () => void;
}) {
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | undefined>();
  const qc = useQueryClient();
  const save = async () => {
    if (!name) return;
    setBusy(true);
    setErr(undefined);
    try {
      await client.setConnectorSecret(connector.id, name, value);
      setValue("");
      void qc.invalidateQueries({ queryKey: ["gubern", "connectors"] });
      onClose();
    } catch (e) {
      setErr(e instanceof Error ? e.message : "Couldn't save it.");
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      open={!!name}
      onClose={onClose}
      title={`Credential ${name ?? ""} for ${connector.name}`}
      className="max-w-md"
    >
      <form
        className="space-y-3 p-5"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <p className="text-[13px] text-muted">
          Stored in Gubern's secret store and given only to this connector. It
          is never shown again.
        </p>
        <input
          type="password"
          autoComplete="off"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          aria-label="Credential value"
          className="w-full rounded-lg border border-border bg-background px-3 py-2 font-mono text-[13px] outline-none focus:border-primary"
        />
        {err && <p className="text-[13px] text-danger">{err}</p>}
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" type="submit" disabled={busy || !value}>
            Save
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

const field =
  "w-full rounded-lg border border-border bg-background px-3 py-2 text-[13px] outline-none focus:border-primary";

function AddConnectorDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const [transport, setTransport] = useState<"stdio" | "http">("stdio");
  const [id, setId] = useState("");
  const [name, setName] = useState("");
  const [command, setCommand] = useState("");
  const [args, setArgs] = useState("");
  const [env, setEnv] = useState("");
  const [network, setNetwork] = useState<"off" | "on">("off");
  const [url, setUrl] = useState("");
  const [local, setLocal] = useState(false);
  const [bearer, setBearer] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | undefined>();
  const qc = useQueryClient();
  const toast = useUIStore((s) => s.toast);
  const submit = async () => {
    setBusy(true);
    setErr(undefined);
    const input: ConnectorInput =
      transport === "stdio"
        ? {
            id: id.trim(),
            name: name.trim() || undefined,
            transport,
            command: command.trim(),
            args: args
              .split("\n")
              .map((a) => a.trim())
              .filter(Boolean),
            env: Object.fromEntries(
              env
                .split("\n")
                .map((l) => l.trim())
                .filter((l) => l.includes("="))
                .map((l) => [
                  l.slice(0, l.indexOf("=")),
                  l.slice(l.indexOf("=") + 1),
                ]),
            ),
            network,
          }
        : {
            id: id.trim(),
            name: name.trim() || undefined,
            transport,
            url: url.trim(),
            local,
            bearerSecret: bearer.trim() || undefined,
          };
    try {
      await client.addConnector(input);
      void qc.invalidateQueries({ queryKey: ["gubern", "connectors"] });
      toast({
        text: `Added ${input.id}. It's off until you turn it on and approve.`,
        tone: "success",
      });
      onClose();
    } catch (e) {
      setErr(e instanceof Error ? e.message : "Couldn't add it.");
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Add a connector"
      className="max-w-lg"
    >
      <form
        className="space-y-3 p-5"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <Segmented<"stdio" | "http">
          label="Transport"
          value={transport}
          options={[
            { value: "stdio", label: "Local program" },
            { value: "http", label: "Remote (HTTP)" },
          ]}
          onChange={setTransport}
        />
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="block text-[13px] font-medium">
            ID
            <input
              value={id}
              onChange={(e) => setId(e.target.value)}
              placeholder="github"
              spellCheck={false}
              className={cx(field, "mt-1 font-mono")}
            />
          </label>
          <label className="block text-[13px] font-medium">
            Name
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="GitHub"
              className={cx(field, "mt-1")}
            />
          </label>
        </div>
        {transport === "stdio" ? (
          <>
            <label className="block text-[13px] font-medium">
              Program (absolute path)
              <input
                value={command}
                onChange={(e) => setCommand(e.target.value)}
                placeholder="/usr/local/bin/mcp-server"
                spellCheck={false}
                className={cx(field, "mt-1 font-mono")}
              />
            </label>
            <label className="block text-[13px] font-medium">
              Arguments, one per line
              <textarea
                value={args}
                onChange={(e) => setArgs(e.target.value)}
                rows={2}
                spellCheck={false}
                className={cx(field, "mt-1 font-mono")}
              />
            </label>
            <label className="block text-[13px] font-medium">
              Environment, NAME=value per line (not for secrets)
              <textarea
                value={env}
                onChange={(e) => setEnv(e.target.value)}
                rows={2}
                spellCheck={false}
                className={cx(field, "mt-1 font-mono")}
              />
            </label>
            <div className="flex items-center gap-3 text-[13px]">
              <span className="font-medium">Network</span>
              <Segmented<"off" | "on">
                label="Network"
                value={network}
                options={[
                  { value: "off", label: "Off" },
                  { value: "on", label: "On" },
                ]}
                onChange={setNetwork}
              />
            </div>
          </>
        ) : (
          <>
            <label className="block text-[13px] font-medium">
              Endpoint URL (https)
              <input
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder="https://mcp.example.com/mcp"
                spellCheck={false}
                className={cx(field, "mt-1 font-mono")}
              />
            </label>
            <label className="flex items-center gap-2 text-[13px]">
              <input
                type="checkbox"
                checked={local}
                onChange={(e) => setLocal(e.target.checked)}
              />
              A server on this machine (allows http://localhost)
            </label>
            <label className="block text-[13px] font-medium">
              Bearer credential name (optional; set its value after adding)
              <input
                value={bearer}
                onChange={(e) => setBearer(e.target.value)}
                placeholder="token"
                spellCheck={false}
                className={cx(field, "mt-1 font-mono")}
              />
            </label>
          </>
        )}
        {err && <p className="text-[13px] text-danger">{err}</p>}
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>Cancel</Button>
          <Button
            variant="primary"
            type="submit"
            disabled={
              busy ||
              !id.trim() ||
              (transport === "stdio" ? !command.trim() : !url.trim())
            }
          >
            Add (off)
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
