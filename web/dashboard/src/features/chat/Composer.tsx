import { useEffect, useRef, useState } from "react";
import { ArrowUp, ChevronDown, Folder, Paperclip, ShieldCheck, Square } from "lucide-react";
import { useModels, useWorkspaces } from "../../data/client";
import type { Mode } from "../../data/model";
import { useUIStore } from "../../store";
import { MenuItem, Popover } from "../../ui/primitives";
import { cx } from "../../ui/util";

const DRAFT_PREFIX = "gubern.draft.";

function loadDraft(key: string): string {
  try {
    return localStorage.getItem(DRAFT_PREFIX + key) ?? "";
  } catch {
    return "";
  }
}
function saveDraft(key: string, v: string) {
  try {
    if (v) localStorage.setItem(DRAFT_PREFIX + key, v);
    else localStorage.removeItem(DRAFT_PREFIX + key);
  } catch {
    /* drafts are a convenience */
  }
}

const MODES: { value: Mode; label: string; hint: string }[] = [
  { value: "agent", label: "Agent", hint: "Works in the workspace with local tools; asks before changes" },
  { value: "chat", label: "Chat", hint: "Answers only; no tools" },
  { value: "team", label: "Team", hint: "Specialist agents with review checkpoints" },
  { value: "research", label: "Research", hint: "Searches sources and writes a report" },
];

const EFFORTS = [
  { value: "low", label: "Low", hint: "Fastest; fine for quick questions" },
  { value: "medium", label: "Medium", hint: "Recommended for most work" },
  { value: "high", label: "High", hint: "More thorough; takes longer" },
  { value: "max", label: "Max", hint: "Uses about 3× more; for the hardest problems" },
] as const;

export interface ComposerProps {
  draftKey: string;
  running?: boolean;
  onSend: (text: string, opts: { queue: boolean }) => void;
  onStop?: () => void;
  autoFocus?: boolean;
  placeholder?: string;
  /** Compact composer above an open question tray. */
  compact?: boolean;
}

