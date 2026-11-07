# Gubern dashboard: UX reference (Phase 0)

The source is 29 screenshots of Claude's web app (Chat and Code modes), taken
2026-11-07. They stay on the maintainer's machine and are not committed,
because they show personal account data (email, organisation ID, login
locations). This document records the **interaction patterns** to reuse. The
new dashboard keeps **Gubern's own identity** (name, logo, colours from
`internal/tui/brand`). It must not copy Claude's logo, wordmark, fonts,
mascot or colours.

## 1. Patterns observed

### 1.1 Shell

- **Left rail (~320 px, collapsible).** Top row: collapse toggle, product
  name, and a two-icon *mode switcher* (Chat / Code). Below that: **New**
  (primary row), **Search**, then the main destinations (Projects, Artifacts,
  Scheduled, Customize, More), then a collapsible **Recents** list.
- **Recents rows** carry status icons on the right, used in Code mode: needs
  attention ⚠, cloud, offline, PR. A context menu offers single-key shortcuts:
  Mark unread `U`, Pin `P`, Rename `R`, Add to project ›, Move to group ›,
  Archive `A`, and Delete `D` in red.
- **The account row** sits at the bottom of the rail: avatar, name · plan, and
  a menu with email, Settings (`Ctrl+Shift+,`), Usage, Language, Get help,
  plans, apps, a Learn more › submenu (legal links and *Keyboard shortcuts*
  `Ctrl+/`), API keys ↗, and Log out.
- **Main column.** Content is centred, and the transcript is narrow and
  readable (width is a setting).
- **Conversation top bar:** a title with a ▾ menu, a notification bell with an
  unread dot, and on the right a side-panel toggle and **Share**.
- **Code mode right context panel:** *Environment* (where the session runs,
  connection status, repo, branch, add), *Context* ›, *Connectors* ›
  ("8 connected"), and *Changes* (open PRs with a ⋯ menu). The top bar shows
  pane toggles for the panel, terminal, files and more.

### 1.2 Home / empty state

- A serif greeting with a brand mark, and the composer in the middle of the
  page.
- **The composer:**
  - The placeholder teaches syntax: "Type / for skills" or "Type / for
    commands".
  - **+** opens the attach menu.
  - On the right: a mic with a ▾, and a voice-mode button.
- **Under the composer:** the Project picker on the left; on the right, the
  model with its effort level, then the permission mode.
- **Recent artifacts:** thumbnail cards with title and date, plus "View all".

### 1.3 Composer menus

- **Model menu.** Each model has a one-line purpose. Gated models show a badge
  and a call to action. The menu has ✓ for the current choice, an **Effort ›**
  submenu, **More models ›**, and a footnote.
- **Permission mode.** Two choices, each with a one-line explanation:
  - *Manual*: asks before new tools or sites.
  - *Auto*: runs on its own and pauses if something looks unsafe.

  The choice is echoed as a dismissable banner above the composer in the
  conversation.
- **Project picker:** search, a list, then "+ New project".
- **Code composer extras:** an environment selector, a repo selector, and a
  small context-usage ring.

### 1.4 Conversation

- Responses use a reading font. Bold lead-ins, lists and numbered questions
  are common.
- **Action row under each response:** copy, read aloud, 👍 👎, retry. A
  suggested next-step chip ("Create a spreadsheet") sits above the row.
- **Agent activity (Code)** shows as quiet, collapsed one-line steps that
  expand ("Read notifications ›", "Deleted scheduled task ›"). Inline links
  carry type icons (a PR icon before `#71`). Code spans are used for
  identifiers.
- A small disclaimer line sits under the composer.

### 1.4b Clarifying questions and focus mode

