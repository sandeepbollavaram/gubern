import { Suspense, useEffect, useRef } from "react";
import { Outlet, useNavigate } from "react-router";
import { X } from "lucide-react";
import { useLiveInvalidation } from "../data/client";
import { RunPanel } from "../features/chat/RunPanel";
import { applyTheme, useUIStore } from "../store";
import { Toaster } from "../ui/Toaster";
import { Skeleton } from "../ui/primitives";
import { CommandPalette } from "./CommandPalette";
import { Rail } from "./Rail";
import { SettingsModal } from "./SettingsModal";
import { ShortcutsSheet } from "./ShortcutsSheet";

function SidePanel() {
  const panel = useUIStore((s) => s.panel);
  const close = useUIStore((s) => s.closePanel);
  if (!panel) return null;
  const title = panel.type === "run" ? "Run" : panel.type === "route" ? `Route ${panel.name}` : "Output";
  return (
    <aside
      aria-label={title}
      className="z-panel flex w-[340px] shrink-0 flex-col border-l border-border bg-rail max-[1279px]:fixed max-[1279px]:inset-y-0 max-[1279px]:right-0 max-[1279px]:shadow-2xl"
    >
      <div className="flex h-14 shrink-0 items-center justify-between border-b border-border px-4">
        <h2 className="text-sm font-semibold">{title}</h2>
        <button
          type="button"
          onClick={close}
          aria-label="Close panel (Ctrl+.)"
          className="flex h-8 w-8 cursor-pointer items-center justify-center rounded text-muted hover:bg-accent hover:text-foreground"
        >
          <X className="h-4 w-4" />
        </button>
      </div>
      <div className="flex-1 overflow-y-auto">
        {panel.type === "run" ? <RunPanel sessionId={panel.sessionId} /> : <p className="p-4 text-sm text-muted">Coming in Phase 3.</p>}
      </div>
    </aside>
  );
}

// Global shortcuts (UX spec §6). Single-letter "G then X" sequences are
// ignored while typing in a field.
function useShortcuts() {
  const navigate = useNavigate();
  const pendingG = useRef(0);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const s = useUIStore.getState();
      const typing = ["INPUT", "TEXTAREA", "SELECT"].includes((e.target as HTMLElement).tagName) || (e.target as HTMLElement).isContentEditable;
      const mod = e.ctrlKey || e.metaKey;
      if (mod && e.key.toLowerCase() === "k") {
        e.preventDefault();
        s.setPalette(!s.paletteOpen);
      } else if (mod && e.key === "/") {
        e.preventDefault();
        s.setShortcuts(!s.shortcutsOpen);
      } else if (mod && e.key.toLowerCase() === "b") {
        e.preventDefault();
        s.toggleRail();
      } else if (mod && e.key === ".") {
        e.preventDefault();
        if (s.panel) s.closePanel();
      } else if (mod && e.shiftKey && e.key.toLowerCase() === "o") {
        e.preventDefault();
        navigate("/", { state: { focusComposer: true } });
      } else if (mod && e.shiftKey && (e.key === "," || e.key === "<")) {
        e.preventDefault();
        s.openSettings("general");
      } else if (!typing && !mod && e.key === "g") {
        pendingG.current = Date.now();
      } else if (!typing && !mod && Date.now() - pendingG.current < 1200) {
        const to = { i: "/inbox", o: "/operate", w: "/workspaces" }[e.key];
        pendingG.current = 0;
        if (to) navigate(to);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [navigate]);
}

export function Shell() {
  useLiveInvalidation();
  useShortcuts();
  const theme = useUIStore((s) => s.theme);
  useEffect(() => {
    applyTheme(theme);
    if (theme !== "system") return;
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const on = () => applyTheme("system");
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, [theme]);

  return (
    <div className="flex h-screen overflow-hidden bg-background text-foreground">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:fixed focus:left-2 focus:top-2 focus:z-toast focus:rounded focus:bg-primary focus:px-3 focus:py-2 focus:text-primary-foreground"
      >
        Skip to content
      </a>
      <Rail />
      <main id="main" className="min-w-0 flex-1">
        <Suspense
          fallback={
            <div className="space-y-3 p-8" role="status" aria-live="polite">
              <span className="sr-only">Loading…</span>
              <Skeleton className="h-8 w-64" />
              <Skeleton className="h-4 w-96" />
              <Skeleton className="h-40 w-full" />
            </div>
          }
        >
          <Outlet />
        </Suspense>
      </main>
      <SidePanel />
      <CommandPalette />
      <SettingsModal />
      <ShortcutsSheet />
      <Toaster />
    </div>
  );
}
