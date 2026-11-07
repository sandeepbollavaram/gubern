// Shared UI primitives for the new dashboard. Rules baked in (UX spec §6):
// visible focus rings, aria-labels on icon-only buttons, busy states that
// disable while in flight, status never shown by colour alone.
import { forwardRef, useEffect, useId, useRef, useState } from "react";
import type { ButtonHTMLAttributes, ReactNode } from "react";
import { AlertTriangle, CheckCircle2, Loader2, OctagonAlert, ShieldAlert, XCircle } from "lucide-react";
import type { Risk, RunStatus } from "../data/model";
import { cx, relative, statusMeta } from "./util";

type Variant = "primary" | "secondary" | "ghost" | "danger";

const variants: Record<Variant, string> = {
  primary: "bg-primary text-primary-foreground hover:bg-primary/90",
  secondary: "bg-surface-2 text-foreground border border-border hover:bg-accent",
  ghost: "text-foreground hover:bg-accent",
  danger: "bg-danger/15 text-danger border border-danger/40 hover:bg-danger/25",
};

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: "sm" | "md";
  busy?: boolean;
  icon?: ReactNode;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "secondary", size = "md", busy, icon, className, children, disabled, ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type="button"
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      className={cx(
        "inline-flex cursor-pointer select-none items-center justify-center gap-1.5 rounded font-medium transition-colors duration-150",
        "disabled:cursor-not-allowed disabled:opacity-50",
        size === "sm" ? "h-8 px-2.5 text-[13px]" : "h-9 px-3.5 text-sm",
        variants[variant],
        className,
      )}
      {...rest}
    >
      {busy ? <Loader2 className="h-4 w-4 animate-spin" aria-hidden /> : icon}
      {children}
    </button>
  );
});

export interface IconButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  label: string;
  active?: boolean;
}

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton({ label, active, className, children, ...rest }, ref) {
  return (
    <button
      ref={ref}
      type="button"
      aria-label={label}
      title={label}
      aria-pressed={active}
      className={cx(
        "inline-flex h-8 w-8 cursor-pointer items-center justify-center rounded text-muted transition-colors duration-150 hover:bg-accent hover:text-foreground",
        active && "bg-accent text-foreground",
        className,
      )}
      {...rest}
    >
      {children}
    </button>
  );
});

export function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="inline-flex min-w-[1.5rem] items-center justify-center rounded border border-border bg-surface-2 px-1.5 font-mono text-[11px] text-muted">
      {children}
    </kbd>
  );
}

// RiskBadge: colour + icon + word, so risk reads without colour vision.
export function RiskBadge({ risk }: { risk: Risk }) {
  const map: Record<Risk, { cls: string; Icon: typeof ShieldAlert; text: string }> = {
    "LOW RISK": { cls: "text-success border-success/40 bg-success/10", Icon: CheckCircle2, text: "Low risk" },
    "MEDIUM RISK": { cls: "text-warning border-warning/40 bg-warning/10", Icon: AlertTriangle, text: "Medium risk" },
    "HIGH RISK": { cls: "text-danger border-danger/40 bg-danger/10", Icon: ShieldAlert, text: "High risk" },
    CRITICAL: { cls: "text-danger border-danger bg-danger/20", Icon: OctagonAlert, text: "Critical" },
  };
  const { cls, Icon, text } = map[risk];
  return (
    <span className={cx("inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide", cls)}>
      <Icon className="h-3.5 w-3.5" aria-hidden />
      {text}
    </span>
  );
}

export function StatusIcon({ status, className }: { status: RunStatus; className?: string }) {
  const m = statusMeta[status];
  return (
    <span title={m.label} className={cx("inline-flex", m.cls, className)}>
      <m.Icon className={cx("h-4 w-4", m.spin && "animate-spin")} aria-hidden />
      <span className="sr-only">{m.label}</span>
    </span>
  );
}

