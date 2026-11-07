import { CheckCircle2, CircleDot, Loader2, ShieldAlert, XCircle } from "lucide-react";
import type { RunStatus } from "../data/model";

export function cx(...parts: (string | false | null | undefined)[]) {
  return parts.filter(Boolean).join(" ");
}

export function relative(at: number, now = Date.now()): string {
  const s = Math.round((now - at) / 1000);
  if (s < 0) return `in ${Math.ceil(-s / 60)} min`;
  if (s < 45) return "just now";
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86_400) return `${Math.round(s / 3600)} h ago`;
  if (s < 172_800) return "yesterday";
  return new Date(at).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

export const statusMeta: Record<RunStatus, { label: string; cls: string; Icon: typeof CircleDot; spin?: boolean }> = {
  running: { label: "Running", cls: "text-primary", Icon: Loader2, spin: true },
  "needs-approval": { label: "Needs approval", cls: "text-warning", Icon: ShieldAlert },
  "needs-answer": { label: "Needs an answer", cls: "text-warning", Icon: CircleDot },
  failed: { label: "Failed", cls: "text-danger", Icon: XCircle },
  done: { label: "Done", cls: "text-muted", Icon: CheckCircle2 },
};

export const titleCase = (s: string) => (s ? s[0].toUpperCase() + s.slice(1) : s);
