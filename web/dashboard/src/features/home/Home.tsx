import { useLocation, useNavigate, Link } from "react-router";
import { AlertTriangle, CheckCircle2, FileText, Hammer, Route as RouteIcon, ShieldAlert, Sparkles, XCircle } from "lucide-react";
import { client, useAttention, useCan, useIdentity, useOutputs, useSummary } from "../../data/client";
import type { AttentionItem, Output } from "../../data/model";
import { useUIStore } from "../../store";
import { Composer } from "../chat/Composer";
import { GubernMark } from "../../app/GubernMark";
import { Countdown, InlineError, RelTime, Skeleton } from "../../ui/primitives";
import { cx, titleCase } from "../../ui/util";

function greeting() {
  const h = new Date().getHours();
  return h < 5 ? "Working late" : h < 12 ? "Good morning" : h < 18 ? "Good afternoon" : "Good evening";
}

// StatusLine answers "is everything OK?" in one sentence, worst state first
// (UX spec F2).
function StatusLine() {
  const { data, isLoading, isError, refetch } = useSummary();
  if (isLoading) return <Skeleton className="mx-auto h-5 w-96" />;
  if (isError || !data) return <InlineError message="Couldn't load system status." onRetry={() => void refetch()} />;
  const Icon = data.worst === "ok" ? CheckCircle2 : data.worst === "degraded" ? AlertTriangle : XCircle;
  const lead =
    data.worst === "ok"
      ? "All systems healthy"
      : data.routesFailing > 0
        ? `${data.routesFailing} route${data.routesFailing === 1 ? "" : "s"} need${data.routesFailing === 1 ? "s" : ""} attention`
        : "Degraded";
  return (
    <p className="flex flex-wrap items-center justify-center gap-x-2 text-sm text-muted">
      <span
        className={cx(
          "inline-flex items-center gap-1.5 font-medium",
          data.worst === "ok" ? "text-success" : data.worst === "degraded" ? "text-warning" : "text-danger",
        )}
      >
        <Icon className="h-4 w-4" aria-hidden />
        {lead}
      </span>
      <span aria-hidden>·</span>
      <Link to="/operate/routes" className="hover:text-foreground">
        {data.routes} routes
      </Link>
      <span aria-hidden>·</span>
      <Link to="/operate/nodes" className="hover:text-foreground">
        {data.nodes} nodes
      </Link>
      <span aria-hidden>·</span>
      <button type="button" className="cursor-pointer hover:text-foreground" onClick={() => useUIStore.getState().openSettings("budget")}>
        ${data.spendToday.toFixed(2)} today of ${data.budgetDaily}
      </button>
    </p>
  );
}

function AttentionRow({ a }: { a: AttentionItem }) {
  const navigate = useNavigate();
  const Icon = a.kind === "approval" ? ShieldAlert : a.kind === "route" ? RouteIcon : AlertTriangle;
  return (
    <li className="flex items-center gap-3 rounded-lg border border-border bg-surface px-3.5 py-2.5">
      <Icon className={cx("h-4 w-4 shrink-0", a.severity === "high" || a.severity === "critical" ? "text-danger" : "text-warning")} aria-hidden />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm">{a.title}</span>
        <span className="block truncate font-mono text-[12px] text-muted">{a.detail}</span>
      </span>
      {a.expiresAt && (
        <span className="shrink-0 text-[12px] text-muted">
          <Countdown until={a.expiresAt} /> left
        </span>
      )}
      <button
        type="button"
        onClick={() => navigate(a.href)}
        className="h-8 shrink-0 cursor-pointer rounded bg-surface-2 px-3 text-[13px] font-medium hover:bg-accent"
      >
        {a.kind === "approval" ? "Review" : a.kind === "question" ? "Answer" : "Open"}
      </button>
    </li>
  );
}

const OUTPUT_ICON: Record<Output["type"], typeof FileText> = { report: FileText, build: Hammer, config: Sparkles, file: FileText };

export function Home() {
  const { data: me } = useIdentity();
  const canRun = useCan("agents:run");
  const { data: attention } = useAttention();
  const { data: outputs, isLoading: outputsLoading } = useOutputs();
  const navigate = useNavigate();
  const location = useLocation();
  const focus = (location.state as { focusComposer?: boolean } | null)?.focusComposer;

  const start = async (text: string) => {
    const prefs = useUIStore.getState();
    const id = await client.send(undefined, text, { mode: prefs.mode, model: prefs.model, workspace: prefs.workspace });
    navigate(`/chat/${id}`);
  };

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-3xl flex-col px-5 pb-16 pt-[14vh]">
        <h1 className="mb-3 flex items-center justify-center gap-3 text-[32px] font-semibold tracking-tight">
          <GubernMark className="h-9 w-9" />
          {greeting()}
          {me ? `, ${me.name}` : ""}
        </h1>
        <div className="mb-8">
          <StatusLine />
        </div>

        {canRun ? (
          <Composer draftKey="new" autoFocus={focus ?? true} onSend={(t) => void start(t)} placeholder="Ask Gubern, or describe a task. Type / for commands" />
        ) : (
          <p className="rounded-lg border border-border bg-surface px-4 py-3 text-center text-[13px] text-muted">
            Your key can read Gubern but not run agents. Ask the owner for an operator key.
          </p>
        )}

        {attention && attention.length > 0 && (
          <section className="mt-10" aria-labelledby="needs-you">
            <div className="mb-2 flex items-center justify-between">
              <h2 id="needs-you" className="text-[13px] font-semibold">
                Needs you <span className="text-muted">({attention.length})</span>
              </h2>
              <Link to="/inbox" className="text-[13px] text-muted hover:text-foreground">
                View inbox
              </Link>
            </div>
            <ul className="space-y-2">
              {attention.slice(0, 4).map((a) => (
                <AttentionRow key={a.id} a={a} />
              ))}
            </ul>
          </section>
        )}

        <section className="mt-10" aria-labelledby="recent-outputs">
          <div className="mb-2 flex items-center justify-between">
            <h2 id="recent-outputs" className="text-[13px] font-semibold">
              Recent outputs
            </h2>
            <Link to="/outputs" className="text-[13px] text-muted hover:text-foreground">
              View all
            </Link>
          </div>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            {outputsLoading
              ? Array.from({ length: 4 }, (_, i) => <Skeleton key={i} className="h-28" />)
              : (outputs ?? []).slice(0, 4).map((o) => {
                  const Icon = OUTPUT_ICON[o.type];
                  return (
                    <button
                      key={o.id}
                      type="button"
                      onClick={() => (o.sessionId ? navigate(`/chat/${o.sessionId}`) : navigate("/outputs"))}
                      className="group flex cursor-pointer flex-col overflow-hidden rounded-lg border border-border bg-surface text-left transition-colors duration-150 hover:border-primary/50"
                    >
                      <div className="flex h-16 items-center justify-center bg-surface-2">
                        <Icon className="h-6 w-6 text-muted group-hover:text-primary" aria-hidden />
                      </div>
                      <div className="px-3 py-2">
                        <p className="line-clamp-2 text-[13px] leading-snug">{o.title}</p>
                        <p className="mt-0.5 text-[12px] text-muted">
                          {titleCase(o.type)} · <RelTime at={o.at} />
                        </p>
                      </div>
                    </button>
                  );
                })}
          </div>
        </section>
      </div>
    </div>
  );
}
