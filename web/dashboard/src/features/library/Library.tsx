import { useNavigate } from "react-router";
import { CalendarClock, FileText, Hammer, Plus, ShieldCheck, Sparkles, Timer } from "lucide-react";
import { useOutputs } from "../../data/client";
import type { Output } from "../../data/model";
import { useUIStore } from "../../store";
import { Button, EmptyState, RelTime, Skeleton } from "../../ui/primitives";
import { titleCase } from "../../ui/util";
import { PageHeader } from "./PageHeader";

const ICON: Record<Output["type"], typeof FileText> = { report: FileText, build: Hammer, config: Sparkles, file: FileText };

export function Outputs() {
  const { data, isLoading } = useOutputs();
  const navigate = useNavigate();
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-5xl px-5 py-8">
        <PageHeader title="Outputs" body="Reports, builds and generated configs from agent runs." />
        {isLoading ? (
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {Array.from({ length: 6 }, (_, i) => (
              <Skeleton key={i} className="h-32" />
            ))}
          </div>
        ) : data?.length ? (
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {data.map((o) => {
              const Icon = ICON[o.type];
              return (
                <button
                  key={o.id}
                  type="button"
                  onClick={() => o.sessionId && navigate(`/chat/${o.sessionId}`)}
                  className="flex cursor-pointer gap-3 rounded-xl border border-border bg-surface p-4 text-left transition-colors duration-150 hover:border-primary/50"
                >
                  <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-surface-2">
                    <Icon className="h-5 w-5 text-ai-foreground" aria-hidden />
                  </span>
                  <span className="min-w-0">
                    <span className="block text-sm font-medium">{o.title}</span>
                    <span className="mt-1 block text-[12px] text-muted">
                      {titleCase(o.type)} · <RelTime at={o.at} />
                    </span>
                  </span>
                </button>
              );
            })}
          </div>
        ) : (
          <EmptyState
            icon={<Sparkles className="h-9 w-9" />}
            title="No outputs yet"
            body="When an agent writes a report or builds something, it appears here."
          />
        )}
      </div>
    </div>
  );
}

const TEMPLATES = [
  { icon: ShieldCheck, title: "Nightly health report", body: "Check every route and node, and send a summary to Telegram.", when: "Every day at 07:00" },
  { icon: CalendarClock, title: "Certificate expiry check", body: "Warn when any TLS certificate expires within 14 days.", when: "Every Monday at 09:00" },
  { icon: Hammer, title: "Dependency audit", body: "Run govulncheck and npm audit; open an issue for anything new.", when: "Every Friday at 16:00" },
  { icon: Timer, title: "Budget watch", body: "Alert when AI spend passes 80% of the monthly budget.", when: "Hourly" },
];

export function Automations() {
  const toast = useUIStore((s) => s.toast);
  const soon = () => toast({ text: "Scheduling needs the automations API (Phase 4).", tone: "info" });
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-4xl px-5 py-8">
        <PageHeader
          title="Automations"
          body="Agent tasks that run on a schedule."
          action={
            <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={soon}>
              New automation
            </Button>
          }
        />
        <EmptyState icon={<Timer className="h-9 w-9" />} title="No automations yet" body="Start from a template, or describe a task and a schedule." />
        <div className="mt-2 border-t border-dashed border-border pt-6">
          <h2 className="mb-3 text-[13px] font-semibold">Templates</h2>
          <ul className="grid gap-3 sm:grid-cols-2">
            {TEMPLATES.map((t) => (
              <li key={t.title}>
                <button
                  type="button"
                  onClick={soon}
                  className="flex w-full cursor-pointer gap-3 rounded-xl border border-border bg-surface p-4 text-left transition-colors duration-150 hover:border-primary/50"
                >
                  <t.icon className="mt-0.5 h-5 w-5 shrink-0 text-muted" aria-hidden />
                  <span>
                    <span className="block text-sm font-medium">{t.title}</span>
                    <span className="mt-0.5 block text-[13px] text-muted">{t.body}</span>
                    <span className="mt-2 flex items-center gap-1 text-[12px] text-muted">
                      <CalendarClock className="h-3.5 w-3.5" aria-hidden />
                      {t.when}
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}
