import { useEffect, useState } from "react";
import { ChevronLeft, ChevronRight, Pencil, X } from "lucide-react";
import { client } from "../../data/client";
import type { QuestionSet } from "../../data/model";
import { cx } from "../../ui/util";

// QuestionTray implements UX spec F3b. It docks above the composer: one
// question at a time, number keys pick an option, "Something else" takes free
// text, and Skip moves on. Answers sync live with other channels.
export function QuestionTray({ set, onClose }: { set: QuestionSet; onClose: () => void }) {
  const firstOpen = Math.max(
    0,
    set.questions.findIndex((q) => !(q.key in set.answers)),
  );
  // Paging is the user's choice; otherwise show the first unanswered question,
  // which advances by itself as answers arrive (here or from another channel).
  const [picked, setIndex] = useState<number | null>(null);
  const index = picked ?? firstOpen;
  const [other, setOther] = useState("");
  const q = set.questions[index];
  const answer = (value: string) => {
    client.answer(set.id, q.key, value);
    setIndex(null);
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement).tagName;
      if (tag === "INPUT" || tag === "TEXTAREA") return;
      const n = Number(e.key);
      if (q && n >= 1 && n <= q.options.length) {
        answer(q.options[n - 1].value);
        e.preventDefault();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  if (!q) return null;
  const answered = set.answers[q.key];

  return (
    <section aria-label="Questions from the agent" className="mb-2 rounded-xl border border-border bg-surface shadow-lg animate-fade-in">
      <header className="flex items-center gap-2 px-4 pb-2 pt-3">
        <h3 className="flex-1 text-sm font-semibold">{q.title}</h3>
        <button
          type="button"
          aria-label="Previous question"
          disabled={index === 0}
          onClick={() => setIndex(index - 1)}
          className="cursor-pointer rounded p-1 text-muted hover:text-foreground disabled:opacity-30"
        >
          <ChevronLeft className="h-4 w-4" />
        </button>
        <span className="text-[12px] tabular-nums text-muted">
          {index + 1} of {set.questions.length}
        </span>
        <button
          type="button"
          aria-label="Next question"
          disabled={index === set.questions.length - 1}
          onClick={() => setIndex(index + 1)}
          className="cursor-pointer rounded p-1 text-muted hover:text-foreground disabled:opacity-30"
        >
          <ChevronRight className="h-4 w-4" />
        </button>
        <button
          type="button"
          aria-label="Close questions (they stay in your Inbox)"
          onClick={onClose}
          className="cursor-pointer rounded p-1 text-muted hover:text-foreground"
        >
          <X className="h-4 w-4" />
        </button>
      </header>
      <ul className="px-2">
        {q.options.map((o, i) => (
          <li key={o.value}>
            <button
              type="button"
              onClick={() => answer(o.value)}
              aria-pressed={answered === o.value}
              className={cx(
                "flex w-full cursor-pointer items-start gap-3 rounded-lg px-2 py-2 text-left transition-colors duration-150 hover:bg-accent",
                answered === o.value && "bg-selected",
              )}
            >
              <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded border border-border text-[12px] text-muted">{i + 1}</span>
              <span>
                <span className="block text-sm">{o.label}</span>
                {o.hint && <span className="block text-[12px] text-muted">{o.hint}</span>}
              </span>
            </button>
          </li>
        ))}
      </ul>
      <form
        className="flex items-center gap-2 border-t border-border px-4 py-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (other.trim()) {
            answer(other.trim());
            setOther("");
          }
        }}
      >
        <Pencil className="h-4 w-4 text-muted" aria-hidden />
        <input
          value={other}
          onChange={(e) => setOther(e.target.value)}
          placeholder="Something else"
          aria-label="Answer in your own words"
          className="h-8 flex-1 bg-transparent text-sm outline-none placeholder:text-muted"
        />
        <button
          type="button"
          onClick={() => {
            client.skipQuestion(set.id, q.key);
            setIndex(null);
          }}
          className="h-7 cursor-pointer rounded bg-surface-2 px-3 text-[13px] hover:bg-accent"
        >
          Skip
        </button>
      </form>
    </section>
  );
}
