import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";
import { ArrowDown, ChevronDown, Download, ListChecks, Pencil, Pin, Trash2 } from "lucide-react";
import { client, useIsRunning, usePlan, useQuestions, useSessions, useTranscript } from "../../data/client";
import type { TranscriptItem, ViewMode } from "../../data/model";
import { useUIStore } from "../../store";
import { RichText } from "../../ui/RichText";
import { EmptyState, MenuItem, Popover, StatusIcon } from "../../ui/primitives";
import { cx } from "../../ui/util";
import { ApprovalCard } from "./ApprovalCard";
import { Composer } from "./Composer";
import { QuestionTray } from "./QuestionTray";
import { ToolGroup } from "./ToolGroup";

function Item({ item, viewMode, highlightApproval }: { item: TranscriptItem; viewMode: ViewMode; highlightApproval?: string }) {
  switch (item.kind) {
    case "user":
      return (
        <div className="flex justify-end">
          <div
            className={cx(
              "max-w-[85%] rounded-xl bg-surface-2 px-4 py-2.5 text-[15px] leading-relaxed",
              item.queued && "border border-dashed border-border opacity-70",
            )}
          >
            {item.text}
            {item.queued && <p className="mt-1 text-[12px] text-muted">Will send after this step</p>}
          </div>
        </div>
      );
    case "assistant":
      return (
        <div className="text-[15px]">
          <RichText text={item.text || " "} streaming={item.streaming} />
        </div>
      );
    case "tools":
      return <ToolGroup tool={item.tool} steps={item.steps} viewMode={viewMode} />;
    case "approval":
      return <ApprovalCard approvalId={item.approvalId} highlight={item.approvalId === highlightApproval} />;
    case "status":
      return (
        <p
          className={cx(
            "inline-flex items-center gap-2 rounded-full px-2.5 py-0.5 text-[12px] font-medium",
            item.tone === "warning" ? "bg-warning/15 text-warning" : item.tone === "danger" ? "bg-danger/15 text-danger" : "bg-ai/15 text-ai-foreground",
          )}
        >
          <span className="h-1.5 w-1.5 rounded-full bg-current" aria-hidden />
          {item.text}
        </p>
      );
    case "answers":
      return (
        <dl className="rounded-lg border border-border bg-surface px-4 py-2.5 text-[13px]">
          {item.pairs.map((p) => (
            <div key={p.label} className="flex gap-2 py-0.5">
              <dt className="text-muted">{p.label}:</dt>
              <dd>{p.value}</dd>
            </div>
          ))}
        </dl>
      );
  }
}

