import { inline } from "./inline";

// RichText renders the small markdown subset agents and skills produce:
// headings, paragraphs, numbered and bulleted lists, fenced code blocks,
// **bold** and `code`. It builds React elements only and never injects HTML,
// so model or repository text cannot smuggle in markup.
export function RichText({ text, streaming }: { text: string; streaming?: boolean }) {
  // Fenced code first: its blank lines must not split it into paragraphs.
  const parts = text.split(/^```[^\n]*\n?/m);
  const nodes: React.ReactNode[] = [];
  parts.forEach((part, pi) => {
    const lastPart = pi === parts.length - 1;
    if (pi % 2 === 1) {
      nodes.push(
        <pre key={`c${pi}`} className="overflow-x-auto rounded-lg border border-border bg-background p-3 font-mono text-[13px] leading-relaxed">
          <code>{part.replace(/\n$/, "")}</code>
        </pre>,
      );
      return;
    }
    const blocks = part.split(/\n{2,}/).filter((b) => b.trim() !== "");
    blocks.forEach((block, i) => {
      const key = `b${pi}-${i}`;
      const lines = block.split("\n").filter((l) => l !== "");
      const last = lastPart && i === blocks.length - 1;
      if (lines.every((l) => /^\d+\.\s/.test(l))) {
        nodes.push(
          <ol key={key} className="list-decimal space-y-1 pl-5">
            {lines.map((l, j) => (
              <li key={j}>{inline(l.replace(/^\d+\.\s/, ""))}</li>
            ))}
          </ol>,
        );
        return;
      }
      if (lines.every((l) => /^[-*]\s/.test(l))) {
        nodes.push(
          <ul key={key} className="list-disc space-y-1 pl-5">
            {lines.map((l, j) => (
              <li key={j}>{inline(l.replace(/^[-*]\s/, ""))}</li>
            ))}
          </ul>,
        );
        return;
      }
      // A heading line starts its own block; any lines after it are a paragraph.
      const h = /^(#{1,3})\s+(.*)$/.exec(lines[0] ?? "");
      if (h) {
        const cls = h[1].length === 1 ? "text-[17px] font-semibold" : h[1].length === 2 ? "text-[15px] font-semibold" : "text-[14px] font-semibold text-muted";
        nodes.push(
          <p key={`${key}h`} role="heading" aria-level={h[1].length} className={cls}>
            {inline(h[2])}
          </p>,
        );
        lines.shift();
        if (!lines.length) return;
      }
      nodes.push(
        <p key={key}>
          {lines.map((l, j) => (
            <span key={j}>
              {j > 0 && <br />}
              {inline(l)}
            </span>
          ))}
          {streaming && last && <span className="ml-0.5 inline-block h-4 w-1.5 translate-y-0.5 animate-pulse bg-primary/70" aria-hidden />}
        </p>,
      );
    });
  });
  return <div className="space-y-3 leading-relaxed">{nodes}</div>;
}
