# Gubern Web Dashboard

React management UI embedded into the Gubern binary and served at `/dashboard/`.

## Stack

- React 19 + TypeScript + Vite
- TanStack Query (server state) + Zustand ([`src/store.ts`](src/store.ts)) for UI state
- React Router 8, Tailwind CSS 3, Recharts, Lucide

## Develop

```bash
npm ci
npm run dev      # Vite dev server (proxy API to a running gubern on :9090 if configured)
npm run lint
npm run build    # writes dist/ — must be committed (Go embeds it)
```

CI rebuilds `dist/` and fails if the committed tree is stale. After UI changes:

```bash
npm run build
git add dist
```

## Live server and mock

By default the dashboard talks to the real server. It asks for an API key
(the `tui-key` file on the server machine, or one from `gubern identity
add-person`). The key is checked against `GET /api/me` and kept in
`sessionStorage`; with "Remember on this device" it goes in `localStorage`.
It is sent only to this origin, as a Bearer token. A 401 signs the person out.

- **The real client** is [`src/data/http.ts`](src/data/http.ts). Runs use the
  recorded event stream: `POST /api/agents/submit?events=1` with an
  `Idempotency-Key`. A dropped connection resumes at
  `/api/runs/{id}/events` with `Last-Event-ID`. Decisions go to
  `POST /api/approvals/{id}/decision` with the action hash the card shows.
- **Scope-aware UI:** `useCan(scope)` hides what the caller's scopes forbid.
  Without `approvals:decide` there are no Approve/Reject buttons; without
  `agents:run` there is no composer. The server enforces every scope
  regardless.
- **Mock:** `?mock=1` switches to the in-memory mock for UI work without a
  server. It sticks for the tab; `?mock=0` leaves it.
- **Not on the server yet:** questions, plans, rename, pin and hide. Rename,
  pin and hide are kept per browser.

## Layout

| Path | Role |
|------|------|
| `src/pages/` | Route screens |
| `src/components/` | Layout + shared UI |
| `src/lib/` | API client, hooks, Prometheus parse |
| `src/store.ts` | Client UI state (Zustand) |
| `dist/` | Production build (embedded) |

Do not invent a second state library — Query for remote data, Zustand for local UI.
