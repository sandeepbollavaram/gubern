import { useUIStore } from "../store";
import { Dialog, Kbd } from "../ui/primitives";

const GROUPS: { title: string; rows: [string, string[]][] }[] = [
  {
    title: "Anywhere",
    rows: [
      ["Search and commands", ["Ctrl", "K"]],
      ["New chat", ["Ctrl", "Shift", "O"]],
      ["Toggle sidebar", ["Ctrl", "B"]],
      ["Toggle side panel", ["Ctrl", "."]],
      ["Settings", ["Ctrl", "Shift", ","]],
      ["Keyboard shortcuts", ["Ctrl", "/"]],
      ["Close the top layer", ["Esc"]],
    ],
  },
  {
    title: "Go to",
    rows: [
      ["Inbox", ["G", "I"]],
      ["Operate", ["G", "O"]],
      ["Workspaces", ["G", "W"]],
    ],
  },
  {
    title: "Conversation",
    rows: [
      ["Send", ["Enter"]],
      ["New line", ["Shift", "Enter"]],
      ["Queue while the agent works", ["Ctrl", "Enter"]],
      ["Stop the agent", ["Esc"]],
      ["Pick a question option", ["1", "–", "9"]],
    ],
  },
  {
    title: "Approvals (card focused)",
    rows: [
      ["Approve", ["A"]],
      ["Reject", ["R"]],
      ["Next / previous in Inbox", ["J", "K"]],
    ],
  },
];

export function ShortcutsSheet() {
  const open = useUIStore((s) => s.shortcutsOpen);
  const setOpen = useUIStore((s) => s.setShortcuts);
  return (
    <Dialog open={open} onClose={() => setOpen(false)} title="Keyboard shortcuts" className="max-w-2xl">
      <div className="grid gap-x-8 gap-y-5 p-5 sm:grid-cols-2">
        {GROUPS.map((g) => (
          <section key={g.title}>
            <h3 className="mb-2 text-[12px] font-medium uppercase tracking-wide text-muted">{g.title}</h3>
            <dl className="space-y-1.5">
              {g.rows.map(([label, keys]) => (
                <div key={label} className="flex items-center justify-between gap-3 text-sm">
                  <dt>{label}</dt>
                  <dd className="flex gap-1">
                    {keys.map((k, i) => (
                      <Kbd key={i}>{k}</Kbd>
                    ))}
                  </dd>
                </div>
              ))}
            </dl>
          </section>
        ))}
      </div>
    </Dialog>
  );
}
