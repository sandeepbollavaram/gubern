import type { ReactNode } from "react";

// inline renders **bold** and `code` spans as React elements; no HTML is
// ever injected, so model output cannot smuggle in markup.
export function inline(s: string): ReactNode[] {
  const out: ReactNode[] = [];
  const re = /(\*\*[^*]+\*\*|`[^`]+`)/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let k = 0;
  while ((m = re.exec(s))) {
    if (m.index > last) out.push(s.slice(last, m.index));
    const tok = m[0];
    if (tok.startsWith("**"))
      out.push(
        <strong key={k++} className="font-semibold">
          {tok.slice(2, -2)}
        </strong>,
      );
    else
      out.push(
        <code key={k++} className="rounded bg-surface-2 px-1 py-0.5 font-mono text-[0.85em]">
          {tok.slice(1, -1)}
        </code>,
      );
    last = m.index + tok.length;
  }
  if (last < s.length) out.push(s.slice(last));
  return out;
}