export function Toggle({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cx(
        "relative inline-flex h-6 w-10 shrink-0 cursor-pointer items-center rounded-full transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50",
        checked ? "bg-primary" : "bg-border",
      )}
    >
      <span
        className={cx(
          "inline-block h-5 w-5 rounded-full bg-white shadow transition-transform duration-150",
          checked ? "translate-x-[18px]" : "translate-x-0.5",
        )}
      />
    </button>
  );
}

export function Segmented<T extends string>({
  value,
  options,
  onChange,
  label,
}: {
  value: T;
  options: { value: T; label: string }[];
  onChange: (v: T) => void;
  label: string;
}) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex rounded border border-border bg-surface-2 p-0.5">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={value === o.value}
          onClick={() => onChange(o.value)}
          className={cx(
            "cursor-pointer rounded px-3 py-1 text-[13px] transition-colors duration-150",
            value === o.value ? "bg-surface text-foreground shadow-sm" : "text-muted hover:text-foreground",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cx("animate-pulse rounded bg-surface-2", className)} aria-hidden />;
}

export function EmptyState({ icon, title, body, action }: { icon: ReactNode; title: string; body?: string; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-3 px-6 py-12 text-center">
      <div className="text-muted">{icon}</div>
      <p className="text-sm font-medium text-foreground">{title}</p>
      {body && <p className="max-w-sm text-sm text-muted">{body}</p>}
      {action}
    </div>
  );
}

export function InlineError({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div role="alert" className="flex items-center justify-between gap-3 rounded border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-danger">
      <span className="flex items-center gap-2">
        <XCircle className="h-4 w-4" aria-hidden />
        {message}
      </span>
      {onRetry && (
        <Button size="sm" variant="ghost" onClick={onRetry}>
          Retry
        </Button>
      )}
    </div>
  );
}

// SettingRow: title + description on the left, one control on the right.
export function SettingRow({ title, description, children, indent }: { title: string; description?: ReactNode; children: ReactNode; indent?: boolean }) {
  return (
    <div className={cx("flex items-center justify-between gap-6 border-b border-border py-4 last:border-b-0", indent && "pl-6")}>
      <div className="min-w-0">
        <p className="text-sm font-medium text-foreground">{title}</p>
        {description && <p className="mt-0.5 text-[13px] text-muted">{description}</p>}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  );
}

// Popover: anchored floating panel that closes on outside click or Escape and
// returns focus to its trigger.
export function Popover({
  trigger,
  children,
  align = "start",
  side = "bottom",
  width = "w-72",
  label,
}: {
  trigger: (props: { open: boolean; toggle: () => void; ref: React.RefObject<HTMLButtonElement | null>; id: string }) => ReactNode;
  children: (close: () => void) => ReactNode;
  align?: "start" | "end";
  side?: "bottom" | "top";
  width?: string;
  label: string;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLButtonElement | null>(null);
  const panel = useRef<HTMLDivElement | null>(null);
  const wasOpen = useRef(false);
  const id = useId();
  // Return focus to the trigger whenever the panel closes.
  useEffect(() => {
    if (wasOpen.current && !open) ref.current?.focus();
    wasOpen.current = open;
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!panel.current?.contains(e.target as Node) && !ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey, true);
    panel.current?.querySelector<HTMLElement>("[role=menuitem],[role=menuitemradio],button,input")?.focus();
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey, true);
    };
  }, [open]);
  const close = () => setOpen(false);
  return (
    <div className="relative inline-flex">
      {trigger({ open, toggle: () => setOpen((o) => !o), ref, id })}
      {open && (
        <div
          ref={panel}
          id={id}
          role="menu"
          aria-label={label}
          className={cx(
            "absolute z-overlay animate-fade-in rounded-lg border border-border bg-surface p-1 shadow-xl",
            width,
            side === "bottom" ? "top-full mt-1" : "bottom-full mb-1",
            align === "start" ? "left-0" : "right-0",
          )}
          onKeyDown={(e) => {
            if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
            const items = Array.from(panel.current?.querySelectorAll<HTMLElement>("[role=menuitem],[role=menuitemradio]") ?? []);
            const i = items.indexOf(document.activeElement as HTMLElement);
            const next = items[(i + (e.key === "ArrowDown" ? 1 : items.length - 1)) % items.length];
            next?.focus();
            e.preventDefault();
          }}
        >
          {children(close)}
        </div>
      )}
    </div>
  );
}

