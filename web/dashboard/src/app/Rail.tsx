import { useMemo, useState } from "react";
import { NavLink, useMatch, useNavigate } from "react-router";
import {
  Activity,
  Blocks,
  BookOpen,
  Plug,
  ChevronDown,
  FolderKanban,
  HeartPulse,
  Inbox,
  Keyboard,
  LayoutDashboard,
  LogOut,
  MoreHorizontal,
  PanelLeftClose,
  PanelLeftOpen,
  Pin,
  PinOff,
  Plus,
  Route as RouteIcon,
  Search,
  Server,
  Settings as SettingsIcon,
  Sparkles,
  Timer,
  Trash2,
  Wallet,
  Hammer,
  Bot,
  Pencil,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { client, useAttention, useIdentity, useSessions } from "../data/client";
import type { SessionSummary } from "../data/model";
import { useUIStore } from "../store";
import { Kbd, MenuItem, Popover, StatusIcon } from "../ui/primitives";
import { cx } from "../ui/util";
import { GubernMark } from "./GubernMark";
import { clearApiKey } from "../data/auth";

interface NavItemProps {
  to: string;
  icon: LucideIcon;
  label: string;
  collapsed: boolean;
  badge?: number;
  end?: boolean;
}

function NavItem({ to, icon: Icon, label, collapsed, badge, end }: NavItemProps) {
  return (
    <NavLink
      to={to}
      end={end}
      title={collapsed ? label : undefined}
      className={({ isActive }) =>
        cx(
          "group flex h-9 items-center gap-2.5 rounded px-2.5 text-sm transition-colors duration-150",
          isActive ? "bg-selected text-foreground" : "text-muted hover:bg-accent hover:text-foreground",
          collapsed && "justify-center px-0",
        )
      }
    >
      <Icon className="h-[18px] w-[18px] shrink-0" aria-hidden />
      {!collapsed && <span className="flex-1 truncate">{label}</span>}
      {badge ? (
        <span
          className={cx("rounded-full bg-warning px-1.5 text-[11px] font-semibold leading-5 text-black", collapsed && "absolute ml-6 -mt-5")}
          aria-label={`${badge} waiting`}
        >
          {badge}
        </span>
      ) : null}
    </NavLink>
  );
}

function Group({ label, collapsed, children, defaultOpen = true }: { label: string; collapsed: boolean; children: React.ReactNode; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  if (collapsed) return <div className="mt-2 border-t border-border pt-2">{children}</div>;
  return (
    <div className="mt-3">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="flex w-full cursor-pointer items-center gap-1 px-2.5 py-1 text-[12px] font-medium text-muted hover:text-foreground"
      >
        {label}
        <ChevronDown className={cx("h-3.5 w-3.5 transition-transform duration-150", !open && "-rotate-90")} aria-hidden />
      </button>
      {open && <div className="mt-0.5 space-y-0.5">{children}</div>}
    </div>
  );
}

function dayBucket(at: number): string {
  const d = new Date(at);
  const today = new Date();
  const y = new Date();
  y.setDate(today.getDate() - 1);
  if (d.toDateString() === today.toDateString()) return "Today";
  if (d.toDateString() === y.toDateString()) return "Yesterday";
  return "Earlier";
}

function SessionRow({ s, active }: { s: SessionSummary; active: boolean }) {
  const navigate = useNavigate();
  const toast = useUIStore((st) => st.toast);
  const [renaming, setRenaming] = useState(false);
  const [title, setTitle] = useState(s.title);

  if (renaming) {
    return (
      <form
        onSubmit={(e) => {
          e.preventDefault();
          client.rename(s.id, title.trim() || s.title);
          setRenaming(false);
        }}
        className="px-1"
      >
        <input
          autoFocus
          aria-label="Session name"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          onBlur={() => setRenaming(false)}
          onKeyDown={(e) => e.key === "Escape" && setRenaming(false)}
          className="h-8 w-full rounded border border-primary bg-surface px-2 text-sm outline-none"
        />
      </form>
    );
  }

  return (
    <div className={cx("group relative flex items-center rounded", active ? "bg-selected" : "hover:bg-accent")}>
      <NavLink to={`/chat/${s.id}`} className="flex h-8 min-w-0 flex-1 items-center gap-2 px-2.5 text-sm text-foreground/90">
        <span className="truncate">{s.title}</span>
        {s.unread && <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-warning" aria-label="Unread" />}
      </NavLink>
      {s.status !== "done" && <StatusIcon status={s.status} className="mr-1 group-hover:hidden group-focus-within:hidden" />}
      <div className="hidden pr-1 group-hover:block group-focus-within:block">
        <Popover
          label={`Actions for ${s.title}`}
          align="end"
          width="w-52"
          trigger={({ toggle, ref, open }) => (
            <button
              ref={ref}
              type="button"
              aria-label={`Actions for ${s.title}`}
              aria-expanded={open}
              onClick={toggle}
              className="flex h-6 w-6 cursor-pointer items-center justify-center rounded text-muted hover:bg-surface-2 hover:text-foreground"
            >
              <MoreHorizontal className="h-4 w-4" aria-hidden />
            </button>
          )}
        >
          {(close) => (
            <>
              <MenuItem
                icon={s.pinned ? <PinOff className="h-4 w-4" /> : <Pin className="h-4 w-4" />}
                shortcut="P"
                onSelect={() => {
                  client.setPinned(s.id, !s.pinned);
                  close();
                }}
              >
                {s.pinned ? "Unpin" : "Pin"}
              </MenuItem>
              <MenuItem
                icon={<Pencil className="h-4 w-4" />}
                shortcut="R"
                onSelect={() => {
                  close();
                  setRenaming(true);
                }}
              >
                Rename
              </MenuItem>
              <MenuItem
                danger
                icon={<Trash2 className="h-4 w-4" />}
                shortcut="D"
                onSelect={() => {
                  close();
                  client.remove(s.id);
                  if (active) navigate("/");
                  toast({ text: `Deleted “${s.title}”`, tone: "info" });
                }}
              >
                Delete
              </MenuItem>
            </>
          )}
        </Popover>
      </div>
    </div>
  );
}

export function Rail() {
  const collapsed = useUIStore((s) => s.railCollapsed);
  const toggleRail = useUIStore((s) => s.toggleRail);
  const setPalette = useUIStore((s) => s.setPalette);
  const openSettings = useUIStore((s) => s.openSettings);
  const setShortcuts = useUIStore((s) => s.setShortcuts);
  const { data: attention } = useAttention();
  const { data: sessions } = useSessions();
  const { data: me } = useIdentity();
  const activeId = useMatch("/chat/:id")?.params.id;
  const navigate = useNavigate();

  // Same count as Home's "Needs you", so the two never disagree.
  const decisions = attention?.length ?? 0;
  const groups = useMemo(() => {
    const pinned = (sessions ?? []).filter((s) => s.pinned);
    const rest = (sessions ?? []).filter((s) => !s.pinned);
    const byDay: Record<string, SessionSummary[]> = {};
    for (const s of rest) (byDay[dayBucket(s.updatedAt)] ??= []).push(s);
    return { pinned, byDay };
  }, [sessions]);

  return (
    <nav
      aria-label="Main"
      className={cx(
        "z-rail flex h-full shrink-0 flex-col border-r border-border bg-rail transition-[width] duration-200",
        collapsed ? "w-[60px]" : "w-[272px]",
      )}
    >
      <div className={cx("flex h-14 items-center gap-2 px-3", collapsed && "justify-center px-0")}>
        {!collapsed && (
          <NavLink to="/" className="flex flex-1 items-center gap-2" aria-label="Gubern home">
            <GubernMark className="h-6 w-6" />
            <span className="text-[17px] font-semibold tracking-tight">Gubern</span>
          </NavLink>
        )}
        <button
          type="button"
          onClick={toggleRail}
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          title={`${collapsed ? "Expand" : "Collapse"} sidebar (Ctrl+B)`}
          className="flex h-8 w-8 cursor-pointer items-center justify-center rounded text-muted hover:bg-accent hover:text-foreground"
        >
          {collapsed ? <PanelLeftOpen className="h-[18px] w-[18px]" /> : <PanelLeftClose className="h-[18px] w-[18px]" />}
        </button>
      </div>

      <div className="flex-1 space-y-0.5 overflow-y-auto px-2 pb-3">
        <button
          type="button"
          onClick={() => navigate("/", { state: { focusComposer: true } })}
          title={collapsed ? "New chat (Ctrl+Shift+O)" : undefined}
          className={cx(
            "flex h-9 w-full cursor-pointer items-center gap-2.5 rounded bg-surface-2 px-2.5 text-sm font-medium text-foreground transition-colors duration-150 hover:bg-accent",
            collapsed && "justify-center px-0",
          )}
        >
          <Plus className="h-[18px] w-[18px]" aria-hidden />
          {!collapsed && <span className="flex-1 text-left">New chat</span>}
        </button>
        <button
          type="button"
          onClick={() => setPalette(true)}
          title={collapsed ? "Search (Ctrl+K)" : undefined}
          className={cx(
            "flex h-9 w-full cursor-pointer items-center gap-2.5 rounded px-2.5 text-sm text-muted transition-colors duration-150 hover:bg-accent hover:text-foreground",
            collapsed && "justify-center px-0",
          )}
        >
          <Search className="h-[18px] w-[18px]" aria-hidden />
          {!collapsed && (
            <>
              <span className="flex-1 text-left">Search</span>
              <Kbd>Ctrl K</Kbd>
            </>
          )}
        </button>
        <div className="relative">
          <NavItem to="/inbox" icon={Inbox} label="Inbox" collapsed={collapsed} badge={decisions} />
        </div>
        <NavItem to="/workspaces" icon={FolderKanban} label="Workspaces" collapsed={collapsed} />
        <NavItem to="/outputs" icon={Sparkles} label="Outputs" collapsed={collapsed} />
        <NavItem to="/automations" icon={Timer} label="Automations" collapsed={collapsed} />
        <NavItem to="/skills" icon={BookOpen} label="Skills" collapsed={collapsed} />
        <NavItem to="/connectors" icon={Plug} label="Connectors" collapsed={collapsed} />

        <Group label="Operate" collapsed={collapsed}>
          <NavItem to="/operate" end icon={LayoutDashboard} label="Overview" collapsed={collapsed} />
          <NavItem to="/operate/routes" icon={RouteIcon} label="Routes" collapsed={collapsed} />
          <NavItem to="/operate/traffic" icon={Activity} label="Traffic" collapsed={collapsed} />
          <NavItem to="/operate/nodes" icon={Server} label="Nodes" collapsed={collapsed} />
          <NavItem to="/operate/healing" icon={HeartPulse} label="Healing" collapsed={collapsed} />
          <NavItem to="/operate/agents" icon={Bot} label="Agents" collapsed={collapsed} />
        </Group>
        <Group label="Build" collapsed={collapsed} defaultOpen={false}>
          <NavItem to="/build/plugins" icon={Blocks} label="Plugins" collapsed={collapsed} />
          <NavItem to="/build/forge" icon={Hammer} label="Forge" collapsed={collapsed} />
        </Group>

        {!collapsed && groups.pinned.length > 0 && (
          <Group label="Pinned" collapsed={false}>
            {groups.pinned.map((s) => (
              <SessionRow key={s.id} s={s} active={s.id === activeId} />
            ))}
          </Group>
        )}
        {!collapsed &&
          ["Today", "Yesterday", "Earlier"].map((day) =>
            groups.byDay[day]?.length ? (
              <Group key={day} label={day} collapsed={false}>
                {groups.byDay[day].map((s) => (
                  <SessionRow key={s.id} s={s} active={s.id === activeId} />
                ))}
              </Group>
            ) : null,
          )}
      </div>

      <div className={cx("border-t border-border p-2", collapsed && "flex justify-center")}>
        <Popover
          label="Account"
          side="top"
          width="w-64"
          trigger={({ toggle, ref, open }) => (
            <button
              ref={ref}
              type="button"
              onClick={toggle}
              aria-expanded={open}
              aria-label="Account menu"
              className={cx("flex w-full cursor-pointer items-center gap-2.5 rounded px-2 py-1.5 text-left hover:bg-accent", collapsed && "w-auto")}
            >
              <span className="flex h-7 w-7 items-center justify-center rounded-full bg-selected text-[12px] font-semibold uppercase text-primary">
                {me?.name.slice(0, 1) ?? "·"}
              </span>
              {!collapsed && (
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm">{me?.name ?? "…"}</span>
                  <span className="block text-[12px] capitalize text-muted">{me ? `${me.role}${me.local ? " · local" : ""}` : ""}</span>
                </span>
              )}
            </button>
          )}
        >
          {(close) => (
            <>
              <MenuItem icon={<SettingsIcon className="h-4 w-4" />} shortcut="Ctrl ⇧ ," onSelect={() => (close(), openSettings("general"))}>
                Settings
              </MenuItem>
              <MenuItem icon={<Wallet className="h-4 w-4" />} onSelect={() => (close(), openSettings("budget"))}>
                Usage and cost
              </MenuItem>
              <MenuItem icon={<Keyboard className="h-4 w-4" />} shortcut="Ctrl /" onSelect={() => (close(), setShortcuts(true))}>
                Keyboard shortcuts
              </MenuItem>
              <MenuItem icon={<HeartPulse className="h-4 w-4" />} onSelect={() => (close(), navigate("/operate"))}>
                System status
              </MenuItem>
              <div className="my-1 border-t border-border" />
              <MenuItem icon={<LogOut className="h-4 w-4" />} onSelect={() => (close(), clearApiKey())}>
                Sign out
              </MenuItem>
            </>
          )}
        </Popover>
      </div>
    </nav>
  );
}
