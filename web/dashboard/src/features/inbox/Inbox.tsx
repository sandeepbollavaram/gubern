import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { AlertTriangle, CheckCircle2, Inbox as InboxIcon, Route as RouteIcon, ShieldAlert } from "lucide-react";
import { useAttention } from "../../data/client";
import type { AttentionItem } from "../../data/model";
import { ApprovalCard } from "../chat/ApprovalCard";
import { Countdown, EmptyState, InlineError, Kbd, RelTime, Skeleton } from "../../ui/primitives";
import { cx } from "../../ui/util";

const ICON = { approval: ShieldAlert, question: AlertTriangle, route: RouteIcon, node: AlertTriangle, budget: AlertTriangle, checkpoint: ShieldAlert };

// Inbox: everything waiting on a person, most urgent first (UX spec §2.1).
// J/K move, Enter opens; approvals can be decided right here.
export function Inbox() {
  const { data, isLoading, isError, refetch } = useAttention();
  const navigate = useNavigate();
  const [cursor, setCursor] = useState(0);
  const list = useRef<HTMLUListElement | null>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement).tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || !data?.length) return;
      if (e.key === "j") setCursor((c) => Math.min(c + 1, data.length - 1));
      if (e.key === "k") setCursor((c) => Math.max(c - 1, 0));
      if (e.key === "Enter" && document.activeElement === document.body) navigate(data[cursor].href);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [data, cursor, navigate]);

  useEffect(() => {
    list.current?.querySelector<HTMLElement>(`[data-index="${cursor}"]`)?.scrollIntoView({ block: "nearest" });
  }, [cursor]);

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-3xl px-5 py-8">
        <header className="mb-6 flex items-end justify-between">
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">Inbox</h1>
            <p className="mt-1 text-sm text-muted">Everything waiting on a person, most urgent first.</p>
          </div>
          <p className="hidden items-center gap-1 text-[12px] text-muted sm:flex">
            <Kbd>J</Kbd>
            <Kbd>K</Kbd> to move
          </p>
        </header>
        {isLoading && (
          <div className="space-y-3">
            {Array.from({ length: 3 }, (_, i) => (
              <Skeleton key={i} className="h-24" />
            ))}
          </div>
        )}
        {isError && <InlineError message="Couldn't load the inbox." onRetry={() => void refetch()} />}
        {data && data.length === 0 && (
          <EmptyState
            icon={<CheckCircle2 className="h-9 w-9 text-success" />}
            title="Nothing needs you right now"
            body="Approvals, questions from agents and failing routes appear here as they happen."
          />
        )}
        <ul ref={list} className="space-y-3">
          {data?.map((a: AttentionItem, i) => {
            const Icon = ICON[a.kind] ?? InboxIcon;
            const approvalId = a.kind === "approval" ? new URL(a.href, "http://x").searchParams.get("approval") : null;
            return (
              <li
                key={a.id}
                data-index={i}
                className={cx(
                  "relative rounded-xl",
                  i === cursor && "before:absolute before:-left-3 before:bottom-3 before:top-3 before:w-1 before:rounded-full before:bg-primary",
                )}
                aria-current={i === cursor ? "true" : undefined}
              >
                {approvalId ? (
                  <ApprovalCard approvalId={approvalId} />
                ) : (
                  <div className="flex items-center gap-3 rounded-xl border border-border bg-surface px-4 py-3">
                    <Icon className={cx("h-5 w-5", a.severity === "high" || a.severity === "critical" ? "text-danger" : "text-warning")} aria-hidden />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium">{a.title}</p>
                      <p className="truncate font-mono text-[12px] text-muted">{a.detail}</p>
                    </div>
                    <span className="text-[12px] text-muted">{a.expiresAt ? <Countdown until={a.expiresAt} /> : <RelTime at={a.at} />}</span>
                    <button
                      type="button"
                      onClick={() => navigate(a.href)}
                      className="h-8 cursor-pointer rounded bg-surface-2 px-3 text-[13px] font-medium hover:bg-accent"
                    >
                      {a.kind === "question" ? "Answer" : "Open"}
                    </button>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      </div>
    </div>
  );
}