- **Question tray.** When the assistant needs input, an amber pill ("Asking a
  question") appears in the transcript. A card docks above the composer with:
  - the question as its title, a **‹ 1 of 3 ›** pager and a close button;
  - numbered options, each with a hint line;
  - a "Something else" free-text row and a **Skip** button.

  The composer below changes to "Or reply directly…". The bottom line shows
  the permission mode (*Manual* here).
- **Long-task progress panel.** A pill in the top bar, **"Step 4 of 13"**,
  toggles a right panel with:
  - **Progress:** a vertical step timeline. "1 earlier step" is collapsed,
    completed steps are dim, the current step has a dot, upcoming steps are
    listed.
  - **Outputs:** thumbnails with title and type (Doc, Artifact).
  - The active tool integration, with a ›.
  - **Used in this session:** Uploads (file names, +2), Connectors (icons),
    Web search, Memory ("Read · project name"), Skills.

  In the transcript, tool use is **grouped**: "Used Claude in Chrome
  (4 actions), **1 failed** ›", with screenshot thumbnails at the right and
  the failure count in red. A floating **↓** button jumps to the latest
  message.
- **Rail extras.** A **Pinned** section above Recents (a pinned project);
  Recents rows show a type icon (doc) and an unread dot (amber).
- **Focus mode.** The rail can be fully hidden in full screen, leaving only
  the greeting, the composer and recent artifacts, with a rail toggle at the
  top left.

### 1.5 Search / command palette (`Ctrl+K`)

- A modal with a search field and type tabs: All, Artifacts, Sessions, Code,
  Projects, Scheduled.
- Results are grouped as **Needs attention**, **Recents**, then **Actions**
  (New session ⏎).
- The footer lists the keyboard hints: Close `Esc`, Change type `← →`,
  Filters `/`, Compose `Tab`.

### 1.6 Library pages (Artifacts, Scheduled, Customize)

- A serif page title, segmented tabs (All / Yours / Shared …), and a right
  toolbar: search, filter, sort or view, and **+ New** (sometimes with ▾).
- A dismissable announcement card with one call to action.
- "Make something new": large coloured tiles, one per type.
- A card grid with a thumbnail, title and "Opened Oct 29".
- **Empty state:** an illustration, one line of text and a primary button,
  then a **templates** list (icon, name, one-line purpose, schedule). This
  makes an empty page useful.
- **Customize** has three tabs (Skills / Connectors / Plugins), each split
  into Yours and Discover:
  - **Discover:** a featured hero card, then "New" and "Most installed"
    sections with "Show all →". Each card has an icon, name, description,
    source and install count, plus **Try** and **+** buttons.
  - **Yours:** rows with a date and a ⋯ menu.
  - **Connectors:** a table (Connector, Type, Status). Status is ✓ or a
    **Connect** button.
  - A builder call-to-action card sits at the bottom.

### 1.7 Settings (modal)

- A large modal with a searchable left nav in three groups:
  - Account / Billing / Usage
  - Appearance / Your Claude / Capabilities / Notifications / Privacy /
    Wellbeing
  - Customize / Code

  An API keys ↗ link sits in the nav footer.
- **Row pattern everywhere:** title and description on the left, one control
  on the right (toggle, select, text input, segmented control, or a
  **Manage** / **Export** button). Child settings are indented.
- **Usage** opens with a headline warning ("at this pace you'll run out this
  evening"). Below it are progress bars with reset times (blue normally,
  amber near the limit), a credits balance with a toggle, and a breakdown by
  product.
- **Account:** editable profile fields, a copyable ID, and a **sessions
  table** (device, location, signed in, last active, a *Current* badge) with
  **Log out of all devices**.
- **Appearance:** theme as three icon buttons (system / light / dark),
  interface and response fonts, text size, transcript width, motion, and code
  themes previewed as live diff snippets.
- **Default transcript view:** a segmented control, Normal / Thinking /
  Verbose. It sets how much agent activity new sessions show.
- **Notifications:** per-event toggles, split into push and email.
- **Privacy:** explanation links, preference toggles, and *Your data* rows,
  each with Export or Manage.

### 1.8 Other surfaces

- **Help widget:** a floating panel with live system status, FAQ, send a
  message, and search help, with Home / Messages / Help tabs.
- **Language picker:** a modal with search and a three-column grid showing
  each name in its own script and in English.
- **Apps page:** large marketing cards with an install button each.

### 1.8b Second batch of screenshots (runs, context, model menu, projects, light, share)

- **A live step during a run:**
  - The header reads "••• **Creating** 31s ▾", with animated dots and an
    elapsed timer.
  - Inside it, a titled sub-step ("Got real Hero.tsx diff from portfolio
    commit ▾") holds the **exact command** in a mono block with a copy
    button, then the **output** in a scrollable box. Diff lines are tinted
    red and green.
  - The footer repeats the current verb.
- **The composer during a run.** The placeholder reads "Enter to send,
  **Ctrl+Enter to queue for later**", and a square **Stop** button sits
  inside the field.
- **Context panel (Code):**
  - Environment, repos (+ add) and branch;
  - **Context: "22% used · 217.1k / 1M"** as a segmented bar, with one row
    per source (coloured dot, tokens, %): messages, system tools, system
    prompt, skills, MCP, since last update, autocompact buffer, free space;
    deferred tools are listed separately;
  - Connectors (icon list with *Show more*).
- **Model menu:**
  - number shortcuts **1–4** next to each model;
  - **Effort ›** with a one-line explanation: Low, Medium (*Recommended*),
    High ✓, Extra, Max (⚠ "3.5× or more usage");
  - toggles for Ultracode and **Fast mode**.
- **Projects list:** a two-column grid of rows (icon, name, last date, pin
  mark), plus search, sort and **New project**.
- **Project page:**
  - a breadcrumb, "Projects / name ▾", with search and pin;
  - a serif title, and a composer scoped to the project ("New session in
    D:\Vayu");
  - under the composer, a chip for where it runs ("Vayu on Claude Desktop
    (Windows)"), the model and the permission mode;
  - an empty state ("references the same knowledge every time"), or the
    project's Recents with dates;
  - a right column of rows, each with one action: **Instructions** Add,
    **Context** "1 linked folder" Add, **Folder** Remove (with a note),
    **Memory** View, **Scheduled** Add, and a "Private" label.
- **Light theme.** A white canvas, a light grey rail, cards with hairline
  borders, near-black text. The same layout, with no other changes.
- **Outputs toggle.** A "▤ 2" counter in the top bar opens the Outputs panel.
  "Used in this session" rows show counts ("Web search · 29 searches"), and
  hovering *Skills* lists them in a popover.
- **Share popover:**
  - "Only messages up until now will be shared";
  - add people by email, then **Invite**;
  - *Who has access* ("Only people invited ›"), the owner row, and a privacy
    warning;
  - a gear for settings and a primary **Copy link**.

  Settings open as a sub-view with Back / Save.
- **Content rendering.** Tables and section headings in the reading font,
  code blocks with a copy button, and a floating ↓ button to jump to the
  latest.

### 1.9 Visual language

- Dark-first. The rail is slightly darker than the main canvas, and raised
  surfaces are one step lighter.
- 1 px low-contrast borders, 8–12 px radii, generous spacing.
- A single accent colour, used for toggles, checks and links. Other colour
  appears only for status (amber warning, red delete) and type tiles.
- Thin line icons. A display serif for page titles and the greeting; sans for
  the interface.

## 2. Mapping to Gubern

| Pattern | Gubern equivalent | Backend today |
|---|---|---|
| Mode switcher (Chat / Code) | **Chat** · **Agent** (local-tool coding agent, team runs) · **Operate** (edge, fleet) | — |
| New + composer | Prompt the coordinator; mode chip *chat / agent / team / research* | `/api/agents/submit` (SSE), `/v1/chat/completions` |
| Model + effort menu | AI providers and models with one-line purpose; cost badge | `/v1/models`, `/api/ai/cost` |
| Permission mode Manual / Auto | Approval policy: approval required vs Auto Mode inside the trusted root | config `trusted_root` / `auto_mode` (**no write API**) |
| Permission banner | Banner showing the active policy and workspace root | — |
| Collapsed activity steps + view modes | Tool cards (read_file, run_terminal, git…) in Normal / Thinking / Verbose | agent SSE stream |
| (not in screenshots) | **Inline approval card**: tool, risk, command or diff preview, Approve / Reject | `/api/agents/approve` |
| Code right panel | **Run panel**: workspace root and branch, pending approvals, checkpoints, changed files (diffs), team agents | `/api/team/*`, `/api/checkpoints/*` |
| Projects | **Namespaces / workspaces** | `/api/namespaces*` |
| Artifacts library | **Outputs**: research reports, Forge builds, generated route configs | `/api/research/reports*`, `/api/forge/*` |
| Scheduled + templates | **Automations**: scheduled agent tasks and orchestration runs (templates: nightly health report, cert expiry check, dependency audit) | `/api/orchestration/*` (**no schedule API**) |
| Customize: Skills / Connectors / Plugins | **Plugins** (WASM), **Channels** (Telegram / Slack / WhatsApp, with registered users), **Providers** | `/api/plugins` (**read-only**) |
| Connectors table (✓ / Connect) | Channel and provider status table | `/api/status`, `/api/keys/status` |
| Search palette, "Needs attention" | `Ctrl+K`: pending approvals and checkpoints, failing routes, unhealthy nodes first | aggregate of existing endpoints |
| Recents status icons | Run status: running · needs approval · failed · done | agent and team status |
| Settings → Usage | **AI cost and budget**: today, this month, by provider | `/api/ai/cost` |
| Settings → Account sessions table | **API keys**: key, role, created, last used, *Current*, Revoke; "Revoke all others" | `/api/keys*` |
| Settings → Notifications | Notification routing per event (approval needed, route down, cert expiring) to Telegram / Slack / WhatsApp | `/api/notify` (**no settings API**) |
| Settings → Privacy → Export | **Audit log**: export, and *Verify chain* | `/api/audit`, `/api/audit/verify` |
| Help widget status | Health and readiness of every subsystem | `/health`, `/ready` |
| — (Gubern only) | **Operate** area: Routes, Route builder, Traffic, Metrics, Nodes, Healing, DevOps servers. Uses the library and table patterns, with details in the side panel | existing ops endpoints |

Items marked **no write API** or **read-only** need new admin endpoints
(Phase 5). Each must be audit-logged and admin-only.

## 3. Gubern visual direction (proposal for the Phase 1 prototype)

- **Colours** from `internal/tui/brand`: background `#0D0D0D`, surface
  `#141414`, border `#2A2A2A`, text `#E8E8E8`, dim text `#666666`, selected
  `#1A2A3A`. The accent is primary blue `#378ADD`; status colours are success
  `#1D9E75`, warning `#EF9F27`, danger `#D85A30`, and purple `#534AB7` for AI
  and agent activity. Phase 1 adds a light theme with the same roles.
- **Type:** an open-source sans for the interface (Inter) and mono for code
  and logs (JetBrains Mono). Display headings may use an open-source serif;
  this is decided in Phase 1.
- **Shape:** 1 px borders, 10 px radius, 4 px spacing grid, thin line icons
  (lucide, already a dependency).
- **The Gubern mark** replaces the brand glyph in the greeting and rail.

## 4. Screens not covered by the screenshots

Received: focus mode, the question tray, the progress panel, a mid-run Code
session, the context panel, the model and effort menus, projects, light
theme, outputs, and share.

Still not captured, so these are designed from first principles in the UX
spec:

1. A **permission prompt** for a tool or command (spec F4).
2. An **artifact open beside the chat** (the spec's side panel).
3. The composer **+ menu**, Customize → Plugins, the Scheduled → New task
   form, and a narrow or mobile width.
