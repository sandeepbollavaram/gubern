import { inline } from "./inline";

// RichText renders the small markdown subset agents produce: paragraphs,
// numbered and bulleted lists, **bold** and `code`. It builds React elements
// only and never injects HTML, so model output cannot smuggle in markup.
export function RichText({ text, streaming }: { text: string; streaming?: boolean }) {
  const blocks = text.split(/\n{2,}/);
  return (
    <div className="space-y-3 leading-relaxed">
      {blocks.map((block, i) => {
        const lines = block.split("\n");
        const last = i === blocks.length - 1;
        if (lines.every((l) => /^\d+\.\s/.test(l))) {
          return (
            <ol key={i} className="list-decimal space-y-1 pl-5">
              {lines.map((l, j) => (
                <li key={j}>{inline(l.replace(/^\d+\.\s/, ""))}</li>
              ))}
            </ol>
          );
        }
        if (lines.every((l) => /^[-*]\s/.test(l))) {
          return (
            <ul key={i} className="list-disc space-y-1 pl-5">
              {lines.map((l, j) => (
                <li key={j}>{inline(l.replace(/^[-*]\s/, ""))}</li>
              ))}
            </ul>
          );
        }
        return (
          <p key={i}>
            {lines.map((l, j) => (
              <span key={j}>
                {j > 0 && <br />}
                {inline(l)}
              </span>
            ))}
            {streaming && last && <span className="ml-0.5 inline-block h-4 w-1.5 translate-y-0.5 animate-pulse bg-primary/70" aria-hidden />}
          </p>
        );
      })}
    </div>
  );
}
