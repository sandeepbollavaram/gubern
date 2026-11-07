import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router";
import {
  Activity,
  AlertTriangle,
  Blocks,
  BookOpen,
  FileText,
  FolderKanban,
  HeartPulse,
  Inbox,
  Keyboard,
  LayoutDashboard,
  MessageSquare,
  Moon,
  Plus,
  Route as RouteIcon,
  Search,
  Server,
  Settings,
  ShieldAlert,
  Sparkles,
  Timer,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useAttention, useSessions } from "../data/client";
import { useUIStore } from "../store";
import { Dialog, Kbd, StatusIcon } from "../ui/primitives";
import { cx } from "../ui/util";

type Tab = "all" | "attention" | "sessions" | "pages" | "actions";
interface Entry {
  id: string;
  group: "Needs attention" | "Sessions" | "Pages" | "Actions";
  label: string;
  hint?: string;
  icon: React.ReactNode;
  run: () => void;
}

const TABS: { id: Tab; label: string }[] = [
  { id: "all", label: "All" },
  { id: "attention", label: "Needs attention" },
  { id: "sessions", label: "Sessions" },
  { id: "pages", label: "Pages" },
  { id: "actions", label: "Actions" },
];

const PAGES: { label: string; to: string; icon: LucideIcon }[] = [
  { label: "Inbox", to: "/inbox", icon: Inbox },
  { label: "Workspaces", to: "/workspaces", icon: FolderKanban },
  { label: "Outputs", to: "/outputs", icon: Sparkles },
  { label: "Automations", to: "/automations", icon: Timer },
  { label: "Skills", to: "/skills", icon: BookOpen },
  { label: "Operate · Overview", to: "/operate", icon: LayoutDashboard },
  { label: "Operate · Routes", to: "/operate/routes", icon: RouteIcon },
  { label: "Operate · Traffic", to: "/operate/traffic", icon: Activity },
  { label: "Operate · Nodes", to: "/operate/nodes", icon: Server },
  { label: "Operate · Healing", to: "/operate/healing", icon: HeartPulse },
  { label: "Build · Plugins", to: "/build/plugins", icon: Blocks },
];

export function CommandPalette() {
  const open = useUIStore((s) => s.paletteOpen);
  const setOpen = useUIStore((s) => s.setPalette);
  return (
    <Dialog open={open} onClose={() => setOpen(false)} title="Search" hideTitle className="max-w-2xl rounded-2xl">
      {/* Mounted only while open, so every opening starts from a clean query. */}
      {open && <PaletteBody />}
    </Dialog>
  );
}