function exportMarkdown(title: string, items: TranscriptItem[]) {
  const lines = [`# ${title}`, ""];
  for (const i of items) {
    if (i.kind === "user") lines.push(`**You:** ${i.text}`, "");
    if (i.kind === "assistant") lines.push(i.text, "");
    if (i.kind === "tools") lines.push(...i.steps.map((s) => `- ${s.status === "failed" ? "✗" : "✓"} ${s.label}`), "");
  }
  const url = URL.createObjectURL(new Blob([lines.join("\n")], { type: "text/markdown" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = `${title.replace(/[^\w-]+/g, "-").slice(0, 60)}.md`;
  a.click();
  URL.revokeObjectURL(url);
}

// Keyed by session id so each conversation starts with fresh local state
// (view mode, tray, scroll) instead of resetting it in effects.
export function ChatPage() {
  const { id = "" } = useParams();
  return <Conversation key={id} id={id} />;
}

function Conversation({ id }: { id: string }) {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const items = useTranscript(id);
  const running = useIsRunning(id);
  const questions = useQuestions(id);
  const plan = usePlan(id);
  const { data: sessions } = useSessions();
  const session = sessions?.find((s) => s.id === id);
  const defaultView = useUIStore((s) => s.viewMode);
  const [viewMode, setViewMode] = useState<ViewMode>(defaultView);
  const [trayClosed, setTrayClosed] = useState(false);
  const togglePanel = useUIStore((s) => s.togglePanel);
  const panel = useUIStore((s) => s.panel);
  const openPanel = useUIStore((s) => s.openPanel);
  const toast = useUIStore((s) => s.toast);
  const scroller = useRef<HTMLDivElement | null>(null);
  const [atBottom, setAtBottom] = useState(true);
  const [renaming, setRenaming] = useState(false);
  const [draftTitle, setDraftTitle] = useState("");
  // Long runs open their progress panel by default.
  useEffect(() => {
    if (plan && !panel) openPanel({ type: "run", sessionId: id });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, !!plan]);

  // Stick to the bottom while output streams, unless the user scrolled up.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && atBottom) el.scrollTop = el.scrollHeight;
  }, [items, atBottom]);

  const openQuestions = questions && Object.keys(questions.answers).length < questions.questions.length ? questions : undefined;
  const title = session?.title ?? "Conversation";
  const currentStep = plan ? plan.steps.findIndex((s) => s.status === "current") + 1 : 0;

  const send = (text: string, { queue }: { queue: boolean }) => {
    if (running && !queue) client.stop(id);
    void client.send(id, text, { mode: "agent", model: useUIStore.getState().model, workspace: useUIStore.getState().workspace });
    setAtBottom(true);
  };

  if (!session && sessions) {
    return <EmptyState icon={<ListChecks className="h-8 w-8" />} title="This conversation doesn't exist" body="It may have been deleted." />;
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center gap-2 border-b border-border px-4">
        {session && session.status !== "done" && <StatusIcon status={session.status} />}
        {renaming ? (
          <form
            className="min-w-0 flex-1"
            onSubmit={(e) => {
              e.preventDefault();
              if (draftTitle.trim()) client.rename(id, draftTitle.trim());
              setRenaming(false);
            }}
          >
            <input
              autoFocus
              aria-label="Conversation name"
              value={draftTitle}
              onChange={(e) => setDraftTitle(e.target.value)}
              onBlur={() => setRenaming(false)}
              onKeyDown={(e) => e.key === "Escape" && setRenaming(false)}
              className="h-8 w-full max-w-md rounded border border-primary bg-surface px-2 text-[15px] font-medium outline-none"
            />
          </form>
        ) : (
          <Popover
            label="Conversation actions"
            width="w-64"
            trigger={({ toggle, ref, open }) => (
              <button
                ref={ref}
                type="button"
                onClick={toggle}
                aria-expanded={open}
                className="flex min-w-0 cursor-pointer items-center gap-1 rounded px-2 py-1 text-[15px] font-medium hover:bg-accent"
              >
                <span className="truncate">{title}</span>
                <ChevronDown className="h-4 w-4 shrink-0 text-muted" aria-hidden />
              </button>
            )}
          >
            {(close) => (
              <>
                <p className="px-2.5 pb-1 pt-1.5 text-[12px] text-muted">Activity view</p>
                {(["normal", "thinking", "verbose"] as const).map((v) => (
                  <MenuItem
                    key={v}
                    checked={viewMode === v}
                    onSelect={() => (setViewMode(v), close())}
                    hint={
                      v === "normal"
                        ? "Steps collapsed; results shown"
                        : v === "thinking"
                          ? "Also shows the agent's reasoning"
                          : "Every tool call and its output"
                    }
                  >
                    <span className="capitalize">{v}</span>
                  </MenuItem>
                ))}
                <div className="my-1 border-t border-border" />
                <MenuItem icon={<Pin className="h-4 w-4" />} onSelect={() => (client.setPinned(id, !session?.pinned), close())}>
                  {session?.pinned ? "Unpin" : "Pin"}
                </MenuItem>
                <MenuItem
                  icon={<Pencil className="h-4 w-4" />}
                  onSelect={() => {
                    close();
                    setDraftTitle(title);
                    setRenaming(true);
                  }}
                >
                  Rename
                </MenuItem>
                <MenuItem icon={<Download className="h-4 w-4" />} onSelect={() => (close(), exportMarkdown(title, items))}>
                  Export as Markdown
                </MenuItem>
                <MenuItem
                  danger
                  icon={<Trash2 className="h-4 w-4" />}
                  onSelect={() => {
                    close();
                    client.remove(id);
                    toast({ text: `Deleted “${title}”`, tone: "info" });
                    navigate("/");
                  }}
                >
                  Delete
                </MenuItem>
              </>
            )}
          </Popover>
        )}
        <span className="flex-1" />
        {plan && (
          <button
            type="button"
            onClick={() => togglePanel({ type: "run", sessionId: id })}
            aria-pressed={panel?.type === "run"}
            className="flex h-8 cursor-pointer items-center gap-1.5 rounded border border-border px-2.5 text-[13px] hover:bg-accent"
          >
            <ListChecks className="h-4 w-4 text-primary" aria-hidden />
            Step {currentStep} of {plan.steps.length}
          </button>
        )}
      </header>

      <div
        ref={scroller}
        onScroll={(e) => {
          const el = e.currentTarget;
          setAtBottom(el.scrollHeight - el.scrollTop - el.clientHeight < 80);
        }}
        className="relative flex-1 overflow-y-auto"
      >
        <div className="mx-auto max-w-3xl space-y-5 px-5 py-6" aria-live="polite" aria-relevant="additions">
          {items.map((item) => (
            <Item key={item.id} item={item} viewMode={viewMode} highlightApproval={params.get("approval") ?? undefined} />
          ))}
        </div>
      </div>

      <div className="relative shrink-0 px-5 pb-4">
        {!atBottom && (
          <button
            type="button"
            onClick={() => setAtBottom(true)}
            className="absolute -top-12 left-1/2 flex -translate-x-1/2 cursor-pointer items-center gap-1 rounded-full border border-border bg-surface px-3 py-1.5 text-[12px] shadow-lg hover:bg-accent"
          >
            <ArrowDown className="h-3.5 w-3.5" aria-hidden />
            Latest
          </button>
        )}
        <div className="mx-auto max-w-3xl">
          {openQuestions && !trayClosed && <QuestionTray set={openQuestions} onClose={() => setTrayClosed(true)} />}
          <Composer
            draftKey={id}
            running={running}
            onSend={send}
            onStop={() => client.stop(id)}
            placeholder={openQuestions && !trayClosed ? "Or reply in your own words…" : "Reply"}
            compact={!!openQuestions && !trayClosed}
          />
          <p className="mt-1.5 text-center text-[11px] text-muted">Gubern's agent can make mistakes. Changes outside the workspace always ask first.</p>
        </div>
      </div>
    </div>
  );
}
