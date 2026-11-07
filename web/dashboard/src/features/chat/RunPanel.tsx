import { useState } from "react";
import { Check, ChevronDown, Circle, FileText, Loader2, X } from "lucide-react";
import { usePlan } from "../../data/client";
import { cx, relative, titleCase } from "../../ui/util";

// RunPanel (UX spec F3c/F5): progress timeline, outputs, what the run used,
// and usage. Shown in the side panel for long or multi-step runs.
export function RunPanel({ sessionId }: { sessionId: string }) {
  const plan = usePlan(sessionId);
  const [showEarlier, setShowEarlier] = useState(false);
  if (!plan) {
    return <p className="p-4 text-sm text-muted">This session has no multi-step plan. Tool activity appears in the conversation.</p>;
  }
  const currentIdx = plan.steps.findIndex((s) => s.status === "current");
  const hiddenCount = showEarlier ? 0 : Math.max(0, currentIdx - 2);
  const visible = plan.steps.slice(hiddenCount);

  return (
    <div className="space-y-6 p-4 text-sm">
      <section aria-label="Progress">
        <h3 className="mb-2 text-[13px] font-semibold">Progress</h3>
        {hiddenCount > 0 && (
          <button
            type="button"
            onClick={() => setShowEarlier(true)}
            className="mb-1 flex cursor-pointer items-center gap-1 pl-6 text-[12px] text-muted hover:text-foreground"
          >
            <ChevronDown className="h-3.5 w-3.5" aria-hidden />
            {hiddenCount} earlier step{hiddenCount === 1 ? "" : "s"}
          </button>
        )}
        <ol className="relative space-y-0.5 border-l border-border pl-4">
          {visible.map((s) => (
            <li key={s.id} className="relative py-1">
              <span className="absolute -left-[23px] top-1.5 flex h-3.5 w-3.5 items-center justify-center rounded-full bg-background">
                {s.status === "done" && <Check className="h-3.5 w-3.5 text-success" aria-hidden />}
                {s.status === "current" && <Loader2 className="h-3.5 w-3.5 animate-spin text-primary" aria-hidden />}
                {s.status === "todo" && <Circle className="h-2.5 w-2.5 text-border" aria-hidden />}
                {s.status === "failed" && <X className="h-3.5 w-3.5 text-danger" aria-hidden />}
              </span>
              <span
                className={cx(
                  s.status === "done" && "text-muted",
                  s.status === "current" && "font-medium text-foreground",
                  s.status === "todo" && "text-muted/80",
                  s.status === "failed" && "text-danger",
                )}
              >
                {s.title}
              </span>
              <span className="sr-only"> ({s.status})</span>
            </li>
          ))}
        </ol>
      </section>

      <section aria-label="Outputs">
        <h3 className="mb-2 text-[13px] font-semibold">Outputs</h3>
        {plan.outputs.length === 0 ? (
          <p className="text-[13px] text-muted">Nothing yet. Reports and files appear here as the run makes them.</p>
        ) : (
          <ul className="space-y-1.5">
            {plan.outputs.map((o) => (
              <li key={o.id} className="flex items-center gap-2.5 rounded-lg border border-border bg-surface px-3 py-2">
                <FileText className="h-4 w-4 text-ai-foreground" aria-hidden />
                <span className="min-w-0 flex-1">
                  <span className="block truncate">{o.title}</span>
                  <span className="block text-[12px] text-muted">
                    {titleCase(o.type)} · {relative(o.at)}
                  </span>
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section aria-label="Used in this run">
        <h3 className="mb-2 text-[13px] font-semibold">Used in this run</h3>
        <dl className="space-y-1.5">
          {plan.used.map((u) => (
            <div key={u.label} className="flex justify-between gap-3 text-[13px]">
              <dt className="text-muted">{u.label}</dt>
              <dd className="truncate text-right font-mono text-[12px]">{u.value}</dd>
            </div>
          ))}
        </dl>
      </section>

      <section aria-label="Usage">
        <h3 className="mb-2 text-[13px] font-semibold">Usage</h3>
        <div className="mb-1 flex justify-between text-[13px]">
          <span>Context</span>
          <span className="tabular-nums text-muted">41.2k / 200k · 21%</span>
        </div>
        <div className="flex h-2 overflow-hidden rounded-full bg-surface-2" aria-hidden>
          <div className="bg-primary" style={{ width: "14%" }} />
          <div className="bg-ai" style={{ width: "4%" }} />
          <div className="bg-success" style={{ width: "3%" }} />
        </div>
        <ul className="mt-2 space-y-1 text-[12px] text-muted">
          <li className="flex items-center gap-2">
            <span className="h-2 w-2 rounded-full bg-primary" /> Messages 28.1k
          </li>
          <li className="flex items-center gap-2">
            <span className="h-2 w-2 rounded-full bg-ai" /> Tools 8.0k
          </li>
          <li className="flex items-center gap-2">
            <span className="h-2 w-2 rounded-full bg-success" /> System prompt and memory 5.1k
          </li>
        </ul>
        <p className="mt-2 text-[13px]">
          Cost so far <span className="font-medium">$0.18</span> <span className="text-muted">· 2% of today's budget</span>
        </p>
      </section>
    </div>
  );
}