export function MenuItem({
  children,
  onSelect,
  hint,
  checked,
  danger,
  icon,
  shortcut,
}: {
  children: ReactNode;
  onSelect: () => void;
  hint?: string;
  checked?: boolean;
  danger?: boolean;
  icon?: ReactNode;
  shortcut?: string;
}) {
  return (
    <button
      type="button"
      role={checked === undefined ? "menuitem" : "menuitemradio"}
      aria-checked={checked}
      onClick={onSelect}
      className={cx(
        "flex w-full cursor-pointer items-start gap-2.5 rounded px-2.5 py-2 text-left text-sm transition-colors duration-150 hover:bg-accent focus:bg-accent focus:outline-none",
        danger ? "text-danger" : "text-foreground",
      )}
    >
      {icon && <span className="mt-0.5 text-muted">{icon}</span>}
      <span className="min-w-0 flex-1">
        <span className="block">{children}</span>
        {hint && <span className="block text-[12px] text-muted">{hint}</span>}
      </span>
      {shortcut && <span className="mt-0.5 font-mono text-[11px] text-muted">{shortcut}</span>}
      {checked && <CheckCircle2 className="mt-0.5 h-4 w-4 text-primary" aria-hidden />}
    </button>
  );
}

// Dialog: modal with backdrop, Escape to close, focus moved in and trapped,
// and focus restored to the opener on close.
export function Dialog({
  open,
  onClose,
  title,
  children,
  className,
  hideTitle,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  className?: string;
  hideTitle?: boolean;
}) {
  const box = useRef<HTMLDivElement | null>(null);
  const id = useId();
  useEffect(() => {
    if (!open) return;
    const opener = document.activeElement as HTMLElement | null;
    const focusables = () =>
      Array.from(box.current?.querySelectorAll<HTMLElement>("a[href],button:not([disabled]),input,textarea,select,[tabindex]:not([tabindex='-1'])") ?? []);
    (box.current?.querySelector<HTMLElement>("[data-autofocus]") ?? focusables()[0])?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      } else if (e.key === "Tab") {
        const f = focusables();
        if (f.length === 0) return;
        const first = f[0];
        const last = f[f.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          last.focus();
          e.preventDefault();
        } else if (!e.shiftKey && document.activeElement === last) {
          first.focus();
          e.preventDefault();
        }
      }
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      opener?.focus?.();
    };
  }, [open, onClose]);
  if (!open) return null;
  return (
    <div
      className="fixed inset-0 z-modal flex items-start justify-center bg-black/60 p-4 pt-[8vh] animate-fade-in"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div
        ref={box}
        role="dialog"
        aria-modal="true"
        aria-labelledby={id}
        className={cx("w-full overflow-hidden rounded-xl border border-border bg-surface shadow-2xl", className)}
      >
        <h2 id={id} className={hideTitle ? "sr-only" : "border-b border-border px-5 py-3.5 text-[15px] font-semibold"}>
          {title}
        </h2>
        {children}
      </div>
    </div>
  );
}

// Relative time with the exact timestamp available on hover.
export function RelTime({ at }: { at: number }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);
  return (
    <time dateTime={new Date(at).toISOString()} title={new Date(at).toLocaleString()}>
      {relative(at, now)}
    </time>
  );
}

export function Countdown({ until }: { until: number }) {
  const [left, setLeft] = useState(() => until - Date.now());
  useEffect(() => {
    const t = setInterval(() => setLeft(until - Date.now()), 1000);
    return () => clearInterval(t);
  }, [until]);
  if (left <= 0) return <span>expired</span>;
  const m = Math.floor(left / 60_000);
  const s = Math.floor((left % 60_000) / 1000);
  return (
    <span className={cx("tabular-nums", left < 120_000 && "text-danger")}>
      {m}:{String(s).padStart(2, "0")}
    </span>
  );
}
