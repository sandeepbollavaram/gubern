import { useEffect, useRef, useState } from "react";
import { CheckCircle2, Clock, ShieldQuestion, XCircle } from "lucide-react";
import { client, useApproval } from "../../data/client";
import { useUIStore } from "../../store";
import { Button, Countdown, RiskBadge } from "../../ui/primitives";
import { cx, relative } from "../../ui/util";

// ApprovalCard implements UX spec F4. It shows what will run, where, who
// asked and the deadline. For high and critical risk, Approve needs a
// deliberate second click: at least 0.5 s after the first (so a reflex
// double-click cannot approve) and within 6 s (long enough for touch and
// assistive tech). A and R decide when the card has focus. Decisions made
// elsewhere (Telegram, Slack) update it live.
const CONFIRM_MIN_MS = 500;
const CONFIRM_WINDOW_MS = 6000;

export function ApprovalCard({ approvalId, highlight }: { approvalId: string; highlight?: boolean }) {
  const a = useApproval(approvalId);
  const toast = useUIStore((s) => s.toast);
  const [armedAt, setArmedAt] = useState<number | null>(null);
  const [armedLeft, setArmedLeft] = useState(0);
  const armed = armedAt !== null;
  const [busy, setBusy] = useState<"approve" | "reject" | null>(null);
  const box = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (armedAt === null) return;
    const tick = setInterval(() => {
      const left = CONFIRM_WINDOW_MS - (Date.now() - armedAt);
      if (left <= 0) setArmedAt(null);
      else setArmedLeft(Math.ceil(left / 1000));
    }, 250);
    return () => clearInterval(tick);
  }, [armedAt]);
  useEffect(() => {
    if (highlight) box.current?.scrollIntoView({ block: "center" });
  }, [highlight]);

  if (!a) return null;
  const strict = a.risk === "HIGH RISK" || a.risk === "CRITICAL";

  const decide = async (approve: boolean) => {
    if (approve && strict) {
      const now = Date.now();
      if (armedAt === null) {
        setArmedAt(now);
        setArmedLeft(CONFIRM_WINDOW_MS / 1000);
        return;
      }
      if (now - armedAt < CONFIRM_MIN_MS) return; // a double-click, not a decision
    }
    setBusy(approve ? "approve" : "reject");
    try {
      await client.decide(a.id, approve);
      toast({ text: approve ? `Approved ${a.tool}` : `Rejected ${a.tool}. Nothing was run.`, tone: approve ? "success" : "info" });
    } catch (e) {
      toast({ text: `Couldn't record the decision: ${(e as Error).message}`, tone: "error" });
    } finally {
      setBusy(null);
      setArmedAt(null);
    }
  };

  if (a.state !== "pending") {
    const via = a.decidedVia && a.decidedVia !== "dashboard" ? ` in ${a.decidedVia[0].toUpperCase()}${a.decidedVia.slice(1)}` : "";
    const who = a.decidedBy === "you" ? "you" : a.decidedBy;
    const line =
      a.state === "approved"
        ? `Approved by ${who}${via}`
        : a.state === "rejected"
          ? `Rejected by ${who}${via}`
          : "Expired. Rejected automatically after 10 minutes.";
    const Icon = a.state === "approved" ? CheckCircle2 : a.state === "rejected" ? XCircle : Clock;
    return (
      <div className="flex items-center gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-[13px] text-muted animate-fade-in">
        <Icon className={cx("h-4 w-4", a.state === "approved" ? "text-success" : a.state === "rejected" ? "text-muted" : "text-warning")} aria-hidden />
        <span>
          <span className="font-medium text-foreground">{a.tool}</span> · {line}
          {a.decidedAt && <> · {relative(a.decidedAt)}</>}
        </span>
      </div>
    );
  }

  return (
    <div
      ref={box}
      tabIndex={0}
      role="group"
      aria-label={`Approval needed: ${a.tool}, ${a.risk.toLowerCase()}`}
      onKeyDown={(e) => {
        if (e.target !== e.currentTarget) return;
        if (e.key === "a" || e.key === "A") void decide(true);
        if (e.key === "r" || e.key === "R") void decide(false);
      }}
      className={cx(
        "rounded-xl border bg-surface shadow-sm outline-none focus-visible:ring-2 focus-visible:ring-ring",
        strict ? "border-danger/50" : "border-warning/50",
        highlight && "animate-flash",
      )}
    >
      <div className="flex items-center gap-2 border-b border-border px-4 py-2.5">
        <ShieldQuestion className="h-4 w-4 text-warning" aria-hidden />
        <span className="text-sm font-semibold">Approval needed</span>
        <span className="flex-1" />
        <RiskBadge risk={a.risk} />
      </div>
      <div className="space-y-2.5 px-4 py-3 text-sm">
        <p>
          <span className="font-mono text-[13px] font-medium">{a.tool}</span>
          <span className="text-muted"> in </span>
          <span className="font-mono text-[13px]">{a.workspace}</span>
          <span className="text-muted">: {a.description}</span>
        </p>
        {(a.command || a.preview) && (
          <pre className="overflow-x-auto rounded-lg border border-border bg-background px-3 py-2 font-mono text-[13px] leading-relaxed">
            {a.command ? <span className="select-none text-muted">$ </span> : null}
            {a.command ?? a.preview}
          </pre>
        )}
        {!a.command && !a.preview && (
          <p className="rounded border border-warning/40 bg-warning/10 px-3 py-2 text-[13px] text-warning">
            This request came with no details. Reject it unless you expected it.
          </p>
        )}
        <p className="text-[13px] text-muted">
          Requested by {a.requestedBy} · Expires in <Countdown until={a.expiresAt} />. It's rejected automatically if nobody decides.
        </p>
      </div>
      <div className="flex items-center gap-2 border-t border-border px-4 py-2.5">
        <Button variant="secondary" busy={busy === "reject"} disabled={!!busy} onClick={() => void decide(false)}>
          Reject <span className="ml-1 font-mono text-[11px] text-muted">R</span>
        </Button>
        <Button
          variant={strict ? (armed ? "danger" : "secondary") : "primary"}
          busy={busy === "approve"}
          disabled={!!busy}
          onClick={() => void decide(true)}
          aria-live="polite"
        >
          {armed ? `Click again to approve (${armedLeft}s)` : "Approve"} <span className="ml-1 font-mono text-[11px] opacity-70">A</span>
        </Button>
        {strict && !armed && <span className="text-[12px] text-muted">High risk: approve takes two clicks.</span>}
      </div>
    </div>
  );
}
