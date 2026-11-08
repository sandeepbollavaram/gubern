import { useState } from "react";
import {
  Check,
  Folder,
  FolderPlus,
  Minus,
  Shield,
  ShieldCheck,
  ShieldOff,
  Trash2,
} from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { client, useCan, useWorkspaceList } from "../../data/client";
import type { SandboxInfo, WorkspaceEntry } from "../../data/model";
import { useUIStore } from "../../store";
import {
  Button,
  Dialog,
  EmptyState,
  IconButton,
  InlineError,
  Skeleton,
} from "../../ui/primitives";
import { cx } from "../../ui/util";
import { AddFolderDialog } from "../chat/Composer";
import { Namespaces } from "../../pages/Namespaces";
import { PageHeader } from "./PageHeader";

// Workspaces (agent-platform Phase 4): the folders agent runs may work in,
// and the boundary their terminal commands run in on this server. The
// sandbox card repeats what the server says it enforces and what it doesn't,
// word for word, so the page never claims more than the platform does.

export function Workspaces() {
  const { data, isLoading, error, refetch } = useWorkspaceList();
  const canManage = useCan("workspaces:manage");
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<WorkspaceEntry | undefined>();
  const toast = useUIStore((s) => s.toast);
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-5xl px-5 py-8">
        <PageHeader
          title="Workspaces"
          body="Folders agents may work in, and the boundary their commands run in on this server."
          action={
            canManage ? (
              <Button
                variant="primary"
                icon={<FolderPlus className="h-4 w-4" />}
                onClick={() => setAdding(true)}
              >
                Add folder
              </Button>
            ) : undefined
          }
        />
        <AddFolderDialog
          open={adding}
          onClose={() => setAdding(false)}
          onAdded={(name) => {
            setAdding(false);
            toast({
              text: `Added ${name}. Pick it in a chat to run agents there.`,
              tone: "success",
            });
          }}
        />
        <RemoveDialog entry={removing} onClose={() => setRemoving(undefined)} />

        {error ? (
          <InlineError
            message="Couldn't load workspaces."
            onRetry={() => void refetch()}
          />
        ) : isLoading ? (
          <div className="space-y-4">
            <Skeleton className="h-48" />
            <Skeleton className="h-40" />
          </div>
        ) : (
          <div className="space-y-8">
            <SandboxCard info={data?.sandbox} />
            <section aria-labelledby="ws-folders">
              <h2 id="ws-folders" className="mb-2 text-[15px] font-semibold">
                Folders
              </h2>
              {!data?.workspaces.length ? (
                <EmptyState
                  icon={<Folder className="h-9 w-9" />}
                  title="No folders yet"
                  body="Add a folder and agents can work in it."
                />
              ) : (
                <ul className="divide-y divide-border rounded-xl border border-border bg-surface">
                  {data.workspaces.map((w) => (
                    <li
                      key={w.name}
                      className="flex items-center gap-3 px-4 py-3"
                    >
                      <Folder
                        className={cx(
                          "h-4 w-4 shrink-0",
                          w.exists ? "text-muted" : "text-warning",
                        )}
                        aria-hidden
                      />
                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="text-[14px] font-medium">
                            {w.name}
                          </span>
                          {w.isDefault && <Tag>Default</Tag>}
                          {!w.exists && (
                            <Tag tone="warning">Folder missing</Tag>
                          )}
                        </div>
                        <p
                          className="truncate font-mono text-[12px] text-muted"
                          title={w.path}
                        >
                          {w.path}
                        </p>
                      </div>
                      {canManage && !w.isDefault && (
                        <IconButton
                          label={`Remove ${w.name}`}
                          onClick={() => setRemoving(w)}
                        >
                          <Trash2 className="h-4 w-4" />
                        </IconButton>
                      )}
                    </li>
                  ))}
                </ul>
              )}
              <p className="mt-2 text-[12.5px] text-muted">
                A run works in the folder picked in the chat. It still asks
                before it changes a file or runs a command.
              </p>
            </section>
            <section aria-labelledby="ws-namespaces">
              <h2 id="ws-namespaces" className="mb-1 text-[15px] font-semibold">
                Namespaces
              </h2>
              <p className="mb-3 text-[13px] text-muted">
                Groups of routes and policy on the edge.
              </p>
              <Namespaces />
            </section>
          </div>
        )}
      </div>
    </div>
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

const levelText: Record<SandboxInfo["level"], string> = {
  isolated:
    "Commands agents run, after your approval, can only change the workspace and can't see the rest of this machine.",
  contained:
    "Commands agents run, after your approval, are kept together and capped, and nothing they start outlives them. They are not fenced off from the rest of this machine.",
  none: "Commands agents run, after your approval, run directly on this machine.",
};

export function SandboxCard({ info }: { info?: SandboxInfo }) {
  if (!info) {
    return (
      <section className="rounded-xl border border-border bg-surface p-5 text-[13px] text-muted">
        This server doesn't report a sandbox. It may be older than this
        dashboard.
      </section>
    );
  }
  const Icon =
    info.level === "isolated"
      ? ShieldCheck
      : info.level === "contained"
        ? Shield
        : ShieldOff;
  const tone =
    info.level === "isolated"
      ? "text-success"
      : info.level === "contained"
        ? "text-warning"
        : "text-muted";
  return (
    <section
      aria-labelledby="ws-sandbox"
      className="rounded-xl border border-border bg-surface"
    >
      <div className="flex items-start gap-3 border-b border-border px-5 py-4">
        <Icon className={cx("mt-0.5 h-5 w-5 shrink-0", tone)} aria-hidden />
        <div className="min-w-0">
          <h2 id="ws-sandbox" className="text-[15px] font-semibold">
            {info.label}
          </h2>
          <p className="mt-0.5 text-[13px] text-muted">
            {levelText[info.level]}
          </p>
          {info.reason && (
            <p className="mt-1 text-[13px] text-muted">Why: {info.reason}.</p>
          )}
        </div>
      </div>
      <div className="grid gap-5 px-5 py-4 sm:grid-cols-2">
        <div>
          <h3 className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-muted">
            Enforced
          </h3>
          <ul className="space-y-1.5">
            {info.enforced.map((e) => (
              <li key={e} className="flex gap-2 text-[13px]">
                <Check
                  className="mt-0.5 h-3.5 w-3.5 shrink-0 text-success"
                  aria-hidden
                />
                <span>{capitalize(e)}</span>
              </li>
            ))}
          </ul>
        </div>
        <div>
          <h3 className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-muted">
            Not enforced here
          </h3>
          <ul className="space-y-1.5">
            {info.notEnforced.map((e) => (
              <li key={e} className="flex gap-2 text-[13px] text-muted">
                <Minus className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden />
                <span>{capitalize(e)}</span>
              </li>
            ))}
          </ul>
        </div>
      </div>
      <dl className="flex flex-wrap gap-x-6 gap-y-1 border-t border-border px-5 py-3 text-[12.5px]">
        <div className="flex gap-1.5">
          <dt className="text-muted">Network</dt>
          <dd
            className={cx(
              "font-medium",
              info.network === "not enforced" && "text-warning",
            )}
          >
            {info.network}
          </dd>
        </div>
        <div className="flex gap-1.5">
          <dt className="text-muted">Adapter</dt>
          <dd className="font-mono">{info.adapter}</dd>
        </div>
        <div className="flex gap-1.5">
          <dt className="text-muted">Mode</dt>
          <dd className="font-mono">{info.mode}</dd>
        </div>
        <div className="text-muted">
          Set in <code className="font-mono">agents.local_tools.sandbox</code>
        </div>
      </dl>
    </section>
  );
}

function capitalize(s: string) {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function RemoveDialog({
  entry,
  onClose,
}: {
  entry?: WorkspaceEntry;
  onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const qc = useQueryClient();
  const toast = useUIStore((s) => s.toast);
  const remove = async () => {
    if (!entry) return;
    setBusy(true);
    try {
      await client.removeWorkspace(entry.name);
      await qc.invalidateQueries({ queryKey: ["gubern", "workspaces"] });
      toast({
        text: `Removed ${entry.name}. The folder itself wasn't touched.`,
        tone: "success",
      });
      onClose();
    } catch (e) {
      toast({
        text: e instanceof Error ? e.message : "Couldn't remove the workspace.",
        tone: "error",
      });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      open={!!entry}
      onClose={onClose}
      title={`Remove ${entry?.name ?? ""}?`}
      className="max-w-md"
    >
      <div className="space-y-4 p-5">
        <p className="text-[13px] text-muted">
          Agent runs won't be able to pick this folder any more. Nothing in the
          folder is deleted, and you can add it again.
        </p>
        <p className="truncate font-mono text-[12px]">{entry?.path}</p>
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>Cancel</Button>
          <Button
            variant="danger"
            onClick={() => void remove()}
            disabled={busy}
          >
            {busy ? "Removing…" : "Remove"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}
