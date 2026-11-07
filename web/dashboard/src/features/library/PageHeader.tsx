import type { ReactNode } from "react";

export function PageHeader({ title, body, action }: { title: string; body: string; action?: ReactNode }) {
  return (
    <header className="mb-6 flex items-end justify-between gap-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        <p className="mt-1 text-sm text-muted">{body}</p>
      </div>
      {action}
    </header>
  );
}