function PaletteBody() {
  const setOpen = useUIStore((s) => s.setPalette);
  const openSettings = useUIStore((s) => s.openSettings);
  const setShortcuts = useUIStore((s) => s.setShortcuts);
  const theme = useUIStore((s) => s.theme);
  const setPref = useUIStore((s) => s.setPref);
  const navigate = useNavigate();
  const { data: attention } = useAttention();
  const { data: sessions } = useSessions();
  const [query, setQuery] = useState("");
  const [tab, setTab] = useState<Tab>("all");
  const [cursor, setCursor] = useState(0);
  const list = useRef<HTMLDivElement | null>(null);

  const go = (fn: () => void) => () => {
    setOpen(false);
    fn();
  };

  const entries = useMemo<Entry[]>(() => {
    const q = query.trim().toLowerCase();
    const match = (s: string) => !q || s.toLowerCase().includes(q);
    const out: Entry[] = [];
    for (const a of attention ?? []) {
      if (!match(`${a.title} ${a.detail}`)) continue;
      out.push({
        id: a.id,
        group: "Needs attention",
        label: a.title,
        hint: a.detail,
        icon: a.kind === "approval" ? <ShieldAlert className="h-4 w-4 text-warning" /> : <AlertTriangle className="h-4 w-4 text-warning" />,
        run: go(() => navigate(a.href)),
      });
    }
    for (const s of sessions ?? []) {
      if (!match(s.title)) continue;
      out.push({
        id: s.id,
        group: "Sessions",
        label: s.title,
        icon: s.status === "done" ? <MessageSquare className="h-4 w-4 text-muted" /> : <StatusIcon status={s.status} />,
        run: go(() => navigate(`/chat/${s.id}`)),
      });
    }
    for (const p of PAGES) {
      if (!match(p.label)) continue;
      out.push({ id: p.to, group: "Pages", label: p.label, icon: <p.icon className="h-4 w-4 text-muted" />, run: go(() => navigate(p.to)) });
    }
    const actions: Omit<Entry, "group">[] = [
      {
        id: "new",
        label: "New chat",
        hint: "Ctrl Shift O",
        icon: <Plus className="h-4 w-4 text-muted" />,
        run: go(() => navigate("/", { state: { focusComposer: true } })),
      },
      {
        id: "settings",
        label: "Open settings",
        hint: "Ctrl Shift ,",
        icon: <Settings className="h-4 w-4 text-muted" />,
        run: go(() => openSettings("general")),
      },
      { id: "audit", label: "Audit log: verify chain", icon: <FileText className="h-4 w-4 text-muted" />, run: go(() => openSettings("security")) },
      {
        id: "theme",
        label: theme === "light" ? "Switch to dark theme" : "Switch to light theme",
        icon: <Moon className="h-4 w-4 text-muted" />,
        run: go(() => setPref("theme", theme === "light" ? "dark" : "light")),
      },
      { id: "keys", label: "Keyboard shortcuts", hint: "Ctrl /", icon: <Keyboard className="h-4 w-4 text-muted" />, run: go(() => setShortcuts(true)) },
    ];
    for (const a of actions) if (match(a.label)) out.push({ ...a, group: "Actions" });
    const allowed: Record<Tab, Entry["group"][]> = {
      all: ["Needs attention", "Sessions", "Pages", "Actions"],
      attention: ["Needs attention"],
      sessions: ["Sessions"],
      pages: ["Pages"],
      actions: ["Actions"],
    };
    const filtered = out.filter((e) => allowed[tab].includes(e.group));
    // On "All" without a query keep it scannable: a few of each group.
    if (tab === "all" && !q) {
      const caps: Record<Entry["group"], number> = { "Needs attention": 5, Sessions: 5, Pages: 0, Actions: 3 };
      const seen: Record<string, number> = {};
      return filtered.filter((e) => (seen[e.group] = (seen[e.group] ?? 0) + 1) <= caps[e.group]);
    }
    return filtered;
    // go/navigate are stable for the palette's lifetime.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, tab, attention, sessions, theme]);

  useEffect(() => {
    list.current?.querySelector<HTMLElement>(`[data-index="${cursor}"]`)?.scrollIntoView({ block: "nearest" });
  }, [cursor]);

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      setCursor((c) => Math.min(c + 1, entries.length - 1));
      e.preventDefault();
    } else if (e.key === "ArrowUp") {
      setCursor((c) => Math.max(c - 1, 0));
      e.preventDefault();
    } else if (e.key === "Enter") {
      entries[cursor]?.run();
      e.preventDefault();
    } else if ((e.key === "ArrowRight" || e.key === "ArrowLeft") && !query) {
      const i = TABS.findIndex((t) => t.id === tab);
      setTab(TABS[(i + (e.key === "ArrowRight" ? 1 : TABS.length - 1)) % TABS.length].id);
      setCursor(0);
      e.preventDefault();
    }
  };

  let lastGroup = "";
  return (
    <div onKeyDown={onKey}>
      <div className="border-b border-border">
        <label className="flex items-center gap-3 px-5 pb-3 pt-4">
          <Search className="h-5 w-5 shrink-0 text-muted" aria-hidden />
          <input
            data-autofocus
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setCursor(0);
            }}
            placeholder="Search sessions, pages and actions"
            aria-label="Search"
            aria-controls="palette-results"
            aria-activedescendant={entries[cursor] ? `pal-${cursor}` : undefined}
            className="h-8 w-full bg-transparent text-base outline-none placeholder:text-muted focus-visible:outline-none"
          />
        </label>
        <div role="tablist" aria-label="Result type" className="flex flex-wrap gap-1.5 px-4 pb-3">
          {TABS.map((t) => (
            <button
              key={t.id}
              role="tab"
              type="button"
              aria-selected={tab === t.id}
              onClick={() => {
                setTab(t.id);
                setCursor(0);
              }}
              className={cx(
                "h-8 cursor-pointer rounded-full border px-3 text-[13px] transition-colors duration-150",
                tab === t.id ? "border-transparent bg-accent font-medium text-foreground" : "border-border text-muted hover:text-foreground",
              )}
            >
              {t.label}
            </button>
          ))}
        </div>
      </div>
      <div ref={list} id="palette-results" role="listbox" aria-label="Results" className="max-h-[min(440px,56vh)] scroll-py-2 overflow-y-auto px-2 pb-2">
        {entries.length === 0 && <p className="px-3 py-10 text-center text-sm text-muted">No matches for “{query}”.</p>}
        {entries.map((e, i) => {
          const header = e.group !== lastGroup ? e.group : null;
          lastGroup = e.group;
          return (
            <div key={`${e.group}-${e.id}`}>
              {header && <p className="px-3 pb-1.5 pt-4 text-[12px] font-medium uppercase tracking-wide text-muted">{header}</p>}
              <div
                id={`pal-${i}`}
                data-index={i}
                role="option"
                aria-selected={i === cursor}
                onMouseMove={() => setCursor(i)}
                onClick={e.run}
                className={cx(
                  "flex min-h-11 cursor-pointer items-center gap-3 rounded-lg px-3 py-2 text-sm transition-colors duration-100",
                  i === cursor && "bg-accent",
                )}
              >
                <span className="flex w-5 shrink-0 justify-center">{e.icon}</span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate">{e.label}</span>
                  {e.hint && e.group === "Needs attention" && <span className="mt-0.5 block truncate text-[12px] text-muted">{e.hint}</span>}
                </span>
                {e.hint && e.group === "Actions" && <span className="font-mono text-[11px] text-muted">{e.hint}</span>}
                {i === cursor && <span className="font-mono text-[11px] text-muted">↵</span>}
              </div>
            </div>
          );
        })}
      </div>
      <div className="flex items-center gap-5 border-t border-border bg-surface-2/60 px-5 py-2.5 text-[12px] text-muted">
        <span className="flex items-center gap-1">
          Open <Kbd>Enter</Kbd>
        </span>
        <span className="flex items-center gap-1">
          Move <Kbd>↑</Kbd>
          <Kbd>↓</Kbd>
        </span>
        <span className="flex items-center gap-1">
          Change type <Kbd>←</Kbd>
          <Kbd>→</Kbd>
        </span>
        <span className="ml-auto flex items-center gap-1">
          Close <Kbd>Esc</Kbd>
        </span>
      </div>
    </div>
  );
}
