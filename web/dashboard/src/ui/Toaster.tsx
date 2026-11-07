import { CheckCircle2, Info, X, XCircle } from "lucide-react";
import { useUIStore } from "../store";
import { cx } from "./util";

// Toaster: bottom-right stack, announced to screen readers. Toasts with an
// action (Undo) stay longer (see store.toast).
export function Toaster() {
  const toasts = useUIStore((s) => s.toasts);
  const dismiss = useUIStore((s) => s.dismissToast);
  return (
    <div className="pointer-events-none fixed bottom-4 right-4 z-toast flex w-80 flex-col gap-2" role="status" aria-live="polite">
      {toasts.map((t) => {
        const Icon = t.tone === "success" ? CheckCircle2 : t.tone === "error" ? XCircle : Info;
        return (
          <div
            key={t.id}
            className={cx(
              "pointer-events-auto flex animate-fade-in items-start gap-2.5 rounded-lg border bg-surface px-3 py-2.5 text-sm shadow-xl",
              t.tone === "error" ? "border-danger/50" : "border-border",
            )}
          >
            <Icon
              className={cx("mt-0.5 h-4 w-4 shrink-0", t.tone === "success" ? "text-success" : t.tone === "error" ? "text-danger" : "text-primary")}
              aria-hidden
            />
            <span className="flex-1">{t.text}</span>
            {t.action && (
              <button
                type="button"
                className="cursor-pointer font-medium text-primary hover:underline"
                onClick={() => {
                  t.action?.run();
                  dismiss(t.id);
                }}
              >
                {t.action.label}
              </button>
            )}
            <button type="button" aria-label="Dismiss" className="cursor-pointer text-muted hover:text-foreground" onClick={() => dismiss(t.id)}>
              <X className="h-4 w-4" aria-hidden />
            </button>
          </div>
        );
      })}
    </div>
  );
}
