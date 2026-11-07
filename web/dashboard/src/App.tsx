import { lazy, useEffect, useSyncExternalStore } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Navigate, Route, Routes, useLocation } from "react-router";
import { Shell } from "./app/Shell";
import { LegacyPage } from "./features/library/LegacyPage";
import { useUIStore } from "./store";
import { currentKey, onAuthChange } from "./data/auth";
import { usingMock } from "./data/client";
import { SignIn } from "./app/SignIn";

// New screens (docs/design/dashboard-ux-spec.md).
const Home = lazy(() => import("./features/home/Home").then((m) => ({ default: m.Home })));
const ChatPage = lazy(() => import("./features/chat/ChatPage").then((m) => ({ default: m.ChatPage })));
const Inbox = lazy(() => import("./features/inbox/Inbox").then((m) => ({ default: m.Inbox })));
const Outputs = lazy(() => import("./features/library/Library").then((m) => ({ default: m.Outputs })));
const Skills = lazy(() => import("./features/library/Skills").then((m) => ({ default: m.Skills })));
const Automations = lazy(() => import("./features/library/Library").then((m) => ({ default: m.Automations })));

// Existing pages, now placed in the new information architecture (§3).
const Overview = lazy(() => import("./pages/Overview").then((m) => ({ default: m.Overview })));
const RoutesPage = lazy(() => import("./pages/Routes").then((m) => ({ default: m.RoutesPage })));
const RouteBuilder = lazy(() => import("./pages/RouteBuilder").then((m) => ({ default: m.RouteBuilder })));
const Namespaces = lazy(() => import("./pages/Namespaces").then((m) => ({ default: m.Namespaces })));
const Metrics = lazy(() => import("./pages/Metrics").then((m) => ({ default: m.Metrics })));
const Nodes = lazy(() => import("./pages/Nodes").then((m) => ({ default: m.Nodes })));
const Plugins = lazy(() => import("./pages/Plugins").then((m) => ({ default: m.Plugins })));
const Healing = lazy(() => import("./pages/Healing").then((m) => ({ default: m.Healing })));
const Traffic = lazy(() => import("./pages/Traffic").then((m) => ({ default: m.Traffic })));
const Agents = lazy(() => import("./pages/Agents").then((m) => ({ default: m.Agents })));
const Security = lazy(() => import("./pages/Security").then((m) => ({ default: m.Security })));

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: true } } });

// Old Settings links open the Settings modal over Home.
function OpenSettings({ section }: { section: "general" | "security" }) {
  const open = useUIStore((s) => s.openSettings);
  useEffect(() => open(section), [open, section]);
  return <Navigate to="/" replace />;
}

// Keeps bookmarks to the old flat URLs working.
function Legacy({ to }: { to: string }) {
  const { search } = useLocation();
  return <Navigate to={to + search} replace />;
}

const legacy = (title: string, body: string, el: React.ReactNode) => (
  <LegacyPage title={title} body={body}>
    {el}
  </LegacyPage>
);

// AuthGate shows the sign-in screen until an API key is set (not in the
// mock), and drops cached data when the person signs in or out.
const signedIn = () => usingMock || !!currentKey();
function AuthGate({ children }: { children: React.ReactNode }) {
  const ok = useSyncExternalStore(onAuthChange, signedIn, signedIn);
  useEffect(() => onAuthChange(() => queryClient.clear()), []);
  return ok ? <>{children}</> : <SignIn />;
}

// Served under /dashboard/ by the Gubern management API (embedded dist).
export default function App() {
  return (
    <AuthGate>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter basename="/dashboard">
        <Routes>
          <Route element={<Shell />}>
            <Route index element={<Home />} />
            <Route path="chat/:id" element={<ChatPage />} />
            <Route path="inbox" element={<Inbox />} />
            <Route path="outputs" element={<Outputs />} />
            <Route path="automations" element={<Automations />} />
            <Route path="skills" element={<Skills />} />
            <Route path="workspaces" element={legacy("Workspaces", "Namespaces and the folders agents may work in.", <Namespaces />)} />

            <Route path="operate">
              <Route index element={legacy("Overview", "Live state of this Gubern server.", <Overview />)} />
              <Route path="routes" element={legacy("Routes", "Traffic Gubern serves, with health and backends.", <RoutesPage />)} />
              <Route path="routes/new" element={legacy("New route", "Build a route and review it before applying.", <RouteBuilder />)} />
              <Route path="traffic" element={legacy("Traffic", "Requests, errors and latency across routes.", <Traffic />)} />
              <Route path="metrics" element={legacy("Metrics", "Raw Prometheus metrics.", <Metrics />)} />
              <Route path="nodes" element={legacy("Nodes", "Machines in this cluster.", <Nodes />)} />
              <Route path="healing" element={legacy("Healing", "Automatic recovery checks and actions.", <Healing />)} />
              <Route path="agents" element={legacy("Agents", "Specialist agents, their capabilities and recent runs.", <Agents />)} />
              <Route path="security" element={legacy("Security", "Edge policy, TLS and identity.", <Security />)} />
            </Route>
            <Route path="build/plugins" element={legacy("Plugins", "WASM plugins attached to routes.", <Plugins />)} />
            <Route
              path="build/forge"
              element={legacy("Forge", "Builds and deploys.", <p className="text-sm text-muted">Forge builds move here in Phase 3.</p>)}
            />

            {/* Old URLs */}
            <Route path="routes" element={<Legacy to="/operate/routes" />} />
            <Route path="routes/new" element={<Legacy to="/operate/routes/new" />} />
            <Route path="nodes" element={<Legacy to="/operate/nodes" />} />
            <Route path="traffic" element={<Legacy to="/operate/traffic" />} />
            <Route path="metrics" element={<Legacy to="/operate/metrics" />} />
            <Route path="healing" element={<Legacy to="/operate/healing" />} />
            <Route path="agents" element={<Legacy to="/operate/agents" />} />
            <Route path="security" element={<Legacy to="/operate/security" />} />
            <Route path="namespaces" element={<Legacy to="/workspaces" />} />
            <Route path="plugins" element={<Legacy to="/build/plugins" />} />
            <Route path="settings" element={<OpenSettings section="general" />} />
            <Route path="secrets" element={<OpenSettings section="security" />} />
            <Route path="audit" element={<OpenSettings section="security" />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
    </AuthGate>
  );
}
