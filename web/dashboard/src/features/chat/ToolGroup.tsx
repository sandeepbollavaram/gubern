import { useEffect, useState } from "react";
import { Check, ChevronRight, Copy, FileSearch, Gauge, Loader2, Terminal, Wrench, X } from "lucide-react";
import type { ToolStep, ViewMode } from "../../data/model";
import { inline } from "../../ui/inline";
import { cx } from "../../ui/util";

const ICONS: Record<string, typeof Wrench> = { run_terminal: Terminal, read: FileSearch, metrics: Gauge };

function Elapsed({ since }: { since?: number }) {
  const [start] = useState(() => since ?? Date.now());
  const [now, setNow] = useState(start);
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  return <span className="tabular-nums">{Math.max(0, Math.round((now - start) / 1000))}s</span>;
}

function duration(ms?: number) {
  if (ms === undefined) return "";
  return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`;
}

function StepRow({ step, open, onToggle }: { step: ToolStep; open: boolean; onToggle: () => void }) {
  const [copied, setCopied] = useState(false);
  const command = /`([^`]+)`/.exec(step.label)?.[1];
  return (
    <li>
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={open}
        className="flex w-full cursor-pointer items-center gap-2 rounded px-2 py-1 text-left text-[13px] text-muted hover:bg-accent hover:text-foreground"
      >
        {step.status === "running" ? (
          <Loader2 className="h-3.5 w-3.5 animate-spin text-primary" aria-label="Running" />
        ) : step.status === "failed" ? (
          <X className="h-3.5 w-3.5 text-danger" aria-label="Failed" />
        ) : (
          <Check className="h-3.5 w-3.5 text-success" aria-label="Done" />
        )}
        <span className="min-w-0 flex-1 truncate">{inline(step.label)}</span>
        {step.durationMs !== undefined && <span className="shrink-0 text-[12px] tabular-nums">{duration(step.durationMs)}</span>}
      </button>
      {open && (
        <div className="mb-2 ml-6 mt-1 overflow-hidden rounded-lg border border-border bg-background">
          {command && (
            <div className="flex items-start gap-2 border-b border-border px-3 py-2 font-mono text-[12px]">
              <span className="select-none text-muted">$</span>
              <span className="flex-1 whitespace-pre-wrap break-all text-primary">{command}</span>
              <button
                type="button"
                aria-label="Copy command"
                onClick={() => {
                  void navigator.clipboard?.writeText(command);
                  setCopied(true);
                  setTimeout(() => setCopied(false), 1500);
                }}
                className="cursor-pointer text-muted hover:text-foreground"
              >
                {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
              </button>
            </div>
          )}
          <pre
            className={cx("max-h-56 overflow-auto px-3 py-2 font-mono text-[12px] leading-relaxed", step.status === "failed" ? "text-danger" : "text-muted")}
          >
            {step.detail ?? (step.status === "running" ? "Waiting for output…" : "No output.")}
          </pre>
        </div>
      )}
    </li>
  );
}

// ToolGroup shows consecutive steps as one quiet row ("Ran 3 commands,
// 1 failed ›") in Normal view; Verbose opens them all; the running step
// shows an elapsed timer.
export function ToolGroup({ tool, steps, viewMode }: { tool: string; steps: ToolStep[]; viewMode: ViewMode }) {
  const failed = steps.filter((s) => s.status === "failed").length;
  const running = steps.some((s) => s.status === "running");
  // The user's toggle wins; otherwise Verbose and failures start open.
  const [toggled, setToggled] = useState<boolean | null>(null);
  const open = toggled ?? (viewMode === "verbose" || failed > 0);
  const setOpen = (v: boolean) => setToggled(v);
  const [pickedStep, setOpenStep] = useState<string | null | undefined>(undefined);
  const openStep =
    pickedStep !== undefined ? pickedStep : viewMode === "verbose" ? "all" : failed ? (steps.find((s) => s.status === "failed")?.id ?? null) : null;
  const Icon = ICONS[tool] ?? Wrench;
  const noun = tool === "run_terminal" ? "command" : "step";
  const summary = running ? null : `${tool === "run_terminal" ? "Ran" : "Used tools for"} ${steps.length} ${noun}${steps.length === 1 ? "" : "s"}`;

  return (
    <div className="text-[13px]">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="flex cursor-pointer items-center gap-2 rounded py-1 text-muted hover:text-foreground"
      >
        {running ? (
          <span className="flex gap-0.5 text-ai-foreground" aria-hidden>
            •••
          </span>
        ) : (
          <Icon className="h-4 w-4" aria-hidden />
        )}
        <span>
          {running ? (
            <>
              <span className="text-foreground">Working</span> · <Elapsed since={steps.find((s) => s.status === "running")?.startedAt} />
            </>
          ) : (
            summary
          )}
          {failed > 0 && <span className="text-danger">, {failed} failed</span>}
        </span>
        <ChevronRight className={cx("h-3.5 w-3.5 transition-transform duration-150", open && "rotate-90")} aria-hidden />
      </button>
      {open && (
        <ul className="mt-0.5 border-l border-border pl-2">
          {steps.map((s) => (
            <StepRow key={s.id} step={s} open={openStep === "all" || openStep === s.id} onToggle={() => setOpenStep(openStep === s.id ? null : s.id)} />
          ))}
        </ul>
      )}
    </div>
  );
}
