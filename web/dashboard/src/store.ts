import { create } from "zustand";
import type { Mode, ViewMode } from "./data/model";

// Client UI state only. Remote data lives in TanStack Query / the data
// client (see src/data). Preferences persist in localStorage, and every
// access is guarded because private windows and blocked storage throw.

export type ThemePref = "system" | "dark" | "light";
export type PanelContent = { type: "run"; sessionId: string } | { type: "route"; name: string } | { type: "output"; id: string };

export interface Toast {
  id: number;
  text: string;
  tone: "info" | "success" | "error";
  action?: { label: string; run: () => void };
}

export type SettingsSection = "access" | "general" | "agent" | "models" | "channels" | "notifications" | "budget" | "security" | "advanced";

interface Prefs {
  theme: ThemePref;
  railCollapsed: boolean;
  viewMode: ViewMode;
  model: string;
  effort: "low" | "medium" | "high" | "max";
  mode: Mode;
  workspace: string;
}

const PREFS_KEY = "gubern.prefs.v1";
const DEFAULT_PREFS: Prefs = {
  theme: "system",
  railCollapsed: false,
  viewMode: "normal",
  model: "claude-sonnet-5-5",
  effort: "medium",
  mode: "agent",
  workspace: "~/gubern",
};

function loadPrefs(): Prefs {
  try {
    const raw = localStorage.getItem(PREFS_KEY);
    return raw ? { ...DEFAULT_PREFS, ...(JSON.parse(raw) as Partial<Prefs>) } : DEFAULT_PREFS;
  } catch {
    return DEFAULT_PREFS;
  }
}

function savePrefs(p: Prefs) {
  try {
    localStorage.setItem(PREFS_KEY, JSON.stringify(p));
  } catch {
    /* storage unavailable: preferences last for this tab only */
  }
}

export function applyTheme(theme: ThemePref) {
  const dark = theme === "dark" || (theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  const root = document.documentElement;
  root.classList.toggle("dark", dark);
  root.classList.toggle("light", !dark);
}

interface UIState extends Prefs {
  setPref: <K extends keyof Prefs>(key: K, value: Prefs[K]) => void;
  toggleRail: () => void;

  panel: PanelContent | null;
  openPanel: (p: PanelContent) => void;
  closePanel: () => void;
  togglePanel: (p: PanelContent) => void;

  paletteOpen: boolean;
  setPalette: (open: boolean) => void;
  shortcutsOpen: boolean;
  setShortcuts: (open: boolean) => void;
  settings: SettingsSection | null;
  openSettings: (s?: SettingsSection) => void;
  closeSettings: () => void;

  toasts: Toast[];
  toast: (t: Omit<Toast, "id">) => void;
  dismissToast: (id: number) => void;
}

let toastSeq = 0;

export const useUIStore = create<UIState>((set, get) => ({
  ...loadPrefs(),
  setPref: (key, value) => {
    set({ [key]: value } as Pick<Prefs, typeof key>);
    const s = get();
    savePrefs({
      theme: s.theme,
      railCollapsed: s.railCollapsed,
      viewMode: s.viewMode,
      model: s.model,
      effort: s.effort,
      mode: s.mode,
      workspace: s.workspace,
    });
    if (key === "theme") applyTheme(value as ThemePref);
  },
  toggleRail: () => get().setPref("railCollapsed", !get().railCollapsed),

  panel: null,
  openPanel: (p) => set({ panel: p }),
  closePanel: () => set({ panel: null }),
  togglePanel: (p) => set((s) => ({ panel: s.panel && JSON.stringify(s.panel) === JSON.stringify(p) ? null : p })),

  paletteOpen: false,
  setPalette: (open) => set({ paletteOpen: open }),
  shortcutsOpen: false,
  setShortcuts: (open) => set({ shortcutsOpen: open }),
  settings: null,
  openSettings: (s = "general") => set({ settings: s }),
  closeSettings: () => set({ settings: null }),

  toasts: [],
  toast: (t) => {
    const id = ++toastSeq;
    set((s) => ({ toasts: [...s.toasts, { ...t, id }] }));
    setTimeout(() => get().dismissToast(id), t.action ? 8000 : 4000);
  },
  dismissToast: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
}));