export function Composer({ draftKey, running, onSend, onStop, autoFocus, placeholder, compact }: ComposerProps) {
  const [text, setText] = useState(() => loadDraft(draftKey));
  const area = useRef<HTMLTextAreaElement | null>(null);
  const { data: models } = useModels();
  const { data: workspaces } = useWorkspaces();
  const model = useUIStore((s) => s.model);
  const effort = useUIStore((s) => s.effort);
  const mode = useUIStore((s) => s.mode);
  const workspace = useUIStore((s) => s.workspace);
  const setPref = useUIStore((s) => s.setPref);
  const openSettings = useUIStore((s) => s.openSettings);

  useEffect(() => saveDraft(draftKey, text), [draftKey, text]);
  useEffect(() => {
    if (autoFocus) area.current?.focus();
  }, [autoFocus, draftKey]);
  // Grow with content up to ~10 lines.
  useEffect(() => {
    const el = area.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 240)}px`;
  }, [text]);

  const submit = (queue: boolean) => {
    const t = text.trim();
    if (!t) return;
    onSend(t, { queue });
    setText("");
  };

  const current = models?.find((m) => m.id === model);
  const hint = running ? "Enter to send now · Ctrl+Enter to queue after this step" : (placeholder ?? "Ask Gubern, or describe a task");

  return (
    <div className="w-full">
      <div
        className={cx(
          "rounded-xl border border-border bg-surface shadow-sm transition-colors duration-150 focus-within:border-primary/60",
          compact ? "px-3 py-2" : "px-4 pb-2 pt-3",
        )}
      >
        <label className="sr-only" htmlFor={`composer-${draftKey}`}>
          Message
        </label>
        <textarea
          id={`composer-${draftKey}`}
          ref={area}
          rows={compact ? 1 : 2}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={hint}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              submit(running ? e.ctrlKey || e.metaKey : false);
            } else if (e.key === "Escape" && running && onStop) {
              e.preventDefault();
              onStop();
            }
          }}
          className="block w-full resize-none bg-transparent text-[15px] leading-relaxed outline-none placeholder:text-muted focus-visible:outline-none"
        />
        <div className="mt-1 flex items-center gap-1">
          <button
            type="button"
            aria-label="Attach a file"
            title="Attach a file"
            className="flex h-8 w-8 cursor-pointer items-center justify-center rounded text-muted hover:bg-accent hover:text-foreground"
          >
            <Paperclip className="h-4 w-4" />
          </button>
          <div className="flex-1" />
          {running && onStop && (
            <button
              type="button"
              onClick={onStop}
              aria-label="Stop the agent (Esc)"
              title="Stop (Esc)"
              className="flex h-8 w-8 cursor-pointer items-center justify-center rounded-full bg-foreground text-background hover:opacity-90"
            >
              <Square className="h-3.5 w-3.5 fill-current" />
            </button>
          )}
          <button
            type="button"
            onClick={() => submit(false)}
            disabled={!text.trim()}
            aria-label="Send (Enter)"
            title="Send (Enter)"
            className="flex h-8 w-8 cursor-pointer items-center justify-center rounded-full bg-primary text-primary-foreground transition-opacity duration-150 disabled:cursor-not-allowed disabled:opacity-30"
          >
            <ArrowUp className="h-4 w-4" />
          </button>
        </div>
      </div>

      {!compact && (
        <div className="mt-2 flex flex-wrap items-center gap-1 px-1 text-[13px]">
          <Popover
            label="Workspace"
            width="w-64"
            trigger={({ toggle, ref, open }) => (
              <button
                ref={ref}
                type="button"
                onClick={toggle}
                aria-expanded={open}
                className="flex h-7 cursor-pointer items-center gap-1.5 rounded px-2 text-muted hover:bg-accent hover:text-foreground"
              >
                <Folder className="h-3.5 w-3.5" aria-hidden />
                {workspace}
                <ChevronDown className="h-3.5 w-3.5" aria-hidden />
              </button>
            )}
          >
            {(close) => (
              <>
                <p className="px-2.5 pb-1 pt-1.5 text-[12px] text-muted">Agents work only inside the workspace folder.</p>
                {(workspaces ?? []).map((w) => (
                  <MenuItem key={w} checked={w === workspace} onSelect={() => (setPref("workspace", w), close())}>
                    <span className="font-mono text-[13px]">{w}</span>
                  </MenuItem>
                ))}
              </>
            )}
          </Popover>
          <Popover
            label="Mode"
            width="w-80"
            trigger={({ toggle, ref, open }) => (
              <button
                ref={ref}
                type="button"
                onClick={toggle}
                aria-expanded={open}
                className="flex h-7 cursor-pointer items-center gap-1 rounded px-2 text-muted hover:bg-accent hover:text-foreground"
              >
                {MODES.find((m) => m.value === mode)?.label}
                <ChevronDown className="h-3.5 w-3.5" aria-hidden />
              </button>
            )}
          >
            {(close) =>
              MODES.map((m) => (
                <MenuItem key={m.value} hint={m.hint} checked={m.value === mode} onSelect={() => (setPref("mode", m.value), close())}>
                  {m.label}
                </MenuItem>
              ))
            }
          </Popover>
          <div className="flex-1" />
          <Popover
            label="Model and effort"
            align="end"
            width="w-80"
            trigger={({ toggle, ref, open }) => (
              <button
                ref={ref}
                type="button"
                onClick={toggle}
                aria-expanded={open}
                className="flex h-7 cursor-pointer items-center gap-1.5 rounded px-2 hover:bg-accent"
              >
                <span className="text-foreground">{current?.label ?? model}</span>
                <span className="capitalize text-muted">{effort}</span>
                <ChevronDown className="h-3.5 w-3.5 text-muted" aria-hidden />
              </button>
            )}
          >
            {(close) => (
              <div
                onKeyDown={(e) => {
                  const n = Number(e.key);
                  if (n >= 1 && models && n <= models.length) {
                    setPref("model", models[n - 1].id);
                    close();
                  }
                }}
              >
                {(models ?? []).map((m, i) => (
                  <MenuItem
                    key={m.id}
                    hint={`${m.purpose} · ${m.provider}`}
                    checked={m.id === model}
                    shortcut={String(i + 1)}
                    onSelect={() => (setPref("model", m.id), close())}
                  >
                    {m.label}
                  </MenuItem>
                ))}
                <div className="my-1 border-t border-border" />
                <p className="px-2.5 pb-1 pt-1 text-[12px] text-muted">Effort: higher means more thorough answers, but slower and costlier.</p>
                <div className="grid grid-cols-4 gap-1 px-1.5 pb-1.5" role="radiogroup" aria-label="Effort">
                  {EFFORTS.map((ef) => (
                    <button
                      key={ef.value}
                      type="button"
                      role="radio"
                      aria-checked={effort === ef.value}
                      title={ef.hint}
                      onClick={() => setPref("effort", ef.value)}
                      className={cx(
                        "cursor-pointer rounded border px-1 py-1 text-[12px]",
                        effort === ef.value ? "border-primary bg-primary/10 text-foreground" : "border-border text-muted hover:text-foreground",
                      )}
                    >
                      {ef.label}
                    </button>
                  ))}
                </div>
                <div className="my-1 border-t border-border" />
                <MenuItem onSelect={() => (close(), openSettings("models"))}>More models and providers…</MenuItem>
              </div>
            )}
          </Popover>
          <button
            type="button"
            onClick={() => openSettings("agent")}
            title="Approval policy: change in Settings → Agent"
            className="flex h-7 cursor-pointer items-center gap-1 rounded px-2 text-muted hover:bg-accent hover:text-foreground"
          >
            <ShieldCheck className="h-3.5 w-3.5" aria-hidden />
            Manual
          </button>
        </div>
      )}
    </div>
  );
}
